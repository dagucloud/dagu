// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"encoding/json"
	"unicode/utf8"
)

// FitText shortens message text until the JSON encoding of messages fits in
// budget bytes. It reports whether any text was shortened.
func FitText(messages []Message, budget int) bool {
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
	return truncated
}

func encodedSize(messages []Message) int {
	data, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	return len(data)
}
