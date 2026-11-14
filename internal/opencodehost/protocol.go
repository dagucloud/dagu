// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package opencodehost

import "strings"

// IsV2 reports whether the host uses the OpenCode v2 HTTP protocol.
func (c Config) IsV2() bool {
	return strings.HasPrefix(c.Version, "2.")
}

// SharingDisabled checks the effective sharing setting in either API's config
// response. V2 returns ordered documents; the last defined value wins.
func SharingDisabled(value any) bool {
	if settings, ok := value.(map[string]any); ok {
		return settings["share"] == "disabled"
	}
	entries, ok := value.([]any)
	if !ok {
		return false
	}
	var share any
	for _, entry := range entries {
		document, ok := entry.(map[string]any)
		if !ok || document["type"] != "document" {
			continue
		}
		info, ok := document["info"].(map[string]any)
		if ok && info["share"] != nil {
			share = info["share"]
		}
	}
	return share == "disabled"
}
