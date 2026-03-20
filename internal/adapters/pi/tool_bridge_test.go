package pi

import (
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello world", "'hello world'"},
		{"it's", "'it'\\''s'"},
		{"", ""},
		{"foo\tbar", "'foo\tbar'"},
		{"$HOME", "'$HOME'"},
	}
	for _, tc := range tests {
		got := shellQuote(tc.input)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCagentCLICommand(t *testing.T) {
	tests := []struct {
		name       string
		bin        string
		config     string
		workID     string
		wantPrefix string
	}{
		{"simple", "cagent", "", "w123", "cagent work show w123"},
		{"with config", "cagent", "/cfg.toml", "w123", "cagent --config /cfg.toml work show w123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CagentCLICommand(tc.bin, tc.config, tc.workID)
			if got != tc.wantPrefix {
				t.Errorf("got %q, want %q", got, tc.wantPrefix)
			}
		})
	}
}

func TestCagentCLINoteAdd(t *testing.T) {
	got := CagentCLINoteAdd("cagent", "/cfg.toml", "w123", "test note")
	want := "cagent --config /cfg.toml work note-add w123 --body 'test note'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCagentCLIWorkUpdate(t *testing.T) {
	got := CagentCLIWorkUpdate("cagent", "/cfg.toml", "w123", "status update")
	want := "cagent --config /cfg.toml work update w123 --message 'status update'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWorkEventFormat(t *testing.T) {
	ev := WorkEvent{
		Kind:      "work_updated",
		WorkID:    "w123",
		Title:     "Fix bug",
		State:     "in_progress",
		PrevState: "claimed",
	}
	tb := &ToolBridge{
		cagentBin:  "cagent",
		configPath: "/cfg.toml",
	}
	msg := tb.formatEvent(ev)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	t.Logf("formatted event:\n%s", msg)
}

func TestDeliveryMode(t *testing.T) {
	if DeliverySteer != DeliveryMode("steer") {
		t.Errorf("DeliverySteer = %q, want %q", DeliverySteer, "steer")
	}
	if DeliveryFollowUp != DeliveryMode("follow_up") {
		t.Errorf("DeliveryFollowUp = %q, want %q", DeliveryFollowUp, "follow_up")
	}
}
