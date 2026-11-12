// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of the section "Verified points" of Spec 092. Each test runs the Dagu
// binary on a static fixture. The CLI agent is the fake claude in
// testdata/verify_bin. The tests set DAGU_COORDINATOR_ENABLED=false, so the
// agent step runs on the host.

// verifyProject is an isolated project with the fake claude first in PATH.
type verifyProject struct {
	t    *testing.T
	dagu *harness.Runner
	home string
	fake string
}

func newVerifyProject(t *testing.T) *verifyProject {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixtures use POSIX shell commands")
	}
	dagu := harness.NewRunner(t)
	// The mode of a file that git checks out depends on the platform.
	require.NoError(t, os.Chmod(dagu.ProjectPath(filepath.Join("verify_bin", "claude")), 0o755))
	// An agent.run step of the provider claude MUST name an endpoint (Spec 091),
	// so the data directory holds the endpoint that the fixtures name.
	home := filepath.Join(t.TempDir(), "dagu")
	harness.SeedTestEndpoint(t, dagu, home)
	// The process has no restricted user, so its untrusted steps need isolation: off.
	harness.WriteIsolationOff(t, home)
	return &verifyProject{
		t:    t,
		dagu: dagu,
		home: home,
		fake: t.TempDir(),
	}
}

// start runs `dagu start` on a fixture.
func (p *verifyProject) start(fixture string) *harness.Result {
	p.t.Helper()
	return p.startWithEnv(fixture)
}

// startWithEnv runs `dagu start` on a fixture with more variables of the
// environment.
func (p *verifyProject) startWithEnv(fixture string, extra ...string) *harness.Result {
	p.t.Helper()
	return p.dagu.RunWithEnv(append([]string{
		"PATH=" + p.dagu.ProjectPath("verify_bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DAGU_HOME=" + p.home,
		"DAGU_COORDINATOR_ENABLED=false",
		"FAKE_CLAUDE_DIR=" + p.fake,
	}, extra...), "start", fixture)
}

// calls returns how many times the fake claude ran.
func (p *verifyProject) calls() int {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, "count"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(p.t, err)
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(p.t, err)
	return n
}

// verifyNode holds the fields of a node of the saved status that the tests read.
type verifyNode struct {
	Step struct {
		ID string `json:"id"`
	} `json:"step"`
	Status       int    `json:"status"`
	Error        string `json:"error"`
	DoneCount    int    `json:"doneCount"`
	Repeated     bool   `json:"repeated"`
	OutputsValue string `json:"outputsValue"`
}

// verifyStatus holds the fields of the saved status of a run that the tests read.
type verifyStatus struct {
	Status int          `json:"status"`
	Nodes  []verifyNode `json:"nodes"`
}

// node returns the node of a step in the last status of the only root run.
// The runs of sub-DAGs are in sub folders and do not count.
func (p *verifyProject) node(id string) verifyNode {
	p.t.Helper()
	for _, n := range p.status().Nodes {
		if n.Step.ID == id {
			return n
		}
	}
	p.t.Fatalf("no node %q in the saved status", id)
	return verifyNode{}
}

// status returns the last saved status of the only root run.
func (p *verifyProject) status() verifyStatus {
	p.t.Helper()

	var files []string
	err := filepath.WalkDir(p.home, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() == "status.jsonl" && !strings.Contains(filepath.ToSlash(path), "/sub/") {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(p.t, err)
	require.Len(p.t, files, 1, "status files under %s", p.home)

	file, err := os.Open(files[0]) // #nosec G304 -- the path is under the temp home of the test.
	require.NoError(p.t, err)
	defer func() { _ = file.Close() }()
	var last []byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			last = append([]byte(nil), line...)
		}
	}
	require.NoError(p.t, scanner.Err())

	var status verifyStatus
	require.NoError(p.t, json.Unmarshal(last, &status))
	return status
}

// verifyOutput returns the output of a run: Dagu writes its log to stderr.
func verifyOutput(result *harness.Result) string {
	return result.Stdout() + result.Stderr()
}

// ---- Verified point 1: repeat_policy on an agent.run step ----

// The step repeats until the command of its condition succeeds. The fake claude
// creates the file that the command tests at its third call. Each repetition
// runs the CLI again and adds one execution.
func TestVerifyRepeatPolicyUntilCommand(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_repeat_command.yaml").ExpectExitCode(0)

	require.Equal(t, 3, p.calls(), "the CLI ran three times, and the condition stopped the loop")
	node := p.node("code")
	assert.True(t, node.Repeated)
	assert.Equal(t, 3, node.DoneCount)
	// Each execution appends one typed result to the stdout file.
	p.dagu.ExpectFileContent("code.out", "{\"verdict\":\"blocked\"}\n{\"verdict\":\"blocked\"}\n{\"verdict\":\"done\"}\n")
}

// A command condition can read the typed result that the step wrote to its
// stdout file. This is the way to loop on the verdict of the agent.
func TestVerifyRepeatPolicyUntilStdoutFile(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_repeat_stdout_file.yaml").ExpectExitCode(0)

	require.Equal(t, 3, p.calls(), "the verdict done at the third call stopped the loop")
	assert.Equal(t, 3, p.node("code").DoneCount)
}

// The limit bounds the number of executions, the first one included. The
// condition never succeeds, so the step stops at the limit and still succeeds.
func TestVerifyRepeatPolicyLimit(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_repeat_limit.yaml").ExpectExitCode(0)

	require.Equal(t, 2, p.calls())
	assert.Equal(t, 2, p.node("code").DoneCount)
}

// A repeat on the exit code works for an agent step: it repeats while the code
// is 0, and the limit stops it.
func TestVerifyRepeatPolicyWhileExitCode(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_repeat_exit_code.yaml").ExpectExitCode(0)

	require.Equal(t, 3, p.calls())
	assert.Equal(t, 3, p.node("code").DoneCount)
}

// A condition on the typed result of the step itself, ${steps.code.outputs.verdict},
// does not work (Spec 092, "Verified points"). Spec 007 says that a
// step cannot read its own output (reason self_reference). Dagu leaves the
// reference as text, and the text never equals "done". The loop runs until the
// limit, also after the fake claude gave the verdict done at its third call.
// Use an exit code or a command condition, or read the output in another step.
func TestVerifyRepeatPolicyOwnOutputIsNotReadable(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	result := p.start("verify_repeat_own_output.yaml")
	result.ExpectExitCode(0)

	require.Equal(t, 5, p.calls(), "the limit stopped the loop, not the condition")
	assert.Equal(t, 5, p.node("code").DoneCount)
	assert.Contains(t, verifyOutput(result), "${steps.code.outputs.verdict} was left unchanged")
}

// ---- Verified point 2: the outputs of a sub-DAG ----

// A parent step reads what a sub-DAG publishes with outputs.write or with
// stdout.outputs as ${steps.<id>.outputs.<name>}. A plain output variable of the
// sub-DAG is not an output of the parent step, also when the same sub-DAG
// publishes another output with outputs.write. Dagu leaves the reference as text.
func TestVerifySubDAGOutputs(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_subdag_outputs.yaml").ExpectExitCode(0)

	p.dagu.ExpectFileContent("read.out", strings.Join([]string{
		"write=clean-write",
		"stdout=clean-stdout",
		"plain=${steps.call_plain.outputs.SCRATCH}",
		"write_scratch=${steps.call_write.outputs.SCRATCH}",
		"",
	}, "\n"))

	assert.JSONEq(t, `{"verdict":"clean-write"}`, p.node("call_write").OutputsValue)
	assert.JSONEq(t, `{"verdict":"clean-stdout"}`, p.node("call_stdout").OutputsValue)
	assert.Empty(t, p.node("call_plain").OutputsValue, "a plain output variable publishes no output")
}

// ---- Verified point 3: max_output_size ----

// An output of 10 bytes is under the limit of 64 bytes.
func TestVerifyMaxOutputSizeUnderTheLimit(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_output_small.yaml").ExpectExitCode(0)
	p.dagu.ExpectFileContent("small.out", "aaaaaaaaaa")
}

// An `output:` capture above the limit fails the step (Spec 092, "Verified
// points"). The code fails the step with an error
// that names the limit. The step that depends on it does not run, and the
// value is not cut. The code that adds the line is a fallback that no run uses.
func TestVerifyMaxOutputSizeCaptureAboveTheLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		fixture string
		limit   string
	}{
		{"set in the DAG", "verify_output_truncated.yaml", "64"},
		{"default", "verify_output_default_limit.yaml", "1048576"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newVerifyProject(t)
			result := p.start(tc.fixture)
			result.ExpectNonZeroExitCode()

			out := verifyOutput(result)
			assert.Contains(t, out, "output exceeded maximum size limit of "+tc.limit+" bytes")
			assert.NotContains(t, out, "[OUTPUT TRUNCATED]")
			assert.Contains(t, p.node("produce").Error, "output exceeded maximum size limit of "+tc.limit+" bytes")
			p.dagu.ExpectNoFile("big.out")
		})
	}
}

