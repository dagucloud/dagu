// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunsLocalSourceActionWithRealDenoIntegration(t *testing.T) {
	if os.Getenv("DAGU_ACTION_DENO_INTEGRATION") != "1" {
		t.Skip("set DAGU_ACTION_DENO_INTEGRATION=1 to run the real Deno/aqua integration test")
	}

	workDir := t.TempDir()
	toolsDir := filepath.Join(t.TempDir(), "tools")
	artifactDir := filepath.Join(t.TempDir(), "artifacts")
	actionDir := filepath.Join(workDir, "actions", "echo")
	writeRealDenoAction(t, actionDir, realDenoVersion())

	dag, err := spec.LoadYAML(context.Background(), fmt.Appendf(nil, `
name: real-deno-action
working_dir: %s
steps:
  - name: echo-action
    action: source:actions/echo@local
    with:
      text: hello from dagu action
`, strconv.Quote(workDir)))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	step := dag.Steps[0]
	require.Equal(t, core.ExecutorTypeAction, step.ExecutorConfig.Type)
	require.Equal(t, "source:actions/echo@local", step.ExecutorConfig.Config["ref"])

	ctx := runtime.NewContext(
		context.Background(),
		dag,
		"run-1",
		"",
		runtime.WithWorkDir(workDir),
		runtime.WithEnvVars(envToolsDir+"="+toolsDir),
		runtime.WithArtifactDir(artifactDir),
	)
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	exec, err := newAction(ctx, step)
	require.NoError(t, err)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	require.NoError(t, exec.Run(ctx))

	var output map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output))
	assert.Equal(t, true, output["ok"])
	assert.Equal(t, "hello from dagu action", output["text"])
	assert.Equal(t, "source:actions/echo@local", output["ref"])
	assert.Equal(t, "echo-action", output["step"])
	assert.Equal(t, true, output["actionDirReadable"])
	assert.Contains(t, stderr.String(), "action stdout")
	assert.NotContains(t, stdout.String(), "action stdout")
}

func realDenoVersion() string {
	if version := strings.TrimSpace(os.Getenv("DAGU_ACTION_DENO_VERSION")); version != "" {
		return version
	}
	return "v2.5.2"
}

func writeRealDenoAction(t *testing.T, dir, denoVersion string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	manifest := `apiVersion: dagu.dev/v1alpha1
name: real-deno-echo
runtime:
  type: deno
  deno: ` + denoVersion + `
  entrypoint: mod.ts
inputs:
  type: object
  required: [text]
  additionalProperties: false
  properties:
    text:
      type: string
permissions:
  env: [DAG_RUN_STEP_NAME]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifest), 0o600))
	source := `const inputPath = Deno.env.get("DAGU_ACTION_INPUT");
const outputPath = Deno.env.get("DAGU_ACTION_OUTPUT");
const actionDir = Deno.env.get("DAGU_ACTION_DIR");
const actionRef = Deno.env.get("DAGU_ACTION_REF");
const step = Deno.env.get("DAG_RUN_STEP_NAME");

if (!inputPath || !outputPath || !actionDir || !actionRef || !step) {
  throw new Error("missing Dagu action environment");
}

const input = JSON.parse(await Deno.readTextFile(inputPath));
const actionInfo = await Deno.stat(actionDir);

console.log("action stdout");
await Deno.writeTextFile(outputPath, JSON.stringify({
  ok: true,
  text: input.text,
  ref: actionRef,
  step,
  actionDirReadable: actionInfo.isDirectory,
}));
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.ts"), []byte(source), 0o600))
}
