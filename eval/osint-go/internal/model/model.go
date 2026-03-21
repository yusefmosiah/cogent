package model

import "time"

type ScanRequest struct {
	Domain string `json:"domain"`
}

type Finding struct {
	Source  string         `json:"source"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details,omitempty"`
}

type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type ScanResult struct {
	ID             string        `json:"id"`
	Domain         string        `json:"domain"`
	StartedAt      time.Time     `json:"started_at"`
	CompletedAt    time.Time     `json:"completed_at"`
	DurationMillis int64         `json:"duration_ms"`
	Findings       []Finding     `json:"findings"`
	Errors         []SourceError `json:"errors,omitempty"`
	PartialFailure bool          `json:"partial_failure"`
}
