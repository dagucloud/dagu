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

	"mvdan.cc/sh/v3/syntax"
)

// StepMasks names the outputs of a step that were restored with a secret
// masked. Their original values are gone, so a read gets starred text.
type StepMasks struct {
	// Output names the masked top-level fields of StepInfo.Output, a JSON
	// object.
	Output []string
	// AllOutput marks all of StepInfo.Output.
	AllOutput bool
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
	// reject makes resolution fail at a masked read.
	reject bool
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

// noteShellReads records the masked output variables that a POSIX shell
// reads from its environment when it runs text.
func noteShellReads(ctx context.Context, text string) {
	reads := maskedReadsFrom(ctx)
	scope := GetEnvScope(ctx)
	if reads == nil || scope == nil {
		return
	}
	for _, name := range shellEnvReads(text) {
		if entry, ok := scope.GetEntry(name); ok {
			reads.noteEntry(entry)
		}
	}
}

// shellEnvReads returns the variables a POSIX shell reads from its
// environment when it runs text: each one the script expands before it sets
// or unsets a value of its own. A function body counts where the function is
// first called. Text the parser rejects counts every name outside single
// quotes.
func shellEnvReads(text string) []string {
	file, err := syntax.NewParser().Parse(strings.NewReader(text), "")
	if err != nil {
		return unparsedShellReads(text)
	}
	scan := shellScan{calls: firstCalls(file), set: make(map[string]shellPos)}
	scan.walk(file, nil)
	var names []string
	for _, r := range scan.reads {
		if at, ok := scan.set[r.name]; ok && !r.at.before(at) {
			continue
		}
		names = append(names, r.name)
	}
	return names
}

// shellPos orders the points where a script runs: call is where the
// enclosing function is called, or the offset itself outside a function.
type shellPos struct {
	call, offset uint
}

func (p shellPos) before(q shellPos) bool {
	return p.call < q.call || (p.call == q.call && p.offset < q.offset)
}

type shellScan struct {
	// calls holds the offset where each function is first called.
	calls map[string]uint
	reads []shellRead
	// set holds, for each variable the script sets, the earliest point
	// where its own value takes effect.
	set map[string]shellPos
}

type shellRead struct {
	name string
	at   shellPos
}

// walk scans node; call is the offset of the call that runs it, or nil.
func (s *shellScan) walk(node syntax.Node, call *uint) {
	at := func(pos syntax.Pos) shellPos {
		if call != nil {
			return shellPos{call: *call, offset: pos.Offset()}
		}
		return shellPos{call: pos.Offset(), offset: pos.Offset()}
	}
	read := func(name string, pos syntax.Pos) {
		s.reads = append(s.reads, shellRead{name: name, at: at(pos)})
	}
	assign := func(name string, pos syntax.Pos) {
		if prev, ok := s.set[name]; !ok || at(pos).before(prev) {
			s.set[name] = at(pos)
		}
	}
	syntax.Walk(node, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			if first, ok := s.calls[n.Name.Value]; ok {
				s.walk(n.Body, &first)
			}
			return false
		case *syntax.ParamExp:
			if n.Param != nil {
				read(n.Param.Value, n.Pos())
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				// NAME=value without a command sets a shell variable;
				// before a command it is only that command's environment.
				for _, a := range n.Assigns {
					if a.Name != nil {
						if a.Append {
							read(a.Name.Value, a.Pos())
						}
						assign(a.Name.Value, a.End())
					}
				}
				break
			}
			for _, name := range assignedNames(n.Args) {
				assign(name, n.End())
			}
		case *syntax.DeclClause:
			for _, a := range n.Args {
				if a.Name == nil || (a.Naked && (n.Variant.Value == "export" || n.Variant.Value == "readonly")) {
					continue
				}
				if a.Append {
					read(a.Name.Value, a.Pos())
				}
				assign(a.Name.Value, a.End())
			}
		case *syntax.WordIter:
			assign(n.Name.Value, n.End())
		case *syntax.ArithmExp:
			arithmReads(n.X, read)
		case *syntax.ArithmCmd:
			arithmReads(n.X, read)
		case *syntax.LetClause:
			for _, x := range n.Exprs {
				arithmReads(x, read)
			}
		}
		return true
	})
}

// assignedNames returns the variables a builtin call sets or unsets: read,
// printf -v, and unset.
func assignedNames(args []*syntax.Word) []string {
	var names []string
	switch args[0].Lit() {
	case "read", "unset":
		for _, arg := range args[1:] {
			if name := arg.Lit(); ValidEnvName(name) {
				names = append(names, name)
			}
		}
	case "printf":
		for i := 1; i+1 < len(args); i++ {
			if args[i].Lit() == "-v" && ValidEnvName(args[i+1].Lit()) {
				names = append(names, args[i+1].Lit())
			}
		}
	}
	return names
}

// firstCalls returns the offset where each function text defines is first
// called.
func firstCalls(file *syntax.File) map[string]uint {
	functions := make(map[string]bool)
	syntax.Walk(file, func(node syntax.Node) bool {
		if fn, ok := node.(*syntax.FuncDecl); ok {
			functions[fn.Name.Value] = true
		}
		return true
	})
	calls := make(map[string]uint)
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		name := call.Args[0].Lit()
		if prev, seen := calls[name]; functions[name] && (!seen || call.Pos().Offset() < prev) {
			calls[name] = call.Pos().Offset()
		}
		return true
	})
	return calls
}

// arithmReads reports each variable that an arithmetic expression names
// without a dollar.
func arithmReads(x syntax.ArithmExpr, read func(string, syntax.Pos)) {
	syntax.Walk(x, func(node syntax.Node) bool {
		word, ok := node.(*syntax.Word)
		if !ok {
			return true
		}
		if name := word.Lit(); ValidEnvName(name) {
			read(name, word.Pos())
		}
		return false
	})
}

// unparsedShellReads returns every variable text names outside single quotes
// and after no backslash.
func unparsedShellReads(text string) []string {
	var names []string
	for _, loc := range reVarSubstitution.FindAllStringSubmatchIndex(text, -1) {
		if isSingleQuotedVar(text, loc[0], loc[1]) || isEscapedDollar(text, loc[0]) {
			continue
		}
		switch {
		case loc[2] >= 0:
			names = append(names, extractPOSIXVarName(text[loc[2]:loc[3]]))
		case loc[4] >= 0:
			names = append(names, text[loc[4]:loc[5]])
		}
	}
	return names
}

// path returns the output that a step property path, such as ".output" or
// ".outputs.token", reads, and whether masking changed it.
func (s StepMasks) path(path string) (string, bool) {
	field, ok := stepTextPathField(path)
	switch {
	case !ok:
		return "", false
	case !strings.HasPrefix(path, ".outputs"):
		if field == "" {
			return "output", s.AllOutput || len(s.Output) > 0
		}
		return field, s.AllOutput || slices.Contains(s.Output, field)
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
