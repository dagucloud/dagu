// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutline(t *testing.T) {
	t.Parallel()

	const outlineOnly = `{"ai": "never", "screenshots": "never", "do": [{"outline": "window"}]}`

	t.Run("publishes the front window's elements with no model", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		// An unnamed group shows nothing and no selector names it, so its
		// button is listed a level up.
		toolbar := []desktop.PathStep{{Role: desktop.RoleGroup, Index: 1}}
		run.elements.elements = append(run.elements.elements,
			desktop.Element{Role: desktop.RoleGroup, Bounds: image.Rect(120, 460, 660, 490), Path: toolbar},
			desktop.Element{Role: desktop.RoleButton, Name: "閉じる", Disabled: true, Bounds: image.Rect(570, 460, 650, 488),
				Path: append(toolbar[:1:1], desktop.PathStep{Role: desktop.RoleButton, Name: "閉じる", Index: 0})},
		)
		run.llm = nil
		execution := run.execute(outlineOnly, nil)
		require.NoError(t, execution.err)
		assert.Equal(t, `window "経費精算 - 請求書 1042" app="expense"
  text "保存しました"
  text "金額"
  text_field value="12,000" id="amountBox"
  button "保存" id="saveButton"
  group "支払情報" id="paymentGroup"
    button "保存" id="paymentSaveButton"
  button "閉じる" disabled
`, execution.exec.GetOutputs()["window"])
		events := operationEvents(execution.exec.GetAgentSession())
		require.Len(t, events, 1)
		assert.Equal(t, agentstep.StatusCompleted, events[0].Status)
		assert.Equal(t, agentstep.ViaExact, events[0].Via)
		assert.Empty(t, run.vision.requests, "no model request")
		assert.Zero(t, run.backend.captures, "no screenshot")
	})

	t.Run("masks a secret a field shows", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		// Quoting would escape the quote and the backslash, so the secret is
		// masked before it is quoted.
		run.secrets = map[string]string{"VENDOR_CODE": `V-77"se\cret`}
		run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleTextField, Name: "取引先コード", Value: `V-77"se\cret`,
			Bounds: image.Rect(190, 220, 390, 244), Path: []desktop.PathStep{{Role: desktop.RoleTextField, Name: "取引先コード", Index: 1}}})
		run.llm = nil
		execution := run.execute(outlineOnly, nil)
		require.NoError(t, execution.err)
		window, _ := execution.exec.GetOutputs()["window"].(string)
		assert.Contains(t, window, `text_field "取引先コード"`)
		assert.NotContains(t, window, "V-77")
	})

	t.Run("says when the window has more elements than it lists", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.elements.elements = nil
		for i := range maxOutlineElements + 1 {
			name := fmt.Sprintf("行 %d", i)
			run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleCell, Name: name,
				Bounds: image.Rect(120, 110, 300, 130), Path: []desktop.PathStep{{Role: desktop.RoleCell, Name: name, Index: i}}})
		}
		run.llm = nil
		execution := run.execute(outlineOnly, nil)
		require.NoError(t, execution.err)
		window, _ := execution.exec.GetOutputs()["window"].(string)
		lines := strings.Split(strings.TrimSuffix(window, "\n"), "\n")
		require.Len(t, lines, maxOutlineElements+2, "the window, the elements listed, and the note")
		assert.Equal(t, fmt.Sprintf(`  cell "行 %d"`, maxOutlineElements-1), lines[maxOutlineElements])
		assert.Equal(t, fmt.Sprintf("(the window has more than %d elements; the rest are not listed)", maxOutlineElements), lines[maxOutlineElements+1])
	})

	t.Run("never reads a window the step never operates", func(t *testing.T) {
		t.Parallel()
		run := newTestRun(t)
		run.elements = newFakeElements()
		run.protectApps = []string{"expense"}
		run.llm = nil
		execution := run.execute(outlineOnly, nil)
		require.ErrorContains(t, execution.err, "which it never operates")
		assert.Empty(t, execution.exec.GetOutputs())
	})
}
