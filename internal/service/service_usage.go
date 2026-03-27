package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yusefmosiah/cogent/internal/core"
	"github.com/yusefmosiah/cogent/internal/pricing"
)

type jobUsageContract struct {
	usage         *core.UsageReport
	usageByModel  []core.UsageReport
	cost          *core.CostEstimate
	vendorCost    *core.CostEstimate
	estimatedCost *core.CostEstimate
	attribution   *core.UsageAttribution
}

type catalogUsageContribution struct {
	key   string
	usage core.UsageReport
}

func usageRoleForWork(work core.WorkItemRecord) string {
	if strings.EqualFold(work.Kind, "attest") {
		return "verifier"
	}
	return "worker"
}

func usageAttributionMap(attr core.UsageAttribution) map[string]any {
	return map[string]any{
		"role":           attr.Role,
		"attempt_epoch":  attr.AttemptEpoch,
		"parent_work_id": attr.ParentWorkID,
		"worker_job_id":  attr.WorkerJobID,
	}
}

func stampJobUsageAttribution(job *core.JobRecord, work core.WorkItemRecord) {
	if job == nil {
		return
	}
	if job.Summary == nil {
		job.Summary = map[string]any{}
	}
	attr := core.UsageAttribution{
		Role:         usageRoleForWork(work),
		AttemptEpoch: workAttemptEpoch(work),
	}
	if attr.Role == "verifier" {
		attr.ParentWorkID = summaryString(work.Metadata, "parent_work_id")
		attr.WorkerJobID = summaryString(work.Metadata, "worker_job_id")
	}
	job.Summary["usage_attribution"] = usageAttributionMap(attr)
}

func usageAttributionFromSummary(summary map[string]any) *core.UsageAttribution {
	if summary == nil {
		return nil
	}
	raw, ok := summary["usage_attribution"].(map[string]any)
	if !ok {
		return nil
	}
	attr := &core.UsageAttribution{
		Role:         summaryString(raw, "role"),
		AttemptEpoch: int(summaryInt64(raw, "attempt_epoch")),
		ParentWorkID: summaryString(raw, "parent_work_id"),
		WorkerJobID:  summaryString(raw, "worker_job_id"),
	}
	if attr.Role == "" && attr.AttemptEpoch == 0 && attr.ParentWorkID == "" && attr.WorkerJobID == "" {
		return nil
	}
	return attr
}

func normalizeUsageAttribution(attr *core.UsageAttribution) *core.UsageAttribution {
	if attr == nil {
		return nil
	}
	if attr.Role == "" && attr.AttemptEpoch == 0 && attr.ParentWorkID == "" && attr.WorkerJobID == "" && !attr.CurrentAttempt {
		return nil
	}
	copy := *attr
	return &copy
}

func usageMatchesModel(report core.UsageReport, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	if strings.EqualFold(report.Model, model) {
		return true
	}
	if report.Provider != "" && report.Model != "" && strings.EqualFold(report.Provider+"/"+report.Model, model) {
		return true
	}
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		return strings.EqualFold(report.Model, parts[1])
	}
	return false
}

func addCatalogUsageTotals(hist *core.CatalogHistory, usage core.UsageReport) {
	hist.TotalInputTokens += usage.InputTokens
	hist.TotalOutputTokens += usage.OutputTokens
	hist.TotalTokens += usage.TotalTokens
	hist.TotalCachedInputTokens += usage.CachedInputTokens
	hist.TotalCacheReadInputTokens += usage.CacheReadInputTokens
	hist.TotalCacheCreationInputTokens += usage.CacheCreationInputTokens
}

func (s *Service) loadWorkForUsage(ctx context.Context, job core.JobRecord, workCache map[string]core.WorkItemRecord) *core.WorkItemRecord {
	if job.WorkID == "" {
		return nil
	}
	if workCache != nil {
		if cached, ok := workCache[job.WorkID]; ok {
			work := cached
			return &work
		}
	}
	work, err := s.store.GetWorkItem(ctx, job.WorkID)
	if err != nil {
		return nil
	}
	if workCache != nil {
		workCache[job.WorkID] = work
	}
	return &work
}

