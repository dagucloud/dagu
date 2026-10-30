// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/cmn/yamlutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/go-viper/mapstructure/v2"
	"github.com/goccy/go-yaml"
)

// positionPrefix matches the "[line:column]" prefix on the first line of a load error.
var positionPrefix = regexp.MustCompile(`^\[(\d+):(\d+)\]`)

// buildValidationIssues converts the results of one spec load into structured issues.
// An error issue keeps the text of the matching errors entry. The order is: load errors,
// build errors, warnings, notices.
func buildValidationIssues(loadErr error, dag *ir.DAG, notices []cmnvalue.ValueReferenceNotice) []api.ValidationIssue {
	issues := []api.ValidationIssue{}
	scope := newIssueScope(dag)

	if loadErr != nil {
		if list, ok := errors.AsType[ir.ErrorList](loadErr); ok {
			for _, e := range list {
				issues = append(issues, newErrorIssue(api.ValidationIssueSourceLoad, e, scope))
			}
		} else {
			issues = append(issues, newErrorIssue(api.ValidationIssueSourceLoad, loadErr, scope))
		}
	}
	if dag != nil {
		for _, e := range dag.BuildErrors {
			issues = append(issues, newErrorIssue(api.ValidationIssueSourceBuild, positionFieldError(e, scope.yaml), scope))
		}
		for _, w := range dag.BuildWarnings {
			issues = append(issues, api.ValidationIssue{
				Severity: api.ValidationIssueSeverityWarning,
				Source:   api.ValidationIssueSourceWarning,
				Message:  w,
			})
		}
	}
	for _, n := range notices {
		issue := api.ValidationIssue{
			Severity: api.ValidationIssueSeverityNotice,
			Source:   api.ValidationIssueSourceValueReference,
			Message:  n.Message,
		}
		if n.FieldPath != "" {
			issue.FieldPath = ptrOf(n.FieldPath)
			if prefix := stepPathPrefix.FindString(n.FieldPath); prefix != "" {
				issue.StepPath = ptrOf(prefix)
				if i := slices.IndexFunc(scope.index, func(e stepEntry) bool { return e.path == prefix }); i >= 0 {
					issue.StepName = ptrOf(scope.index[i].name)
				}
			}
		}
		if n.Reason != "" {
			issue.Reason = ptrOf(string(n.Reason))
		}
		issues = append(issues, issue)
	}
	return issues
}

// errorPosition returns the 1-based position of an error in the YAML. A YAML
// parser error gives its token. Other errors give the "[line:column]" prefix
// of the message when they have one.
func errorPosition(err error) (line, column int, ok bool) {
	if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
		if tk := yamlErr.GetToken(); tk != nil && tk.Position != nil && tk.Position.Line > 0 {
			return tk.Position.Line, max(tk.Position.Column, 1), true
		}
	}
	if m := positionPrefix.FindStringSubmatch(err.Error()); m != nil {
		line, _ := strconv.Atoi(m[1])
		column, _ := strconv.Atoi(m[2])
		return line, max(column, 1), true
	}
	return 0, 0, false
}

// positionFieldError prefixes an error about one field of the document,
// such as a schedule's calendar, with the [line:column] of the field, as the
// errors list of a response does.
func positionFieldError(err error, source []byte) error {
	if _, ok := errors.AsType[*spec.FieldError](err); !ok || len(source) == 0 {
		return err
	}
	return &positionedError{message: spec.ErrorMessages(source, []error{err})[0], err: err}
}

// positionedError is an error whose text starts with its position.
type positionedError struct {
	message string
	err     error
}

func (e *positionedError) Error() string { return e.message }

func (e *positionedError) Unwrap() error { return e.err }

func newErrorIssue(source api.ValidationIssueSource, err error, scope *issueScope) api.ValidationIssue {
	issue := api.ValidationIssue{
		Severity: api.ValidationIssueSeverityError,
		Source:   source,
		Message:  err.Error(),
	}
	if line, column, ok := errorPosition(err); ok {
		issue.Line = &line
		issue.Column = &column
	}
	attr := scope.attribute(err)
	if attr.stepName != "" {
		issue.StepName = ptrOf(attr.stepName)
	}
	if attr.stepPath != "" {
		issue.StepPath = ptrOf(attr.stepPath)
	}
	if attr.fieldPath != "" {
		issue.FieldPath = ptrOf(attr.fieldPath)
	}
	return issue
}

