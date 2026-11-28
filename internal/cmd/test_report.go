// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
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
	// fix and fileFix hold the YAML that handles the unhandled sources of
	// an invalid case: case fields, and the test file's secrets.
	fix     string
	fileFix string
	// expect holds the assertions that the run was checked against, set
	// when the run ended without a timeout or an error.
	expect dagtest.Expect
	// failures lists every failed assertion, or what kept the run from its
	// assertions.
	failures []caseFailure
	// defaultMocked lists the labels of the steps and handlers the default
	// mock applies to in the case, whether or not the run reached them:
	// steps in DAG order, then handlers.
	defaultMocked []string
	// autoApproved lists the labels of the steps whose waits the case
	// approved automatically, in DAG order.
	autoApproved []string
	// notifications lists the notifications the run would have sent, in
	// the order it would have sent them.
	notifications []testNotification
	// caseDir is the absolute path of the case directory that --keep kept.
	caseDir string
	// update is what --update did with the case's expect, or nil without
	// it.
	update *caseUpdate
}

// Fields of a case failure (Spec 101, JSON Report).
const (
	failureStatus        = "status"
	failureAttempts      = "attempts"
	failureNotifications = "notifications"
	failureTimeout       = "timeout"
	failureLimit         = "limit"
	// failureError is a run that could not complete, such as one with a
	// wait the case cannot settle.
	failureError = "error"
	// failureOutputPrefix starts the field of an output assertion, which
	// ends with the output's name.
	failureOutputPrefix = "outputs."
	// failureContains and failureMatches end the field of a stream
	// assertion, which starts with the stream's name.
	failureContains = ".contains"
	failureMatches  = ".matches"
	// failureCall is a called_with assertion on a step with no execution.
	// The fields of its parts start with it: the run text, which a contains
	// or matches part extends, and a with input or a variable, whose name
	// ends its field.
	failureCall            = "called_with"
	failureCallRun         = failureCall + ".run"
	failureCallInputPrefix = failureCall + ".with."
	failureCallEnvPrefix   = failureCall + ".env."
)

// caseFailure is one failed assertion of a case, or what kept its run from
// its assertions.
type caseFailure struct {
	// step is the step key as expect.steps writes it, or the step label of
	// a limit failure. Empty for a run-level failure.
	step string
	// field is one of the failure fields above.
	field string
	// expected and actual are the asserted and the actual value: a token or
	// number, the compact JSON of an output or of a called_with part, the
	// text or expression of a stream assertion, the lists of notifications,
	// the --timeout limit of a timeout, the resolved limit of a limit
	// failure, or the reason of an error. A stream failure, an output the
	// step did not publish, and a called_with part the call lacks have no
	// actual, and a step with no call has neither.
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
	case f.field == failureNotifications:
		return fmt.Sprintf("notifications: expected %s, got %s",
			notificationList(f.expected.([]testNotification)), notificationList(f.actual.([]testNotification)))
	case strings.HasPrefix(f.field, failureOutputPrefix):
		actual := "nothing"
		if f.actual != nil {
			actual = fmt.Sprintf("%s", f.actual)
		}
		return fmt.Sprintf("step %s output %s: expected %s, got %s", f.step, strings.TrimPrefix(f.field, failureOutputPrefix), f.expected, actual)
	case f.field == failureCall:
		return fmt.Sprintf("step %s called_with: expected a call, got none", f.step)
	case f.field == failureCallRun+failureContains:
		return fmt.Sprintf("step %s %s: expected to contain %s, got %s", f.step, failureCallRun, f.expected, f.actual)
	case f.field == failureCallRun+failureMatches:
		return fmt.Sprintf("step %s %s: expected to match %s, got %s", f.step, failureCallRun, f.expected, f.actual)
	case strings.HasPrefix(f.field, failureCall):
		actual := "nothing"
		if f.actual != nil {
			actual = fmt.Sprintf("%s", f.actual)
		}
		return fmt.Sprintf("step %s %s: expected %s, got %s", f.step, f.field, f.expected, actual)
	case strings.HasSuffix(f.field, failureContains):
		return fmt.Sprintf("step %s %s: does not contain %s", f.step, strings.TrimSuffix(f.field, failureContains), quoteJSON(f.expected))
	case strings.HasSuffix(f.field, failureMatches):
		return fmt.Sprintf("step %s %s: does not match %s", f.step, strings.TrimSuffix(f.field, failureMatches), quoteJSON(f.expected))
	case f.step == "":
		return fmt.Sprintf("%s: expected %v, got %v", f.field, f.expected, f.actual)
	case f.field == failureStatus:
		return fmt.Sprintf("step %s: expected %v, got %v", f.step, f.expected, f.actual)
	default:
		return fmt.Sprintf("step %s %s: expected %v, got %v", f.step, f.field, f.expected, f.actual)
	}
}

// Results of a case.
const (
	resultPass    = "pass"
	resultFail    = "fail"
	resultInvalid = "invalid"
)

func (c caseResult) result() string {
	switch {
	case len(c.errors) > 0:
		return resultInvalid
	case len(c.failures) > 0:
		return resultFail
	default:
		return resultPass
	}
}

// testReport writes the report of dagu test and counts the cases. The text
// report is written as each case ends.
type testReport struct {
	out io.Writer
	// json and junit collect the report for --format json and --format
	// junit, which is written once every case has ended. Both nil writes
	// text.
	json         *jsonTestReport
	junit        *junitTestReport
	passed       int
	failed       int
	invalid      int
	invalidFiles int
}

