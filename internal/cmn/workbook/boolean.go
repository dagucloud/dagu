// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import "strings"

// booleanWords maps the words and marks a form writes in a yes/no column
// to their values, after folding and lowercasing. Nothing outside the map
// is a boolean: a prefix such as 未 or a suffix such as 済 does not decide
// a word, so 未定 and 対応済み fail rather than guess.
var booleanWords = map[string]bool{
	// English.
	"true": true, "yes": true, "y": true, "on": true, "ok": true, "1": true,
	"false": false, "no": false, "n": false, "off": false, "ng": false, "0": false,
	// Words.
	"はい": true, "有": true, "有り": true, "あり": true, "済": true, "済み": true, "完了": true, "完": true, "了": true,
	"可": true, "要": true, "必要": true, "対象": true, "該当": true, "真": true,
	"いいえ": false, "無": false, "無し": false, "なし": false, "未": false, "未済": false, "未了": false, "未完": false,
	"未完了": false, "不可": false, "否": false, "不要": false, "対象外": false, "非該当": false, "偽": false,
	// Marks.
	"○": true, "〇": true, "◯": true, "◎": true, "●": true, "✓": true, "✔": true, "✅": true, "☑": true, "レ": true,
	"×": false, "✕": false, "✗": false, "✘": false, "❌": false, "☐": false, "-": false, "ー": false, "―": false, "—": false,
}

// parseBooleanText reads a yes/no word or mark the way a Japanese form
// writes it. See spec 077, Japanese text under a pinned type.
func parseBooleanText(text string) (bool, bool) {
	value, ok := booleanWords[strings.ToLower(foldWidth(text))]
	return value, ok
}
