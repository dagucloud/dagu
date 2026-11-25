// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec013_step_run_test

import (
	"fmt"
	"maps"
	"net/http"
	"runtime"
	"strings"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changedDAG is fromRunsDAG with a step after consume, which a run of
// consume skips and carries nothing for.
const changedDAG = fromRunsDAG + `  - name: notify
    depends: [consume]
    run: echo notify
`

// refusal is the 400 answer of a start that refuses carried steps.
type refusal struct {
	Message string `json:"message"`
	Details struct {
		RefusedSteps []api.CarriedStepCheck `json:"refusedSteps"`
	} `json:"details"`
}

func editSpec(t *testing.T, server test.Server, name, spec string) {
	t.Helper()
	server.Client().Put("/api/v1/dags/"+name+"/spec", map[string]any{"spec": spec}).
		ExpectStatus(http.StatusOK).Send(t)
}

// A step carried from outputsFromRunId that changed since that run is refused
// with what changed, unless allowChangedSteps names it. A run that reuses it
// anyway records what changed, and a later run that takes the step from that
// run needs consent of its own. A changed step after the selection carries
// nothing and does not count.
func TestChangedCarriedStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures assume a POSIX sh is available")
	}
	server := newServer(t)
	const name = "changed_carried"
	createDAG(t, server, name, changedDAG)
	path := "/api/v1/dags/" + name + "/start"
	from := func(runID string, extra map[string]any) map[string]any {
		body := map[string]any{"steps": []string{"consume"}, "outputsFromRunId": runID}
		maps.Copy(body, extra)
		return body
	}

	source := startRun(t, server, path, map[string]any{})
	waitForStatus(t, server, name, source, api.StatusSuccess)
	sourceTag := builtTag(t, server, name, source)

	editSpec(t, server, name, strings.Replace(changedDAG, "echo notify", "echo notified", 1))
	after := startRun(t, server, path, from(source, nil))
	waitForStatus(t, server, name, after, api.StatusSuccess)

	edited := strings.Replace(changedDAG, `echo "built $T"`, `echo "built again $T"`, 1)
	editSpec(t, server, name, edited)
	var refused refusal
	server.Client().Post(path, from(source, nil)).ExpectStatus(http.StatusBadRequest).Send(t).Unmarshal(t, &refused)
	want := fmt.Sprintf("step build cannot be reused from run %s: the step changed since that run", source)
	assert.Equal(t, want, refused.Message)
	require.Len(t, refused.Details.RefusedSteps, 1)
	step := refused.Details.RefusedSteps[0]
	assert.Equal(t, "build", step.Step)
	assert.Equal(t, source, step.RunId)
	assert.Equal(t, api.CarriedStepCheckStatusChanged, step.Status)
	assert.Equal(t, []api.StepChangeKind{api.StepChangeKindStep}, step.Changes)
	require.NotNil(t, step.Message)
	assert.Equal(t, want, *step.Message)

	consented := startRun(t, server, path, from(source, map[string]any{"allowChangedSteps": []string{"img"}}))
	waitForStatus(t, server, name, consented, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, consented, "consume"), "tag="+sourceTag)
	inputs := runDetails(t, server, name, consented).ReusedInputs
	require.NotNil(t, inputs)
	require.Len(t, *inputs, 1)
	require.NotNil(t, (*inputs)[0].Changes)
	assert.Equal(t, []api.StepChangeKind{api.StepChangeKindStep}, *(*inputs)[0].Changes)

	// The definition of build is the same as in the consented run, but its
	// outputs there came from the build before the edit.
	server.Client().Post(path, from(consented, nil)).ExpectStatus(http.StatusBadRequest).Send(t).Unmarshal(t, &refused)
	assert.Equal(t, fmt.Sprintf("step build cannot be reused from run %s: in that run it or a step it depends on took outputs of a changed step", consented), refused.Message)

	body := server.Client().Post(path, from(source, map[string]any{"allowChangedSteps": []string{"nope"}})).
		ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body, `allowChangedSteps: unknown step \"nope\"`)
	body = server.Client().Post(path, map[string]any{"reuseFrom": source, "allowChangedSteps": []string{"build"}}).
		ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body, "or allowChangedSteps")
	// The request validator or the server answers, each naming the field.
	body = server.Client().Post(path, map[string]any{"allowChangedSteps": []string{"build"}}).
		ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body, "allowChangedSteps")
}

// The previews plan a run of selected steps as the start would and report
// each carried step, for the saved file and for a draft of it.
func TestPreviewCarriedSteps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures assume a POSIX sh is available")
	}
	server := newServer(t)
	const name = "changed_preview"
	createDAG(t, server, name, changedDAG)
	source := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, source, api.StatusSuccess)
	edited := strings.Replace(changedDAG, `echo "built $T"`, `echo "built again $T"`, 1)
	editSpec(t, server, name, edited)

	var saved api.StepReuseCheck
	server.Client().Post("/api/v1/dags/"+name+"/start-preview", map[string]any{
		"steps":            []string{"consume"},
		"outputsFromRunId": source,
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &saved)
	require.Len(t, saved.Carried, 1)
	assert.Equal(t, "build", saved.Carried[0].Step)
	assert.Equal(t, api.CarriedStepCheckStatusChanged, saved.Carried[0].Status)
	assert.Equal(t, []api.StepChangeKind{api.StepChangeKindStep}, saved.Carried[0].Changes)
	assert.Empty(t, saved.Missing)

	// A draft with the text of the source run: nothing changed for build.
	var draft api.StepReuseCheck
	server.Client().Post("/api/v1/dag-runs/preview", map[string]any{
		"spec":             changedDAG,
		"name":             name,
		"draftOf":          name,
		"steps":            []string{"consume"},
		"outputsFromRunId": source,
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &draft)
	require.Len(t, draft.Carried, 1)
	assert.Equal(t, api.CarriedStepCheckStatusUnchanged, draft.Carried[0].Status)
	assert.Empty(t, draft.Carried[0].Changes)
	assert.Nil(t, draft.Carried[0].Message)

	// An entry whose run is gone is reported, not refused.
	var gone api.StepReuseCheck
	server.Client().Post("/api/v1/dags/"+name+"/start-preview", map[string]any{
		"steps":           []string{"consume"},
		"outputsFromRuns": map[string]string{"build": "gone-run"},
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &gone)
	require.Len(t, gone.Carried, 1)
	assert.Equal(t, api.CarriedStepCheckStatusUnavailable, gone.Carried[0].Status)
}
