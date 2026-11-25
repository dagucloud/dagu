// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"gopkg.in/yaml.v3"
)

// errCaseForm is why Update keeps an expect whose text it cannot change in
// place. An edit through an anchor or alias would change another part of
// the file.
var errCaseForm = errors.New("the case is written in flow style or uses a YAML anchor, alias, or merge key")

// errNotInPlace is why Update keeps an expect whose rewritten text would not
// read back as the expect it was given, with the rest of the file as before.
var errNotInPlace = errors.New("the expect could not be rewritten in place")

// defaultIndent is the indentation step of written YAML, and of an expect
// whose own step yaml.v3 cannot write, which is 2 to 9 spaces.
const defaultIndent = 2

// Update rewrites the expect of cases of a test file that LoadFile read, as
// dagu test --update does (Spec 101, Authoring Flags). Only the text of
// what an expect changes is rewritten, so the rest of the file stays as it
// is, comments included, and each rewrite is read back before it is kept.
type Update struct {
	file  *File
	text  *yamlText
	steps *stepIndex
	// cases is the file's list of cases, or nil when it is not written
	// directly under the top-level map.
	cases *yaml.Node
	// edits holds the text edits of every expect that Set changed, written
	// the text they make, and decoded that text decoded.
	edits   []textEdit
	written []byte
	decoded fileSpec
}

// NewUpdate returns the update of the test file f, which LoadFile returned.
func NewUpdate(f *File) (*Update, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(f.data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s holds no test file", f.Path)
	}
	decoded, errs := decodeFile(f.data)
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %s", f.Path, joinFindings(errs))
	}
	u := &Update{file: f, text: newYAMLText(f.data), steps: indexSteps(f.DAG), written: f.data, decoded: decoded}
	top := resolve(doc.Content[0])
	if i := keyIndex(top, "cases"); i >= 0 {
		u.cases = top.Content[i+1]
	}
	return u, nil
}

// Set rewrites the expect of the named case to expect: the case's expect as
// written, with its assertions in their order and some of their values
// changed, followed by the step entries it adds. An output value is written
// as yaml.v3 writes it, so a number is an int64, uint64, or float64. Set
// reports whether the file's text changes. A case that uses a YAML anchor,
// alias, or merge key, or is written in flow style, keeps its expect, and so
// does one whose rewritten text would not read back as expect with the rest
// of the file as before; Set then returns why.
func (u *Update) Set(name string, expect Expect) (bool, error) {
	i := slices.IndexFunc(u.file.Cases, func(c Case) bool { return c.Name == name })
	if i < 0 {
		return false, fmt.Errorf("the test file has no case %q", name)
	}
	at, err := u.expectEntry(name)
	if err != nil {
		return false, err
	}
	ed := &expectEditor{text: u.text, steps: u.steps, indent: indentStep(at)}
	ed.expect(at, u.file.Cases[i].Expect, expect)
	edits, err := ed.render()
	if err != nil || len(edits) == 0 {
		return false, err
	}
	if err := u.accept(i, expect, edits); err != nil {
		return false, err
	}
	return true, nil
}

// accept keeps the edits that rewrite the expect of the case at index i to
// expect when the text they make, with the edits kept before, decodes with
// that case's expect as expect and the case otherwise valid as before, and
// with every other part of the file as before. Otherwise it keeps nothing and
// returns errNotInPlace.
func (u *Update) accept(i int, expect Expect, edits []textEdit) error {
	all := append(slices.Clone(u.edits), edits...)
	text := u.text.apply(slices.Clone(all))
	decoded, errs := decodeFile(text)
	if len(errs) > 0 || len(decoded.cases) != len(u.decoded.cases) {
		return errNotInPlace
	}
	rebuilt := buildCase(u.file.DAG, u.steps, decoded, decoded.cases[i])
	if !sameExpect(rebuilt.Expect, expect) || !reflect.DeepEqual(rebuilt.Errors, u.file.Cases[i].Errors) {
		return errNotInPlace
	}
	rest := decoded
	rest.cases = slices.Clone(decoded.cases)
	rest.cases[i].expect = u.decoded.cases[i].expect
	if !reflect.DeepEqual(rest, u.decoded) {
		return errNotInPlace
	}
	u.edits, u.written, u.decoded = all, text, decoded
	return nil
}

