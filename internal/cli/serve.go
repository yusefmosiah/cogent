package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yusefmosiah/cagent/internal/core"
	"github.com/yusefmosiah/cagent/internal/service"
	"github.com/yusefmosiah/cagent/internal/web"
)

func newServeCommand(root *rootOptions) *cobra.Command {
	var port int
	var host string
	var auto bool
	var noUI bool
	var noBrowser bool
	var maxConcurrent int
	var defaultAdapter string
	var devAssets string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the cagent service: web UI, API, and housekeeping",
		Long: `Starts the cagent service: mind-graph web UI, HTTP API, and background
housekeeping (lease reconciliation, stall detection).

By default, no work is auto-dispatched. Use --auto to enable autonomous
claiming and execution of ready work items.

Examples:
  cagent serve                          # UI + API + housekeeping
  cagent serve --auto                   # also auto-dispatch ready work
  cagent serve --host 0.0.0.0           # accessible via Tailscale/LAN
  cagent serve --no-browser             # don't open browser on start`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, root, port, host, auto, noUI, noBrowser, maxConcurrent, defaultAdapter, devAssets)
		},
	}

	cmd.Flags().IntVar(&port, "port", 4242, "HTTP server port")
	cmd.Flags().StringVar(&host, "host", "localhost", "HTTP bind host")
	cmd.Flags().BoolVar(&auto, "auto", false, "auto-dispatch ready work items")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "skip web UI, run housekeeping only")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "don't auto-open browser")
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 1, "max simultaneous jobs (with --auto)")
	cmd.Flags().StringVar(&defaultAdapter, "default-adapter", "codex", "fallback adapter (with --auto)")
	cmd.Flags().StringVar(&devAssets, "dev-assets", "", "serve UI from filesystem instead of embedded (for development)")

	return cmd
}

func runServe(cmd *cobra.Command, root *rootOptions, port int, host string, auto, noUI, noBrowser bool, maxConcurrent int, defaultAdapter, devAssets string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open service once — shared by all goroutines
	svc, err := service.Open(ctx, root.configPath)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer func() { _ = svc.Close() }()

	cwd, _ := os.Getwd()

	// Find a free port
	listenAddr := net.JoinHostPort(host, fmt.Sprint(port))
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// Try next port
		listener, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port

	// Write serve.json for CLI discovery
	serveInfo := map[string]any{
		"pid":  os.Getpid(),
		"port": actualPort,
		"cwd":  cwd,
		"auto": auto,
	}
	serveJSON, _ := json.MarshalIndent(serveInfo, "", "  ")
	servePath := filepath.Join(svc.Paths.StateDir, "serve.json")
	_ = os.WriteFile(servePath, serveJSON, 0o644)
	defer os.Remove(servePath)

	// Set up HTTP handlers
	mux := http.NewServeMux()
	registerAPIHandlers(mux, svc, cwd)

	if !noUI {
		// Serve mind-graph UI
		if devAssets != "" {
			// Development: serve from filesystem
			mux.Handle("/", http.FileServer(http.Dir(devAssets)))
		} else {
			// Production: serve from embedded assets
			subFS, err := fs.Sub(web.Assets, "dist")
			if err != nil {
				return fmt.Errorf("embedded assets: %w", err)
			}
			mux.Handle("/", http.FileServer(http.FS(subFS)))
		}
	}

	server := &http.Server{Handler: mux}

	var wg sync.WaitGroup

	// Always run housekeeping (reconcile leases, detect stalls)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runHousekeeping(ctx, svc, cwd)
	}()

	// Only auto-dispatch when --auto is set
	if auto {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runInProcessSupervisor(ctx, svc, cwd, root, maxConcurrent, defaultAdapter)
		}()
	}

	// Start HTTP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := server.Serve(listener); err != http.ErrServerClosed {
			fmt.Fprintf(cmd.ErrOrStderr(), "HTTP server error: %v\n", err)
		}
	}()

	displayHost := host
	if displayHost == "0.0.0.0" || displayHost == "::" || displayHost == "" {
		displayHost = "localhost"
	}
	url := "http://" + net.JoinHostPort(displayHost, fmt.Sprint(actualPort))
	mode := "serve"
	if auto {
		mode = "serve --auto"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "cagent %s: %s (pid %d)\n", mode, url, os.Getpid())

	// Auto-open browser
	if !noUI && !noBrowser {
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = exec.Command("open", url).Start() // macOS
		}()
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(cmd.OutOrStdout(), "\ncagent serve: shutting down...")
	cancel() // stops housekeeping and supervisor goroutines

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)

	wg.Wait()
	fmt.Fprintln(cmd.OutOrStdout(), "cagent serve: stopped")
	return nil
}

