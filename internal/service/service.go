package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yusefmosiah/cagent/internal/adapterapi"
	"github.com/yusefmosiah/cagent/internal/adapters"
	"github.com/yusefmosiah/cagent/internal/core"
	"github.com/yusefmosiah/cagent/internal/events"
	handoffpkg "github.com/yusefmosiah/cagent/internal/handoff"
	"github.com/yusefmosiah/cagent/internal/store"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrUnsupported        = errors.New("unsupported operation")
	ErrAdapterUnavailable = errors.New("adapter not available")
	ErrInvalidInput       = errors.New("invalid input")
	ErrBusy               = errors.New("resource busy")
	ErrSessionLocked      = errors.New("session locked")
	ErrVendorProcess      = errors.New("vendor process failed")
)

type Service struct {
	Paths  core.Paths
	Config core.Config
	store  *store.Store
}

type RunRequest struct {
	Adapter         string
	CWD             string
	Prompt          string
	PromptSource    string
	Label           string
	Model           string
	Profile         string
	Detached        bool
	EnvFile         string
	ArtifactDir     string
	SessionID       string
	ParentSessionID string
	HandoffID       string
}

type SendRequest struct {
	SessionID    string
	Adapter      string
	Prompt       string
	PromptSource string
	Model        string
	Profile      string
}

type RunResult struct {
	Job     core.JobRecord     `json:"job"`
	Session core.SessionRecord `json:"session"`
	Message string             `json:"message,omitempty"`
}

type HandoffExportRequest struct {
	JobID      string
	SessionID  string
	OutputPath string
}

type HandoffExportResult struct {
	Handoff core.HandoffRecord `json:"handoff"`
	Path    string             `json:"path"`
}

type HandoffRunRequest struct {
	HandoffRef string
	Adapter    string
	CWD        string
	Model      string
	Profile    string
	Label      string
}

type StatusResult struct {
	Job            core.JobRecord             `json:"job"`
	Session        core.SessionRecord         `json:"session"`
	NativeSessions []core.NativeSessionRecord `json:"native_sessions"`
	Events         []core.EventRecord         `json:"events"`
}

type SessionAction struct {
	Action          string `json:"action"`
	Adapter         string `json:"adapter"`
	NativeSessionID string `json:"native_session_id"`
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
}

type SessionResult struct {
	Session        core.SessionRecord         `json:"session"`
	NativeSessions []core.NativeSessionRecord `json:"native_sessions"`
	Turns          []core.TurnRecord          `json:"turns"`
	RecentJobs     []core.JobRecord           `json:"recent_jobs"`
	Actions        []SessionAction            `json:"actions"`
}

