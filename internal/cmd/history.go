// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/spf13/cobra"
)

func History() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "history [flags] [DAG name]",
			Short: "Display DAG run history",
			Long: `Display execution history of DAG runs with filtering and formatting options.

This command retrieves and displays historical DAG run information from local storage,
allowing you to query by various criteria including time range, status, labels, and run ID.

Date/Time Filtering:
  --from, --to      Absolute date range in UTC (formats: 2006-01-02 or 2006-01-02T15:04:05Z)
  --last            Relative time period (examples: 7d, 24h, 1w, 30d)
                    Note: --last cannot be combined with --from or --to

Status Filtering:
  --status          Filter by execution status. Accepts a single status or comma-separated statuses
                    (running, succeeded, failed, aborted, queued, waiting, rejected, not_started, partially_succeeded)

Other Filters:
  --labels          Filter by DAG labels (comma-separated, AND logic)
  --tags            Deprecated alias for --labels
  --run-id          Filter by run ID (partial match supported)

Output Control:
  --format          Output format: table (default) or json
  --limit           Maximum number of results to display (default: 100, max: 1000)

Default Behavior:
  Without date filters, displays runs from the last 30 days, newest first.
  All timestamps are displayed in UTC timezone.

Examples:
  dagu history                                # All runs from last 30 days
  dagu history my-dag                         # All runs for specific DAG
  dagu history --from 2026-01-01              # Runs since date
  dagu history --last 7d                      # Last 7 days
  dagu history --status failed                # Only failed runs
  dagu history --status running,queued        # Running or queued runs
  dagu history --format json                  # JSON output
  dagu history --labels "prod,critical"       # Filter by labels (AND logic)
  dagu history --limit 50                     # Limit to 50 results
  dagu history my-dag --status failed --last 24h  # Combined filters
`,
			Args: cobra.MaximumNArgs(1),
		},
		historyFlags,
		runHistory,
	)
}

var historyFlags = []commandLineFlag{
	historyFromFlag,
	historyToFlag,
	historyLastFlag,
	historyStatusFlag,
	historyRunIDFlag,
	historyLabelsFlag,
	historyTagsFlag,
	historyFormatFlag,
	historyLimitFlag,
}

func runHistory(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunHistory(ctx, args)
	}
	// Validate format early
	format, err := ctx.StringParam("format")
	if err != nil {
		return fmt.Errorf("failed to get format parameter: %w", err)
	}

	if err := validateFormat(format); err != nil {
		return err
	}

	// Parse and validate flags
	opts, err := buildHistoryOptions(ctx, args)
	if err != nil {
		return err
	}

	// Query DAG run history
	statuses, err := ctx.DAGRunStore.ListStatuses(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to query DAG run history: %w", err)
	}

	// Handle empty results
	if len(statuses) == 0 {
		return renderEmptyHistory(format)
	}

	// Render output based on format
	return renderHistory(format, statuses)
}

const noHistoryRunsMessage = "No DAG runs found matching the specified filters."

func renderEmptyHistory(format string) error {
	_, _ = fmt.Fprintln(os.Stderr, noHistoryRunsMessage)

	switch format {
	case "json", "csv":
		return renderHistory(format, nil)
	default:
		return nil
	}
}

// validateFormat checks if the output format is valid.
func validateFormat(format string) error {
	validFormats := map[string]bool{
		"":      true, // default
		"table": true,
		"json":  true,
		"csv":   true,
	}

	if !validFormats[format] {
		return fmt.Errorf("invalid format '%s'. Valid formats: table, json, csv", format)
	}
	return nil
}

// renderHistory renders DAG run history in the specified format.
func renderHistory(format string, statuses []*exec.DAGRunStatus) error {
	switch format {
	case "json":
		return renderHistoryJSON(statuses)
	case "csv":
		return renderHistoryCSV(statuses)
	default: // "table" or ""
		return renderHistoryTable(statuses)
	}
}

