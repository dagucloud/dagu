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
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
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
	// execution succeeds. An output declared with type json (Spec 012)
	// publishes any value as its JSON text. Any other output publishes a
	// string as written and any other value as its JSON text.
	Outputs map[string]any
}

// Step scripts the action of one mocked step.
type Step struct {
	// Results holds one result for each execution of the action: the first
	// attempt, each retry, and each repeat iteration. After the last result,
	// the last one repeats. With no results, every execution succeeds with no
	// output.
	Results []Result
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
		script:      script,
		jsonOutputs: jsonOutputs(step),
		stdout:      io.Discard,
		stderr:      io.Discard,
	}, true
}

// jsonOutputs returns the names of the step's outputs declared with type json.
func jsonOutputs(step ir.Step) map[string]bool {
	names := make(map[string]bool)
	for _, declaration := range step.ValueOutputs() {
		if declaration.Type == ir.StepDeclaredOutputTypeJSON {
			names[declaration.Name] = true
		}
	}
	return names
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
	script      *script
	jsonOutputs map[string]bool
	stdout      io.Writer
	stderr      io.Writer
	exitCode    int
	// outputs holds the text of each output the execution publishes.
	outputs map[string]string
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
func (e *mockExecutor) NamedOutputs() map[string]string { return e.outputs }

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
	outputs, err := outputTexts(result.Outputs, e.jsonOutputs)
	if err != nil {
		return err
	}
	e.outputs = outputs
	return nil
}

// outputTexts returns the text each output publishes.
func outputTexts(outputs map[string]any, jsonOutputs map[string]bool) (map[string]string, error) {
	if len(outputs) == 0 {
		return nil, nil
	}
	texts := make(map[string]string, len(outputs))
	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		text, err := outputText(outputs[name], jsonOutputs[name])
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", name, err)
		}
		texts[name] = text
	}
	return texts, nil
}

// outputText returns the text an output value publishes. A json output
// publishes a string as a JSON string, as Spec 101 defines.
func outputText(value any, asJSON bool) (string, error) {
	if text, ok := value.(string); ok && !asJSON {
		return text, nil
	}
	data, err := jsonutil.MarshalUnescaped(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
