package native

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestChannelManagerUsesWorkIDKey(t *testing.T) {
	t.Parallel()

	manager := NewChannelManager()

	first, err := manager.Channel("work-123")
	if err != nil {
		t.Fatalf("Channel(work-123) returned error: %v", err)
	}
	second, err := manager.Channel("work-123")
	if err != nil {
		t.Fatalf("Channel(work-123) second call returned error: %v", err)
	}
	other, err := manager.Channel("work-456")
	if err != nil {
		t.Fatalf("Channel(work-456) returned error: %v", err)
	}

	if first != second {
		t.Fatal("expected same channel pointer for repeated work_id")
	}
	if first == other {
		t.Fatal("expected different channel pointers for different work_id values")
	}
}

func TestAgentChannelPostAndReadPreservesOrder(t *testing.T) {
	t.Parallel()

	ch := NewAgentChannel()
	want := []ChannelMessage{
		{From: "worker-1", Role: "worker", Content: "first"},
		{From: "checker-1", Role: "checker", Content: "second"},
		{From: "worker-1", Role: "worker", Content: "third"},
	}
	for _, msg := range want {
		if _, err := ch.Post(msg); err != nil {
			t.Fatalf("Post returned error: %v", err)
		}
	}

	got, cursor, err := ch.ReadSince(0)
	if err != nil {
		t.Fatalf("ReadSince returned error: %v", err)
	}
	if cursor != uint64(len(want)) {
		t.Fatalf("cursor = %d, want %d", cursor, len(want))
	}
	if len(got) != len(want) {
		t.Fatalf("message count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].From != want[i].From || got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("message[%d] = %+v, want %+v", i, got[i], want[i])
		}
		if got[i].Timestamp.IsZero() {
			t.Fatalf("message[%d] missing timestamp", i)
		}
	}
}

func TestAgentChannelCursorReadsOnlyNewMessages(t *testing.T) {
	t.Parallel()

	ch := NewAgentChannel()
	for _, content := range []string{"alpha", "beta"} {
		if _, err := ch.Post(ChannelMessage{From: "worker", Role: "worker", Content: content}); err != nil {
			t.Fatalf("Post returned error: %v", err)
		}
	}

	firstBatch, cursor, err := ch.ReadSince(0)
	if err != nil {
		t.Fatalf("ReadSince returned error: %v", err)
	}
	if len(firstBatch) != 2 || cursor != 2 {
		t.Fatalf("first read = %d messages, cursor %d; want 2 messages, cursor 2", len(firstBatch), cursor)
	}

	if _, err := ch.Post(ChannelMessage{From: "checker", Role: "checker", Content: "gamma"}); err != nil {
		t.Fatalf("Post returned error: %v", err)
	}

	secondBatch, nextCursor, err := ch.ReadSince(cursor)
	if err != nil {
		t.Fatalf("ReadSince(cursor) returned error: %v", err)
	}
	if len(secondBatch) != 1 || secondBatch[0].Content != "gamma" {
		t.Fatalf("unexpected second batch: %+v", secondBatch)
	}
	if nextCursor != 3 {
		t.Fatalf("next cursor = %d, want 3", nextCursor)
	}

	emptyBatch, finalCursor, err := ch.ReadSince(nextCursor)
	if err != nil {
		t.Fatalf("ReadSince(final cursor) returned error: %v", err)
	}
	if len(emptyBatch) != 0 {
		t.Fatalf("expected no new messages, got %+v", emptyBatch)
	}
	if finalCursor != nextCursor {
		t.Fatalf("final cursor = %d, want %d", finalCursor, nextCursor)
	}
}

func TestAgentChannelWaitTimeoutAndDelivery(t *testing.T) {
	t.Parallel()

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		ch := NewAgentChannel()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		start := time.Now()
		got, cursor, err := ch.Wait(ctx, 0)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait error = %v, want deadline exceeded", err)
		}
		if len(got) != 0 || cursor != 0 {
			t.Fatalf("Wait timeout returned messages=%v cursor=%d, want none and cursor 0", got, cursor)
		}
		if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
			t.Fatalf("Wait returned too quickly: %s", elapsed)
		}
	})

	t.Run("delivery", func(t *testing.T) {
		t.Parallel()

		ch := NewAgentChannel()
		go func() {
			time.Sleep(20 * time.Millisecond)
			_, _ = ch.Post(ChannelMessage{From: "checker", Role: "checker", Content: "done"})
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		got, cursor, err := ch.Wait(ctx, 0)
		if err != nil {
			t.Fatalf("Wait returned error: %v", err)
		}
		if len(got) != 1 || got[0].Content != "done" {
			t.Fatalf("Wait returned %+v, want delivered message", got)
		}
		if cursor != 1 {
			t.Fatalf("cursor = %d, want 1", cursor)
		}
	})
}

func TestAgentChannelConcurrentPosts(t *testing.T) {
	t.Parallel()

	ch := NewAgentChannel()
	const total = 32

	var wg sync.WaitGroup
	for i := range total {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := ch.Post(ChannelMessage{
				From:    fmt.Sprintf("agent-%d", idx%4),
				Role:    "worker",
				Content: fmt.Sprintf("message-%d", idx),
			})
			if err != nil {
				t.Errorf("Post(%d) returned error: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	got, cursor, err := ch.ReadSince(0)
	if err != nil {
		t.Fatalf("ReadSince returned error: %v", err)
	}
	if len(got) != total {
		t.Fatalf("message count = %d, want %d", len(got), total)
	}
	if cursor != total {
		t.Fatalf("cursor = %d, want %d", cursor, total)
	}

	seen := make(map[string]struct{}, total)
	for _, msg := range got {
		seen[msg.Content] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("unique message count = %d, want %d", len(seen), total)
	}
}

func TestAgentChannelCloseUnblocksWaiters(t *testing.T) {
	t.Parallel()

	ch := NewAgentChannel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := ch.Wait(ctx, 0)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	ch.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrChannelClosed) {
			t.Fatalf("Wait error = %v, want ErrChannelClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for waiter to unblock")
	}
}

func TestAgentChannelPostAfterCloseFails(t *testing.T) {
	t.Parallel()

	ch := NewAgentChannel()
	ch.Close()

	if _, err := ch.Post(ChannelMessage{From: "worker", Role: "worker", Content: "late"}); !errors.Is(err, ErrChannelClosed) {
		t.Fatalf("Post after Close error = %v, want ErrChannelClosed", err)
	}
}
