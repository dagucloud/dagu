// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// checkInitArgs returns the usage error of --init with --update, with other
// than one argument, or with a flag that selects or reports cases.
// --timeout bounds the run of its one case.
func checkInitArgs(opts testOptions, args []string) error {
	switch {
	case opts.update:
		return errors.New("--update cannot be used with --init")
	case len(args) != 1:
		return fmt.Errorf("--init takes exactly one DAG file, got %d arguments", len(args))
	case opts.run != nil:
		return errors.New("--run cannot be used with --init")
	case opts.keep:
		return errors.New("--keep cannot be used with --init")
	case opts.json:
		return errors.New("--format json cannot be used with --init")
	case opts.junit:
		return errors.New("--format junit cannot be used with --init")
	}
	return nil
}

// initUnfinished ends the report of a written case whose run did not
// finish, since no run observed its expect.
const initUnfinished = "the run did not finish, so the written expect is status: succeeded, not the run's result"

// factsOfRun checks a case's run against the run's own facts, which pass
// once the run finishes.
func factsOfRun(_ dagtest.Expect, tc *testCase, status *ir.DAGRunStatus) dagtest.Expect {
	return tc.expectFromRun(status)
}

// runTestInit writes the test file of the DAG file at dagPath (Spec 101,
// Authoring Flags) and prints its path. Its case expects the facts of a run
// with its mocks. A case that is still invalid, or whose run does not
// finish, expects status: succeeded, and its report follows the path. An
// interrupted run writes nothing.
func runTestInit(ctx *Context, dagPath string, opts testOptions) error {
	draft, err := dagtest.NewDraft(ctx, dagPath, testLoadOptions(ctx)...)
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}
	c := draft.File.Cases[0]
	result := runTestCase(ctx, draft.File, c, opts, factsOfRun)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	expect := c.Expect
	if result.result() == resultPass {
		expect = result.expect
	}
	if err := draft.Write(expect); err != nil {
		return err
	}

	abs, err := filepath.Abs(draft.File.Path)
	if err != nil {
		return err
	}
	result.file = testReportPath(abs)
	report := &testReport{out: ctx.Command.OutOrStdout()}
	report.printf("%s\n", result.file)
	if result.result() == resultPass {
		return nil
	}
	report.writeCase(result)
	if result.result() == resultFail {
		report.printf("  %s\n", initUnfinished)
	}
	// The report already names why the written case does not pass.
	ctx.Command.SilenceErrors = true
	return &exitStatusError{code: report.exitStatus(), err: errors.New("the written test case does not pass")}
}
