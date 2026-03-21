package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed web/*
var webAssets embed.FS

type Server struct {
	root string
	mux  *http.ServeMux
}

type fileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	Readable bool      `json:"readable"`
}

type readResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeResponse struct {
	Path    string `json:"path"`
	Written bool   `json:"written"`
}

func NewServer(root string) (*Server, error) {
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("ensure workspace root: %w", err)
	}

	s := &Server{root: root, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.mux)
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	static, _ := fs.Sub(webAssets, "web")
	s.mux.Handle("/", http.FileServer(http.FS(static)))
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/files", s.handleListFiles)
	s.mux.HandleFunc("/api/files/read", s.handleReadFile)
	s.mux.HandleFunc("/api/files/write", s.handleWriteFile)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rel := r.URL.Query().Get("path")
	entries, err := s.list(rel)
	if err != nil {
		s.writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    cleanDisplayPath(rel),
		"entries": entries,
	})
}

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rel := r.URL.Query().Get("path")
	path, err := s.resolve(rel)
	if err != nil {
		s.writeError(w, err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if info.IsDir() {
		s.writeError(w, errors.New("path is a directory"))
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		s.writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, readResponse{
		Path:    cleanDisplayPath(rel),
		Content: string(content),
	})
}

func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req writeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, fmt.Errorf("decode request: %w", err))
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		s.writeError(w, errors.New("path is required"))
		return
	}

	path, err := s.resolve(req.Path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.writeError(w, err)
		return
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		s.writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, writeResponse{
		Path:    cleanDisplayPath(req.Path),
		Written: true,
	})
}

func (s *Server) list(rel string) ([]fileEntry, error) {
	path, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	result := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		entryPath := filepath.ToSlash(strings.TrimPrefix(filepath.Join(cleanDisplayPath(rel), entry.Name()), "./"))
		result = append(result, fileEntry{
			Name:     entry.Name(),
			Path:     entryPath,
			IsDir:    entry.IsDir(),
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC(),
			Readable: true,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func (s *Server) resolve(rel string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(rel))
	if clean == "." || clean == string(filepath.Separator) || clean == "" {
		clean = "."
	}
	if filepath.IsAbs(clean) {
		return "", errors.New("absolute paths are not allowed")
	}
	full := filepath.Join(s.root, clean)
	full = filepath.Clean(full)
	rootPrefix := filepath.Clean(s.root) + string(filepath.Separator)
	if full != filepath.Clean(s.root) && !strings.HasPrefix(full, rootPrefix) {
		return "", errors.New("path escapes workspace root")
	}
	return full, nil
}

func cleanDisplayPath(rel string) string {
	trimmed := strings.TrimSpace(rel)
	trimmed = strings.TrimPrefix(trimmed, "./")
	trimmed = filepath.ToSlash(trimmed)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "." {
		return ""
	}
	return trimmed
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}