// sameExpect reports whether two expects hold the same assertions, with
// numbers compared by value.
func sameExpect(a, b Expect) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// Write writes the file with every expect that Set changed, through a
// temporary file in its directory renamed over it, and keeps its mode. It
// writes nothing when Set changed nothing, and fails without writing when
// the file no longer holds the text LoadFile read.
func (u *Update) Write() error {
	if len(u.edits) == 0 {
		return nil
	}
	path, err := filepath.EvalSymlinks(u.file.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	current, err := fileutil.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, u.text.data) {
		return fmt.Errorf("%s changed while its cases ran, so its expects were not updated", u.file.Path)
	}
	return fileutil.WriteFileAtomic(path, u.written, info.Mode().Perm())
}

// expectEntry returns the expect entry of the named case, or errCaseForm
// when the case or its expect cannot be changed in place.
func (u *Update) expectEntry(name string) (slot, error) {
	if u.cases == nil || u.cases.Kind != yaml.SequenceNode {
		return slot{}, errCaseForm
	}
	for _, item := range u.cases.Content {
		n := keyIndex(item, "name")
		if item.Kind != yaml.MappingNode || n < 0 || resolve(item.Content[n+1]).Value != name {
			continue
		}
		x := keyIndex(item, "expect")
		if x < 0 || item.Anchor != "" || item.Style&yaml.FlowStyle != 0 || keyIndex(item, "<<") >= 0 || !plainTree(item.Content[x+1]) {
			break
		}
		return slot{parent: item, index: x}, nil
	}
	return slot{}, errCaseForm
}

// plainTree reports whether n holds no anchor, alias, or merge key.
func plainTree(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.ShortTag() == "!!merge" {
		return false
	}
	return !slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return !plainTree(c) })
}

// keyIndex returns the index in a map's content of the key with the given
// text, or -1.
func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// indentStep returns how far the entries of the expect at are indented past
// its key.
func indentStep(at slot) int {
	v := at.value()
	if v.Style&yaml.FlowStyle != 0 || len(v.Content) == 0 {
		return defaultIndent
	}
	if step := v.Content[0].Column - at.key().Column; step >= 2 && step <= 9 {
		return step
	}
	return defaultIndent
}

// slot is an entry of a map in the tree of a test file.
type slot struct {
	parent *yaml.Node
	// index is the index of the entry's key in parent's content.
	index int
	// flow is the outermost entry of a block map whose value is a flow
	// collection that holds this entry, or nil.
	flow *slot
}

func (s slot) key() *yaml.Node   { return s.parent.Content[s.index] }
func (s slot) value() *yaml.Node { return s.parent.Content[s.index+1] }

// child returns the entry of key in the map that s holds, or nil.
func (s slot) child(key string) *slot {
	v := s.value()
	i := keyIndex(v, key)
	if i < 0 {
		return nil
	}
	c := &slot{parent: v, index: i, flow: s.flow}
	if c.flow == nil && v.Style&yaml.FlowStyle != 0 {
		c.flow = &s
	}
	return c
}

// expectEditor changes the tree of one case's expect and makes the text
// edits that write the changes.
type expectEditor struct {
	text  *yamlText
	steps *stepIndex
	// indent is the indentation step of written YAML.
	indent int
	// changes lists the changed entries in the order they changed.
	changes []change
}

// change is an entry whose value was set, or a map entry that entries were
// appended to.
type change struct {
	at slot
	// old is the entry's value as the file writes it, for its position and
	// style.
	old yaml.Node
	// added reports entries appended to the map that at holds, after the
	// entries of old.
	added bool
}

