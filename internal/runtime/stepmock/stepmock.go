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
}

// Step scripts one mocked step: the results of its action, and how its wait
// for a person is settled.
type Step struct {
	// Results holds one result for each execution of the action: the first
	// attempt, each retry, and each repeat iteration. After the last result,
	// the last one repeats. With no results, every execution succeeds with no
	// output.
	Results []Result
	// Review settles each wait of the step for a person. Nil approves each
	// wait with no values.
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

// Set replaces the actions of the steps it holds with their scripted
// results. A step's next result depends on its earlier executions, so a Set
// serves a single run.
type Set struct {
	steps map[string]*script
}

var _ runtime.ExecutorOverride = (*Set)(nil)

// New returns a Set holding the given steps, keyed by step name.
func New(steps map[string]Step) *Set {
	set := &Set{steps: make(map[string]*script, len(steps))}
	for name, step := range steps {
		set.steps[name] = &script{results: slices.Clone(step.Results)}
	}
	return set
}

// Executor returns an executor that runs the step's next result, or false
// when the step is not in the set.
func (s *Set) Executor(_ context.Context, step ir.Step) (executor.Executor, bool) {
	script, ok := s.steps[step.Name]
	if !ok {
		return nil, false
	}
	return &mockExecutor{
		script: script,
		stdout: io.Discard,
		stderr: io.Discard,
	}, true
}

// script holds the results of one mocked step and how many executions took
// one.
type script struct {
	mu         sync.Mutex
	results    []Result
	executions int
}

// next returns the result of the next execution.
func (s *script) next() Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result Result
	if len(s.results) > 0 {
		result = s.results[min(s.executions, len(s.results)-1)]
	}
	s.executions++
	return result
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
