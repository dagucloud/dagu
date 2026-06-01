// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/output"
	"golang.org/x/term"
)

// RemoteProgressDisplay displays progress for distributed DAG execution.
// It polls the coordinator for status updates and renders a progress display
// similar to the local SimpleProgressDisplay.
type RemoteProgressDisplay struct {
	dag          *core.DAG
	dagRunID     string
	startTime    time.Time
	total        int
	colorEnabled bool
	isTTY        bool
	termWidth    int

	mu                      sync.Mutex
	completed               int
	spinnerIndex            int
	lastStatus              *exec.DAGRunStatus
	workerID                string
	headerUpdatedWithWorker bool
	stopped                 bool
	stopCh                  chan struct{}
	wg                      sync.WaitGroup
}

// NewRemoteProgressDisplay creates a new remote progress display.
func NewRemoteProgressDisplay(dag *core.DAG, dagRunID string) *RemoteProgressDisplay {
	total := 0
	if dag != nil {
		total = len(dag.Steps)
	}
	isTTY := term.IsTerminal(int(os.Stderr.Fd()))
	termWidth := 80 // default
	if isTTY {
		if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
			termWidth = w
		}
	}
	return &RemoteProgressDisplay{
		dag:          dag,
		dagRunID:     dagRunID,
		total:        total,
		colorEnabled: isTTY,
		isTTY:        isTTY,
		termWidth:    termWidth,
		startTime:    time.Now(),
		stopCh:       make(chan struct{}),
	}
}

// Start prints the initial header and starts the animation loop.
func (p *RemoteProgressDisplay) Start() {
	p.printHeader()

	if p.isTTY {
		p.wg.Add(1)
		go p.animationLoop()
	}
}

// animationLoop runs a fast ticker to update the spinner animation.
func (p *RemoteProgressDisplay) animationLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			if !p.stopped {
				p.render()
			}
			p.mu.Unlock()
		}
	}
}

// Update updates the display with new status from coordinator.
func (p *RemoteProgressDisplay) Update(status *exec.DAGRunStatus) {
	if status == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.lastStatus = status

	// Capture worker ID if available and update header
	if status.WorkerID != "" && p.workerID == "" {
		p.workerID = status.WorkerID
		// Update header to show worker assignment
		if p.isTTY && !p.headerUpdatedWithWorker {
			p.updateHeaderWithWorker()
			p.headerUpdatedWithWorker = true
		}
	}

	// Count completed nodes
	p.completed = 0
	for _, node := range status.Nodes {
		if node.Status.IsDone() {
			p.completed++
		}
	}
}

// Stop stops the display and prints final status.
func (p *RemoteProgressDisplay) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	status := p.lastStatus
	p.mu.Unlock()

	// Stop the animation loop
	close(p.stopCh)
	p.wg.Wait()

	p.printFinal(status)
}

// GetLastStatus returns the last known status.
func (p *RemoteProgressDisplay) GetLastStatus() *exec.DAGRunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastStatus
}

// SetCancelled updates the status to Aborted when cancellation is requested.
func (p *RemoteProgressDisplay) SetCancelled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastStatus != nil {
		p.lastStatus.Status = core.Aborted
	}
}

// PrintSummary prints the final tree summary.
func (p *RemoteProgressDisplay) PrintSummary() {
	p.mu.Lock()
	status := p.lastStatus
	dag := p.dag
	p.mu.Unlock()

	if status == nil || dag == nil {
		return
	}

	// Create renderer config - logs are synced from remote workers
	config := output.DefaultConfig()
	config.ColorEnabled = term.IsTerminal(int(os.Stdout.Fd()))
	config.MaxOutputLines = 10

	renderer := output.NewRenderer(config)
	summary := renderer.RenderDAGStatus(dag, status)

	_, _ = os.Stdout.WriteString(summary)
	_, _ = os.Stdout.WriteString("\n")
	_ = os.Stdout.Sync()
}

func (p *RemoteProgressDisplay) printHeader() {
	dagName := "unknown"
	if p.dag != nil {
		dagName = p.dag.Name
	}

	if p.isTTY {
		fmt.Fprintf(os.Stderr, "▶ %s %s\n", dagName, p.gray("("+p.dagRunID+")"))
	} else {
		// Non-TTY: just print once
		fmt.Fprintf(os.Stderr, "Started: %s (%s)\n", dagName, p.dagRunID)
	}
}