// attribution is the location that one error points to.
type attribution struct {
	stepName  string
	stepPath  string
	fieldPath string
}

// issueScope holds what the attribution of the errors of one DAG shares.
type issueScope struct {
	index stepIndex
	yaml  []byte
	raw   *rawDoc
	// rawLoaded is true when raw holds the result of the YAML read.
	rawLoaded bool
}

func newIssueScope(dag *ir.DAG) *issueScope {
	scope := &issueScope{index: newStepIndex(dag)}
	if dag != nil {
		scope.yaml = dag.YamlData
	}
	return scope
}

// rawDoc returns the raw main document, or nil when the DAG has none. The
// YAML is read once, when the first error needs it.
func (s *issueScope) rawDoc() *rawDoc {
	if !s.rawLoaded {
		s.rawLoaded = true
		s.raw = parseRawDoc(s.yaml)
	}
	return s.raw
}

// stepLocators find the step of an error, from the most to the least reliable.
var stepLocators = []func(errorPath, stepIndex) (stepEntry, bool){
	stepFromValue,
	func(p errorPath, _ stepIndex) (stepEntry, bool) { return handlerFromDecodeName(p) },
	stepFromFieldPrefix,
	stepFromQuotedText,
	stepFromOwnerText,
}

// attribute finds the step and the field that err is about. It tries each path
// of the error tree in order and uses the first path that gives a step. When
// the DAG has no such step, because the build stopped at it, the raw YAML
// gives the step. A failed lookup gives the zero value.
func (s *issueScope) attribute(err error) attribution {
	paths := errorPaths(err)
	var partial *attribution
	for _, path := range paths {
		for _, locate := range stepLocators {
			entry, ok := locate(path, s.index)
			if !ok {
				continue
			}
			if entry.path != "" {
				return attribution{
					stepName:  entry.name,
					stepPath:  entry.path,
					fieldPath: fieldPathOf(path, entry.path),
				}
			}
			if partial == nil {
				partial = &attribution{stepName: entry.name, fieldPath: fieldPathOf(path, "")}
				// The name is known, so the raw document gives the path.
				if raw := s.rawDoc(); raw != nil {
					if r, ok := raw.stepByName(entry.name); ok {
						partial.stepPath = r.path
						partial.fieldPath = fieldPathOf(path, r.path)
					}
				}
			}
		}
	}
	if partial != nil {
		return *partial
	}
	for _, path := range paths {
		if raw := s.rawDoc(); raw != nil {
			if r, ok := raw.locate(path); ok {
				return attribution{stepName: r.name, stepPath: r.path, fieldPath: fieldPathOf(path, r.path)}
			}
		}
	}
	if len(paths) > 0 {
		return attribution{fieldPath: fieldPathOf(paths[0], "")}
	}
	return attribution{}
}

// stepWrapperFields are the fields that only wrap the error of a step or a handler.
var stepWrapperFields = []string{"steps", "handlers", "handler_on"}

// dagFields are the first segments of fields that always belong to the DAG, not to a step.
var dagFields = []string{
	"params", "consts", "defaults", "registry_auths", "harnesses", "otel",
	"tasks", "schedule", "dependencies", "step_types", "actions",
}

// fieldPathOf builds the field path of an error from its ValidationError
// chain. stepPath is the path of the step that owns the error, or empty. It
// returns an empty string when the field cannot be placed in the DAG.
func fieldPathOf(path errorPath, stepPath string) string {
	if len(path.fields) == 0 && decodeHandlerPrefix.MatchString(path.decodeName) {
		return path.decodeName
	}
	rest := path.fields
	wrapped := false
	for len(rest) > 0 && slices.Contains(stepWrapperFields, rest[0]) {
		rest = rest[1:]
		wrapped = true
	}
	if len(rest) > 0 && stepPathPrefix.MatchString(rest[0]) {
		return rest[0]
	}
	switch {
	case stepPath != "":
		joined := strings.Join(append([]string{stepPath}, rest...), ".")
		if strings.HasSuffix(joined, ".with") {
			if suffix := schemaFieldSuffix(path.detail); suffix != "" {
				joined += "." + suffix
			}
		}
		return joined
	case wrapped || len(rest) == 0:
		return ""
	}
	first, _, _ := strings.Cut(rest[0], ".")
	if !slices.Contains(dagFields, first) {
		return ""
	}
	return strings.Join(rest, ".")
}

