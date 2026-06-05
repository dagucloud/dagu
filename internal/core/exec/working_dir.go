// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

// WorkingDirOrigin identifies the source of a resolved step working directory.
type WorkingDirOrigin string

const (
	WorkingDirOriginStepExplicit    WorkingDirOrigin = "step_explicit"
	WorkingDirOriginDAGExplicit     WorkingDirOrigin = "dag_explicit"
	WorkingDirOriginRunWorkDir      WorkingDirOrigin = "run_work_dir"
	WorkingDirOriginLoaderFallback  WorkingDirOrigin = "loader_fallback"
	WorkingDirOriginProcessFallback WorkingDirOrigin = "process_fallback"
	WorkingDirOriginUnknown         WorkingDirOrigin = "unknown"
)

// WorkingDirSnapshot records the resolved working directory and its source.
type WorkingDirSnapshot struct {
	Origin    WorkingDirOrigin `json:"origin,omitempty"`
	Raw       string           `json:"raw,omitempty"`
	Evaluated string           `json:"evaluated,omitempty"`
	Base      string           `json:"base,omitempty"`
}

func (s WorkingDirSnapshot) IsZero() bool {
	return s.Origin == "" && s.Raw == "" && s.Evaluated == "" && s.Base == ""
}
