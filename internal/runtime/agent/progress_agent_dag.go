// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
)

// maxReasonWidth bounds how much of an agent justification is shown on one
// timeline line. The full text is in the run's timeline and the Web UI.
const maxReasonWidth = 120

// AgentDAGProgressDisplay renders an agent run as its decision timeline:
// one line per decision as it settles, a live line for what the agent is
// doing now, and a final line with the outcome.
//
// Percent progress does not apply to an agent run: the number of turns is
// unknown in advance, an action may repeat, and a step the agent never
// picks was never pending work.
type AgentDAGProgressDisplay struct {
	progressWriter

	dag      *ir.DAG
	dagRunID string
	params   string

	mu             sync.Mutex
	status         ir.Status
	state          agentloop.State
	printedEvents  int
	runningActions map[string]struct{}
	spinnerIndex   int
	startTime      time.Time
	liveLineShown  bool

	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan struct{}
}

var _ ProgressReporter = (*AgentDAGProgressDisplay)(nil)

// NewAgentDAGProgressDisplay creates a progress display for an agent DAG.
func NewAgentDAGProgressDisplay(dag *ir.DAG) *AgentDAGProgressDisplay {
	return &AgentDAGProgressDisplay{
		progressWriter: newProgressWriter(),
		dag:            dag,
		runningActions: make(map[string]struct{}),
		stopCh:         make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// Start begins the progress display.
func (p *AgentDAGProgressDisplay) Start() {
	go p.run()
}

// Stop stops the progress display. Safe to call multiple times.
func (p *AgentDAGProgressDisplay) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	<-p.done
}

// UpdateNode consumes node updates. The agent node carries the decision
// timeline; every other node marks which action is in flight.
func (p *AgentDAGProgressDisplay) UpdateNode(node *ir.Node) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if node.Step.Name == ir.AgentStepName {
		if len(node.AgentState) == 0 {
			return
		}
		var state agentloop.State
		if err := json.Unmarshal(node.AgentState, &state); err != nil {
			return
		}
		p.state = state
		p.flushEventsLocked(false)
		return
	}

	switch node.Status {
	case ir.NodeRunning, ir.NodeRetrying, ir.NodeWaiting:
		p.runningActions[node.Step.Name] = struct{}{}
	default:
		delete(p.runningActions, node.Step.Name)
	}
}

// UpdateStatus updates the overall DAG status.
func (p *AgentDAGProgressDisplay) UpdateStatus(status *ir.DAGRunStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status.Status
}

// SetDAGRunInfo sets the DAG run ID and parameters.
func (p *AgentDAGProgressDisplay) SetDAGRunInfo(dagRunID, params string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dagRunID = dagRunID
	p.params = params
}

