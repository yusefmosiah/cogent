package native_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yusefmosiah/fase/internal/adapterapi"
	"github.com/yusefmosiah/fase/internal/adapters/native"
)

// TestLiveAdapter_StartSession verifies that the native adapter creates a
// session emitting session.started and that the session ID is non-empty.
func TestLiveAdapter_StartSession(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if session.SessionID() == "" {
		t.Fatal("expected non-empty session ID")
	}

	ev := drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)
	if ev.SessionID != session.SessionID() {
		t.Fatalf("session ID mismatch: %s != %s", ev.SessionID, session.SessionID())
	}
	t.Logf("session started: %s", session.SessionID())
}

// TestLiveAdapter_ResumeSession verifies that ResumeSession emits session.resumed
// and preserves the provided session ID.
func TestLiveAdapter_ResumeSession(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const nativeID = "nsess_RESUMETEST"
	session, err := adapter.ResumeSession(ctx, nativeID, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if session.SessionID() != nativeID {
		t.Fatalf("session ID mismatch: got %s want %s", session.SessionID(), nativeID)
	}

	ev := drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionResumed)
	if ev.SessionID != nativeID {
		t.Fatalf("event session ID mismatch: %s != %s", ev.SessionID, nativeID)
	}
}

// TestLiveAdapter_StartTurn_Echo verifies that StartTurn with no model
// routes to the echo worker and produces the correct event sequence:
// turn.started → output.delta → turn.completed.
func TestLiveAdapter_StartTurn_Echo(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	const prompt = "hello from conductor"
	turnID, err := session.StartTurn(ctx, []adapterapi.Input{
		adapterapi.TextInput(prompt),
	})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if turnID == "" {
		t.Fatal("expected non-empty turn ID")
	}

	var gotDelta, gotCompleted bool
	deadline := time.After(4 * time.Second)
	for !gotDelta || !gotCompleted {
		select {
		case ev, ok := <-session.Events():
			if !ok {
				t.Fatal("event channel closed prematurely")
			}
			t.Logf("event: kind=%s turn=%s text=%q", ev.Kind, ev.TurnID, ev.Text)
			switch ev.Kind {
			case adapterapi.EventKindTurnStarted:
				// StartTurn emits this before the goroutine; may see again from echo.
			case adapterapi.EventKindOutputDelta:
				if ev.TurnID != turnID {
					t.Errorf("delta turn ID mismatch: %s != %s", ev.TurnID, turnID)
				}
				if ev.Text != prompt {
					t.Errorf("unexpected delta text: %q", ev.Text)
				}
				gotDelta = true
			case adapterapi.EventKindTurnCompleted:
				if ev.TurnID != turnID {
					t.Errorf("completed turn ID mismatch: %s != %s", ev.TurnID, turnID)
				}
				gotCompleted = true
			case adapterapi.EventKindTurnFailed:
				t.Fatalf("unexpected turn.failed: %s", ev.Text)
			}
		case <-deadline:
			t.Fatalf("timeout: gotDelta=%v gotCompleted=%v", gotDelta, gotCompleted)
		}
	}
}

// TestLiveAdapter_MultipleTurns verifies that a session can handle sequential
// turns correctly, each producing complete event sequences.
func TestLiveAdapter_MultipleTurns(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	for i := 0; i < 3; i++ {
		turnID, err := session.StartTurn(ctx, []adapterapi.Input{
			adapterapi.TextInput("ping"),
		})
		if err != nil {
			t.Fatalf("StartTurn %d: %v", i, err)
		}
		t.Logf("turn %d started: %s", i, turnID)

		drainUntil(t, ctx, session.Events(), adapterapi.EventKindTurnCompleted)
		t.Logf("turn %d completed", i)
	}
}

// TestLiveAdapter_CoAgent verifies that the conductor routes turns to an
// external co-agent adapter when a model is specified.
func TestLiveAdapter_CoAgent(t *testing.T) {
	mock := &mockLiveAdapter{name: "mock"}
	coAgents := map[string]adapterapi.LiveAgentAdapter{"mock": mock}

	adapter := native.NewLiveAdapter(nil, coAgents)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{
		Model: "mock/test-model",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	turnID, err := session.StartTurn(ctx, []adapterapi.Input{
		adapterapi.TextInput("via co-agent"),
	})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if turnID == "" {
		t.Fatal("expected non-empty turn ID")
	}

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindTurnCompleted)
	t.Logf("co-agent turn completed: %s", turnID)

	if mock.lastModel != "test-model" {
		t.Errorf("co-agent got model %q, want %q", mock.lastModel, "test-model")
	}
}