func (r *testReport) writeInvalidFile(file string, errs []dagtest.Finding) {
	r.invalidFiles++
	if r.json != nil {
		r.json.InvalidFiles = append(r.json.InvalidFiles, jsonInvalidFile{File: file, Errors: jsonErrors(errs)})
		return
	}
	if r.junit != nil {
		r.junit.addInvalidFile(file, errs)
		return
	}
	r.printf("INVALID %s\n", file)
	for _, err := range errs {
		r.printf("  %s\n", err)
	}
}

func (r *testReport) writeCase(c caseResult) {
	switch c.result() {
	case resultInvalid:
		r.invalid++
	case resultFail:
		r.failed++
	default:
		r.passed++
	}
	if r.json != nil {
		r.json.Cases = append(r.json.Cases, jsonCaseOf(c))
		return
	}
	if r.junit != nil {
		r.junit.addCase(c)
		return
	}
	switch c.result() {
	case resultInvalid:
		r.printf("INVALID %s: %s\n", c.file, c.name)
		for _, err := range c.errors {
			r.printf("  %s\n", err)
		}
		r.writeFix("fix, ready to paste:", c.fix)
		r.writeFix("fix for the test file, ready to paste:", c.fileFix)
	case resultFail:
		r.printf("FAIL %s: %s (%.2fs)\n", c.file, c.name, c.duration.Seconds())
		for _, failure := range c.failures {
			r.printf("  %s\n", failure)
		}
	default:
		r.printf("PASS %s: %s (%.2fs)\n", c.file, c.name, c.duration.Seconds())
	}
	if len(c.defaultMocked) > 0 {
		r.printf("  default mock: %s\n", strings.Join(c.defaultMocked, ", "))
	}
	if len(c.autoApproved) > 0 {
		r.printf("  approved automatically: %s\n", strings.Join(c.autoApproved, ", "))
	}
	for _, n := range c.notifications {
		r.printf("  %s\n", n.line())
	}
	if c.caseDir != "" {
		r.printf("  case directory %s\n", c.caseDir)
	}
	if c.update != nil {
		for _, k := range c.update.kept {
			r.printf("  %s\n", k)
		}
		r.printf("  %s\n", c.update.line())
	}
}

// writeFix writes a fix under its title, indented by four spaces.
func (r *testReport) writeFix(title, fix string) {
	if fix == "" {
		return
	}
	r.printf("  %s\n", title)
	for line := range strings.Lines(fix) {
		r.printf("    %s", line)
	}
}

// writeSummary ends the report: the text report's last line, which counts
// the cases by outcome, the JSON object, or the JUnit XML document.
func (r *testReport) writeSummary() error {
	if r.json != nil {
		return json.NewEncoder(r.out).Encode(r.json)
	}
	if r.junit != nil {
		return r.junit.write(r.out)
	}
	total := r.passed + r.failed + r.invalid
	r.printf("%d %s: %d passed, %d failed", total, plural(total, "case", "cases"), r.passed, r.failed)
	if r.invalid > 0 {
		r.printf(", %d invalid", r.invalid)
	}
	if r.invalidFiles > 0 {
		r.printf(", %d invalid %s", r.invalidFiles, plural(r.invalidFiles, "file", "files"))
	}
	r.printf("\n")
	return nil
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

// testNotification is a notification as the report writes it.
type testNotification struct {
	Kind string `json:"kind"`
	// Step is the label of the step whose mail_on_error sends it.
	Step string `json:"step,omitempty"`
	// To lists the recipients. An expected notification without to
	// matches any.
	To []string `json:"to,omitempty"`
}

// String writes the notification as an item of a notifications failure.
func (n testNotification) String() string {
	text := n.Kind
	if n.Step != "" {
		text += " step " + n.Step
	}
	if n.To != nil {
		text += " to " + strings.Join(n.To, ", ")
	}
	return text
}

// line writes the notification as a line of a case that ran.
func (n testNotification) line() string {
	to := strings.Join(n.To, ", ")
	if n.Step != "" {
		return fmt.Sprintf("notification %s, step %s: %s", n.Kind, n.Step, to)
	}
	return fmt.Sprintf("notification %s: %s", n.Kind, to)
}

// notificationList writes the notifications of a notifications failure,
// or none.
func notificationList(list []testNotification) string {
	if len(list) == 0 {
		return "none"
	}
	items := make([]string, 0, len(list))
	for _, n := range list {
		items = append(items, n.String())
	}
	return strings.Join(items, "; ")
}

// sentNotifications returns the notifications a run would have sent, as
// the report writes them.
func sentNotifications(dag *ir.DAG, sent []agent.Notification) []testNotification {
	out := make([]testNotification, 0, len(sent))
	for _, n := range sent {
		out = append(out, newTestNotification(dag, string(n.Kind), n.Step, n.To))
	}
	return out
}

// expectedNotifications returns the notifications a case expects, as the
// report writes them.
func expectedNotifications(dag *ir.DAG, expected []dagtest.Notification) []testNotification {
	out := make([]testNotification, 0, len(expected))
	for _, n := range expected {
		out = append(out, newTestNotification(dag, n.Kind, n.Step, n.To))
	}
	return out
}

// newTestNotification names the sending step, given by name, by its label.
func newTestNotification(dag *ir.DAG, kind, step string, to []string) testNotification {
	n := testNotification{Kind: kind, To: to}
	if step != "" {
		n.Step = stepLabelOf(dag, step)
	}
	return n
}

// quoteJSON writes text as a JSON string.
func quoteJSON(text any) string {
	data, _ := jsonutil.MarshalUnescaped(text)
	return string(data)
}
