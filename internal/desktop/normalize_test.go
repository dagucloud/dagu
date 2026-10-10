// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ABC123", NormalizeText("ＡＢＣ１２３"))
	assert.Equal(t, "カタカナ", NormalizeText("ｶﾀｶﾅ"))
	assert.Equal(t, "保存 しました", NormalizeText("  保存　しました "))
	assert.Equal(t, "請求書 INV-0001", NormalizeText("請求書\t\nINV-0001"))
	assert.Equal(t, "", NormalizeText("　"))
	assert.Equal(t, "金額", normalizeLabel("金額："))
	assert.Equal(t, "金額", normalizeLabel(" 金額: "))
	assert.True(t, nameMatches("保存", "保存　"))
	assert.True(t, nameMatches("請求書 *", "請求書　ＩＮＶ-0001"))
	assert.False(t, nameMatches("保存", "保存する"))
}
