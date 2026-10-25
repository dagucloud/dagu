// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const elementsWindowTitle = "Dagu elements test"

// e2eElements opens the elements reader on an interactive desktop with the
// required permissions, when asked to with DAGU_DESKTOP_E2E=1.
func e2eElements(t *testing.T) (*darwinBackend, Elements) {
	t.Helper()
	if os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 to run on an interactive desktop")
	}
	require.NoError(t, Check().Err())
	backend, err := newDarwinBackend()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })
	els, err := OpenElements()
	require.NoError(t, err)
	t.Cleanup(func() { _ = els.Close() })
	return backend, els
}

// elementsWindow starts the conformance window with named controls and
// waits for it to be in front.
func elementsWindow(t *testing.T, els Elements) Element {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	script := filepath.Join(filepath.Dir(file), "..", "..", "conformance", "spec074_computer", "testdata", "elements.js")
	fixture := exec.Command("/usr/bin/osascript", "-l", "JavaScript", script)
	require.NoError(t, fixture.Start())
	t.Cleanup(func() {
		_ = fixture.Process.Kill()
		_ = fixture.Wait()
	})
	var window Element
	require.Eventually(t, func() bool {
		w, err := els.FrontWindow()
		window = w
		return err == nil && w.Name == elementsWindowTitle
	}, 20*time.Second, 200*time.Millisecond, "the window did not come to the front; %+v was there", window)
	return window
}

// outlineByID reads a window's elements and indexes those with an id.
func outlineByID(t *testing.T, els Elements, window Element) ([]Element, map[string]Element) {
	t.Helper()
	outline, err := els.Outline(window, 0)
	require.NoError(t, err)
	byID := map[string]Element{}
	for _, e := range outline {
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	return outline, byID
}

// The reader sees an AppKit window's controls with their roles, ids, and
// paths; finds them by selector as matching its outline would; places the
// element under a point; and focuses and reads a field.
func TestDarwinElements(t *testing.T) {
	backend, els := e2eElements(t)
	window := elementsWindow(t, els)

	assert.Equal(t, RoleWindow, window.Role)
	assert.Equal(t, "osascript", window.App)
	assert.Equal(t, elementsWindowTitle, window.Window)
	assert.Empty(t, window.Path)

	began := time.Now()
	outline, byID := outlineByID(t, els, window)
	t.Logf("elements: outline of %d elements in %s", len(outline), time.Since(began))
	for _, e := range outline {
		assert.Contains(t, Roles, e.Role)
		assert.NotEmpty(t, e.Path, "%+v", e)
		assert.True(t, e.Bounds.Overlaps(window.Bounds), "%+v lies outside the window %v", e, window.Bounds)
		assert.Equal(t, elementsWindowTitle, e.Window)
	}
	for id, role := range map[string]string{
		"lblAmount": RoleText, "amountBox": RoleTextField, "saveButton": RoleButton, "agreeBox": RoleCheckbox,
		"methodBox": RoleComboBox, "tier1": RoleRadio, "paymentAmountBox": RoleTextField, "paymentSaveButton": RoleButton,
		"linesList": RoleList,
	} {
		require.Contains(t, byID, id, "outline: %+v", outline)
		assert.Equal(t, role, byID[id].Role, id)
	}
	assert.Equal(t, "金額", byID["lblAmount"].Name, "static text is named by what it shows")
	assert.Equal(t, "支払方法", byID["methodBox"].Name, "an accessible label is the name")
	assert.Equal(t, "同意する", byID["agreeBox"].Name)
	assert.Equal(t, "false", byID["agreeBox"].Value, "a checkbox holds its state")

	saves, err := els.Find(Selector{Role: RoleButton, Name: "保存"})
	require.NoError(t, err)
	require.Len(t, saves, 2, "%+v", saves)
	expected, err := Match(window, outline, Selector{Role: RoleButton, Name: "保存"})
	require.NoError(t, err)
	assert.Equal(t, expected, saves)

	inner, err := els.Find(Selector{Role: RoleButton, Name: "保存", In: &Selector{Role: RoleGroup, Name: "支払情報"}})
	require.NoError(t, err)
	require.Len(t, inner, 1, "the group's title names it: %+v", inner)
	assert.Equal(t, "paymentSaveButton", inner[0].ID)

	near, err := els.Find(Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}})
	require.NoError(t, err)
	require.Len(t, near, 2, "%+v", near)
	assert.Equal(t, []string{"amountBox", "paymentAmountBox"}, []string{near[0].ID, near[1].ID})

	// The element under a point, with its place in the window.
	target := inner[0].Bounds.Min.Add(inner[0].Bounds.Size().Div(2))
	began = time.Now()
	at, err := els.At(target.X, target.Y)
	t.Logf("elements: at in %s", time.Since(began))
	require.NoError(t, err)
	assert.Equal(t, "paymentSaveButton", at.ID)
	assert.Equal(t, inner[0].Path, at.Path)
	assert.Equal(t, elementsWindowTitle, at.Window)
	assert.Equal(t, inner[0].Bounds, at.Bounds)

	// Focus, and the element with the focus.
	require.NoError(t, els.Focus(byID["amountBox"]))
	require.Eventually(t, func() bool {
		focused, err := els.Focused()
		return err == nil && focused.ID == "amountBox"
	}, 3*time.Second, 100*time.Millisecond)
	assert.ErrorIs(t, els.Focus(Element{Role: RoleButton, Name: "nope", Path: []PathStep{{Role: RoleButton, Name: "nope"}}}), ErrNotFound)

	// Typed text is the field's value.
	require.NoError(t, backend.Type("1200"))
	field := byID["amountBox"].Bounds.Min.Add(byID["amountBox"].Bounds.Size().Div(2))
	require.Eventually(t, func() bool {
		filled, err := els.At(field.X, field.Y)
		return err == nil && filled.Value == "1200"
	}, 3*time.Second, 100*time.Millisecond)
}

