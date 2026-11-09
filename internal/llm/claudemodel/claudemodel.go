// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package claudemodel reads Claude model IDs as the providers that serve
// Claude spell them, to tell what a model accepts.
package claudemodel

import (
	"regexp"
	"strconv"
	"strings"
)

// modelPattern reads the family and version from a model ID such as
// claude-opus-4-8, anthropic.claude-sonnet-5, claude-opus-4-5@20251101, or
// anthropic/claude-sonnet-5.5.
var modelPattern = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:[-.](\d+))?`)

// legacyPattern reads the version from model IDs of the claude-3
// generation, which put the version before the family.
var legacyPattern = regexp.MustCompile(`claude-(\d+)`)

// dateSuffixMin separates a date suffix such as 20250929 from a minor
// version.
const dateSuffixMin = 100

// Model is the family and version a Claude model ID names.
type Model struct {
	// Family is opus, sonnet, haiku, fable, or mythos. It is empty for the
	// claude-3 generation.
	Family string
	Major  int
	// Minor is zero when the ID names none.
	Minor int
}

// Parse reads a Claude model ID. It reports false for an ID that names no
// Claude model.
func Parse(id string) (Model, bool) {
	if match := modelPattern.FindStringSubmatch(id); match != nil {
		major, _ := strconv.Atoi(match[2])
		minor, _ := strconv.Atoi(match[3])
		if minor >= dateSuffixMin {
			minor = 0
		}
		return Model{Family: match[1], Major: major, Minor: minor}, true
	}
	if match := legacyPattern.FindStringSubmatch(id); match != nil {
		major, _ := strconv.Atoi(match[1])
		return Model{Major: major}, true
	}
	return Model{}, false
}

// ForcedToolChoiceSupported reports whether a model accepts a forced tool
// choice while thinking is off. Models from Claude 5 on think by default or
// reject a forced choice outright. An unrecognized ID reports false, so the
// caller asks for auto, which every model accepts.
func ForcedToolChoiceSupported(id string) bool {
	model, ok := Parse(id)
	return ok && model.Family != "fable" && model.Family != "mythos" && model.Major < 5
}

// SamplingSupported reports whether a model accepts custom sampling parameters.
// Newer models use provider-controlled sampling even with thinking off.
// Unrecognized IDs report true to preserve sampling for proxy deployments.
func SamplingSupported(id string) bool {
	// Mythos Preview has no version segment for Parse to read.
	if strings.Contains(id, "claude-mythos-preview") {
		return false
	}
	model, ok := Parse(id)
	if !ok {
		return true
	}
	if model.Family == "fable" || model.Family == "mythos" {
		return false
	}
	// Opus dropped custom sampling at 4.7, ahead of the 5 generation.
	if model.Family == "opus" && model.Major == 4 && model.Minor >= 7 {
		return false
	}
	return model.Major < 5
}
