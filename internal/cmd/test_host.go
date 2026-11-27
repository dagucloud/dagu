// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
)

// hostValues holds the values that a case's run took from the host
// environment (Spec 101, Host Values): those of the variables that
// resolving the DAG and its steps read from the host, and of those that
// Dagu forwards to steps, except the variables it forwards by default.
// --update writes none of them, and the report masks them.
type hostValues struct {
	// vars holds each host variable, sorted by name.
	vars []hostVar
	// values holds the values of every host variable.
	values []string
}

// hostVar is a host variable with a masker of its values.
type hostVar struct {
	name   string
	masker *masking.Masker
}

// newHostValues returns the host values of a run whose reads ctx tracks
// (cmnvalue.TrackHostReads) and whose steps start from base.
func newHostValues(ctx context.Context, base config.BaseEnv) hostValues {
	read := cmnvalue.HostReads(ctx)
	if read == nil {
		read = make(map[string][]string)
	}
	for _, entry := range base.WithoutCredentials() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			read[name] = append(read[name], value)
		}
	}
	var h hostValues
	for _, name := range slices.Sorted(maps.Keys(read)) {
		values := slices.DeleteFunc(read[name], func(v string) bool { return v == "" })
		if len(values) == 0 || config.ForwardedByDefault(name) {
			continue
		}
		h.vars = append(h.vars, hostVar{name: name, masker: masking.NewValueMasker(values)})
		h.values = append(h.values, values...)
	}
	return h
}

// held returns, sorted, the names of the host variables whose values text
// holds, as written or inside a JSON string.
func (h *hostValues) held(text string) []string {
	var names []string
	for _, v := range h.vars {
		if v.masker.MaskString(text) != text {
			names = append(names, v.name)
		}
	}
	return names
}

// hostGuard keeps the run values that hold a host value out of the
// assertions --update rewrites on one step, and records each assertion
// it keeps as written.
type hostGuard struct {
	host *hostValues
	// step is the step key as expect.steps writes it.
	step string
	// call is the step's last call, whose env names what a case can set.
	call stepmock.Call
	kept []keptValue
}

// value returns updated, the run's value of the assertion of field, or
// written when updated differs from it and holds a host value.
func (g *hostGuard) value(field string, written, updated any) any {
	if reflect.DeepEqual(written, updated) {
		return updated
	}
	if vars := g.host.held(outputText(updated)); len(vars) > 0 {
		g.keep(field, vars)
		return written
	}
	return updated
}

// variable returns updated, the call's value of the named variable, or
// written when updated differs from it and is the host's or holds a host
// value.
func (g *hostGuard) variable(name, written, updated string) string {
	if written == updated {
		return updated
	}
	vars := g.host.held(updated)
	if slices.Contains(g.call.Host, name) && !slices.Contains(vars, name) {
		vars = append(vars, name)
		slices.Sort(vars)
	}
	if len(vars) == 0 {
		return updated
	}
	g.keep(failureCallEnvPrefix+name, vars)
	return written
}

// keep records that the assertion of field stays as written because the
// run's value holds the values of the host variables vars. The variables
// of the call that hold them are the ones the case can set under env.
func (g *hostGuard) keep(field string, vars []string) {
	k := keptValue{step: g.step, field: field, vars: vars}
	for _, name := range slices.Sorted(maps.Keys(g.call.Env)) {
		forwarded := slices.Contains(vars, name) && slices.Contains(g.call.Host, name)
		if forwarded || slices.ContainsFunc(g.host.held(g.call.Env[name]), func(v string) bool { return slices.Contains(vars, v) }) {
			k.env = append(k.env, name)
		}
	}
	g.kept = append(g.kept, k)
}

// keptValue is an assertion that --update keeps as written because the
// run's value holds a host value.
type keptValue struct {
	// step and field name the assertion as its failure does.
	step  string
	field string
	// vars names, sorted, the host variables whose values the run's value
	// holds.
	vars []string
	// env names, sorted, the variables of the step's call that hold them.
	env []string
}

// String writes the kept assertion as a line of the text report.
func (k keptValue) String() string {
	label := k.field
	if name, ok := strings.CutPrefix(k.field, failureOutputPrefix); ok {
		label = "output " + name
	}
	line := fmt.Sprintf("step %s %s: kept, it holds the %s of %s %s", k.step, label,
		plural(len(k.vars), "value", "values"), plural(len(k.vars), "host variable", "host variables"), strings.Join(k.vars, ", "))
	if len(k.env) > 0 {
		line += "; set " + strings.Join(k.env, ", ") + " under env"
	}
	return line
}