func (s *Service) canonicalJobUsage(ctx context.Context, job core.JobRecord, workCache map[string]core.WorkItemRecord) *jobUsageContract {
	usage := usageFromSummary(job.Summary)
	usageByModel := modelUsageFromSummary(job.Summary)
	vendorCost := vendorCostFromSummary(job)
	estimatedCost := estimatedCostFromSummary(job)
	selectedCost := vendorCost
	if selectedCost == nil {
		selectedCost = estimatedCost
	}

	attr := usageAttributionFromSummary(job.Summary)
	if work := s.loadWorkForUsage(ctx, job, workCache); work != nil {
		if attr == nil {
			attr = &core.UsageAttribution{}
		}
		if attr.Role == "" {
			attr.Role = usageRoleForWork(*work)
		}
		if attr.AttemptEpoch == 0 {
			attr.AttemptEpoch = workAttemptEpoch(*work)
		}
		if attr.Role == "verifier" {
			if attr.ParentWorkID == "" {
				attr.ParentWorkID = summaryString(work.Metadata, "parent_work_id")
			}
			if attr.WorkerJobID == "" {
				attr.WorkerJobID = summaryString(work.Metadata, "worker_job_id")
			}
		}
		if attr.AttemptEpoch > 0 {
			attr.CurrentAttempt = attr.AttemptEpoch == workAttemptEpoch(*work)
		}
	}

	if usage == nil && len(usageByModel) == 0 && vendorCost == nil && estimatedCost == nil && normalizeUsageAttribution(attr) == nil {
		return nil
	}
	return &jobUsageContract{
		usage:         usage,
		usageByModel:  usageByModel,
		cost:          selectedCost,
		vendorCost:    vendorCost,
		estimatedCost: estimatedCost,
		attribution:   normalizeUsageAttribution(attr),
	}
}

