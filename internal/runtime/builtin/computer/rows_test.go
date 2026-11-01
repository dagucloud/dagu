// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rowInstruction names both of a row's values; the payment method box of
// choiceRun shows 銀行振込 until something is chosen in it.
const rowInstruction = "金額に %amount% を入れ、支払方法を %method% にする"

// rowStep is the step for one row of a sheet: the instruction stays the
// same and the variables carry the row's values, under the choice ai.
func rowStep(ai, amount, method string) string {
	return fmt.Sprintf(`{"ai": %q, "idle": "0", "variables": {"amount": %q, "method": %q}, "visible": ["method"], "do": [{"act": %q}]}`,
		ai, amount, method, rowInstruction)
}

// typesTheAmount is a model that types the amount and leaves the method as
// the box shows it.
func typesTheAmount() *scriptedSession {
	return &scriptedSession{turns: []*computeruse.Turn{actions(typing("%amount%")), done("Entered the amount; the method already shows")}}
}

// typedField is a run whose focused field takes the keys typed into it,
// focused on the amount box.
func typedField(run *testRun) *testRun {
	run.focusOn("amountBox")
	run.backend.onType = func(text string) {
		run.elements.mu.Lock()
		defer run.elements.mu.Unlock()
		run.elements.focused.Value = text
	}
	return run
}

// methodShown is what the payment method box shows.
func methodShown(t *testing.T, run *testRun) string {
	t.Helper()
	box, err := run.elements.Find(desktop.Selector{Role: desktop.RoleComboBox, ID: "methodBox"})
	require.NoError(t, err)
	require.Len(t, box, 1)
	return box[0].Value
}

// A recording made on a row whose box already showed that row's method
// never chose it. It replays for a row with the same method, but a row
// with another method is worked by the model, whose recording then chooses
// the method and replays for every row.
func TestARecordingThatLeftAValueAsShownFitsOnlyThatValue(t *testing.T) {
	t.Parallel()

	run := typedField(choiceRun(t, true))
	run.sessions = []*scriptedSession{typesTheAmount()}
	require.NoError(t, run.execute(rowStep(aiOnMiss, "12,000", "銀行振込"), nil).err)

	run.backend.events = nil
	same := run.execute(rowStep(aiOnMiss, "9,000", "銀行振込"), nil)
	require.NoError(t, same.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCacheHit}, eventNames(same.exec.GetAgentSession()), "a row with the same method replays")
	assert.Contains(t, run.backend.inputs(), "type 9,000")

	run.backend.events = nil
	never := run.execute(rowStep(aiNever, "8,000", "クレジット"), nil)
	require.ErrorContains(t, never.err, "the act cannot run without AI: its recording does nothing with %method%, so it fits only the value %method% had when it was recorded")
	assert.Empty(t, run.backend.inputs(), "nothing is replayed for a row it does not fit")

	chooses := &scriptedSession{turns: []*computeruse.Turn{
		actions(typing("%amount%"), computeruse.Action{CallID: "c", Kind: computeruse.KindChoose, ElementID: "e3", Text: "%method%"}),
		done("Entered the amount and chose the method"),
	}}
	run.sessions = []*scriptedSession{chooses}
	other := run.execute(rowStep(aiOnMiss, "8,000", "クレジット"), nil)
	require.NoError(t, other.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCompleted}, eventNames(other.exec.GetAgentSession()), "the model works a row with another method")
	assert.NotEmpty(t, chooses.observations)
	assert.Contains(t, other.stderr.String(), "not replayed: its recording does nothing with %method%")
	assert.Equal(t, "クレジット", methodShown(t, run))

	run.sessions = nil
	next := run.execute(rowStep(aiOnMiss, "5,000", "現金"), nil)
	require.NoError(t, next.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCacheHit}, eventNames(next.exec.GetAgentSession()), "a recording that chose the method replays for any row")
	assert.Equal(t, "現金", methodShown(t, run))
}

// A recording that does not say what its unused variables held, as one made
// before recordings kept it, fits no row, so the model works the act and its
// recording takes the place of the old one.
func TestARecordingThatDoesNotSayWhatItAssumedIsNotReplayed(t *testing.T) {
	t.Parallel()

	run := typedField(choiceRun(t, true))
	run.sessions = []*scriptedSession{typesTheAmount()}
	require.NoError(t, run.execute(rowStep(aiOnMiss, "12,000", "銀行振込"), nil).err)
	cache := openReplayCache(filepath.Join(run.dataDir, computerhost.DataDirName), "invoices", "post")
	key, entry, ok := cache.Find(func(string, recording) bool { return true })
	require.True(t, ok)
	entry.Assumed = nil
	cache.Stage(key, entry)
	require.NoError(t, cache.Commit(t.Context()))

	again := typesTheAmount()
	run.sessions = []*scriptedSession{again}
	healed := run.execute(rowStep(aiOnMiss, "12,000", "銀行振込"), nil)
	require.NoError(t, healed.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCompleted}, eventNames(healed.exec.GetAgentSession()))
	assert.NotEmpty(t, again.observations, "the model was asked")

	run.sessions = nil
	replayed := run.execute(rowStep(aiOnMiss, "12,000", "銀行振込"), nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCacheHit}, eventNames(replayed.exec.GetAgentSession()), "the new recording says what it assumed")
}

// A secret the act never typed, as a password the application remembered,
// is kept only as a digest, and still tells a run with another secret from
// one with the same.
func TestAnAssumedSecretIsNeverStored(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-secret"
	step := func(password string) string {
		return fmt.Sprintf(`{"idle": "0", "variables": {"user": "taro", "password": %q}, "do": [{"act": "Sign in as %%user%% with %%password%%"}]}`, password)
	}
	run := typedField(elementRun(t))
	run.secrets = map[string]string{"PORTAL_PASSWORD": secret, "OTHER_PASSWORD": "other-secret"}
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(typing("%user%")), done("Signed in; the password was filled in")}}}
	require.NoError(t, run.execute(step(secret), nil).err)

	data, err := os.ReadFile(replaycache.New(filepath.Join(run.dataDir, computerhost.DataDirName)).Path("invoices", "post"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"assumed"`)
	assert.NotContains(t, string(data), secret)

	same := run.execute(step(secret), nil)
	require.NoError(t, same.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCacheHit}, eventNames(same.exec.GetAgentSession()))

	asked := &scriptedSession{turns: []*computeruse.Turn{actions(typing("%user%")), done("Signed in")}}
	run.sessions = []*scriptedSession{asked}
	other := run.execute(step("other-secret"), nil)
	require.NoError(t, other.err)
	assert.Equal(t, []string{"act:" + agentstep.StatusCompleted}, eventNames(other.exec.GetAgentSession()))
	assert.NotEmpty(t, asked.observations, "the model was asked for another password")
}
