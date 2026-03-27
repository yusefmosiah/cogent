package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yusefmosiah/cogent/internal/adapterapi"
	"github.com/yusefmosiah/cogent/internal/adapters"
	"github.com/yusefmosiah/cogent/internal/channelmeta"
	"github.com/yusefmosiah/cogent/internal/core"
	"github.com/yusefmosiah/cogent/internal/events"
	"github.com/yusefmosiah/cogent/internal/store"
)

func (s *Service) cancelReleaseWorkClaim(ctx context.Context, jobID, workID string) {
	if workID == "" {
		return
	}
	work, err := s.store.GetWorkItem(ctx, workID)
	if err != nil {
		return
	}
	if work.ClaimedBy == "" && work.ClaimedUntil == nil {
		return
	}
	now := time.Now().UTC()
	leaseExpired := work.ClaimedUntil != nil && !work.ClaimedUntil.After(now)
	workState := core.WorkExecutionStateFailed
	if leaseExpired || work.ClaimedBy == "" {
		workState = core.WorkExecutionStateReady
	}
	_, _ = s.UpdateWork(ctx, WorkUpdateRequest{
		WorkID:         workID,
		ExecutionState: workState,
		Message:        fmt.Sprintf("cancelled: job %s", jobID),
		CreatedBy:      "cancel",
	})
}

