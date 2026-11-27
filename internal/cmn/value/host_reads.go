// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"maps"
	"slices"
	"sync"
)

type hostReadsKey struct{}

// hostReads holds the process environment variables that resolution read
// through a context, each with the values read.
type hostReads struct {
	mu   sync.Mutex
	vars map[string][]string
}

// TrackHostReads returns a context that records each process environment
// variable that resolving values with it reads, directly or as an EnvScope
// entry taken from the process environment. HostReads returns them.
func TrackHostReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, hostReadsKey{}, &hostReads{vars: make(map[string][]string)})
}

// HostReads returns the process environment variables read through ctx, by
// name, each with the distinct values read. A JSON path into a variable
// reads its whole value. It returns nil when ctx tracks no reads.
func HostReads(ctx context.Context) map[string][]string {
	reads := hostReadsFrom(ctx)
	if reads == nil {
		return nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	out := maps.Clone(reads.vars)
	for name, values := range out {
		out[name] = slices.Clone(values)
	}
	return out
}

// hostReadsFrom returns the host reads that ctx tracks, or nil. A nil ctx,
// which ExpandEnvContext accepts, tracks none.
func hostReadsFrom(ctx context.Context) *hostReads {
	if ctx == nil {
		return nil
	}
	reads, _ := ctx.Value(hostReadsKey{}).(*hostReads)
	return reads
}

// lookup looks name up in the process environment as a workflow sees it,
// and records the variable it finds.
func (h *hostReads) lookup(name string) (string, bool) {
	value, ok := lookupProcessEnv(name)
	if ok {
		h.note(name, value)
	}
	return value, ok
}

// noteEntry records entry when it was taken from the process environment.
func (h *hostReads) noteEntry(entry EnvEntry) {
	if entry.Source == EnvSourceOS {
		h.note(entry.Key, entry.Value)
	}
}

// note records a read of value from the named variable.
func (h *hostReads) note(name, value string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(h.vars[name], value) {
		h.vars[name] = append(h.vars[name], value)
	}
}