// expect changes the expect at from written to e.
func (ed *expectEditor) expect(at slot, written, e Expect) {
	if e.Status != written.Status {
		ed.set(at.child("status"), e.Status)
	}
	if !reflect.DeepEqual(e.Attempts, written.Attempts) {
		ed.set(at.child("attempts"), *e.Attempts)
	}
	steps := at.child("steps")
	var added []*yaml.Node
	for i, s := range e.Steps {
		if i < len(written.Steps) {
			ed.step(steps.child(s.Key), written.Steps[i], s)
		} else {
			added = append(added, valueNode(s.Key), stepExpectNode(s))
		}
	}
	switch {
	case len(added) == 0:
	case steps == nil:
		ed.add(at, valueNode("steps"), &yaml.Node{Kind: yaml.MappingNode, Content: added})
	default:
		ed.add(*steps, added...)
	}
	if !reflect.DeepEqual(e.Notifications, written.Notifications) {
		list := &yaml.Node{Kind: yaml.SequenceNode}
		for _, x := range e.Notifications {
			list.Content = append(list.Content, notificationNode(ed.steps, x))
		}
		ed.replace(*at.child("notifications"), list)
	}
}

// step changes the entry at of one step or handler from written to s. A
// status token stays a token.
func (ed *expectEditor) step(at *slot, written, s StepExpect) {
	if at.value().Kind == yaml.ScalarNode {
		if s.Status != written.Status {
			ed.set(at, s.Status)
		}
		return
	}
	if s.Status != written.Status {
		ed.set(at.child("status"), s.Status)
	}
	if !reflect.DeepEqual(s.Attempts, written.Attempts) {
		ed.set(at.child("attempts"), *s.Attempts)
	}
	outputs := at.child("outputs")
	for i, o := range s.Outputs {
		ed.value(outputs.child(o.Name), written.Outputs[i].Value, o.Value)
	}
	if s.CalledWith != nil {
		ed.call(at.child("called_with"), *written.CalledWith, *s.CalledWith)
	}
}

// call changes the called_with entry at from written to c. A run given as
// contains and matches keeps its map.
func (ed *expectEditor) call(at *slot, written, c CallExpect) {
	if c.Run != nil && c.Run.Text != nil && *c.Run.Text != *written.Run.Text {
		ed.set(at.child("run"), *c.Run.Text)
	}
	with := at.child("with")
	for i, in := range c.With {
		ed.value(with.child(in.Name), written.With[i].Value, in.Value)
	}
	env := at.child("env")
	for i, e := range c.Env {
		if e.Value != written.Env[i].Value {
			ed.set(env.child(e.Name), e.Value)
		}
	}
}

// value changes the value at from written to v. An object that stays an
// object changes only the values of its keys that change.
func (ed *expectEditor) value(at *slot, written, v any) {
	if reflect.DeepEqual(written, v) {
		return
	}
	old, wasObject := written.(map[string]any)
	object, isObject := v.(map[string]any)
	if n := at.value(); wasObject && isObject && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			ed.value(at.child(key), old[key], object[key])
		}
		return
	}
	ed.set(at, v)
}

// set sets the value at to v. A scalar that stays a scalar keeps its node,
// and its quotes when it stays a string on one line.
func (ed *expectEditor) set(at *slot, v any) {
	n, cur := valueNode(v), at.value()
	if cur.Kind != yaml.ScalarNode || n.Kind != yaml.ScalarNode {
		ed.replace(*at, n)
		return
	}
	ed.changes = append(ed.changes, change{at: *at, old: *cur})
	quoted := yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle
	if cur.ShortTag() == "!!str" && n.ShortTag() == "!!str" && cur.Style&quoted != 0 && !strings.Contains(n.Value, "\n") {
		n.Style = cur.Style
	}
	cur.Value, cur.Tag, cur.Style = n.Value, n.Tag, n.Style
}

