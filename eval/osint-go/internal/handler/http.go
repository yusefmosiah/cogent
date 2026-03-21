package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/scanner"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/service"
)

type Handler struct {
	aggregator *service.Aggregator
	store      service.ResultStore
}

func New(aggregator *service.Aggregator, store service.ResultStore) *Handler {
	return &Handler{aggregator: aggregator, store: store}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /scan", h.handleScan)
	mux.HandleFunc("GET /results/{id}", h.handleResult)
	return mux
}

func (h *Handler) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req model.ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}
	result, err := h.aggregator.Scan(r.Context(), req.Domain)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, scanner.ErrInvalidDomain) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleResult(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "result id is required", http.StatusBadRequest)
		return
	}
	result, ok := h.store.Get(r.Context(), id)
	if !ok {
		http.Error(w, "result not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
