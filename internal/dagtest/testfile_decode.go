// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// fileSpec is a test file as written.
type fileSpec struct {
	dag         string
	params      []entry
	env         []entry
	secrets     []entry
	mocks       []mockEntry
	defaultMock *mockSpec
	real        []string
	cases       []caseSpec
}

// caseSpec is one case as written.
type caseSpec struct {
	name         string
	params       []entry
	env          []entry
	scheduledAt  string
	trigger      *string
	precondition *string
	mocks        []mockEntry
	defaultMock  *mockSpec
	real         []string
	expect       expectSpec
}

// entry is one name and its scalar's text as written.
type entry struct {
	name  string
	value string
}

type mockEntry struct {
	key  string
	mock mockSpec
}

// mockSpec is a mock as written.
type mockSpec struct {
	// inline holds the inline result fields, or nil when it sets none.
	inline *resultSpec
	// results holds the results list, or nil when it sets none.
	results      []resultSpec
	precondition *string
	review       *reviewSpec
}

type resultSpec struct {
	exitCode int
	stdout   string
	stderr   string
	// outputs is nil when the result sets none.
	outputs   map[string]any
	condition *string
}

type reviewSpec struct {
	decision string
	values   []reviewValue
}

// reviewValue is one review value, typed and as its text when it is a
// scalar.
type reviewValue struct {
	name   string
	value  any
	text   string
	scalar bool
}

type expectSpec struct {
	status        *string
	attempts      *int
	steps         []stepExpectSpec
	notifications []notificationSpec
	// hasNotifications reports that notifications is set, empty or not.
	hasNotifications bool
}

type stepExpectSpec struct {
	key string
	// token reports the status-token form, which sets only status.
	token    bool
	empty    bool
	status   *string
	attempts *int
	outputs  []OutputExpect
	stdout   *streamSpec
	stderr   *streamSpec
	// calledWith is nil when the step sets no called_with.
	calledWith *callSpec
}

// callSpec is a called_with assertion as written. A field it leaves out is
// nil, and one written as an empty map is empty but not nil.
type callSpec struct {
	run  *runSpec
	with []InputExpect
	env  []entry
}

// runSpec is a run assertion: text, or a map with contains and matches.
type runSpec struct {
	text   *string
	stream *streamSpec
}

type streamSpec struct {
	contains *string
	matches  *string
}

type notificationSpec struct {
	kind string
	step string
	to   []string
}

// decodeFile decodes a test file strictly: a field Spec 101 does not define
// or a value of the wrong type is an error labeled with its path in the
// file. A file with top-level steps and no cases reads as a DAG and gets one
// error that asks for a rename.
func decodeFile(data []byte) (fileSpec, []Finding) {
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil && !errors.Is(err, io.EOF) {
		return fileSpec{}, []Finding{{Message: "not valid YAML: " + err.Error()}}
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return fileSpec{}, []Finding{{Message: "holds more than one YAML document"}}
	}
	if err := checkAliases(&root); err != nil {
		return fileSpec{}, []Finding{{Message: "not valid YAML: " + err.Error()}}
	}
	if len(root.Content) == 0 || resolve(root.Content[0]).Kind != yaml.MappingNode {
		return fileSpec{}, []Finding{{Message: "the top level must be a map"}}
	}
	top := resolve(root.Content[0])
	if hasKey(top, "steps") && !hasKey(top, "cases") {
		return fileSpec{}, []Finding{{Message: "reads as a DAG, with steps and no cases; rename it so its name does not end in .test.yaml or .test.yml"}}
	}

	d := &decoder{}
	f := d.file(top)
	return f, d.errs
}

// maxExpandedNodes bounds the nodes of a test file with its aliases
// expanded.
const maxExpandedNodes = 1 << 20

// checkAliases reports an alias inside the node its anchor names, and
// aliases that expand the file past maxExpandedNodes. The decoder follows
// aliases as it walks, so it walks only a tree that passes.
func checkAliases(root *yaml.Node) error {
	sizes := make(map[*yaml.Node]int)
	open := make(map[*yaml.Node]bool)
	var expand func(n *yaml.Node) (int, error)
	expand = func(n *yaml.Node) (int, error) {
		n = resolve(n)
		if open[n] {
			return 0, fmt.Errorf("anchor %q holds an alias of itself", n.Anchor)
		}
		if size, ok := sizes[n]; ok {
			return size, nil
		}
		open[n] = true
		size := 1
		for _, child := range n.Content {
			childSize, err := expand(child)
			if err != nil {
				return 0, err
			}
			if size += childSize; size > maxExpandedNodes {
				return 0, errors.New("its aliases expand it too far")
			}
		}
		delete(open, n)
		sizes[n] = size
		return size, nil
	}
	_, err := expand(root)
	return err
}

