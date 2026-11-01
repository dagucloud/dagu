// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// itemStep is the step for one row of a sheet: the instruction stays the
// same and the variables carry the row's item, under the choice ai.
func itemStep(ai, item string) string {
	return fmt.Sprintf(`{"url": "https://shop.example.com/orders/new", "ai": %q, "variables": {"item": %q}, "do": [{"act": "Choose %%item%% as the item and add it"}]}`, ai, item)
}

// A recording made on a row whose page already showed that row's item
// never chose it. It replays for a row with the same item, but a row with
// another item is worked by the model, whose recording then fills the item
// and replays for every row.
func TestARecordingThatLeftAValueAsShownFitsOnlyThatValue(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	require.NoError(t, run.execute(itemStep(aiOnMiss, "A4 paper"), nil).err)

	same := run.execute(itemStep(aiOnMiss, "A4 paper"), nil)
	require.NoError(t, same.err)
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(same.exec.GetAgentSession()), "a row with the same item replays")

	run.noModel = true
	never := run.execute(itemStep(aiNever, "Toner T-20"), nil)
	require.ErrorContains(t, never.err, "the act cannot run without AI: its recording does nothing with %item%, so it fits only the value %item% had when it was recorded")
	assert.Len(t, run.engine.replays, 1, "nothing is replayed for a row it does not fit")

	run.noModel = false
	run.engine.actFills = "%item%"
	other := run.execute(itemStep(aiOnMiss, "Toner T-20"), nil)
	require.NoError(t, other.err)
	assert.Equal(t, []string{"goto:completed", "act:completed"}, eventNames(other.exec.GetAgentSession()), "the model works a row with another item")
	assert.Len(t, run.engine.actInstructions(), 2)
	assert.Contains(t, other.stderr.String(), "not replayed: its recording does nothing with %item%")

	next := run.execute(itemStep(aiOnMiss, "Label roll"), nil)
	require.NoError(t, next.err)
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(next.exec.GetAgentSession()), "a recording that fills the item replays for any row")
	assert.Len(t, run.engine.actInstructions(), 2)
}

// A secret the act never typed, as a password the page remembered, is kept
// only as a digest.
func TestAnAssumedSecretIsNeverStored(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-secret"
	run := newTestRun(t, pageModel(nil))
	run.secrets = map[string]string{"SHOP_PASSWORD": secret}
	steps := `{"url": "https://shop.example.com/login", "variables": {"password": "` + secret + `"}, "do": [{"act": "Sign in with %password%"}]}`
	require.NoError(t, run.execute(steps, nil).err)

	cache := run.cacheFile()
	assert.Contains(t, cache, `"assumed"`)
	assert.NotContains(t, cache, secret)
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(replayed.exec.GetAgentSession()))
}

// An action on an element named by the value of the variable an act names,
// or on one of several Delete buttons in the row that shows the value, finds
// the element by the value, so its recording fits every value.
func TestARecordingThatFindsItsElementByAValueFitsEveryValue(t *testing.T) {
	t.Parallel()

	link := []recordedAction{{Selector: "xpath=/html[1]/body[1]/a[1]", Method: "click"}}
	identifyOn(ordersPage, link, map[string]string{"customer": "ACME-1001"})
	opened := recordAct("Open %customer%", link, map[string]string{"customer": "ACME-1001"})
	assert.Empty(t, unfit("Open %customer%", opened, map[string]string{"customer": "ACME-1002"}))

	const instruction = "Delete %customer%"
	actions := []recordedAction{{Selector: "xpath=/html[1]/body[1]/ul[1]/li[2]/button[1]", Method: "click"}}
	identifyOn(rowsPage("Alpha", "Beta"), actions, map[string]string{"customer": "Beta"})
	rec := recordAct(instruction, actions, map[string]string{"customer": "Beta"})
	assert.Empty(t, unfit(instruction, rec, map[string]string{"customer": "Delta"}))

	other := []recordedAction{{Selector: "xpath=/html[1]/body[1]/ul[1]/li[2]/button[1]", Method: "click"}}
	identifyOn(rowsPage("Alpha", "Beta"), other, map[string]string{"customer": "Gamma"})
	rec = recordAct(instruction, other, map[string]string{"customer": "Gamma"})
	assert.Equal(t, "customer", unfit(instruction, rec, map[string]string{"customer": "Delta"}), "a row that did not show the customer is not found by it")
}

// A cache an older engine wrote keeps each recording as a bare list of
// actions. It still replays where its actions use every variable the act
// names, and one that cannot say what it assumed is not replayed. A later
// run's recording is written beside it, not over it.
func TestARecordingKeptAsAListReplays(t *testing.T) {
	t.Parallel()

	const pageURL = "https://shop.example.com/orders/new"
	run := newTestRun(t, pageModel(nil))
	path := replaycache.New(filepath.Join(run.dataDir, browserhost.DataDirName)).Path("orders", "shop")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, fmt.Appendf(nil, `{%q: [{"selector": "xpath=/html/body/button", "method": "click"}], %q: [{"selector": "xpath=/html/body/input", "method": "fill", "arguments": ["%%item%%"]}]}`,
		replayKey(0, "Choose %item% as the item and add it", pageURL), replayKey(0, "Type %item% into the item field", pageURL)), 0o600))

	const fills = `{"url": "` + pageURL + `", "variables": {"item": "Toner T-20"}, "do": [{"act": "Type %item% into the item field"}]}`
	filled := run.execute(fills, nil)
	require.NoError(t, filled.err)
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(filled.exec.GetAgentSession()))

	chosen := run.execute(itemStep(aiOnMiss, "Toner T-20"), nil)
	require.NoError(t, chosen.err)
	assert.Equal(t, []string{"goto:completed", "act:completed"}, eventNames(chosen.exec.GetAgentSession()), "the act that never used %item% asks the model")

	again := run.execute(fills, nil)
	require.NoError(t, again.err)
	assert.Equal(t, []string{"goto:completed", "act:cache-hit"}, eventNames(again.exec.GetAgentSession()), "the old recording outlives the new one's commit")
}