func catalogUsageContributions(job core.JobRecord, contract *jobUsageContract) []catalogUsageContribution {
	if contract != nil && len(contract.usageByModel) > 0 {
		perProvider := make(map[string]core.UsageReport)
		result := make([]catalogUsageContribution, 0, len(contract.usageByModel)*2)
		for _, usage := range contract.usageByModel {
			provider, model := pricingLookupContext(job, &usage)
			if provider == "" && model == "" {
				continue
			}
			usage.Provider = provider
			usage.Model = model
			result = append(result, catalogUsageContribution{
				key:   catalogHistoryKey(job.Adapter, provider, model),
				usage: usage,
			})
			bucket := perProvider[provider]
			bucket.Provider = provider
			bucket.InputTokens += usage.InputTokens
			bucket.OutputTokens += usage.OutputTokens
			bucket.TotalTokens += usage.TotalTokens
			bucket.CachedInputTokens += usage.CachedInputTokens
			bucket.CacheReadInputTokens += usage.CacheReadInputTokens
			bucket.CacheCreationInputTokens += usage.CacheCreationInputTokens
			perProvider[provider] = bucket
		}
		providers := make([]string, 0, len(perProvider))
		for provider := range perProvider {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		for _, provider := range providers {
			result = append(result, catalogUsageContribution{
				key:   catalogHistoryKey(job.Adapter, provider, ""),
				usage: perProvider[provider],
			})
		}
		return result
	}
	if contract == nil || contract.usage == nil {
		return nil
	}
	usage := *contract.usage
	provider, model := pricingLookupContext(job, &usage)
	usage.Provider = provider
	usage.Model = model
	result := []catalogUsageContribution{{
		key:   catalogHistoryKey(job.Adapter, provider, model),
		usage: usage,
	}}
	if model != "" {
		providerTotal := usage
		providerTotal.Model = ""
		result = append(result, catalogUsageContribution{
			key:   catalogHistoryKey(job.Adapter, provider, ""),
			usage: providerTotal,
		})
	}
	return result
}

func applyUsageContract(match *core.HistoryMatch, contract *jobUsageContract) {
	if match == nil || contract == nil {
		return
	}
	match.Usage = contract.usage
	match.UsageByModel = contract.usageByModel
	match.Cost = contract.cost
	match.VendorCost = contract.vendorCost
	match.EstimatedCost = contract.estimatedCost
	match.UsageAttribution = contract.attribution
}

func (s *Service) catalogHistory(ctx context.Context, limit int) (map[string]core.CatalogHistory, error) {
	jobs, err := s.store.ListJobs(ctx, limit)
	if err != nil {
		return nil, err
	}

	history := make(map[string]core.CatalogHistory)
	workCache := make(map[string]core.WorkItemRecord)
	for _, job := range jobs {
		contract := s.canonicalJobUsage(ctx, job, workCache)
		contributions := catalogUsageContributions(job, contract)
		keys := make([]string, 0, len(contributions)+2)
		for _, contribution := range contributions {
			keys = append(keys, contribution.key)
		}
		if len(keys) == 0 {
			provider, model := pricingLookupContext(job, nil)
			keys = append(keys, catalogHistoryKey(job.Adapter, provider, model))
			if model != "" {
				keys = append(keys, catalogHistoryKey(job.Adapter, provider, ""))
			}
		}

		seenKeys := make(map[string]bool)
		for _, key := range keys {
			if seenKeys[key] {
				continue
			}
			seenKeys[key] = true
			hist := history[key]
			if hist.RecentJobs == 0 {
				hist.LastJobID = job.JobID
				hist.LastSessionID = job.SessionID
				lastUsedAt := job.UpdatedAt
				hist.LastUsedAt = &lastUsedAt
			}
			hist.RecentJobs++
			switch job.State {
			case core.JobStateCompleted:
				hist.RecentSuccesses++
				if hist.LastSucceededAt == nil {
					lastSucceededAt := job.UpdatedAt
					hist.LastSucceededAt = &lastSucceededAt
				}
			case core.JobStateFailed, core.JobStateBlocked:
				hist.RecentFailures++
				if hist.LastFailedAt == nil {
					lastFailedAt := job.UpdatedAt
					hist.LastFailedAt = &lastFailedAt
				}
			case core.JobStateCancelled:
				hist.RecentCancelled++
			}
			history[key] = hist
		}
		for _, contribution := range contributions {
			hist := history[contribution.key]
			addCatalogUsageTotals(&hist, contribution.usage)
			history[contribution.key] = hist
		}
	}

	return history, nil
}

func catalogEntryLess(a, b core.CatalogEntry) bool {
	if probeRank(a.ProbeStatus) != probeRank(b.ProbeStatus) {
		return probeRank(a.ProbeStatus) < probeRank(b.ProbeStatus)
	}
	if historySuccesses(a.History) != historySuccesses(b.History) {
		return historySuccesses(a.History) > historySuccesses(b.History)
	}
	if cmp := compareTimes(historySucceededAt(a.History), historySucceededAt(b.History)); cmp != 0 {
		return cmp > 0
	}
	if cmp := compareTimes(historyUsedAt(a.History), historyUsedAt(b.History)); cmp != 0 {
		return cmp > 0
	}
	if a.Selected != b.Selected {
		return a.Selected
	}
	if a.Available != b.Available {
		return a.Available
	}
	if a.Adapter != b.Adapter {
		return a.Adapter < b.Adapter
	}
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	return a.Model < b.Model
}

func probeRank(status string) int {
	switch status {
	case "runnable":
		return 0
	case "":
		return 1
	case "unsupported_by_plan":
		return 2
	case "hung_or_unstable":
		return 3
	default:
		return 4
	}
}

func historySuccesses(history *core.CatalogHistory) int {
	if history == nil {
		return 0
	}
	return history.RecentSuccesses
}

func historySucceededAt(history *core.CatalogHistory) *time.Time {
	if history == nil {
		return nil
	}
	return history.LastSucceededAt
}

func historyUsedAt(history *core.CatalogHistory) *time.Time {
	if history == nil {
		return nil
	}
	return history.LastUsedAt
}

func compareTimes(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	case a.After(*b):
		return 1
	case a.Before(*b):
		return -1
	default:
		return 0
	}
}

