// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// NodeTargets is a step's nodes field (RFC 013): which inventory nodes the
// step runs on, and how it dispatches them.
type NodeTargets struct {
	// Selector holds label filter terms (Spec 087) joined by commas. It may
	// hold references, which resolve when the step starts.
	Selector string `json:"selector"`
	// MaxConcurrent is how many nodes run at once.
	MaxConcurrent int `json:"maxConcurrent"`
	// KeepGoing starts the remaining nodes after one fails.
	KeepGoing bool `json:"keepGoing,omitempty"`
}

const (
	// DefaultNodeMaxConcurrent runs one node at a time, so a command that
	// restarts a service never reaches every host at once by default.
	DefaultNodeMaxConcurrent = 1
	// MaxNodeMaxConcurrent is the largest max_concurrent a node step takes.
	MaxNodeMaxConcurrent = 1000
)
