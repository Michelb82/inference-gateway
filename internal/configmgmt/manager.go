package configmgmt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// InstanceIdentity identifies a process lifetime, not an authenticated identity.
type InstanceIdentity struct {
	ID       string
	Hostname string
}

// NewInstanceIdentity creates an identifier once for a gateway process.
func NewInstanceIdentity() (InstanceIdentity, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return InstanceIdentity{}, fmt.Errorf("generate gateway instance ID: %w", err)
	}
	hostname, _ := os.Hostname()
	return InstanceIdentity{ID: hex.EncodeToString(id[:]), Hostname: hostname}, nil
}

// ApplyFunc is invoked when a candidate snapshot should be activated.
// first is true when no snapshot is currently applied.
// Returning an error keeps the previous snapshot (or remains dormant).
type ApplyFunc func(snapshot Snapshot, first bool) error

// Manager owns the current manifest snapshot and polls AI Manager for updates.
type Manager struct {
	url      string
	interval time.Duration
	client   *http.Client
	logger   *slog.Logger
	onApply  ApplyFunc
	identity InstanceIdentity

	mu      sync.RWMutex
	current *Snapshot
}

func NewManager(identity InstanceIdentity, url string, interval time.Duration, logger *slog.Logger, onApply ApplyFunc) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if interval < time.Second {
		interval = time.Second
	}
	return &Manager{
		url:      url,
		interval: interval,
		client:   &http.Client{Timeout: defaultFetchTimeout},
		logger:   logger,
		onApply:  onApply,
		identity: identity,
	}
}

// Snapshot returns the current applied snapshot, or nil when dormant.
func (m *Manager) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return nil
	}
	copySnap := *m.current
	return &copySnap
}

// HasSnapshot reports whether a configuration snapshot is currently applied.
func (m *Manager) HasSnapshot() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current != nil
}

// Bootstrap optionally loads a local manifest file. If path is empty, the
// manager stays dormant until a remote poll succeeds.
func (m *Manager) Bootstrap(localPath string) error {
	if strings.TrimSpace(localPath) == "" {
		m.logger.Info("configuration management dormant; awaiting remote manifest", "manifest_url", m.url)
		return nil
	}
	manifest, err := LoadFile(localPath)
	if err != nil {
		return err
	}
	snap, err := Resolve(manifest)
	if err != nil {
		return err
	}
	if err := m.apply(snap); err != nil {
		return err
	}
	m.logger.Info("loaded local manifest", "path", localPath, "fingerprint", snap.Fingerprint)
	return nil
}

// Run polls MANIFEST_URL until ctx is cancelled. Soft-fails keep the current snapshot.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	m.pollOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollOnce(ctx)
		}
	}
}

func (m *Manager) pollOnce(ctx context.Context) {
	if strings.TrimSpace(m.url) == "" {
		m.logger.Warn("manifest poll skipped; MANIFEST_URL is blank")
		return
	}

	manifest, err := fetchURL(ctx, m.client, m.url, m.identity)
	if err != nil {
		if m.HasSnapshot() {
			m.logger.Warn("manifest poll failed; keeping current snapshot", "url", m.url, "error", err)
		} else {
			m.logger.Warn("manifest poll failed; remaining dormant", "url", m.url, "error", err)
		}
		return
	}

	snap, err := Resolve(manifest)
	if err != nil {
		if m.HasSnapshot() {
			m.logger.Warn("manifest resolve failed; keeping current snapshot", "error", err)
		} else {
			m.logger.Warn("manifest resolve failed; remaining dormant", "error", err)
		}
		return
	}

	m.mu.RLock()
	same := m.current != nil && m.current.Fingerprint == snap.Fingerprint
	m.mu.RUnlock()
	if same {
		m.logger.Debug("manifest poll unchanged", "fingerprint", snap.Fingerprint)
		return
	}

	if err := m.apply(snap); err != nil {
		if m.HasSnapshot() {
			m.logger.Warn("manifest apply failed; keeping current snapshot", "error", err)
		} else {
			m.logger.Warn("manifest apply failed; remaining dormant", "error", err)
		}
		return
	}
	m.logger.Info("applied remote manifest", "url", m.url, "fingerprint", snap.Fingerprint)
}

func (m *Manager) apply(snap Snapshot) error {
	m.mu.RLock()
	first := m.current == nil
	m.mu.RUnlock()

	if m.onApply != nil {
		if err := m.onApply(snap, first); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.current = &snap
	m.mu.Unlock()
	return nil
}