func historyJobMatches(job core.JobRecord, contract *jobUsageContract, req HistorySearchRequest) bool {
	if req.Adapter != "" && job.Adapter != req.Adapter {
		return false
	}
	if req.SessionID != "" && job.SessionID != req.SessionID {
		return false
	}
	if req.CWD != "" && job.CWD != req.CWD {
		return false
	}
	if req.Model != "" {
		if strings.EqualFold(summaryString(job.Summary, "model"), req.Model) {
			return true
		}
		if contract != nil {
			if contract.usage != nil && usageMatchesModel(*contract.usage, req.Model) {
				return true
			}
			for _, usage := range contract.usageByModel {
				if usageMatchesModel(usage, req.Model) {
					return true
				}
			}
		}
		return false
	}
	return true
}

func stringifySummary(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

func makeHistoryMatch(kind, query, text string) (string, bool) {
	if text == "" {
		return "", false
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if lowerQuery == "" {
		return "", false
	}
	idx := strings.Index(lowerText, lowerQuery)
	if idx == -1 {
		return "", false
	}
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 160
	if end > len(text) {
		end = len(text)
	}
	snippet := strings.TrimSpace(text[start:end])
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	snippet = strings.Join(strings.Fields(snippet), " ")
	return snippet, true
}

func historyScore(query, text string) int {
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	if lowerQuery == "" {
		return 0
	}
	count := strings.Count(lowerText, lowerQuery)
	if count == 0 {
		return 0
	}
	score := count * 10
	if idx := strings.Index(lowerText, lowerQuery); idx >= 0 {
		score += max(0, 1000-idx)
	}
	return score
}

func shouldSearchArtifactContent(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 256*1024 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".txt", ".json", ".jsonl", ".log", ".yaml", ".yml", ".toml", ".xml", ".csv":
		return true
	}
	return ext == ""
}

func (s *Service) probeCatalogEntry(ctx context.Context, entry core.CatalogEntry, req ProbeCatalogRequest, timeout time.Duration) (core.CatalogEntry, *core.CatalogIssue) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Reply with exactly OK and nothing else."
	}
	cwd := req.CWD
	if cwd == "" {
		cwd = "."
	}
	model := probeModelArg(entry)
	startedAt := time.Now().UTC()
	entry.ProbeAt = &startedAt
	entry.ProbeStatus = "launching"
	entry.ProbeMessage = ""
	entry.ProbeJobID = ""

	runResult, runErr := s.Run(probeCtx, RunRequest{
		Adapter:      entry.Adapter,
		Model:        model,
		CWD:          cwd,
		Prompt:       prompt,
		PromptSource: "catalog_probe",
		Label:        "catalog probe",
	})
	if runErr != nil {
		entry.ProbeStatus = "launch_error"
		entry.ProbeMessage = runErr.Error()
		return entry, &core.CatalogIssue{
			Adapter:  entry.Adapter,
			Severity: "warning",
			Message:  fmt.Sprintf("catalog probe launch failed for %s/%s: %v", entry.Provider, entry.Model, runErr),
		}
	}

	entry.ProbeJobID = runResult.Job.JobID
	status, waitErr := s.WaitStatus(probeCtx, runResult.Job.JobID, 250*time.Millisecond, timeout)
	if waitErr != nil {
		entry.ProbeStatus = "hung_or_unstable"
		entry.ProbeMessage = waitErr.Error()
		return entry, nil
	}

	classification, message := classifyProbeOutcome(status)
	entry.ProbeStatus = classification
	entry.ProbeMessage = message
	return entry, nil
}

func probeModelArg(entry core.CatalogEntry) string {
	if entry.Model == "" {
		return ""
	}
	switch entry.Adapter {
	case "opencode":
		if entry.Provider != "" {
			return entry.Provider + "/" + entry.Model
		}
	}
	return entry.Model
}

