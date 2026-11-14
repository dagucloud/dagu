// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
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

// noteShellReads records the masked output variables that a POSIX shell
// reads from its environment when it runs text.
func noteShellReads(ctx context.Context, text string) {
	reads := maskedReadsFrom(ctx)
	scope := GetEnvScope(ctx)
	if reads == nil || !scope.hasMaskedOutput() || !shellText(text) {
		return
	}
	for _, name := range shellEnvReads(text) {
		if entry, ok := scope.GetEntry(name); ok {
			reads.noteEntry(entry)
		}
	}
}

// shellText reports whether a POSIX shell runs text. A script whose shebang
// names another interpreter, directly or through env, is not shell text.
func shellText(text string) bool {
	line, _, _ := strings.Cut(text, "\n")
	interpreter, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "#!")
	if !ok {
		return true
	}
	words := strings.Fields(interpreter)
	if len(words) > 0 && path.Base(words[0]) == "env" {
		words = slices.DeleteFunc(words[1:], func(word string) bool { return strings.HasPrefix(word, "-") })
	}
	return len(words) == 0 || cmdutil.IsUnixLikeShell(words[0]) || cmdutil.IsNixShell(words[0])
}

// shellEnvReads returns the variables a POSIX shell reads from its
// environment when it runs text: each one the script expands before it sets
// or unsets a value of its own. A value set in a subshell, in code that may
// not run, or as a function's local holds only there, and a function body
// counts where the function first runs. Text the parser rejects counts every
// name outside single quotes.
func shellEnvReads(text string) []string {
	file, err := syntax.NewParser().Parse(strings.NewReader(text), "")
	if err != nil {
		return unparsedShellReads(text)
	}
	scan := shellScan{calls: functionRuns(file, uint(len(text)))}
	root := scan.newScope(-1, scopeScript)
	for _, stmt := range file.Stmts {
		scan.walk(stmt, root, nil)
	}
	var names []string
	for _, r := range scan.reads {
		if !scan.setBefore(r) {
			names = append(names, r.name)
		}
	}
	return names
}

// shellPos orders the points where a script runs: the offsets of the calls
// that first run the enclosing functions, outermost first, then the offset
// itself.
type shellPos []uint

// before reports whether the script reaches p before q. A call comes before
// the body it runs.
func (p shellPos) before(q shellPos) bool {
	for i := range min(len(p), len(q)) {
		if p[i] != q[i] {
			return p[i] < q[i]
		}
	}
	return len(p) < len(q)
}

type scopeKind int

const (
	scopeScript scopeKind = iota
	// scopeContained is a subshell or code that may not run: values set in
	// it hold only there.
	scopeContained
	// scopeFunction is a function body: only its local values hold only
	// there.
	scopeFunction
)

type shellScope struct {
	parent int
	kind   scopeKind
}

// shellVar is a variable read or set at a point of a scope.
type shellVar struct {
	name  string
	at    shellPos
	scope int
}

type shellScan struct {
	// calls holds how each function first runs.
	calls  map[string]functionRun
	scopes []shellScope
	reads  []shellVar
	sets   []shellVar
}

func (s *shellScan) newScope(parent int, kind scopeKind) int {
	s.scopes = append(s.scopes, shellScope{parent: parent, kind: kind})
	return len(s.scopes) - 1
}

// setBefore reports whether the script sets r's variable, where r's scope
// sees it, before r reads it.
func (s *shellScan) setBefore(r shellVar) bool {
	for _, set := range s.sets {
		if set.name == r.name && !r.at.before(set.at) && s.encloses(set.scope, r.scope) {
			return true
		}
	}
	return false
}

// encloses reports whether scope outer is inner or one of its parents.
func (s *shellScan) encloses(outer, inner int) bool {
	for scope := inner; scope >= 0; scope = s.scopes[scope].parent {
		if scope == outer {
			return true
		}
	}
	return false
}

// globalScope returns the scope a plain assignment in scope sets: functions
// share the variables of the code that calls them.
func (s *shellScan) globalScope(scope int) int {
	for s.scopes[scope].kind == scopeFunction {
		scope = s.scopes[scope].parent
	}
	return scope
}

