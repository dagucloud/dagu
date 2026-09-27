// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"encoding/json"
	"unicode/utf8"
)

// Fit makes the JSON encoding of messages fit in budget bytes. It shortens
// text first, then drops the newest messages when headers alone are too large,
// so the oldest emails are kept for processing. It reports whether anything
// was shortened or dropped.
func Fit(messages []Message, budget int) ([]Message, bool) {
	truncated := false
	for limit := TextLimit / 2; encodedSize(messages) > budget; limit /= 2 {
		for i := range messages {
			if utf8.RuneCountInString(messages[i].Text) > limit {
				messages[i].Text = truncateRunes(messages[i].Text, limit)
				truncated = true
			}
		}
		if limit == 0 {
			break
		}
	}
	for len(messages) > 0 && encodedSize(messages) > budget {
		messages = messages[:len(messages)-1]
		truncated = true
	}
	return messages, truncated
}

func encodedSize(messages []Message) int {
	data, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	return len(data)
}