// replace replaces the value at with n.
func (ed *expectEditor) replace(at slot, n *yaml.Node) {
	ed.changes = append(ed.changes, change{at: at, old: *at.value()})
	at.parent.Content[at.index+1] = n
}

// add appends entries to the map that at holds.
func (ed *expectEditor) add(at slot, entries ...*yaml.Node) {
	m := at.value()
	ed.changes = append(ed.changes, change{at: at, old: *m, added: true})
	m.Content = append(slices.Clip(m.Content), entries...)
}

// render returns the text edits of the changes. A scalar that fits on the
// line of its old text replaces that text. Any other change inside a flow
// collection rewrites the outermost one in block style, comments inside it
// included.
func (ed *expectEditor) render() ([]textEdit, error) {
	edits := make([]textEdit, 0, len(ed.changes))
	for _, c := range ed.changes {
		if edit, ok := ed.scalarEdit(c); ok {
			edits = append(edits, edit)
			continue
		}
		var edit textEdit
		var err error
		switch root := c.at.flow; {
		case root == nil && c.added && c.at.value().Style&yaml.FlowStyle != 0:
			edit, err = ed.valueEdit(c.at, c.old, blockCopy(c.at.value()))
		case root != nil:
			edit, err = ed.valueEdit(*root, *root.value(), blockCopy(root.value()))
		case c.added:
			edit = ed.appendEdit(c)
		default:
			edit, err = ed.valueEdit(c.at, c.old, c.at.value())
		}
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit)
	}
	return edits, nil
}

// scalarEdit returns the edit that writes a scalar set in place over its old
// text, when both fit on one line.
func (ed *expectEditor) scalarEdit(c change) (textEdit, bool) {
	cur := c.at.value()
	if c.added || c.old.Kind != yaml.ScalarNode || cur.Kind != yaml.ScalarNode {
		return textEdit{}, false
	}
	start, end, ok := ed.text.span(&c.old)
	inline, below := ed.place(cur, 0)
	// A plain scalar with a flow indicator would end a flow collection.
	if !ok || below != "" || c.at.flow != nil && strings.ContainsAny(inline, ",[]{}#:") {
		return textEdit{}, false
	}
	return textEdit{start: start, end: end, text: inline}, true
}

// valueEdit returns the edit that writes n as the value of the entry at,
// whose value the file writes as old. A comment on the key's line stays
// there.
func (ed *expectEditor) valueEdit(at slot, old yaml.Node, n *yaml.Node) (textEdit, error) {
	key := at.key()
	colon, ok := ed.text.colonEnd(key)
	if !ok {
		return textEdit{}, errNotInPlace
	}
	inline, below := ed.place(n, key.Column-1)
	start, end, ok := ed.text.span(&old)
	last := ed.text.entryEnd(key, &old)
	if ok {
		// A flow collection may close on a line no deeper than its key.
		last = max(last, ed.text.lineEnd(ed.text.lineOf(end)))
	}
	var comment string
	switch {
	case old.Line != key.Line:
		comment = ed.text.restOfLine(colon)
	case ok && below == "":
		return textEdit{start: start, end: end, text: inline}, nil
	case ok:
		comment = ed.text.restOfLine(end)
	}
	if inline != "" {
		inline = " " + inline
	}
	return textEdit{start: colon, end: last, text: inline + comment + below}, nil
}

// appendEdit returns the edit that writes the entries appended to a block
// map after the map's last line.
func (ed *expectEditor) appendEdit(c change) textEdit {
	m := c.at.value()
	added := &yaml.Node{Kind: yaml.MappingNode, Content: m.Content[len(c.old.Content):]}
	end := ed.text.entryEnd(c.at.key(), &c.old)
	return textEdit{start: end, end: end, text: ed.below(ed.lines(added), m.Content[0].Column-1)}
}