// schemaLocation matches a schema location in the message of a JSON-schema
// error, such as "validating /properties/repeat:".
var schemaLocation = regexp.MustCompile(`validating (/[^\s:]+):`)

// extraProperty matches the message about one unexpected input property.
var extraProperty = regexp.MustCompile(`unexpected additional properties \["([^"]+)"\]`)

// schemaFieldSuffix returns the input field that a JSON-schema error is about,
// relative to the "with" of the step. The validator reports the location in
// the schema, not in the input, so only a chain of "properties" gives a
// field. One unexpected property gives its name. Other errors give "".
func schemaFieldSuffix(detail string) string {
	if locations := schemaLocation.FindAllStringSubmatch(detail, -1); len(locations) > 0 {
		var names []string
		tokens := strings.Split(strings.TrimPrefix(locations[len(locations)-1][1], "/"), "/")
		for i := 0; i+1 < len(tokens) && tokens[i] == "properties"; i += 2 {
			names = append(names, tokens[i+1])
		}
		// Anything else in the location leaves the chain, so the field is unknown.
		if len(names)*2 == len(tokens) {
			return strings.Join(names, ".")
		}
		return ""
	}
	if m := extraProperty.FindStringSubmatch(detail); m != nil {
		return m[1]
	}
	return ""
}

// stepPathPrefix matches the step that a field path starts with.
var stepPathPrefix = regexp.MustCompile(`^(?:steps\[\d+\]|handler_on\.[a-z]+)`)

// stepFromFieldPrefix reads the step from a field path such as
// "steps[1].run[0].command". The inferred dependency check reports its errors
// with such paths.
func stepFromFieldPrefix(path errorPath, index stepIndex) (stepEntry, bool) {
	for _, field := range path.fields {
		prefix := stepPathPrefix.FindString(field)
		if prefix == "" {
			continue
		}
		for _, e := range index {
			if e.path == prefix {
				return e, true
			}
		}
	}
	return stepEntry{}, false
}

// quotedStepText matches the owner of a message such as step 'b': ... ,
// step "b": ... or router "r" references ... .
var quotedStepText = regexp.MustCompile(`\b(?:step|router) (?:'([^']+)'|("(?:[^"\\]|\\.)*"))`)

// stepFromQuotedText reads a quoted step name from the message. The entry has
// no path when the DAG has no step of this name, for example when the build
// stopped before it produced steps.
func stepFromQuotedText(path errorPath, index stepIndex) (stepEntry, bool) {
	m := quotedStepText.FindStringSubmatch(path.detail)
	if m == nil {
		return stepEntry{}, false
	}
	name := m[1]
	if m[2] != "" {
		unquoted, err := strconv.Unquote(m[2])
		if err != nil {
			return stepEntry{}, false
		}
		name = unquoted
	}
	return index.byName(name), name != ""
}

// stepWord matches the word "step" that starts a clause of a message.
var stepWord = regexp.MustCompile(`(?:^|\s)step `)

// stepFromOwnerText reads the owner of a message such as "step b depends on
// non-existent step zz". The name is not quoted, so the first "step " is
// followed by the longest step name of the DAG. Names that the DAG does not
// have give no result.
func stepFromOwnerText(path errorPath, index stepIndex) (stepEntry, bool) {
	loc := stepWord.FindStringIndex(path.detail)
	if loc == nil {
		return stepEntry{}, false
	}
	rest := path.detail[loc[1]:]
	best := -1
	for i, e := range index {
		if e.name != "" && strings.HasPrefix(rest, e.name+" ") && (best < 0 || len(e.name) > len(index[best].name)) {
			best = i
		}
	}
	if best < 0 {
		return stepEntry{}, false
	}
	return index[best], true
}

