// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
)

// maxReasonWidth bounds how much of a controller justification is shown on one
// timeline line. The full text is in the run's timeline and the Web UI.
const maxReasonWidth = 120

// ControllerProgressDisplay renders a controller run as its decision timeline:
// one line per decision as it settles, a live line for what the controller is
// doing now, and a final line with the outcome.
//
// Percent progress does not apply to a controller run: the number of turns is
// unknown in advance, an action may repeat, and a step the controller never
// picks was never pending work.
type ControllerProgressDisplay struct {
	progressWriter

	dag      *core.DAG
	dagRunID string
	params   string

	mu            sync.Mutex
	status        core.Status
	state         controller.State
	printedEvents int
	runningAction string
	spinnerIndex  int
	startTime     time.Time
	liveLineShown bool

	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

var _ ProgressReporter = (*ControllerProgressDisplay)(nil)

// NewControllerProgressDisplay creates a progress display for a controller DAG.
func NewControllerProgressDisplay(dag *core.DAG) *ControllerProgressDisplay {
	return &ControllerProgressDisplay{
		progressWriter: newProgressWriter(),
		dag:            dag,
		stopCh:         make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// Start begins the progress display.
func (p *ControllerProgressDisplay) Start() {
	go p.run()
}

// Stop stops the progress display. Safe to call multiple times.
func (p *ControllerProgressDisplay) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	<-p.done
}

// UpdateNode consumes node updates. The controller node carries the decision
// timeline; every other node marks which action is in flight.
func (p *ControllerProgressDisplay) UpdateNode(node *exec.Node) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if node.Step.Name == core.ControllerStepName {
		if len(node.ControllerState) == 0 {
			return
		}
		var state controller.State
		if err := json.Unmarshal(node.ControllerState, &state); err != nil {
			return
		}
		p.state = state
		p.flushEventsLocked()
		return
	}

	switch {
	case node.Status == core.NodeRunning:
		p.runningAction = node.Step.Name
	case p.runningAction == node.Step.Name:
		p.runningAction = ""
	}
}

// UpdateStatus updates the overall DAG status.
func (p *ControllerProgressDisplay) UpdateStatus(status *exec.DAGRunStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status.Status
}

// SetDAGRunInfo sets the DAG run ID and parameters.
func (p *ControllerProgressDisplay) SetDAGRunInfo(dagRunID, params string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dagRunID = dagRunID
	p.params = params
}

func (p *ControllerProgressDisplay) run() {
	defer close(p.done)

	p.mu.Lock()
	p.startTime = time.Now()
	p.mu.Unlock()

	p.mu.Lock()
	dag, runID, params := p.dag, p.dagRunID, p.params
	p.mu.Unlock()
	p.printHeader(dag, runID, params)

	if !p.tty {
		// Timeline lines are printed as they arrive; there is no live line to
		// animate.
		<-p.stopCh
		p.printFinal()
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			p.printFinal()
			return
		case <-ticker.C:
			p.renderLiveLine()
		}
	}
}

// flushEventsLocked prints timeline entries that have not been shown yet. The
// timeline is append-only within one run attempt, so entries are printed in
// order exactly once.
func (p *ControllerProgressDisplay) flushEventsLocked() {
	for ; p.printedEvents < len(p.state.Events); p.printedEvents++ {
		p.printLineLocked(p.formatEvent(p.state.Events[p.printedEvents]))
	}
}

// printLineLocked prints one permanent line, clearing the live line first so
// the timeline stays intact above it.
func (p *ControllerProgressDisplay) printLineLocked(line string) {
	if p.liveLineShown {
		fmt.Fprint(p.out, "\r\033[K")
		p.liveLineShown = false
	}
	fmt.Fprintln(p.out, line)
}