func classifyProbeOutcome(status *StatusResult) (string, string) {
	if status == nil {
		return "provider_error", ""
	}
	message := summaryString(status.Job.Summary, "message")
	eventsText := strings.ToLower(message)
	for _, event := range status.Events {
		eventsText += " " + strings.ToLower(string(event.Payload))
	}

	switch {
	case strings.Contains(eventsText, "not supported when using codex with a chatgpt account"),
		strings.Contains(eventsText, "not supported"),
		strings.Contains(eventsText, "unsupported"),
		strings.Contains(eventsText, "plan"):
		if message == "" {
			message = "unsupported by current account or plan"
		}
		return "unsupported_by_plan", message
	case status.Job.State == core.JobStateFailed:
		if message == "" {
			message = "provider-side failure"
		}
		return "provider_error", message
	}

	trimmed := strings.TrimSpace(message)
	if status.Job.State == core.JobStateCompleted && trimmed == "OK" {
		return "runnable", message
	}
	if status.Job.State == core.JobStateCompleted {
		if message == "" {
			message = "completed without the expected probe response"
		}
		return "hung_or_unstable", message
	}
	if status.Job.State == core.JobStateCancelled {
		if message == "" {
			message = "probe cancelled"
		}
		return "hung_or_unstable", message
	}
	if message == "" {
		message = string(status.Job.State)
	}
	return "provider_error", message
}

func (s *Service) applyUsageHint(ctx context.Context, job *core.JobRecord, payload map[string]any) error {
	if job.Summary == nil {
		job.Summary = map[string]any{}
	}

	usage := usageFromPayload(payload)
	if usage == nil {
		return nil
	}
	if usage.Model != "" && summaryString(job.Summary, "model") == "" {
		job.Summary["model"] = usage.Model
	}
	if usage.Provider != "" && summaryString(job.Summary, "provider") == "" {
		job.Summary["provider"] = usage.Provider
	}

	merged := mergeUsageReports(usageFromSummary(job.Summary), *usage)
	if merged != nil {
		job.Summary["usage"] = map[string]any{
			"provider":                    merged.Provider,
			"model":                       merged.Model,
			"input_tokens":                merged.InputTokens,
			"output_tokens":               merged.OutputTokens,
			"total_tokens":                merged.TotalTokens,
			"cached_input_tokens":         merged.CachedInputTokens,
			"cache_read_input_tokens":     merged.CacheReadInputTokens,
			"cache_creation_input_tokens": merged.CacheCreationInputTokens,
			"source":                      merged.Source,
		}
	}
	if usageByModel := modelUsageFromPayload(payload); len(usageByModel) > 0 {
		job.Summary["usage_by_model"] = modelUsageMaps(mergeModelUsageReports(modelUsageFromSummary(job.Summary), usageByModel))
	}

	if vendor := costFromPayload(payload); vendor != nil {
		job.Summary["vendor_cost"] = costMap(*vendor)
	}
	if estimated := s.estimateCostForJob(*job); estimated != nil {
		job.Summary["estimated_cost"] = costMap(*estimated)
	}
	if preferred := preferredCostFromSummary(*job); preferred != nil {
		job.Summary["cost"] = costMap(*preferred)
	} else {
		delete(job.Summary, "cost")
	}

	return s.store.UpdateJob(ctx, *job)
}

