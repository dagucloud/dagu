// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"image"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// meetWait bounds how long a test waits for something a correct step does
// at once, so a step that never does it fails the test rather than hang it.
const meetWait = 10 * time.Second

// inBackground is a desktop where app's form is open behind a window of
// notes, which has the keyboard focus and shows none of the form's
// elements, so a step that read the window in front would not find them.
// It returns the form's elements.
func inBackground(t *testing.T, app string) (*testRun, *fakeElements) {
	t.Helper()
	run := elementRun(t)
	form := newFakeElements()
	form.asApp(app)
	notes := desktop.WindowID{Handle: 1, Title: "メモ", PID: 10, App: "notes", Bounds: image.Rect(0, 0, 800, 600)}
	own := desktop.WindowID{Handle: 2, Title: form.window.Name, PID: 20, App: app, Bounds: form.window.Bounds}
	run.backend.windows = []desktop.WindowID{notes, own}
	run.backend.focusOn(notes)
	run.elements.show(desktop.Element{Role: desktop.RoleWindow, Name: notes.Title, App: notes.App, Window: notes.Title, Bounds: notes.Bounds}, nil)
	run.openBehind(own.Handle, form)
	return run, form
}

// savesIn is a step that works in app's form by its elements alone: it
// saves the form through the 保存 button's own pattern, checks the window
// and the notice, reads the amount, and waits.
func savesIn(app string) string {
	return fmt.Sprintf(`{"ai": "never", "input": "direct", "idle": "100ms", "do": [
		{"act": {"instruction": "Save the invoice", "recorded": {"windows": [{"click": {"app": %[1]q, "role": "button", "id": "saveButton"}}]}}},
		{"expect": {"window": "経費精算*"}},
		{"expect": {"text": "保存しました"}},
		{"extract": {"instruction": "The amount", "schema": {"type": "object", "properties": {"amount": {"type": "string"}}},
			"fields": {"windows": {"amount": {"app": %[1]q, "role": "text_field", "id": "amountBox"}}}}},
		{"wait": "10ms"}]}`, app)
}

// pointerSave is a step that saves the expense form with a click of the
// pointer on its 保存 button.
const pointerSave = `{"ai": "never", "idle": "100ms", "do": [{"act": {"instruction": "Save the invoice",
	"recorded": {"windows": [{"click": {"app": "expense", "role": "button", "id": "saveButton"}}]}}}]}`

// holdDesktop holds the desktop as another step would, until the returned
// lease is released.
func holdDesktop(t *testing.T, lockDir string) *desktopLease {
	t.Helper()
	quiet := &agentstep.Timeline{Log: io.Discard, Masker: agentstep.NewMasker(nil, nil), Update: func(func(*ir.AgentSession)) {}}
	lease, err := acquireDesktop(t.Context(), lockDir, quiet)
	require.NoError(t, err)
	return lease
}

// waitsFor returns a progress hook that closes the channel once the step
// waits for reason, and that channel.
func waitsFor(reason string) (func(*ir.AgentSession), <-chan struct{}) {
	waiting := make(chan struct{})
	var once sync.Once
	return func(s *ir.AgentSession) {
		if slices.Contains(waitReasons(s), reason) {
			once.Do(func() { close(waiting) })
		}
	}, waiting
}

// await fails the test unless ch closes within meetWait.
func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(meetWait):
		t.Fatalf("%s did not happen", what)
	}
}

// A step whose operations send no pointer or key input, a pattern, exact
// checks, an extract from elements, and a wait, runs while a person keeps
// using the desktop, in its app's window behind theirs: it waits for
// nobody, brings no window forward, and never reads the window in front.
func TestElementStepRunsBesideAPerson(t *testing.T) {
	t.Parallel()

	run, form := inBackground(t, "expense")
	run.backend.personKeepsUsing(1000)
	execution := run.execute(savesIn("expense"), nil)
	require.NoError(t, execution.err)
	session := execution.exec.GetAgentSession()
	assert.Equal(t, []string{"act:cache-hit", "expect:completed", "expect:completed", "extract:cache-hit", "wait:completed"}, eventNames(session))
	assert.Empty(t, waitReasons(session), "the step waits for no person and no other step")
	assert.Equal(t, []string{"invoke saveButton"}, run.backend.inputs())
	assert.Equal(t, map[string]any{"amount": "12,000"}, execution.exec.GetOutputs())
	assert.Empty(t, run.backend.raised, "no window is brought forward")
	assert.Zero(t, run.elements.reads, "the window in front is never read")
	assert.Positive(t, form.reads)
}