func (s *Service) Cancel(ctx context.Context, jobID string) (*core.JobRecord, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, normalizeStoreError("job", jobID, err)
	}

	if job.State.Terminal() {
		s.cancelReleaseWorkClaim(ctx, jobID, job.WorkID)
		return &job, nil
	}

	now := time.Now().UTC()
	if err := s.upsertJobRuntime(ctx, job.JobID, func(rec *core.JobRuntimeRecord) {
		rec.CancelRequestedAt = &now
	}); err != nil {
		return nil, err
	}

	var runtimeRec *core.JobRuntimeRecord
	waitForRuntimeUntil := time.Now().Add(5 * time.Second)
	for runtimeRec == nil && time.Now().Before(waitForRuntimeUntil) {
		rec, runtimeErr := s.store.GetJobRuntime(ctx, job.JobID)
		if runtimeErr == nil {
			runtimeRec = &rec
			break
		}
		if runtimeErr != nil && !errors.Is(runtimeErr, store.ErrNotFound) {
			return nil, runtimeErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	signals := []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL}
	delays := []time.Duration{1500 * time.Millisecond, 1500 * time.Millisecond, 1500 * time.Millisecond}
	for idx, sig := range signals {
		if runtimeRec == nil {
			rec, runtimeErr := s.store.GetJobRuntime(ctx, job.JobID)
			if runtimeErr == nil {
				runtimeRec = &rec
			} else if runtimeErr != nil && !errors.Is(runtimeErr, store.ErrNotFound) {
				return nil, runtimeErr
			}
		}
		if runtimeRec != nil {
			if runtimeRec.VendorPID != 0 {
				_ = signalProcessGroup(runtimeRec.VendorPID, sig)
			} else if runtimeRec.SupervisorPID != 0 {
				_ = signalProcessGroup(runtimeRec.SupervisorPID, sig)
			}
		}
		waitUntil := time.Now().Add(delays[idx])
		for time.Now().Before(waitUntil) {
			current, err := s.store.GetJob(ctx, jobID)
			if err == nil && current.State.Terminal() {
				s.cancelReleaseWorkClaim(ctx, jobID, current.WorkID)
				return &current, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	current, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	s.cancelReleaseWorkClaim(ctx, jobID, current.WorkID)

	if current.State.Terminal() {
		return &current, nil
	}

	return &current, fmt.Errorf("%w: job %s did not exit after cancellation signals", ErrBusy, jobID)
}

func (s *Service) queuePreparedJob(ctx context.Context, job *core.JobRecord, turn *core.TurnRecord) (string, error) {
	if err := s.transitionJob(ctx, job, core.JobStateQueued, map[string]any{"message": "job queued for background execution"}); err != nil {
		return "", err
	}
	turn.Status = string(job.State)
	if err := s.store.UpdateTurn(ctx, *turn); err != nil {
		return "", err
	}

	pid, err := s.launchDetachedWorker(job.JobID, turn.TurnID)
	if err != nil {
		message := fmt.Sprintf("failed to launch background worker: %v", err)
		if failErr := s.failPreparedJobLifecycle(ctx, job, turn, message); failErr != nil {
			return "", failErr
		}
		return message, fmt.Errorf("%w: %s", ErrBusy, message)
	}
	if err := s.upsertJobRuntime(ctx, job.JobID, func(rec *core.JobRuntimeRecord) {
		rec.Detached = true
		rec.SupervisorPID = pid
	}); err != nil {
		return "", err
	}

	message := fmt.Sprintf("job launched as background worker pid %d", pid)
	job.Summary["message"] = message
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return "", err
	}
	return message, nil
}

func (s *Service) prepareJobLifecycle(
	ctx context.Context,
	job *core.JobRecord,
	turn *core.TurnRecord,
	opts startExecutionOptions,
) error {
	if err := s.store.CreateTurn(ctx, *turn); err != nil {
		return err
	}

	if _, err := s.emitEvent(ctx, *job, "job.created", "lifecycle", map[string]any{
		"cwd":           job.CWD,
		"label":         job.Label,
		"prompt_source": opts.PromptSource,
		"continued":     opts.Continue,
	}, "", nil); err != nil {
		return err
	}

	rawPrompt, _ := json.Marshal(map[string]any{
		"prompt":    opts.Prompt,
		"source":    opts.PromptSource,
		"continued": opts.Continue,
	})
	if _, err := s.emitEvent(ctx, *job, "user.message", "input", map[string]any{
		"text":   opts.Prompt,
		"source": opts.PromptSource,
	}, "native", rawPrompt); err != nil {
		return err
	}

	return nil
}

func (s *Service) startPreparedJobLifecycle(
	ctx context.Context,
	adapter adapterapi.Adapter,
	descriptor adapters.Diagnosis,
	job *core.JobRecord,
	turn *core.TurnRecord,
	opts startExecutionOptions,
) (string, error) {
	if err := s.transitionJob(ctx, job, core.JobStateStarting, map[string]any{"message": "job starting"}); err != nil {
		return "", err
	}
	turn.Status = string(job.State)
	if err := s.store.UpdateTurn(ctx, *turn); err != nil {
		return "", err
	}

	var (
		message string
		runErr  error
	)
	cancelRequested := false
	switch {
	case !descriptor.Available:
		message = fmt.Sprintf("adapter %q binary %q is not available on PATH", job.Adapter, descriptor.Binary)
		runErr = fmt.Errorf("%w: %s", ErrAdapterUnavailable, message)
	case !descriptor.Implemented:
		message = fmt.Sprintf("adapter %q is detected but not implemented in this build yet", job.Adapter)
		runErr = fmt.Errorf("%w: %s", ErrUnsupported, message)
	default:
		message, runErr = s.executeAdapter(ctx, adapter, job, opts)
	}
	cancelRequested = s.isCancelRequested(ctx, job.JobID)
	if runErr != nil && !cancelRequested {
		if _, err := s.emitEvent(ctx, *job, "diagnostic", "translation", map[string]any{
			"message": message,
		}, "", nil); err != nil {
			return message, err
		}
		if _, err := s.emitEvent(ctx, *job, "process.stderr", "execution", map[string]any{
			"message": message,
		}, "stderr", []byte(message+"\n")); err != nil {
			return message, err
		}
	}

	job.Summary["message"] = message
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return message, err
	}

	terminalState := core.JobStateCompleted
	terminalEvent := "job.completed"
	if cancelRequested {
		terminalState = core.JobStateCancelled
		terminalEvent = "job.cancelled"
		if message == "" {
			message = "job cancelled"
		}
	} else if runErr != nil {
		terminalState = core.JobStateFailed
		terminalEvent = "job.failed"
	}
	if err := s.finishJob(ctx, job, terminalState); err != nil {
		return message, err
	}
	if terminalState == core.JobStateCompleted {
		if err := s.persistDebrief(ctx, job, message); err != nil {
			return message, err
		}
	}
	if _, err := s.emitEvent(ctx, *job, terminalEvent, "lifecycle", map[string]any{
		"message": message,
	}, "", nil); err != nil {
		return message, err
	}

	turn.CompletedAt = job.FinishedAt
	turn.ResultSummary = message
	turn.Status = string(job.State)
	turn.NativeSessionID = job.NativeSessionID
	if err := s.store.UpdateTurn(ctx, *turn); err != nil {
		return message, err
	}
	if err := s.upsertJobRuntime(ctx, job.JobID, func(rec *core.JobRuntimeRecord) {
		completedAt := time.Now().UTC()
		rec.VendorPID = 0
		rec.CompletedAt = &completedAt
	}); err != nil {
		return message, err
	}

	// Report job completion to supervisor/host via channel notification.
	// This ensures the dispatch loop always gets notified, even if the
	// worker LLM didn't call report itself.
	s.reportJobCompletion(*job, terminalEvent, message)

	return message, runErr
}

// reportJobCompletion sends a channel notification about job completion.
// Uses serve's HTTP API to reach the host via the MCP proxy channel.
// Best-effort: failures are silently dropped since the host can still
// observe job state via polling.
func (s *Service) reportJobCompletion(job core.JobRecord, terminalEvent, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reportMsg := fmt.Sprintf("[job %s] %s: %s (work: %s)", job.JobID, terminalEvent, message, job.WorkID)
	if job.WorkID != "" {
		reportMsg = reportMsg + " | proof: " + formatProofBundleRefs(s.notificationProofBundle(ctx, core.WorkItemRecord{WorkID: job.WorkID}))
	}
	_ = s.postServeChannelEvent(ctx, reportMsg, channelmeta.JobCompletionMeta())
}

func (s *Service) postServeChannelEvent(ctx context.Context, message string, meta map[string]string) error {
	serveJSONPath := filepath.Join(s.Paths.StateDir, "serve.json")
	data, err := os.ReadFile(serveJSONPath)
	if err != nil {
		return err
	}
	var info struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	if info.Port == 0 {
		return fmt.Errorf("serve.json missing port")
	}

	body, err := json.Marshal(map[string]any{
		"content": message,
		"meta":    meta,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://localhost:%d/api/channel/send", info.Port), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("channel send failed: %s", strings.TrimSpace(string(limited)))
	}
	return nil
}

func (s *Service) failPreparedJobLifecycle(ctx context.Context, job *core.JobRecord, turn *core.TurnRecord, message string) error {
	job.Summary["message"] = message
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	if _, err := s.emitEvent(ctx, *job, "diagnostic", "execution", map[string]any{
		"message": message,
	}, "", nil); err != nil {
		return err
	}
	if err := s.finishJob(ctx, job, core.JobStateFailed); err != nil {
		return err
	}
	if _, err := s.emitEvent(ctx, *job, "job.failed", "lifecycle", map[string]any{
		"message": message,
	}, "", nil); err != nil {
		return err
	}
	turn.CompletedAt = job.FinishedAt
	turn.ResultSummary = message
	turn.Status = string(job.State)
	return s.store.UpdateTurn(ctx, *turn)
}

func (s *Service) executeAdapter(
	ctx context.Context,
	adapter adapterapi.Adapter,
	job *core.JobRecord,
	opts startExecutionOptions,
) (string, error) {
	var (
		handle *adapterapi.RunHandle
		err    error
	)

	switch {
	case opts.Continue:
		handle, err = adapter.ContinueRun(ctx, adapterapi.ContinueRunRequest{
			CanonicalSessionID: job.SessionID,
			CWD:                job.CWD,
			Prompt:             opts.Prompt,
			Model:              opts.Model,
			Profile:            opts.Profile,
			NativeSessionID:    opts.NativeSessionID,
			NativeSessionMeta:  opts.NativeSessionMeta,
		})
	default:
		handle, err = adapter.StartRun(ctx, adapterapi.StartRunRequest{
			CanonicalSessionID: job.SessionID,
			CWD:                job.CWD,
			Prompt:             opts.Prompt,
			Model:              opts.Model,
			Profile:            opts.Profile,
		})
	}
	if err != nil {
		return err.Error(), err
	}
	defer func() {
		if handle.Cleanup != nil {
			_ = handle.Cleanup()
		}
	}()

	if err := s.upsertJobRuntime(ctx, job.JobID, func(rec *core.JobRuntimeRecord) {
		rec.Detached = true
		rec.SupervisorPID = os.Getpid()
		rec.VendorPID = handle.Cmd.Process.Pid
	}); err != nil {
		return "", err
	}

	if _, err := s.emitEvent(ctx, *job, "process.spawned", "execution", map[string]any{
		"argv": handle.Cmd.Args,
		"pid":  handle.Cmd.Process.Pid,
	}, "", nil); err != nil {
		return "", err
	}
	if _, err := s.emitEvent(ctx, *job, "job.started", "lifecycle", map[string]any{
		"message": "job entered running state",
	}, "", nil); err != nil {
		return "", err
	}
	if err := s.transitionJob(ctx, job, core.JobStateRunning, map[string]any{"message": "job running"}); err != nil {
		return "", err
	}

	lineCh := make(chan lineItem, 64)
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go s.scanStream(handle.Stdout, "stdout", lineCh, errCh, &wg)
	go s.scanStream(handle.Stderr, "stderr", lineCh, errCh, &wg)
	go func() {
		wg.Wait()
		close(lineCh)
		close(errCh)
	}()

	var lastAssistant string
	var translatedError string
	for item := range lineCh {
		if _, err := s.emitEvent(ctx, *job, "process."+item.stream, "execution", map[string]any{
			"line": item.line,
		}, item.stream, []byte(item.line+"\n")); err != nil {
			return lastAssistant, err
		}

		hints := events.TranslateLine(job.Adapter, item.stream, item.line)
		for _, hint := range hints {
			emitHint := true
			if hint.NativeSessionID != "" {
				if job.NativeSessionID == "" {
					job.NativeSessionID = hint.NativeSessionID
					if err := s.store.UpdateJob(ctx, *job); err != nil {
						return lastAssistant, err
					}
					if err := s.store.UpsertNativeSession(ctx, core.NativeSessionRecord{
						SessionID:       job.SessionID,
						Adapter:         job.Adapter,
						NativeSessionID: hint.NativeSessionID,
						Resumable:       adapter.Capabilities().NativeResume,
						Metadata:        cloneMap(handle.NativeSessionMeta),
					}); err != nil {
						return lastAssistant, err
					}
				} else if hint.Kind == "session.discovered" && job.NativeSessionID == hint.NativeSessionID {
					emitHint = false
				}
			}
			if text, ok := hint.Payload["text"].(string); ok && text != "" && hint.Kind == "assistant.message" {
				if text == lastAssistant {
					emitHint = false
				}
				lastAssistant = text
			}
			if emitHint {
				event, err := s.emitEvent(ctx, *job, hint.Kind, hint.Phase, hint.Payload, "", nil)
				if err != nil {
					return lastAssistant, err
				}
				if hint.NativeSessionID != "" {
					event.NativeSessionID = hint.NativeSessionID
				}
			}
			if hint.Kind == "usage.reported" {
				if err := s.applyUsageHint(ctx, job, hint.Payload); err != nil {
					return lastAssistant, err
				}
			}
			if hint.Kind == "diagnostic" && translatedError == "" {
				translatedError = diagnosticMessage(hint.Payload)
			}
		}
	}

	for scanErr := range errCh {
		if scanErr != nil {
			return lastAssistant, scanErr
		}
	}

	waitErr := handle.Cmd.Wait()
	if lastMessage, err := s.readLastMessage(handle.LastMessagePath); err == nil && lastMessage != "" && lastMessage != lastAssistant {
		if _, emitErr := s.emitEvent(ctx, *job, "assistant.message", "translation", map[string]any{
			"text":   lastMessage,
			"source": "last_message_file",
		}, "", nil); emitErr != nil {
			return lastAssistant, emitErr
		}
		lastAssistant = lastMessage
	}

	if waitErr != nil {
		if _, err := s.emitEvent(ctx, *job, "diagnostic", "execution", map[string]any{
			"message": waitErr.Error(),
		}, "", nil); err != nil {
			return lastAssistant, err
		}
		return lastAssistant, fmt.Errorf("%w: %v", ErrVendorProcess, waitErr)
	}
	if translatedError != "" && lastAssistant == "" {
		return translatedError, fmt.Errorf("%w: %s", ErrVendorProcess, translatedError)
	}

	if lastAssistant == "" {
		lastAssistant = "adapter completed without a translated assistant message"
	}

	return lastAssistant, nil
}

func (s *Service) ExecuteDetachedJob(ctx context.Context, jobID, turnID string) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return normalizeStoreError("job", jobID, err)
	}
	turn, err := s.store.GetTurn(ctx, turnID)
	if err != nil {
		return normalizeStoreError("turn", turnID, err)
	}
	defer s.releaseContinuationLock(context.Background(), job)

	adapter, descriptor, err := s.resolveAdapter(ctx, job.Adapter)
	if err != nil {
		return err
	}

	if err := s.upsertJobRuntime(ctx, job.JobID, func(rec *core.JobRuntimeRecord) {
		rec.Detached = true
		rec.SupervisorPID = os.Getpid()
	}); err != nil {
		return err
	}

	opts, err := s.executionOptionsForJob(ctx, job, turn)
	if err != nil {
		return err
	}

	_, runErr := s.startPreparedJobLifecycle(ctx, adapter, descriptor, &job, &turn, opts)
	return runErr
}