// TestLiveAdapter_UnknownCoAgent verifies that an unknown adapter name causes
// StartTurn to fail cleanly with a turn.failed event.
func TestLiveAdapter_UnknownCoAgent(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{
		Model: "nonexistent/some-model",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	_, err = session.StartTurn(ctx, []adapterapi.Input{adapterapi.TextInput("hello")})
	if err == nil {
		t.Fatal("expected error for unknown co-agent, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// TestLiveAdapter_Interrupt verifies that an active turn can be interrupted.
func TestLiveAdapter_Interrupt(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	_, err = session.StartTurn(ctx, []adapterapi.Input{adapterapi.TextInput("interrupt me")})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// Wait for turn.started before interrupting.
	drainUntil(t, ctx, session.Events(), adapterapi.EventKindTurnStarted)

	if err := session.Interrupt(ctx); err != nil {
		// The echo worker may complete before the interrupt arrives — not fatal.
		t.Logf("Interrupt: %v (may be ok if turn already completed)", err)
	}

	for {
		select {
		case ev, ok := <-session.Events():
			if !ok {
				t.Fatal("event channel closed unexpectedly")
			}
			t.Logf("event: kind=%s", ev.Kind)
			switch ev.Kind {
			case adapterapi.EventKindTurnCompleted,
				adapterapi.EventKindTurnInterrupted,
				adapterapi.EventKindTurnFailed:
				t.Logf("turn ended: %s", ev.Kind)
				return
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for turn to end")
		}
	}
}

// TestLiveAdapter_Close verifies that Close shuts down the session and causes
// the event channel to be closed.
func TestLiveAdapter_Close(t *testing.T) {
	adapter := native.NewLiveAdapter(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := adapter.StartSession(ctx, adapterapi.StartSessionRequest{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	drainUntil(t, ctx, session.Events(), adapterapi.EventKindSessionStarted)

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-session.Events():
			if !ok {
				t.Log("event channel closed after session.Close()")
				return
			}
			// Drain session.closed or other tail events.
		case <-deadline:
			t.Fatal("timeout waiting for event channel to close after Close()")
		}
	}
}

// TestParseModel verifies the adapter/model string parsing.
func TestParseModel(t *testing.T) {
	tests := []struct {
		input       string
		wantAdapter string
		wantModel   string
	}{
		{"", "", ""},
		{"claude/claude-opus-4-6", "claude", "claude-opus-4-6"},
		{"opencode/anthropic/claude-opus-4-6", "opencode", "anthropic/claude-opus-4-6"},
		{"codex", "codex", ""},
		{"codex/gpt-5.4-mini", "codex", "gpt-5.4-mini"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotAdapter, gotModel := native.ParseModelForTest(tt.input)
			if gotAdapter != tt.wantAdapter || gotModel != tt.wantModel {
				t.Errorf("ParseModel(%q) = (%q, %q), want (%q, %q)",
					tt.input, gotAdapter, gotModel, tt.wantAdapter, tt.wantModel)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// drainUntil reads events until the target kind is found.
func drainUntil(t *testing.T, ctx context.Context, ch <-chan adapterapi.Event, kind adapterapi.EventKind) adapterapi.Event {
	t.Helper()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed before receiving %s", kind)
			}
			t.Logf("event: kind=%s session=%s turn=%s text=%q", ev.Kind, ev.SessionID, ev.TurnID, ev.Text)
			if ev.Kind == kind {
				return ev
			}
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %s event", kind)
		}
	}
}

// -----------------------------------------------------------------------
// Mock co-agent adapter
// -----------------------------------------------------------------------

// mockLiveAdapter is a test double backed by mock sessions (echo with prefix).
type mockLiveAdapter struct {
	name      string
	lastModel string
}

func (m *mockLiveAdapter) Name() string { return m.name }

func (m *mockLiveAdapter) StartSession(_ context.Context, req adapterapi.StartSessionRequest) (adapterapi.LiveSession, error) {
	m.lastModel = req.Model
	return newMockSession(), nil
}

func (m *mockLiveAdapter) ResumeSession(_ context.Context, _ string, req adapterapi.StartSessionRequest) (adapterapi.LiveSession, error) {
	m.lastModel = req.Model
	return newMockSession(), nil
}

// mockSession mimics an external adapter session:
// emits session.started immediately, then echoes turns with a "[mock:]" prefix.
type mockSession struct {
	sessionID string
	eventCh   chan adapterapi.Event
	turnSeq   atomic.Int64

	activeMu   sync.Mutex
	activeTurn string

	closeOnce sync.Once
}

func newMockSession() *mockSession {
	id := fmt.Sprintf("mock-%d", time.Now().UnixNano())
	s := &mockSession{
		sessionID: id,
		eventCh:   make(chan adapterapi.Event, 64),
	}
	s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindSessionStarted, SessionID: id}
	return s
}

func (s *mockSession) SessionID() string { return s.sessionID }

func (s *mockSession) ActiveTurnID() string {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.activeTurn
}

func (s *mockSession) StartTurn(_ context.Context, input []adapterapi.Input) (string, error) {
	turnID := fmt.Sprintf("mturn-%d", s.turnSeq.Add(1))
	s.activeMu.Lock()
	s.activeTurn = turnID
	s.activeMu.Unlock()

	go func() {
		s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindTurnStarted, SessionID: s.sessionID, TurnID: turnID}
		for _, inp := range input {
			if inp.Text != "" {
				s.eventCh <- adapterapi.Event{
					Kind:      adapterapi.EventKindOutputDelta,
					SessionID: s.sessionID,
					TurnID:    turnID,
					Text:      "[mock:" + inp.Text + "]",
				}
			}
		}
		s.activeMu.Lock()
		if s.activeTurn == turnID {
			s.activeTurn = ""
		}
		s.activeMu.Unlock()
		s.eventCh <- adapterapi.Event{Kind: adapterapi.EventKindTurnCompleted, SessionID: s.sessionID, TurnID: turnID}
	}()

	return turnID, nil
}

func (s *mockSession) Steer(_ context.Context, _ string, _ []adapterapi.Input) error { return nil }
func (s *mockSession) Interrupt(_ context.Context) error                             { return nil }
func (s *mockSession) Events() <-chan adapterapi.Event                               { return s.eventCh }
func (s *mockSession) Close() error {
	s.closeOnce.Do(func() { close(s.eventCh) })
	return nil
}