// Two steps that work in two apps by their elements alone run at once:
// each step's save waits until the other step has saved too, which only
// steps running at the same time can both do.
func TestElementStepsOfTwoAppsRunAtOnce(t *testing.T) {
	t.Parallel()

	expense, expenseForm := inBackground(t, "expense")
	ledger, ledgerForm := inBackground(t, "ledger")
	ledger.desktopLock = expense.desktopLock
	var mu sync.Mutex
	met := 0
	meet := func(mine chan struct{}, theirs <-chan struct{}) func(desktop.Element, desktop.Pattern, string) {
		var once sync.Once
		return func(desktop.Element, desktop.Pattern, string) {
			once.Do(func() {
				close(mine)
				select {
				case <-theirs:
					mu.Lock()
					met++
					mu.Unlock()
				case <-time.After(meetWait):
				}
			})
		}
	}
	expenseSaved, ledgerSaved := make(chan struct{}), make(chan struct{})
	expenseForm.onApply = meet(expenseSaved, ledgerSaved)
	ledgerForm.onApply = meet(ledgerSaved, expenseSaved)

	results := make(chan error, 2)
	go func() { results <- expense.execute(savesIn("expense"), nil).err }()
	go func() { results <- ledger.execute(savesIn("ledger"), nil).err }()
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	assert.Equal(t, 2, met, "each step saved while the other one ran")
}

// A step that clicks with the pointer waits while another step holds the
// desktop, sending nothing meanwhile, then for a person to stop using it,
// and only then clicks.
func TestPointerStepWaitsForTheDesktopAndThePerson(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.backend.windows = []desktop.WindowID{{Handle: 2, Title: expenseWindow().Name, PID: 20, App: "expense"}}
	holder := holdDesktop(t, run.desktopLock)
	progress, waiting := waitsFor(waitReasonDesktop)
	run.onProgress = progress
	run.backend.personKeepsUsing(2)
	done := make(chan *stepExecution, 1)
	go func() { done <- run.execute(pointerSave, nil) }()

	await(t, waiting, "the wait for the desktop")
	assert.Empty(t, run.backend.inputs(), "nothing is sent while another step holds the desktop")
	holder.release()
	execution := <-done
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"move 235,194", "left down #1"}, run.backend.inputs())
	assert.Equal(t, []string{waitReasonDesktop, waitReasonPerson}, waitReasons(execution.exec.GetAgentSession()))
}

// While a step holds the desktop, a step that clicks waits and sends
// nothing, and a step working in another app by its elements runs to its
// end; the clicking step's input follows all of the holder's.
func TestNoStepSendsInputWhileAnotherHoldsTheDesktop(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var pressed []string
	press := func(name string) func() {
		return func() {
			mu.Lock()
			pressed = append(pressed, name)
			mu.Unlock()
		}
	}

	model := newTestRun(t)
	model.backend.onPress = press("model")
	clicker := elementRun(t)
	clicker.desktopLock = model.desktopLock
	clicker.backend.windows = []desktop.WindowID{{Handle: 2, Title: expenseWindow().Name, PID: 20, App: "expense"}}
	clicker.backend.onPress = press("clicker")
	elements, _ := inBackground(t, "ledger")
	elements.desktopLock = model.desktopLock

	var heldInput []string
	waiting := make(chan struct{})
	var once sync.Once
	clicker.onProgress = func(s *ir.AgentSession) {
		if slices.Contains(waitReasons(s), waitReasonDesktop) {
			once.Do(func() {
				heldInput = clicker.backend.inputs()
				close(waiting)
			})
		}
	}
	holding, elementsDone := make(chan struct{}), make(chan struct{})
	var first sync.Once
	model.sessions = []*scriptedSession{{
		turns: []*computeruse.Turn{actions(clickAt(1, 1)), done("Clicked")},
		// The model's first turn holds the desktop until the other two steps
		// have waited for it and run beside it.
		onNext: func() {
			first.Do(func() {
				close(holding)
				for _, ch := range []<-chan struct{}{waiting, elementsDone} {
					select {
					case <-ch:
					case <-time.After(meetWait):
					}
				}
			})
		},
	}}

	modelDone, clickerDone := make(chan error, 1), make(chan error, 1)
	go func() { modelDone <- model.execute(`{"idle": "0", "do": [{"act": "Click"}]}`, nil).err }()
	await(t, holding, "the model's first turn")
	go func() { clickerDone <- clicker.execute(pointerSave, nil).err }()
	elementsResult := elements.execute(savesIn("ledger"), nil)
	close(elementsDone)

	require.NoError(t, elementsResult.err, "a step working by elements runs while another holds the desktop")
	assert.Equal(t, []string{"invoke saveButton"}, elements.backend.inputs())
	require.NoError(t, <-modelDone)
	require.NoError(t, <-clickerDone)
	assert.Empty(t, heldInput, "the clicking step sent nothing while the model's step held the desktop")
	assert.Equal(t, []string{"model", "clicker"}, pressed)
}