// inFunction reports whether scope is in a function body.
func (s *shellScan) inFunction(scope int) bool {
	return s.scopes[s.localScope(scope)].kind == scopeFunction
}

// localScope returns the scope a local declaration in scope sets: the
// enclosing function body, or the scope itself outside a function.
func (s *shellScan) localScope(scope int) int {
	for at := scope; at >= 0; at = s.scopes[at].parent {
		if s.scopes[at].kind == scopeFunction {
			return at
		}
	}
	return scope
}

// walk scans node, which runs in scope; call is the point where the
// enclosing function first runs, or nil outside a function.
func (s *shellScan) walk(node syntax.Node, scope int, call shellPos) {
	at := func(pos syntax.Pos) shellPos {
		return append(slices.Clip(call), pos.Offset())
	}
	read := func(name string, pos syntax.Pos) {
		s.reads = append(s.reads, shellVar{name: name, at: at(pos), scope: scope})
	}
	set := func(name string, pos syntax.Pos, target int) {
		s.sets = append(s.sets, shellVar{name: name, at: at(pos), scope: target})
	}
	nested := func(stmts []*syntax.Stmt, kind scopeKind) {
		inner := s.newScope(scope, kind)
		for _, stmt := range stmts {
			s.walk(stmt, inner, call)
		}
	}
	syntax.Walk(node, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl:
			run := s.calls[n.Name.Value]
			parent := scope
			if !run.sure {
				// The values a body sets hold after its call only when the
				// call surely runs it in this shell.
				parent = s.newScope(scope, scopeContained)
			}
			s.walk(n.Body, s.newScope(parent, scopeFunction), run.at)
			return false
		case *syntax.Stmt:
			// A background job or a coprocess runs in a subshell.
			if n.Background || n.Coprocess {
				inner := s.newScope(scope, scopeContained)
				for _, redirect := range n.Redirs {
					s.walk(redirect, inner, call)
				}
				if n.Cmd != nil {
					s.walk(n.Cmd, inner, call)
				}
				return false
			}
		case *syntax.CoprocClause:
			nested([]*syntax.Stmt{n.Stmt}, scopeContained)
			return false
		case *syntax.Subshell:
			nested(n.Stmts, scopeContained)
			return false
		case *syntax.CmdSubst:
			nested(n.Stmts, scopeContained)
			return false
		case *syntax.ProcSubst:
			nested(n.Stmts, scopeContained)
			return false
		case *syntax.IfClause:
			// A variable every branch sets holds after the clause.
			for name := range s.walkIf(n, scope, call) {
				set(name, n.End(), s.globalScope(scope))
			}
			return false
		case *syntax.WhileClause:
			for _, stmt := range n.Cond {
				s.walk(stmt, scope, call)
			}
			nested(n.Do, scopeContained)
			return false
		case *syntax.ForClause:
			body := s.newScope(scope, scopeContained)
			if iter, ok := n.Loop.(*syntax.WordIter); ok {
				for _, item := range iter.Items {
					s.walk(item, scope, call)
				}
				// The loop sets its variable only when it runs at least once.
				target := body
				if surelyIterates(iter) {
					target = s.globalScope(scope)
				}
				set(iter.Name.Value, iter.End(), target)
			} else {
				s.walk(n.Loop, scope, call)
			}
			for _, stmt := range n.Do {
				s.walk(stmt, body, call)
			}
			return false
		case *syntax.CaseClause:
			s.walk(n.Word, scope, call)
			for _, item := range n.Items {
				for _, pattern := range item.Patterns {
					s.walk(pattern, scope, call)
				}
				nested(item.Stmts, scopeContained)
			}
			return false
		case *syntax.BinaryCmd:
			// Each side of a pipeline runs in a subshell; the right side of
			// && and || may not run.
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				nested([]*syntax.Stmt{n.X}, scopeContained)
			} else {
				s.walk(n.X, scope, call)
			}
			nested([]*syntax.Stmt{n.Y}, scopeContained)
			return false
		case *syntax.ParamExp:
			if n.Param != nil {
				read(n.Param.Value, n.Pos())
			}
			// An index or a slice is arithmetic, which names variables
			// without a dollar.
			if n.Index != nil {
				arithmReads(n.Index, read)
			}
			if n.Slice != nil {
				for _, x := range []syntax.ArithmExpr{n.Slice.Offset, n.Slice.Length} {
					if x != nil {
						arithmReads(x, read)
					}
				}
			}
		case *syntax.CStyleLoop:
			for _, x := range []syntax.ArithmExpr{n.Init, n.Cond, n.Post} {
				if x != nil {
					arithmReads(x, read)
				}
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
						set(a.Name.Value, a.End(), s.globalScope(scope))
					}
				}
				break
			}
			for _, name := range assignedNames(n.Args) {
				set(name, n.End(), s.globalScope(scope))
			}
		case *syntax.DeclClause:
			// declare and typeset in a function declare locals. Without a
			// value, a declaration keeps the inherited value, as dash does
			// even for a local.
			variant := n.Variant.Value
			if variant == "local" && !s.inFunction(scope) {
				// bash rejects local outside a function.
				break
			}
			local := variant == "local" || ((variant == "declare" || variant == "typeset") && s.inFunction(scope))
			target := s.globalScope(scope)
			if local {
				target = s.localScope(scope)
			}
			// A name reference reads the variable it names wherever it is
			// used, so naming one counts as reading it.
			nameref := variant == "nameref" || slices.ContainsFunc(n.Args, isNamerefFlag)
			for _, a := range n.Args {
				if a.Name == nil || a.Naked {
					continue
				}
				if nameref && a.Value != nil && ValidEnvName(a.Value.Lit()) {
					read(a.Value.Lit(), a.Pos())
				}
				if a.Append {
					read(a.Name.Value, a.Pos())
				}
				set(a.Name.Value, a.End(), target)
			}
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