// updateHeaderWithWorker reprints the header line with worker info.
// Must be called with mu held and only in TTY mode.
func (p *RemoteProgressDisplay) updateHeaderWithWorker() {
	dagName := "unknown"
	if p.dag != nil {
		dagName = p.dag.Name
	}

	// Build the base header to calculate remaining width
	baseHeader := fmt.Sprintf("▶ %s (%s) → ", dagName, p.dagRunID)
	remainingWidth := p.termWidth - len(baseHeader) - 1

	workerDisplay := p.truncateWorkerDisplay(remainingWidth)

	// ANSI: clear line, move up, clear header, print new header
	if workerDisplay != "" {
		fmt.Fprintf(os.Stderr, "\r\033[K\033[1A\r\033[K▶ %s %s %s\n", dagName, p.gray("("+p.dagRunID+")"), p.gray("→ "+workerDisplay))
	} else {
		fmt.Fprintf(os.Stderr, "\r\033[K\033[1A\r\033[K▶ %s %s\n", dagName, p.gray("("+p.dagRunID+")"))
	}
}

// truncateWorkerDisplay truncates the worker ID for header display.
// Returns the truncated worker ID without the arrow prefix.
func (p *RemoteProgressDisplay) truncateWorkerDisplay(maxWidth int) string {
	if p.workerID == "" || maxWidth <= 3 {
		return ""
	}
	if len(p.workerID) > maxWidth {
		return p.workerID[:maxWidth-1] + "…"
	}
	return p.workerID
}

// completedAndPercent returns capped completed count and percentage.
// Must be called with mu held.
func (p *RemoteProgressDisplay) completedAndPercent() (completed, percent int) {
	completed = min(p.completed, p.total)
	if p.total > 0 {
		percent = (completed * 100) / p.total
	}
	return completed, percent
}

// truncateWorkerID truncates the worker ID to fit within the available width.
// Returns empty string if insufficient space, otherwise returns " -> " + workerID (possibly truncated).
func (p *RemoteProgressDisplay) truncateWorkerID(availableWidth int) string {
	if p.workerID == "" || availableWidth <= 5 {
		return ""
	}

	workerSuffix := " → " + p.workerID
	if len(workerSuffix) > availableWidth {
		return workerSuffix[:availableWidth-1] + "…"
	}
	return workerSuffix
}

// render must be called with mu held.
func (p *RemoteProgressDisplay) render() {
	if !p.isTTY {
		return
	}

	spinner := stringutil.SpinnerFrames[p.spinnerIndex%len(stringutil.SpinnerFrames)]
	p.spinnerIndex++

	completed, percent := p.completedAndPercent()
	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	// Build the base progress text (without worker info)
	baseText := fmt.Sprintf("%s %d%% (%d/%d steps) %s", spinner, percent, completed, p.total, elapsed)

	// Calculate remaining space for worker info (account for padding spaces and safety margin)
	remainingWidth := p.termWidth - len(baseText) - 4
	workerInfo := p.truncateWorkerID(remainingWidth)

	// Use \r to overwrite the line, pad with spaces to clear previous content
	fmt.Fprintf(os.Stderr, "\r%s %d%% (%d/%d steps) %s%s   ", spinner, percent, completed, p.total, p.gray(elapsed), p.gray(workerInfo))
}

func (p *RemoteProgressDisplay) printFinal(status *exec.DAGRunStatus) {
	p.mu.Lock()
	completed, percent := p.completedAndPercent()
	workerID := p.workerID
	isTTY := p.isTTY
	total := p.total
	termWidth := p.termWidth
	p.mu.Unlock()

	icon := "✓"
	statusText := "completed"
	if status != nil && (status.Status == core.Failed || status.Status == core.Aborted) {
		icon = "✗"
		statusText = output.StatusText(status.Status)
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	if isTTY {
		// Build the base text to calculate remaining width
		baseText := fmt.Sprintf("%s %d%% (%d/%d steps) %s", icon, percent, completed, total, elapsed)
		remainingWidth := termWidth - len(baseText) - 4
		workerInfo := p.truncateWorkerID(remainingWidth)
		fmt.Fprintf(os.Stderr, "\r%s %d%% (%d/%d steps) %s%s   \n", icon, percent, completed, total, p.gray(elapsed), p.gray(workerInfo))
		return
	}

	if workerID != "" {
		fmt.Fprintf(os.Stderr, "Finished: %s (%d/%d steps) [%s] worker=%s\n", statusText, completed, total, elapsed, workerID)
	} else {
		fmt.Fprintf(os.Stderr, "Finished: %s (%d/%d steps) [%s]\n", statusText, completed, total, elapsed)
	}
}

// gray returns text in gray color if color is enabled.
func (p *RemoteProgressDisplay) gray(s string) string {
	if !p.colorEnabled {
		return s
	}
	return "\033[38;5;245m" + s + "\033[0m"
}