// byName returns the first step of this name, or an entry that holds only the name.
func (x stepIndex) byName(name string) stepEntry {
	for _, e := range x {
		if e.name == name {
			return e
		}
	}
	return stepEntry{name: name}
}

// stepFromValue resolves the Value of a "steps" or "handler_on" validation
// error to a step.
func stepFromValue(path errorPath, index stepIndex) (stepEntry, bool) {
	i := path.stepsFieldIndex()
	if i < 0 {
		return stepEntry{}, false
	}
	value, _ := path.values[i].(string)
	if value == "" {
		return stepEntry{}, false
	}
	return index.lookupValue(value, path.detail)
}

// stepEntry is one step of the DAG as the editor keys it.
type stepEntry struct {
	name string
	id   string
	// path is "steps[<index>]" or "handler_on.<event>".
	path string
}

// stepIndex lists the steps of a DAG in editor order.
type stepIndex []stepEntry

func newStepIndex(dag *ir.DAG) stepIndex {
	if dag == nil {
		return nil
	}
	index := make(stepIndex, 0, len(dag.Steps)+len(handlerEvents))
	for i := range dag.Steps {
		index = append(index, stepEntry{
			name: dag.Steps[i].Name,
			id:   dag.Steps[i].ID,
			path: fmt.Sprintf("steps[%d]", i),
		})
	}
	for _, h := range handlerEvents {
		if step := h.step(&dag.HandlerOn); step != nil {
			index = append(index, stepEntry{name: step.Name, id: step.ID, path: "handler_on." + h.event})
		}
	}
	return index
}

// handlerEvents lists the lifecycle handlers in the order of the editor.
var handlerEvents = []struct {
	event string
	step  func(*ir.HandlerOn) *ir.Step
}{
	{"init", func(h *ir.HandlerOn) *ir.Step { return h.Init }},
	{"success", func(h *ir.HandlerOn) *ir.Step { return h.Success }},
	{"failure", func(h *ir.HandlerOn) *ir.Step { return h.Failure }},
	{"abort", func(h *ir.HandlerOn) *ir.Step { return h.Abort }},
	{"exit", func(h *ir.HandlerOn) *ir.Step { return h.Exit }},
	{"wait", func(h *ir.HandlerOn) *ir.Step { return h.Wait }},
}

// lookupValue resolves the value of a "steps" validation error. The value is a
// step name or a step ID. The message tells which one: "step ID" before
// "step name" means an ID. Otherwise the name has priority. A duplicate error
// points to the later step.
func (x stepIndex) lookupValue(value, detail string) (stepEntry, bool) {
	byName := func(e stepEntry) bool { return e.name == value }
	byID := func(e stepEntry) bool { return e.id == value }
	first, second := byName, byID
	if i, j := strings.Index(detail, "step ID"), strings.Index(detail, "step name"); i >= 0 && (j < 0 || i < j) {
		first, second = byID, byName
	}
	duplicate := strings.Contains(detail, "duplicate") || strings.Contains(detail, "must be unique")
	for _, match := range []func(stepEntry) bool{first, second} {
		var found []stepEntry
		for _, e := range x {
			if match(e) {
				found = append(found, e)
			}
		}
		switch {
		case len(found) == 0:
		case duplicate:
			return found[len(found)-1], true
		default:
			return found[0], true
		}
	}
	return stepEntry{}, false
}

// maxErrorPaths bounds the paths that errorPaths returns for one error.
const maxErrorPaths = 64

// errorPath is one root-to-leaf path through an error tree.
type errorPath struct {
	// fields and values hold the Field and Value of each ir.ValidationError on
	// the path, from the outermost to the innermost.
	fields []string
	values []any
	// decodeName is the name that the first mapstructure error of the path
	// gives to the failed key, such as "[s2].Depends" or "handler_on.failure".
	decodeName string
	// detail is the message of the first error below the innermost
	// ir.ValidationError that does not wrap several errors.
	detail string
}

