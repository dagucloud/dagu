// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"golang.org/x/term"
)

// SimpleProgressDisplay provides a minimal inline progress display.
type SimpleProgressDisplay struct {
	dag      *core.DAG
	dagRunID string
	params   string

	mu             sync.Mutex
	total          int
	completed      int
	completedNodes map[string]bool // track which nodes are already counted
	status         core.Status
	spinnerIndex   int
	startTime      time.Time
	colorEnabled   bool

	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

// NewSimpleProgressDisplay creates a new simple progress display.
func NewSimpleProgressDisplay(dag *core.DAG) *SimpleProgressDisplay {
	total := 0
	if dag != nil {
		total = len(dag.Steps)
	}
	return &SimpleProgressDisplay{
		dag:            dag,
		total:          total,
		completedNodes: make(map[string]bool),
		colorEnabled:   term.IsTerminal(int(os.Stderr.Fd())),
		stopCh:         make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// Start begins the progress display.
func (p *SimpleProgressDisplay) Start() {
	go p.run()
}

// Stop stops the progress display. Safe to call multiple times.
func (p *SimpleProgressDisplay) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	<-p.done
}

// UpdateNode updates the progress for a specific node.
func (p *SimpleProgressDisplay) UpdateNode(node *exec.Node) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only count completed nodes once
	if node.Status.IsDone() {
		if !p.completedNodes[node.Step.Name] {
			p.completedNodes[node.Step.Name] = true
			p.completed++
		}
	}
}

// UpdateStatus updates the overall DAG status.
func (p *SimpleProgressDisplay) UpdateStatus(status *exec.DAGRunStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status.Status
}

// SetDAGRunInfo sets the DAG run ID and parameters.
func (p *SimpleProgressDisplay) SetDAGRunInfo(dagRunID, params string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dagRunID = dagRunID
	p.params = params
}

func (p *SimpleProgressDisplay) run() {
	defer close(p.done)

	p.mu.Lock()
	p.startTime = time.Now()
	p.mu.Unlock()

	// Print header
	p.printHeader()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			p.printFinal()
			return
		case <-ticker.C:
			p.render()
		}
	}
}

func (p *SimpleProgressDisplay) printHeader() {
	p.mu.Lock()
	defer p.mu.Unlock()

	dagName := "unknown"
	if p.dag != nil {
		dagName = p.dag.Name
	}

	runID := p.dagRunID
	if runID == "" {
		runID = "..."
	}

	if p.params != "" {
		fmt.Fprintf(os.Stderr, "▶ %s %s %s\n", dagName, p.gray("("+runID+")"), p.gray("["+p.params+"]"))
	} else {
		fmt.Fprintf(os.Stderr, "▶ %s %s\n", dagName, p.gray("("+runID+")"))
	}
}

// gray returns text in gray color if color is enabled.
func (p *SimpleProgressDisplay) gray(s string) string {
	if !p.colorEnabled {
		return s
	}
	return "\033[38;5;245m" + s + "\033[0m"
}

func (p *SimpleProgressDisplay) render() {
	p.mu.Lock()
	defer p.mu.Unlock()

	spinner := stringutil.SpinnerFrames[p.spinnerIndex%len(stringutil.SpinnerFrames)]
	p.spinnerIndex++

	percent := 0
	if p.total > 0 {
		percent = (p.completed * 100) / p.total
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	// Use \r to overwrite the line, pad with spaces to clear previous content
	fmt.Fprintf(os.Stderr, "\r%s %d%% (%d/%d steps) %s   ", spinner, percent, p.completed, p.total, p.gray(elapsed))
}

func (p *SimpleProgressDisplay) printFinal() {
	p.mu.Lock()
	defer p.mu.Unlock()

	percent := 0
	if p.total > 0 {
		percent = (p.completed * 100) / p.total
	}

	icon := "✓"
	if p.status == core.Failed || p.status == core.Aborted {
		icon = "✗"
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	// Clear line and print final status
	fmt.Fprintf(os.Stderr, "\r%s %d%% (%d/%d steps) %s   \n", icon, percent, p.completed, p.total, p.gray(elapsed))
}
