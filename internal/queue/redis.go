package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/priority"
	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix      = "queue:"
	correlationTTL = 5 * time.Minute
)

// ErrWaitTimeout is returned when Wait's BRPOP expires with no correlated response.
var ErrWaitTimeout = errors.New("inference wait timeout")

type RedisQueue struct {
	client       *redis.Client
	inputQueue   string
	outputQueue  string
	brpopTimeout time.Duration
	scheduler    *priority.Scheduler
}

func NewRedisQueue(
	client *redis.Client,
	inputQueue, outputQueue string,
	brpopTimeoutSeconds int,
	priorityHighCount, priorityMediumCount int,
) *RedisQueue {
	return &RedisQueue{
		client:       client,
		inputQueue:   inputQueue,
		outputQueue:  outputQueue,
		brpopTimeout: time.Duration(brpopTimeoutSeconds) * time.Second,
		scheduler:    priority.NewScheduler(priorityHighCount, priorityMediumCount),
	}
}

func (q *RedisQueue) Consume(ctx context.Context) (*cloudevent.Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if err := q.drainInput(ctx); err != nil {
			return nil, err
		}

		level, err := q.pickLevel(ctx)
		if err != nil {
			return nil, err
		}
		if level != priority.None {
			event, err := q.popLane(ctx, level)
			if err != nil {
				return nil, err
			}
			if event == nil {
				continue
			}
			q.scheduler.Record(level)
			return event, nil
		}

		result, err := q.client.BRPop(ctx, q.brpopTimeout, q.key(q.inputQueue)).Result()
		if err == redis.Nil {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("consume from %s: %w", q.key(q.inputQueue), err)
		}
		if len(result) < 2 {
			return nil, nil
		}
		if err := q.routePayload(ctx, result[1]); err != nil {
			return nil, err
		}
	}
}

func (q *RedisQueue) Enqueue(ctx context.Context, event *cloudevent.Event) error {
	payload, err := event.ToJSON()
	if err != nil {
		return err
	}
	key := q.key(q.inputQueue)
	if err := q.client.LPush(ctx, key, payload).Err(); err != nil {
		return fmt.Errorf("enqueue to %s: %w", key, err)
	}
	return nil
}

func (q *RedisQueue) Wait(ctx context.Context, requestID string, timeout time.Duration) (*cloudevent.Event, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("request id must not be blank")
	}
	if timeout <= 0 {
		timeout = q.brpopTimeout
	}

	key := q.correlationKey(requestID)
	result, err := q.client.BRPop(ctx, timeout, key).Result()
	if err == redis.Nil {
		return nil, ErrWaitTimeout
	}
	if err != nil {
		return nil, fmt.Errorf("wait on %s: %w", key, err)
	}
	if len(result) < 2 {
		return nil, ErrWaitTimeout
	}
	event, err := cloudevent.FromJSON(result[1])
	if err != nil {
		return nil, fmt.Errorf("decode correlated response: %w", err)
	}
	return event, nil
}

func (q *RedisQueue) Publish(ctx context.Context, event *cloudevent.Event) error {
	payload, err := event.ToJSON()
	if err != nil {
		return err
	}

	key := q.key(q.outputQueue)
	if err := q.client.LPush(ctx, key, payload).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", key, err)
	}

	if event != nil && event.Subject != nil {
		subject := strings.TrimSpace(*event.Subject)
		if subject != "" {
			corr := q.correlationKey(subject)
			if err := q.client.LPush(ctx, corr, payload).Err(); err != nil {
				return fmt.Errorf("publish to %s: %w", corr, err)
			}
			if err := q.client.Expire(ctx, corr, correlationTTL).Err(); err != nil {
				return fmt.Errorf("expire %s: %w", corr, err)
			}
		}
	}
	return nil
}

func (q *RedisQueue) drainInput(ctx context.Context) error {
	inputKey := q.key(q.inputQueue)
	for {
		payload, err := q.client.RPop(ctx, inputKey).Result()
		if err == redis.Nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("drain from %s: %w", inputKey, err)
		}
		if err := q.routePayload(ctx, payload); err != nil {
			return err
		}
	}
}

func (q *RedisQueue) routePayload(ctx context.Context, payload string) error {
	event, err := cloudevent.FromJSON(payload)
	if err != nil {
		// Payload already removed from Redis; skip instead of failing Consume.
		slog.Warn("skipping malformed queue payload", "error", err)
		return nil
	}

	level := priority.Low
	if event.Data != nil {
		level = priority.Parse(stringValue(event.Data["priority"]))
	}

	laneKey := q.laneKey(level)
	if err := q.client.LPush(ctx, laneKey, payload).Err(); err != nil {
		return fmt.Errorf("enqueue to %s: %w", laneKey, err)
	}
	return nil
}

func (q *RedisQueue) pickLevel(ctx context.Context) (priority.Level, error) {
	avail, err := q.availability(ctx)
	if err != nil {
		return priority.None, err
	}
	return q.scheduler.Pick(avail), nil
}

func (q *RedisQueue) availability(ctx context.Context) (priority.Availability, error) {
	levels := []priority.Level{priority.Critical, priority.High, priority.Medium, priority.Low}
	avail := priority.Availability{}
	for _, level := range levels {
		n, err := q.client.LLen(ctx, q.laneKey(level)).Result()
		if err != nil {
			return priority.Availability{}, fmt.Errorf("llen %s: %w", q.laneKey(level), err)
		}
		has := n > 0
		switch level {
		case priority.Critical:
			avail.Critical = has
		case priority.High:
			avail.High = has
		case priority.Medium:
			avail.Medium = has
		case priority.Low:
			avail.Low = has
		}
	}
	return avail, nil
}

func (q *RedisQueue) popLane(ctx context.Context, level priority.Level) (*cloudevent.Event, error) {
	laneKey := q.laneKey(level)
	payload, err := q.client.RPop(ctx, laneKey).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pop from %s: %w", laneKey, err)
	}
	event, err := cloudevent.FromJSON(payload)
	if err != nil {
		// Payload already removed from the lane; skip instead of failing Consume.
		slog.Warn("skipping malformed lane payload", "lane", laneKey, "error", err)
		return nil, nil
	}
	return event, nil
}

func (q *RedisQueue) correlationKey(requestID string) string {
	return q.key(q.outputQueue + ":" + requestID)
}

func (q *RedisQueue) laneKey(level priority.Level) string {
	return q.key(q.inputQueue + ":" + level.LaneSuffix())
}

func (q *RedisQueue) key(queueName string) string {
	queueName = strings.TrimSpace(queueName)
	return keyPrefix + queueName
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
	default:
		return ""
	}
}
