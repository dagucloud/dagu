// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// File is a test file whose fields decode and whose tested DAG loads.
type File struct {
	// Path is the test file's path, as given to LoadFile.
	Path string
	// DAGPath is the path of the tested DAG file.
	DAGPath string
	// DAG is the tested DAG. Every params[].eval of it waits for the run, a
	// map-form worker_selector stays as written, and each action step keeps
	// its with as written.
	DAG *ir.DAG
	// Cases holds the cases in file order, each with the file's fields
	// merged in.
	Cases []Case
	// data holds the text the file decoded from.
	data []byte
}

// Case is one case of a test file, with the file's fields merged in.
type Case struct {
	// Name is the case's name, unique inside its file.
	Name string
	// Params holds the run's parameters as name=value assignments, in the
	// form dagu start -- name=value takes them.
	Params []string
	// Env holds the DAG-level env entries the case sets, as KEY=VALUE text in
	// file order. Each replaces the DAG's entry of the same name, or follows
	// the DAG's last entry. Values are literal.
	Env []string
	// Secrets holds the test value of each DAG secret by name and of each
	// SSH credential ref by ref.
	Secrets map[string]string
	// ScheduledAt is the schedule slot as written, in a form dagu dry
	// --scheduled-at takes, or empty.
	ScheduledAt string
	// Trigger is the run's context.trigger.type.
	Trigger ir.TriggerType
	// Precondition is the outcome of the DAG-level preconditions that run a
	// command, or nil when the case sets none.
	Precondition *bool
	// Steps holds how the case handles each step it mocks or runs for real,
	// by step name, the default mock included. A step that is not in it runs
	// its own action.
	Steps map[string]Script
	// Handlers holds the same for lifecycle handlers, by handler type.
	Handlers map[ir.HandlerType]Script
	// DefaultMocked lists the labels of the steps and handlers the default
	// mock applies to, whether or not a run reaches them: steps in DAG
	// order, then handlers.
	DefaultMocked []string
	// Expect holds the case's assertions.
	Expect Expect
	// Errors lists what makes the case invalid: field errors sorted by label,
	// then the unhandled sources of the fail-closed rule. An invalid case
	// runs none of its steps. Empty for a valid case.
	Errors []Finding
	// Fix holds the case fields that handle every unhandled source of
	// Errors, as YAML to paste into the case, or "" when there is none.
	Fix string
	// FileFix holds the same for the test file's secrets, or "".
	FileFix string
}

// Script is how a case handles one step or lifecycle handler.
type Script struct {
	// Real reports that the step runs its real action.
	Real bool
	// DefaultMock reports that the step took the case's default mock.
	DefaultMock bool
	// ReplacesAction reports that Results run in place of the step's action,
	// or one successful result with no output when Results is empty. False
	// keeps the action, and Results is then empty.
	ReplacesAction bool
	// Results holds one result for each execution of the action. After the
	// last one, the last repeats.
	Results []Result
	// Precondition is the outcome of the step's preconditions that run a
	// command, for each start of the step, or nil.
	Precondition *bool
	// Review settles each wait of the step for a person, or nil to approve a
	// wait that needs no values with none.
	Review *Review
}

// Result is what one execution of a replaced action returns.
type Result struct {
	// ExitCode is the execution's exit code.
	ExitCode int
	// Stdout and Stderr are written to the step's streams as given.
	Stdout string
	Stderr string
	// Outputs holds the named outputs the execution publishes when it
	// succeeds.
	Outputs map[string]any
	// Condition is the outcome of the step's command-check repeat condition
	// after this execution, or nil.
	Condition *bool
}

// Review is the decision for each wait of a step.
type Review struct {
	// Reject rejects the wait. False approves it.
	Reject bool
	// Values holds the completion input of a human.task, typed as
	// --inputs-json types it, or the inputs of an approval gate, each as its
	// text.
	Values map[string]any
}

// Expect holds the assertions of a case.
type Expect struct {
	// Status is the expected run status token, or empty.
	Status string
	// Attempts is the expected number of run attempts, or nil.
	Attempts *int
	// Steps holds the step assertions in the order the case lists them.
	Steps []StepExpect
	// Notifications holds the notifications the run would have sent. Nil
	// asserts nothing, and an empty list asserts that none would be sent.
	Notifications []Notification
}

// StepExpect holds the assertions on one step or lifecycle handler.
type StepExpect struct {
	// Key is the step key as the case writes it.
	Key string
	// Step is the asserted step's name. Empty for a handler.
	Step string
	// Handler is the asserted handler's type. Empty for a step.
	Handler ir.HandlerType
	// Status is the expected node status token, or empty.
	Status string
	// Attempts is the expected number of executions, or nil.
	Attempts *int
	// Outputs holds the expected named outputs in the order listed.
	Outputs []OutputExpect
	// Stdout and Stderr are the stream assertions, or nil.
	Stdout *StreamExpect
	Stderr *StreamExpect
	// CalledWith asserts on what the step's last execution was called with,
	// or is nil.
	CalledWith *CallExpect
}

// CallExpect asserts on what a step's last execution was called with, as
// Dagu resolved it.
type CallExpect struct {
	// Run asserts on the command or script text of a step written with run,
	// or is nil.
	Run *RunExpect
	// With holds the expected with inputs of a step written with action, in
	// the order listed.
	With []InputExpect
	// Env holds the expected environment variables in the order listed.
	Env []EnvExpect
}

// RunExpect asserts on a step's resolved command or script text.
type RunExpect struct {
	// Text is the text the run equals, or nil.
	Text *string
	// Contains is text the run contains, or nil.
	Contains *string
	// Matches is an expression that matches somewhere in the run, or nil.
	Matches *regexp.Regexp
}