// buildHistoryOptions constructs query options from command-line flags.
func buildHistoryOptions(ctx *Context, args []string) ([]exec.ListDAGRunStatusesOption, error) {
	var opts []exec.ListDAGRunStatusesOption

	// DAG name filter
	if len(args) > 0 {
		dagName, err := extractDAGName(ctx, args[0])
		if err != nil {
			return nil, fmt.Errorf("failed to extract DAG name: %w", err)
		}
		opts = append(opts, exec.WithExactName(dagName))
	}

	// Date range filters
	dateOpts, err := buildDateRangeOptions(ctx)
	if err != nil {
		return nil, err
	}
	opts = append(opts, dateOpts...)

	// Status filter
	if statusOpt, err := buildStatusOption(ctx); err != nil {
		return nil, err
	} else if statusOpt != nil {
		opts = append(opts, statusOpt)
	}

	// Run ID filter
	if runIDOpt, err := buildRunIDOption(ctx); err != nil {
		return nil, err
	} else if runIDOpt != nil {
		opts = append(opts, runIDOpt)
	}

	// Labels filter
	if labelsOpt, err := buildLabelsOption(ctx); err != nil {
		return nil, err
	} else if labelsOpt != nil {
		opts = append(opts, labelsOpt)
	}

	// Limit filter
	if limitOpt, err := buildLimitOption(ctx); err != nil {
		return nil, err
	} else if limitOpt != nil {
		opts = append(opts, limitOpt)
	}

	return opts, nil
}

// buildDateRangeOptions constructs date range filtering options.
func buildDateRangeOptions(ctx *Context) ([]exec.ListDAGRunStatusesOption, error) {
	var opts []exec.ListDAGRunStatusesOption

	lastDuration, _ := ctx.StringParam("last")
	fromDate, _ := ctx.StringParam("from")
	toDate, _ := ctx.StringParam("to")

	// Validate conflicting flags
	if lastDuration != "" && (fromDate != "" || toDate != "") {
		return nil, fmt.Errorf("cannot use --last with --from or --to (conflicting time range specifications)")
	}

	if lastDuration != "" {
		// Handle relative duration
		duration, err := parseRelativeDuration(lastDuration)
		if err != nil {
			return nil, fmt.Errorf("invalid --last value '%s': %w. Valid formats: 7d, 24h, 1w, 30d", lastDuration, err)
		}
		fromTime := time.Now().UTC().Add(-duration)
		opts = append(opts, exec.WithFrom(exec.NewUTC(fromTime)))
		return opts, nil
	}

	// Handle absolute dates
	var fromTime, toTime time.Time
	var err error

	if fromDate != "" {
		fromTime, err = parseAbsoluteDateTime(fromDate)
		if err != nil {
			return nil, fmt.Errorf("invalid --from date '%s': %w. Expected format: 2006-01-02 or 2006-01-02T15:04:05Z", fromDate, err)
		}
		opts = append(opts, exec.WithFrom(exec.NewUTC(fromTime)))
	} else if toDate == "" {
		// Default: last 30 days if no date filters specified
		defaultFrom := time.Now().UTC().AddDate(0, 0, -30)
		opts = append(opts, exec.WithFrom(exec.NewUTC(defaultFrom)))
	}

	if toDate != "" {
		toTime, err = parseAbsoluteDateTime(toDate)
		if err != nil {
			return nil, fmt.Errorf("invalid --to date '%s': %w. Expected format: 2006-01-02 or 2006-01-02T15:04:05Z", toDate, err)
		}
		opts = append(opts, exec.WithTo(exec.NewUTC(toTime)))

		// Validate date range if both dates are provided
		if fromDate != "" && fromTime.After(toTime) {
			return nil, fmt.Errorf("--from date (%s) must be before --to date (%s)", fromDate, toDate)
		}
	}

	return opts, nil
}

