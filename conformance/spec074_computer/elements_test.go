// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec074_computer_test

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const elementsWindowTitle = "Dagu elements test"

// elementJSON is an element as the command prints it.
type elementJSON struct {
	Role   string `json:"role"`
	Name   string `json:"name"`
	ID     string `json:"id"`
	Value  string `json:"value"`
	Label  string `json:"label"`
	App    string `json:"app"`
	Window string `json:"window"`
	Bounds struct {
		X, Y, Width, Height int
	} `json:"bounds"`
	Path []struct {
		Role  string `json:"role"`
		Name  string `json:"name"`
		Index int    `json:"index"`
	} `json:"path"`
}

type errorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// elementsDesktop skips unless the test runs on an interactive Windows
// desktop, then starts the window with named controls and brings it to
// the front, as a person would by clicking its title bar.
func elementsDesktop(t *testing.T) *harness.Runner {
	t.Helper()
	if runtime.GOOS != "windows" || os.Getenv("DAGU_DESKTOP_E2E") != "1" {
		t.Skip("set DAGU_DESKTOP_E2E=1 on an interactive Windows desktop")
	}
	dagu := harness.NewRunner(t).WithCommandTimeout(desktopCommandTimeout)
	window := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", dagu.ProjectPath("elements.ps1"))
	require.NoError(t, window.Start())
	t.Cleanup(func() {
		_ = window.Process.Kill()
		_ = window.Wait()
	})
	var front string
	require.Eventually(t, func() bool {
		result := dagu.Run("computer", "elements", "--format", "json", "--limit", "1")
		var outline struct {
			Window elementJSON `json:"window"`
		}
		if json.Unmarshal([]byte(result.Stdout()), &outline) != nil {
			return false
		}
		front = outline.Window.Name
		if front == elementsWindowTitle {
			return true
		}
		clickWindow(t)
		return false
	}, 30*time.Second, time.Second, "the window did not come to the front; %q is there", &front)
	return dagu
}

// clickWindow clicks where the test window's title bar is: the window
// opens at (100,100), so its title bar is just below that.
func clickWindow(t *testing.T) {
	t.Helper()
	script := `Add-Type -AssemblyName System.Windows.Forms
Add-Type @"
using System; using System.Runtime.InteropServices;
public class Dagu { [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint x, uint y, uint d, UIntPtr e); }
"@
[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(400, 112)
[Dagu]::mouse_event(2, 0, 0, 0, [UIntPtr]::Zero); [Dagu]::mouse_event(4, 0, 0, 0, [UIntPtr]::Zero)`
	_ = exec.Command("powershell.exe", "-NoProfile", "-Command", script).Run()
}

