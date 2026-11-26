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
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/spf13/cobra"
)

// Exit statuses of dagu test other than 0 (Spec 101).
const (
	testExitFailed  = 1
	testExitInvalid = 2
)

var (
	testRunFlag = commandLineFlag{
		name:  "run",
		usage: "Run only the cases whose name this RE2 regular expression matches",
	}
	testTimeoutFlag = commandLineFlag{
		name:         "timeout",
		defaultValue: "5m",
		usage:        "Longest a case may run, such as 30s or 5m",
	}
	testFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "text",
		usage:        "Report format: text (default) or json",
	}
	testKeepFlag = commandLineFlag{
		name:   "keep",
		usage:  "Keep each case directory and report its path",
		isBool: true,
	}
	testInitFlag = commandLineFlag{
		name:   "init",
		usage:  "Write a test file for the DAG file given as the one argument",
		isBool: true,
	}
	testUpdateFlag = commandLineFlag{
		name:   "update",
		usage:  "Rewrite the expect of each case from its run",
		isBool: true,
	}
)

var testFlags = []commandLineFlag{testRunFlag, testTimeoutFlag, testFormatFlag, testKeepFlag, testInitFlag, testUpdateFlag}

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

--format json writes one JSON object to stdout once every case has ended.
--keep keeps each case directory and reports its path.

--init writes the test file of the DAG file given as the one argument, with
a mock for every step that needs one and one case that expects the result
of a run with those mocks, and prints its path. It never replaces a test
file.

--update runs the selected cases and rewrites the expect of each from its
run, so a change in behavior shows as a diff to review. It never removes an
assertion, keeps stdout and stderr assertions as written, and leaves
invalid cases and runs that do not finish as they are.

Exit status: 0 when every case passed, 1 when a case failed, and 2 when a
test file or case is invalid or the arguments are.

Example:
  dagu test workflows/
  dagu test --run 'failed build' --timeout 30s workflows/release.test.yaml
  dagu test --format json --keep workflows/
  dagu test --init workflows/release.yaml
  dagu test --update workflows/release.test.yaml
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
	// run selects the cases by name, or is nil to select every case.
	run *regexp.Regexp
	// timeout is the longest a case may run.
	timeout time.Duration
	// json writes the report as one JSON object.
	json bool
	// keep keeps each case directory.
	keep bool
	// init writes the test file of the one DAG file argument.
	init bool
	// update rewrites the expect of each case from its run.
	update bool
}

func runTest(ctx *Context, args []string) error {
	opts, err := testOptionsFromFlags(ctx)
	if err == nil && opts.init {
		err = checkInitArgs(opts, args)
	}
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}

	// An interrupt stops the running case and starts no further one.
	runCtx, stop := notifyShutdownContext(ctx.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = ctx.WithContext(runCtx)
	if opts.init {
		return runTestInit(ctx, args[0], opts)
	}
	files, err := selectTestFiles(ctx.projectPaths().DAGsDir, args)
	if err == nil && len(args) == 0 {
		files, err = ctx.ownTestFiles(files)
	}
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}

	report := &testReport{out: ctx.Command.OutOrStdout()}
	if opts.json {
		report.json = newJSONTestReport()
	}
	// A test file that --update cannot write does not stop the others.
	var updateErrs []error
	for _, file := range files {
		// A test file runs in the project of the DAG it tests.
		fileCtx, err := ctx.inProjectOf(file.path)
		if err != nil {
			return &exitStatusError{code: testExitInvalid, err: err}
		}
		if err := runTestFile(fileCtx, file, opts, report); err != nil {
			if ctx.Err() != nil {
				return err
			}
			updateErrs = append(updateErrs, err)
		}
	}
	if err := report.writeSummary(); err != nil {
		return err
	}
	if err := errors.Join(updateErrs...); err != nil {
		return &exitStatusError{code: max(report.exitStatus(), testExitFailed), err: err}
	}
	if code := report.exitStatus(); code != 0 {
		// The report already names every failed and invalid case.
		ctx.Command.SilenceErrors = true
		return &exitStatusError{code: code, err: errors.New("workflow tests did not pass")}
	}
	return nil
}

