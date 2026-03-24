package channelmeta

import "strings"

const (
	SourceWorker    = "worker"
	SourceJobRunner = "job_runner"

	TypeInfo         = "info"
	TypeStatusUpdate = "status_update"
	TypeEscalation   = "escalation"
	TypeJobCompleted = "job_completed"
)

func NormalizeWorkerReportType(reportType string) string {
	switch strings.TrimSpace(reportType) {
	case "", TypeInfo:
		return TypeInfo
	case TypeStatusUpdate:
		return TypeStatusUpdate
	case TypeEscalation:
		return TypeEscalation
	default:
		return TypeInfo
	}
}

func WorkerReportMeta(reportType string) map[string]string {
	return map[string]string{
		"source": SourceWorker,
		"type":   NormalizeWorkerReportType(reportType),
	}
}

func JobCompletionMeta() map[string]string {
	return map[string]string{
		"source": SourceJobRunner,
		"type":   TypeJobCompleted,
	}
}