// stepsFieldIndex returns the index of the innermost "steps" or "handler_on"
// field that has a value, or -1.
func (p errorPath) stepsFieldIndex() int {
	for i, v := range slices.Backward(p.fields) {
		if (v == "steps" || v == "handler_on") && p.values[i] != nil {
			return i
		}
	}
	return -1
}

// errorPaths walks err depth-first through Unwrap() error and Unwrap() []error
// and returns one path for each leaf, in tree order.
func errorPaths(err error) []errorPath {
	var paths []errorPath
	var walk func(e error, cur errorPath, hasDetail bool)
	walk = func(e error, cur errorPath, hasDetail bool) {
		if e == nil || len(paths) >= maxErrorPaths {
			return
		}
		if de, ok := e.(*mapstructure.DecodeError); ok && cur.decodeName == "" {
			cur.decodeName = de.Name()
		}
		switch x := e.(type) {
		case *ir.ValidationError:
			cur.fields = append(slices.Clip(cur.fields), x.Field)
			cur.values = append(slices.Clip(cur.values), x.Value)
			cur.detail, hasDetail = "", false
		case ir.ErrorList, interface{ Unwrap() []error }:
		default:
			if !hasDetail {
				cur.detail, hasDetail = e.Error(), true
			}
		}
		var children []error
		switch x := e.(type) {
		case interface{ Unwrap() []error }:
			children = x.Unwrap()
		case interface{ Unwrap() error }:
			if c := x.Unwrap(); c != nil {
				children = []error{c}
			}
		}
		children = slices.DeleteFunc(slices.Clone(children), func(c error) bool { return c == nil })
		if len(children) == 0 {
			if !hasDetail {
				cur.detail = e.Error()
			}
			paths = append(paths, cur)
			return
		}
		for _, c := range children {
			walk(c, cur, hasDetail)
		}
	}
	walk(err, errorPath{}, false)
	return paths
}

// specValueReferenceNotices returns the notices of one load of source, with the same
// options as GetDAGSpec. LoadSpec does not return the notices that the build reports,
// so the spec is loaded once more. A failed load gives no notices.
func (a *API) specValueReferenceNotices(ctx context.Context, name, source string) []cmnvalue.ValueReferenceNotice {
	opts := []spec.LoadOption{
		spec.WithName(name),
		spec.WithAllowBuildErrors(),
		spec.WithoutEval(),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(a.config.Paths.DAGsDir)),
	}
	if a.config.Paths.BaseConfig != "" {
		opts = append(opts, spec.WithBaseConfig(a.config.Paths.BaseConfig))
	}
	result, err := spec.LoadYAMLWithResult(ctx, []byte(source), opts...)
	if err != nil || result == nil {
		return nil
	}
	return result.ValueReferenceNotices
}

// rawStep is one step of the raw YAML. The path is the one that the spec
// gives to the step: its place in the flat step list of the DAG.
type rawStep struct {
	path string
	// name is the explicit name, or the key of the map form. It is empty when
	// the spec generates the name.
	name  string
	entry map[string]any
}

// rawDoc is the main YAML document, read the way internal/spec reads it.
type rawDoc struct {
	steps    []rawStep
	handlers []rawStep
}

// parseRawDoc reads the first non-empty document of data. It returns nil when
// the data has no usable document.
func parseRawDoc(data []byte) *rawDoc {
	if len(data) == 0 {
		return nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(yamlutil.ClearEmptyDocumentSeparators(data)))
	var doc map[string]any
	for len(doc) == 0 {
		if err := decoder.Decode(&doc); err != nil {
			return nil
		}
	}
	raw := &rawDoc{steps: flattenRawSteps(doc["steps"])}
	handlers, _ := doc["handler_on"].(map[string]any)
	for _, h := range handlerEvents {
		if entry, ok := handlers[h.event].(map[string]any); ok {
			raw.handlers = append(raw.handlers, rawStep{
				path:  "handler_on." + h.event,
				name:  handlerName(h.event),
				entry: entry,
			})
		}
	}
	return raw
}