// Anything that moves the foreground takes the desktop: focusing a field,
// which on Windows brings its window forward, restoring a minimized window,
// and a launch, whose window comes forward. Each waits while another step
// holds the desktop and does nothing meanwhile.
func TestMovingTheForegroundTakesTheDesktop(t *testing.T) {
	t.Parallel()

	form := desktop.WindowID{Handle: 2, Title: expenseWindow().Name, PID: 20, App: "expense"}
	for _, tc := range []struct {
		name   string
		steps  string
		window desktop.WindowID
		// effect is what the step did, read once it ran.
		effect func(run *testRun) any
		want   any
	}{
		{
			name:   "focus a field",
			steps:  `{"ai": "never", "input": "direct", "idle": "0", "do": [{"act": {"instruction": "Focus the amount", "recorded": {"windows": [{"click": {"app": "expense", "role": "text_field", "id": "amountBox"}}]}}}]}`,
			window: form,
			effect: func(run *testRun) any { return run.backend.inputs() },
			want:   []string{"focus amountBox"},
		},
		{
			name:   "restore a minimized window",
			steps:  savesIn("expense"),
			window: desktop.WindowID{Handle: form.Handle, Title: form.Title, PID: form.PID, App: form.App, Minimized: true},
			effect: func(run *testRun) any { return len(run.backend.raisedWindows()) },
			want:   1,
		},
		{
			name:   "launch",
			steps:  `{"ai": "never", "idle": "0", "do": [{"launch": "C:\\Apps\\expense.exe"}]}`,
			window: form,
			effect: func(run *testRun) any { return len(run.launches) },
			want:   1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			run.backend.windows = []desktop.WindowID{tc.window}
			holder := holdDesktop(t, run.desktopLock)
			progress, waiting := waitsFor(waitReasonDesktop)
			run.onProgress = progress
			done := make(chan *stepExecution, 1)
			go func() { done <- run.execute(tc.steps, nil) }()

			await(t, waiting, "the wait for the desktop")
			assert.Empty(t, run.backend.inputs())
			assert.Empty(t, run.backend.raisedWindows())
			holder.release()
			execution := <-done
			require.NoError(t, execution.err)
			assert.Equal(t, tc.want, tc.effect(run))
		})
	}
}

// Two steps working in one app take turns, since they share its windows:
// the second waits for the app until the first step ends, though neither
// holds the desktop.
func TestStepsOfOneAppTakeTurns(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var applied []string
	first, firstForm := inBackground(t, "expense")
	second, secondForm := inBackground(t, "expense")
	second.desktopLock = first.desktopLock
	progress, secondWaits := waitsFor(waitReasonApp)
	second.onProgress = progress
	// The first step's first save holds it until the second step waits
	// for the app.
	firstSaving := make(chan struct{})
	var once sync.Once
	firstForm.onApply = func(e desktop.Element, _ desktop.Pattern, _ string) {
		once.Do(func() {
			close(firstSaving)
			select {
			case <-secondWaits:
			case <-time.After(meetWait):
			}
		})
		mu.Lock()
		applied = append(applied, "first "+e.ID)
		mu.Unlock()
	}
	secondForm.onApply = func(e desktop.Element, _ desktop.Pattern, _ string) {
		mu.Lock()
		applied = append(applied, "second "+e.ID)
		mu.Unlock()
	}
	const saveBoth = `{"ai": "never", "input": "direct", "do": [{"act": {"instruction": "Save the invoice and the payment", "recorded": {"windows": [
		{"click": {"app": "expense", "role": "button", "id": "saveButton"}},
		{"click": {"app": "expense", "role": "button", "id": "paymentSaveButton"}}]}}}]}`

	firstDone := make(chan error, 1)
	go func() { firstDone <- first.execute(saveBoth, nil).err }()
	await(t, firstSaving, "the first step's save")
	execution := second.execute(saveBoth, nil)
	require.NoError(t, <-firstDone)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"first saveButton", "first paymentSaveButton", "second saveButton", "second paymentSaveButton"}, applied)
	assert.Equal(t, []string{waitReasonApp}, waitReasons(execution.exec.GetAgentSession()))
}
