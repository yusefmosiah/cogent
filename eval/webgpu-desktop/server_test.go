package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileAPIListReadWrite(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "notes", "hello.txt"), "hello world")
	mustWrite(t, filepath.Join(root, "projects", "readme.md"), "# project")

	srv, err := NewServer(root)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/files?path=")
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}

	var list struct {
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"isDir"`
		} `json:"entries"`
	}
	decode(t, resp.Body, &list)
	if list.Path != "" {
		t.Fatalf("list path = %q", list.Path)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("entry count = %d", len(list.Entries))
	}
	if !list.Entries[0].IsDir || list.Entries[0].Name != "notes" {
		t.Fatalf("first entry = %+v", list.Entries[0])
	}

	readResp, err := http.Get(ts.URL + "/api/files/read?path=notes/hello.txt")
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	defer readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d", readResp.StatusCode)
	}
	var readBody readResponse
	decode(t, readResp.Body, &readBody)
	if readBody.Content != "hello world" {
		t.Fatalf("read content = %q", readBody.Content)
	}

	writePayload := `{"path":"notes/new.txt","content":"fresh"}`
	writeResp, err := http.Post(ts.URL+"/api/files/write", "application/json", strings.NewReader(writePayload))
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
	defer writeResp.Body.Close()
	if writeResp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d", writeResp.StatusCode)
	}
	if got := mustReadFile(t, filepath.Join(root, "notes", "new.txt")); got != "fresh" {
		t.Fatalf("written content = %q", got)
	}
}

func TestFileAPISecuresTraversal(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(root)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/files/read?path=../secret.txt")
	if err != nil {
		t.Fatalf("traversal request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status = %d", resp.StatusCode)
	}
}

func TestIndexServed(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(root)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("index request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", resp.StatusCode)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func decode(t *testing.T, body io.Reader, out any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