func testOptionsFromFlags(ctx *Context) (testOptions, error) {
	var opts testOptions
	value, err := ctx.StringParam(testTimeoutFlag.name)
	if err != nil {
		return opts, err
	}
	if opts.timeout, err = time.ParseDuration(value); err != nil || opts.timeout <= 0 {
		return opts, fmt.Errorf("--timeout %q: use a duration greater than zero, such as 30s or 5m", value)
	}
	if value, err = ctx.StringParam(testRunFlag.name); err != nil {
		return opts, err
	}
	if value != "" {
		if opts.run, err = regexp.Compile(value); err != nil {
			return opts, fmt.Errorf("--run %q: %w", value, err)
		}
	}
	format, err := outputFormatParam(ctx)
	if err != nil {
		return opts, err
	}
	opts.json = format == "json"
	if opts.keep, err = ctx.Command.Flags().GetBool(testKeepFlag.name); err != nil {
		return opts, err
	}
	if opts.init, err = ctx.Command.Flags().GetBool(testInitFlag.name); err != nil {
		return opts, err
	}
	opts.update, err = ctx.Command.Flags().GetBool(testUpdateFlag.name)
	return opts, err
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
			if !spec.IsTestFile(arg) {
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
		case entry.Type().IsRegular() && spec.IsTestFile(path):
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

// runTestFile runs the cases of one test file and reports each. With
// --update, it then writes the expects the runs updated. It returns an error
// only when dagu test is interrupted, or when the file cannot be updated.
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
	var update *dagtest.Update
	if opts.update {
		if update, err = dagtest.NewUpdate(loaded); err != nil {
			return err
		}
	}
	for _, c := range loaded.Cases {
		// A case that --run does not select is neither checked nor reported.
		if opts.run != nil && !opts.run.MatchString(c.Name) {
			continue
		}
		expect := caseExpect(asWritten)
		var outcome *caseUpdate
		if update != nil {
			outcome = &caseUpdate{outcome: updateKept, reason: keptUnfinished}
			expect = updateExpect(update, c.Name, outcome)
		}
		result := runTestCase(ctx, loaded, c, opts, expect)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		result.file = file.report
		if outcome != nil && result.result() == resultInvalid {
			outcome.reason = keptInvalid
		}
		result.update = outcome
		report.writeCase(result)
	}
	if update != nil {
		return update.Write()
	}
	return nil
}

// testLoadOptions returns the options a tested DAG loads with, as dagu start
// loads it.
func testLoadOptions(ctx *Context) []spec.LoadOption {
	return ctx.projectLoadOptions()
}

// caseExpect returns the assertions that a case's run is checked against,
// from the case's expect as written, its run, and the status of the run's
// last attempt.
type caseExpect func(written dagtest.Expect, tc *testCase, status *ir.DAGRunStatus) dagtest.Expect

// asWritten checks a case's run against its expect as written.
func asWritten(written dagtest.Expect, _ *testCase, _ *ir.DAGRunStatus) dagtest.Expect {
	return written
}

// runTestCase runs one case in its own case directory and checks the
// assertions that expect returns. An invalid case runs nothing.
func runTestCase(ctx *Context, file *dagtest.File, c dagtest.Case, opts testOptions, expect caseExpect) (result caseResult) {
	result.name = c.Name
	result.errors = append(slices.Clone(c.Errors), dagtest.CheckRealChildren(file.DAG, c, childResolver(ctx, file.DAG))...)
	if len(result.errors) > 0 {
		result.fix, result.fileFix = c.Fix, c.FileFix
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
	caseOpts.keep = opts.keep
	tc, err := openCase(ctx.WithContext(caseCtx), caseOpts)
	if err != nil {
		result.failures = []caseFailure{{field: failureError, actual: err.Error()}}
		return result
	}
	if opts.keep {
		result.caseDir, _ = filepath.Abs(tc.dir)
	}
	defer func() {
		if err := tc.close(); err != nil {
			_, _ = fmt.Fprintf(ctx.Command.ErrOrStderr(), "failed to remove case directory %s: %v\n", tc.dir, err)
		}
	}()
	status, err := tc.run(dag)
	result.defaultMocked = c.DefaultMocked
	result.autoApproved = stepLabels(dag, tc.autoApproved)
	result.notifications = sentNotifications(dag, tc.notifications)
	switch {
	case errors.Is(context.Cause(caseCtx), context.DeadlineExceeded):
		result.failures = []caseFailure{{field: failureTimeout, expected: opts.timeout}}
	case err != nil:
		result.failures = []caseFailure{{field: failureError, actual: err.Error()}}
	default:
		result.expect = expect(c.Expect, tc, status)
		result.failures = tc.check(result.expect, status)
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
	// params[].eval runs before they do, as in LoadFile. Each action step
	// keeps its with as written, which called_with asserts on.
	loadOpts := append(testLoadOptions(ctx), spec.WithParams(quoteStartDashArgs(c.Params)), spec.DeferAllParamEvals(), spec.KeepActionWith())
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

// childResolver returns the child DAGs that the steps of parent start, as
// an in-process child run loads them: a document of the parent's file by
// name, else the DAG the repository finds. A child takes no handler from
// the base config, and none of its params[].eval runs.
func childResolver(ctx *Context, parent *ir.DAG) func(name string) (*ir.DAG, error) {
	return func(name string) (*ir.DAG, error) {
		opts := append(testLoadOptions(ctx), spec.WithSkipBaseHandlers(), spec.DeferAllParamEvals())
		if local, ok := parent.LocalDAGs[name]; ok {
			return spec.LoadYAML(ctx, local.YamlData, append(opts, spec.WithName(name))...)
		}
		repository, err := ctx.dagRepository(dagRepositoryConfig{SearchPaths: []string{filepath.Dir(parent.Location)}})
		if err != nil {
			return nil, err
		}
		found, err := repository.GetDetails(ctx, name, persis.DAGLoadOptions{})
		if err != nil {
			return nil, err
		}
		return spec.Load(ctx, found.Location, opts...)
	}
}
