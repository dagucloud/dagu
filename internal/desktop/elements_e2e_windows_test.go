// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"fmt"
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

// swShowMinimized minimizes a window and leaves it active.
const swShowMinimized = 2

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
	assert.Equal(t, RoleDialog, front.Role, "a message box holds the window it belongs to")
	require.NoError(t, driver.PressKeys(ctx, []Key{KeyEnter}, 1))
	require.Eventually(t, func() bool {
		front, err = els.FrontWindow()
		return err == nil && front.Name == elementsWindowTitle
	}, 3*time.Second, 100*time.Millisecond, "the window is not back in front: %+v %v", front, err)
	assert.Equal(t, RoleWindow, front.Role)
	require.NoError(t, els.Apply(byID["agreeBox"], PatternToggle, ""))
	expectStatus("同意: True")
}

// A reader of one window reads it while another application's window has
// the foreground, and leaves the foreground where it is: it finds the
// window's elements, outlines it, and reads a table. Windows performs every
// pattern of a Windows Forms control by focusing it, so the reader refuses
// them rather than bring the window forward. A window that is gone reads as
// no window.
func TestWindowsElementsInWindowBehindAnother(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	elementsWindow(t, driver, els)
	ctx := t.Context()

	own := listedWindow(t, driver, func(w WindowID) bool { return w.Title == elementsWindowTitle })
	if alreadyRunning(t, "notepad.exe") {
		t.Skip("notepad is already running; its windows would be left behind")
	}
	startApp(t, "notepad.exe")
	other := listedWindow(t, driver, func(w WindowID) bool { return strings.EqualFold(w.App, "notepad") })
	require.Eventually(t, func() bool {
		_ = driver.Raise(other)
		return driver.FocusedWindow().Handle == other.Handle
	}, 15*time.Second, 500*time.Millisecond, "notepad did not take the foreground")
	foreground := func() string {
		w := driver.FocusedWindow()
		return fmt.Sprintf("%s %q %#x", w.App, w.Title, w.Handle)
	}
	inFront := foreground()

	in := els.InWindow(own)
	window, err := in.FrontWindow()
	require.NoError(t, err)
	assert.Equal(t, elementsWindowTitle, window.Name)
	front, err := els.FrontWindow()
	require.NoError(t, err)
	assert.Equal(t, "notepad", front.App, "the reader of the window in front still reads notepad")

	status := func() string {
		labels, err := in.Find(Selector{Role: RoleText, ID: "statusLabel"})
		if err != nil || len(labels) != 1 {
			return fmt.Sprint(labels, err)
		}
		return labels[0].Name
	}
	save, err := in.Find(Selector{Role: RoleButton, ID: "saveButton"})
	require.NoError(t, err)
	require.Len(t, save, 1)
	assert.Equal(t, inFront, foreground(), "reads leave the foreground where it was")
	assert.ErrorIs(t, in.Apply(save[0], PatternInvoke, ""), ErrForeground)
	assert.Equal(t, "待機中", status(), "the refused button was not pressed")
	assert.Equal(t, inFront, foreground(), "a refused invoke leaves the foreground where it was")

	amount, err := in.Find(Selector{Role: RoleTextField, ID: "amountBox"})
	require.NoError(t, err)
	require.Len(t, amount, 1)
	assert.ErrorIs(t, in.Apply(amount[0], PatternSetValue, "3400"), ErrForeground)
	assert.Equal(t, inFront, foreground(), "a refused value leaves the foreground where it was")

	outline, err := in.Outline(window, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, outline)
	rows, err := in.Rows(ctx, Selector{Role: RoleList, ID: "linesList"}, []string{"品目"}, 100)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"りんご"}, {"みかん"}}, rows)

	assert.Equal(t, inFront, foreground(), "reading a table leaves the foreground where it was")
	assert.NoError(t, in.Close(), "closing a window's reader leaves the reader it came from open")
	_, err = els.FrontWindow()
	assert.NoError(t, err)

	gone := own
	gone.PID++
	_, err = els.InWindow(gone).FrontWindow()
	assert.ErrorIs(t, err, ErrNoWindow, "a handle that names another process's window is a window that closed")
}

// webPage is a page with a field and a button that renames the page.
const webPage = `data:text/html,<title>Dagu web test</title><input id="amount" aria-label="amount" value="1200"><button id="save" onclick="document.title='Dagu web saved'">Save</button>`

