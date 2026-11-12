// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec086_backfill_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestSlotRetentionDays(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file        string
		exitCode    int
		stderrParts []string
	}{
		{file: "slot_retention_days.yaml"},
		{file: "slot_retention_days_zero.yaml", exitCode: 1, stderrParts: []string{"slot_retention_days"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			result := harness.NewRunner(t).Run("validate", tc.file)
			result.ExpectExitCode(tc.exitCode)
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}

// project runs commands against one Dagu home, so runs and backfills persist
// between them, with schedules read in UTC.
type project struct {
	*harness.Runner
	env []string
}

func newProject(t *testing.T) project {
	t.Helper()
	return project{
		Runner: harness.NewRunner(t),
		env:    []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"), "DAGU_TZ=UTC"},
	}
}

func (p project) run(args ...string) *harness.Result {
	return p.RunWithEnv(p.env, args...)
}

// runSlot starts a run of a DAG for a past slot and waits for it to end.
func (p project) runSlot(dag, slot string, params ...string) {
	args := []string{"start", "--scheduled-at", slot, dag}
	if len(params) > 0 {
		args = append(append(args, "--"), params...)
	}
	p.run(args...)
}

type planJSON struct {
	Clamped   bool           `json:"clamped"`
	RunCounts map[string]int `json:"runCounts"`
	Slots     []struct {
		Slot      string `json:"slot"`
		Action    string `json:"action"`
		CoveredBy string `json:"coveredBy"`
	} `json:"slots"`
}

func (p project) plan(t *testing.T, args ...string) planJSON {
	t.Helper()
	result := p.run(append([]string{"backfill", "start", "--dry-run", "--format", "json"}, args...)...)
	result.ExpectExitCode(0)
	var plan planJSON
	if err := json.Unmarshal([]byte(result.Stdout()), &plan); err != nil {
		t.Fatalf("decoding plan: %v\n%s", err, result.Stdout())
	}
	return plan
}

func (p planJSON) actions() []string {
	actions := make([]string, len(p.Slots))
	for i, slot := range p.Slots {
		actions[i] = slot.Action
	}
	return actions
}

func expectActions(t *testing.T, plan planJSON, want ...string) {
	t.Helper()
	if got := plan.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %q, want %q", got, want)
	}
}

// Slots whose latest run succeeded or failed are skipped under the default
// rerun, and --rerun failed and all count more slots.
func TestPlanFollowsSlotHistory(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.runSlot("etl.yaml", "2025-10-02")
	p.runSlot("etl.yaml", "2025-10-03", "FAIL=1")

	plan := p.plan(t, "etl.yaml", "--from", "2025-10-01", "--to", "2025-10-05")

	expectActions(t, plan, "run", "skip: succeeded", "skip: failed", "run", "run")
	if plan.RunCounts["none"] != 3 || plan.RunCounts["failed"] != 4 || plan.RunCounts["all"] != 5 {
		t.Fatalf("runCounts = %v", plan.RunCounts)
	}
	expectActions(t, p.plan(t, "etl.yaml", "--from", "2025-10-01", "--to", "2025-10-05", "--rerun", "failed"),
		"run", "skip: succeeded", "run", "run", "run")
}

// A slot keeps the status of a run that history retention removed.
func TestRetentionKeepsSlotStatus(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	// With one run kept, the second run's start removes the first.
	p.runSlot("retained.yaml", "2025-10-02")
	p.runSlot("retained.yaml", "2025-10-03")

	expectActions(t, p.plan(t, "retained.yaml", "--from", "2025-10-02", "--to", "2025-10-03"),
		"skip: succeeded", "skip: succeeded")
}

// A run an operator removes no longer counts for its slot.
func TestOperatorRemovalDropsSlot(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.runSlot("etl.yaml", "2025-10-02")
	p.run("rm", "--history", "--force", "etl").ExpectExitCode(0)

	expectActions(t, p.plan(t, "etl.yaml", "--from", "2025-10-02", "--to", "2025-10-02"), "run")
}

// After the schedule moves from 02:00 to 01:00, the scheduler's 02:00 run of
// a day covers that day's 01:00 slot.
func TestEarlierScheduleCoversSlot(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.run("start", "--trigger-type=scheduler", "--schedule-time=2025-10-02T02:00:00Z", "moved.yaml")
	moved, err := os.ReadFile(filepath.Join("testdata", "moved_to_0100.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p.WriteFile("moved.yaml", string(moved))

	plan := p.plan(t, "moved.yaml", "--from", "2025-10-02", "--to", "2025-10-03")

	expectActions(t, plan, "skip: succeeded", "run")
	if plan.Slots[0].CoveredBy != "2025-10-02T02:00:00Z" {
		t.Fatalf("coveredBy = %q", plan.Slots[0].CoveredBy)
	}
}

func TestStartRejects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		env    []string
		args   []string
		stderr string
	}{
		{name: "from later than now", args: []string{"--from", "2099-01-01", "--dry-run"}, stderr: "later than now"},
		{name: "slot that is not a fire time", args: []string{"--slot", "2025-10-02T03:00", "--dry-run"}, stderr: "not a fire time"},
		{name: "slot with a range", args: []string{"--slot", "2025-10-02T02:00", "--from", "2025-10-01", "--dry-run"}, stderr: "slots cannot be combined"},
		{name: "no range", args: []string{"--dry-run"}, stderr: "give from"},
		{name: "more slots than the bound", env: []string{"DAGU_SCHEDULER_BACKFILL_MAX_SLOTS=3"}, args: []string{"--from", "2025-10-01", "--to", "2025-10-05", "--dry-run"}, stderr: "more than 3 slots"},
		{name: "no terminal and no --yes", args: []string{"--from", "2025-10-01", "--to", "2025-10-05"}, stderr: "--yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := harness.NewRunner(t).RunWithEnv(tc.env, append([]string{"backfill", "start", "etl.yaml"}, tc.args...)...)
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.stderr)
		})
	}
}