// buildStatusOption constructs status filtering option.
func buildStatusOption(ctx *Context) (exec.ListDAGRunStatusesOption, error) {
	statusStr, err := ctx.StringParam("status")
	if err != nil {
		return nil, fmt.Errorf("failed to get 'status' parameter: %w", err)
	}

	if statusStr == "" {
		return nil, nil
	}

	statuses, err := parseStatuses(statusStr)
	if err != nil {
		return nil, err
	}

	return exec.WithStatuses(statuses), nil
}

// buildRunIDOption constructs run ID filtering option.
func buildRunIDOption(ctx *Context) (exec.ListDAGRunStatusesOption, error) {
	runID, err := ctx.StringParam("run-id")
	if err != nil {
		return nil, fmt.Errorf("failed to get 'run-id' parameter: %w", err)
	}

	if runID == "" {
		return nil, nil
	}

	return exec.WithDAGRunID(runID), nil
}

// buildLabelsOption constructs labels filtering option.
func buildLabelsOption(ctx *Context) (exec.ListDAGRunStatusesOption, error) {
	labelsStr, err := labelsParam(ctx)
	if err != nil {
		return nil, err
	}

	if labelsStr == "" {
		return nil, nil
	}

	if labels := parseLabels(labelsStr); len(labels) > 0 {
		return exec.WithLabels(labels), nil
	}

	return nil, nil
}

// buildLimitOption constructs limit option with validation.
func buildLimitOption(ctx *Context) (exec.ListDAGRunStatusesOption, error) {
	const (
		defaultLimit = 100
		maxLimit     = 1000
	)

	limitStr, err := ctx.StringParam("limit")
	if err != nil {
		return nil, fmt.Errorf("failed to get 'limit' parameter: %w", err)
	}

	limit := defaultLimit
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit < 1 {
			return nil, fmt.Errorf("invalid --limit value '%s': must be a positive integer", limitStr)
		}
		if parsedLimit > maxLimit {
			fmt.Fprintf(os.Stderr, "Warning: limit capped at %d (requested: %d)\n", maxLimit, parsedLimit)
			limit = maxLimit
		} else {
			limit = parsedLimit
		}
	}

	return exec.WithLimit(limit), nil
}

// parseRelativeDuration parses relative time duration strings like "7d", "24h", "1w".
func parseRelativeDuration(s string) (time.Duration, error) {
	const expectedFormat = "invalid format (expected: 7d, 24h, 1w)"

	if len(s) < 2 {
		return 0, errors.New(expectedFormat)
	}

	// Extract number and unit parts
	valueStr := s[:len(s)-1]
	unit := s[len(s)-1]

	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 0 {
		return 0, errors.New(expectedFormat)
	}

	// Convert to duration based on unit
	unitMultipliers := map[byte]time.Duration{
		'h': time.Hour,
		'd': 24 * time.Hour,
		'w': 7 * 24 * time.Hour,
	}

	if multiplier, ok := unitMultipliers[unit]; ok {
		return time.Duration(value) * multiplier, nil
	}

	return 0, errors.New(expectedFormat)
}

// parseAbsoluteDateTime parses absolute date/time strings in UTC.
// Supported formats: "2006-01-02" (midnight UTC) and "2006-01-02T15:04:05Z" (RFC3339).
func parseAbsoluteDateTime(s string) (time.Time, error) {
	// Define supported formats in order of preference
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			// Ensure UTC timezone for consistency
			return t.In(time.UTC), nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported date format")
}

