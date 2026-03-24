package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/yusefmosiah/fase/internal/channelmeta"
	"github.com/yusefmosiah/fase/internal/core"
)

func TestReportJobCompletionPostsChannelNotification(t *testing.T) {
	var got struct {
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/channel/send" {
			t.Fatalf("expected /api/channel/send, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"sent"}`))
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	stateDir := t.TempDir()
	serveData, err := json.Marshal(map[string]any{"port": port})
	if err != nil {
		t.Fatalf("marshal serve.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "serve.json"), serveData, 0o644); err != nil {
		t.Fatalf("write serve.json: %v", err)
	}

	svc := &Service{Paths: core.Paths{StateDir: stateDir}}
	svc.reportJobCompletion("job finished")

	if got.Content != "job finished" {
		t.Fatalf("expected content to round-trip, got %q", got.Content)
	}
	if want := channelmeta.JobCompletionMeta(); !reflect.DeepEqual(got.Meta, want) {
		t.Fatalf("unexpected meta: got %#v want %#v", got.Meta, want)
	}
}