func (s *Service) launchDetachedWorker(jobID, turnID string) (int, error) {
	exePath, err := detachedExecutablePath()
	if err != nil {
		return 0, err
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	args := []string{
		"--config", s.ConfigPath,
		"__run-job",
		"--job", jobID,
		"--turn", turnID,
	}
	cmd := exec.Command(exePath, args...)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Stdin = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = s.detachedWorkerEnv(exePath)
	adapterapi.PrepareCommand(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start detached worker: %w", err)
	}

	return cmd.Process.Pid, nil
}

func (s *Service) detachedWorkerEnv(exePath string) []string {
	envMap := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}

	envMap["COGENT_EXECUTABLE"] = exePath
	if s.ConfigPath != "" {
		envMap["COGENT_CONFIG_DIR"] = filepath.Dir(s.ConfigPath)
	}
	if s.Paths.StateDir != "" {
		envMap["COGENT_STATE_DIR"] = s.Paths.StateDir
	}
	if s.Paths.CacheDir != "" {
		envMap["COGENT_CACHE_DIR"] = s.Paths.CacheDir
	}
	if exeDir := filepath.Dir(exePath); exeDir != "" {
		if pathValue, ok := envMap["PATH"]; ok && pathValue != "" {
			envMap["PATH"] = exeDir + string(os.PathListSeparator) + pathValue
		} else {
			envMap["PATH"] = exeDir
		}
	}

	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func (s *Service) releaseContinuationLock(ctx context.Context, job core.JobRecord) {
	continued, _ := job.Summary["continued"].(bool)
	if !continued || job.NativeSessionID == "" {
		return
	}
	_ = s.store.ReleaseLock(ctx, lockKey(job.Adapter, job.NativeSessionID), job.JobID)
}

func (s *Service) queueContinuation(
	ctx context.Context,
	session core.SessionRecord,
	target core.NativeSessionRecord,
	req continuationRequest,
) (*RunResult, error) {
	now := time.Now().UTC()
	job := core.JobRecord{
		JobID:           core.GenerateID("job"),
		SessionID:       session.SessionID,
		WorkID:          req.WorkID,
		Adapter:         target.Adapter,
		State:           core.JobStateCreated,
		CWD:             session.CWD,
		CreatedAt:       now,
		UpdatedAt:       now,
		NativeSessionID: target.NativeSessionID,
		Summary:         cloneMap(req.Summary),
	}
	if job.Summary == nil {
		job.Summary = map[string]any{}
	}
	if req.PromptSource != "" {
		job.Summary["prompt_source"] = req.PromptSource
	}
	if req.Model != "" {
		job.Summary["model"] = req.Model
	}
	if req.Profile != "" {
		job.Summary["profile"] = req.Profile
	}
	if debriefRequested, _ := job.Summary["debrief"].(bool); debriefRequested {
		path, err := s.resolveDebriefOutputPath(summaryString(job.Summary, "debrief_path"), session.SessionID, job.JobID)
		if err != nil {
			return nil, err
		}
		job.Summary["debrief_path"] = path
	}
	if err := s.store.CreateJobAndUpdateSession(ctx, session.SessionID, now, job); err != nil {
		return nil, err
	}
	session.LatestJobID = job.JobID
	session.UpdatedAt = now
	if req.WorkID != "" {
		if err := s.markWorkQueued(ctx, req.WorkID, &job, session); err != nil {
			return nil, err
		}
	}

	lock := core.LockRecord{
		LockKey:         lockKey(target.Adapter, target.NativeSessionID),
		Adapter:         target.Adapter,
		NativeSessionID: target.NativeSessionID,
		JobID:           job.JobID,
		AcquiredAt:      now,
	}
	if err := s.store.AcquireLock(ctx, lock); err != nil {
		message := fmt.Sprintf("native session %s is already in use", target.NativeSessionID)
		job.Summary["message"] = message
		_ = s.finishJob(ctx, &job, core.JobStateBlocked)
		return &RunResult{
			Job:     job,
			Session: session,
			Message: message,
		}, fmt.Errorf("%w: %s", ErrSessionLocked, message)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = s.store.ReleaseLock(context.Background(), lock.LockKey, lock.JobID)
		}
	}()

	turn := core.TurnRecord{
		TurnID:          core.GenerateID("turn"),
		SessionID:       session.SessionID,
		JobID:           job.JobID,
		Adapter:         job.Adapter,
		StartedAt:       now,
		InputText:       req.Prompt,
		InputSource:     req.PromptSource,
		Status:          string(core.JobStateCreated),
		NativeSessionID: target.NativeSessionID,
		Stats:           map[string]any{},
	}

	if err := s.prepareJobLifecycle(ctx, &job, &turn, startExecutionOptions{
		Prompt:            req.Prompt,
		PromptSource:      req.PromptSource,
		Model:             req.Model,
		Profile:           req.Profile,
		Continue:          true,
		NativeSessionID:   target.NativeSessionID,
		NativeSessionMeta: target.Metadata,
	}); err != nil {
		return nil, err
	}
	message, runErr := s.queuePreparedJob(ctx, &job, &turn)
	if runErr == nil {
		lockHeld = false
	}

	return &RunResult{
		Job:     job,
		Session: session,
		Message: message,
	}, runErr
}

