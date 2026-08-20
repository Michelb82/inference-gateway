package queue_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mywebsite/construction-ai-gateway/internal/cloudevent"
	"github.com/mywebsite/construction-ai-gateway/internal/queue"
	"github.com/redis/go-redis/v9"
)

func TestPublishUsesQueuePrefix(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	event := sampleEvent(t)

	if err := q.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	values, err := mr.List("queue:ai.responses")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(values) != 1 {
		t.Fatalf("len(values) = %d, want 1", len(values))
	}

	corr, err := mr.List("queue:ai.responses:abc-123")
	if err != nil {
		t.Fatalf("correlation List() error = %v", err)
	}
	if len(corr) != 1 {
		t.Fatalf("len(correlation) = %d, want 1", len(corr))
	}
	if ttl := mr.TTL("queue:ai.responses:abc-123"); ttl <= 0 {
		t.Fatalf("correlation TTL = %v, want > 0", ttl)
	}
}

func TestPublishNilSubjectSkipsCorrelation(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	event := sampleEvent(t)
	event.Subject = nil
	event.ID = "no-subject"

	if err := q.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if _, err := mr.List("queue:ai.responses:no-subject"); err == nil {
		t.Fatal("expected missing correlation key when Subject is nil")
	}
}

func TestEnqueueThenConsume(t *testing.T) {
	_, client := newTestRedis(t)
	q := newQueue(client)

	event := sampleEvent(t)
	event.ID = "enqueued-1"
	event.Data = map[string]any{"priority": "HIGH"}
	if err := q.Enqueue(context.Background(), event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	got, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if got == nil || got.ID != "enqueued-1" {
		t.Fatalf("got = %#v, want enqueued-1", got)
	}
}

func TestWaitReturnsCorrelatedResponse(t *testing.T) {
	_, client := newTestRedis(t)
	q := newQueue(client)
	ctx := context.Background()

	request := sampleEvent(t)
	request.ID = "req-wait-1"
	response := cloudevent.NewResponse(request, cloudevent.EventTypeRequestCompleted, map[string]any{
		"result": map[string]any{"ok": true},
	})
	if err := q.Publish(ctx, response); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	got, err := q.Wait(ctx, request.ID, time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got == nil || got.Subject == nil || *got.Subject != request.ID {
		t.Fatalf("got = %#v, want subject %q", got, request.ID)
	}
}

func TestWaitTimeout(t *testing.T) {
	_, client := newTestRedis(t)
	q := newQueue(client)

	_, err := q.Wait(context.Background(), "missing-id", time.Second)
	if !errors.Is(err, queue.ErrWaitTimeout) {
		t.Fatalf("Wait() error = %v, want ErrWaitTimeout", err)
	}
}

func TestWaitDoesNotPopSharedOutput(t *testing.T) {
	mr, client := newTestRedis(t)
	q := newQueue(client)
	ctx := context.Background()

	other := sampleEvent(t)
	other.ID = "other"
	subject := "other"
	other.Subject = &subject
	if err := q.Publish(ctx, other); err != nil {
		t.Fatalf("Publish() other error = %v", err)
	}

	_, err := q.Wait(ctx, "wanted-id", time.Second)
	if !errors.Is(err, queue.ErrWaitTimeout) {
		t.Fatalf("Wait() error = %v, want ErrWaitTimeout", err)
	}

	shared, err := mr.List("queue:ai.responses")
	if err != nil || len(shared) != 1 {
		t.Fatalf("shared list = %v err = %v, want 1 leftover", shared, err)
	}
}

func TestWaitContextCancel(t *testing.T) {
	_, client := newTestRedis(t)
	q := newQueue(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := q.Wait(ctx, "any", 5*time.Second)
	if err == nil {
		t.Fatal("Wait() expected context error")
	}
}

func TestConsumeReturnsNilOnTimeout(t *testing.T) {
	_, client := newTestRedis(t)

	q := newQueue(client)
	event, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if event != nil {
		t.Fatalf("event = %#v, want nil", event)
	}
}

func TestConsumeSamePriorityPreservesOrder(t *testing.T) {
	_, client := newTestRedis(t)

	q := newQueue(client)
	pushRequest(t, q, "1", "LOW")
	pushRequest(t, q, "2", "LOW")

	first, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() first error = %v", err)
	}
	second, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() second error = %v", err)
	}

	if first.ID != "1" {
		t.Fatalf("first.ID = %q, want 1", first.ID)
	}
	if second.ID != "2" {
		t.Fatalf("second.ID = %q, want 2", second.ID)
	}
}

func TestConsumeCriticalJumpsQueue(t *testing.T) {
	_, client := newTestRedis(t)

	q := newQueue(client)
	pushRequest(t, q, "low-1", "LOW")
	pushRequest(t, q, "high-1", "HIGH")
	pushRequest(t, q, "crit-1", "CRITICAL")
	first, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if first.ID != "crit-1" {
		t.Fatalf("first.ID = %q, want crit-1", first.ID)
	}
}

func TestConsumeMissingPriorityDefaultsToLow(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	mr.Lpush("queue:ai.requests", `{"type":"t","source":"/s","id":"no-prio","data":{}}`)
	pushRequest(t, q, "high-1", "HIGH")
	first, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if first.ID != "high-1" {
		t.Fatalf("first.ID = %q, want high-1", first.ID)
	}
}

func TestConsumeSkipsMalformedInputPayload(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	mr.Lpush("queue:ai.requests", `not-json`)
	pushRequest(t, q, "ok-1", "LOW")
	event, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if event == nil {
		t.Fatal("Consume() returned nil, want ok-1")
	}
	if event.ID != "ok-1" {
		t.Fatalf("event.ID = %q, want ok-1", event.ID)
	}
}

func TestConsumeSkipsMalformedLanePayload(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	// Pre-seed a poisoned lane entry (as if a prior route left bad data).
	mr.Lpush("queue:ai.requests:low", `{bad`)
	pushRequest(t, q, "ok-2", "LOW")
	event, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if event == nil {
		t.Fatal("Consume() returned nil, want ok-2")
	}
	if event.ID != "ok-2" {
		t.Fatalf("event.ID = %q, want ok-2", event.ID)
	}
}

func TestConsumeSkipsStructurallyInvalidInputPayload(t *testing.T) {
	mr, client := newTestRedis(t)
	defer mr.Close()

	q := newQueue(client)
	// Valid JSON object but missing required CloudEvent fields.
	mr.Lpush("queue:ai.requests", `{}`)
	pushRequest(t, q, "ok-3", "HIGH")
	event, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if event == nil {
		t.Fatal("Consume() returned nil, want ok-3")
	}
	if event.ID != "ok-3" {
		t.Fatalf("event.ID = %q, want ok-3", event.ID)
	}
}

func TestConsumeFairnessHighMediumLow(t *testing.T) {
	_, client := newTestRedis(t)

	q := newQueue(client)
	for i := 1; i <= 9; i++ {
		pushRequest(t, q, fmt.Sprintf("h-%d", i), "HIGH")
	}
	pushRequest(t, q, "m-1", "MEDIUM")
	pushRequest(t, q, "m-2", "MEDIUM")
	pushRequest(t, q, "m-3", "MEDIUM")
	pushRequest(t, q, "l-1", "LOW")
	pushRequest(t, q, "l-2", "LOW")
	want := []string{
		"h-1", "h-2", "h-3", "m-1",
		"h-4", "h-5", "h-6", "m-2",
		"h-7", "h-8", "h-9", "l-1",
		"m-3", "l-2",
	}

	for i, id := range want {
		event, err := q.Consume(context.Background())
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

func newQueue(client *redis.Client) *queue.RedisQueue {
	return queue.NewRedisQueue(client, "ai.requests", "ai.responses", 1, 3, 3)
}

func pushRequest(t *testing.T, q *queue.RedisQueue, id, prio string) {
	t.Helper()
	event, err := cloudevent.FromJSON(fmt.Sprintf(
		`{"type":"t","source":"/s","id":%q,"data":{"priority":%q}}`,
		id, prio,
	))
	if err != nil {
		t.Fatalf("FromJSON() error = %v", err)
	}
	if err := q.Enqueue(context.Background(), event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	return mr, client
}

func sampleEvent(t *testing.T) *cloudevent.Event {
	t.Helper()
	event, err := cloudevent.FromJSON(`{
		"type":"com.mywebsite.ai.request",
		"source":"/ai-gateway",
		"id":"abc-123",
		"data":{"capability":"routing","input":{"message":"hello"}}
	}`)
	if err != nil {
		t.Fatalf("FromJSON() error = %v", err)
	}
	subject := event.ID
	event.Subject = &subject
	return event
}