// place writes n as the value of a key indented by indent: inline holds the
// text on the key's line, and below the lines under it.
func (ed *expectEditor) place(n *yaml.Node, indent int) (inline, below string) {
	lines := ed.lines(n)
	if n.Kind != yaml.ScalarNode && n.Style&yaml.FlowStyle == 0 && len(n.Content) > 0 {
		return "", ed.below(lines, indent+ed.indent)
	}
	// The lines after a block scalar's header are indented by the step.
	return lines[0], ed.below(lines[1:], indent)
}

// lines returns the lines of n written as YAML. The comments of n itself
// stay on the lines of its old text, and those inside n come along.
func (ed *expectEditor) lines(n *yaml.Node) []string {
	bare := *n
	bare.HeadComment, bare.LineComment, bare.FootComment = "", "", ""
	return strings.Split(strings.TrimSuffix(encodeYAML(&bare, ed.indent), "\n"), "\n")
}

// below returns each line after a line break, indented by pad.
func (ed *expectEditor) below(lines []string, pad int) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(ed.text.newline)
		if line != "" {
			b.WriteString(strings.Repeat(" ", pad) + line)
		}
	}
	return b.String()
}

// blockCopy returns a copy of n in block style. An empty collection stays
// in flow style.
func blockCopy(n *yaml.Node) *yaml.Node {
	c := *n
	if len(c.Content) > 0 {
		c.Style &^= yaml.FlowStyle
	}
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = blockCopy(child)
	}
	return &c
}

// textEdit replaces the text from start to end with text.
type textEdit struct {
	start, end int
	text       string
}

// yamlText is the text of a YAML file, addressed by the line and column a
// yaml.v3 node reports.
type yamlText struct {
	data []byte
	// starts holds the offset of each line, the first line first.
	starts []int
	// newline is the file's line break.
	newline string
}

func newYAMLText(data []byte) *yamlText {
	t := &yamlText{data: data, starts: []int{0}, newline: "\n"}
	for i, b := range data {
		if b == '\n' {
			t.starts = append(t.starts, i+1)
		}
	}
	if bytes.Contains(data, []byte("\r\n")) {
		t.newline = "\r\n"
	}
	return t
}

// apply returns the text with edits made. Edits of one expect are disjoint
// or nested, and a nested edit is part of the edit that holds it, so the
// edit that starts first, the longest first, wins.
func (t *yamlText) apply(edits []textEdit) []byte {
	slices.SortStableFunc(edits, func(a, b textEdit) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(b.end, a.end))
	})
	var out bytes.Buffer
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			continue
		}
		out.Write(t.data[pos:e.start])
		out.WriteString(e.text)
		pos = e.end
	}
	out.Write(t.data[pos:])
	return out.Bytes()
}

// offset returns the offset of a line and a column, both counted from 1,
// the column in characters.
func (t *yamlText) offset(line, column int) int {
	pos := t.starts[line-1]
	for ; column > 1 && pos < len(t.data); column-- {
		_, size := utf8.DecodeRune(t.data[pos:])
		pos += size
	}
	return pos
}

// lineEnd returns the offset of the end of a line, before its line break.
func (t *yamlText) lineEnd(line int) int {
	end := len(t.data)
	if line < len(t.starts) {
		end = t.starts[line] - 1
	}
	if end > t.starts[line-1] && t.data[end-1] == '\r' {
		end--
	}
	return end
}

// lineOf returns the line that holds the offset pos.
func (t *yamlText) lineOf(pos int) int {
	line, _ := slices.BinarySearch(t.starts, pos+1)
	return line
}

// restOfLine returns the text from pos to the end of its line.
func (t *yamlText) restOfLine(pos int) string {
	return string(t.data[pos:t.lineEnd(t.lineOf(pos))])
}

