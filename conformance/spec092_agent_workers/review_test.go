// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"os/exec"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
)

// Tests of the threat model of Spec 092. Each test fails while the property is
// broken. They need the host of harness.RequireRestrictedUserHost: run them in a
// root container with the flag no-new-privileges.

// Property (threat model): the agent cannot read a secret of the DAG. An
// untrusted step runs at the same time as a trusted step that lists the
// secret. Dagu puts the value of ${GIT_TOKEN} in the text of the command, so
// the value is in the arguments of a process of the worker. Each user can read
// /proc/<pid>/cmdline.
func TestReviewSecretInArguments(t *testing.T) {
	untrustedRequireRoot(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"REVIEW_GIT_TOKEN=review-token-abc123"},
		"start", "--config", dagu.ProjectPath("review_isolation_config.yaml"), "review_argv.yaml")
	out := result.Stdout() + result.Stderr()
	t.Log(out)
	assert.Contains(t, out, "uid=64031", "the agent step runs as the restricted user")
	// The agent prints the value reversed, so that the masking does not hide it.
	assert.NotContains(t, out, "stolen-reversed=321cba-nekot-weiver", "the restricted user read the secret in /proc/<pid>/cmdline")
}

// Property (threat "The agent leaves a process that watches a later step"):
// at the end of an untrusted step, Dagu stops each process of the restricted
// user. The agent leaves a process whose main thread exited: the end of the
// step does not see it. Its thread reads the arguments of the later trusted
// step push, which has the token in its URL.
func TestReviewLeftoverThreadWatchesLaterStep(t *testing.T) {
	untrustedRequireRoot(t)
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("needs gcc")
	}

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"REVIEW_GIT_TOKEN=review-token-abc123"},
		"start", "--config", dagu.ProjectPath("review_isolation_config.yaml"), "review_leftover_thread.yaml")
	out := result.Stdout() + result.Stderr()
	t.Log(out)
	assert.Contains(t, out, "agent:done")
	assert.Contains(t, out, "report:done")
	assert.NotContains(t, out, "stolen-after-end-reversed=321cba-nekot-weiver", "a thread of the agent read the secret of a later step")
}

// Property (threat ".git"): the agent cannot make a later trusted step run its
// code through git. The agent makes a bare repository in a folder of the
// repository; a trusted git status below it runs core.fsmonitor as root.
func TestReviewBareRepository(t *testing.T) {
	untrustedRequireRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--config", dagu.ProjectPath("review_isolation_config.yaml"), "review_bare_repo.yaml")
	out := result.Stdout() + result.Stderr()
	t.Log(out)
	assert.Contains(t, out, "agent:done")
	assert.Contains(t, out, "the restricted user made the git folder", "the end of the untrusted step finds the git folder of the agent")
	assert.Contains(t, out, "fsmonitor-ran-as=none", "a trusted git command ran a command of the agent")
}

// Property (threat model, "The agent returns a false result"): a gate that Dagu
// runs protects an action. The step gate of the example of Spec 092 puts an
// output of the agent step in the text of a shell command. Dagu substitutes
// ${steps.code.outputs.summary} in the text, so a quote in the value makes the
// rest of the value shell code of a trusted step, as root.
//
// dagu validate rejects the reference in the command of the trusted step. The
// value reaches the shell through env, as data.
func TestReviewOutputInjection(t *testing.T) {
	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "review_inject.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`step "gate"`, "${steps.code.outputs.summary}", `an output of the untrusted step "code"`, "pass it through env")

	untrustedRequireRoot(t)
	result = dagu.Run("start", "--config", dagu.ProjectPath("review_isolation_config.yaml"), "review_inject_env.yaml")
	out := result.Stdout() + result.Stderr()
	t.Log(out)
	result.ExpectExitCode(0)
	assert.Contains(t, out, "injected-as=none", "an output of the agent ran as shell code of a trusted step")
}
