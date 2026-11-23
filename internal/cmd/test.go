// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

// Exit statuses of dagu test other than 0 (Spec 101).
const (
	testExitFailed  = 1
	testExitInvalid = 2
)

var testTimeoutFlag = commandLineFlag{
	name:         "timeout",
	defaultValue: "5m",
	usage:        "Longest a case may run, such as 30s or 5m",
}

var testFlags = []commandLineFlag{testTimeoutFlag}

// Test returns the cobra command that runs workflow tests.
func Test() *cobra.Command {
	command := NewCommand(
		&cobra.Command{
			Use:   "test [flags] [<test file or directory>...]",
			Short: "Run the test cases of DAGs with their step actions mocked",
			Long: `Run workflow tests (Spec 101). A test file sits beside the DAG it tests and
ends in .test.yaml or .test.yml. Each case runs the DAG through the same
runner as dagu start, in its own temporary directory, with step actions
replaced by scripted results. An action with side effects that the case
neither mocks nor lists under real makes the case invalid, and nothing runs.

A directory argument runs every test file under it. With no argument, every
test file under the DAGs directory runs.

Exit status: 0 when every case passed, 1 when a case failed, and 2 when a
test file or case is invalid or the arguments are.

Example:
  dagu test workflows/
  dagu test --timeout 30s workflows/release.test.yaml
`,
		}, testFlags, runTest,
	)
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitStatusError{code: testExitInvalid, err: err}
	})
	return command
}

// testOptions holds the flags of dagu test.
type testOptions struct {
	// timeout is the longest a case may run.
	timeout time.Duration
}

func runTest(ctx *Context, args []string) error {
	opts, err := testOptionsFromFlags(ctx)
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}
	files, err := selectTestFiles(ctx.Config.Paths.DAGsDir, args)
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}

	// An interrupt stops the running case and starts no further one.
	runCtx, stop := notifyShutdownContext(ctx.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = ctx.WithContext(runCtx)

	report := &testReport{out: ctx.Command.OutOrStdout()}
	for _, file := range files {
		if err := runTestFile(ctx, file, opts, report); err != nil {
			return err
		}
	}
	report.writeSummary()
	if code := report.exitStatus(); code != 0 {
		// The report already names every failed and invalid case.
		ctx.Command.SilenceErrors = true
		return &exitStatusError{code: code, err: errors.New("workflow tests did not pass")}
	}
	return nil
}

func testOptionsFromFlags(ctx *Context) (testOptions, error) {
	value, err := ctx.StringParam(testTimeoutFlag.name)
	if err != nil {
		return testOptions{}, err
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return testOptions{}, fmt.Errorf("--timeout %q: use a duration greater than zero, such as 30s or 5m", value)
	}
	return testOptions{timeout: timeout}, nil
}

// testFileRef is a selected test file.
type testFileRef struct {
	// path is the file's absolute path.
	path string
	// report names the file in the report.
	report string
}

// selectTestFiles returns the test files that args select, in the order
// they run: each file argument, and every test file under each directory
// argument, or under root when there is no argument. A file that several
// arguments select is listed once.
func selectTestFiles(root string, args []string) ([]testFileRef, error) {
	if len(args) == 0 {
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		args = []string{root}
	}
	var files []testFileRef
	add := func(path string) error {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(files, func(f testFileRef) bool { return f.path == abs }) {
			files = append(files, testFileRef{path: abs, report: testReportPath(abs)})
		}
		return nil
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !dagtest.IsTestFile(arg) {
				return nil, fmt.Errorf("%s is not a test file; its name must end in .test.yaml or .test.yml", arg)
			}
			if err := add(arg); err != nil {
				return nil, err
			}
			continue
		}
		if err := walkTestFiles(arg, add); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(files, func(a, b testFileRef) int { return cmp.Compare(a.report, b.report) })
	return files, nil
}

// walkTestFiles calls add for each test file under dir. Directories whose
// names start with . are skipped, and symbolic links are not followed.
func walkTestFiles(dir string, add func(string) error) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir() && path != dir && strings.HasPrefix(entry.Name(), "."):
			return filepath.SkipDir
		case entry.Type().IsRegular() && dagtest.IsTestFile(path):
			return add(path)
		default:
			return nil
		}
	})
}

// testReportPath names a test file in the report: its path relative to the
// current directory when it is under it, else its absolute path, with / as
// the separator.
func testReportPath(abs string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, abs); err == nil && filepath.IsLocal(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(abs)
}

// runTestFile runs the cases of one test file and reports each. It returns
// an error only when dagu test is interrupted.
func runTestFile(ctx *Context, file testFileRef, opts testOptions, report *testReport) error {
	loaded, err := dagtest.LoadFile(ctx, file.path, testLoadOptions(ctx)...)
	if err != nil {
		invalid, ok := errors.AsType[*dagtest.InvalidFileError](err)
		if !ok {
			invalid = &dagtest.InvalidFileError{Errors: []dagtest.Finding{{Message: err.Error()}}}
		}
		report.writeInvalidFile(file.report, invalid.Errors)
		return nil
	}
	for _, c := range loaded.Cases {
		result := runTestCase(ctx, loaded, c, opts)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		result.file = file.report
		report.writeCase(result)
	}
	return nil
}