func (p *AgentDAGProgressDisplay) run() {
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

// flushEventsLocked prints timeline entries in order, each exactly once. An
// action entry is held back until its status is settled: a resumed run
// finalizes a suspended action in place, so printing it as waiting would
// freeze a stale mark into the scrollback. With final set, held-back entries
// print as they stand, because nothing will settle them in this process.
func (p *AgentDAGProgressDisplay) flushEventsLocked(final bool) {
	for ; p.printedEvents < len(p.state.Events); p.printedEvents++ {
		event := p.state.Events[p.printedEvents]
		if !final && event.Kind == agentloop.EventAction && !actionSettled(event.Status) {
			return
		}
		p.printLineLocked(p.formatEvent(event))
	}
}

// actionSettled reports whether an action event carries its final outcome.
func actionSettled(status string) bool {
	switch status {
	case "", ir.NodeRunning.String(), ir.NodeRetrying.String(), ir.NodeWaiting.String():
		return false
	}
	return true
}

// printLineLocked prints one permanent line, clearing the live line first so
// the timeline stays intact above it.
func (p *AgentDAGProgressDisplay) printLineLocked(line string) {
	if p.liveLineShown {
		fmt.Fprint(p.out, "\r\033[K")
		p.liveLineShown = false
	}
	fmt.Fprintln(p.out, line)
}

func (p *AgentDAGProgressDisplay) formatEvent(event agentloop.Event) string {
	turn := p.gray(fmt.Sprintf("turn %d", event.Turn))

	switch event.Kind {
	case agentloop.EventAction:
		line := fmt.Sprintf("● %s  %s %s", turn, event.Name, nodeStatusMark(event.Status))
		if duration := eventDuration(event); duration != "" {
			line += " " + p.gray(duration)
		}
		if event.Attempt > 1 {
			line += " " + p.gray(fmt.Sprintf("(attempt %d)", event.Attempt))
		}
		if event.Status == ir.NodeFailed.String() && event.Reason != "" {
			line += " " + p.gray(compactReason(event.Reason))
		}
		return line
	case agentloop.EventTaskStatus:
		verb := event.Status
		if event.Status == string(agentloop.TaskOpen) {
			verb = "reopened"
		}
		mark := "●"
		if event.Status == string(agentloop.TaskFailed) {
			mark = "✗"
		}
		line := fmt.Sprintf("%s %s  task %s %s", mark, turn, event.Name, verb)
		if event.Reason != "" {
			line += ": " + compactReason(event.Reason)
		}
		return line
	case agentloop.EventAskUser:
		return fmt.Sprintf("● %s  asked: %s", turn, compactReason(event.Reason))
	case agentloop.EventRejected:
		return fmt.Sprintf("● %s  rejected %s: %s", turn, event.Name, p.gray(compactReason(event.Reason)))
	case agentloop.EventStalled:
		return fmt.Sprintf("● %s  stalled: %s", turn, p.gray(compactReason(event.Reason)))
	default:
		return fmt.Sprintf("● %s  %s", turn, event.Kind)
	}
}

func (p *AgentDAGProgressDisplay) renderLiveLine() {
	p.mu.Lock()
	defer p.mu.Unlock()

	spinner := stringutil.SpinnerFrames[p.spinnerIndex%len(stringutil.SpinnerFrames)]
	p.spinnerIndex++

	activity := "deciding"
	if len(p.runningActions) > 0 {
		actions := make([]string, 0, len(p.runningActions))
		for action := range p.runningActions {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		activity = "running " + strings.Join(actions, ", ")
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))

	fmt.Fprintf(p.out, "\r\033[K%s %s %s %s", spinner, activity, p.gray(p.openTasksText()), p.gray(elapsed))
	p.liveLineShown = true
}

func (p *AgentDAGProgressDisplay) printFinal() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Show decisions that arrived after the last node update, such as the final
	// task settlement, and any action still unsettled at suspension.
	p.flushEventsLocked(true)
	if p.liveLineShown {
		fmt.Fprint(p.out, "\r\033[K")
		p.liveLineShown = false
	}

	actions := 0
	for _, event := range p.state.Events {
		if event.Kind == agentloop.EventAction {
			actions++
		}
	}

	elapsed := stringutil.FormatDuration(time.Since(p.startTime))
	fmt.Fprintf(p.out, "%s %s\n", statusIcon(p.status),
		p.gray(fmt.Sprintf("%d actions, %d turns, %s", actions, p.state.Turns, elapsed)))
}

func (p *AgentDAGProgressDisplay) openTasksText() string {
	open := 0
	switch {
	case len(p.state.Tasks) > 0:
		for _, task := range p.state.Tasks {
			if task.Status == agentloop.TaskOpen || task.Status == "" {
				open++
			}
		}
	case p.dag != nil:
		// No agent state has arrived yet; every declared task is open.
		open = len(p.dag.Tasks)
	}
	if open == 1 {
		return "1 task open"
	}
	return fmt.Sprintf("%d tasks open", open)
}

// compactReason renders an agent justification as one bounded line.
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
	case ir.NodeSucceeded.String():
		return "✓"
	case ir.NodeFailed.String(), ir.NodeAborted.String(), ir.NodeRejected.String():
		return "✗"
	case ir.NodeWaiting.String():
		return "⏸"
	default:
		return status
	}
}

// eventDuration formats how long an action took, when both timestamps exist.
func eventDuration(event agentloop.Event) string {
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
