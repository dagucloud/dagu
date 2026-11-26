// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// IsUntrusted reports whether the step runs as an untrusted step.
func (s Step) IsUntrusted() bool {
	return s.Untrusted
}

// HasUntrustedStep reports whether a step, a handler or a step in the body of
// a foreach step of the DAG is untrusted.
func (d *DAG) HasUntrustedStep() bool {
	if d == nil {
		return false
	}
	var untrusted func(steps []Step) bool
	untrusted = func(steps []Step) bool {
		for i := range steps {
			if steps[i].IsUntrusted() || steps[i].Foreach != nil && untrusted(steps[i].Foreach.Steps) {
				return true
			}
		}
		return false
	}
	if untrusted(d.Steps) {
		return true
	}
	for _, handler := range []*Step{d.HandlerOn.Init, d.HandlerOn.Failure, d.HandlerOn.Success, d.HandlerOn.Abort, d.HandlerOn.Exit, d.HandlerOn.Wait} {
		if handler != nil && untrusted([]Step{*handler}) {
			return true
		}
	}
	return false
}
