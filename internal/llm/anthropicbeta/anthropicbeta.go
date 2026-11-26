// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package anthropicbeta builds the anthropic-beta header of a request to the
// Anthropic API.
package anthropicbeta

import "strings"

// OAuth is the beta that the Anthropic API needs with the OAuth token of a
// subscription.
const OAuth = "oauth-2025-04-20"

// Join returns the values of anthropic-beta as one list, with beta added when no
// value names it. The values of the client keep their order.
func Join(values []string, beta string) string {
	var list []string
	found := false
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			found = found || item == beta
			list = append(list, item)
		}
	}
	if !found {
		list = append(list, beta)
	}
	return strings.Join(list, ",")
}