func usageFromPayload(payload map[string]any) *core.UsageReport {
	usage := &core.UsageReport{
		Provider:                 summaryString(payload, "provider"),
		Model:                    summaryString(payload, "model"),
		InputTokens:              summaryInt64(payload, "input_tokens"),
		OutputTokens:             summaryInt64(payload, "output_tokens"),
		TotalTokens:              summaryInt64(payload, "total_tokens"),
		CachedInputTokens:        summaryInt64(payload, "cached_input_tokens"),
		CacheReadInputTokens:     summaryInt64(payload, "cache_read_input_tokens"),
		CacheCreationInputTokens: summaryInt64(payload, "cache_creation_input_tokens"),
		Source:                   "vendor_report",
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 && usage.CachedInputTokens == 0 && usage.CacheReadInputTokens == 0 && usage.CacheCreationInputTokens == 0 {
		return nil
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CachedInputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	}
	return usage
}

func usageFromSummary(summary map[string]any) *core.UsageReport {
	if summary == nil {
		return nil
	}
	raw, ok := summary["usage"].(map[string]any)
	if !ok {
		return nil
	}
	usage := &core.UsageReport{
		Provider:                 summaryString(raw, "provider"),
		Model:                    summaryString(raw, "model"),
		InputTokens:              summaryInt64(raw, "input_tokens"),
		OutputTokens:             summaryInt64(raw, "output_tokens"),
		TotalTokens:              summaryInt64(raw, "total_tokens"),
		CachedInputTokens:        summaryInt64(raw, "cached_input_tokens"),
		CacheReadInputTokens:     summaryInt64(raw, "cache_read_input_tokens"),
		CacheCreationInputTokens: summaryInt64(raw, "cache_creation_input_tokens"),
		Source:                   summaryString(raw, "source"),
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 && usage.CachedInputTokens == 0 && usage.CacheReadInputTokens == 0 && usage.CacheCreationInputTokens == 0 {
		return nil
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CachedInputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	}
	return usage
}

func mergeUsageReports(existing *core.UsageReport, incoming core.UsageReport) *core.UsageReport {
	if existing == nil {
		copy := incoming
		return &copy
	}
	merged := *existing
	merged.InputTokens = max(merged.InputTokens, incoming.InputTokens)
	merged.OutputTokens = max(merged.OutputTokens, incoming.OutputTokens)
	merged.TotalTokens = max(merged.TotalTokens, incoming.TotalTokens)
	merged.CachedInputTokens = max(merged.CachedInputTokens, incoming.CachedInputTokens)
	merged.CacheReadInputTokens = max(merged.CacheReadInputTokens, incoming.CacheReadInputTokens)
	merged.CacheCreationInputTokens = max(merged.CacheCreationInputTokens, incoming.CacheCreationInputTokens)
	if merged.Model == "" {
		merged.Model = incoming.Model
	}
	if merged.Provider == "" {
		merged.Provider = incoming.Provider
	}
	if incoming.Source != "" {
		merged.Source = incoming.Source
	}
	return &merged
}

func modelUsageFromPayload(payload map[string]any) []core.UsageReport {
	raw, ok := payload["model_usage"].([]any)
	if !ok {
		return nil
	}

	models := make([]core.UsageReport, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		report := core.UsageReport{
			Provider:                 summaryString(entry, "provider"),
			Model:                    summaryString(entry, "model"),
			InputTokens:              summaryInt64(entry, "input_tokens"),
			OutputTokens:             summaryInt64(entry, "output_tokens"),
			TotalTokens:              summaryInt64(entry, "total_tokens"),
			CachedInputTokens:        summaryInt64(entry, "cached_input_tokens"),
			CacheReadInputTokens:     summaryInt64(entry, "cache_read_input_tokens"),
			CacheCreationInputTokens: summaryInt64(entry, "cache_creation_input_tokens"),
			CostUSD:                  summaryFloat64(entry, "cost_usd"),
			Source:                   "vendor_report",
		}
		if report.TotalTokens == 0 {
			report.TotalTokens = report.InputTokens + report.OutputTokens + report.CachedInputTokens + report.CacheReadInputTokens + report.CacheCreationInputTokens
		}
		if report.Model == "" {
			continue
		}
		models = append(models, report)
	}
	if len(models) == 0 {
		return nil
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].Model < models[j].Model
	})
	return models
}

func modelUsageFromSummary(summary map[string]any) []core.UsageReport {
	if summary == nil {
		return nil
	}
	raw, ok := summary["usage_by_model"].([]any)
	if !ok {
		return nil
	}
	models := make([]core.UsageReport, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model := core.UsageReport{
			Provider:                 summaryString(entry, "provider"),
			Model:                    summaryString(entry, "model"),
			InputTokens:              summaryInt64(entry, "input_tokens"),
			OutputTokens:             summaryInt64(entry, "output_tokens"),
			TotalTokens:              summaryInt64(entry, "total_tokens"),
			CachedInputTokens:        summaryInt64(entry, "cached_input_tokens"),
			CacheReadInputTokens:     summaryInt64(entry, "cache_read_input_tokens"),
			CacheCreationInputTokens: summaryInt64(entry, "cache_creation_input_tokens"),
			CostUSD:                  summaryFloat64(entry, "cost_usd"),
			Source:                   summaryString(entry, "source"),
		}
		if model.TotalTokens == 0 {
			model.TotalTokens = model.InputTokens + model.OutputTokens + model.CachedInputTokens + model.CacheReadInputTokens + model.CacheCreationInputTokens
		}
		if model.Model == "" {
			continue
		}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].Model < models[j].Model
	})
	return models
}