// Elements perform their patterns when asked: a button invokes, a checkbox
// toggles, a field takes a value, a radio button selects, a popup button
// opens; a disabled control and a label refuse; and a handler that holds
// the call inside a dialog does not hold the reader past its timeout.
func TestDarwinDirectInput(t *testing.T) {
	backend, els := e2eElements(t)
	window := elementsWindow(t, els)
	_, byID := outlineByID(t, els, window)
	for _, id := range []string{"statusLabel", "saveButton", "agreeBox", "amountBox", "methodBox", "tier1", "submitButton", "confirmButton", "lblAmount"} {
		require.Contains(t, byID, id)
	}
	statusAt := byID["statusLabel"].Bounds.Min.Add(byID["statusLabel"].Bounds.Size().Div(2))
	status := func() string {
		e, err := els.At(statusAt.X, statusAt.Y)
		if err != nil {
			return err.Error()
		}
		return e.Name
	}
	expectStatus := func(want string) {
		t.Helper()
		assert.Eventually(t, func() bool { return status() == want }, 3*time.Second, 100*time.Millisecond, "status reads %q", status())
	}
	assert.Equal(t, "待機中", status())

	began := time.Now()
	require.NoError(t, els.Apply(byID["saveButton"], PatternInvoke, ""))
	t.Logf("elements: invoke in %s", time.Since(began))
	expectStatus("保存しました")

	require.NoError(t, els.Apply(byID["agreeBox"], PatternToggle, ""))
	expectStatus("同意: True")
	require.NoError(t, els.Apply(byID["agreeBox"], PatternToggle, ""))
	expectStatus("同意: False")

	require.NoError(t, els.Apply(byID["amountBox"], PatternSetValue, "１２００"))
	field := byID["amountBox"].Bounds.Min.Add(byID["amountBox"].Bounds.Size().Div(2))
	filled, err := els.At(field.X, field.Y)
	require.NoError(t, err)
	assert.Equal(t, "１２００", filled.Value, "the value is set as given")

	require.NoError(t, els.Apply(byID["tier1"], PatternSelect, ""))
	expectStatus("区分: 優先")

	require.NoError(t, els.Apply(byID["methodBox"], PatternExpand, ""))
	var credit []Element
	require.Eventually(t, func() bool {
		credit, err = els.Find(Selector{Role: RoleMenuItem, Name: "クレジット"})
		return err == nil && len(credit) == 1
	}, 3*time.Second, 100*time.Millisecond, "the popup's menu did not open")
	require.NoError(t, els.Apply(credit[0], PatternInvoke, ""))
	expectStatus("支払方法: クレジット")
	assert.Eventually(t, func() bool {
		items, err := els.Find(Selector{Role: RoleMenuItem, Name: "クレジット"})
		return err == nil && len(items) == 0
	}, 3*time.Second, 100*time.Millisecond, "the popup's menu closed")

	assert.ErrorIs(t, els.Apply(byID["submitButton"], PatternInvoke, ""), ErrDisabled)
	assert.ErrorIs(t, els.Apply(byID["lblAmount"], PatternInvoke, ""), ErrNoPattern, "a label has nothing to invoke")
	assert.ErrorIs(t, els.Apply(Element{Role: RoleButton, Name: "nope", Path: []PathStep{{Role: RoleButton, Name: "nope"}}}, PatternInvoke, ""), ErrNotFound)

	// A handler that opens a dialog holds the call; the answer still comes
	// within the messaging timeout, and the reader answers afterwards.
	began = time.Now()
	err = els.Apply(byID["confirmButton"], PatternInvoke, "")
	t.Logf("elements: invoke that opens a dialog answered %v in %s", err, time.Since(began))
	assert.Less(t, time.Since(began), (axMessagingTimeout+1)*time.Second)
	require.Eventually(t, func() bool {
		front, err := els.FrontWindow()
		return err == nil && front.Name != elementsWindowTitle
	}, 5*time.Second, 100*time.Millisecond, "the dialog is not in front")
	require.NoError(t, backend.Key(KeyEnter, true))
	require.NoError(t, backend.Key(KeyEnter, false))
	require.Eventually(t, func() bool {
		front, err := els.FrontWindow()
		return err == nil && front.Name == elementsWindowTitle
	}, 5*time.Second, 100*time.Millisecond, "the window is not back in front")
	require.NoError(t, els.Apply(byID["agreeBox"], PatternToggle, ""))
	expectStatus("同意: True")
}