func (p *ControllerProgressDisplay) formatEvent(event controller.Event) string {
	turn := p.gray(fmt.Sprintf("turn %d", event.Turn))

	switch event.Kind {
	case controller.EventAction:
		line := fmt.Sprintf("● %s  %s %s", turn, event.Name, nodeStatusMark(event.Status))
		if duration := eventDuration(event); duration != "" {
			line += " " + p.gray(duration)
		}
		if event.Attempt > 1 {
			line += " " + p.gray(fmt.Sprintf("(attempt %d)", event.Attempt))
		}
		if event.Status == core.NodeFailed.String() && event.Reason != "" {
			line += " " + p.gray(compactReason(event.Reason))
		}
		return line
	case controller.EventTaskStatus:
		verb := event.Status
		if event.Status == string(controller.TaskOpen) {
			verb = "reopened"
		}
		mark := "●"
		if event.Status == string(controller.TaskFailed) {
			mark = "✗"
		}
		line := fmt.Sprintf("%s %s  task %s %s", mark, turn, event.Name, verb)
		if event.Reason != "" {
			line += ": " + compactReason(event.Reason)
		}
		return line
	case controller.EventAskUser:
		return fmt.Sprintf("● %s  asked: %s", turn, compactReason(event.Reason))
	case controller.EventRejected:
		return fmt.Sprintf("● %s  rejected %s: %s", turn, event.Name, p.gray(compactReason(event.Reason)))
	case controller.EventStalled:
		return fmt.Sprintf("● %s  stalled: %s", turn, p.gray(compactReason(event.Reason)))
	default:
		return fmt.Sprintf("● %s  %s", turn, event.Kind)
	}
}

func (p *ControllerProgressDisplay) renderLiveLine() {
	p.mu.Lock()
	defer p.mu.Unlock()

	spinner := stringutil.SpinnerFrames[p.spinnerIndex%len(stringutil.SpinnerFrames)]
	p.spinnerIndex++

	activity := "deciding"
	if p.runningAction != "" {
		activity = "running " + p.runningAction
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	fmt.Fprintf(p.out, "\r\033[K%s %s %s %s", spinner, activity, p.gray(p.openTasksText()), p.gray(elapsed))
	p.liveLineShown = true
}

func (p *ControllerProgressDisplay) printFinal() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Show decisions that arrived after the last node update, such as the final
	// task settlement.
	p.flushEventsLocked()
	if p.liveLineShown {
		fmt.Fprint(p.out, "\r\033[K")
		p.liveLineShown = false
	}

	icon := "✓"
	switch p.status {
	case core.Failed, core.Aborted:
		icon = "✗"
	case core.Waiting:
		icon = "⏸"
	}

	actions := 0
	for _, event := range p.state.Events {
		if event.Kind == controller.EventAction {
			actions++
		}
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))
	fmt.Fprintf(p.out, "%s %s\n", icon,
		p.gray(fmt.Sprintf("%d actions, %d turns, %s", actions, p.state.Turns, elapsed)))
}

func (p *ControllerProgressDisplay) openTasksText() string {
	open := 0
	switch {
	case len(p.state.Tasks) > 0:
		for _, task := range p.state.Tasks {
			if task.Status == controller.TaskOpen || task.Status == "" {
				open++
			}
		}
	case p.dag != nil:
		// No controller state has arrived yet; every declared task is open.
		open = len(p.dag.Tasks)
	}
	if open == 1 {
		return "1 task open"
	}
	return fmt.Sprintf("%d tasks open", open)
}

// compactReason renders a controller justification as one bounded line.
func compactReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	runes := []rune(reason)
	if len(runes) <= maxReasonWidth {
		return reason
	}
	return string(runes[:maxReasonWidth]) + "…"
}

// nodeStatusMark maps a node status to a one-character outcome mark.
func nodeStatusMark(status string) string {
	switch status {
	case core.NodeSucceeded.String():
		return "✓"
	case core.NodeFailed.String(), core.NodeAborted.String():
		return "✗"
	case core.NodeWaiting.String():
		return "⏸"
	default:
		return status
	}
}

// eventDuration formats how long an action took, when both timestamps exist.
func eventDuration(event controller.Event) string {
	if event.StartedAt == "" || event.FinishedAt == "" {
		return ""
	}
	started, err := stringutil.ParseTime(event.StartedAt)
	if err != nil {
		return ""
	}
	finished, err := stringutil.ParseTime(event.FinishedAt)
	if err != nil {
		return ""
	}
	if finished.Before(started) {
		return ""
	}
	return stringutil.FormatDuration(finished.Sub(started))
}
