// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const elementsWindowTitle = "Dagu elements test"

// e2eElements opens the elements reader on an interactive desktop.
func e2eElements(t *testing.T) Elements {
	t.Helper()
	els, err := OpenElements()
	require.NoError(t, err)
	t.Cleanup(func() { _ = els.Close() })
	return els
}

// startApp starts a program and ends it with the test.
func startApp(t *testing.T, command string, args ...string) {
	t.Helper()
	cmd := exec.Command(command, args...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// frontWindowNamed waits until the window in front is the one wanted and
// returns it. Windows does not hand a new process the foreground while a
// person is using another, so a click on the window's title bar, when its
// place is known, brings it to the front as a person would.
func frontWindowNamed(t *testing.T, driver *Driver, els Elements, titleBar *image.Point, matches func(Element) bool) Element {
	t.Helper()
	window, seen, ok := waitFront(t, driver, els, titleBar, matches, 30*time.Second)
	require.True(t, ok, "the window did not come to the front; the front window was %+v", seen)
	return window
}

// waitFront polls for the window wanted to be in front, clicking its
// title bar between looks when its place is known. It returns the window,
// or what was in front instead.
func waitFront(t *testing.T, driver *Driver, els Elements, titleBar *image.Point, matches func(Element) bool, timeout time.Duration) (Element, Element, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var seen Element
	for time.Now().Before(deadline) {
		w, err := els.FrontWindow()
		if err == nil {
			seen = w
			if matches(w) {
				return w, seen, true
			}
		}
		if titleBar != nil {
			_ = driver.Click(t.Context(), titleBar, ButtonLeft, 1, nil)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return Element{}, seen, false
}

// elementsWindow starts the conformance window with named controls, which
// opens at a fixed place, and brings it to the front.
func elementsWindow(t *testing.T, driver *Driver, els Elements) Element {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	script := filepath.Join(filepath.Dir(file), "..", "..", "conformance", "spec074_computer", "testdata", "elements.ps1")
	startApp(t, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	time.Sleep(time.Second)
	titleBar := image.Pt(400, 112)
	return frontWindowNamed(t, driver, els, &titleBar, func(w Element) bool { return w.Name == elementsWindowTitle })
}

func centre(r image.Rectangle) image.Point {
	return r.Min.Add(r.Size().Div(2))
}

// The reader sees a WinForms window's controls with their roles, ids, and
// paths; finds them by selector as matching its outline would; places the
// element under the pointer; and focuses and reads a field.
func TestWindowsElements(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	window := elementsWindow(t, driver, els)
	ctx := t.Context()

	assert.Equal(t, RoleWindow, window.Role)
	assert.Equal(t, "powershell", window.App)
	assert.Equal(t, elementsWindowTitle, window.Window)
	assert.Empty(t, window.Path)

	began := time.Now()
	outline, err := els.Outline(window, 0)
	t.Logf("elements: outline of %d elements in %s", len(outline), time.Since(began))
	require.NoError(t, err)
	byID := map[string]Element{}
	for _, e := range outline {
		assert.Contains(t, Roles, e.Role)
		assert.NotEmpty(t, e.Path, "%+v", e)
		assert.True(t, e.Bounds.In(window.Bounds), "%+v lies outside the window %v", e, window.Bounds)
		assert.Equal(t, elementsWindowTitle, e.Window)
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	for id, role := range map[string]string{
		"lblAmount": RoleText, "amountBox": RoleTextField, "saveButton": RoleButton, "agreeBox": RoleCheckbox,
		"methodBox": RoleComboBox, "paymentGroup": RoleGroup, "paymentAmountBox": RoleTextField, "paymentSaveButton": RoleButton,
	} {
		require.Contains(t, byID, id, "outline: %+v", outline)
		assert.Equal(t, role, byID[id].Role, id)
	}
	assert.Equal(t, "支払方法", byID["methodBox"].Name, "an accessible name is the name")
	assert.Equal(t, "同意する", byID["agreeBox"].Name)
	assert.Equal(t, PathStep{Role: RoleGroup, Name: "支払情報", Index: 0}, byID["paymentSaveButton"].Path[0], "the path leads through the group")

	// The disabled submit button reads as disabled, and enabled selects by it.
	require.Contains(t, byID, "submitButton")
	assert.True(t, byID["submitButton"].Disabled, "a disabled button reads as disabled")
	assert.False(t, byID["saveButton"].Disabled)
	grayed, err := els.Find(Selector{Role: RoleButton, Name: "送信", Enabled: new(false)})
	require.NoError(t, err)
	require.Len(t, grayed, 1, "%+v", grayed)
	assert.Equal(t, "submitButton", grayed[0].ID)
	usable, err := els.Find(Selector{Role: RoleButton, Name: "送信", Enabled: new(true)})
	require.NoError(t, err)
	assert.Empty(t, usable, "the disabled button is not an enabled one")

	// Find agrees with matching the outline.
	began = time.Now()
	saves, err := els.Find(Selector{Role: RoleButton, Name: "保存"})
	t.Logf("elements: find in %s", time.Since(began))
	require.NoError(t, err)
	require.Len(t, saves, 2, "%+v", saves)
	expected, err := Match(window, outline, Selector{Role: RoleButton, Name: "保存"})
	require.NoError(t, err)
	assert.Equal(t, expected, saves)

	inner, err := els.Find(Selector{Role: RoleButton, Name: "保存", In: &Selector{Role: RoleGroup, Name: "支払情報"}})
	require.NoError(t, err)
	require.Len(t, inner, 1)
	assert.Equal(t, "paymentSaveButton", inner[0].ID)

	near, err := els.Find(Selector{Role: RoleTextField, Near: &Near{Label: "金額", Side: SideRight}})
	require.NoError(t, err)
	assert.Equal(t, []string{"amountBox", "paymentAmountBox"}, []string{near[0].ID, near[1].ID}, "%+v", near)

	// The element under the pointer, with its place in the window.
	target := centre(inner[0].Bounds)
	require.NoError(t, driver.Move(ctx, target))
	began = time.Now()
	at, err := els.At(target.X, target.Y)
	t.Logf("elements: at in %s", time.Since(began))
	require.NoError(t, err)
	assert.Equal(t, "paymentSaveButton", at.ID)
	assert.Equal(t, inner[0].Path, at.Path)
	assert.Equal(t, elementsWindowTitle, at.Window)
	assert.Equal(t, inner[0].Bounds, at.Bounds)

	// The title bar is an element of its own, one step below the window.
	titleBar, err := els.At(window.Bounds.Min.X+300, window.Bounds.Min.Y+8)
	require.NoError(t, err)
	assert.Equal(t, "TitleBar", titleBar.ID, "%+v", titleBar)
	assert.Len(t, titleBar.Path, 1)
	assert.Equal(t, elementsWindowTitle, titleBar.Window)

	// Focus, and the element with the focus.
	require.NoError(t, els.Focus(byID["methodBox"]))
	focused, err := els.Focused()
	require.NoError(t, err)
	assert.Equal(t, "methodBox", focused.ID)
	assert.ErrorIs(t, els.Focus(Element{Role: RoleButton, Name: "nope", Path: []PathStep{{Role: RoleButton, Name: "nope"}}}), ErrNotFound)

	// Typed text is the field's value.
	require.NoError(t, els.Focus(byID["amountBox"]))
	require.NoError(t, driver.Type(ctx, "1200"))
	field := centre(byID["amountBox"].Bounds)
	filled, err := els.At(field.X, field.Y)
	require.NoError(t, err)
	assert.Equal(t, "1200", filled.Value)
}

// Elements perform their patterns when asked: a button invokes, a checkbox
// toggles, a field takes a value, a list item selects, a combo box expands;
// a disabled control and a label refuse; and an application that holds the
// call inside a dialog does not stop the reader.
func TestWindowsDirectInput(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	window := elementsWindow(t, driver, els)
	ctx := t.Context()

	outline, err := els.Outline(window, 0)
	require.NoError(t, err)
	byID := map[string]Element{}
	for _, e := range outline {
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	for _, id := range []string{"statusLabel", "saveButton", "agreeBox", "amountBox", "methodBox", "tierList", "submitButton", "confirmButton"} {
		require.Contains(t, byID, id, "outline: %+v", outline)
	}
	statusAt := centre(byID["statusLabel"].Bounds)
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

	began = time.Now()
	require.NoError(t, els.Apply(byID["amountBox"], PatternSetValue, "１２００"))
	t.Logf("elements: set value in %s", time.Since(began))
	field := centre(byID["amountBox"].Bounds)
	filled, err := els.At(field.X, field.Y)
	require.NoError(t, err)
	assert.Equal(t, "１２００", filled.Value, "the value is set as given")

	var items []Element
	require.Eventually(t, func() bool {
		items, err = els.Find(Selector{Role: RoleListItem, Name: "優先"})
		return err == nil && len(items) == 1
	}, 3*time.Second, 100*time.Millisecond, "list item: %+v %v", items, err)
	require.NoError(t, els.Apply(items[0], PatternSelect, ""))
	expectStatus("区分: 優先")

	// A Win32 combo box offers no expand pattern of its own, only an open
	// button; the list it opens is a window of its own, and the item under
	// a point below the box belongs to it.
	assert.ErrorIs(t, els.Apply(byID["methodBox"], PatternExpand, ""), ErrNoPattern)
	open, err := els.Find(Selector{Role: RoleButton, In: &Selector{Role: RoleComboBox, Name: "支払方法"}})
	require.NoError(t, err)
	require.Len(t, open, 1, "%+v", open)
	require.NoError(t, els.Apply(open[0], PatternInvoke, ""))
	box := byID["methodBox"].Bounds
	var item Element
	require.Eventually(t, func() bool {
		item, err = els.At(box.Min.X+20, box.Max.Y+12)
		return err == nil && item.Role == RoleListItem
	}, 3*time.Second, 100*time.Millisecond, "no list item below the combo box: %+v %v", item, err)
	require.NoError(t, driver.PressKeys(ctx, []Key{KeyEscape}, 1))

	assert.ErrorIs(t, els.Apply(byID["submitButton"], PatternInvoke, ""), ErrDisabled)
	assert.ErrorIs(t, els.Apply(byID["lblAmount"], PatternInvoke, ""), ErrNoPattern, "a label has nothing to invoke")
	assert.ErrorIs(t, els.Apply(Element{Role: RoleButton, Name: "nope", Path: []PathStep{{Role: RoleButton, Name: "nope"}}}, PatternInvoke, ""), ErrNotFound)
	assert.ErrorIs(t, els.Apply(byID["submitButton"], PatternSetValue, "x"), ErrDisabled)

	// A handler that opens a dialog holds the call; the answer still comes
	// within the deadline, the reader still answers, and the next call
	// works once the dialog is gone.
	began = time.Now()
	err = els.Apply(byID["confirmButton"], PatternInvoke, "")
	t.Logf("elements: invoke that opens a dialog answered %v in %s", err, time.Since(began))
	assert.Less(t, time.Since(began), elementsTimeout+time.Second)
	var front Element
	require.Eventually(t, func() bool {
		front, err = els.FrontWindow()
		return err == nil && front.Name == "確認"
	}, 3*time.Second, 100*time.Millisecond, "the dialog is not in front: %+v %v", front, err)
	require.NoError(t, driver.PressKeys(ctx, []Key{KeyEnter}, 1))
	require.Eventually(t, func() bool {
		front, err = els.FrontWindow()
		return err == nil && front.Name == elementsWindowTitle
	}, 3*time.Second, 100*time.Millisecond, "the window is not back in front: %+v %v", front, err)
	require.NoError(t, els.Apply(byID["agreeBox"], PatternToggle, ""))
	expectStatus("同意: True")
}

// A table in Details view is read row by row, each cell under the column
// its header names; a column with no header is a miss.
func TestWindowsTableRead(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	elementsWindow(t, driver, els)
	ctx := t.Context()

	container := Selector{Role: RoleList, ID: "linesList"}
	rows, err := els.Rows(ctx, container, []string{"品目", "金額"}, 100)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"りんご", "1,200"}, {"みかん", "3,400"}}, rows)

	// The columns may be asked for in any order, by their headers.
	swapped, err := els.Rows(ctx, container, []string{"金額", "品目"}, 100)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1,200", "りんご"}, {"3,400", "みかん"}}, swapped)

	// max bounds the rows read.
	one, err := els.Rows(ctx, container, []string{"品目"}, 1)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"りんご"}}, one)

	// A header that is not there is a miss.
	_, err = els.Rows(ctx, container, []string{"単価"}, 100)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestWindowsElementTimings logs how long reading elements takes in common
// applications. It asserts nothing about time; the numbers inform the
// replay design.
func TestWindowsElementTimings(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "..", "..", "conformance", "spec074_computer", "testdata", "elements.ps1")
	programFiles := os.Getenv("ProgramFiles(x86)")
	apps := []struct {
		name    string
		command string
		args    []string
	}{
		{"powershell", "powershell.exe", []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}},
		{"notepad", "notepad.exe", nil},
		{"msedge", filepath.Join(programFiles, "Microsoft", "Edge", "Application", "msedge.exe"), []string{"--new-window", "about:blank"}},
		{"code", filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "Code.exe"), []string{"--new-window", "--disable-workspace-trust"}},
	}
	for _, app := range apps {
		t.Run(app.name, func(t *testing.T) {
			if _, err := exec.LookPath(app.command); err != nil {
				t.Skipf("%s is not installed", app.command)
			}
			if alreadyRunning(t, app.name+".exe") {
				t.Skipf("%s is already running; its windows would be left behind", app.name)
			}
			startApp(t, app.command, app.args...)
			var titleBar *image.Point
			if app.name == "powershell" {
				time.Sleep(time.Second)
				titleBar = &image.Point{X: 400, Y: 112}
			}
			window, seen, ok := waitFront(t, driver, els, titleBar, func(w Element) bool { return strings.EqualFold(w.App, app.name) }, 15*time.Second)
			if !ok {
				t.Skipf("the %s window did not come to the front; %q is there", app.name, seen.Name)
			}
			began := time.Now()
			outline, err := els.Outline(window, 0)
			outlined := time.Since(began)
			if err != nil {
				t.Logf("elements: app=%s window=%q outline failed: %v", app.name, window.Name, err)
				return
			}
			at := median(5, func() { _, _ = els.At(centre(window.Bounds).X, centre(window.Bounds).Y) })
			find := median(5, func() { _, _ = els.Find(Selector{Role: RoleButton, Name: "*"}) })
			t.Logf("elements: app=%s window=%q elements=%d outline=%s at=%s find=%s", app.name, window.Name, len(outline), outlined, at, find)
		})
	}
}

// alreadyRunning reports whether a process with the image name runs.
func alreadyRunning(t *testing.T, image string) bool {
	t.Helper()
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/NH").Output()
	return err == nil && strings.Contains(strings.ToLower(string(out)), strings.ToLower(image))
}

// median times fn n times and returns the middle duration.
func median(n int, fn func()) time.Duration {
	times := make([]time.Duration, 0, n)
	for range n {
		began := time.Now()
		fn()
		times = append(times, time.Since(began))
	}
	slices.Sort(times)
	return times[len(times)/2]
}