// InputExpect is the expected value of one with input. A string matches
// the input's text exactly, and any other value matches by the JSON rule of
// outputs.
type InputExpect struct {
	Name  string
	Value any
}

// EnvExpect is the expected text of one environment variable.
type EnvExpect struct {
	Name  string
	Value string
}

// OutputExpect is the expected value of one named output.
type OutputExpect struct {
	Name  string
	Value any
	// JSON reports that the step declares the output with type: json, so
	// its published text is compared as JSON whatever Value is.
	JSON bool
}

// StreamExpect asserts on a step's stdout or stderr.
type StreamExpect struct {
	// Contains is text the stream contains, or nil.
	Contains *string
	// Matches is an expression that matches somewhere in the stream, or nil.
	Matches *regexp.Regexp
}

// Notification is one notification a run would have sent.
type Notification struct {
	// Kind is mail_on.failure, mail_on.success, mail_on.wait, or
	// mail_on_error.
	Kind string
	// Step is the name of the step whose mail_on_error sends it, or empty.
	Step string
	// To lists the recipients, or nil to match any.
	To []string
}

// InvalidFileError reports a test file that is invalid, so that none of its
// cases runs.
type InvalidFileError struct {
	// Path is the test file's path, as given to LoadFile.
	Path string
	// Errors lists every error of the file, in file order.
	Errors []Finding
}

func (e *InvalidFileError) Error() string {
	return fmt.Sprintf("invalid test file %s: %s", e.Path, joinFindings(e.Errors))
}

// joinFindings writes findings on one line, separated by semicolons.
func joinFindings(findings []Finding) string {
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		lines = append(lines, finding.String())
	}
	return strings.Join(lines, "; ")
}

// LoadFile reads the test file at path, loads the DAG it tests with opts,
// with every params[].eval deferred to the run, and with each action step's
// with kept as written, and returns its cases as
// Spec 101 merges and validates them. A case's Errors report what makes it
// invalid. An error is an *InvalidFileError.
func LoadFile(ctx context.Context, path string, opts ...spec.LoadOption) (*File, error) {
	invalid := func(errs ...Finding) error { return &InvalidFileError{Path: path, Errors: errs} }

	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, invalid(Finding{Message: err.Error()})
	}
	decoded, errs := decodeFile(path, data)
	if len(errs) > 0 {
		return nil, invalid(errs...)
	}
	dagPath, finding := testedDAGPath(path, decoded.dag)
	if finding != nil {
		return nil, invalid(*finding)
	}
	dag, findings := loadTestedDAG(ctx, dagPath, opts)
	if len(findings) > 0 {
		return nil, invalid(findings...)
	}
	file := newFile(path, dagPath, dag, decoded)
	file.data = data
	return file, nil
}

// loadTestedDAG loads the DAG a test file tests with opts, with every
// params[].eval deferred to the run, and with each action step's with kept
// as written. A DAG that fails to load, and a
// type: build DAG, return the findings that make the test file invalid.
func loadTestedDAG(ctx context.Context, dagPath string, opts []spec.LoadOption) (*ir.DAG, []Finding) {
	dag, err := spec.Load(ctx, dagPath, append(slices.Clip(opts), spec.DeferAllParamEvals(), spec.KeepActionWith())...)
	if err != nil {
		return nil, loadFindings(err)
	}
	if dag.Type == ir.TypeBuild {
		return nil, []Finding{{Message: "the DAG is a type: build DAG, whose path outputs a test cannot mock (Spec 033)"}}
	}
	return dag, nil
}

// newFile returns the test file at path decoded as decoded, with each case
// merged and validated against dag.
func newFile(path, dagPath string, dag *ir.DAG, decoded fileSpec) *File {
	file := &File{Path: path, DAGPath: dagPath, DAG: dag}
	steps := indexSteps(dag)
	for _, c := range decoded.cases {
		file.Cases = append(file.Cases, buildCase(dag, steps, decoded, c))
	}
	return file
}

// dagFileExtensions are the extensions of a tested DAG file, in the order a
// test file without dag looks for them.
var dagFileExtensions = []string{".yaml", ".yml"}

// testedDAGPath returns the DAG file a test file tests: dag resolved against
// the test file's directory, or the file beside it with the test suffix
// removed, .yaml before .yml.
func testedDAGPath(testFile, dag string) (string, *Finding) {
	if dag != "" {
		if !filepath.IsAbs(dag) {
			dag = filepath.Join(filepath.Dir(testFile), dag)
		}
		if spec.IsTestFile(dag) {
			return "", &Finding{Source: "dag", Message: "names a test file, not a DAG"}
		}
		if _, err := os.Stat(dag); err != nil {
			return "", &Finding{Source: "dag", Message: fmt.Sprintf("DAG file %s does not exist", dag)}
		}
		return dag, nil
	}
	base, ok := spec.CutTestFileSuffix(testFile)
	if !ok {
		return "", &Finding{Message: "the file name does not end in .test.yaml or .test.yml; set dag"}
	}
	for _, ext := range dagFileExtensions {
		if _, err := os.Stat(base + ext); err == nil {
			return base + ext, nil
		}
	}
	return "", &Finding{Message: fmt.Sprintf("no DAG file %s.yaml or %[1]s.yml beside the test file; set dag", base)}
}

// loadFindings returns one finding for each error a DAG load joined.
func loadFindings(err error) []Finding {
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	findings := make([]Finding, 0, len(errs))
	for _, err := range errs {
		findings = append(findings, Finding{Message: "the DAG fails to load: " + err.Error()})
	}
	return findings
}
