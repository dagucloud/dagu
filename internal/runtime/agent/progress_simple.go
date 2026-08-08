// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// SimpleProgressDisplay provides a minimal inline progress display.
type SimpleProgressDisplay struct {
	progressWriter

	dag      *ir.DAG
	dagRunID string
	params   string

	mu             sync.Mutex
	total          int
	completed      int
	completedNodes map[string]bool // track which nodes are already counted
	status         ir.Status
	spinnerIndex   int
	startTime      time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

// NewSimpleProgressDisplay creates a new simple progress display.
func NewSimpleProgressDisplay(dag *ir.DAG) *SimpleProgressDisplay {
	total := 0
	if dag != nil {
		total = len(dag.Steps)
	}
	return &SimpleProgressDisplay{
		progressWriter: newProgressWriter(),
		dag:            dag,
		total:          total,
		completedNodes: make(map[string]bool),
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
	p.mu.Lock()
	dag, runID, params := p.dag, p.dagRunID, p.params
	p.mu.Unlock()
	p.printHeader(dag, runID, params)

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
	fmt.Fprintf(p.out, "\r%s %d%% (%d/%d steps) %s   ", spinner, percent, p.completed, p.total, p.gray(elapsed))
}

func (p *SimpleProgressDisplay) printFinal() {
	p.mu.Lock()
	defer p.mu.Unlock()

	percent := 0
	if p.total > 0 {
		percent = (p.completed * 100) / p.total
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	// Clear line and print final status
	fmt.Fprintf(p.out, "\r%s %d%% (%d/%d steps) %s   \n", statusIcon(p.status), percent, p.completed, p.total, p.gray(elapsed))
}
