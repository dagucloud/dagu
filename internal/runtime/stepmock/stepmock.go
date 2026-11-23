// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stepmock runs scripted results in place of step actions, so that a
// workflow test runs a DAG through the real runner without running what its
// steps do.
package stepmock

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// Result is what one execution of a mocked action returns.
type Result struct {
	// ExitCode is the exit code of the action. A non-zero code fails the
	// execution.
	ExitCode int
	// Stdout is written to the step's stdout.
	Stdout string
	// Stderr is written to the step's stderr.
	Stderr string
	// Outputs holds every named output the step publishes, whatever its
	// source: the output file, output_schema, object-form output,
	// stdout.outputs, or the action. They are published only when the
	// execution succeeds, and reach the references and run outputs that
	// their source reaches in a real run. An output declared with type json
	// (Spec 012) publishes any value as its JSON text. Any other output
	// publishes a string as written and any other value as its JSON text.
	Outputs map[string]any
	// Condition is whether the step's repeat condition, when it runs a
	// command, is met after this execution. Nil leaves it to run.
	Condition *bool
}

// Step scripts one step of a workflow test: its action, the conditions that
// would run a command, and how its wait for a person is settled.
type Step struct {
	// Results holds one result for each execution of the action: the first
	// attempt, each retry, and each repeat iteration. After the last result,
	// the last one repeats, except on a step that repeats without a limit,
	// which then stops. With no results, every execution succeeds with no
	// output, unless the step keeps its action.
	Results []Result
	// Precondition is whether the step's preconditions that run a command
	// are met, for each start of the step, whether or not its action is
	// replaced. Nil leaves them to run.
	Precondition *bool
	// KeepAction runs the step's own action when it has no Results. Results
	// always replace the action, since each execution takes its repeat
	// condition from its result.
	KeepAction bool
	// Real marks a step that the test runs for real. It keeps its action as
	// KeepAction does, and repeats as written when it has no repeat limit.
	Real bool
	// Review settles each wait of the step for a person. Nil approves each
	// wait with no values. A Set does not read it; the caller that settles
	// the run's waits does.
	Review *Review
}

// Review is the decision a workflow test makes for a step that waits for a
// person.
type Review struct {
	// Reject rejects the wait. False approves it.
	Reject bool
	// Values holds the completion input of a human task, or the inputs of an
	// approval gate.
	Values map[string]any
}

// Set scripts the steps it holds in place of their actions and of the
// conditions that would run a command. A step that is not in the set runs
// its own action and must not repeat without a limit. A step's next result
// depends on its earlier executions, so a Set serves a single run.
type Set struct {
	steps           map[string]*script
	dagPrecondition *bool
}

var (
	_ runtime.ExecutorOverride  = (*Set)(nil)
	_ runtime.ConditionOverride = (*Set)(nil)
)

// New returns a Set holding the given steps, keyed by step name.
func New(steps map[string]Step) *Set {
	set := &Set{steps: make(map[string]*script, len(steps))}
	for name, step := range steps {
		step.Results = slices.Clone(step.Results)
		set.steps[name] = newScript(step)
	}
	return set
}

// SetDAGPrecondition scripts whether the DAG-level preconditions that run a
// command are met. It must be called before the run starts.
func (s *Set) SetDAGPrecondition(met bool) {
	s.dagPrecondition = &met
}

// DAGPrecondition returns what SetDAGPrecondition scripts, or nil.
func (s *Set) DAGPrecondition() *bool {
	return s.dagPrecondition
}

// Precondition returns the step's Precondition.
func (s *Set) Precondition(step ir.Step) *bool {
	script, ok := s.steps[step.Name]
	if !ok {
		return nil
	}
	return script.step.Precondition
}

// RepeatCondition returns the Condition of the result that the step's
// latest execution took, or of its first result before any. A step whose
// action the set does not replace scripts none.
func (s *Set) RepeatCondition(step ir.Step) *bool {
	script, ok := s.steps[step.Name]
	if !ok || !script.replaces() {
		return nil
	}
	return script.latest().Condition
}

// ContinuousLimit bounds a step that repeats without a limit. A step whose
// action the set replaces stops after one execution for each result, or
// after one when it has no results. A Real step repeats as written. Any
// other step gets no execution.
func (s *Set) ContinuousLimit(step ir.Step) (limit int, ok bool) {
	script, ok := s.steps[step.Name]
	switch {
	case !ok:
		return 0, true
	case script.replaces():
		return max(1, len(script.step.Results)), true
	case script.step.Real:
		return 0, false
	default:
		return 0, true
	}
}

// Executor returns an executor that runs the step's next result, or false
// when the set does not replace the step's action.
func (s *Set) Executor(_ context.Context, step ir.Step) (executor.Executor, bool) {
	script, ok := s.steps[step.Name]
	if !ok || !script.replaces() {
		return nil, false
	}
	return &mockExecutor{
		script: script,
		stdout: io.Discard,
		stderr: io.Discard,
	}, true
}

// script holds one scripted step and how many executions took a result.
type script struct {
	step Step

	mu         sync.Mutex
	executions int
	// last is the result the latest execution took.
	last Result
}

func newScript(step Step) *script {
	s := &script{step: step}
	if len(step.Results) > 0 {
		s.last = step.Results[0]
	}
	return s
}

// replaces reports whether the step's results run in place of its action.
func (s *script) replaces() bool {
	return len(s.step.Results) > 0 || !s.step.KeepAction && !s.step.Real
}

// next returns the result of the next execution.
func (s *script) next() Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := s.step.Results
	var result Result
	if len(results) > 0 {
		result = results[min(s.executions, len(results)-1)]
	}
	s.executions++
	s.last = result
	return result
}

// latest returns the result the latest execution took, or the first result
// before any execution.
func (s *script) latest() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// mockExecutor runs one execution of a mocked step. It starts no process and
// reads no file.
type mockExecutor struct {
	script   *script
	stdout   io.Writer
	stderr   io.Writer
	exitCode int
	// outputs holds the outputs the execution publishes.
	outputs map[string]any
}

var (
	_ executor.ExitCoder             = (*mockExecutor)(nil)
	_ executor.NamedOutputsPublisher = (*mockExecutor)(nil)
)

func (e *mockExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *mockExecutor) SetStderr(out io.Writer) { e.stderr = out }
func (*mockExecutor) Kill(os.Signal) error      { return nil }

// ExitCode returns the exit code of the result the execution took.
func (e *mockExecutor) ExitCode() int { return e.exitCode }

// NamedOutputs returns the outputs of the result the execution took.
func (e *mockExecutor) NamedOutputs() map[string]any { return e.outputs }

func (e *mockExecutor) Run(context.Context) error {
	result := e.script.next()
	e.exitCode = result.ExitCode
	if _, err := io.WriteString(e.stdout, result.Stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(e.stderr, result.Stderr); err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("exit status %d", result.ExitCode)
	}
	e.outputs = result.Outputs
	return nil
}