// testLoadOptions returns the options a tested DAG loads with, as dagu start
// loads it.
func testLoadOptions(ctx *Context) []spec.LoadOption {
	return []spec.LoadOption{
		spec.WithBaseConfig(ctx.Config.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
	}
}

// runTestCase runs one case in its own case directory and checks its
// assertions. An invalid case runs nothing.
func runTestCase(ctx *Context, file *dagtest.File, c dagtest.Case, opts testOptions) (result caseResult) {
	result.name = c.Name
	result.errors = append(slices.Clone(c.Errors), realChildRuns(file.DAG, c)...)
	result.errors = append(result.errors, unsupportedAssertions(c.Expect)...)
	if len(result.errors) > 0 {
		return result
	}

	started := time.Now()
	defer func() { result.duration = time.Since(started) }()
	dag, caseOpts, errs := prepareCase(ctx, file, c)
	if len(errs) > 0 {
		result.errors = errs
		return result
	}

	caseCtx, cancel := context.WithTimeout(ctx.Context, opts.timeout)
	defer cancel()
	tc, err := openCase(ctx.WithContext(caseCtx), caseOpts)
	if err != nil {
		result.failures = []caseFailure{{field: failureError, actual: err.Error()}}
		return result
	}
	defer func() {
		if err := tc.close(); err != nil {
			_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "failed to remove case directory %s: %v\n", tc.dir, err)
		}
	}()
	status, err := tc.run(dag)
	switch {
	case errors.Is(context.Cause(caseCtx), context.DeadlineExceeded):
		result.failures = []caseFailure{{field: failureTimeout, expected: opts.timeout}}
	case err != nil:
		result.failures = []caseFailure{{field: failureError, actual: err.Error()}}
	default:
		result.failures = checkCase(c.Expect, status, tc.waited)
	}
	return result
}

// prepareCase returns the DAG a valid case runs, with the case's params and
// env applied, and the options of its case directory. A param that dagu
// start would reject, and a schedule slot that does not resolve, make the
// case invalid.
func prepareCase(ctx *Context, file *dagtest.File, c dagtest.Case) (*ir.DAG, caseOptions, []dagtest.Finding) {
	invalid := func(source string, err error) (*ir.DAG, caseOptions, []dagtest.Finding) {
		return nil, caseOptions{}, []dagtest.Finding{{Source: source, Message: err.Error()}}
	}
	// The params apply as dagu start -- name=value applies them, and no
	// params[].eval runs before they do, as in LoadFile.
	loadOpts := append(testLoadOptions(ctx), spec.WithParams(quoteStartDashArgs(c.Params)), spec.DeferAllParamEvals())
	dag, err := spec.Load(ctx, file.DAGPath, loadOpts...)
	if err != nil {
		return invalid("params", err)
	}
	applyCaseEnv(dag, c.Env)

	// A slot no schedule fires at is still a slot, and the report is the
	// command's only output, so its warning is not printed.
	var slot runSlot
	if c.ScheduledAt != "" {
		if slot, _, err = resolveScheduledAt(ctx, dag, "", c.ScheduledAt, true); err != nil {
			return invalid("scheduled_at", err)
		}
	}
	slot = withDefaultSlot(ctx, dag, "", slot)
	dates, err := slotDates(ctx, dag, "", slot)
	if err != nil {
		return invalid("scheduled_at", err)
	}

	opts := caseOptions{
		secrets:         c.Secrets,
		trigger:         c.Trigger,
		mocks:           make(map[string]stepmock.Step, len(c.Steps)),
		handlers:        make(map[ir.HandlerType]stepmock.Step, len(c.Handlers)),
		dagPrecondition: c.Precondition,
		slot:            slot,
		dates:           dates,
	}
	for name, script := range c.Steps {
		opts.mocks[name] = scriptedStep(script)
		if script.Real {
			opts.real = append(opts.real, name)
		}
	}
	slices.Sort(opts.real)
	for kind, script := range c.Handlers {
		opts.handlers[kind] = scriptedStep(script)
	}
	return dag, opts, nil
}

// applyCaseEnv sets each KEY=VALUE entry of a case as a DAG-level env entry,
// in place of the DAG's entries of the same name or after its last entry.
func applyCaseEnv(dag *ir.DAG, env []string) {
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		replaced := false
		for i, e := range dag.Env {
			if strings.HasPrefix(e, key+"=") {
				dag.Env[i], replaced = entry, true
			}
		}
		if !replaced {
			dag.Env = append(dag.Env, entry)
		}
	}
}

// scriptedStep returns how the run scripts a step or handler of a case. A
// step whose action the case does not replace keeps it.
func scriptedStep(s dagtest.Script) stepmock.Step {
	step := stepmock.Step{
		Precondition: s.Precondition,
		KeepAction:   !s.ReplacesAction,
		Real:         s.Real,
	}
	for _, r := range s.Results {
		step.Results = append(step.Results, stepmock.Result{
			ExitCode:  r.ExitCode,
			Stdout:    r.Stdout,
			Stderr:    r.Stderr,
			Outputs:   r.Outputs,
			Condition: r.Condition,
		})
	}
	if s.Review != nil {
		step.Review = &stepmock.Review{Reject: s.Review.Reject, Values: s.Review.Values}
	}
	return step
}
