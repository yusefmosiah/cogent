package cli

import (
	"bytes"
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
)

func TestReportCommandUsesWorkerReportContract(t *testing.T) {
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
	t.Setenv("FASE_STATE_DIR", stateDir)
	serveData, err := json.Marshal(serveInfo{
		PID:  os.Getpid(),
		Port: port,
		CWD:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("marshal serve.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "serve.json"), serveData, 0o644); err != nil {
		t.Fatalf("write serve.json: %v", err)
	}

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"report", "hello from cli"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute report command: %v", err)
	}

	if got.Content != "hello from cli" {
		t.Fatalf("expected content to round-trip, got %q", got.Content)
	}
	if want := channelmeta.WorkerReportMeta(channelmeta.TypeInfo); !reflect.DeepEqual(got.Meta, want) {
		t.Fatalf("unexpected meta: got %#v want %#v", got.Meta, want)
	}
}