// flattenRawSteps lists the steps the way the spec builds them. The array
// form keeps the order and expands nested arrays. The map form sorts by key.
// The list stops at the first entry that is not a step.
func flattenRawSteps(node any) []rawStep {
	var steps []rawStep
	add := func(name string, entry map[string]any) {
		steps = append(steps, rawStep{path: fmt.Sprintf("steps[%d]", len(steps)), name: name, entry: entry})
	}
	asEntry := func(item any) (map[string]any, bool) {
		switch v := item.(type) {
		case string:
			return map[string]any{"command": v}, true
		case map[string]any:
			return v, true
		}
		return nil, false
	}
	switch v := node.(type) {
	case []any:
		for _, item := range v {
			group := []any{item}
			if nested, ok := item.([]any); ok {
				group = nested
			}
			for _, member := range group {
				entry, ok := asEntry(member)
				if !ok {
					return steps
				}
				name, _ := entry["name"].(string)
				add(name, entry)
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			entry, _ := v[key].(map[string]any)
			add(key, entry)
		}
	}
	return steps
}

// stepByName returns the only step with this explicit name.
func (d *rawDoc) stepByName(name string) (rawStep, bool) {
	return uniqueRawStep(d.steps, func(r rawStep) bool { return name != "" && r.name == name })
}

func uniqueRawStep(list []rawStep, match func(rawStep) bool) (rawStep, bool) {
	var found []rawStep
	for _, r := range list {
		if match(r) {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		return rawStep{}, false
	}
	return found[0], true
}

// decodeKeyPrefix matches the key of the map form in the name of a decode error.
var decodeKeyPrefix = regexp.MustCompile(`^\[([^\]]+)\]`)

// decodeHandlerPrefix matches a handler in the name of a decode error.
var decodeHandlerPrefix = regexp.MustCompile(`^handler_on\.([a-z]+)`)

// handlerFromDecodeName reads the handler from the name of a decode error. The
// decode of the whole document fails before it builds the DAG, so this needs no
// DAG and no raw YAML.
func handlerFromDecodeName(path errorPath) (stepEntry, bool) {
	m := decodeHandlerPrefix.FindStringSubmatch(path.decodeName)
	if m == nil {
		return stepEntry{}, false
	}
	for _, h := range handlerEvents {
		if h.event == m[1] {
			return stepEntry{name: handlerName(h.event), path: "handler_on." + h.event}, true
		}
	}
	return stepEntry{}, false
}

// handlerName returns the name that the spec gives to the handler of an event.
func handlerName(event string) string {
	return "on" + strings.ToUpper(event[:1]) + event[1:]
}

// locate finds the step or the handler that a path of an error is about, from
// the names of decode errors and from the values of validation errors. A
// match must be unique.
func (d *rawDoc) locate(path errorPath) (rawStep, bool) {
	if m := decodeKeyPrefix.FindStringSubmatch(path.decodeName); m != nil {
		if r, ok := d.stepByName(m[1]); ok {
			return r, true
		}
	}
	k := len(path.values) - 1
	for k >= 0 && path.values[k] == nil {
		k--
	}
	if k < 0 {
		return rawStep{}, false
	}
	value, field := path.values[k], path.fields[k]
	match := func(r rawStep) bool { return rawStepHolds(r, value, field) }
	switch {
	case slices.Contains(path.fields, "handlers"):
		return uniqueRawStep(d.handlers, match)
	case slices.Contains(path.fields, "steps"):
		return uniqueRawStep(d.steps, match)
	}
	return rawStep{}, false
}

// rawStepHolds reports whether value is the step, or the value of field in the
// step. A value that has a name belongs to the step of this name.
func rawStepHolds(r rawStep, value any, field string) bool {
	if m, ok := value.(map[string]any); ok {
		if name, ok := m["name"].(string); ok && name != "" {
			return r.name == name
		}
	}
	return sameValue(value, r.entry) || sameValue(value, r.entry[field])
}

// sameValue compares a value of a validation error with a raw YAML value. The
// error holds a typed copy, such as []string, of a raw []any, so the check
// falls back to the printed form.
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(a, b) || fmt.Sprint(a) == fmt.Sprint(b)
}