// parseStatus converts status string to core.Status with validation.
func parseStatus(s string) (core.Status, error) {
	normalized := strings.ToLower(strings.TrimSpace(s))

	// Map of all accepted status values to their core.Status equivalents
	statusMap := map[string]core.Status{
		// Canonical names
		"not_started":         core.NotStarted,
		"running":             core.Running,
		"succeeded":           core.Succeeded,
		"failed":              core.Failed,
		"aborted":             core.Aborted,
		"queued":              core.Queued,
		"partially_succeeded": core.PartiallySucceeded,
		"waiting":             core.Waiting,
		"rejected":            core.Rejected,

		// Common aliases
		"notstarted":         core.NotStarted,
		"success":            core.Succeeded,
		"failure":            core.Failed,
		"canceled":           core.Aborted,
		"cancelled":          core.Aborted,
		"cancel":             core.Aborted,
		"partiallysucceeded": core.PartiallySucceeded,
	}

	if status, ok := statusMap[normalized]; ok {
		return status, nil
	}

	return core.NotStarted, fmt.Errorf("invalid status '%s'. Valid values: running, succeeded, failed, aborted, queued, waiting, rejected, not_started, partially_succeeded", s)
}

// parseStatuses converts a comma-separated status string to core.Status values.
func parseStatuses(s string) ([]core.Status, error) {
	parts := strings.Split(s, ",")
	statuses := make([]core.Status, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		status, err := parseStatus(trimmed)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("invalid status '%s'. Valid values: running, succeeded, failed, aborted, queued, waiting, rejected, not_started, partially_succeeded", s)
	}
	return statuses, nil
}

// parseLabels splits comma-separated labels and trims whitespace.
func parseLabels(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// renderHistoryTable displays DAG run history as an aligned table.
func renderHistoryTable(statuses []*exec.DAGRunStatus) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer func() {
		_ = w.Flush()
	}()

	// Header
	if _, err := fmt.Fprintln(w, "DAG NAME\tRUN ID\tSTATUS\tSTARTED (UTC)\tDURATION\tPARAMS"); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Rows
	for _, status := range statuses {
		dagName := status.Name
		runID := status.DAGRunID
		statusText := formatStatusText(status.Status)
		startedAt := formatTimestamp(status.StartedAt)
		duration := formatDuration(status)
		params := formatParams(status.Params)

		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			dagName,
			runID,
			statusText,
			startedAt,
			duration,
			params,
		); err != nil {
			return fmt.Errorf("failed to write row: %w", err)
		}
	}

	return nil
}

