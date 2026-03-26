package native

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yusefmosiah/cogent/internal/adapterapi"
)

func TestNewCoAgentToolsRegistersChannelTools(t *testing.T) {
	t.Parallel()

	tools := NewCoAgentTools(newCoAgentManager(t.TempDir(), "", map[string]adapterapi.LiveAgentAdapter{}))
	want := []string{"spawn_agent", "post_message", "read_messages", "wait_for_message", "close_agent"}
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(tools), len(want))
	}
	for i, name := range want {
		if tools[i].Name != name {
			t.Fatalf("tool[%d] = %q, want %q", i, tools[i].Name, name)
		}
	}
}

func TestCoAgentManagerSpawnPostReadWaitAndClose(t *testing.T) {
	t.Parallel()

	manager := newCoAgentManager(t.TempDir(), "", map[string]adapterapi.LiveAgentAdapter{
		"fake": fakeLiveAdapter{responseText: "delegated"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spawned, err := manager.spawn(ctx, "work-123", "fake", "fake/model", "checker", "", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if spawned == "" {
		t.Fatal("expected spawn response")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(spawned), &payload); err != nil {
		t.Fatalf("unmarshal spawn response: %v", err)
	}
	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session_id in spawn response: %s", spawned)
	}

	posted, err := manager.postMessage("work-123", "worker-1", "worker", "hello co-agent")
	if err != nil {
		t.Fatalf("postMessage: %v", err)
	}
	if posted == "" {
		t.Fatal("expected postMessage response")
	}

	waited, err := manager.waitForMessage(ctx, "work-123", 1, time.Second)
	if err != nil {
		t.Fatalf("waitForMessage: %v", err)
	}

	var waitPayload struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   uint64           `json:"cursor"`
		TimedOut bool             `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(waited), &waitPayload); err != nil {
		t.Fatalf("unmarshal wait response: %v", err)
	}
	if waitPayload.TimedOut {
		t.Fatal("expected wait_for_message to receive a reply")
	}
	if len(waitPayload.Messages) != 1 {
		t.Fatalf("wait message count = %d, want 1", len(waitPayload.Messages))
	}
	if waitPayload.Messages[0].Role != "checker" || waitPayload.Messages[0].Content != "delegated" {
		t.Fatalf("unexpected waited message: %+v", waitPayload.Messages[0])
	}
	if waitPayload.Cursor != 2 {
		t.Fatalf("wait cursor = %d, want 2", waitPayload.Cursor)
	}

	read, err := manager.readMessages("work-123", 0)
	if err != nil {
		t.Fatalf("readMessages: %v", err)
	}
	var readPayload struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   uint64           `json:"cursor"`
	}
	if err := json.Unmarshal([]byte(read), &readPayload); err != nil {
		t.Fatalf("unmarshal read response: %v", err)
	}
	if len(readPayload.Messages) != 2 {
		t.Fatalf("read message count = %d, want 2", len(readPayload.Messages))
	}
	if readPayload.Messages[0].Content != "hello co-agent" || readPayload.Messages[1].Content != "delegated" {
		t.Fatalf("unexpected read messages: %+v", readPayload.Messages)
	}

	closed, err := manager.closeOne(sessionID)
	if err != nil {
		t.Fatalf("closeOne: %v", err)
	}
	if closed == "" {
		t.Fatal("expected close response")
	}
}

func TestWaitForMessageToolReturnsTimeoutPayload(t *testing.T) {
	t.Parallel()

	manager := newCoAgentManager(t.TempDir(), "", map[string]adapterapi.LiveAgentAdapter{"fake": fakeLiveAdapter{}})
	registry := MustNewToolRegistry()
	if err := RegisterCoAgentTools(registry, manager); err != nil {
		t.Fatalf("RegisterCoAgentTools returned error: %v", err)
	}

	out, err := registry.Execute(context.Background(), "wait_for_message", mustJSON(t, map[string]any{
		"work_id":    "work-timeout",
		"timeout_ms": 25,
	}))
	if err != nil {
		t.Fatalf("wait_for_message returned error: %v", err)
	}

	var payload struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   uint64           `json:"cursor"`
		TimedOut bool             `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode wait_for_message output: %v", err)
	}
	if !payload.TimedOut {
		t.Fatalf("timed_out = false, want true: %s", out)
	}
	if len(payload.Messages) != 0 || payload.Cursor != 0 {
		t.Fatalf("unexpected timeout payload: %+v", payload)
	}
}

type fakeLiveAdapter struct {
	responseText string
}

func (fakeLiveAdapter) Name() string { return "fake" }

func (f fakeLiveAdapter) StartSession(ctx context.Context, req adapterapi.StartSessionRequest) (adapterapi.LiveSession, error) {
	return newFakeLiveSession(f.responseText), nil
}

func (f fakeLiveAdapter) ResumeSession(ctx context.Context, nativeSessionID string, req adapterapi.StartSessionRequest) (adapterapi.LiveSession, error) {
	return newFakeLiveSession(f.responseText), nil
}

type fakeLiveSession struct {
	responseText string
	id           string
	turnID       string
	eventCh      chan adapterapi.Event
}

var fakeLiveSessionSeq atomic.Int64

func newFakeLiveSession(responseText string) *fakeLiveSession {
	s := &fakeLiveSession{
		responseText: responseText,
		id:           fmt.Sprintf("fake-session-%d", fakeLiveSessionSeq.Add(1)),
		eventCh:      make(chan adapterapi.Event, 8),
	}
	s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindSessionStarted, SessionID: s.id}
	return s
}

func (s *fakeLiveSession) SessionID() string { return s.id }
func (s *fakeLiveSession) ActiveTurnID() string {
	return s.turnID
}
func (s *fakeLiveSession) StartTurn(ctx context.Context, input []adapterapi.Input) (string, error) {
	s.turnID = "fake-turn"
	s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindTurnStarted, SessionID: s.id, TurnID: s.turnID}
	text := s.responseText
	if text == "" {
		text = "delegated"
	}
	s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindOutputDelta, SessionID: s.id, TurnID: s.turnID, Text: text}
	s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindTurnCompleted, SessionID: s.id, TurnID: s.turnID}
	return s.turnID, nil
}
func (s *fakeLiveSession) Steer(ctx context.Context, expectedTurnID string, input []adapterapi.Input) error {
	return nil
}
func (s *fakeLiveSession) Interrupt(ctx context.Context) error { return nil }
func (s *fakeLiveSession) Events() <-chan adapterapi.Event     { return s.eventCh }
func (s *fakeLiveSession) Close() error {
	close(s.eventCh)
	return nil
}