func (s *Service) executionOptionsForJob(ctx context.Context, job core.JobRecord, turn core.TurnRecord) (startExecutionOptions, error) {
	opts := startExecutionOptions{
		Prompt:       turn.InputText,
		PromptSource: turn.InputSource,
		Model:        summaryString(job.Summary, "model"),
		Profile:      summaryString(job.Summary, "profile"),
	}

	continued, _ := job.Summary["continued"].(bool)
	if !continued {
		return opts, nil
	}

	opts.Continue = true
	opts.NativeSessionID = job.NativeSessionID
	if job.NativeSessionID == "" {
		return opts, nil
	}

	metadata, err := s.nativeSessionMetadata(ctx, job.SessionID, job.Adapter, job.NativeSessionID)
	if err != nil {
		return opts, err
	}
	opts.NativeSessionMeta = metadata
	return opts, nil
}

func (s *Service) nativeSessionMetadata(ctx context.Context, sessionID, adapter, nativeSessionID string) (map[string]any, error) {
	if sessionID == "" || adapter == "" || nativeSessionID == "" {
		return nil, nil
	}

	nativeSessions, err := s.store.ListNativeSessions(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, native := range nativeSessions {
		if native.Adapter == adapter && native.NativeSessionID == nativeSessionID {
			return cloneMap(native.Metadata), nil
		}
	}
	return nil, nil
}

func (s *Service) resolveDebriefOutputPath(outputPath, sessionID, jobID string) (string, error) {
	path := strings.TrimSpace(outputPath)
	if path == "" {
		name := "latest.md"
		if jobID != "" {
			name = jobID + ".md"
		}
		path = filepath.Join(s.Paths.DebriefsDir, sessionID, name)
	} else {
		expanded, err := core.ExpandPath(path)
		if err != nil {
			return "", fmt.Errorf("%w: expand debrief output path: %v", ErrInvalidInput, err)
		}
		path = expanded
	}

	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("%w: resolve debrief output path: %v", ErrInvalidInput, err)
		}
		path = absolute
	}

	return path, nil
}

