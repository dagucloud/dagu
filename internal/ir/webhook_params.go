// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// BuildWebhookRuntimeParams formats webhook payload, headers, and optional
// trigger-specific metadata into the inline runtime-param syntax Dagu already
// uses for queued and direct webhook runs.
func BuildWebhookRuntimeParams(payload, headers string, extras map[string]string) string {
	parts := []string{
		fmt.Sprintf("WEBHOOK_PAYLOAD=%s", strconv.Quote(payload)),
		fmt.Sprintf("WEBHOOK_HEADERS=%s", strconv.Quote(headers)),
	}

	if len(extras) == 0 {
		return strings.Join(parts, " ")
	}

	keys := slices.Sorted(maps.Keys(extras))
	for _, key := range keys {
		value := extras[key]
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, strconv.Quote(value)))
	}

	return strings.Join(parts, " ")
}