func mergeModelUsageReports(existing []core.UsageReport, incoming []core.UsageReport) []core.UsageReport {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}

	merged := make(map[string]core.UsageReport, len(existing)+len(incoming))
	add := func(report core.UsageReport) {
		if report.TotalTokens == 0 {
			report.TotalTokens = report.InputTokens + report.OutputTokens + report.CachedInputTokens + report.CacheReadInputTokens + report.CacheCreationInputTokens
		}
		key := strings.ToLower(report.Provider + "|" + report.Model)
		if current, ok := merged[key]; ok {
			updated := mergeUsageReports(&current, report)
			updated.CostUSD = max(current.CostUSD, report.CostUSD)
			merged[key] = *updated
			return
		}
		merged[key] = report
	}
	for _, report := range existing {
		add(report)
	}
	for _, report := range incoming {
		add(report)
	}

	result := make([]core.UsageReport, 0, len(merged))
	for _, report := range merged {
		result = append(result, report)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	return result
}

func modelUsageMaps(models []core.UsageReport) []map[string]any {
	if len(models) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(models))
	for _, model := range models {
		result = append(result, map[string]any{
			"provider":                    model.Provider,
			"model":                       model.Model,
			"input_tokens":                model.InputTokens,
			"output_tokens":               model.OutputTokens,
			"total_tokens":                model.TotalTokens,
			"cached_input_tokens":         model.CachedInputTokens,
			"cache_read_input_tokens":     model.CacheReadInputTokens,
			"cache_creation_input_tokens": model.CacheCreationInputTokens,
			"cost_usd":                    model.CostUSD,
			"source":                      model.Source,
		})
	}
	return result
}

func costFromPayload(payload map[string]any) *core.CostEstimate {
	total := summaryFloat64(payload, "cost_usd")
	if total == 0 {
		return nil
	}
	return &core.CostEstimate{
		Currency:     "USD",
		TotalCostUSD: total,
		Estimated:    false,
		Source:       "vendor_report",
	}
}

func preferredCostFromSummary(job core.JobRecord) *core.CostEstimate {
	if vendor := vendorCostFromSummary(job); vendor != nil {
		return vendor
	}
	return estimatedCostFromSummary(job)
}

func vendorCostFromSummary(job core.JobRecord) *core.CostEstimate {
	if cost := summaryCost(job.Summary, "vendor_cost"); cost != nil {
		return cost
	}
	if cost := summaryCost(job.Summary, "cost"); cost != nil && !cost.Estimated {
		return cost
	}
	return nil
}

func estimatedCostFromSummary(job core.JobRecord) *core.CostEstimate {
	if cost := summaryCost(job.Summary, "estimated_cost"); cost != nil {
		return cost
	}
	if cost := summaryCost(job.Summary, "cost"); cost != nil && cost.Estimated {
		return cost
	}
	return nil
}

func (s *Service) estimateCostForJob(job core.JobRecord) *core.CostEstimate {
	if models := modelUsageFromSummary(job.Summary); len(models) > 0 {
		total := &core.CostEstimate{
			Currency:  "USD",
			Estimated: true,
		}
		for _, modelUsage := range models {
			usage := core.UsageReport{
				Provider:                 modelUsage.Provider,
				Model:                    modelUsage.Model,
				InputTokens:              modelUsage.InputTokens,
				OutputTokens:             modelUsage.OutputTokens,
				TotalTokens:              modelUsage.TotalTokens,
				CachedInputTokens:        modelUsage.CachedInputTokens,
				CacheReadInputTokens:     modelUsage.CacheReadInputTokens,
				CacheCreationInputTokens: modelUsage.CacheCreationInputTokens,
				Source:                   modelUsage.Source,
			}
			provider, model := pricingLookupContext(job, &usage)
			if provider == "" || model == "" {
				continue
			}
			usage.Provider = provider
			usage.Model = model
			estimate := pricing.Estimate(usage, pricing.Resolve(s.Config, provider, model))
			if estimate == nil {
				continue
			}
			total.InputCostUSD += estimate.InputCostUSD
			total.OutputCostUSD += estimate.OutputCostUSD
			total.CachedInputCostUSD += estimate.CachedInputCostUSD
			total.CacheReadCostUSD += estimate.CacheReadCostUSD
			total.CacheCreationCostUSD += estimate.CacheCreationCostUSD
			total.TotalCostUSD += estimate.TotalCostUSD
			if total.Source == "" {
				total.Source = estimate.Source
			}
			if total.SourceURL == "" {
				total.SourceURL = estimate.SourceURL
			}
			if total.ObservedAt == nil {
				total.ObservedAt = estimate.ObservedAt
			}
		}
		if total.TotalCostUSD > 0 {
			return total
		}
	}

	usage := usageFromSummary(job.Summary)
	if usage == nil {
		return nil
	}
	provider, model := pricingLookupContext(job, usage)
	if provider == "" || model == "" {
		return nil
	}
	usage.Provider = provider
	usage.Model = model
	return pricing.Estimate(*usage, pricing.Resolve(s.Config, provider, model))
}

