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
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
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

// Call is what one execution of a step's action was called with, as Dagu
// resolved it before the action, or the result that replaced it, ran.
type Call struct {
	// Run is the command or script text of a step written without action:
	// its script, or its commands one per line.
	Run string
	// With holds the with inputs of a step written with action, when the
	// DAG was loaded keeping them (spec.KeepActionWith), or nil.
	With map[string]any
	// Env holds the step's environment by name, secrets included.
	Env map[string]string
	// Secrets lists the values that output masks as secrets.
	Secrets []string
}

// Set scripts the steps it holds in place of their actions and of the
// conditions that would run a command. A step that is not in the set runs
// its own action and must not repeat without a limit. A step's next result
// depends on its earlier executions, so a Set serves a single run. A Set
// also records the call of the latest execution of every step and handler,
// whether or not it holds the step.
type Set struct {
	steps           map[string]*script
	handlers        map[ir.HandlerType]*script
	dagPrecondition *bool

	mu sync.Mutex
	// calls and handlerCalls hold the call of each step's latest execution,
	// by step name, and of each handler's, by handler type.
	calls        map[string]Call
	handlerCalls map[ir.HandlerType]Call
}

var (
	_ runtime.ExecutorOverride  = (*Set)(nil)
	_ runtime.ConditionOverride = (*Set)(nil)
)

// New returns a Set holding the given steps, keyed by step name, and the
// given lifecycle handlers, keyed by handler type. A step named like a
// handler takes only its own script.
func New(steps map[string]Step, handlers map[ir.HandlerType]Step) *Set {
	set := &Set{
		steps:        make(map[string]*script, len(steps)),
		handlers:     make(map[ir.HandlerType]*script, len(handlers)),
		calls:        make(map[string]Call),
		handlerCalls: make(map[ir.HandlerType]Call),
	}
	for name, step := range steps {
		set.steps[name] = newScript(step)
	}
	for kind, step := range handlers {
		set.handlers[kind] = newScript(step)
	}
	return set
}

// lookup returns the script of the step or lifecycle handler that runs
// under ctx.
func (s *Set) lookup(ctx context.Context, step ir.Step) (*script, bool) {
	if kind := runtime.LifecycleHandler(ctx); kind != "" {
		script, ok := s.handlers[kind]
		return script, ok
	}
	script, ok := s.steps[step.Name]
	return script, ok
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

// Precondition returns the Precondition of the step or handler.
func (s *Set) Precondition(ctx context.Context, step ir.Step) *bool {
	script, ok := s.lookup(ctx, step)
	if !ok {
		return nil
	}
	return script.step.Precondition
}

// RepeatCondition returns the Condition of the result that the step's
// latest execution took, or of its first result before any. A step whose
// action the set does not replace scripts none. A lifecycle handler does not
// repeat, so the step is never one.
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

// Executor returns an executor that runs the next result of the step or
// handler, or false when the set does not replace its action. Either way, it
// records the call of the execution.
func (s *Set) Executor(ctx context.Context, step ir.Step) (executor.Executor, bool) {
	s.record(ctx, step)
	script, ok := s.lookup(ctx, step)
	if !ok || !script.replaces() {
		return nil, false
	}
	return &mockExecutor{
		script: script,
		stdout: io.Discard,
		stderr: io.Discard,
	}, true
}

// LastCall returns the call of the named step's latest execution, or false
// when the step never executed.
func (s *Set) LastCall(step string) (Call, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call, ok := s.calls[step]
	return call, ok
}

// LastHandlerCall returns the call of the lifecycle handler's latest
// execution, or false when it never executed.
func (s *Set) LastHandlerCall(kind ir.HandlerType) (Call, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call, ok := s.handlerCalls[kind]
	return call, ok
}

// record keeps the call of an execution of step, whose fields are resolved
// as they are for its own action. The with object resolves as the action's
// with does, without counting as a read of the step. A with that does not
// resolve is not recorded, so no assertion holds on it.
func (s *Set) record(ctx context.Context, step ir.Step) {
	call := Call{Env: runtime.AllEnvsMap(ctx)}
	if scope := runtime.GetEnv(ctx).Scope; scope != nil {
		call.Secrets = slices.Collect(maps.Values(scope.AllSecrets()))
	}
	if step.ActionWith == nil {
		call.Run = runText(step)
	} else {
		resolved, err := runtime.ValueResolver(ctx).Object(cmnvalue.UntrackMaskedReads(ctx), step.ActionWith, registry.ConfigField(step, "with"))
		if with, ok := resolved.(map[string]any); err == nil && ok {
			call.With = with
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if kind := runtime.LifecycleHandler(ctx); kind != "" {
		s.handlerCalls[kind] = call
		return
	}
	s.calls[step.Name] = call
}

// runText returns the command or script text of a step: its script, or its
// commands one per line.
func runText(step ir.Step) string {
	if step.Script != "" {
		return step.Script
	}
	lines := make([]string, 0, len(step.Commands))
	for _, command := range step.Commands {
		lines = append(lines, command.CmdWithArgs)
	}
	return strings.Join(lines, "\n")
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
	step.Results = slices.Clone(step.Results)
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
