// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// ledgerPage saves an entry through a request when Save entry is pressed.
const ledgerPage = `<!doctype html><html><head><title>Ledger</title></head><body>
<p id="result">Pending</p>
<button onclick="fetch('/entries', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({amount: 1000})}).then(() => { document.getElementById('result').textContent = 'Saved'; }, () => { document.getElementById('result').textContent = 'Rejected'; })">Save entry</button>
</body></html>`

// A step in ask mode waits before the act a rehearsal found to write.
// Answered once, the act sends its write; answered skip, it does not run,
// and the step and those after it are skipped while the run succeeds.
func TestBrowserWriteGateAsksBeforeTheCommitAct(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("write_gate ask is not supported on Windows")
	}
	requireBrowser(t)
	t.Setenv("DAGU_BROWSER_SANDBOX", "false")

	var saves atomic.Int64
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodPost {
			saves.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, ledgerPage)
	}))
	t.Cleanup(page.Close)
	model := httptest.NewServer(http.HandlerFunc(verifyModel))
	t.Cleanup(model.Close)
	server := test.SetupServer(t)

	spec := func(mode string) string {
		return fmt.Sprintf(`llm:
  provider: local
  model: test-model
  base_url: %s
steps:
  - id: login
    action: browser.run
    with:
      url: %s
      browser:
        write_gate: %s
      do:
        - act: Click the Save entry button
  - id: after
    depends: [login]
    run: echo done
`, model.URL, page.URL, mode)
	}
	start := func(dagName, runID string) {
		runIDValue := runID
		server.Client().Post(fmt.Sprintf("/api/v1/dags/%s/start", dagName), api.ExecuteDAGJSONRequestBody{DagRunId: &runIDValue}).
			ExpectStatus(http.StatusOK).Send(t)
	}
	for name, mode := range map[string]string{"intg_wg_trial": "rehearse", "intg_wg_ask": "ask"} {
		body := spec(mode)
		server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{Name: name, Spec: &body}).ExpectStatus(http.StatusCreated).Send(t)
	}

	// The rehearsal finds the act that writes, and its point is copied to
	// the DAG that asks, as approving from a rehearsal does.
	start("intg_wg_trial", "trial")
	waitForDAGRunStatus(t, server, "intg_wg_trial", "trial", ir.PartiallySucceeded)
	require.Zero(t, saves.Load())
	store := writegate.NewCommitStore(filepath.Join(server.Config.Paths.DataDir, browserhost.DataDirName))
	points, err := store.Load("intg_wg_trial", "login")
	require.NoError(t, err)
	require.Len(t, points, 1)
	require.NoError(t, store.Note(server.Context, "intg_wg_ask", "login", points...))

	// respond answers the run's pending write permission; sent is how many
	// writes the ledger had before the run.
	respond := func(runID, decision string, sent int64) {
		var interaction ir.AgentInteraction
		waitForBrowserStep(t, server, "intg_wg_ask", runID, func(node *ir.Node) bool {
			if node.AgentSession == nil || len(node.AgentSession.Interactions) == 0 {
				return false
			}
			interaction = node.AgentSession.Interactions[len(node.AgentSession.Interactions)-1]
			return interaction.Status == ir.AgentInteractionPending
		})
		require.Equal(t, ir.AgentPermissionBrowserWrite, interaction.Permission)
		require.Equal(t, []string{points[0].Signature}, interaction.Patterns)
		require.Equal(t, sent, saves.Load(), "nothing is sent while the step waits")
		value := api.AgentInteractionResponseRequestDecision(decision)
		server.Client().Post(
			fmt.Sprintf("/api/v1/dag-runs/intg_wg_ask/%s/steps/login/agent-interactions/%s/respond", runID, interaction.ID),
			api.AgentInteractionResponseRequest{Decision: &value},
		).ExpectStatus(http.StatusOK).Send(t)
	}

	start("intg_wg_ask", "once")
	respond("once", "once", 0)
	status := waitForDAGRunStatus(t, server, "intg_wg_ask", "once", ir.Succeeded)
	require.Equal(t, int64(1), saves.Load(), "the answered act sends its write")
	require.Equal(t, ir.NodeSucceeded, nodeByName(t, status, "after").Status)

	start("intg_wg_ask", "skip")
	respond("skip", ir.AgentDecisionSkip, 1)
	status = waitForDAGRunStatus(t, server, "intg_wg_ask", "skip", ir.Succeeded)
	require.Equal(t, int64(1), saves.Load(), "a skipped act sends nothing")
	require.Equal(t, ir.NodeSkipped, nodeByName(t, status, "login").Status)
	require.Equal(t, ir.NodeSkipped, nodeByName(t, status, "after").Status)
}
