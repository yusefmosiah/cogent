package cli

import (
	"strings"
	"testing"

	"github.com/yusefmosiah/fase/internal/service"
)

func TestAgenticSupervisorPauseResume(t *testing.T) {
	sup := &agenticSupervisor{hostCh: make(chan string, 16)}

	if sup.isPaused() {
		t.Fatal("supervisor should not be paused initially")
	}

	sup.pause()
	if !sup.isPaused() {
		t.Fatal("supervisor should be paused after pause()")
	}

	sup.resume()
	if sup.isPaused() {
		t.Fatal("supervisor should not be paused after resume()")
	}
}

func TestFormatTurnPromptWithEvents(t *testing.T) {
	input := turnInput{
		events: []service.WorkEvent{
			{Kind: service.WorkEventCreated, WorkID: "work_1", Title: "New task"},
			{Kind: service.WorkEventUpdated, WorkID: "work_2", Title: "Running task", State: "completed", PrevState: "in_progress"},
			{Kind: service.WorkEventAttested, WorkID: "work_3", Title: "Done task"},
		},
	}

	prompt := formatTurnPrompt(input)

	for _, want := range []string{"NEW: New task", "Running task", "in_progress → completed", "ATTESTED: Done task"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestFormatTurnPromptWithHostMessage(t *testing.T) {
	input := turnInput{
		hostMessages: []string{"Please dispatch work_123 next"},
	}

	prompt := formatTurnPrompt(input)

	if !strings.Contains(prompt, "Message from host") {
		t.Error("prompt should contain host message header")
	}
	if !strings.Contains(prompt, "dispatch work_123") {
		t.Error("prompt should contain the host message")
	}
}

func TestFormatTurnPromptWithBoth(t *testing.T) {
	input := turnInput{
		events:       []service.WorkEvent{{Kind: service.WorkEventCreated, WorkID: "w1", Title: "Task"}},
		hostMessages: []string{"Focus on w1"},
	}

	prompt := formatTurnPrompt(input)

	if !strings.Contains(prompt, "Message from host") {
		t.Error("prompt should contain host message")
	}
	if !strings.Contains(prompt, "Events since") {
		t.Error("prompt should contain events")
	}
}

func TestFormatTurnPromptEmpty(t *testing.T) {
	prompt := formatTurnPrompt(turnInput{})
	if prompt == "" {
		t.Fatal("expected non-empty prompt even with no input")
	}
}

func TestSupervisorSend(t *testing.T) {
	sup := &agenticSupervisor{hostCh: make(chan string, 16)}
	sup.send("hello supervisor")

	select {
	case msg := <-sup.hostCh:
		if msg != "hello supervisor" {
			t.Fatalf("got %q, want %q", msg, "hello supervisor")
		}
	default:
		t.Fatal("expected message on hostCh")
	}
}

func TestIsRelevantEvent(t *testing.T) {
	for _, tc := range []struct {
		kind service.WorkEventKind
		want bool
	}{
		{service.WorkEventCreated, true},
		{service.WorkEventUpdated, true},
		{service.WorkEventAttested, true},
		{service.WorkEventReleased, true},
		{service.WorkEventClaimed, false},
		{service.WorkEventLeaseRenew, false},
	} {
		if got := isRelevantEvent(service.WorkEvent{Kind: tc.kind}); got != tc.want {
			t.Errorf("isRelevantEvent(%s) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}
