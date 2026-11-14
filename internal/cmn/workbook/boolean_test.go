// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every yes/no word and mark spec 077 lists under Japanese text under a
// pinned type, and what it says is not read.
func TestParseBooleanText(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"true", "TRUE", "yes", "y", "on", "ok", "ＯＫ", "1", "１",
		"はい", "有", "有り", "あり", "済", "済み", "　済　", "完了", "完", "了", "可", "要", "必要", "対象", "該当", "真",
		"○", "〇", "◯", "◎", "●", "✓", "✔", "✅", "☑", "レ", "ﾚ",
	} {
		v, ok := parseBooleanText(in)
		require.True(t, ok, in)
		assert.True(t, v, in)
	}
	for _, in := range []string{
		"false", "no", "n", "off", "ng", "0", "０",
		"いいえ", "無", "無し", "なし", "未", "未済", "未了", "未完", "未完了", "不可", "否", "不要", "対象外", "非該当", "偽",
		"×", "✕", "✗", "✘", "❌", "☐", "-", "－", "−", "ー", "―", "—",
	} {
		v, ok := parseBooleanText(in)
		require.True(t, ok, in)
		assert.False(t, v, in)
	}
	for _, in := range []string{"△", "☒", "?", "？", "未定", "保留", "TBD", "-1", "2", "対応済み", "", "yes no"} {
		_, ok := parseBooleanText(in)
		assert.False(t, ok, in)
	}
}

// Through coerce: text, real booleans, and 0 and 1 all read; a failure
// quotes the cell's text.
func TestCoerceJapaneseBooleans(t *testing.T) {
	t.Parallel()
	for in, want := range map[any]bool{"済": true, "×": false, "ＴＲＵＥ": true, "１": true, true: true, int64(0): false, float64(1): true} {
		v, err := coerce(in, TypeBoolean, false)
		require.NoError(t, err, in)
		assert.Equal(t, want, v, in)
	}
	_, err := coerce("未定", TypeBoolean, false)
	require.EqualError(t, err, `expected boolean, found "未定"`)
	_, err = coerce(int64(2), TypeBoolean, false)
	require.EqualError(t, err, `expected boolean, found 2`)
}