func outline(t *testing.T, dagu *harness.Runner, args ...string) (struct {
	Window    elementJSON   `json:"window"`
	Elements  []elementJSON `json:"elements"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated"`
}, *harness.Result) {
	t.Helper()
	result := dagu.Run(append([]string{"computer", "elements", "--format", "json"}, args...)...)
	var out struct {
		Window    elementJSON   `json:"window"`
		Elements  []elementJSON `json:"elements"`
		Count     int           `json:"count"`
		Truncated bool          `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &out), result.Stdout())
	return out, result
}

// The outline names every control with its role, id, and path, in JSON
// and as text.
func TestComputerElementsOutline(t *testing.T) {
	dagu := elementsDesktop(t)

	out, result := outline(t, dagu)
	result.ExpectExitCode(0)
	assert.Equal(t, elementsWindowTitle, out.Window.Name)
	assert.Equal(t, "powershell", out.Window.App)
	assert.Equal(t, "window", out.Window.Role)
	assert.NotNil(t, out.Window.Path)
	assert.False(t, out.Truncated)
	assert.Equal(t, len(out.Elements), out.Count)

	byID := map[string]elementJSON{}
	for _, e := range out.Elements {
		assert.NotEmpty(t, e.Path, "%+v", e)
		assert.Equal(t, elementsWindowTitle, e.Window)
		assert.GreaterOrEqual(t, e.Bounds.X, out.Window.Bounds.X)
		assert.GreaterOrEqual(t, e.Bounds.Y, out.Window.Bounds.Y)
		if e.ID != "" {
			byID[e.ID] = e
		}
	}
	for id, role := range map[string]string{
		"lblAmount": "text", "amountBox": "text_field", "saveButton": "button", "agreeBox": "checkbox",
		"methodBox": "combo_box", "paymentGroup": "group", "paymentAmountBox": "text_field", "paymentSaveButton": "button",
	} {
		require.Contains(t, byID, id, "%+v", out.Elements)
		assert.Equal(t, role, byID[id].Role, id)
	}
	assert.Equal(t, "支払方法", byID["methodBox"].Name)
	require.Len(t, byID["paymentSaveButton"].Path, 2)
	assert.Equal(t, "支払情報", byID["paymentSaveButton"].Path[0].Name)

	short, result := outline(t, dagu, "--limit", "3")
	result.ExpectExitCode(0)
	assert.Len(t, short.Elements, 3)
	assert.True(t, short.Truncated)

	text := dagu.Run("computer", "elements")
	text.ExpectExitCode(0)
	assert.Contains(t, text.Stdout(), `Window: "Dagu elements test" (powershell)`)
	assert.Contains(t, text.Stdout(), `button "保存" id=saveButton`)
	assert.Contains(t, text.Stdout(), `  [`, "elements in the group are indented")
}

// A selector reports its matches; the command succeeds only on exactly
// one, and otherwise names why with the matches still listed.
func TestComputerElementsMatch(t *testing.T) {
	dagu := elementsDesktop(t)

	for _, tc := range []struct {
		name     string
		selector string
		ids      []string
		code     string
	}{
		{"ambiguous", `{"role":"button","name":"保存"}`, []string{"saveButton", "paymentSaveButton"}, "ambiguous"},
		{"in", `{"role":"button","name":"保存","in":{"role":"group","name":"支払情報"}}`, []string{"paymentSaveButton"}, ""},
		{"id", `{"role":"text_field","id":"paymentAmountBox"}`, []string{"paymentAmountBox"}, ""},
		{"near ambiguous", `{"role":"text_field","near":{"label":"金額","side":"right"}}`, []string{"amountBox", "paymentAmountBox"}, "ambiguous"},
		{"near and nth", `{"role":"text_field","near":{"label":"金額","side":"right"},"nth":0}`, []string{"amountBox"}, ""},
		{"near in", `{"role":"text_field","near":{"label":"金額","side":"right"},"in":{"role":"group","id":"paymentGroup"}}`, []string{"paymentAmountBox"}, ""},
		{"wildcard", `{"role":"checkbox","name":"同意*"}`, []string{"agreeBox"}, ""},
		{"window", `{"window":"Dagu elements*","role":"combo_box","name":"支払方法"}`, []string{"methodBox"}, ""},
		{"not found", `{"role":"button","name":"削除"}`, nil, "not_found"},
		{"other window", `{"window":"経費精算","role":"button","name":"保存"}`, nil, "not_found"},
		{"other app", `{"app":"Expense","role":"button","name":"保存"}`, nil, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := dagu.Run("computer", "elements", "--format", "json", "--match", tc.selector)
			var out struct {
				Error   *errorJSON    `json:"error"`
				Window  elementJSON   `json:"window"`
				Count   int           `json:"count"`
				Matches []elementJSON `json:"matches"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &out), result.Stdout())
			assert.Equal(t, elementsWindowTitle, out.Window.Name)
			var ids []string
			for _, e := range out.Matches {
				ids = append(ids, e.ID)
			}
			assert.Equal(t, tc.ids, ids)
			assert.Equal(t, len(tc.ids), out.Count)
			if tc.code == "" {
				result.ExpectExitCode(0)
				assert.Nil(t, out.Error)
				return
			}
			result.ExpectNonZeroExitCode()
			require.NotNil(t, out.Error)
			assert.Equal(t, tc.code, out.Error.Code)
		})
	}

	stdin := dagu.RunWithStdin(nil, strings.NewReader(`{"role":"combo_box","name":"支払方法"}`), "computer", "elements", "--format", "json", "--match", "-")
	stdin.ExpectExitCode(0)
	assert.Contains(t, stdin.Stdout(), `"id": "methodBox"`)
}

// The element under the pointer is reported once, or whenever it changes
// while watching, as the pointer is where the title bar click left it.
func TestComputerElementsAtPointer(t *testing.T) {
	dagu := elementsDesktop(t)

	result := dagu.Run("computer", "elements", "--format", "json", "--at-pointer", "--after", "0s")
	result.ExpectExitCode(0)
	var at elementJSON
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &at), result.Stdout())
	assert.Equal(t, elementsWindowTitle, at.Window)
	assert.NotEmpty(t, at.Role)

	// Starting the command and opening the desktop take a moment before
	// the first look under the pointer.
	reader, writer := io.Pipe()
	go func() {
		time.Sleep(3 * time.Second)
		_ = writer.Close()
	}()
	watch := dagu.RunWithStdin(nil, reader, "computer", "elements", "--format", "json", "--watch")
	watch.ExpectExitCode(0)
	lines := strings.Split(strings.TrimSpace(watch.Stdout()), "\n")
	require.NotEmpty(t, lines[0], "one element is reported as soon as the watch starts")
	var first elementJSON
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first), lines[0])
	assert.Equal(t, elementsWindowTitle, first.Window)
}