// runHousekeeping runs periodic maintenance without dispatching work:
// - Reconcile expired leases (orphaned claims)
// - Detect stalled jobs (no output for 10 minutes)
// - Dispatch verification for completed jobs (from cagent dispatch)
func runHousekeeping(ctx context.Context, svc *service.Service, cwd string) {
	selfBin, _ := os.Executable()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Track which work items we've already dispatched verification for
	verified := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Reconcile expired leases
			_, _ = svc.ReconcileOnStartup(ctx)

			// Check for stalled jobs and completed jobs needing verification
			rawDir := filepath.Join(cwd, ".cagent", "raw", "stdout")
			entries, err := os.ReadDir(rawDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "job_") {
					continue
				}
				jobID := entry.Name()
				jobDir := filepath.Join(rawDir, jobID)

				statusResult, err := svc.Status(ctx, jobID)
				if err != nil {
					continue
				}
				jobState := string(statusResult.Job.State)
				workID := statusResult.Job.WorkID

				if workID == "" {
					continue
				}

				if isJobStalled(jobDir, 10*time.Minute) && !isTerminal(jobState) {
					_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
						WorkID:         workID,
						ExecutionState: core.WorkExecutionStateFailed,
						Message:        fmt.Sprintf("housekeeping: job %s stalled (no output for 10m)", jobID),
						CreatedBy:      "housekeeping",
					})
					continue
				}

				// If job completed and work is still in_progress, dispatch attestation
				if isTerminal(jobState) && !verified[workID] {
					workResult, err := svc.Work(ctx, workID)
					if err != nil {
						continue
					}
					if workResult.Work.ExecutionState == core.WorkExecutionStateInProgress ||
						workResult.Work.ExecutionState == core.WorkExecutionStateClaimed {
						verified[workID] = true
						flight := &inFlightJob{
							workID:  workID,
							jobID:   jobID,
							adapter: statusResult.Job.Adapter,
						}
						// Get config path from the binary's default
						configPath := ""
						go handleJobCompletion(ctx, svc, selfBin, configPath, cwd, workID, flight, "codex")
					}
				}
			}
		}
	}
}

