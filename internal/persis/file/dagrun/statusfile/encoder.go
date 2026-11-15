// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package statusfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// nodesKey is the JSON name of the status field that holds the step nodes.
const nodesKey = "nodes"

// handlerKeys are the JSON names of the status fields that hold handler nodes,
// in the order of handlerFields.
var handlerKeys = [...]string{"onInit", "onExit", "onSuccess", "onFailure", "onAbort", "onWait"}

func handlerFields(st *ir.DAGRunStatus) [len(handlerKeys)]**ir.Node {
	return [...]**ir.Node{&st.OnInit, &st.OnExit, &st.OnSuccess, &st.OnFailure, &st.OnAbort, &st.OnWait}
}

// Encoder encodes statuses as status file lines. Each line records the whole
// status, given the lines the encoder returned before it since it was created
// or reset. The zero value is ready to use.
type Encoder struct {
	last      *parts // what the last line records; nil when the next must be full
	lastSum   uint64 // checksum of the last line
	fullSize  int    // size of the last full record
	chainSize int    // size of the change records after it
}

// Encode returns the line, without its newline, that records st. When a line
// does not reach the file whole, Reset must be called before the next Encode.
func (e *Encoder) Encode(st ir.DAGRunStatus) ([]byte, error) {
	p, err := newParts(st)
	if err != nil {
		return nil, err
	}
	line := e.changeRecord(&p)
	if line != nil {
		e.chainSize += len(line)
	} else {
		line = p.record()
		e.fullSize, e.chainSize = len(line), 0
	}
	e.last, e.lastSum = &p, crc64.Checksum(line, crcTable)
	return line, nil
}

// Reset makes the next line a full record.
func (e *Encoder) Reset() {
	*e = Encoder{}
}

// changeRecord returns p as a change record on the last line, or nil when p
// must be a full record.
func (e *Encoder) changeRecord(p *parts) []byte {
	last := e.last
	if last == nil || len(p.nodes) != len(last.nodes) {
		return nil
	}
	for i := range p.handlers {
		if (p.handlers[i] == nil) != (last.handlers[i] == nil) {
			return nil
		}
	}

	line := make([]byte, 0, changeHeaderSize+len(p.top)+64)
	line = append(line, changePrefix)
	line = fmt.Appendf(line, "%0*x ", baseDigits, e.lastSum)
	line = append(line, `{"top":`...)
	line = append(line, p.top...)
	line = append(line, `,"nodes":{`...)
	changed := 0
	add := func(key string, node json.RawMessage) {
		if changed > 0 {
			line = append(line, ',')
		}
		changed++
		line = strconv.AppendQuote(line, key)
		line = append(line, ':')
		line = append(line, node...)
	}
	for i, node := range p.nodes {
		if !bytes.Equal(node, last.nodes[i]) {
			add(strconv.Itoa(i), node)
		}
	}
	for i, node := range p.handlers {
		if !bytes.Equal(node, last.handlers[i]) {
			add(handlerKeys[i], node)
		}
	}
	line = append(line, "}}"...)

	// A full record follows once the change records after the last one would
	// add up to more than half its size. A reader of the newest line then
	// decodes at most 1.5 times one full record.
	if e.chainSize+len(line) > e.fullSize/2 {
		return nil
	}
	return line
}

// parts are the pieces of a stored status that a change record replaces one
// by one: the run-level fields, each step node and each handler node.
type parts struct {
	top      json.RawMessage
	nodes    []json.RawMessage
	handlers [len(handlerKeys)]json.RawMessage // nil for a handler the status lacks
}

func newParts(st ir.DAGRunStatus) (parts, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	marshal := func(v any) (json.RawMessage, error) {
		buf.Reset()
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return bytes.Clone(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})), nil
	}

	stored := ir.StoredStatus(st)
	var p parts
	var err error
	if len(stored.Nodes) > 0 {
		p.nodes = make([]json.RawMessage, len(stored.Nodes))
		for i, node := range stored.Nodes {
			if p.nodes[i], err = marshal(node); err != nil {
				return parts{}, err
			}
		}
	}
	for i, field := range handlerFields(&stored) {
		if *field != nil {
			if p.handlers[i], err = marshal(*field); err != nil {
				return parts{}, err
			}
		}
		*field = nil
	}
	stored.Nodes = nil
	if p.top, err = marshal(stored); err != nil {
		return parts{}, err
	}
	return p, nil
}

// partsOfRecord splits a full record line into the pieces a change record
// replaces. Its run-level fields are left out, since every change record holds
// them all.
func partsOfRecord(line []byte) (parts, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return parts{}, err
	}
	var p parts
	if raw, ok := fields[nodesKey]; ok {
		if err := json.Unmarshal(raw, &p.nodes); err != nil {
			return parts{}, err
		}
	}
	for i, key := range handlerKeys {
		p.handlers[i] = fields[key]
	}
	return p, nil
}

// apply replaces the nodes that a change record holds.
func (p *parts) apply(nodes map[string]json.RawMessage) error {
	for key, node := range nodes {
		if i := slices.Index(handlerKeys[:], key); i >= 0 {
			if p.handlers[i] == nil {
				return errMalformedChange
			}
			p.handlers[i] = node
			continue
		}
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= len(p.nodes) {
			return errMalformedChange
		}
		p.nodes[i] = node
	}
	return nil
}

// record returns the parts as a full record line. The run-level fields come
// first, then the step nodes and the handlers.
func (p *parts) record() []byte {
	size := len(p.top) + len(`,"nodes":[]`) + len(p.nodes)
	for _, node := range p.nodes {
		size += len(node)
	}
	for i, node := range p.handlers {
		if node != nil {
			size += len(handlerKeys[i]) + len(node) + len(`,"":`)
		}
	}

	line := make([]byte, 0, size)
	line = append(line, p.top[:len(p.top)-1]...)
	empty := len(bytes.TrimSpace(p.top[1:len(p.top)-1])) == 0
	member := func(key string) {
		if !empty {
			line = append(line, ',')
		}
		empty = false
		line = strconv.AppendQuote(line, key)
		line = append(line, ':')
	}
	if p.nodes != nil {
		member(nodesKey)
		line = append(line, '[')
		for i, node := range p.nodes {
			if i > 0 {
				line = append(line, ',')
			}
			line = append(line, node...)
		}
		line = append(line, ']')
	}
	for i, node := range p.handlers {
		if node != nil {
			member(handlerKeys[i])
			line = append(line, node...)
		}
	}
	return append(line, '}')
}