func (s *Service) persistDebrief(ctx context.Context, job *core.JobRecord, message string) error {
	requested, _ := job.Summary["debrief"].(bool)
	if !requested {
		return nil
	}

	path, err := s.resolveDebriefOutputPath(summaryString(job.Summary, "debrief_path"), job.SessionID, job.JobID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create debrief directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(message)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write debrief: %w", err)
	}

	artifact := core.ArtifactRecord{
		ArtifactID: core.GenerateID("art"),
		JobID:      job.JobID,
		SessionID:  job.SessionID,
		Kind:       "debrief",
		Path:       path,
		CreatedAt:  time.Now().UTC(),
		Metadata: map[string]any{
			"adapter": job.Adapter,
			"format":  "markdown",
			"reason":  summaryString(job.Summary, "debrief_reason"),
		},
	}
	if err := s.store.InsertArtifact(ctx, artifact); err != nil {
		return err
	}

	job.Summary["debrief_path"] = path
	job.Summary["debrief_format"] = "markdown"
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	if _, err := s.emitEvent(ctx, *job, "debrief.exported", "debrief", map[string]any{
		"path":   path,
		"format": "markdown",
		"reason": summaryString(job.Summary, "debrief_reason"),
	}, "", nil); err != nil {
		return err
	}
	return nil
}

