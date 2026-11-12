// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of Spec 092, "A verify loop". The fixture loop_rounds.yaml has three
// rounds of code_N and verify_N in one DAG. The fake claude (testdata/verify_bin)
// plays the scenarios loop, loop_stuck, loop_fast and loop_crash. The tests set
// DAGU_COORDINATOR_ENABLED=false and untrusted.isolation: off, so the CLI agent
// runs on the host with no isolation. The isolation of the steps is the subject of
// the other tests.

// Status values of a node and of a run in the saved status (internal/ir/status.go).
const (
	loopFailed    = 2
	loopAborted   = 3
	loopSucceeded = 4
	loopSkipped   = 5
	loopPartial   = 6
)

// loopRun runs loop_rounds.yaml with a scenario of the fake claude.
func loopRun(t *testing.T, scenario string) (*verifyProject, string) {
	t.Helper()
	p := newVerifyProject(t)
	result := p.startWithEnv("loop_rounds.yaml", "FAKE_CLAUDE_SCENARIO="+scenario)
	if scenario == "loop_fast" {
		result.ExpectExitCode(0)
	} else {
		// A failed verify that the run continues after, or a failed run, ends with 1.
		result.ExpectExitCode(1)
	}
	return p, verifyOutput(result)
}

// workFile returns the path of a file in the work directory of the only run, or
// "" when the file does not exist.
func (p *verifyProject) workFile(name string) string {
	p.t.Helper()
	var found string
	err := filepath.WalkDir(p.home, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() == name && strings.Contains(filepath.ToSlash(path), "/dag-run-work/") {
			found = path
		}
		return nil
	})
	require.NoError(p.t, err)
	return found
}

func (p *verifyProject) rounds() []string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, "rounds"))
	require.NoError(p.t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (p *verifyProject) requireStatuses(want map[string]int) {
	p.t.Helper()
	for id, status := range want {
		assert.Equal(p.t, status, p.node(id).Status, "status of the step %s", id)
	}
}

// Round 1 fixes the visible failure and the verify fails on a second one. Round
// 2 reads the log of the verify, fixes the second failure, and the verify
// passes. The last round and its verify are skipped, and the steps after the
// loop run once. The run ends with the status partially succeeded, because a
// verify failed and the run went on.
func TestLoopFixesInRoundTwo(t *testing.T) {
	t.Parallel()

	p, out := loopRun(t, "loop")

	p.requireStatuses(map[string]int{
		"code_1": loopSucceeded, "verify_1": loopFailed,
		"code_2": loopSucceeded, "verify_2": loopSucceeded,
		"code_3": loopSkipped, "verify_3": loopSkipped,
		"check_paths": loopSucceeded, "push": loopSucceeded,
	})
	assert.Equal(t, loopPartial, p.status().Status)
	assert.Equal(t, 2, p.calls(), "the CLI ran in round 1 and round 2 only")

	rounds := p.rounds()
	require.Len(t, rounds, 2)
	assert.Equal(t, "1 first", rounds[0])
	assert.True(t, strings.HasPrefix(rounds[1], "2 read "), "round 2 read a log: %s", rounds[1])
	assert.True(t, strings.HasSuffix(rounds[1], "/verify_1.log"), "round 2 read the log of verify_1: %s", rounds[1])

	log, err := os.ReadFile(p.workFile("verify_1.log"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "need: two.txt must say fixed")
	assert.Contains(t, out, "FAIL: two.txt is not fixed", "the log of the verify is also in the step log")
	assert.NotEmpty(t, p.workFile("push.marker"))
	assert.NotEmpty(t, p.workFile("check_paths.marker"))
}

// The fake claude does not fix anything in a later round. Each round runs, each
// verify fails, the run fails, and no step after the loop runs: nothing is pushed.
func TestLoopFailsWhenAllRoundsFail(t *testing.T) {
	t.Parallel()

	p, _ := loopRun(t, "loop_stuck")

	p.requireStatuses(map[string]int{
		"code_1": loopSucceeded, "verify_1": loopFailed,
		"code_2": loopSucceeded, "verify_2": loopFailed,
		"code_3": loopSucceeded, "verify_3": loopFailed,
		"check_paths": loopAborted, "push": loopAborted,
	})
	assert.Equal(t, loopFailed, p.status().Status)
	assert.Equal(t, 3, p.calls(), "the CLI ran in three rounds, no more")
	assert.Empty(t, p.workFile("push.marker"))
	assert.Empty(t, p.workFile("check_paths.marker"))
}

// The verify passes after round 1: the later rounds and their verify steps are
// skipped, and the run succeeds.
func TestLoopPassesInRoundOne(t *testing.T) {
	t.Parallel()

	p, _ := loopRun(t, "loop_fast")

	p.requireStatuses(map[string]int{
		"code_1": loopSucceeded, "verify_1": loopSucceeded,
		"code_2": loopSkipped, "verify_2": loopSkipped,
		"code_3": loopSkipped, "verify_3": loopSkipped,
		"check_paths": loopSucceeded, "push": loopSucceeded,
	})
	assert.Equal(t, loopSucceeded, p.status().Status)
	assert.Equal(t, 1, p.calls())
	assert.NotEmpty(t, p.workFile("push.marker"))
}

// An agent that fails in round 2 ends the run: the verify of the failed round
// does not run, the run does not fall through to the steps after the loop, and
// nothing is pushed.
func TestLoopFailsWhenARoundFails(t *testing.T) {
	t.Parallel()

	p, _ := loopRun(t, "loop_crash")

	p.requireStatuses(map[string]int{
		"code_1": loopSucceeded, "verify_1": loopFailed,
		"code_2": loopFailed, "verify_2": loopAborted,
		"code_3": loopAborted, "verify_3": loopAborted,
		"check_paths": loopAborted, "push": loopAborted,
	})
	assert.Equal(t, loopFailed, p.status().Status)
	assert.Empty(t, p.workFile("push.marker"))
}