func registerAPIHandlers(mux *http.ServeMux, svc *service.Service, cwd string) {
	// Work items list
	mux.HandleFunc("/api/work/items", func(w http.ResponseWriter, r *http.Request) {
		includeArchived := r.URL.Query().Get("include_archived") == "1" || strings.EqualFold(r.URL.Query().Get("include_archived"), "true")
		items, err := svc.ListWork(r.Context(), service.WorkListRequest{Limit: 500, IncludeArchived: includeArchived})
		if err != nil {
			writeJSONHTTP(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSONHTTP(w, 200, items)
	})

	// Work edges list (for DAG view)
	mux.HandleFunc("/api/work/edges", func(w http.ResponseWriter, r *http.Request) {
		edges, err := svc.ListEdges(r.Context(), 500, "", "", "")
		if err != nil {
			writeJSONHTTP(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSONHTTP(w, 200, edges)
	})

	// Work item show
	mux.HandleFunc("/api/work/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/work/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			writeJSONHTTP(w, 404, map[string]string{"error": "missing work id"})
			return
		}
		workID := parts[0]

		if len(parts) == 1 {
			result, err := svc.Work(r.Context(), workID)
			if err != nil {
				writeJSONHTTP(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSONHTTP(w, 200, result)
			return
		}

		switch parts[1] {
		case "hydrate":
			mode := r.URL.Query().Get("mode")
			if mode == "" {
				mode = "standard"
			}
			result, err := svc.HydrateWork(r.Context(), service.WorkHydrateRequest{
				WorkID: workID,
				Mode:   mode,
			})
			if err != nil {
				writeJSONHTTP(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSONHTTP(w, 200, result)
		default:
			writeJSONHTTP(w, 404, map[string]string{"error": "unknown endpoint"})
		}
	})

	// Supervisor status
	mux.HandleFunc("/api/supervisor/status", func(w http.ResponseWriter, r *http.Request) {
		supPath := filepath.Join(cwd, ".cagent", "supervisor.json")
		supData, _ := os.ReadFile(supPath)
		var sup any
		if len(supData) > 0 {
			_ = json.Unmarshal(supData, &sup)
		}

		// Git diff stat
		diffStat := ""
		if out, err := exec.CommandContext(r.Context(), "git", "diff", "--stat").Output(); err == nil {
			diffStat = string(out)
		}

		writeJSONHTTP(w, 200, map[string]any{
			"supervisor": sup,
			"diff_stat":  diffStat,
		})
	})

	// Full diff
	mux.HandleFunc("/api/diff", func(w http.ResponseWriter, r *http.Request) {
		out, _ := exec.CommandContext(r.Context(), "git", "diff", "--patch").Output()
		writeJSONHTTP(w, 200, map[string]any{"diff": string(out)})
	})

	// Bash log
	mux.HandleFunc("/api/bash-log", func(w http.ResponseWriter, r *http.Request) {
		rawDir := filepath.Join(cwd, ".cagent", "raw", "stdout")
		commands, jobID := collectBashLogCommands(rawDir)
		writeJSONHTTP(w, 200, map[string]any{
			"commands": commands,
			"job_id":   jobID,
		})
	})
}

func writeJSONHTTP(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type bashLogCommand struct {
	Command       string `json:"command,omitempty"`
	ExitCode      int    `json:"exit_code,omitempty"`
	OutputPreview string `json:"output_preview,omitempty"`
	Comment       string `json:"comment,omitempty"`
}

type bashLogState struct {
	commands []bashLogCommand
	pending  map[string]int
}

func collectBashLogCommands(rawDir string) ([]bashLogCommand, string) {
	dirs, err := os.ReadDir(rawDir)
	if err != nil {
		return []bashLogCommand{}, ""
	}

	// Find the newest job directory by sorted ReadDir order.
	var latestDir string
	for i := len(dirs) - 1; i >= 0; i-- {
		if strings.HasPrefix(dirs[i].Name(), "job_") {
			latestDir = filepath.Join(rawDir, dirs[i].Name())
			break
		}
	}
	if latestDir == "" {
		return []bashLogCommand{}, ""
	}

	state := &bashLogState{
		pending: map[string]int{},
	}

	files, err := os.ReadDir(latestDir)
	if err != nil {
		return []bashLogCommand{}, filepath.Base(latestDir)
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(latestDir, f.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			state.ingest(ev)
		}
	}

	return state.commands, filepath.Base(latestDir)
}

func (s *bashLogState) ingest(ev map[string]any) {
	if ev == nil {
		return
	}

	if isBashResultEvent(ev) {
		s.updateFromResult(ev)
		return
	}

	if command, id, exitCode, output, ok := extractBashCommand(ev); ok {
		s.addCommand(id, command, exitCode, output)
		return
	}

	if comment := extractBashComment(ev); comment != "" {
		s.addComment(comment)
	}
}

func (s *bashLogState) addComment(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(text) > 300 {
		text = text[:300]
	}
	s.commands = append(s.commands, bashLogCommand{Comment: text})
}

func (s *bashLogState) addCommand(id, command string, exitCode *int, output string) {
	command = normalizeBashCommand(command)
	if command == "" {
		return
	}

	entry := bashLogCommand{Command: command}
	if exitCode != nil {
		entry.ExitCode = *exitCode
	}
	if output != "" {
		entry.OutputPreview = truncateText(output, 500)
	}

	index := len(s.commands)
	s.commands = append(s.commands, entry)
	if id != "" {
		s.pending[id] = index
	}
}

func (s *bashLogState) updateFromResult(ev map[string]any) {
	id := firstString(ev, "tool_use_id", "toolUseId", "id", "call_id")
	if part, ok := ev["part"].(map[string]any); ok && id == "" {
		id = firstString(part, "tool_use_id", "toolUseId", "id", "call_id")
	}

	exitCode, hasExitCode := extractIntField(ev, "exit_code")
	if !hasExitCode {
		if part, ok := ev["part"].(map[string]any); ok {
			exitCode, hasExitCode = extractIntField(part, "exit_code")
		}
	}

	output := extractBashOutput(ev)
	if output == "" {
		if part, ok := ev["part"].(map[string]any); ok {
			output = extractBashOutput(part)
		}
	}

	index := -1
	if id != "" {
		if pendingIndex, ok := s.pending[id]; ok {
			index = pendingIndex
		}
	}
	if index < 0 {
		for i := len(s.commands) - 1; i >= 0; i-- {
			if s.commands[i].Command != "" {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return
	}

	if hasExitCode {
		s.commands[index].ExitCode = exitCode
	}
	if output != "" {
		s.commands[index].OutputPreview = truncateText(output, 500)
	}
}

func isBashResultEvent(ev map[string]any) bool {
	eventType := strings.ToLower(firstString(ev, "type", "event"))
	if strings.Contains(eventType, "tool_result") || strings.Contains(eventType, "command_execution_result") {
		return true
	}
	if part, ok := ev["part"].(map[string]any); ok {
		partType := strings.ToLower(firstString(part, "type"))
		return strings.Contains(partType, "tool_result") || strings.Contains(partType, "command_execution_result")
	}
	return false
}

func extractBashCommand(ev map[string]any) (command, id string, exitCode *int, output string, ok bool) {
	for _, candidate := range bashLogCandidateMaps(ev) {
		if candidate == nil {
			continue
		}

		if id == "" {
			id = firstString(candidate, "tool_use_id", "toolUseId", "id", "call_id")
		}

		if command == "" {
			command = extractCommandString(candidate)
		}
		if command == "" {
			continue
		}

		if code, hasCode := extractIntField(candidate, "exit_code"); hasCode {
			exitCode = &code
		}
		if output == "" {
			output = extractBashOutput(candidate)
		}
		ok = true
		break
	}
	return
}

func bashLogCandidateMaps(ev map[string]any) []map[string]any {
	maps := []map[string]any{ev}
	for _, key := range []string{"item", "part", "message", "completion", "result"} {
		if nested, ok := ev[key].(map[string]any); ok {
			maps = append(maps, nested)
		}
	}
	return maps
}

func extractCommandString(m map[string]any) string {
	if command := firstString(m, "command", "cmd", "shell", "script"); command != "" {
		return command
	}

	if input, ok := m["input"].(map[string]any); ok {
		if command := firstString(input, "command", "cmd", "shell", "script", "text"); command != "" {
			return command
		}
		if args, ok := input["args"].([]any); ok {
			var parts []string
			for _, arg := range args {
				if text, ok := arg.(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, " ")
			}
		}
	}

	if args, ok := m["args"].([]any); ok {
		var parts []string
		for _, arg := range args {
			if text, ok := arg.(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}

	return ""
}

func extractBashComment(ev map[string]any) string {
	for _, candidate := range bashLogCandidateMaps(ev) {
		if candidate == nil {
			continue
		}

		if strings.ToLower(firstString(candidate, "type")) == "agent_message" {
			if text := extractText(candidate["text"], candidate["content"], candidate["message"]); text != "" {
				return text
			}
		}

		if role := strings.ToLower(firstString(candidate, "role")); role == "assistant" {
			if text := extractText(candidate["content"], candidate["text"], candidate["message"]); text != "" {
				return text
			}
		}

		if strings.Contains(strings.ToLower(firstString(candidate, "type")), "assistant") {
			if text := extractText(candidate["content"], candidate["text"], candidate["message"]); text != "" {
				return text
			}
		}

		if strings.ToLower(firstString(candidate, "type")) == "text" {
			if text := extractText(candidate["part"], candidate["text"], candidate["content"]); text != "" {
				return text
			}
		}

		if text := extractText(candidate["finalText"], candidate["final_text"], candidate["result"]); text != "" {
			if strings.ToLower(firstString(candidate, "type")) == "completion" {
				return text
			}
		}
	}

	return ""
}

func extractBashOutput(m map[string]any) string {
	if output := extractText(
		m["aggregated_output"],
		m["stdout"],
		m["output"],
		m["content"],
		m["text"],
	); output != "" {
		return output
	}

	if result, ok := m["result"].(map[string]any); ok {
		if output := extractText(result["content"], result["stdout"], result["output"], result["text"]); output != "" {
			return output
		}
	}

	if part, ok := m["part"].(map[string]any); ok {
		if output := extractText(part["content"], part["stdout"], part["output"], part["text"]); output != "" {
			return output
		}
	}

	return ""
}

func extractText(values ...any) string {
	var parts []string
	for _, value := range values {
		text := extractTextValue(value)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractTextValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"text", "content", "message", "finalText", "final_text", "stdout", "output", "result"} {
			if text := extractTextValue(typed[key]); text != "" {
				return text
			}
		}
		return ""
	case []any:
		var parts []string
		for _, item := range typed {
			if text := extractTextValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func intValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		var parsed int64
		_, err := fmt.Sscan(strings.TrimSpace(typed), &parsed)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func extractIntField(m map[string]any, key string) (int, bool) {
	value, ok := intValue(m[key])
	return int(value), ok
}

func normalizeBashCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	for _, prefix := range []string{"/bin/zsh -lc ", "/bin/bash -lc ", "zsh -lc ", "bash -lc "} {
		if strings.HasPrefix(command, prefix) {
			command = strings.TrimSpace(command[len(prefix):])
			break
		}
	}

	if len(command) >= 2 {
		if (strings.HasPrefix(command, "'") && strings.HasSuffix(command, "'")) ||
			(strings.HasPrefix(command, "\"") && strings.HasSuffix(command, "\"")) {
			command = command[1 : len(command)-1]
		}
	}

	return strings.TrimSpace(command)
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

// handleJobCompletion dispatches an attestation run after a worker completes.
// The attestor independently reviews: did files change? do changes match the
// objective? did the worker report verification results? does it build?
// The attestor marks the work item done (pass) or failed (retry with context).
//
// Three-layer model:
//   - Verification: workers verify their own work during their run (tests, build checks)
//     and report findings as notes on the work item.
//   - Attestation: an independent agent reviews after the worker exits. Checks the diff,
//     reads worker findings, runs its own checks, and gates the state transition.
//   - Retry: if attestation fails, the failure reason feeds into the next worker's briefing.
func handleJobCompletion(ctx context.Context, svc *service.Service, selfBin, configPath, cwd, workID string, flight *inFlightJob, defaultAdapter string) {
	workResult, err := svc.Work(ctx, workID)
	if err != nil {
		_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
			WorkID:         workID,
			ExecutionState: core.WorkExecutionStateFailed,
			Message:        fmt.Sprintf("attestation: could not fetch work state: %v", err),
			CreatedBy:      "attestation",
		})
		return
	}

	work := workResult.Work

	// Generate attestation nonce — this didn't exist while the worker was running,
	// so the worker cannot have used it. The attestor receives it in the prompt.
	nonce := core.GenerateID("nonce")
	if work.Metadata == nil {
		work.Metadata = map[string]any{}
	}
	work.Metadata["attestation_nonce"] = nonce
	_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
		WorkID:    workID,
		Metadata:  work.Metadata,
		CreatedBy: "attestation",
	})

	// Collect worker's verification findings (notes) for the attestor
	var workerFindings string
	for _, note := range workResult.Notes {
		if note.NoteType == "finding" || note.NoteType == "verification" {
			workerFindings += fmt.Sprintf("- [%s] %s\n", note.NoteType, note.Body)
		}
	}
	if workerFindings == "" {
		workerFindings = "(worker reported no verification findings)"
	}

	attestPrompt := fmt.Sprintf(`You are an attestation agent. A worker just finished work item %s.
Your job is to independently verify the work was done correctly.

## Work item
Title: %s
Objective: %s
Worker adapter: %s
Worker job: %s

## Worker's verification findings
%s

## Attestation procedure
1. Run: git diff --stat
2. If NO files changed (only .cagent/cagent.db or nothing):
   The worker failed silently. Attest failure:
   cagent work attest %s --nonce %s --result failed --summary "no code changes produced by worker" --verifier-kind attestation --method automated_review
   Stop.

3. If files changed, review the diff:
   - Run: git diff
   - Do the changes address the objective?
   - Check build: run the appropriate build command (go build ./... or similar)
   - Check for obvious errors or regressions

4. If the work is correct and complete:
   cagent work attest %s --nonce %s --result passed --summary "N files changed, builds clean, changes match objective" --verifier-kind attestation --method automated_review
   Stop.

5. If the work is incorrect or incomplete:
   cagent work attest %s --nonce %s --result failed --summary "<specific reason>" --verifier-kind attestation --method automated_review
   Stop.

IMPORTANT: You MUST run exactly one cagent work attest command. This is the attestation contract —
the attest command atomically records your finding AND transitions the work item state.
Do not use cagent work complete or cagent work fail. Only cagent work attest with the nonce provided above.
Be thorough but concise.`,
		workID, work.Title, work.Objective, flight.adapter, flight.jobID,
		workerFindings,
		workID, nonce, workID, nonce, workID, nonce)

	// Dispatch attestation via a different model than the worker (rotation offset +1).
	attestAdapter, attestModel := attestAdapterModel(flight.adapter)

	args := []string{"run", "--json", "--adapter", attestAdapter, "--cwd", cwd,
		"--model", attestModel, "--work", workID, "--prompt", attestPrompt}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	runCmd := exec.Command(selfBin, args...)
	runCmd.Dir = cwd
	runCmd.Stderr = nil
	runCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, spawnErr := runCmd.Output()

	if spawnErr != nil {
		_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
			WorkID:         workID,
			ExecutionState: core.WorkExecutionStateFailed,
			Message:        fmt.Sprintf("attestation: dispatch failed: %v", spawnErr),
			CreatedBy:      "attestation",
		})
		return
	}

	// Extract attestation job ID for tracking
	var result struct {
		Job struct {
			JobID string `json:"job_id"`
		} `json:"job"`
	}
	attestJobID := "(unknown)"
	if json.Unmarshal(out, &result) == nil && result.Job.JobID != "" {
		attestJobID = result.Job.JobID
	}

	// Don't mark done — the attestor will do it via cagent work attest
	_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
		WorkID:    workID,
		Message:   fmt.Sprintf("attestation: dispatched %s via %s/%s", attestJobID, attestAdapter, attestModel),
		CreatedBy: "attestation",
	})
}

// runInProcessSupervisor runs the autonomous dispatch loop using the shared Service instance.
// Only active when --auto is set.
func runInProcessSupervisor(ctx context.Context, svc *service.Service, cwd string, root *rootOptions, maxConcurrent int, defaultAdapter string) {
	selfBin, err := os.Executable()
	if err != nil {
		selfBin = "cagent"
	}

	var mu sync.Mutex
	inFlight := make(map[string]*inFlightJob)
	cycle := 0
	leaseDuration := 30 * time.Minute
	leaseRenewInterval := 10 * time.Minute

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: cancel in-flight jobs
			mu.Lock()
			for workID, flight := range inFlight {
				_, _ = svc.Cancel(ctx, flight.jobID)
				_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
					WorkID:         workID,
					ExecutionState: core.WorkExecutionStateFailed,
					Message:        "supervisor: cancelled during shutdown",
					CreatedBy:      "supervisor",
				})
			}
			mu.Unlock()
			return
		default:
		}

		cycle++

		// Auto-init
		if cycle == 1 {
			readyWork, _ := svc.ReadyWork(ctx, 1, false)
			if len(readyWork) == 0 {
				_ = bootstrapRepo(ctx, svc, cwd)
			}
		}

		// Reconcile
		_, _ = svc.ReconcileOnStartup(ctx)

		// Check in-flight
		mu.Lock()
		for workID, flight := range inFlight {
			statusResult, err := svc.Status(ctx, flight.jobID)
			if err != nil {
				continue
			}
			jobState := string(statusResult.Job.State)

			if isTerminal(jobState) {
				if jobState == "failed" || jobState == "cancelled" {
					_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
						WorkID:         workID,
						ExecutionState: core.WorkExecutionStateFailed,
						Message:        fmt.Sprintf("supervisor: job %s %s", flight.jobID, jobState),
						CreatedBy:      "supervisor",
					})
				} else {
					handleJobCompletion(ctx, svc, selfBin, root.configPath, cwd, workID, flight, defaultAdapter)
				}
				delete(inFlight, workID)
			} else if isJobStalled(filepath.Join(cwd, ".cagent", "raw", "stdout", flight.jobID), 10*time.Minute) {
				_, _ = svc.UpdateWork(ctx, service.WorkUpdateRequest{
					WorkID:         workID,
					ExecutionState: core.WorkExecutionStateFailed,
					Message:        fmt.Sprintf("supervisor: job %s stalled (no output for 10m)", flight.jobID),
					CreatedBy:      "supervisor",
				})
				delete(inFlight, workID)
			} else if time.Now().After(flight.leaseNext) {
				_, _ = svc.RenewWorkLease(ctx, service.WorkRenewLeaseRequest{
					WorkID:        workID,
					Claimant:      "supervisor",
					LeaseDuration: leaseDuration,
				})
				flight.leaseNext = time.Now().Add(leaseRenewInterval)
			}
		}
		inFlightCount := len(inFlight)
		mu.Unlock()

		// Dispatch
		if inFlightCount < maxConcurrent {
			readyItems, _ := svc.ReadyWork(ctx, maxConcurrent*2, false)
			for _, item := range readyItems {
				mu.Lock()
				if len(inFlight) >= maxConcurrent {
					mu.Unlock()
					break
				}
				if _, tracked := inFlight[item.WorkID]; tracked {
					mu.Unlock()
					continue
				}
				mu.Unlock()

				// Look up job history to inform rotation-based adapter selection.
				var jobHistory []core.JobRecord
				if workDetail, wErr := svc.Work(ctx, item.WorkID); wErr == nil {
					jobHistory = workDetail.Jobs
				}
				adapter, model := pickAdapterModel(item, jobHistory, defaultAdapter)

				claimed, err := svc.ClaimWork(ctx, service.WorkClaimRequest{
					WorkID:        item.WorkID,
					Claimant:      "supervisor",
					LeaseDuration: leaseDuration,
				})
				if err != nil {
					continue
				}

				briefing, err := svc.HydrateWork(ctx, service.WorkHydrateRequest{
					WorkID:   claimed.WorkID,
					Mode:     "standard",
					Claimant: "supervisor",
				})
				if err != nil {
					_, _ = svc.ReleaseWork(ctx, service.WorkReleaseRequest{
						WorkID:   claimed.WorkID,
						Claimant: "supervisor",
					})
					continue
				}

				briefingJSON, _ := json.Marshal(briefing)
				jobID, err := spawnRun(selfBin, root.configPath, adapter, model, cwd, string(briefingJSON))
				if err != nil {
					_, _ = svc.ReleaseWork(ctx, service.WorkReleaseRequest{
						WorkID:   claimed.WorkID,
						Claimant: "supervisor",
					})
					continue
				}

				mu.Lock()
				inFlight[claimed.WorkID] = &inFlightJob{
					workID:    claimed.WorkID,
					jobID:     jobID,
					adapter:   adapter,
					model:     model,
					started:   time.Now(),
					leaseNext: time.Now().Add(leaseRenewInterval),
				}
				mu.Unlock()
			}
		}

		// Write state file
		writeSupState(cwd, cycle, inFlight, supervisorCycleReport{
			Cycle:     cycle,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			InFlight:  len(inFlight),
			Ready:     0,
		})

		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}
