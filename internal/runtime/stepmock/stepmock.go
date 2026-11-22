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

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
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
	// Outputs holds the step's named outputs, published only when the
	// execution succeeds. They are subject to the step's declared outputs
	// (Spec 012). A string is published as written; any other value is
	// published as its JSON text.
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
	return &mockExecutor{script: script, stdout: io.Discard, stderr: io.Discard}, true
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
}

var _ executor.ExitCoder = (*mockExecutor)(nil)

func (e *mockExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *mockExecutor) SetStderr(out io.Writer) { e.stderr = out }
func (*mockExecutor) Kill(os.Signal) error      { return nil }

// ExitCode returns the exit code of the result the execution took.
func (e *mockExecutor) ExitCode() int { return e.exitCode }

func (e *mockExecutor) Run(ctx context.Context) error {
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
	return writeOutputs(ctx, result.Outputs)
}

// writeOutputs appends outputs to the step's DAGU_OUTPUT_FILE as an action
// does, so the runtime parses and checks them as it does for any step.
func writeOutputs(ctx context.Context, outputs map[string]any) error {
	if len(outputs) == 0 {
		return nil
	}
	path, ok := runtime.GetEnv(ctx).Scope.Get(runenv.EnvKeyDAGUOutputFile)
	if !ok || path == "" {
		return fmt.Errorf("%s is not set", runenv.EnvKeyDAGUOutputFile)
	}
	records, err := outputRecords(outputs)
	if err != nil {
		return err
	}
	// #nosec G304 -- the runtime creates DAGU_OUTPUT_FILE for this execution.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(records); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// outputRecords encodes outputs as DAGU_OUTPUT_FILE records, in name order.
// A value that spans lines takes a multiline record.
func outputRecords(outputs map[string]any) (string, error) {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		value, err := outputText(outputs[name])
		if err != nil {
			return "", fmt.Errorf("output %q: %w", name, err)
		}
		if !strings.ContainsAny(value, "\r\n") {
			fmt.Fprintf(&b, "%s=%s\n", name, value)
			continue
		}
		delimiter := multilineDelimiter(value)
		fmt.Fprintf(&b, "%s<<%s\n%s\n%s\n", name, delimiter, value, delimiter)
	}
	return b.String(), nil
}

// outputText returns the text an output value publishes.
func outputText(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := jsonutil.MarshalUnescaped(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// multilineDelimiter returns a delimiter that no line of value equals, so the
// record ends after the whole value. The reader also splits lines at "\r".
func multilineDelimiter(value string) string {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value), "\n")
	delimiter := "EOF"
	for i := 1; slices.Contains(lines, delimiter); i++ {
		delimiter = fmt.Sprintf("EOF_%d", i)
	}
	return delimiter
}
