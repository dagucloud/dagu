// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package output

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
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
func (r *Renderer) RenderDAGStatus(dag *core.DAG, status *exec.DAGRunStatus) string {
	var buf strings.Builder

	buf.WriteString(r.renderHeader(status))
	buf.WriteString("\n\n")
	buf.WriteString(r.renderDAGLine(dag, status))
	buf.WriteString("\n")

	if status.Log != "" {
		buf.WriteString(r.renderSchedulerLog(status.Log, len(status.Nodes) > 0))
	}

	if len(status.Nodes) > 0 {
		buf.WriteString("│\n")
	}

	for i, node := range status.Nodes {
		buf.WriteString(r.renderStep(node, i == len(status.Nodes)-1, ""))
	}

	buf.WriteString(r.renderFinalStatus(status))

	return buf.String()
}

// renderHeader renders the status header line with status text and timestamp.
func (r *Renderer) renderHeader(status *exec.DAGRunStatus) string {
	startTime := status.StartedAt
	if startTime == "" || startTime == "-" {
		startTime = time.Now().Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%s - %s", StatusText(status.Status), startTime)
}

// renderDAGLine renders the DAG name with total duration.
func (r *Renderer) renderDAGLine(dag *core.DAG, status *exec.DAGRunStatus) string {
	duration := r.calculateDuration(status.StartedAt, status.FinishedAt, status.Status)
	if duration != "" {
		return fmt.Sprintf("dag: %s %s", dag.Name, r.gray("("+duration+")"))
	}
	return fmt.Sprintf("dag: %s", dag.Name)
}

// renderStep renders a single step with its commands and output content.
func (r *Renderer) renderStep(node *exec.Node, isLast bool, prefix string) string {
	var buf strings.Builder

	buf.WriteString(r.renderStepHeader(node, isLast, prefix))

	// Skip details for non-executed steps
	if isSkippedStatus(node.Status) {
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
func (r *Renderer) renderStepHeader(node *exec.Node, isLast bool, prefix string) string {
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
func (r *Renderer) renderStepContent(node *exec.Node, isLast bool, prefix string) string {
	var buf strings.Builder
	cPrefix := childPrefix(prefix, isLast)

	hasOutput := r.hasOutput(node)
	hasError := node.Error != "" && node.Status == core.NodeFailed
	hasSubRuns := len(node.SubRuns) > 0
	hasHumanTask := node.Status == core.NodeWaiting && node.Step.HumanTask != nil

	wroteField := r.renderCommands(&buf, node, cPrefix, hasOutput, hasError, hasSubRuns, hasHumanTask)

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

// renderCommands renders step commands and returns true if any were written.
func (r *Renderer) renderCommands(buf *strings.Builder, node *exec.Node, cPrefix string, hasOutput, hasError, hasSubRuns, hasHumanTask bool) bool {
	hasFollowingContent := hasOutput || hasError || hasSubRuns || hasHumanTask

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

func (r *Renderer) renderHumanTask(node *exec.Node, isLastSection bool, prefix string) string {
	details := []string{
		"step id: " + node.Step.ID,
		"prompt: " + node.HumanTaskPrompt,
	}
	if len(node.Step.HumanTask.Form) > 0 {
		details = append(details, "form: "+string(node.Step.HumanTask.Form))
	}

	var buf strings.Builder
	for i, detail := range details {
		isLast := isLastSection && i == len(details)-1
		buf.WriteString(r.renderCommandLine(detail, isLast, prefix))
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
func isSkippedStatus(status core.NodeStatus) bool {
	return status == core.NodeSkipped || status == core.NodeAborted || status == core.NodeNotStarted
}

// shouldShowDuration returns true for statuses that should display duration.
func shouldShowDuration(status core.NodeStatus) bool {
	return status == core.NodeSucceeded || status == core.NodeFailed ||
		status == core.NodeRunning || status == core.NodeRetrying ||
		status == core.NodePartiallySucceeded
}

// getStatusLabel returns a text label for the node status.
func (r *Renderer) getStatusLabel(status core.NodeStatus) string {
	if status == core.NodeNotStarted {
		return ""
	}
	return "[" + status.String() + "]"
}

// hasOutput checks if the node has any stdout or stderr content.
func (r *Renderer) hasOutput(node *exec.Node) bool {
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
		prefixLen := utf8.RuneCountInString(prefix) + utf8.RuneCountInString(branch)
		maxContentWidth := r.config.MaxWidth - prefixLen
		if maxContentWidth > 20 && (utf8.RuneCountInString(cmdStr) > maxContentWidth || strings.ContainsAny(cmdStr, "\r\n")) {
			return r.renderWrappedLine(cmdStr, branch, isLast, prefix)
		}
	}
	if strings.ContainsAny(cmdStr, "\r\n") {
		return r.renderWrappedLine(cmdStr, branch, isLast, prefix)
	}

	return prefix + branch + r.text(cmdStr) + "\n"
}

// renderWrappedLine renders a line with word wrapping.
func (r *Renderer) renderWrappedLine(text string, branch string, isLast bool, prefix string) string {
	var buf strings.Builder

	contPrefix := childPrefix(prefix, isLast)
	var lines []string
	if r.config.MaxWidth > 0 {
		maxContentWidth := max(r.config.MaxWidth-utf8.RuneCountInString(prefix)-utf8.RuneCountInString(branch), 20)
		lines = wrapText(text, maxContentWidth)
	} else {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		lines = strings.Split(text, "\n")
	}

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
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !strings.Contains(text, "\n") && utf8.RuneCountInString(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for logicalLine := range strings.SplitSeq(text, "\n") {
		lines = append(lines, wrapTextLine(logicalLine, maxWidth)...)
	}
	return lines
}

func wrapTextLine(text string, maxWidth int) []string {
	if utf8.RuneCountInString(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	var currentLine strings.Builder
	currentWidth := 0

	for _, word := range words {
		wordRunes := []rune(word)
		if len(wordRunes) > maxWidth {
			if currentLine.Len() > 0 {
				lines = append(lines, currentLine.String())
				currentLine.Reset()
				currentWidth = 0
			}
			for len(wordRunes) > maxWidth {
				lines = append(lines, string(wordRunes[:maxWidth]))
				wordRunes = wordRunes[maxWidth:]
			}
			if len(wordRunes) > 0 {
				currentLine.WriteString(string(wordRunes))
				currentWidth = len(wordRunes)
			}
			continue
		}

		if currentLine.Len() == 0 {
			currentLine.WriteString(word)
			currentWidth = len(wordRunes)
		} else if currentWidth+1+len(wordRunes) <= maxWidth {
			currentLine.WriteString(" ")
			currentLine.WriteString(word)
			currentWidth += 1 + len(wordRunes)
		} else {
			lines = append(lines, currentLine.String())
			currentLine.Reset()
			currentLine.WriteString(word)
			currentWidth = len(wordRunes)
		}
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// getLegacyCommand extracts command string from legacy step format.
func (r *Renderer) getLegacyCommand(node *exec.Node) string {
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
func (r *Renderer) renderOutputs(node *exec.Node, isLast bool, prefix string) string {
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
func (r *Renderer) renderSubRuns(subRuns []exec.SubDAGRun, isLastSection bool, prefix string) string {
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
func (r *Renderer) renderFinalStatus(status *exec.DAGRunStatus) string {
	label := "Result"
	if status.Status == core.Running {
		label = "Status"
	}
	return fmt.Sprintf("\n%s: %s\n", label, StatusText(status.Status))
}

// renderSchedulerLog renders the DAG-level scheduler log path.
func (r *Renderer) renderSchedulerLog(logPath string, hasSteps bool) string {
	return branchChar(!hasSteps) + r.text("log: ") + r.gray(logPath) + "\n"
}

// calculateDuration calculates the duration string between start and finish times.
func (r *Renderer) calculateDuration(startedAt, finishedAt string, status core.Status) string {
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
	} else if status == core.Running {
		end = time.Now()
	} else {
		return ""
	}

	return stringutil.FormatDuration(end.Sub(start))
}

// calculateNodeDuration calculates duration for a specific node.
func (r *Renderer) calculateNodeDuration(node *exec.Node) string {
	nodeStatus := nodeStatusToStatus(node.Status)
	return r.calculateDuration(node.StartedAt, node.FinishedAt, nodeStatus)
}

// nodeStatusToStatus converts NodeStatus to Status for duration calculation.
func nodeStatusToStatus(ns core.NodeStatus) core.Status {
	switch ns {
	case core.NodeRunning:
		return core.Running
	case core.NodeRetrying:
		return core.Running
	case core.NodeSucceeded:
		return core.Succeeded
	case core.NodeFailed:
		return core.Failed
	case core.NodeAborted:
		return core.Aborted
	case core.NodePartiallySucceeded:
		return core.PartiallySucceeded
	case core.NodeWaiting:
		return core.Waiting
	case core.NodeRejected:
		return core.Rejected
	case core.NodeNotStarted, core.NodeSkipped:
		return core.NotStarted
	default:
		return core.NotStarted
	}
}