// renderHistoryCSV displays DAG run history as comma-separated values.
func renderHistoryCSV(statuses []*exec.DAGRunStatus) error {
	const csvHeader = "DAG NAME,RUN ID,STATUS,STARTED (UTC),DURATION,PARAMS"

	// Write header
	if _, err := fmt.Fprintln(os.Stdout, csvHeader); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write rows
	for _, status := range statuses {
		row := formatCSVRow(status)
		if _, err := fmt.Fprintln(os.Stdout, row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// formatCSVRow formats a single DAG run status as a CSV row.
func formatCSVRow(status *exec.DAGRunStatus) string {
	fields := []string{
		escapeCSV(status.Name),
		escapeCSV(status.DAGRunID),
		escapeCSV(formatStatusText(status.Status)),
		escapeCSV(formatTimestamp(status.StartedAt)),
		escapeCSV(formatDuration(status)),
		escapeCSV(formatParams(status.Params)),
	}
	return strings.Join(fields, ",")
}

// escapeCSV escapes a string for CSV output according to RFC 4180.
// Values containing special characters (comma, quote, newline) are quoted.
// Internal quotes are escaped by doubling.
func escapeCSV(s string) string {
	if !needsCSVQuoting(s) {
		return s
	}

	// Escape quotes by doubling them, then wrap in quotes
	escaped := strings.ReplaceAll(s, `"`, `""`)
	return `"` + escaped + `"`
}

// needsCSVQuoting checks if a string requires quoting in CSV format.
func needsCSVQuoting(s string) bool {
	return strings.ContainsAny(s, ",\"\n\r")
}

// renderHistoryJSON displays DAG run history as JSON.
func renderHistoryJSON(statuses []*exec.DAGRunStatus) error {
	type historyEntry struct {
		Name       string   `json:"name"`
		DAGRunID   string   `json:"dagRunId"`
		Status     string   `json:"status"`
		StartedAt  string   `json:"startedAt,omitempty"`
		FinishedAt string   `json:"finishedAt,omitempty"`
		Duration   string   `json:"duration,omitempty"`
		Params     string   `json:"params,omitempty"`
		Labels     []string `json:"labels,omitempty"`
		WorkerID   string   `json:"workerId,omitempty"`
		Error      string   `json:"error,omitempty"`
	}

	entries := make([]historyEntry, 0, len(statuses))
	for _, status := range statuses {
		entry := historyEntry{
			Name:       status.Name,
			DAGRunID:   status.DAGRunID,
			Status:     status.Status.String(),
			StartedAt:  status.StartedAt,
			FinishedAt: status.FinishedAt,
			Duration:   formatDuration(status),
			Params:     status.Params,
			Labels:     status.Labels,
			WorkerID:   status.WorkerID,
			Error:      status.Error,
		}
		entries = append(entries, entry)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(entries)
}

// formatStatusText converts core.Status to human-readable text.
func formatStatusText(status core.Status) string {
	switch status {
	case core.NotStarted:
		return "Not Started"
	case core.Running:
		return "Running"
	case core.Succeeded:
		return "Succeeded"
	case core.Failed:
		return "Failed"
	case core.Aborted:
		return "Aborted"
	case core.Queued:
		return "Queued"
	case core.PartiallySucceeded:
		return "Partially Succeeded"
	case core.Waiting:
		return "Waiting"
	case core.Rejected:
		return "Rejected"
	default:
		return status.String()
	}
}

// formatTimestamp formats a timestamp string for display in UTC.
// Handles empty strings and invalid formats gracefully.
func formatTimestamp(ts string) string {
	if ts == "" || ts == "-" {
		return "-"
	}

	t := parseTimeString(ts)
	if t.IsZero() {
		return ts // Return as-is if unparsable
	}

	return t.Format("2006-01-02 15:04:05")
}

// formatDuration calculates and formats the duration of a DAG run.
// For running DAGs, shows elapsed time. For completed DAGs, shows total duration.
func formatDuration(status *exec.DAGRunStatus) string {
	if status.StartedAt == "" {
		return "-"
	}

	startTime := parseTimeString(status.StartedAt)
	if startTime.IsZero() {
		return "-"
	}

	endTime := time.Now().UTC()
	if status.FinishedAt != "" {
		if parsed := parseTimeString(status.FinishedAt); !parsed.IsZero() {
			endTime = parsed
		}
	}

	return formatDurationHuman(endTime.Sub(startTime))
}

// parseTimeString attempts to parse a time string in common formats.
func parseTimeString(ts string) time.Time {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// formatDurationHuman formats a duration in a human-readable way.
// Examples: "5s", "2m30s", "1h5m", "2d3h"
func formatDurationHuman(d time.Duration) string {
	if d < 0 {
		return "-"
	}

	if d < time.Second {
		return "< 1s"
	}

	// Calculate time components
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	// Build duration string with two most significant components
	switch {
	case days > 0:
		return formatTimeComponents(days, "d", hours, "h")
	case hours > 0:
		return formatTimeComponents(hours, "h", minutes, "m")
	case minutes > 0:
		return formatTimeComponents(minutes, "m", seconds, "s")
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// formatTimeComponents formats up to two time components.
func formatTimeComponents(major int, majorUnit string, minor int, minorUnit string) string {
	if minor > 0 {
		return fmt.Sprintf("%d%s%d%s", major, majorUnit, minor, minorUnit)
	}
	return fmt.Sprintf("%d%s", major, majorUnit)
}

// formatParams formats parameters for table display.
// Truncates long parameter strings with ellipsis.
func formatParams(params string) string {
	if params == "" {
		return "-"
	}

	// Truncate if too long (max 40 chars for table readability)
	maxLen := 40
	if len(params) > maxLen {
		return params[:maxLen-3] + "..."
	}

	return params
}
