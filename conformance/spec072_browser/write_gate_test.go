// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec072_browser_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledgerPage saves an entry through a request. Its first button only
// opens the entry, which reads.
const ledgerPage = `<!doctype html><html><head><title>Ledger</title></head><body>
<p id="status">ready</p>
<button onclick="fetch('/api/entries?draft=1').then(() => { document.getElementById('status').textContent = 'opened'; })">Open entry</button>
<button onclick="fetch('/api/entries', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({amount: 1000})}).then(() => { document.getElementById('status').textContent = 'saved'; }, () => { document.getElementById('status').textContent = 'not saved'; })">Save entry</button>
<button onclick="fetch('/Default.aspx', {method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded'}, body: '__EVENTTARGET=Save'})">Postback</button>
</body></html>`

// startLedger serves the ledger and counts the writes it receives.
func startLedger(t *testing.T) (string, func() int) {
	t.Helper()
	var mu sync.Mutex
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method != http.MethodGet {
			mu.Lock()
			writes++
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, ledgerPage)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() int {
		mu.Lock()
		defer mu.Unlock()
		return writes
	}
}

// gateReport is the part of write-gate.json the tests read.
type gateReport struct {
	Mode      string `json:"mode"`
	StoppedAt *int   `json:"stopped_at"`
	NotRun    []int  `json:"not_run"`
	WouldSend *struct {
		Method    string `json:"method"`
		URL       string `json:"url"`
		Signature string `json:"signature"`
	} `json:"would_send"`
}

// A rehearsing step blocks its save, stops there, and partially succeeds,
// leaving what it would have sent in its artifacts; the step after it runs.
func TestBrowserRehearsalStopsAtTheSave(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		dag string
		env []string
	}{
		"the step asks":            {dag: "write_gate.yaml"},
		"the host floor beats off": {dag: "write_gate_off.yaml", env: []string{"DAGU_BROWSER_WRITE_GATE=rehearse"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b := newBrowserEnv(t)
			ledger, writes := startLedger(t)
			home := filepath.Join(t.TempDir(), "dagu")
			runEnv := append(append([]string{}, b.env...), "LEDGER_URL="+ledger, "DAGU_HOME="+home)
			runEnv = append(runEnv, tc.env...)
			const runID = "rehearsal"
			b.dagu.RunWithEnv(runEnv, "start", "--run-id="+runID, tc.dag).ExpectExitCode(0)

			assert.Zero(t, writes(), "nothing reaches the ledger")
			status := b.dagu.RunWithEnv(runEnv, "status", "--run-id="+runID, tc.dag)
			status.ExpectExitCode(0)
			assert.Contains(t, status.Stdout(), "Partially Succeeded")

			data, err := os.ReadFile(b.dagu.ProjectPath("gate.out"))
			require.NoError(t, err)
			var report gateReport
			require.NoError(t, json.Unmarshal(data, &report), string(data))
			assert.Equal(t, "rehearse", report.Mode)
			require.NotNil(t, report.StoppedAt)
			assert.Equal(t, 1, *report.StoppedAt)
			assert.Equal(t, []int{2, 3}, report.NotRun)
			require.NotNil(t, report.WouldSend)
			assert.Equal(t, "POST "+ledger+"/api/entries", report.WouldSend.Method+" "+report.WouldSend.URL)
			assert.Equal(t, "POST "+ledger+"/api/entries? {amount}", report.WouldSend.Signature)
			assert.Equal(t, 2, b.model.count(kindAct), "the postback after the save does not run")
		})
	}
}