// A published output above the limit fails the step, for each way to publish.
// The agent step is the case of the spec: a typed result with a long field.
func TestVerifyMaxOutputSizePublishedAboveTheLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		fixture string
		step    string
		wantErr string
		calls   int
	}{
		{"DAGU_OUTPUT_FILE", "verify_published_file.yaml", "publish",
			"failed to parse DAGU_OUTPUT_FILE: step outputs exceeded maximum size limit of 64 bytes", 0},
		{"outputs.write", "verify_published_write.yaml", "publish",
			"outputs exceeded maximum size limit of 64 bytes", 0},
		{"stdout.outputs", "verify_published_stdout.yaml", "publish",
			"output exceeded maximum size limit of 64 bytes", 0},
		{"agent.run typed result", "verify_published_agent.yaml", "code",
			"output exceeded maximum size limit of 64 bytes", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newVerifyProject(t)
			result := p.start(tc.fixture)
			result.ExpectNonZeroExitCode()

			assert.Contains(t, verifyOutput(result), tc.wantErr)
			assert.Contains(t, p.node(tc.step).Error, tc.wantErr)
			assert.Equal(t, tc.calls, p.calls())
		})
	}
}

// The control of the test above: a published output under the limit works.
func TestVerifyMaxOutputSizePublishedUnderTheLimit(t *testing.T) {
	t.Parallel()

	p := newVerifyProject(t)
	p.start("verify_published_file_small.yaml").ExpectExitCode(0)
	p.dagu.ExpectFileContent("read.out", "aaaaaaaaaa")
}
