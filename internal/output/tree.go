// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Tree drawing characters using Unicode box-drawing characters.
const (
	TreeBranch     = "├─"
	TreeLastBranch = "└─"
	TreePipe       = "│ "
	TreeSpace      = "  "
)

const (
	// DefaultMaxOutputLines is the default number of lines to show for stdout/stderr.
	DefaultMaxOutputLines = 50
	// DefaultMaxWidth is the maximum line width before wrapping.
	DefaultMaxWidth = 80
)

// Config holds configuration for tree rendering.
type Config struct {
	ColorEnabled   bool // Enable colored output using ANSI escape codes.
	ShowStdout     bool // Display stdout content in the tree.
	ShowStderr     bool // Display stderr content in the tree.
	MaxOutputLines int  // Limit stdout/stderr to last N lines (0 = unlimited).
	MaxWidth       int  // Maximum line width before wrapping (0 = no wrapping).
}

// DefaultConfig returns the default configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ColorEnabled:   true,
		ShowStdout:     true,
		ShowStderr:     true,
		MaxOutputLines: DefaultMaxOutputLines,
		MaxWidth:       DefaultMaxWidth,
	}
}

// Renderer renders DAG execution status as a tree structure.
type Renderer struct {
	config Config
}

// text applies a soft light blue color (ANSI 256 color 110) for visual distinction.
func (r *Renderer) text(s string) string {
	if !r.config.ColorEnabled {
		return s
	}
	return "\033[38;5;110m" + s + "\033[0m"
}

// gray applies a medium gray color (ANSI 256 color 245) for secondary information.
func (r *Renderer) gray(s string) string {
	if !r.config.ColorEnabled {
		return s
	}
	return "\033[38;5;245m" + s + "\033[0m"
}

// branchChar returns the appropriate tree branch character based on position.
func branchChar(isLast bool) string {
	if isLast {
		return TreeLastBranch
	}
	return TreeBranch
}

// childPrefix returns the prefix for child elements based on parent position.
func childPrefix(prefix string, isLast bool) string {
	if isLast {
		return prefix + TreeSpace
	}
	return prefix + TreePipe
}

// NewRenderer creates a new tree renderer with the given configuration.
func NewRenderer(config Config) *Renderer {
	return &Renderer{config: config}
}

// RenderDAGStatus renders the complete DAG status as a tree structure.
func (r *Renderer) RenderDAGStatus(dag *ir.DAG, status *ir.DAGRunStatus) string {
	var buf strings.Builder

	buf.WriteString(r.renderHeader(status))
	buf.WriteString("\n\n")
	buf.WriteString(r.renderDAGLine(dag, status))
	buf.WriteString("\n")

	nodes := status.NodesInRunOrder()

	if status.Log != "" {
		buf.WriteString(r.renderSchedulerLog(status.Log, len(nodes) > 0))
	}

	if len(nodes) > 0 {
		buf.WriteString("│\n")
	}

	for i, node := range nodes {
		buf.WriteString(r.renderStep(node, i == len(nodes)-1, ""))
	}

	buf.WriteString(r.renderFinalStatus(status))

	return buf.String()
}

