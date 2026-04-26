// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import "maps"

// FilterPushBackInputs returns only declared push-back inputs. If no allowlist
// is provided, the stored inputs are preserved as-is.
func FilterPushBackInputs(allowed []string, inputs map[string]string) map[string]string {
	if len(inputs) == 0 {
		return nil
	}
	if len(allowed) == 0 {
		return maps.Clone(inputs)
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}

	filtered := make(map[string]string)
	for key, value := range inputs {
		if _, ok := allowedSet[key]; ok {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// NormalizePushBackHistory ensures stored history is filtered and seeded from
// legacy state when only the latest iteration/input pair is available.
func NormalizePushBackHistory(
	allowed []string,
	iteration int,
	latestInputs map[string]string,
	history []PushBackEntry,
) []PushBackEntry {
	normalized := ClonePushBackHistory(history)
	if len(normalized) == 0 && iteration > 0 {
		normalized = append(normalized, PushBackEntry{
			Iteration: iteration,
			Inputs:    FilterPushBackInputs(allowed, latestInputs),
		})
	}
	for i := range normalized {
		normalized[i].Inputs = FilterPushBackInputs(allowed, normalized[i].Inputs)
	}
	return normalized
}

// ClonePushBackHistory returns a deep copy of push-back history entries.
func ClonePushBackHistory(src []PushBackEntry) []PushBackEntry {
	if len(src) == 0 {
		return nil
	}

	dst := make([]PushBackEntry, len(src))
	for i, entry := range src {
		dst[i] = PushBackEntry{
			Iteration: entry.Iteration,
			By:        entry.By,
			At:        entry.At,
			Inputs:    maps.Clone(entry.Inputs),
		}
	}
	return dst
}
