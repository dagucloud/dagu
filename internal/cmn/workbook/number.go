// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import "strings"

// numberText is the text a pinned number is parsed from: folded, without
// thousands separators, and without one yen sign before it or one 円
// after it, so ￥123,000 and 123,000円 read as 123000.
func numberText(s string) string {
	s = foldWidth(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "¥"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "円"))
	return strings.ReplaceAll(s, ",", "")
}