func TestToIsClampedToNow(t *testing.T) {
	t.Parallel()
	plan := newProject(t).plan(t, "etl.yaml", "--from", "2025-10-01", "--to", "2099-01-01")
	if !plan.Clamped {
		t.Fatal("plan is not clamped")
	}
}

// Submitting the same backfill while it is open starts nothing, and an
// overlapping one leaves the open backfill's slots to it.
func TestSubmitWhileOpen(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	submit := func(from, to string) map[string]any {
		t.Helper()
		result := p.run("backfill", "start", "etl.yaml", "--from", from, "--to", to, "--yes", "--format", "json")
		result.ExpectExitCode(0)
		var out map[string]any
		if err := json.Unmarshal([]byte(result.Stdout()), &out); err != nil {
			t.Fatalf("decoding submit: %v\n%s", err, result.Stdout())
		}
		return out
	}

	first := submit("2025-10-01", "2025-10-03")
	if first["id"] == nil || first["existing"] == true {
		t.Fatalf("first submit = %v", first)
	}
	again := submit("2025-10-01", "2025-10-03")
	if again["id"] != first["id"] || again["existing"] != true {
		t.Fatalf("second submit = %v, want backfill %v", again, first["id"])
	}

	expectActions(t, p.plan(t, "etl.yaml", "--from", "2025-10-03", "--to", "2025-10-04"), "skip: active", "run")
}

// A running scheduler queues a backfill's runs, one slot after another, and
// the backfill ends succeeded once every slot's run has.
func TestSchedulerRunsBackfill(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	env := append(slices.Clone(p.env), fmt.Sprintf("DAGU_SCHEDULER_PORT=%d", harness.FreePort(t)))
	scheduler := p.StartWithEnv(env, "scheduler", "--dags="+p.ProjectPath("."))
	t.Cleanup(func() {
		scheduler.Stop()
		if t.Failed() {
			t.Logf("scheduler output:\n%s", scheduler.FailureOutput())
		}
	})

	submitted := p.run("backfill", "start", "etl.yaml", "--from", "2025-10-01", "--to", "2025-10-02", "--yes", "--format", "json")
	submitted.ExpectExitCode(0)
	var submit struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(submitted.Stdout()), &submit); err != nil || submit.ID == "" {
		t.Fatalf("decoding submit: %v\n%s", err, submitted.Stdout())
	}

	type statusJSON struct {
		Status string `json:"status"`
		Slots  []struct {
			Slot  string `json:"slot"`
			State string `json:"state"`
			RunID string `json:"runId"`
		} `json:"slots"`
	}
	var status statusJSON
	deadline := time.Now().Add(2 * harness.WaitTimeout(t))
	for {
		result := p.run("backfill", "status", submit.ID, "--format", "json")
		result.ExpectExitCode(0)
		status = statusJSON{}
		if err := json.Unmarshal([]byte(result.Stdout()), &status); err != nil {
			t.Fatalf("decoding status: %v\n%s", err, result.Stdout())
		}
		if status.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backfill did not succeed: %+v", status)
		}
		time.Sleep(time.Second)
	}
	for i, want := range []string{"2025-10-01T02:00:00Z", "2025-10-02T02:00:00Z"} {
		slot := status.Slots[i]
		if slot.Slot != want || slot.State != "succeeded" || !strings.HasPrefix(slot.RunID, "backfill-"+submit.ID+"-") {
			t.Fatalf("slot %d = %+v, want %s succeeded by a run of the backfill", i, slot, want)
		}
	}
	listed := p.run("backfill", "ls", "etl")
	listed.ExpectExitCode(0)
	if !strings.Contains(listed.Stdout(), submit.ID) {
		t.Fatalf("backfill ls does not list %s:\n%s", submit.ID, listed.Stdout())
	}
}

// Cancelling an open backfill cancels its slots, and resuming it runs them
// again with new runs.
func TestCancelAndResume(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	submitted := p.run("backfill", "start", "etl.yaml", "--from", "2025-10-01", "--to", "2025-10-03", "--yes", "--format", "json")
	submitted.ExpectExitCode(0)
	var submit struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(submitted.Stdout()), &submit); err != nil {
		t.Fatalf("decoding submit: %v\n%s", err, submitted.Stdout())
	}
	status := func() (string, []string) {
		t.Helper()
		result := p.run("backfill", "status", submit.ID, "--format", "json")
		result.ExpectExitCode(0)
		var out struct {
			Status string `json:"status"`
			Slots  []struct {
				State string `json:"state"`
			} `json:"slots"`
		}
		if err := json.Unmarshal([]byte(result.Stdout()), &out); err != nil {
			t.Fatalf("decoding status: %v\n%s", err, result.Stdout())
		}
		states := make([]string, len(out.Slots))
		for i, slot := range out.Slots {
			states[i] = slot.State
		}
		return out.Status, states
	}

	p.run("backfill", "cancel", submit.ID).ExpectExitCode(0)
	if got, states := status(); got != "cancelled" || strings.Join(states, ",") != "cancelled,cancelled,cancelled" {
		t.Fatalf("after cancel: %s %v", got, states)
	}
	again := p.run("backfill", "cancel", submit.ID)
	again.ExpectExitCode(1)
	again.ExpectStderrContains("already ended")

	p.run("backfill", "resume", submit.ID, "--yes").ExpectExitCode(0)
	if got, states := status(); got != "waiting for scheduler" || strings.Join(states, ",") != "pending,pending,pending" {
		t.Fatalf("after resume: %s %v", got, states)
	}
}