// walkIf scans an if clause that runs in scope and returns the variables
// that every one of its branches sets.
func (s *shellScan) walkIf(n *syntax.IfClause, scope int, call shellPos) map[string]bool {
	for _, stmt := range n.Cond {
		s.walk(stmt, scope, call)
	}
	branch := s.newScope(scope, scopeContained)
	for _, stmt := range n.Then {
		s.walk(stmt, branch, call)
	}
	if n.Else == nil {
		return nil
	}
	rest := s.newScope(scope, scopeContained)
	var others map[string]bool
	if len(n.Else.Cond) == 0 {
		for _, stmt := range n.Else.Then {
			s.walk(stmt, rest, call)
		}
		others = s.setIn(rest)
	} else {
		others = s.walkIf(n.Else, rest, call)
	}
	all := make(map[string]bool)
	for name := range s.setIn(branch) {
		if others[name] {
			all[name] = true
		}
	}
	return all
}

// setIn returns the variables set directly in scope.
func (s *shellScan) setIn(scope int) map[string]bool {
	names := make(map[string]bool)
	for _, set := range s.sets {
		if set.scope == scope {
			names[set.name] = true
		}
	}
	return names
}

// isNamerefFlag reports whether a declaration argument is an option that
// includes -n.
func isNamerefFlag(a *syntax.Assign) bool {
	if a.Name != nil || a.Value == nil {
		return false
	}
	flag := a.Value.Lit()
	return strings.HasPrefix(flag, "-") && strings.Contains(flag, "n")
}

