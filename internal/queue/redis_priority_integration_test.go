//go:build integration

package queue_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
	"github.com/redis/go-redis/v9"
)

func TestPriorityConsumeAgainstLiveRedis(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "redis:6379"
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping(%s) error = %v (is Redis up on dev?)", addr, err)
	}

	inputQueue := fmt.Sprintf("ai.requests.priority-test-%d", os.Getpid())
	outputQueue := fmt.Sprintf("ai.responses.priority-test-%d", os.Getpid())
	keys := []string{
		"queue:" + inputQueue,
		"queue:" + inputQueue + ":critical",
		"queue:" + inputQueue + ":high",
		"queue:" + inputQueue + ":medium",
		"queue:" + inputQueue + ":low",
		"queue:" + outputQueue,
	}
	t.Cleanup(func() {
		_ = client.Del(context.Background(), keys...).Err()
	})
	if err := client.Del(ctx, keys...).Err(); err != nil {
		t.Fatalf("Del() error = %v", err)
	}

	q := queue.NewRedisQueue(client, inputQueue, outputQueue, 2, 3, 3)
	push := func(id, prio string) {
		t.Helper()
		payload := fmt.Sprintf(
			`{"type":"t","source":"/integration","id":%q,"data":{"priority":%q}}`,
			id, prio,
		)
		event, err := cloudevent.FromJSON(payload)
		if err != nil {
			t.Fatalf("FromJSON(%s) error = %v", id, err)
		}
		if err := q.Enqueue(ctx, event); err != nil {
			t.Fatalf("Enqueue(%s) error = %v", id, err)
		}
	}

	push("low-1", "LOW")
	push("high-1", "HIGH")
	push("crit-1", "CRITICAL")

	first, err := q.Consume(ctx)
	if err != nil {
		t.Fatalf("Consume() critical error = %v", err)
	}
	if first == nil || first.ID != "crit-1" {
		t.Fatalf("first = %#v, want crit-1", first)
	}

	// Drain leftover messages, then use a fresh scheduler for the fairness sequence.
	for {
		event, err := q.Consume(ctx)
		if err != nil {
			t.Fatalf("drain error = %v", err)
		}
		if event == nil {
			break
		}
	}

	for i := 1; i <= 9; i++ {
		push(fmt.Sprintf("h-%d", i), "HIGH")
	}
	push("m-1", "MEDIUM")
	push("m-2", "MEDIUM")
	push("m-3", "MEDIUM")
	push("l-1", "LOW")
	push("l-2", "LOW")

	fair := queue.NewRedisQueue(client, inputQueue, outputQueue, 2, 3, 3)
	want := []string{
		"h-1", "h-2", "h-3", "m-1",
		"h-4", "h-5", "h-6", "m-2",
		"h-7", "h-8", "h-9", "l-1",
		"m-3", "l-2",
	}
	for i, id := range want {
		event, err := fair.Consume(ctx)
		if err != nil {
			t.Fatalf("Consume() step %d error = %v", i, err)
		}
		if event == nil {
			t.Fatalf("Consume() step %d returned nil", i)
		}
		if event.ID != id {
			t.Fatalf("step %d: ID = %q, want %q", i, event.ID, id)
		}
	}
}