// renderHeader renders the status header line with status text and timestamp.
func (r *Renderer) renderHeader(status *ir.DAGRunStatus) string {
	startTime := status.StartedAt
	if startTime == "" || startTime == "-" {
		startTime = time.Now().Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%s - %s", StatusText(status.Status), startTime)
}

// renderDAGLine renders the DAG name with total duration.
func (r *Renderer) renderDAGLine(dag *ir.DAG, status *ir.DAGRunStatus) string {
	duration := r.calculateDuration(status.StartedAt, status.FinishedAt, status.Status)
	if duration != "" {
		return fmt.Sprintf("dag: %s %s", dag.Name, r.gray("("+duration+")"))
	}
	return fmt.Sprintf("dag: %s", dag.Name)
}

// renderStep renders a single step with its commands and output content.
func (r *Renderer) renderStep(node *ir.Node, isLast bool, prefix string) string {
	var buf strings.Builder

	buf.WriteString(r.renderStepHeader(node, isLast, prefix))

	// A skipped build node still has a decision explaining why execution
	// did not begin.
	if isSkippedStatus(node.Status) {
		if node.Build != nil {
			cPrefix := childPrefix(prefix, isLast)
			buf.WriteString(r.renderBuild(node.Build, true, cPrefix))
		}
		if !isLast {
			buf.WriteString(prefix + TreePipe + "\n")
		}
		return buf.String()
	}

	buf.WriteString(r.renderStepContent(node, isLast, prefix))

	if !isLast {
		buf.WriteString(prefix + TreePipe + "\n")
	}

	return buf.String()
}

// renderStepHeader renders the step name line with duration and status.
func (r *Renderer) renderStepHeader(node *ir.Node, isLast bool, prefix string) string {
	lineParts := []string{r.text(node.Step.Name)}

	if shouldShowDuration(node.Status) {
		if duration := r.calculateNodeDuration(node); duration != "" {
			lineParts = append(lineParts, r.gray("("+duration+")"))
		}
	}

	if label := r.getStatusLabel(node.Status); label != "" {
		lineParts = append(lineParts, r.text(label))
	}

	return prefix + branchChar(isLast) + strings.Join(lineParts, " ") + "\n"
}

// renderStepContent renders commands, outputs, sub-runs, and errors for a step.
func (r *Renderer) renderStepContent(node *ir.Node, isLast bool, prefix string) string {
	var buf strings.Builder
	cPrefix := childPrefix(prefix, isLast)

	hasOutput := r.hasOutput(node)
	hasError := node.Error != "" && node.Status == ir.NodeFailed
	hasSubRuns := len(node.SubRuns) > 0
	hasHumanTask := node.Status == ir.NodeWaiting && node.Step.HumanTask != nil
	hasBuild := node.Build != nil

	hasFollowingContent := hasOutput || hasError || hasSubRuns || hasHumanTask || hasBuild
	wroteField := r.renderCommands(&buf, node, cPrefix, hasFollowingContent)

	if hasBuild {
		r.addFieldSpacing(&buf, wroteField, cPrefix)
		buf.WriteString(r.renderBuild(node.Build, !hasOutput && !hasError && !hasSubRuns && !hasHumanTask, cPrefix))
		wroteField = true
	}

	if hasHumanTask {
		r.addFieldSpacing(&buf, wroteField, cPrefix)
		buf.WriteString(r.renderHumanTask(node, !hasOutput && !hasError && !hasSubRuns, cPrefix))
		wroteField = true
	}

	if hasOutput {
		r.addFieldSpacing(&buf, wroteField, cPrefix)
		buf.WriteString(r.renderOutputs(node, !hasError && !hasSubRuns, cPrefix))
		wroteField = true
	}

	if hasSubRuns {
		r.addFieldSpacing(&buf, wroteField, cPrefix)
		buf.WriteString(r.renderSubRuns(node.SubRuns, !hasError, cPrefix))
		wroteField = true
	}

	if hasError {
		r.addFieldSpacing(&buf, wroteField, cPrefix)
		buf.WriteString(r.renderError(node.Error, cPrefix))
	}

	return buf.String()
}

func (r *Renderer) renderBuild(value *ir.BuildExecution, isLast bool, prefix string) string {
	line := fmt.Sprintf("build: %s (%s)", value.Decision, value.Reason)
	if value.Detail != "" {
		line += " - " + value.Detail
	}
	if !value.ProducerRun.Zero() {
		line += "; producer: " + value.ProducerRun.String()
	}
	return r.renderCommandLine(line, isLast, prefix)
}

// renderCommands renders step commands and returns true if any were written.
func (r *Renderer) renderCommands(buf *strings.Builder, node *ir.Node, cPrefix string, hasFollowingContent bool) bool {
	if len(node.Step.Commands) > 0 {
		for i, cmd := range node.Step.Commands {
			isLastCmd := i == len(node.Step.Commands)-1 && !hasFollowingContent
			buf.WriteString(r.renderCommandLine(cmd.String(), isLastCmd, cPrefix))
		}
		return true
	}

	// Handle legacy single command format
	if cmdStr := r.getLegacyCommand(node); cmdStr != "" {
		buf.WriteString(r.renderCommandLine(cmdStr, !hasFollowingContent, cPrefix))
		return true
	}

	return false
}

func (r *Renderer) renderHumanTask(node *ir.Node, isLastSection bool, prefix string) string {
	details := []string{
		"step id: " + node.Step.ID,
		"prompt: " + node.Step.HumanTask.Prompt,
	}
	if len(node.Step.HumanTask.Form) > 0 {
		details = append(details, "form: "+string(node.Step.HumanTask.Form))
	}

	var buf strings.Builder
	var lines []string
	for _, detail := range details {
		detail = strings.ReplaceAll(detail, "\r\n", "\n")
		detail = strings.ReplaceAll(detail, "\r", "\n")
		lines = append(lines, strings.Split(detail, "\n")...)
	}
	for i, line := range lines {
		isLast := isLastSection && i == len(lines)-1
		if len(line) != len([]rune(line)) {
			buf.WriteString(prefix + branchChar(isLast) + r.text(line) + "\n")
			continue
		}
		buf.WriteString(r.renderCommandLine(line, isLast, prefix))
	}
	return buf.String()
}

// addFieldSpacing adds vertical spacing between fields if needed.
func (r *Renderer) addFieldSpacing(buf *strings.Builder, wroteField bool, cPrefix string) {
	if wroteField {
		buf.WriteString(cPrefix + "│\n")
	}
}

// isSkippedStatus returns true for statuses that should not show details.
func isSkippedStatus(status ir.NodeStatus) bool {
	return status == ir.NodeSkipped || status == ir.NodeAborted || status == ir.NodeNotStarted
}

// shouldShowDuration returns true for statuses that should display duration.
func shouldShowDuration(status ir.NodeStatus) bool {
	return status == ir.NodeSucceeded || status == ir.NodeFailed ||
		status == ir.NodeRunning || status == ir.NodeRetrying ||
		status == ir.NodePartiallySucceeded
}

// getStatusLabel returns a text label for the node status.
func (r *Renderer) getStatusLabel(status ir.NodeStatus) string {
	if status == ir.NodeNotStarted {
		return ""
	}
	return "[" + status.String() + "]"
}

// hasOutput checks if the node has any stdout or stderr content.
func (r *Renderer) hasOutput(node *ir.Node) bool {
	if !r.config.ShowStdout && !r.config.ShowStderr {
		return false
	}
	if r.config.ShowStdout && node.Stdout != "" {
		lines, _, _ := ReadLogFileTail(node.Stdout, 1)
		if len(lines) > 0 {
			return true
		}
	}
	if r.config.ShowStderr && node.Stderr != "" {
		lines, _, _ := ReadLogFileTail(node.Stderr, 1)
		if len(lines) > 0 {
			return true
		}
	}
	return false
}

// renderCommandLine renders a single command line with optional wrapping.
func (r *Renderer) renderCommandLine(cmdStr string, isLast bool, prefix string) string {
	branch := branchChar(isLast)

	if r.config.MaxWidth > 0 {
		prefixLen := len(prefix) + len(branch)
		maxContentWidth := r.config.MaxWidth - prefixLen
		if maxContentWidth > 20 && len(cmdStr) > maxContentWidth {
			return r.renderWrappedLine(cmdStr, branch, isLast, prefix)
		}
	}

	return prefix + branch + r.text(cmdStr) + "\n"
}

// renderWrappedLine renders a line with word wrapping.
func (r *Renderer) renderWrappedLine(text string, branch string, isLast bool, prefix string) string {
	var buf strings.Builder

	maxContentWidth := max(r.config.MaxWidth-len(prefix)-len(branch), 20)
	contPrefix := childPrefix(prefix, isLast)
	lines := wrapText(text, maxContentWidth)

	for i, line := range lines {
		if i == 0 {
			buf.WriteString(prefix + branch + r.text(line) + "\n")
		} else {
			buf.WriteString(contPrefix + "  " + r.text(line) + "\n")
		}
	}

	return buf.String()
}

// wrapText wraps text at word boundaries to fit within maxWidth.
func wrapText(text string, maxWidth int) []string {
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	var currentLine strings.Builder

	for _, word := range words {
		if len(word) > maxWidth {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
			}
			for len(word) > maxWidth {
				lines = append(lines, word[:maxWidth])
				word = word[maxWidth:]
			}
			if len(word) > 0 {
				currentLine.WriteString(word)
			}
			continue
		}

		if currentLine.Len() == 0 {
			currentLine.WriteString(word)
		} else if currentLine.Len()+1+len(word) <= maxWidth {
			currentLine.WriteString(" ")
			currentLine.WriteString(word)
		} else {
			lines = append(lines, currentLine.String())
			currentLine.Reset()
			currentLine.WriteString(word)
		}
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// getLegacyCommand extracts command string from legacy step format.
func (r *Renderer) getLegacyCommand(node *ir.Node) string {
	if node.Step.CmdWithArgs != "" {
		return node.Step.CmdWithArgs
	}
	if node.Step.Command != "" {
		cmd := node.Step.Command
		if len(node.Step.Args) > 0 {
			cmd += " " + strings.Join(node.Step.Args, " ")
		}
		return cmd
	}
	return ""
}

// renderOutputs renders stdout and stderr for a node.
func (r *Renderer) renderOutputs(node *ir.Node, isLast bool, prefix string) string {
	var buf strings.Builder

	hasStdoutContent := r.config.ShowStdout && r.hasLogContent(node.Stdout)
	hasStderrContent := r.config.ShowStderr && r.hasLogContent(node.Stderr)

	if hasStdoutContent {
		buf.WriteString(r.renderOutput("stdout", node.Stdout, isLast && !hasStderrContent, prefix))
	}
	if hasStderrContent {
		buf.WriteString(r.renderOutput("stderr", node.Stderr, isLast, prefix))
	}

	return buf.String()
}

// hasLogContent checks if a log file has any content.
func (r *Renderer) hasLogContent(path string) bool {
	if path == "" {
		return false
	}
	lines, _, _ := ReadLogFileTail(path, 1)
	return len(lines) > 0
}

// renderOutput renders a single output stream (stdout or stderr) with content.
func (r *Renderer) renderOutput(label string, filePath string, isLast bool, prefix string) string {
	lines, truncated, err := ReadLogFileTail(filePath, r.config.MaxOutputLines)
	if err != nil || len(lines) == 0 {
		return ""
	}

	var buf strings.Builder
	branch := branchChar(isLast)
	contPrefix := childPrefix(prefix, isLast)
	maxContentWidth := max(r.config.MaxWidth-len(contPrefix)-2, 20)

	buf.WriteString(prefix + branch + r.text(label+": ") + r.gray(filePath) + "\n")

	if truncated > 0 {
		buf.WriteString(contPrefix + "  " + r.gray(fmt.Sprintf("... (%d more lines)", truncated)) + "\n")
	}

	for _, line := range lines {
		r.writeContentLine(&buf, line, contPrefix, maxContentWidth)
	}

	return buf.String()
}

// writeContentLine writes a content line with optional wrapping.
func (r *Renderer) writeContentLine(buf *strings.Builder, line string, contPrefix string, maxWidth int) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	if len(trimmed) > maxWidth {
		for _, wl := range wrapText(trimmed, maxWidth) {
			buf.WriteString(contPrefix + "  " + r.text(wl) + "\n")
		}
	} else {
		buf.WriteString(contPrefix + "  " + r.text(trimmed) + "\n")
	}
}

// renderSubRuns renders references to sub-DAG runs.
func (r *Renderer) renderSubRuns(subRuns []ir.SubDAGRun, isLastSection bool, prefix string) string {
	var buf strings.Builder

	for i, sub := range subRuns {
		isLastItem := i == len(subRuns)-1 && isLastSection
		subInfo := fmt.Sprintf("subdag: %s", sub.DAGRunID)
		if sub.Params != "" {
			subInfo += fmt.Sprintf(" [%s]", sub.Params)
		}
		buf.WriteString(prefix + branchChar(isLastItem) + r.text(subInfo) + "\n")
	}

	return buf.String()
}

// renderError renders an error message with wrapping.
func (r *Renderer) renderError(errMsg string, prefix string) string {
	errStr := "error: " + cleanErrorMessage(errMsg)
	maxWidth := max(r.config.MaxWidth-len(prefix)-len(TreeLastBranch), 20)

	if len(errStr) <= maxWidth {
		return prefix + TreeLastBranch + r.text(errStr) + "\n"
	}

	var buf strings.Builder
	for i, line := range wrapText(errStr, maxWidth) {
		if i == 0 {
			buf.WriteString(prefix + TreeLastBranch + r.text(line) + "\n")
		} else {
			buf.WriteString(prefix + "    " + r.text(line) + "\n")
		}
	}
	return buf.String()
}

// cleanErrorMessage removes the "recent stderr (tail):" section from error messages.
func cleanErrorMessage(errMsg string) string {
	const stderrMarker = "recent stderr (tail):"
	if before, _, ok := strings.Cut(errMsg, "\n"+stderrMarker); ok {
		return strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(errMsg, stderrMarker); ok {
		return strings.TrimSpace(before)
	}
	return errMsg
}

// renderFinalStatus renders the final result line at the bottom of the tree.
func (r *Renderer) renderFinalStatus(status *ir.DAGRunStatus) string {
	label := "Result"
	if status.Status == ir.Running {
		label = "Status"
	}
	return fmt.Sprintf("\n%s: %s\n", label, StatusText(status.Status))
}

// renderSchedulerLog renders the DAG-level scheduler log path.
func (r *Renderer) renderSchedulerLog(logPath string, hasSteps bool) string {
	return branchChar(!hasSteps) + r.text("log: ") + r.gray(logPath) + "\n"
}

// calculateDuration calculates the duration string between start and finish times.
func (r *Renderer) calculateDuration(startedAt, finishedAt string, status ir.Status) string {
	if startedAt == "" || startedAt == "-" {
		return ""
	}

	start, err := stringutil.ParseTime(startedAt)
	if err != nil {
		return ""
	}

	var end time.Time
	if finishedAt != "" && finishedAt != "-" {
		end, err = stringutil.ParseTime(finishedAt)
		if err != nil {
			end = time.Now()
		}
	} else if status == ir.Running {
		end = time.Now()
	} else {
		return ""
	}

	return stringutil.FormatDuration(end.Sub(start))
}

// calculateNodeDuration calculates duration for a specific node.
func (r *Renderer) calculateNodeDuration(node *ir.Node) string {
	nodeStatus := nodeStatusToStatus(node.Status)
	return r.calculateDuration(node.StartedAt, node.FinishedAt, nodeStatus)
}

// nodeStatusToStatus converts NodeStatus to Status for duration calculation.
func nodeStatusToStatus(ns ir.NodeStatus) ir.Status {
	switch ns {
	case ir.NodeRunning:
		return ir.Running
	case ir.NodeRetrying:
		return ir.Running
	case ir.NodeSucceeded:
		return ir.Succeeded
	case ir.NodeFailed:
		return ir.Failed
	case ir.NodeAborted:
		return ir.Aborted
	case ir.NodePartiallySucceeded:
		return ir.PartiallySucceeded
	case ir.NodeWaiting:
		return ir.Waiting
	case ir.NodeRejected:
		return ir.Rejected
	case ir.NodeNotStarted, ir.NodeSkipped:
		return ir.NotStarted
	default:
		return ir.NotStarted
	}
}
