// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

type Values map[string]any
type StepOutputs map[string]Values
type StepOutputNames map[string]struct{}
type StepOutputContracts map[string]StepOutputNames

// StaticScope contains declarations and contracts used by static validation.
type StaticScope struct {
	Consts Values
	Params Values
	Env    Values
	Steps  StepOutputContracts
}

// RuntimeScope contains actual values available during runtime resolution.
type RuntimeScope struct {
	Consts  Values
	Params  Values
	Env     Values
	Steps   StepOutputs
	StepMap map[string]StepInfo
}

// ValuesFromStrings converts string variables into binding values.
func ValuesFromStrings(values map[string]string) Values {
	if len(values) == 0 {
		return nil
	}
	out := make(Values, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