func detachedExecutablePath() (string, error) {
	path, err := osExecutable()
	if err == nil && path != "" {
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".test") && !strings.Contains(lower, string(filepath.Separator)+"go-build"+string(filepath.Separator)) {
			return path, nil
		}
	}
	if explicit := os.Getenv("COGENT_EXECUTABLE"); explicit != "" {
		return explicit, nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve cogent executable: %w", err)
	}
	return path, nil
}

// nativeServiceInjector is implemented by adapters that need a service reference.
type nativeServiceInjector interface {
	SetService(svc any)
}

func (s *Service) resolveAdapter(ctx context.Context, name string) (adapterapi.Adapter, adapters.Diagnosis, error) {
	adapter, descriptor, ok := adapters.Resolve(ctx, s.Config, name)
	if ok {
		// Inject service into adapters that need it (native adapter for Cogent tools).
		if injector, ok := adapter.(nativeServiceInjector); ok {
			injector.SetService(s)
		}
	}
	if !ok {
		for _, entry := range adapters.CatalogFromConfig(s.Config) {
			if entry.Adapter == name {
				if !entry.Enabled {
					return nil, entry, fmt.Errorf("%w: adapter %q is disabled in config", ErrUnsupported, name)
				}
				return nil, entry, nil
			}
		}
		return nil, adapters.Diagnosis{}, fmt.Errorf("%w: unknown adapter %q", ErrInvalidInput, name)
	}
	if !descriptor.Enabled {
		return nil, descriptor, fmt.Errorf("%w: adapter %q is disabled in config", ErrUnsupported, name)
	}
	return adapter, descriptor, nil
}

