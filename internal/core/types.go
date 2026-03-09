package core

import (
	"encoding/json"
	"time"
)

type JobState string

const (
	JobStateCreated      JobState = "created"
	JobStateQueued       JobState = "queued"
	JobStateStarting     JobState = "starting"
	JobStateRunning      JobState = "running"
	JobStateWaitingInput JobState = "waiting_input"
	JobStateCompleted    JobState = "completed"
	JobStateFailed       JobState = "failed"
	JobStateCancelled    JobState = "cancelled"
	JobStateBlocked      JobState = "blocked"
)

func (s JobState) Terminal() bool {
	switch s {
	case JobStateCompleted, JobStateFailed, JobStateCancelled, JobStateBlocked:
		return true
	default:
		return false
	}
}

type SessionRecord struct {
	SessionID      string         `json:"session_id"`
	Label          string         `json:"label,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Status         string         `json:"status"`
	OriginAdapter  string         `json:"origin_adapter"`
	OriginJobID    string         `json:"origin_job_id"`
	CWD            string         `json:"cwd"`
	LatestJobID    string         `json:"latest_job_id,omitempty"`
	ParentSession  *string        `json:"parent_session_id,omitempty"`
	ForkedFromTurn *string        `json:"forked_from_turn_id,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Metadata       map[string]any `json:"metadata"`
}

type JobRecord struct {
	JobID           string         `json:"job_id"`
	SessionID       string         `json:"session_id"`
	Adapter         string         `json:"adapter"`
	State           JobState       `json:"state"`
	Label           string         `json:"label,omitempty"`
	NativeSessionID string         `json:"native_session_id,omitempty"`
	CWD             string         `json:"cwd"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	FinishedAt      *time.Time     `json:"finished_at,omitempty"`
	Summary         map[string]any `json:"summary"`
	LastRawArtifact string         `json:"last_raw_artifact,omitempty"`
}

type EventRecord struct {
	EventID         string          `json:"event_id"`
	Seq             int64           `json:"seq"`
	TS              time.Time       `json:"ts"`
	JobID           string          `json:"job_id"`
	SessionID       string          `json:"session_id"`
	Adapter         string          `json:"adapter"`
	Kind            string          `json:"kind"`
	Phase           string          `json:"phase,omitempty"`
	NativeSessionID string          `json:"native_session_id,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	RawRef          string          `json:"raw_ref,omitempty"`
}

type ArtifactRecord struct {
	ArtifactID string         `json:"artifact_id"`
	JobID      string         `json:"job_id"`
	SessionID  string         `json:"session_id"`
	Kind       string         `json:"kind"`
	Path       string         `json:"path"`
	CreatedAt  time.Time      `json:"created_at"`
	Metadata   map[string]any `json:"metadata"`
}
