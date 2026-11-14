// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// StepMasks marks the outputs of a step that were restored with a secret
// masked. Their original values are gone, so a read gets starred text.
type StepMasks struct {
	// Output marks StepInfo.Output.
	Output bool
	// Outputs marks StepInfo.Outputs and StepInfo.DeclaredOutputs.
	Outputs bool
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
		"reads output %q of step %q, which was stored masked because it holds a secret; step %q has to run again, together with the steps after it, to produce it",
		e.Output, e.Step, e.Step,
	)
}

type maskedReadsKey struct{}

// maskedReads holds the first masked output read through a context.
type maskedReads struct {
	mu    sync.Mutex
	first *MaskedReadError
	// reject makes resolution fail at a masked read.
	reject bool
}

// TrackMaskedReads returns a context that records each read of a masked
// output made while resolving values with it. MaskedRead reports the first.
func TrackMaskedReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, maskedReadsKey{}, &maskedReads{})
}

// UntrackMaskedReads returns ctx without the masked-read tracking it carries,
// for a run that starts inside a step, whose reads are its own.
func UntrackMaskedReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, maskedReadsKey{}, (*maskedReads)(nil))
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

// RejectMaskedReads makes resolution with ctx fail at a masked read from now
// on. A step sets it once it starts, so that an executor resolving a field
// while it runs stops before it uses the value.
func RejectMaskedReads(ctx context.Context) {
	if reads := maskedReadsFrom(ctx); reads != nil {
		reads.mu.Lock()
		defer reads.mu.Unlock()
		reads.reject = true
	}
}

// rejected returns the masked read that resolution fails at, or nil.
func (m *maskedReads) rejected() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.reject || m.first == nil {
		return nil
	}
	return m.first
}

// AddMaskedRead records in ctx the masked read that err reports, if any.
func AddMaskedRead(ctx context.Context, err error) {
	if read, ok := errors.AsType[*MaskedReadError](err); ok {
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

// maskedText returns the stored text that a step property path, such as
// ".output" or ".outputs.token", reads, the output to name for it, and
// whether a restore brought that text back masked.
func (s StepInfo) maskedText(path string) (text, output string, masked bool) {
	field, ok := stepTextPathField(path)
	switch {
	case !ok:
		return "", "", false
	case strings.HasPrefix(path, ".outputs"):
		if !s.Masked.Outputs || s.Outputs == nil {
			return "", "", false
		}
		text, output = *s.Outputs, "outputs"
	default:
		if !s.Masked.Output || s.Output == nil {
			return "", "", false
		}
		text, output = *s.Output, "output"
	}
	if field = strings.Trim(field, `"'?`); field != "" {
		output = field
	}
	return text, output, true
}

// readsMask reports whether resolving a reference into masked text read the
// mask: the value it resolved holds it, or the reference failed on text that
// masking left no longer JSON.
func readsMask(text, value string, ok bool) bool {
	if !strings.Contains(text, masking.DefaultMaskString) {
		return false
	}
	return strings.Contains(value, masking.DefaultMaskString) || (!ok && !json.Valid([]byte(text)))
}
