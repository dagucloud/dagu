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

// checkInitArgs returns the usage error of --init with other than one
// argument, or with a flag that selects or reports cases. --timeout bounds
// the run of its one case.
func checkInitArgs(opts testOptions, args []string) error {
	switch {
	case len(args) != 1:
		return fmt.Errorf("--init takes exactly one DAG file, got %d arguments", len(args))
	case opts.run != nil:
		return errors.New("--run cannot be used with --init")
	case opts.keep:
		return errors.New("--keep cannot be used with --init")
	case opts.json:
		return errors.New("--format json cannot be used with --init")
	}
	return nil
}

// runTestInit writes the test file of the DAG file at dagPath (Spec 101,
// Authoring Flags) and prints its path. Its case expects the facts of a run
// with its mocks. A case that is still invalid, or whose run fails before
// its assertions, expects status: succeeded, and its report follows the
// path. An interrupted run writes nothing.
func runTestInit(ctx *Context, dagPath string, opts testOptions) error {
	draft, err := dagtest.NewDraft(ctx, dagPath, testLoadOptions(ctx)...)
	if err != nil {
		return &exitStatusError{code: testExitInvalid, err: err}
	}
	c := draft.File.Cases[0]
	var actual dagtest.Expect
	result := runTestCase(ctx, draft.File, c, opts, func(tc *testCase, status *ir.DAGRunStatus) dagtest.Expect {
		actual = tc.expectFromRun(status)
		return actual
	})
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	expect := c.Expect
	if result.result() == resultPass {
		expect = actual
	}
	if err := draft.Write(expect); err != nil {
		return err
	}

	abs, err := filepath.Abs(draft.File.Path)
	if err != nil {
		return err
	}
	report := &testReport{out: ctx.Command.OutOrStdout()}
	report.printf("%s\n", testReportPath(abs))
	if result.result() == resultPass {
		return nil
	}
	result.file = testReportPath(abs)
	report.writeCase(result)
	// The report already names why the written case does not pass.
	ctx.Command.SilenceErrors = true
	return &exitStatusError{code: report.exitStatus(), err: errors.New("the written test case does not pass")}
}