// assignedNames returns the variables a builtin call sets or unsets: read,
// mapfile, readarray, getopts, printf -v, and unset.
func assignedNames(args []*syntax.Word) []string {
	var names []string
	switch args[0].Lit() {
	case "read", "unset", "mapfile", "readarray":
		for _, arg := range args[1:] {
			if name := arg.Lit(); ValidEnvName(name) {
				names = append(names, name)
			}
		}
	case "getopts":
		if len(args) > 2 && ValidEnvName(args[2].Lit()) {
			names = append(names, args[2].Lit())
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

// surelyIterates reports whether a for loop runs at least once: its list is
// plain words, so it does not expand to nothing.
func surelyIterates(iter *syntax.WordIter) bool {
	if !iter.InPos.IsValid() || len(iter.Items) == 0 {
		return false
	}
	for _, item := range iter.Items {
		if item.Lit() == "" {
			return false
		}
	}
	return true
}

// functionRun is how a function first runs.
type functionRun struct {
	// at is the point of its first run.
	at shellPos
	// sure is set when that run surely happens in the script's own shell,
	// so the values the body sets hold after it.
	sure bool
}

// functionRuns returns how each function text defines first runs: its first
// call outside a function, or its call in the body of a function, placed
// where that function first runs. A function no call names, such as one a
// trap runs, counts as running at the end of the script, which is end bytes
// long.
func functionRuns(file *syntax.File, end uint) map[string]functionRun {
	type callSite struct {
		// caller is the function whose body holds the call, or empty.
		caller string
		offset uint
		// sure is set when the call is not in a subshell or in code that
		// may not run.
		sure bool
	}
	// functions counts the definitions of each function. Which definition a
	// call runs depends on the order they run in, so a redefined function's
	// values are not sure to hold after its call.
	functions := make(map[string]int)
	sites := make(map[string][]callSite)
	var visit func(node syntax.Node, caller string, sure bool)
	visitAll := func(stmts []*syntax.Stmt, caller string, sure bool) {
		for _, stmt := range stmts {
			visit(stmt, caller, sure)
		}
	}
	visit = func(node syntax.Node, caller string, sure bool) {
		syntax.Walk(node, func(node syntax.Node) bool {
			switch n := node.(type) {
			case *syntax.FuncDecl:
				functions[n.Name.Value]++
				visit(n.Body, n.Name.Value, true)
				return false
			case *syntax.Stmt:
				if n.Background || n.Coprocess {
					for _, redirect := range n.Redirs {
						visit(redirect, caller, false)
					}
					if n.Cmd != nil {
						visit(n.Cmd, caller, false)
					}
					return false
				}
			case *syntax.CoprocClause:
				visit(n.Stmt, caller, false)
				return false
			case *syntax.CallExpr:
				if len(n.Args) > 0 {
					if name := n.Args[0].Lit(); name != "" {
						sites[name] = append(sites[name], callSite{caller: caller, offset: n.Pos().Offset(), sure: sure})
					}
				}
			case *syntax.Subshell:
				visitAll(n.Stmts, caller, false)
				return false
			case *syntax.CmdSubst:
				visitAll(n.Stmts, caller, false)
				return false
			case *syntax.ProcSubst:
				visitAll(n.Stmts, caller, false)
				return false
			case *syntax.IfClause:
				visitAll(n.Cond, caller, sure)
				visitAll(n.Then, caller, false)
				if n.Else != nil {
					visit(n.Else, caller, false)
				}
				return false
			case *syntax.WhileClause:
				visitAll(n.Cond, caller, sure)
				visitAll(n.Do, caller, false)
				return false
			case *syntax.ForClause:
				visit(n.Loop, caller, sure)
				visitAll(n.Do, caller, false)
				return false
			case *syntax.CaseClause:
				visit(n.Word, caller, sure)
				for _, item := range n.Items {
					visitAll(item.Stmts, caller, false)
				}
				return false
			case *syntax.BinaryCmd:
				pipe := n.Op == syntax.Pipe || n.Op == syntax.PipeAll
				visit(n.X, caller, sure && !pipe)
				visit(n.Y, caller, false)
				return false
			}
			return true
		})
	}
	visit(file, "", true)
	runs := make(map[string]functionRun)
	// A call in a function body runs when that function first runs, so the
	// first runs settle over passes. A recursive call never comes first.
	for changed := true; changed; {
		changed = false
		for name := range functions {
			for _, site := range sites[name] {
				caller := functionRun{sure: true}
				if site.caller != "" {
					first, ok := runs[site.caller]
					if !ok {
						continue
					}
					caller = first
				}
				at := append(slices.Clip(caller.at), site.offset)
				if prev, ok := runs[name]; !ok || at.before(prev.at) {
					runs[name] = functionRun{at: at, sure: site.sure && caller.sure && functions[name] == 1}
					changed = true
				}
			}
		}
	}
	for name := range functions {
		if _, ok := runs[name]; !ok {
			runs[name] = functionRun{at: shellPos{end}}
		}
	}
	return runs
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
