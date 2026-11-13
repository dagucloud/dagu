// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// Spec 092, "The agent worker". An agent worker has one poller, takes one run,
// announces the label of its provider, and stops when the run ends. The result
// of the run is the result of the final report: the status of the run does not
// change it.

// EnableAgentMode makes the worker an agent worker of the provider. Call it
// before Start. The mode sets one poller, one run and the label
// agent.provider.<provider>: "true". No other option changes these rules.
func (w *Worker) EnableAgentMode(provider string) error {
	if w.labelErr != nil {
		return w.labelErr
	}
	if !ir.IsAgentCLIProvider(provider) {
		return fmt.Errorf("provider %q is not a CLI provider", provider)
	}
	w.agentProvider = provider
	w.maxActiveRuns = 1
	w.labels[ir.AgentProviderLabel(provider)] = ir.AgentProviderLabelValue
	w.agentRunDone = make(chan struct{})
	return nil
}

// AgentProvider returns the provider of an agent worker, or an empty string for
// a general worker.
func (w *Worker) AgentProvider() string {
	return w.agentProvider
}

// Done is closed when an agent worker has ended its one run and sent the final
// report, or failed to send it. It is nil for a general worker, so a receive
// from it blocks.
func (w *Worker) Done() <-chan struct{} {
	return w.agentRunDone
}

// AgentRunErr returns the result of the one run of an agent worker. It is nil
// when the worker took no run or sent the final status of the run. Otherwise it
// is the error of the final report.
func (w *Worker) AgentRunErr() error {
	w.pollersMu.Lock()
	defer w.pollersMu.Unlock()
	return w.agentRunErr
}

func (w *Worker) finishAgentRun(err error) {
	w.agentRunOnce.Do(func() {
		w.pollersMu.Lock()
		w.agentRunErr = err
		w.pollersMu.Unlock()
		if w.agentRunDone != nil {
			close(w.agentRunDone)
		}
	})
}

// CheckConfiguredLabels checks the labels of worker.labels before the worker
// connects to the coordinator. A key that starts with agent. or with a prefix
// of the environment providers is an error.
func CheckConfiguredLabels(configured map[string]string) error {
	_, err := workerLabels(configured)
	return err
}

// finalReport records the last push of a final status of the DAG run of one
// task. A final status is a status that ends the task: a terminal status or
// waiting.
type finalReport struct {
	dagRunID string

	mu     sync.Mutex
	pushed bool
	status ir.Status
	err    error
}

type finalReportKey struct{}

func newFinalReport(dagRunID string) *finalReport {
	return &finalReport{dagRunID: dagRunID}
}

func withFinalReport(ctx context.Context, report *finalReport) context.Context {
	return context.WithValue(ctx, finalReportKey{}, report)
}

func finalReportFrom(ctx context.Context) *finalReport {
	report, _ := ctx.Value(finalReportKey{}).(*finalReport)
	return report
}

func (r *finalReport) record(status ir.DAGRunStatus, err error) {
	if r == nil || status.DAGRunID != r.dagRunID {
		return
	}
	switch status.Status { //nolint:exhaustive // only the statuses that end the task count
	case ir.NotStarted, ir.Running, ir.Queued:
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushed = true
	r.status = status.Status
	r.err = err
}

// result returns nil when the coordinator accepted the last final status.
func (r *finalReport) result() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pushed {
		return fmt.Errorf("the worker sent no final status for the DAG run %s", r.dagRunID)
	}
	if r.err != nil {
		return fmt.Errorf("the worker could not send the final status %q of the DAG run %s: %w", r.status.String(), r.dagRunID, r.err)
	}
	return nil
}

// finalReportPusher records each push of the pusher in the final report.
type finalReportPusher struct {
	pusher runtime.StatusPusher
	report *finalReport
}

func (p *finalReportPusher) Push(ctx context.Context, status ir.DAGRunStatus) error {
	err := p.pusher.Push(ctx, status)
	p.report.record(status, err)
	return err
}

// wrapFinalReport returns the pusher that records its pushes in the final
// report of the context, or the pusher itself when the context has none.
func wrapFinalReport(ctx context.Context, pusher runtime.StatusPusher) runtime.StatusPusher {
	report := finalReportFrom(ctx)
	if report == nil || pusher == nil {
		return pusher
	}
	return &finalReportPusher{pusher: pusher, report: report}
}

// refusedByRouting wraps the error of checkAgentRouting. The worker ran no step
// of the run, so the refusal counts as a claim rejected before execution: an
// agent worker keeps its one run for a task of its provider.
func refusedByRouting(err error) error {
	return fmt.Errorf("%w: refused the DAG: %w", errTaskClaimRejectedBeforeExecution, err)
}

// checkAgentRouting refuses a DAG that the worker must not run (Spec 092,
// "Match"). An agent worker runs only a DAG that needs its provider. A general
// worker runs no DAG that needs a provider. The coordinator applies the same
// rule to the labels; this check guards against a wrong route.
func checkAgentRouting(dag *ir.DAG, agentProvider string) error {
	providers := dag.AgentProviders()
	if agentProvider == "" {
		if len(providers) > 0 {
			return fmt.Errorf("the DAG needs the provider %q: only an agent worker of this provider runs it", providers[0])
		}
		return nil
	}
	if len(providers) != 1 || providers[0] != agentProvider {
		return fmt.Errorf("the agent worker of the provider %q runs only a DAG that needs this provider; the DAG needs %v",
			agentProvider, providers)
	}
	return nil
}