func hasKey(mapping *yaml.Node, key string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return true
		}
	}
	return false
}

// decoder reads YAML nodes into a test file's fields and collects every
// error, each labeled with the path of its field.
type decoder struct {
	errs []Finding
}

func (d *decoder) fail(path, format string, args ...any) {
	d.errs = append(d.errs, Finding{Source: path, Message: fmt.Sprintf(format, args...)})
}

func (d *decoder) unknown(f field) {
	d.fail(f.path, "is not a field of a test file")
}

func (d *decoder) file(n *yaml.Node) fileSpec {
	var f fileSpec
	hasCases := false
	for _, fd := range d.fields(n, "") {
		switch fd.key {
		case "dag":
			f.dag = d.str(fd.value, fd.path)
		case "params":
			f.params = d.entries(fd.value, fd.path)
		case "env":
			f.env = d.entries(fd.value, fd.path)
		case "secrets":
			f.secrets = d.entries(fd.value, fd.path)
		case "mocks":
			f.mocks = d.mocks(fd.value, fd.path)
		case "default_mock":
			mock := d.mock(fd.value, fd.path)
			f.defaultMock = &mock
		case "real":
			f.real = d.strs(fd.value, fd.path)
		case "cases":
			hasCases = true
			f.cases = d.cases(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	if !hasCases {
		d.fail("cases", "is required")
	}
	return f
}

func (d *decoder) cases(n *yaml.Node, path string) []caseSpec {
	var cases []caseSpec
	names := make(map[string]bool)
	items := d.items(n, path)
	if resolve(n).Kind == yaml.SequenceNode && len(items) == 0 {
		d.fail(path, "must list at least one case")
	}
	for i, item := range items {
		itemPath := indexPath(path, i)
		if !d.isMap(item, itemPath) {
			continue
		}
		c := d.caseSpec(item, itemPath)
		switch {
		case c.name == "":
			d.fail(itemPath+".name", "is required and must not be empty")
		case names[c.name]:
			d.fail(itemPath+".name", "%q names another case of the file", c.name)
		}
		names[c.name] = true
		cases = append(cases, c)
	}
	return cases
}

func (d *decoder) caseSpec(n *yaml.Node, path string) caseSpec {
	var c caseSpec
	hasExpect := false
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "name":
			c.name = d.str(fd.value, fd.path)
		case "params":
			c.params = d.entries(fd.value, fd.path)
		case "env":
			c.env = d.entries(fd.value, fd.path)
		case "scheduled_at":
			c.scheduledAt = d.str(fd.value, fd.path)
		case "trigger":
			c.trigger = d.optStr(fd.value, fd.path)
		case "precondition":
			c.precondition = d.optStr(fd.value, fd.path)
		case "mocks":
			c.mocks = d.mocks(fd.value, fd.path)
		case "default_mock":
			mock := d.mock(fd.value, fd.path)
			c.defaultMock = &mock
		case "real":
			c.real = d.strs(fd.value, fd.path)
		case "expect":
			hasExpect = true
			c.expect = d.expect(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	if !hasExpect {
		d.fail(join(path, "expect"), "is required")
	}
	return c
}

func (d *decoder) mocks(n *yaml.Node, path string) []mockEntry {
	var mocks []mockEntry
	for _, fd := range d.fields(n, path) {
		mocks = append(mocks, mockEntry{key: fd.key, mock: d.mock(fd.value, fd.path)})
	}
	return mocks
}

func (d *decoder) mock(n *yaml.Node, path string) mockSpec {
	var m mockSpec
	var inline resultSpec
	for _, fd := range d.fields(n, path) {
		switch {
		case d.resultField(&inline, fd):
			m.inline = &inline
		case fd.key == "results":
			m.results = []resultSpec{}
			for i, item := range d.items(fd.value, fd.path) {
				m.results = append(m.results, d.result(item, indexPath(fd.path, i)))
			}
		case fd.key == "precondition":
			m.precondition = d.optStr(fd.value, fd.path)
		case fd.key == "review":
			m.review = d.review(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	return m
}

func (d *decoder) result(n *yaml.Node, path string) resultSpec {
	var r resultSpec
	for _, fd := range d.fields(n, path) {
		if !d.resultField(&r, fd) {
			d.unknown(fd)
		}
	}
	return r
}

// resultField decodes fd into r when it is a result field.
func (d *decoder) resultField(r *resultSpec, fd field) bool {
	switch fd.key {
	case "exit_code":
		r.exitCode = d.int(fd.value, fd.path)
	case "stdout":
		r.stdout = d.str(fd.value, fd.path)
	case "stderr":
		r.stderr = d.str(fd.value, fd.path)
	case "outputs":
		r.outputs = map[string]any{}
		for _, out := range d.fields(fd.value, fd.path) {
			r.outputs[out.key] = d.value(out.value, out.path)
		}
	case "condition":
		r.condition = d.optStr(fd.value, fd.path)
	default:
		return false
	}
	return true
}

func (d *decoder) review(n *yaml.Node, path string) *reviewSpec {
	r := &reviewSpec{}
	if !d.isMap(n, path) {
		return r
	}
	hasDecision := false
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "decision":
			hasDecision = true
			r.decision = d.str(fd.value, fd.path)
		case "values":
			for _, v := range d.fields(fd.value, fd.path) {
				node := resolve(v.value)
				r.values = append(r.values, reviewValue{
					name:   v.key,
					value:  d.value(node, v.path),
					text:   scalarText(node),
					scalar: node.Kind == yaml.ScalarNode,
				})
			}
		default:
			d.unknown(fd)
		}
	}
	if !hasDecision {
		d.fail(join(path, "decision"), "is required")
	}
	return r
}

func (d *decoder) expect(n *yaml.Node, path string) expectSpec {
	var e expectSpec
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "status":
			e.status = d.optStr(fd.value, fd.path)
		case "attempts":
			attempts := d.int(fd.value, fd.path)
			e.attempts = &attempts
		case "steps":
			for _, step := range d.fields(fd.value, fd.path) {
				e.steps = append(e.steps, d.stepExpect(step))
			}
		case "notifications":
			e.hasNotifications = true
			for i, item := range d.items(fd.value, fd.path) {
				e.notifications = append(e.notifications, d.notification(item, indexPath(fd.path, i)))
			}
		default:
			d.unknown(fd)
		}
	}
	return e
}

func (d *decoder) stepExpect(f field) stepExpectSpec {
	s := stepExpectSpec{key: f.key}
	kind := resolve(f.value).Kind
	if kind == yaml.ScalarNode {
		s.token = true
		s.status = d.optStr(f.value, f.path)
		return s
	}
	if kind != yaml.MappingNode {
		d.fail(f.path, "must be a status token or a map")
		return s
	}
	fields := d.fields(f.value, f.path)
	s.empty = len(fields) == 0
	for _, fd := range fields {
		switch fd.key {
		case "status":
			s.status = d.optStr(fd.value, fd.path)
		case "attempts":
			attempts := d.int(fd.value, fd.path)
			s.attempts = &attempts
		case "outputs":
			for _, out := range d.fields(fd.value, fd.path) {
				s.outputs = append(s.outputs, OutputExpect{Name: out.key, Value: d.value(out.value, out.path)})
			}
		case "stdout":
			s.stdout = d.stream(fd.value, fd.path)
		case "stderr":
			s.stderr = d.stream(fd.value, fd.path)
		case "called_with":
			s.calledWith = d.call(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	return s
}

func (d *decoder) call(n *yaml.Node, path string) *callSpec {
	c := &callSpec{}
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "run":
			c.run = &runSpec{}
			if resolve(fd.value).Kind == yaml.MappingNode {
				c.run.stream = d.stream(fd.value, fd.path)
			} else {
				c.run.text = d.optStr(fd.value, fd.path)
			}
		case "with":
			c.with = []InputExpect{}
			for _, in := range d.fields(fd.value, fd.path) {
				c.with = append(c.with, InputExpect{Name: in.key, Value: d.value(in.value, in.path)})
			}
		case "env":
			c.env = d.entries(fd.value, fd.path)
			if c.env == nil {
				c.env = []entry{}
			}
		default:
			d.unknown(fd)
		}
	}
	return c
}

func (d *decoder) stream(n *yaml.Node, path string) *streamSpec {
	s := &streamSpec{}
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "contains":
			s.contains = d.optStr(fd.value, fd.path)
		case "matches":
			s.matches = d.optStr(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	return s
}

func (d *decoder) notification(n *yaml.Node, path string) notificationSpec {
	var s notificationSpec
	for _, fd := range d.fields(n, path) {
		switch fd.key {
		case "kind":
			s.kind = d.str(fd.value, fd.path)
		case "step":
			s.step = d.str(fd.value, fd.path)
		case "to":
			s.to = d.strs(fd.value, fd.path)
		default:
			d.unknown(fd)
		}
	}
	return s
}

// field is one entry of a YAML map.
type field struct {
	key   string
	value *yaml.Node
	path  string
	// merged reports an entry that a merge key (<<) brought in.
	merged bool
}

// isMap reports whether n is a map, and an error when it is not. A caller
// checks it first when it would also report a missing field.
func (d *decoder) isMap(n *yaml.Node, path string) bool {
	if resolve(n).Kind != yaml.MappingNode {
		d.fail(path, "must be a map")
		return false
	}
	return true
}

// fields returns the entries of a map in order. Merge keys are expanded,
// and an entry written in the map wins over a merged one.
func (d *decoder) fields(n *yaml.Node, path string) []field {
	if !d.isMap(n, path) {
		return nil
	}
	n = resolve(n)
	var out []field
	index := make(map[string]int)
	hasMerge := false
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := resolve(n.Content[i]), n.Content[i+1]
		if key.ShortTag() == "!!merge" {
			if hasMerge {
				d.fail(join(path, key.Value), "appears more than once; merge a list of maps instead")
				continue
			}
			hasMerge = true
			for _, f := range d.merged(value, path) {
				if _, ok := index[f.key]; !ok {
					index[f.key] = len(out)
					out = append(out, f)
				}
			}
			continue
		}
		f := field{key: key.Value, value: value, path: join(path, key.Value)}
		j, ok := index[f.key]
		switch {
		case !ok:
			index[f.key] = len(out)
			out = append(out, f)
		case out[j].merged:
			out[j] = f
		default:
			d.fail(f.path, "appears more than once")
		}
	}
	return out
}

// merged returns the entries a merge key brings into the map at path: those
// of one map, or of a list of maps, the earlier map winning.
func (d *decoder) merged(n *yaml.Node, path string) []field {
	n = resolve(n)
	sources := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		sources = n.Content
	}
	var out []field
	for _, source := range sources {
		for _, f := range d.fields(source, path) {
			f.merged = true
			out = append(out, f)
		}
	}
	return out
}

func (d *decoder) items(n *yaml.Node, path string) []*yaml.Node {
	n = resolve(n)
	if n.Kind != yaml.SequenceNode {
		d.fail(path, "must be a list")
		return nil
	}
	return n.Content
}

// entries decodes a map of names to scalars, each as its text.
func (d *decoder) entries(n *yaml.Node, path string) []entry {
	var out []entry
	for _, fd := range d.fields(n, path) {
		value := resolve(fd.value)
		if value.Kind != yaml.ScalarNode {
			d.fail(fd.path, "must be a scalar")
			continue
		}
		if d.tagged(value, fd.path) {
			out = append(out, entry{name: fd.key, value: scalarText(value)})
		}
	}
	return out
}

func (d *decoder) strs(n *yaml.Node, path string) []string {
	var out []string
	for i, item := range d.items(n, path) {
		out = append(out, d.str(item, indexPath(path, i)))
	}
	return out
}

func (d *decoder) optStr(n *yaml.Node, path string) *string {
	s := d.str(n, path)
	return &s
}

// str decodes a string. An unquoted date or time is read as written.
func (d *decoder) str(n *yaml.Node, path string) string {
	n = resolve(n)
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" && n.ShortTag() != "!!timestamp" {
		d.fail(path, "must be a string")
		return ""
	}
	d.tagged(n, path)
	return n.Value
}

// tagged reports whether a scalar that is kept as its text holds a value of
// its tag, such as an explicit !!int, and an error when it does not.
func (d *decoder) tagged(n *yaml.Node, path string) bool {
	var v any
	if err := n.Decode(&v); err != nil {
		d.fail(path, "is not a valid %s", n.ShortTag())
		return false
	}
	return true
}

func (d *decoder) int(n *yaml.Node, path string) int {
	var v int
	n = resolve(n)
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int" || n.Decode(&v) != nil {
		d.fail(path, "must be an integer")
	}
	return v
}

// value decodes any YAML value into the form JSON encodes: maps by their
// keys' text, and a date or time as written.
func (d *decoder) value(n *yaml.Node, path string) any {
	n = resolve(n)
	if n.Kind == yaml.MappingNode {
		m := make(map[string]any)
		for _, fd := range d.fields(n, path) {
			m[fd.key] = d.value(fd.value, fd.path)
		}
		return m
	}
	if n.Kind == yaml.SequenceNode {
		list := make([]any, 0, len(n.Content))
		for i, item := range n.Content {
			list = append(list, d.value(item, indexPath(path, i)))
		}
		return list
	}
	switch n.ShortTag() {
	case "!!str", "!!timestamp":
		return n.Value
	case "!!null":
		return nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		d.fail(path, "%v", err)
	}
	return v
}

// scalarText returns a scalar's text as written. A null is empty.
func scalarText(n *yaml.Node) string {
	if n.ShortTag() == "!!null" {
		return ""
	}
	return n.Value
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func indexPath(path string, i int) string {
	return fmt.Sprintf("%s[%d]", path, i)
}
