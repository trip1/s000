package server

import (
	"encoding/json"
	"net/http"
)

func lifecycleConfigDebug(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if opts.Lifecycle == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lifecycle worker is not configured"})
			return
		}

		snapshot := opts.Lifecycle.Snapshot()
		type rule struct {
			Prefix      string `json:"prefix"`
			ExpireAfter string `json:"expire_after"`
		}
		rules := make([]rule, 0, len(snapshot.Rules))
		for _, lifecycleRule := range snapshot.Rules {
			rules = append(rules, rule{Prefix: lifecycleRule.Prefix, ExpireAfter: lifecycleRule.ExpireAfter.String()})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"rules":         rules,
			"interval":      snapshot.Interval.String(),
			"batch_size":    snapshot.BatchSize,
			"max_retries":   snapshot.MaxRetries,
			"retry_backoff": snapshot.RetryBackoff.String(),
			"dry_run":       snapshot.DryRun,
		})
	}
}

func lifecycleMetricsDebug(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if opts.Lifecycle == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lifecycle worker is not configured"})
			return
		}

		metrics := opts.Lifecycle.Snapshot().Metrics
		writeJSON(w, http.StatusOK, map[string]any{
			"runs":              metrics.Runs,
			"scanned_total":     metrics.ScannedTotal,
			"eligible_total":    metrics.EligibleTotal,
			"deleted_total":     metrics.DeletedTotal,
			"failed_total":      metrics.FailedTotal,
			"last_run_duration": metrics.LastRunDuration.String(),
			"last_run_dry_run":  metrics.LastRunDryRun,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