// A reader of one window reads a web page in a browser window while another
// application's window has the foreground, and leaves the foreground where
// it is. Chromium takes the foreground to set a field's value, so the
// reader refuses the page's patterns as it does every other.
func TestWindowsElementsInWindowReadsAPageBehindAnother(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	edge := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
	if _, err := exec.LookPath(edge); err != nil {
		t.Skip("Microsoft Edge is not installed")
	}
	for _, image := range []string{"msedge.exe", "notepad.exe"} {
		if alreadyRunning(t, image) {
			t.Skipf("%s is already running; its windows would be left behind", image)
		}
	}
	// Edge hands the page to a browser process of its own, which outlives
	// the one started.
	t.Cleanup(func() { _ = exec.Command("taskkill", "/F", "/IM", "msedge.exe").Run() })
	startApp(t, edge, "--new-window", "--no-first-run", webPage)
	page := listedWindow(t, driver, func(w WindowID) bool { return strings.HasPrefix(w.Title, "Dagu web test") })
	startApp(t, "notepad.exe")
	other := listedWindow(t, driver, func(w WindowID) bool { return strings.EqualFold(w.App, "notepad") })

	in := els.InWindow(page)
	var save, amount []Element
	require.Eventually(t, func() bool {
		var err error
		save, err = in.Find(Selector{Role: RoleButton, Name: "Save"})
		if err != nil || len(save) != 1 {
			return false
		}
		amount, err = in.Find(Selector{Role: RoleTextField, Name: "amount"})
		return err == nil && len(amount) == 1
	}, 15*time.Second, 500*time.Millisecond, "the page's elements: %+v %+v", save, amount)
	// Notepad takes the foreground once the page is up, so a browser that
	// raises its window while it starts is not taken for a pattern's doing.
	require.Eventually(t, func() bool {
		_ = driver.Raise(other)
		return driver.FocusedWindow().Handle == other.Handle
	}, 15*time.Second, 500*time.Millisecond, "notepad did not take the foreground")
	foreground := func() string {
		w := driver.FocusedWindow()
		return fmt.Sprintf("%s %q %#x", w.App, w.Title, w.Handle)
	}
	inFront := foreground()

	assert.Equal(t, "1200", amount[0].Value)
	assert.ErrorIs(t, in.Apply(amount[0], PatternSetValue, "3400"), ErrForeground)
	assert.ErrorIs(t, in.Apply(save[0], PatternInvoke, ""), ErrForeground)
	assert.Equal(t, inFront, foreground(), "reading the page leaves the foreground where it was")
}

// listedWindow waits until Driver.Windows lists a window that matches.
func listedWindow(t *testing.T, driver *Driver, matches func(WindowID) bool) WindowID {
	t.Helper()
	var found WindowID
	require.Eventually(t, func() bool {
		for _, w := range driver.Windows() {
			if matches(w) {
				found = w
				return true
			}
		}
		return false
	}, 30*time.Second, 200*time.Millisecond, "no such window is listed: %+v", driver.Windows())
	return found
}

// A minimized window shows none of its elements. UI Automation still reports
// them, not offscreen and with their size, at the place Windows parks the
// window, so the window in front, when it is minimized, lists none.
func TestWindowsElementsOfMinimizedWindow(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	elementsWindow(t, driver, els)
	front := driver.FocusedWindow()
	require.Equal(t, elementsWindowTitle, front.Title)

	_, _, _ = procShowWindow.Call(uintptr(front.Handle), swShowMinimized)
	require.Eventually(t, func() bool {
		iconic, _, _ := procIsIconic.Call(uintptr(front.Handle))
		return iconic != 0
	}, 3*time.Second, 50*time.Millisecond, "the window did not minimize")
	window, err := els.FrontWindow()
	require.NoError(t, err)
	require.Equal(t, elementsWindowTitle, window.Name, "a window minimized this way keeps the foreground")

	outline, err := els.Outline(window, 0)
	require.NoError(t, err)
	assert.Empty(t, outline, "a minimized window shows no element")
	found, err := els.Find(Selector{Role: RoleButton, ID: "saveButton"})
	require.NoError(t, err)
	assert.Empty(t, found, "nothing is found in a minimized window")
}

// A Chromium window builds the tree of its page once a client first asks
// for it, and reads thin until then. The reader that met the page keeps
// seeing it as it grows: here the page adds its button five seconds after
// it loads, so the first read comes before the button is there.
func TestWindowsElementsOfChromiumPage(t *testing.T) {
	driver := e2eDesktop(t)
	els := e2eElements(t)
	edge := edgePath()
	if edge == "" {
		t.Skip("Edge is not installed")
	}
	dir, err := os.MkdirTemp("", "dagu-chromium-")
	require.NoError(t, err)
	// Edge may hold its profile a moment after it ends.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	page := filepath.Join(dir, "page.html")
	html := `<!doctype html><title>Dagu page test</title><body><script>
setTimeout(function () {
  var button = document.createElement('button');
  button.textContent = 'Dagu page button';
  document.body.appendChild(button);
}, 5000);
</script></body>`
	require.NoError(t, os.WriteFile(page, []byte(html), 0o600))
	startApp(t, edge, "--user-data-dir="+filepath.Join(dir, "profile"), "--no-first-run", "--no-default-browser-check", "--new-window", "file:///"+filepath.ToSlash(page))
	window := frontWindowNamed(t, driver, els, nil, func(w Element) bool { return strings.HasPrefix(w.Name, "Dagu page test") })

	button := Selector{Role: RoleButton, Name: "Dagu page button"}
	outline, err := els.Outline(window, 0)
	require.NoError(t, err)
	t.Logf("elements: first read of the page's window has %d elements", len(outline))
	found, err := Match(window, outline, button)
	require.NoError(t, err)
	require.Empty(t, found, "the first read comes before the page adds its button")
	began := time.Now()
	for deadline := began.Add(30 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		found, err = els.Find(button)
		require.NoError(t, err)
		if len(found) == 1 {
			break
		}
		require.True(t, time.Now().Before(deadline), "the reader never saw the page's button: %+v", found)
	}
	t.Logf("elements: the reader saw the page's button %s after its first read", time.Since(began))
}

// edgePath returns where Edge is installed, or "" when it is not.
func edgePath() string {
	for _, programs := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if programs == "" {
			continue
		}
		path := filepath.Join(programs, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
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
