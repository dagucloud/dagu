// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
)

// caseResult is the outcome of one case: invalid when it has errors, failed
// when it has failures, and passed otherwise.
type caseResult struct {
	// file names the case's test file in the report.
	file string
	name string
	// duration is the wall-clock time of a case that ran.
	duration time.Duration
	// errors lists what makes the case invalid. An invalid case runs
	// nothing.
	errors []dagtest.Finding
	// failures lists every failed assertion, or what kept the run from its
	// assertions.
	failures []caseFailure
}

// Fields of a case failure (Spec 101, JSON Report).
const (
	failureStatus   = "status"
	failureAttempts = "attempts"
	failureTimeout  = "timeout"
	failureLimit    = "limit"
	// failureError is a run that could not complete, such as one with a
	// wait the case cannot settle.
	failureError = "error"
)

// caseFailure is one failed assertion of a case, or what kept its run from
// its assertions.
type caseFailure struct {
	// step is the step key as expect.steps writes it, or the step label of
	// a limit failure. Empty for a run-level failure.
	step string
	// field is one of the failure fields above.
	field string
	// expected and actual are the asserted and the actual token or number,
	// the --timeout limit of a timeout, the resolved limit of a limit
	// failure, and the reason of an error.
	expected any
	actual   any
	// message is the reason of a limit failure, as the step's error states
	// it.
	message string
}

// String returns the failure as a line of the text report.
func (f caseFailure) String() string {
	switch {
	case f.field == failureError:
		return fmt.Sprint(f.actual)
	case f.field == failureTimeout:
		return fmt.Sprintf("timeout: did not finish within %v", f.expected)
	case f.field == failureLimit:
		return fmt.Sprintf("step %s: %s", f.step, f.message)
	case f.step == "":
		return fmt.Sprintf("%s: expected %v, got %v", f.field, f.expected, f.actual)
	case f.field == failureStatus:
		return fmt.Sprintf("step %s: expected %v, got %v", f.step, f.expected, f.actual)
	default:
		return fmt.Sprintf("step %s %s: expected %v, got %v", f.step, f.field, f.expected, f.actual)
	}
}

// testReport writes the text report of dagu test as each case ends, and
// counts the cases.
type testReport struct {
	out          io.Writer
	passed       int
	failed       int
	invalid      int
	invalidFiles int
}

func (r *testReport) writeInvalidFile(file string, errs []dagtest.Finding) {
	r.invalidFiles++
	r.printf("INVALID %s\n", file)
	for _, err := range errs {
		r.printf("  %s\n", err)
	}
}

func (r *testReport) writeCase(c caseResult) {
	switch {
	case len(c.errors) > 0:
		r.invalid++
		r.printf("INVALID %s: %s\n", c.file, c.name)
		for _, err := range c.errors {
			r.printf("  %s\n", err)
		}
	case len(c.failures) > 0:
		r.failed++
		r.printf("FAIL %s: %s (%.2fs)\n", c.file, c.name, c.duration.Seconds())
		for _, failure := range c.failures {
			r.printf("  %s\n", failure)
		}
	default:
		r.passed++
		r.printf("PASS %s: %s (%.2fs)\n", c.file, c.name, c.duration.Seconds())
	}
}

// writeSummary writes the last line, which counts the cases by outcome.
func (r *testReport) writeSummary() {
	total := r.passed + r.failed + r.invalid
	r.printf("%d %s: %d passed, %d failed", total, plural(total, "case", "cases"), r.passed, r.failed)
	if r.invalid > 0 {
		r.printf(", %d invalid", r.invalid)
	}
	if r.invalidFiles > 0 {
		r.printf(", %d invalid %s", r.invalidFiles, plural(r.invalidFiles, "file", "files"))
	}
	r.printf("\n")
}

// exitStatus returns the exit status of the reported cases: the highest
// that applies.
func (r *testReport) exitStatus() int {
	switch {
	case r.invalid > 0 || r.invalidFiles > 0:
		return testExitInvalid
	case r.failed > 0:
		return testExitFailed
	default:
		return 0
	}
}

func (r *testReport) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
