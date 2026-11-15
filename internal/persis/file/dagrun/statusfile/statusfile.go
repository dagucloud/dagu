// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package statusfile reads and encodes the lines of an attempt's status file.
//
// A status file is append-only, and each line records the whole status at one
// point in time. A line is either a full record, the stored status as a JSON
// object, or a change record:
//
//	~<base> {"top":{...},"nodes":{"<index or handler>":{...}}}
//
// A change record builds on the earlier line whose CRC-64 (ECMA) checksum is
// base, in 16 hex digits. It holds every run-level field under "top" and only
// the nodes whose encoding differs from that line under "nodes", keyed by step
// index or by handler field name. A change record is not JSON, so a reader that
// skips lines it cannot decode skips it.
package statusfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/crc64"
	"io"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// changePrefix starts a change record.
const changePrefix = '~'

// baseDigits is how many hex digits hold the base checksum of a change record.
const baseDigits = 16

// changeHeaderSize is the size of the prefix, the base and the space after it.
const changeHeaderSize = 1 + baseDigits + 1

var crcTable = crc64.MakeTable(crc64.ECMA)

var errMalformedChange = errors.New("malformed change record")

// Latest is the newest status a status file holds.
type Latest struct {
	Status *ir.DAGRunStatus
	// OnlyRecord reports that the file holds this status as its one full record
	// and nothing else.
	OnlyRecord bool
}

// ReadLatest returns the newest status in the first size bytes of r that can be
// read completely. A last line without its newline is still being written and
// is ignored, as are lines that cannot be decoded and change records that do
// not lead back to a full record. It returns io.EOF when no line holds a
// status.
func ReadLatest(ctx context.Context, r io.ReaderAt, size int64) (Latest, error) {
	end := size
	for {
		s := scan{size: size, next: end}
		if err := fileutil.ReadLinesBackward(ctx, r, end, s.line); err != nil {
			return Latest{}, err
		}
		if s.latest.Status != nil {
			return s.latest, nil
		}
		if len(s.chain) == 0 {
			return Latest{}, io.EOF
		}
		// The newest change record of the chain cannot be read, so the line
		// before it is the newest that may be.
		end = s.head
	}
}

// scan is one pass backward over the lines of a status file. It stops at the
// first line that can be read, or at a chain of change records it cannot read.
type scan struct {
	size    int64 // size of the whole file
	next    int64 // offset where the next line, going backward, ends
	started bool
	chain   []change // change records being followed, newest first
	head    int64    // offset of the newest record in chain
	latest  Latest
}

// line handles one line of the file, last first.
func (s *scan) line(line []byte, terminated bool) (bool, error) {
	lineEnd := s.next
	if !s.started {
		s.started = true
		if terminated {
			lineEnd--
		}
	}
	start := lineEnd - int64(len(line))
	s.next = start - 1

	if !terminated || len(bytes.TrimSpace(line)) == 0 {
		return false, nil
	}
	if len(s.chain) > 0 {
		return s.follow(line)
	}
	if line[0] == changePrefix {
		if c, err := parseChange(line); err == nil {
			s.chain = append(s.chain, c)
			s.head = start
		}
		return false, nil
	}
	var status ir.DAGRunStatus
	if err := json.Unmarshal(line, &status); err != nil {
		return false, nil
	}
	s.latest = Latest{Status: &status, OnlyRecord: start == 0 && lineEnd == s.size-1}
	return true, nil
}

// follow handles a line that comes before the oldest change record in the
// chain. Lines other than the one it builds on are passed over.
func (s *scan) follow(line []byte) (bool, error) {
	if crc64.Checksum(line, crcTable) != s.chain[len(s.chain)-1].base {
		return false, nil
	}
	if line[0] == changePrefix {
		c, err := parseChange(line)
		if err != nil {
			return true, nil
		}
		s.chain = append(s.chain, c)
		return false, nil
	}
	status, err := rebuild(line, s.chain)
	if err != nil {
		return true, nil
	}
	s.latest = Latest{Status: status}
	return true, nil
}

// change is a decoded change record.
type change struct {
	base  uint64
	top   json.RawMessage
	nodes map[string]json.RawMessage
}

func parseChange(line []byte) (change, error) {
	if len(line) < changeHeaderSize || line[changeHeaderSize-1] != ' ' {
		return change{}, errMalformedChange
	}
	base, err := strconv.ParseUint(string(line[1:changeHeaderSize-1]), 16, 64)
	if err != nil {
		return change{}, err
	}
	var body struct {
		Top   json.RawMessage            `json:"top"`
		Nodes map[string]json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(line[changeHeaderSize:], &body); err != nil {
		return change{}, err
	}
	if len(body.Top) == 0 || body.Top[0] != '{' {
		return change{}, errMalformedChange
	}
	return change{base: base, top: body.Top, nodes: body.Nodes}, nil
}

// rebuild returns the status that chain, newest first, records on top of the
// full record line.
func rebuild(line []byte, chain []change) (*ir.DAGRunStatus, error) {
	p, err := partsOfRecord(line)
	if err != nil {
		return nil, err
	}
	for _, c := range slices.Backward(chain) {
		if err := p.apply(c.nodes); err != nil {
			return nil, err
		}
	}
	// Each change record holds every run-level field.
	p.top = chain[0].top
	var status ir.DAGRunStatus
	if err := json.Unmarshal(p.record(), &status); err != nil {
		return nil, err
	}
	return &status, nil
}