// span returns the offsets of the text of n when it can tell where it ends:
// a scalar other than a block scalar that ends on its first line, or a flow
// collection.
func (t *yamlText) span(n *yaml.Node) (start, end int, ok bool) {
	start = t.offset(n.Line, n.Column)
	if start > t.lineEnd(n.Line) {
		return start, start, false
	}
	switch {
	case n.Kind == yaml.ScalarNode:
		end, ok = scalarEnd(t.data[start:t.lineEnd(n.Line)], n)
	case n.Style&yaml.FlowStyle != 0:
		end, ok = flowEnd(t.data[start:])
	}
	return start, start + end, ok
}

// scalarEnd returns the length of the text of the scalar n at the start of
// rest, its explicit tag included, when it ends there.
func scalarEnd(rest []byte, n *yaml.Node) (int, bool) {
	if n.Style&yaml.TaggedStyle != 0 {
		tag := bytes.IndexAny(rest, " \t")
		if tag < 0 {
			return 0, false
		}
		skip := len(rest) - len(bytes.TrimLeft(rest[tag:], " \t"))
		untagged := *n
		untagged.Style &^= yaml.TaggedStyle
		end, ok := scalarEnd(rest[skip:], &untagged)
		return skip + end, ok
	}
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			switch rest[i] {
			case '\\':
				i++
			case '"':
				return i + 1, true
			}
		}
	case n.Style&yaml.SingleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			if rest[i] != '\'' {
				continue
			}
			if i+1 < len(rest) && rest[i+1] == '\'' {
				i++
				continue
			}
			return i + 1, true
		}
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 && n.Value != "" && bytes.HasPrefix(rest, []byte(n.Value)):
		return len(n.Value), true
	}
	return 0, false
}

// flowEnd returns the length of the flow collection at the start of rest,
// which may span lines.
func flowEnd(rest []byte) (int, bool) {
	depth := 0
	var quote, prev byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote == '\'' && c == '\'' && i+1 < len(rest) && rest[i+1] == '\'':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '#' && i > 0 && strings.IndexByte(" \t\n", rest[i-1]) >= 0:
			// A comment runs to the end of its line.
			j := bytes.IndexByte(rest[i:], '\n')
			if j < 0 {
				return 0, false
			}
			i += j
			continue
		case (c == '"' || c == '\'') && strings.IndexByte("{[,:", prev) >= 0:
			// A quote starts a scalar only where a scalar can start.
			quote = c
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			if depth--; depth == 0 {
				return i + 1, true
			}
		}
		if strings.IndexByte(" \t\r\n", c) < 0 {
			prev = c
		}
	}
	return 0, false
}

// colonEnd returns the offset after the colon that follows key.
func (t *yamlText) colonEnd(key *yaml.Node) (int, bool) {
	_, end, ok := t.span(key)
	if !ok {
		return 0, false
	}
	rest := bytes.TrimLeft(t.data[end:t.lineEnd(key.Line)], " \t")
	if len(rest) == 0 || rest[0] != ':' {
		return 0, false
	}
	return t.lineEnd(key.Line) - len(rest) + 1, true
}

// entryEnd returns the offset where the entry of key, whose value is value,
// ends: after the last line, from the key's line on, that holds its text. A
// line below the key's holds it when it is indented deeper than the key, or
// is an item of value, a block list as deep as the key. A blank line, and a
// comment line no deeper than the key, hold nothing of the entry, and the
// entry ends before the next line that holds something else.
func (t *yamlText) entryEnd(key, value *yaml.Node) int {
	indent := key.Column - 1
	listAtKey := value.Kind == yaml.SequenceNode && value.Style&yaml.FlowStyle == 0 && value.Column == key.Column
	last := key.Line
	for line := key.Line + 1; line <= len(t.starts); line++ {
		text := string(t.data[t.starts[line-1]:t.lineEnd(line)])
		body := strings.TrimLeft(text, " ")
		depth := len(text) - len(body)
		switch {
		case strings.TrimSpace(body) == "" || body[0] == '#' && depth <= indent:
		case depth > indent || listAtKey && depth == indent && body[0] == '-':
			last = line
		default:
			return t.lineEnd(last)
		}
	}
	return t.lineEnd(last)
}
