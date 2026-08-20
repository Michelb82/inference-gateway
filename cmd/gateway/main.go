package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/config"
	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
	"github.com/mywebsite/construction-ai-gateway/internal/health"
	"github.com/mywebsite/construction-ai-gateway/internal/ollama"
	"github.com/mywebsite/construction-ai-gateway/internal/openai"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
	"github.com/mywebsite/construction-ai-gateway/internal/worker"
	"github.com/redis/go-redis/v9"
)

const (
	debugLogPath    = "debug.log"
	defaultHTTPAddr = ":80"
)

func main() {
	bootstrap := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	manifestPath := flag.String("manifest", "", "optional local manifest path (dev/test/experimental)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		bootstrap.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger, debugFile, err := newLogger(cfg.Debug)
	if err != nil {
		bootstrap.Error("failed to set up logger", "error", err)
		os.Exit(1)
	}
	if debugFile != nil {
		defer debugFile.Close()
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registryHolder := capability.NewHolder()
	overridePolicy := capability.NewOverridePolicyHolder()
	llmPool := ollama.NewPool()
	plane := &dataPlane{
		parent:         ctx,
		registry:       registryHolder,
		overridePolicy: overridePolicy,
		llmPool:        llmPool,
		logger:         logger,
	}

	healthHandler := health.NewHandler(registryHolder, llmPool)
	openaiHandler := openai.NewHandler(plane, logger)
	mux := http.NewServeMux()
	healthHandler.Register(mux)
	openaiHandler.Register(mux)
	httpServer := health.NewHTTPServer(defaultHTTPAddr, mux)

	go func() {
		logger.Info("health server listening", "addr", defaultHTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("health server stopped with error", "error", err)
			stop()
		}
	}()

	mgr := configmgmt.NewManager(cfg.ManifestURL, cfg.ManifestPollingInterval, logger, plane.Apply)
	if err := mgr.Bootstrap(*manifestPath); err != nil {
		logger.Error("failed to bootstrap manifest", "error", err)
		os.Exit(1)
	}

	go mgr.Run(ctx)

	logger.Info("ai gateway started",
		"manifest_path", *manifestPath,
		"manifest_url", cfg.ManifestURL,
		"manifest_polling_interval", cfg.ManifestPollingInterval.String(),
		"configured", mgr.HasSnapshot(),
		"debug", cfg.Debug,
	)

	<-ctx.Done()
	plane.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("health server shutdown failed", "error", err)
	}

	logger.Info("ai gateway stopped")
}

type dataPlane struct {
	parent         context.Context
	registry       *capability.Holder
	overridePolicy *capability.OverridePolicyHolder
	llmPool        *ollama.Pool
	logger         *slog.Logger

	mu       sync.Mutex
	snap     *configmgmt.Snapshot
	redis    *redis.Client
	queue    *queue.RedisQueue
	cancel   context.CancelFunc
	done     chan struct{}
	httpAddr string
}

func (d *dataPlane) Apply(snap configmgmt.Snapshot, first bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	bindings := make(map[string]capability.ModelBinding, len(snap.Bindings))
	for name, binding := range snap.Bindings {
		bindings[name] = capability.ModelBinding{
			BaseURL:       binding.BaseURL,
			Model:         binding.Model,
			KeepAlive:     binding.KeepAlive,
			MaxInputChars: binding.MaxInputChars,
		}
	}
	reg, err := capability.NewRegistryFromBindings(bindings)
	if err != nil {
		return err
	}

	needRedisRestart := d.redis == nil ||
		d.snap == nil ||
		d.snap.RedisAddr != snap.RedisAddr ||
		d.snap.InputQueue != snap.InputQueue ||
		d.snap.OutputQueue != snap.OutputQueue ||
		d.snap.BRPopTimeout != snap.BRPopTimeout ||
		d.snap.PriorityHighCount != snap.PriorityHighCount ||
		d.snap.PriorityMediumCount != snap.PriorityMediumCount

	// Prepare: open a new Redis client locally; do not mutate live state yet.
	var newRedis *redis.Client
	if needRedisRestart {
		client := redis.NewClient(&redis.Options{Addr: snap.RedisAddr})
		pingCtx, cancel := context.WithTimeout(d.parent, 5*time.Second)
		err := client.Ping(pingCtx).Err()
		cancel()
		if err != nil {
			_ = client.Close()
			return fmt.Errorf("connect redis at %s: %w", snap.RedisAddr, err)
		}
		newRedis = client
	}

	// Verify models before any live swap so a failure leaves the data plane untouched.
	targets := make([]ollama.ModelTarget, 0, len(reg.All()))
	for _, def := range reg.All() {
		targets = append(targets, ollama.ModelTarget{BaseURL: def.BaseURL, Name: def.Model})
	}
	d.logger.Info("ensuring llm models", "targets", targets)
	availableModels, unavailableModels, err := d.llmPool.EnsureModels(d.parent, targets)
	if err != nil {
		if newRedis != nil {
			_ = newRedis.Close()
		}
		d.logger.Error("llm model readiness failed", "error", err, "unavailable", unavailableModels)
		return fmt.Errorf("llm model readiness failed: %w", err)
	}
	if len(unavailableModels) > 0 {
		d.logger.Warn("some llm models unavailable; continuing with remaining models",
			"available", availableModels,
			"unavailable", unavailableModels,
		)
	} else {
		d.logger.Info("llm models ready", "available", availableModels)
	}

	// Abort before live mutation if the process is shutting down.
	if err := d.parent.Err(); err != nil {
		if newRedis != nil {
			_ = newRedis.Close()
		}
		return fmt.Errorf("apply aborted: %w", err)
	}

	if snap.HTTPAddr != defaultHTTPAddr && snap.HTTPAddr != d.httpAddr {
		d.logger.Warn("manifest config.http_address differs from the dormant health listener; restart required to rebind",
			"configured", snap.HTTPAddr,
			"listening", defaultHTTPAddr,
		)
	}
	d.httpAddr = snap.HTTPAddr

	// Stop the old worker before swapping registry/types/Redis so it never
	// observes a half-applied configuration.
	if needRedisRestart {
		d.stopWorkerLocked()
		if d.redis != nil {
			_ = d.redis.Close()
			d.redis = nil
		}
		d.queue = nil
	}

	cloudevent.ConfigureTypes(snap.CloudEventTypePrefix)
	d.registry.Store(reg)
	d.overridePolicy.Store(capability.PolicyFromOrgs(snap.SystemPromptOverrideOrgs, snap.MaxSystemPromptChars))

	if needRedisRestart {
		d.redis = newRedis
		eventQueue := queue.NewRedisQueue(
			d.redis,
			snap.InputQueue,
			snap.OutputQueue,
			snap.BRPopTimeout,
			snap.PriorityHighCount,
			snap.PriorityMediumCount,
		)
		d.queue = eventQueue

		workerCtx, cancel := context.WithCancel(d.parent)
		d.cancel = cancel
		d.done = make(chan struct{})
		appWorker := worker.New(eventQueue, eventQueue, d.llmPool, d.llmPool, d.registry, d.overridePolicy, d.logger)
		go func() {
			defer close(d.done)
			worker.Supervise(workerCtx, appWorker.Run, d.logger, time.Second)
		}()
		d.logger.Info("data plane activated",
			"redis_addr", snap.RedisAddr,
			"input_queue", snap.InputQueue,
			"output_queue", snap.OutputQueue,
			"cloudevent_type_prefix", snap.CloudEventTypePrefix,
			"first", first,
		)
	} else {
		d.logger.Info("data plane configuration updated", "fingerprint", snap.Fingerprint, "first", first)
	}

	copySnap := snap
	d.snap = &copySnap
	return nil
}

func (d *dataPlane) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopWorkerLocked()
	d.queue = nil
	if d.redis != nil {
		_ = d.redis.Close()
		d.redis = nil
	}
}

func (d *dataPlane) InferenceQueue() openai.Queue {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.queue == nil {
		return nil
	}
	return d.queue
}

func (d *dataPlane) stopWorkerLocked() {
	if d.cancel != nil {
		d.cancel()
		if d.done != nil {
			<-d.done
		}
		d.cancel = nil
		d.done = nil
	}
}

func newLogger(debug bool) (*slog.Logger, *os.File, error) {
	writer := io.Writer(os.Stdout)
	var debugFile *os.File

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
		file, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		debugFile = file
		writer = io.MultiWriter(os.Stdout, debugFile)
	}

	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
	})), debugFile, nil
}
