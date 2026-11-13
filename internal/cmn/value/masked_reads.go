// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// StepMasks names the outputs of a step that were restored with a secret
// masked. Their original values are gone, so a read gets starred text.
type StepMasks struct {
	// Output marks StepInfo.Output.
	Output bool
	// Outputs names the masked top-level fields of StepInfo.Outputs and
	// StepInfo.DeclaredOutputs.
	Outputs []string
	// AllOutputs marks every field of StepInfo.Outputs and
	// StepInfo.DeclaredOutputs.
	AllOutputs bool
}

// MaskedReadError reports a read of an output that was restored with a
// secret masked.
type MaskedReadError struct {
	// Step is the step that produced the output.
	Step string
	// Output names the output read.
	Output string
}

func (e *MaskedReadError) Error() string {
	return fmt.Sprintf(
		"reads output %q of step %q, which was stored masked because it holds a secret; step %q has to run again to produce it",
		e.Output, e.Step, e.Step,
	)
}

type maskedReadsKey struct{}

// maskedReads holds the first masked output read through a context.
type maskedReads struct {
	mu    sync.Mutex
	first *MaskedReadError
}

// TrackMaskedReads returns a context that records each read of a masked
// output made while resolving values with it. MaskedRead reports the first.
func TrackMaskedReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, maskedReadsKey{}, &maskedReads{})
}

// MaskedRead returns a *MaskedReadError for the first masked output read
// through ctx, or nil.
func MaskedRead(ctx context.Context) error {
	reads := maskedReadsFrom(ctx)
	if reads == nil {
		return nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if reads.first == nil {
		return nil
	}
	return reads.first
}

// AddMaskedRead records in ctx the masked read that err reports, if any.
func AddMaskedRead(ctx context.Context, err error) {
	var read *MaskedReadError
	if errors.As(err, &read) {
		maskedReadsFrom(ctx).note(read.Step, read.Output)
	}
}

func maskedReadsFrom(ctx context.Context) *maskedReads {
	reads, _ := ctx.Value(maskedReadsKey{}).(*maskedReads)
	return reads
}

// note records a read of the named output of step.
func (m *maskedReads) note(step, output string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.first == nil {
		m.first = &MaskedReadError{Step: step, Output: output}
	}
}

// noteEntry records a read of entry when it holds a masked output.
func (m *maskedReads) noteEntry(entry EnvEntry) {
	if entry.Masked {
		m.note(entry.Origin, entry.Key)
	}
}

// noteShellReads records the masked output variables that a shell reads from
// its environment when it expands text. A shell leaves a name in single
// quotes or after a backslash as written.
func noteShellReads(ctx context.Context, text string) {
	reads := maskedReadsFrom(ctx)
	scope := GetEnvScope(ctx)
	if reads == nil || scope == nil {
		return
	}
	for _, loc := range reVarSubstitution.FindAllStringSubmatchIndex(text, -1) {
		if isSingleQuotedVar(text, loc[0], loc[1]) || isEscapedDollar(text, loc[0]) {
			continue
		}
		var name string
		switch {
		case loc[2] >= 0:
			name = extractPOSIXVarName(text[loc[2]:loc[3]])
		case loc[4] >= 0:
			name = text[loc[4]:loc[5]]
		default:
			continue
		}
		if entry, ok := scope.GetEntry(name); ok {
			reads.noteEntry(entry)
		}
	}
}

// path returns the output that a step property path, such as ".output" or
// ".outputs.token", reads, and whether masking changed it.
func (s StepMasks) path(path string) (string, bool) {
	field, ok := stepTextPathField(path)
	switch {
	case !ok:
		return "", false
	case !strings.HasPrefix(path, ".outputs"):
		return "output", s.Output
	case field == "":
		return "outputs", s.AllOutputs || len(s.Outputs) > 0
	default:
		return field, s.field(field)
	}
}

// field reports whether masking changed the named published output.
func (s StepMasks) field(name string) bool {
	return s.AllOutputs || slices.Contains(s.Outputs, name)
}