func (s *Service) resolveContinuationTarget(ctx context.Context, session core.SessionRecord, adapterName string) (core.NativeSessionRecord, error) {
	nativeSessions, err := s.store.ListNativeSessions(ctx, session.SessionID)
	if err != nil {
		return core.NativeSessionRecord{}, err
	}

	var candidates []core.NativeSessionRecord
	for _, native := range nativeSessions {
		if !native.Resumable {
			continue
		}
		if adapterName != "" && native.Adapter != adapterName {
			continue
		}
		candidates = append(candidates, native)
	}
	if len(candidates) == 0 {
		if adapterName != "" {
			return core.NativeSessionRecord{}, fmt.Errorf("%w: no resumable native session linked for adapter %q", ErrUnsupported, adapterName)
		}
		return core.NativeSessionRecord{}, fmt.Errorf("%w: session %s has no resumable native sessions", ErrUnsupported, session.SessionID)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	if session.LatestJobID != "" {
		job, err := s.store.GetJob(ctx, session.LatestJobID)
		if err == nil {
			for _, candidate := range candidates {
				if candidate.Adapter == job.Adapter && candidate.NativeSessionID == job.NativeSessionID {
					return candidate, nil
				}
			}
		}
	}

	return core.NativeSessionRecord{}, fmt.Errorf("%w: session %s has multiple resumable native sessions; specify --adapter", ErrInvalidInput, session.SessionID)
}

func lockKey(adapter, nativeSessionID string) string {
	return "native:" + adapter + ":" + nativeSessionID
}
func (s *Service) upsertJobRuntime(ctx context.Context, jobID string, mutate func(*core.JobRuntimeRecord)) error {
	now := time.Now().UTC()
	rec, err := s.store.GetJobRuntime(ctx, jobID)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		rec = core.JobRuntimeRecord{
			JobID:     jobID,
			StartedAt: now,
		}
	default:
		return err
	}

	mutate(&rec)
	if rec.StartedAt.IsZero() {
		rec.StartedAt = now
	}
	rec.UpdatedAt = now
	return s.store.UpsertJobRuntime(ctx, rec)
}

func (s *Service) isCancelRequested(ctx context.Context, jobID string) bool {
	rec, err := s.store.GetJobRuntime(ctx, jobID)
	if err != nil {
		return false
	}
	return rec.CancelRequestedAt != nil
}

func (s *Service) markWorkQueued(ctx context.Context, workID string, job *core.JobRecord, session core.SessionRecord) error {
	work, err := s.store.GetWorkItem(ctx, workID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("%w: job is required", ErrInvalidInput)
	}
	now := time.Now().UTC()
	stampJobUsageAttribution(job, work)
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	work.CurrentJobID = job.JobID
	work.CurrentSessionID = session.SessionID
	work.ExecutionState = core.WorkExecutionStateClaimed
	work.UpdatedAt = now
	if err := s.store.UpdateWorkItem(ctx, work); err != nil {
		return err
	}
	return s.store.CreateWorkUpdate(ctx, core.WorkUpdateRecord{
		UpdateID:       core.GenerateID("wup"),
		WorkID:         workID,
		ExecutionState: core.WorkExecutionStateClaimed,
		Message:        "job queued",
		JobID:          job.JobID,
		SessionID:      session.SessionID,
		CreatedBy:      "service",
		CreatedAt:      now,
		Metadata:       map[string]any{"job_state": string(job.State)},
	})
}