// A table is read row by row, each cell under the column its header names;
// a column with no header is a miss.
func TestDarwinTableRead(t *testing.T) {
	_, els := e2eElements(t)
	elementsWindow(t, els)
	ctx := t.Context()

	container := Selector{Role: RoleList, ID: "linesList"}
	rows, err := els.Rows(ctx, container, []string{"品目", "金額"}, 100)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"りんご", "1,200"}, {"みかん", "3,400"}}, rows)

	swapped, err := els.Rows(ctx, container, []string{"金額", "品目"}, 100)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1,200", "りんご"}, {"3,400", "みかん"}}, swapped)

	one, err := els.Rows(ctx, container, []string{"品目"}, 1)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"りんご"}}, one)

	_, err = els.Rows(ctx, container, []string{"単価"}, 100)
	assert.ErrorIs(t, err, ErrNotFound)
}

// chromePath is where Google Chrome installs itself.
const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

// A web page's list box in Chromium opens on a press, names its options by
// their text, places the option its open menu shows over the box, and
// chooses an option pressed through Accessibility, closing the menu with
// the choice kept.
func TestDarwinChromiumListBox(t *testing.T) {
	backend, els := e2eElements(t)
	if _, err := os.Stat(chromePath); err != nil {
		t.Skip("Google Chrome is not installed")
	}
	page, err := filepath.Abs("testdata/select.html")
	require.NoError(t, err)
	// A profile of its own keeps the test apart from any Chrome a person runs.
	chrome := exec.Command(chromePath, "--user-data-dir="+t.TempDir(), "--no-first-run", "--no-default-browser-check", "--app=file://"+page)
	require.NoError(t, chrome.Start())
	t.Cleanup(func() {
		_ = chrome.Process.Kill()
		_ = chrome.Wait()
	})
	var box []Element
	require.Eventually(t, func() bool {
		box, err = els.Find(Selector{Role: RoleComboBox, ID: "fruit"})
		return err == nil && len(box) == 1
	}, 30*time.Second, 200*time.Millisecond, "the page's list box did not show")
	status := func() string {
		found, err := els.Find(Selector{Role: RoleText, Name: "選択: *"})
		if err != nil || len(found) != 1 {
			return ""
		}
		return found[0].Name
	}

	// A recent Chromium's list box offers no press and opens on a click.
	open := func(box Element) {
		t.Helper()
		if err := els.Apply(box, PatternExpand, ""); errors.Is(err, ErrNoPattern) {
			centre := box.Bounds.Min.Add(box.Bounds.Size().Div(2))
			require.NoError(t, New(backend).Click(t.Context(), &centre, ButtonLeft, 1, nil))
		} else {
			require.NoError(t, err)
		}
	}
	option := func(name, box string) Element {
		t.Helper()
		var found []Element
		require.Eventually(t, func() bool {
			found, err = els.Find(Selector{Role: RoleMenuItem, Name: name, In: &Selector{Role: RoleComboBox, ID: box}})
			return err == nil && len(found) == 1
		}, 3*time.Second, 100*time.Millisecond, "the list box %s did not open", box)
		return found[0]
	}
	centre := box[0].Bounds.Min.Add(box[0].Bounds.Size().Div(2))
	open(box[0])
	orange := []Element{option("みかん", "fruit")}

	// The open menu shows the chosen option over the box; it is read as the
	// list box's option in the window, once Chromium has laid it out.
	read := func() (Element, bool) {
		e, err := els.At(centre.X, centre.Y)
		return e, err == nil
	}
	assert.Eventually(t, func() bool {
		e, ok := read()
		return ok && e.Role == RoleMenuItem && e.Name == "りんご" && e.Window == "Dagu select test"
	}, 3*time.Second, 100*time.Millisecond, "the option over the box was not read")

	require.NoError(t, els.Apply(orange[0], PatternInvoke, ""))
	assert.Eventually(t, func() bool { return status() == "選択: みかん" }, 3*time.Second, 100*time.Millisecond, "the page did not see the choice")
	// With the menu closed, the box is under its centre again.
	assert.Eventually(t, func() bool {
		e, ok := read()
		return ok && e.Role == RoleComboBox
	}, 3*time.Second, 100*time.Millisecond, "the menu stayed open")
	chosen, err := els.Find(Selector{Role: RoleComboBox, ID: "fruit"})
	require.NoError(t, err)
	require.Len(t, chosen, 1)
	assert.Equal(t, "みかん", chosen[0].Value)

	// A box the page builds again on the change is chosen from all the same.
	size, err := els.Find(Selector{Role: RoleComboBox, ID: "size"})
	require.NoError(t, err)
	require.Len(t, size, 1)
	open(size[0])
	require.NoError(t, els.Apply(option("中", "size"), PatternInvoke, ""))
	assert.Eventually(t, func() bool {
		rebuilt, err := els.Find(Selector{Role: RoleComboBox, ID: "size"})
		return err == nil && len(rebuilt) == 1 && rebuilt[0].Value == "中"
	}, 3*time.Second, 100*time.Millisecond, "the rebuilt box does not show the choice")
}