type RawLogEntry struct {
	Stream  string `json:"stream"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type lineItem struct {
	stream string
	line   string
}

type startExecutionOptions struct {
	Prompt            string
	PromptSource      string
	Model             string
	Profile           string
	Continue          bool
	NativeSessionID   string
	NativeSessionMeta map[string]any
}

func Open(ctx context.Context, configPath string) (*Service, error) {
	paths, err := core.ResolvePaths()
	if err != nil {
		return nil, fmt.Errorf("resolve runtime paths: %w", err)
	}

	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.Store.StateDir != "" {
		stateDir, err := core.ExpandPath(cfg.Store.StateDir)
		if err != nil {
			return nil, fmt.Errorf("expand state dir: %w", err)
		}
		paths = paths.WithStateDir(stateDir)
	}

	if err := core.EnsurePaths(paths); err != nil {
		return nil, fmt.Errorf("ensure runtime paths: %w", err)
	}

	db, err := store.Open(ctx, paths.DBPath)
	if err != nil {
		return nil, err
	}

	return &Service{
		Paths:  paths,
		Config: cfg,
		store:  db,
	}, nil
}

func (s *Service) Close() error {
	return s.store.Close()
}

func (s *Service) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	cwd, err := filepath.Abs(req.CWD)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve cwd: %v", ErrInvalidInput, err)
	}
	if stat, err := os.Stat(cwd); err != nil || !stat.IsDir() {
		return nil, fmt.Errorf("%w: cwd must be an existing directory", ErrInvalidInput)
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("%w: prompt must not be empty", ErrInvalidInput)
	}

	adapter, descriptor, err := s.resolveAdapter(ctx, req.Adapter)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	jobID := core.GenerateID("job")
	sessionID := req.SessionID
	var session core.SessionRecord

	if sessionID == "" {
		var parentSession *string
		if req.ParentSessionID != "" {
			parent := req.ParentSessionID
			parentSession = &parent
		}
		metadata := map[string]any{}
		if req.HandoffID != "" {
			metadata["source_handoff_id"] = req.HandoffID
		}
		sessionID = core.GenerateID("ses")
		session = core.SessionRecord{
			SessionID:     sessionID,
			Label:         req.Label,
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        "active",
			OriginAdapter: req.Adapter,
			OriginJobID:   jobID,
			CWD:           cwd,
			LatestJobID:   jobID,
			ParentSession: parentSession,
			Tags:          []string{},
			Metadata:      metadata,
		}
	} else {
		session, err = s.store.GetSession(ctx, sessionID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: session %s", ErrNotFound, sessionID)
			}
			return nil, err
		}
		session.LatestJobID = jobID
		session.UpdatedAt = now
	}

	job := core.JobRecord{
		JobID:     jobID,
		SessionID: sessionID,
		Adapter:   req.Adapter,
		State:     core.JobStateCreated,
		Label:     req.Label,
		CWD:       cwd,
		CreatedAt: now,
		UpdatedAt: now,
		Summary: map[string]any{
			"prompt_source": req.PromptSource,
			"detached":      req.Detached,
		},
	}
	if req.HandoffID != "" {
		job.Summary["handoff_id"] = req.HandoffID
	}
	if req.SessionID == "" {
		if err := s.store.CreateSessionAndJob(ctx, session, job); err != nil {
			return nil, err
		}
	} else {
		if err := s.store.CreateJobAndUpdateSession(ctx, sessionID, now, job); err != nil {
			return nil, err
		}
	}

	result := &RunResult{
		Job:     job,
		Session: session,
	}

	turn := core.TurnRecord{
		TurnID:      core.GenerateID("turn"),
		SessionID:   session.SessionID,
		JobID:       job.JobID,
		Adapter:     job.Adapter,
		StartedAt:   now,
		InputText:   req.Prompt,
		InputSource: req.PromptSource,
		Status:      string(core.JobStateCreated),
		Stats:       map[string]any{},
	}

	result.Message, err = s.executeJobLifecycle(ctx, adapter, descriptor, &job, &turn, startExecutionOptions{
		Prompt:       req.Prompt,
		PromptSource: req.PromptSource,
		Model:        req.Model,
		Profile:      req.Profile,
	})
	result.Job = job
	return result, err
}

func (s *Service) Send(ctx context.Context, req SendRequest) (*RunResult, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("%w: session must not be empty", ErrInvalidInput)
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("%w: prompt must not be empty", ErrInvalidInput)
	}

	session, err := s.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, normalizeStoreError("session", req.SessionID, err)
	}

	active, err := s.store.FindActiveJobBySession(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return nil, fmt.Errorf("%w: session %s already has active job %s", ErrSessionLocked, req.SessionID, active.JobID)
	}

	target, err := s.resolveContinuationTarget(ctx, session, req.Adapter)
	if err != nil {
		return nil, err
	}

	adapter, descriptor, err := s.resolveAdapter(ctx, target.Adapter)
	if err != nil {
		return nil, err
	}
	if !descriptor.Capabilities.NativeResume {
		return nil, fmt.Errorf("%w: adapter %q does not support continuation", ErrUnsupported, target.Adapter)
	}

	now := time.Now().UTC()
	job := core.JobRecord{
		JobID:           core.GenerateID("job"),
		SessionID:       session.SessionID,
		Adapter:         target.Adapter,
		State:           core.JobStateCreated,
		CWD:             session.CWD,
		CreatedAt:       now,
		UpdatedAt:       now,
		NativeSessionID: target.NativeSessionID,
		Summary: map[string]any{
			"prompt_source": req.PromptSource,
			"continued":     true,
		},
	}
	if err := s.store.CreateJobAndUpdateSession(ctx, session.SessionID, now, job); err != nil {
		return nil, err
	}
	session.LatestJobID = job.JobID
	session.UpdatedAt = now

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
	defer func() {
		_ = s.store.ReleaseLock(context.Background(), lock.LockKey, lock.JobID)
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

	message, runErr := s.executeJobLifecycle(ctx, adapter, descriptor, &job, &turn, startExecutionOptions{
		Prompt:            req.Prompt,
		PromptSource:      req.PromptSource,
		Model:             req.Model,
		Profile:           req.Profile,
		Continue:          true,
		NativeSessionID:   target.NativeSessionID,
		NativeSessionMeta: target.Metadata,
	})

	return &RunResult{
		Job:     job,
		Session: session,
		Message: message,
	}, runErr
}

func (s *Service) Session(ctx context.Context, sessionID string) (*SessionResult, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, normalizeStoreError("session", sessionID, err)
	}

	nativeSessions, err := s.store.ListNativeSessions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	turns, err := s.store.ListTurnsBySession(ctx, sessionID, 20)
	if err != nil {
		return nil, err
	}

	recentJobs, err := s.store.ListJobsBySession(ctx, sessionID, 10)
	if err != nil {
		return nil, err
	}

	active, err := s.store.FindActiveJobBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	actions := make([]SessionAction, 0, len(nativeSessions))
	for _, native := range nativeSessions {
		action := SessionAction{
			Action:          "send",
			Adapter:         native.Adapter,
			NativeSessionID: native.NativeSessionID,
			Available:       native.Resumable && active == nil && native.LockedByJobID == "",
		}
		switch {
		case !native.Resumable:
			action.Reason = "adapter does not declare native continuation"
		case active != nil:
			action.Reason = fmt.Sprintf("active job %s is still running", active.JobID)
		case native.LockedByJobID != "":
			action.Reason = fmt.Sprintf("native session lock held by job %s", native.LockedByJobID)
		}
		actions = append(actions, action)
	}

	return &SessionResult{
		Session:        session,
		NativeSessions: nativeSessions,
		Turns:          turns,
		RecentJobs:     recentJobs,
		Actions:        actions,
	}, nil
}

func (s *Service) ExportHandoff(ctx context.Context, req HandoffExportRequest) (*HandoffExportResult, error) {
	if (req.JobID == "" && req.SessionID == "") || (req.JobID != "" && req.SessionID != "") {
		return nil, fmt.Errorf("%w: specify exactly one of job_id or session_id", ErrInvalidInput)
	}

	var (
		job     core.JobRecord
		session core.SessionRecord
		err     error
	)
	switch {
	case req.JobID != "":
		job, err = s.store.GetJob(ctx, req.JobID)
		if err != nil {
			return nil, normalizeStoreError("job", req.JobID, err)
		}
		session, err = s.store.GetSession(ctx, job.SessionID)
		if err != nil {
			return nil, normalizeStoreError("session", job.SessionID, err)
		}
	default:
		session, err = s.store.GetSession(ctx, req.SessionID)
		if err != nil {
			return nil, normalizeStoreError("session", req.SessionID, err)
		}
		if session.LatestJobID == "" {
			return nil, fmt.Errorf("%w: session %s has no jobs to export", ErrNotFound, session.SessionID)
		}
		job, err = s.store.GetJob(ctx, session.LatestJobID)
		if err != nil {
			return nil, normalizeStoreError("job", session.LatestJobID, err)
		}
	}

	turns, err := s.store.ListTurnsBySession(ctx, session.SessionID, 5)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListEventsBySession(ctx, session.SessionID, 20)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.store.ListArtifactsBySession(ctx, session.SessionID, 10)
	if err != nil {
		return nil, err
	}

	packet := s.buildHandoffPacket(job, session, turns, events, artifacts)
	record := core.HandoffRecord{
		HandoffID: packet.HandoffID,
		JobID:     job.JobID,
		SessionID: session.SessionID,
		CreatedAt: packet.ExportedAt,
		Packet:    packet,
	}
	if err := s.store.CreateHandoff(ctx, record); err != nil {
		return nil, err
	}

	path, err := s.writeHandoffFile(packet, req.OutputPath)
	if err != nil {
		return nil, err
	}
	if err := s.store.InsertArtifact(ctx, core.ArtifactRecord{
		ArtifactID: core.GenerateID("art"),
		JobID:      job.JobID,
		SessionID:  session.SessionID,
		Kind:       "handoff",
		Path:       path,
		CreatedAt:  packet.ExportedAt,
		Metadata: map[string]any{
			"handoff_id": packet.HandoffID,
		},
	}); err != nil {
		return nil, err
	}
	if _, err := s.emitEvent(ctx, job, "handoff.exported", "handoff", map[string]any{
		"handoff_id": packet.HandoffID,
		"path":       path,
	}, "", nil); err != nil {
		return nil, err
	}

	return &HandoffExportResult{
		Handoff: record,
		Path:    path,
	}, nil
}

func (s *Service) RunHandoff(ctx context.Context, req HandoffRunRequest) (*RunResult, error) {
	if req.HandoffRef == "" {
		return nil, fmt.Errorf("%w: handoff must not be empty", ErrInvalidInput)
	}
	if req.Adapter == "" {
		return nil, fmt.Errorf("%w: adapter must not be empty", ErrInvalidInput)
	}
	if _, _, err := s.resolveAdapter(ctx, req.Adapter); err != nil {
		return nil, err
	}

	record, err := s.loadHandoff(ctx, req.HandoffRef)
	if err != nil {
		return nil, err
	}

	cwd := req.CWD
	if cwd == "" {
		if session, sessionErr := s.store.GetSession(ctx, record.Packet.Source.SessionID); sessionErr == nil {
			cwd = session.CWD
		}
	}
	if cwd == "" {
		return nil, fmt.Errorf("%w: cwd is required when the source session is not available locally", ErrInvalidInput)
	}

	prompt := handoffpkg.RenderPrompt(req.Adapter, record.Packet)
	result, runErr := s.Run(ctx, RunRequest{
		Adapter:         req.Adapter,
		CWD:             cwd,
		Prompt:          prompt,
		PromptSource:    "handoff",
		Label:           req.Label,
		Model:           req.Model,
		Profile:         req.Profile,
		ParentSessionID: record.Packet.Source.SessionID,
		HandoffID:       record.Packet.HandoffID,
	})
	if result != nil {
		if _, err := s.emitEvent(ctx, result.Job, "handoff.started", "handoff", map[string]any{
			"handoff_id":     record.Packet.HandoffID,
			"source_adapter": record.Packet.Source.Adapter,
		}, "", nil); err != nil {
			return result, err
		}
	}

	return result, runErr
}

func (s *Service) Status(ctx context.Context, jobID string) (*StatusResult, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, normalizeStoreError("job", jobID, err)
	}

	session, err := s.store.GetSession(ctx, job.SessionID)
	if err != nil {
		return nil, normalizeStoreError("session", job.SessionID, err)
	}

	nativeSessions, err := s.store.ListNativeSessions(ctx, job.SessionID)
	if err != nil {
		return nil, err
	}

	events, err := s.store.ListEvents(ctx, jobID, 50)
	if err != nil {
		return nil, err
	}

	return &StatusResult{
		Job:            job,
		Session:        session,
		NativeSessions: nativeSessions,
		Events:         events,
	}, nil
}

func (s *Service) ListJobs(ctx context.Context, limit int) ([]core.JobRecord, error) {
	return s.store.ListJobs(ctx, limit)
}

func (s *Service) Logs(ctx context.Context, jobID string, limit int) ([]core.EventRecord, error) {
	if _, err := s.store.GetJob(ctx, jobID); err != nil {
		return nil, normalizeStoreError("job", jobID, err)
	}
	return s.store.ListEvents(ctx, jobID, limit)
}

func (s *Service) RawLogs(ctx context.Context, jobID string, limit int) ([]RawLogEntry, error) {
	events, err := s.Logs(ctx, jobID, limit)
	if err != nil {
		return nil, err
	}

	var logs []RawLogEntry
	for _, event := range events {
		if event.RawRef == "" {
			continue
		}

		path := filepath.Join(s.Paths.StateDir, event.RawRef)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read raw artifact %q: %w", path, err)
		}

		logs = append(logs, RawLogEntry{
			Stream:  streamFromRawRef(event.RawRef),
			Path:    path,
			Content: string(data),
		})
	}

	return logs, nil
}

func (s *Service) Cancel(ctx context.Context, jobID string) (*core.JobRecord, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, normalizeStoreError("job", jobID, err)
	}

	if job.State.Terminal() {
		return &job, nil
	}

	job.Summary["message"] = "job cancelled before adapter execution was wired"
	if _, err := s.emitEvent(ctx, job, "job.cancelled", "lifecycle", map[string]any{
		"message": "job cancelled",
	}, "", nil); err != nil {
		return nil, err
	}
	if err := s.finishJob(ctx, &job, core.JobStateCancelled); err != nil {
		return nil, err
	}

	return &job, nil
}

func (s *Service) executeJobLifecycle(
	ctx context.Context,
	adapter adapterapi.Adapter,
	descriptor adapters.Diagnosis,
	job *core.JobRecord,
	turn *core.TurnRecord,
	opts startExecutionOptions,
) (string, error) {
	if err := s.store.CreateTurn(ctx, *turn); err != nil {
		return "", err
	}

	if _, err := s.emitEvent(ctx, *job, "job.created", "lifecycle", map[string]any{
		"cwd":           job.CWD,
		"label":         job.Label,
		"prompt_source": opts.PromptSource,
		"continued":     opts.Continue,
	}, "", nil); err != nil {
		return "", err
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
		return "", err
	}

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
	if runErr != nil {
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
	if runErr != nil {
		terminalState = core.JobStateFailed
		terminalEvent = "job.failed"
	}
	if err := s.finishJob(ctx, job, terminalState); err != nil {
		return message, err
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

	return message, runErr
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

	if lastAssistant == "" {
		lastAssistant = "adapter completed without a translated assistant message"
	}

	return lastAssistant, nil
}

func (s *Service) resolveAdapter(ctx context.Context, name string) (adapterapi.Adapter, adapters.Diagnosis, error) {
	adapter, descriptor, ok := adapters.Resolve(ctx, s.Config, name)
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

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}

	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (s *Service) transitionJob(ctx context.Context, job *core.JobRecord, state core.JobState, payload map[string]any) error {
	job.State = state
	job.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(ctx, *job); err != nil {
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
	return s.store.UpdateJob(ctx, *job)
}

func (s *Service) emitEvent(
	ctx context.Context,
	job core.JobRecord,
	kind string,
	phase string,
	payload any,
	rawStream string,
	rawData []byte,
) (*core.EventRecord, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal event payload: %w", err)
	}

	event := &core.EventRecord{
		EventID:         core.GenerateID("evt"),
		TS:              time.Now().UTC(),
		JobID:           job.JobID,
		SessionID:       job.SessionID,
		Adapter:         job.Adapter,
		Kind:            kind,
		Phase:           phase,
		NativeSessionID: job.NativeSessionID,
		Payload:         encoded,
	}

	if err := s.store.AppendEvent(ctx, event); err != nil {
		return nil, err
	}

	if len(rawData) > 0 && rawStream != "" {
		rawRef, err := s.writeRawArtifact(job, rawStream, event.Seq, rawData)
		if err != nil {
			return nil, err
		}

		artifact := core.ArtifactRecord{
			ArtifactID: core.GenerateID("art"),
			JobID:      job.JobID,
			SessionID:  job.SessionID,
			Kind:       rawStream,
			Path:       rawRef,
			CreatedAt:  time.Now().UTC(),
			Metadata: map[string]any{
				"seq": event.Seq,
			},
		}

		if err := s.store.AttachArtifactToEvent(ctx, event.EventID, job.JobID, rawRef, artifact); err != nil {
			_ = os.Remove(filepath.Join(s.Paths.StateDir, rawRef))
			return nil, err
		}

		event.RawRef = rawRef
	}

	return event, nil
}

func (s *Service) scanStream(
	reader io.Reader,
	stream string,
	lineCh chan<- lineItem,
	errCh chan<- error,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		lineCh <- lineItem{stream: stream, line: scanner.Text()}
	}
	errCh <- scanner.Err()
}

func (s *Service) writeRawArtifact(job core.JobRecord, stream string, seq int64, data []byte) (string, error) {
	dir := filepath.Join(s.Paths.RawDir, stream, job.JobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create raw artifact dir: %w", err)
	}

	name := fmt.Sprintf("%05d.jsonl", seq)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write raw artifact: %w", err)
	}

	return filepath.ToSlash(filepath.Join("raw", stream, job.JobID, name)), nil
}

func (s *Service) readLastMessage(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func (s *Service) buildHandoffPacket(
	job core.JobRecord,
	session core.SessionRecord,
	turns []core.TurnRecord,
	events []core.EventRecord,
	artifacts []core.ArtifactRecord,
) core.HandoffPacket {
	packet := core.HandoffPacket{
		HandoffID:  core.GenerateID("hof"),
		ExportedAt: time.Now().UTC(),
		Source: core.HandoffSource{
			Adapter:         job.Adapter,
			JobID:           job.JobID,
			SessionID:       session.SessionID,
			NativeSessionID: job.NativeSessionID,
			CWD:             session.CWD,
		},
		Objective:            latestObjective(turns),
		Summary:              summarizeTurns(turns),
		Unresolved:           collectUnresolved(job, events),
		ImportantFiles:       collectImportantFiles(session.CWD, events, artifacts),
		RecentTurns:          turns,
		RecentEvents:         condenseEvents(events, 12),
		Artifacts:            toHandoffArtifacts(s.Paths.StateDir, artifacts),
		Constraints:          []string{"Keep CLI flags and JSON output backward compatible.", fmt.Sprintf("Work within %s.", session.CWD)},
		RecommendedNextSteps: recommendNextSteps(job, turns),
	}
	if packet.Objective == "" {
		packet.Objective = "Continue the latest session objective."
	}
	if packet.Summary == "" {
		packet.Summary = "No prior turn summary was captured."
	}
	if len(packet.Unresolved) == 0 && job.State != core.JobStateCompleted {
		packet.Unresolved = []string{fmt.Sprintf("Latest job ended in state %s.", job.State)}
	}
	if packet.Unresolved == nil {
		packet.Unresolved = []string{}
	}
	if packet.ImportantFiles == nil {
		packet.ImportantFiles = []string{}
	}
	if packet.RecentTurns == nil {
		packet.RecentTurns = []core.TurnRecord{}
	}
	if packet.RecentEvents == nil {
		packet.RecentEvents = []core.EventRecord{}
	}
	if packet.Artifacts == nil {
		packet.Artifacts = []core.HandoffArtifact{}
	}
	return packet
}

func (s *Service) writeHandoffFile(packet core.HandoffPacket, outputPath string) (string, error) {
	path := outputPath
	if path == "" {
		path = filepath.Join(s.Paths.HandoffsDir, packet.HandoffID+".json")
	} else {
		expanded, err := core.ExpandPath(outputPath)
		if err != nil {
			return "", fmt.Errorf("%w: expand handoff output path: %v", ErrInvalidInput, err)
		}
		path = expanded
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("%w: resolve handoff output path: %v", ErrInvalidInput, err)
		}
		path = absolute
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create handoff directory: %w", err)
	}

	encoded, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal handoff packet: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write handoff packet: %w", err)
	}

	return path, nil
}

func (s *Service) loadHandoff(ctx context.Context, ref string) (core.HandoffRecord, error) {
	if stat, err := os.Stat(ref); err == nil && !stat.IsDir() {
		data, err := os.ReadFile(ref)
		if err != nil {
			return core.HandoffRecord{}, fmt.Errorf("read handoff file: %w", err)
		}
		var packet core.HandoffPacket
		if err := json.Unmarshal(data, &packet); err != nil {
			return core.HandoffRecord{}, fmt.Errorf("%w: decode handoff file: %v", ErrInvalidInput, err)
		}
		return core.HandoffRecord{
			HandoffID: packet.HandoffID,
			JobID:     packet.Source.JobID,
			SessionID: packet.Source.SessionID,
			CreatedAt: packet.ExportedAt,
			Packet:    packet,
		}, nil
	}

	record, err := s.store.GetHandoff(ctx, ref)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return core.HandoffRecord{}, fmt.Errorf("%w: handoff %s", ErrNotFound, ref)
		}
		return core.HandoffRecord{}, err
	}
	return record, nil
}

func latestObjective(turns []core.TurnRecord) string {
	for _, turn := range turns {
		if strings.TrimSpace(turn.InputText) != "" {
			return strings.TrimSpace(turn.InputText)
		}
	}
	return ""
}

func summarizeTurns(turns []core.TurnRecord) string {
	var parts []string
	for _, turn := range turns {
		if text := strings.TrimSpace(turn.ResultSummary); text != "" {
			parts = append(parts, text)
		}
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, "\n")
}

func collectUnresolved(job core.JobRecord, events []core.EventRecord) []string {
	var unresolved []string
	if job.State != core.JobStateCompleted {
		unresolved = append(unresolved, fmt.Sprintf("Latest job state is %s.", job.State))
	}
	for _, event := range events {
		if event.Kind != "diagnostic" && event.Kind != "job.failed" && event.Kind != "job.cancelled" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
			unresolved = append(unresolved, strings.TrimSpace(message))
		}
	}
	return dedupeStrings(unresolved, 6)
}

func collectImportantFiles(cwd string, events []core.EventRecord, artifacts []core.ArtifactRecord) []string {
	var files []string
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		stat, err := os.Stat(path)
		if err != nil || stat.IsDir() {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}

	for _, artifact := range artifacts {
		add(artifact.Path)
	}
	for _, event := range events {
		var decoded any
		if err := json.Unmarshal(event.Payload, &decoded); err != nil {
			continue
		}
		walkStrings(decoded, func(value string) {
			add(value)
			if cwd != "" && !filepath.IsAbs(value) {
				add(filepath.Join(cwd, value))
			}
		})
	}

	if len(files) > 12 {
		files = files[:12]
	}
	return files
}

func condenseEvents(events []core.EventRecord, limit int) []core.EventRecord {
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	condensed := make([]core.EventRecord, 0, len(events))
	for _, event := range events {
		var payload any
		if err := json.Unmarshal(event.Payload, &payload); err == nil {
			payload = truncateNestedStrings(payload, 400)
			if encoded, err := json.Marshal(payload); err == nil {
				event.Payload = encoded
			}
		}
		condensed = append(condensed, event)
	}
	return condensed
}

func truncateNestedStrings(value any, max int) any {
	switch typed := value.(type) {
	case string:
		if max > 0 && len(typed) > max {
			return typed[:max] + "...(truncated)"
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = truncateNestedStrings(typed[i], max)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = truncateNestedStrings(item, max)
		}
		return typed
	default:
		return value
	}
}

func toHandoffArtifacts(stateDir string, artifacts []core.ArtifactRecord) []core.HandoffArtifact {
	result := make([]core.HandoffArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		path := artifact.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(stateDir, path)
		}
		result = append(result, core.HandoffArtifact{
			Kind:     artifact.Kind,
			Path:     path,
			Metadata: artifact.Metadata,
		})
	}
	return result
}

func recommendNextSteps(job core.JobRecord, turns []core.TurnRecord) []string {
	steps := []string{"Review the most recent summary and unresolved items.", "Inspect the important files before making changes.", "Run verification before finishing."}
	if job.State != core.JobStateCompleted {
		steps[0] = fmt.Sprintf("Investigate why the latest job ended in state %s.", job.State)
	}
	if len(turns) > 0 && turns[0].ResultSummary != "" {
		steps = append([]string{"Use the last turn summary as the starting context."}, steps...)
	}
	return dedupeStrings(steps, 5)
}

func walkStrings(value any, visit func(string)) {
	switch typed := value.(type) {
	case string:
		visit(typed)
	case []any:
		for _, item := range typed {
			walkStrings(item, visit)
		}
	case map[string]any:
		for _, item := range typed {
			walkStrings(item, visit)
		}
	}
}

func dedupeStrings(values []string, limit int) []string {
	seen := map[string]struct{}{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if limit > 0 && len(result) == limit {
			break
		}
	}
	return result
}

func normalizeStoreError(kind, id string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
	}
	return err
}

func streamFromRawRef(rawRef string) string {
	parts := strings.Split(filepath.ToSlash(rawRef), "/")
	for _, candidate := range []string{"stdout", "stderr", "native"} {
		for _, part := range parts {
			if part == candidate {
				return candidate
			}
		}
		if filepath.ToSlash(rawRef) == candidate || filepath.Clean(rawRef) == candidate {
			return candidate
		}
	}

	return "raw"
}