func (s *Service) syncWorkStateFromJob(ctx context.Context, job core.JobRecord, payload map[string]any) error {
	if job.WorkID == "" {
		return nil
	}
	work, err := s.store.GetWorkItem(ctx, job.WorkID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	now := time.Now().UTC()
	prevState := string(work.ExecutionState)
	work.CurrentJobID = job.JobID
	work.CurrentSessionID = job.SessionID
	work.UpdatedAt = now

	var (
		workState core.WorkExecutionState
		message   string
	)
	switch job.State {
	case core.JobStateQueued, core.JobStateCreated:
		workState = core.WorkExecutionStateClaimed
	case core.JobStateStarting, core.JobStateRunning, core.JobStateWaitingInput:
		workState = core.WorkExecutionStateInProgress
		if work.Kind != "attest" && work.AttestationFrozenAt == nil {
			frozenAt := now
			work.AttestationFrozenAt = &frozenAt
		}
	case core.JobStateCompleted:
		if issues, err := s.completionGateIssues(ctx, work); err != nil {
			return err
		} else if len(issues) > 0 {
			workState = core.WorkExecutionStateInProgress
		} else {
			workState = core.WorkExecutionStateDone
			if shouldSetPendingApproval(work) {
				work.ApprovalState = core.WorkApprovalStatePending
			}
		}
		work.ClaimedBy = ""
		work.ClaimedUntil = nil
	case core.JobStateFailed:
		workState = core.WorkExecutionStateFailed
		work.ClaimedBy = ""
		work.ClaimedUntil = nil
	case core.JobStateCancelled:
		workState = core.WorkExecutionStateCancelled
		work.ClaimedBy = ""
		work.ClaimedUntil = nil
	case core.JobStateBlocked:
		workState = core.WorkExecutionStateBlocked
		work.ClaimedBy = ""
		work.ClaimedUntil = nil
	default:
		workState = work.ExecutionState
	}
	workState = workState.Canonical()
	work.ExecutionState = workState
	if payload != nil {
		message = summaryString(payload, "message")
	}
	if message == "" {
		message = summaryString(job.Summary, "message")
	}
	if err := s.store.UpdateWorkItem(ctx, work); err != nil {
		return err
	}
	if err := s.store.CreateWorkUpdate(ctx, core.WorkUpdateRecord{
		UpdateID:       core.GenerateID("wup"),
		WorkID:         work.WorkID,
		ExecutionState: work.ExecutionState,
		ApprovalState:  work.ApprovalState,
		Message:        message,
		JobID:          job.JobID,
		SessionID:      job.SessionID,
		CreatedBy:      "service",
		CreatedAt:      now,
		Metadata:       map[string]any{"job_state": string(job.State)},
	}); err != nil {
		return err
	}
	// Only publish if the state actually changed — prevents stale event replay
	// when syncWorkStateFromJob is called repeatedly for the same terminal job.
	if string(work.ExecutionState) != prevState {
		actor := ActorService
		if job.Label == "supervisor" {
			actor = ActorSupervisor
		}
		s.Events.Publish(WorkEvent{
			Kind:      WorkEventUpdated,
			WorkID:    work.WorkID,
			Title:     work.Title,
			State:     string(work.ExecutionState),
			PrevState: prevState,
			JobID:     job.JobID,
			Actor:     actor,
			Cause:     CauseJobLifecycle,
		})
	}
	return nil
}

func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid == 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err := syscall.Kill(pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("signal pid %d with %s", pid, sig)
}

func (s *Service) transitionJob(ctx context.Context, job *core.JobRecord, state core.JobState, payload map[string]any) error {
	job.State = state
	job.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	if err := s.syncWorkStateFromJob(ctx, *job, payload); err != nil {
		return err
	}
	_, err := s.emitEvent(ctx, *job, "job.state_changed", "lifecycle", map[string]any{
		"state":   state,
		"message": payload["message"],
	}, "", nil)
	return err
}

func (s *Service) finishJob(ctx context.Context, job *core.JobRecord, state core.JobState) error {
	now := time.Now().UTC()
	job.State = state
	job.UpdatedAt = now
	job.FinishedAt = &now
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	if err := s.syncWorkStateFromJob(ctx, *job, job.Summary); err != nil {
		return err
	}
	return nil
}

// forceDoneWarningEvent is the structured log payload emitted to stderr when
// the force-done escape hatch is used.
type forceDoneWarningEvent struct {
	Level     string `json:"level"`
	Kind      string `json:"kind"`
	WorkID    string `json:"work_id"`
	Actor     string `json:"actor,omitempty"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

// emitForceDoneWarning writes a structured warning to stderr when --force
// bypasses guardDoneTransition. Errors are intentionally swallowed; this is