func pricingLookupContext(job core.JobRecord, usage *core.UsageReport) (string, string) {
	provider := ""
	model := ""
	if usage != nil {
		provider = usage.Provider
		model = usage.Model
	}
	if provider == "" {
		provider = summaryString(job.Summary, "provider")
	}
	if model == "" {
		model = summaryString(job.Summary, "model")
	}
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if provider == "" {
			provider = parts[0]
		}
		model = parts[1]
	}
	if provider == "" {
		switch job.Adapter {
		case "codex":
			provider = "openai"
		case "claude":
			provider = "anthropic"
		case "gemini":
			provider = "google"
		}
	}
	return strings.ToLower(provider), strings.ToLower(model)
}

func costMap(cost core.CostEstimate) map[string]any {
	result := map[string]any{
		"currency":                cost.Currency,
		"input_cost_usd":          cost.InputCostUSD,
		"output_cost_usd":         cost.OutputCostUSD,
		"cached_input_cost_usd":   cost.CachedInputCostUSD,
		"cache_read_cost_usd":     cost.CacheReadCostUSD,
		"cache_creation_cost_usd": cost.CacheCreationCostUSD,
		"total_cost_usd":          cost.TotalCostUSD,
		"estimated":               cost.Estimated,
		"source":                  cost.Source,
		"source_url":              cost.SourceURL,
	}
	if cost.ObservedAt != nil {
		result["observed_at"] = cost.ObservedAt.Format(time.RFC3339Nano)
	}
	return result
}

func summaryCost(summary map[string]any, key string) *core.CostEstimate {
	if summary == nil {
		return nil
	}
	raw, ok := summary[key].(map[string]any)
	if !ok {
		return nil
	}
	cost := &core.CostEstimate{
		Currency:             summaryString(raw, "currency"),
		InputCostUSD:         summaryFloat64(raw, "input_cost_usd"),
		OutputCostUSD:        summaryFloat64(raw, "output_cost_usd"),
		CachedInputCostUSD:   summaryFloat64(raw, "cached_input_cost_usd"),
		CacheReadCostUSD:     summaryFloat64(raw, "cache_read_cost_usd"),
		CacheCreationCostUSD: summaryFloat64(raw, "cache_creation_cost_usd"),
		TotalCostUSD:         summaryFloat64(raw, "total_cost_usd"),
		Estimated:            summaryBool(raw, "estimated"),
		Source:               summaryString(raw, "source"),
		SourceURL:            summaryString(raw, "source_url"),
		ObservedAt:           summaryTime(raw, "observed_at"),
	}
	if cost.Currency == "" {
		cost.Currency = "USD"
	}
	if cost.TotalCostUSD <= 0 {
		return nil
	}
	return cost
}

func summaryInt64(summary map[string]any, key string) int64 {
	if summary == nil {
		return 0
	}
	value := summary[key]
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func summaryFloat64(summary map[string]any, key string) float64 {
	if summary == nil {
		return 0
	}
	value := summary[key]
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func summaryTime(summary map[string]any, key string) *time.Time {
	if summary == nil {
		return nil
	}
	value, _ := summary[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func summaryBool(summary map[string]any, key string) bool {
	if summary == nil {
		return false
	}
	value, _ := summary[key].(bool)
	return value
}
