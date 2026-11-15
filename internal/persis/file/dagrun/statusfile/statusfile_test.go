// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package statusfile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun/statusfile"
)

// TestReadLatestMatchesFullRecords writes random histories in which change
// records mix with other writers' full records, cut-off writes, blank lines and
// compactions, and then damages a line. After each step, ReadLatest must return
// the status of the newest line that leads back to a full record, decoded as
// that status's full record would be.
func TestReadLatestMatchesFullRecords(t *testing.T) {
	var changes, chained int
	for seed := range uint64(60) {
		h := newHistory(t, seed)
		for range 40 {
			h.step()
			h.check()
		}
		h.damage()
		h.check()
		changes += h.changes
		chained += h.chained
	}
	// Without change records, and chains of them, the test proves nothing.
	require.Positive(t, changes)
	require.Positive(t, chained)
}

func TestReadLatest(t *testing.T) {
	st := testStatus(2)
	full := storedLine(t, st)
	older := storedLine(t, ir.DAGRunStatus{Name: "etl", DAGRunID: "run-1", Status: ir.Queued})

	t.Run("Empty", func(t *testing.T) {
		_, err := readLatest(nil)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("BlankAndCutOffLines", func(t *testing.T) {
		_, err := readLatest([]byte("\n \n" + string(full)))
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("OnlyRecord", func(t *testing.T) {
		got, err := readLatest(file(full))
		require.NoError(t, err)
		require.Equal(t, decoded(t, st), got.Status)
		require.True(t, got.OnlyRecord)
	})

	t.Run("NotOnlyRecord", func(t *testing.T) {
		for name, data := range map[string][]byte{
			"BlankLineAfter": append(file(full), '\n'),
			"CutOffAfter":    append(file(full), full[:10]...),
			"RecordBefore":   file(older, full),
		} {
			t.Run(name, func(t *testing.T) {
				got, err := readLatest(data)
				require.NoError(t, err)
				require.Equal(t, decoded(t, st), got.Status)
				require.False(t, got.OnlyRecord)
			})
		}
	})

	t.Run("ChangeRecord", func(t *testing.T) {
		var enc statusfile.Encoder
		first := encode(t, &enc, testStatus(10))
		next := testStatus(10)
		next.Nodes[1].Status = ir.NodeRunning
		change := encode(t, &enc, next)
		require.Equal(t, byte('~'), change[0])

		// Another writer's line between them is not part of the chain.
		got, err := readLatest(file(first, older, change))
		require.NoError(t, err)
		require.Equal(t, decoded(t, next), got.Status)
		require.False(t, got.OnlyRecord)
	})

	t.Run("MissingBase", func(t *testing.T) {
		got, err := readLatest(file(older, changeLine([]byte("gone"), validBody(t, st))))
		require.NoError(t, err)
		require.Equal(t, decodedLine(t, older), got.Status)
	})

	// A change record that cannot be decoded is skipped like any other line
	// that cannot be.
	t.Run("MalformedChangeRecord", func(t *testing.T) {
		for name, line := range map[string][]byte{
			"Short":        []byte("~12"),
			"NoSpace":      fmt.Appendf(nil, "~%016x%s", checksum(older), validBody(t, st)),
			"BadChecksum":  []byte("~zzzzzzzzzzzzzzzz " + validBody(t, st)),
			"BadJSON":      changeLine(older, `{"top":`),
			"TopNotObject": changeLine(older, `{"top":1,"nodes":{}}`),
			"NoTop":        changeLine(older, `{"nodes":{}}`),
		} {
			t.Run(name, func(t *testing.T) {
				got, err := readLatest(file(older, line))
				require.NoError(t, err)
				require.Equal(t, decodedLine(t, older), got.Status)
			})
		}
	})

	// A change record that names nodes its base lacks, or that rebuilds into
	// a record that cannot be decoded, leaves the line before it the newest.
	t.Run("UnusableChangeRecord", func(t *testing.T) {
		for name, body := range map[string]string{
			"IndexPastEnd":   `{"top":{"name":"etl"},"nodes":{"2":{}}}`,
			"NegativeIndex":  `{"top":{"name":"etl"},"nodes":{"-1":{}}}`,
			"UnknownKey":     `{"top":{"name":"etl"},"nodes":{"step":{}}}`,
			"MissingHandler": `{"top":{"name":"etl"},"nodes":{"onExit":{}}}`,
			"BadTopField":    `{"top":{"pid":"x"},"nodes":{}}`,
		} {
			t.Run(name, func(t *testing.T) {
				got, err := readLatest(file(older, full, changeLine(full, body)))
				require.NoError(t, err)
				require.Equal(t, decoded(t, st), got.Status)
			})
		}
	})

	t.Run("UnreadableChainRecord", func(t *testing.T) {
		broken := changeLine(full, `{"top":`)
		got, err := readLatest(file(full, broken, changeLine(broken, validBody(t, st))))
		require.NoError(t, err)
		require.Equal(t, decoded(t, st), got.Status)
	})

	t.Run("UnreadableBase", func(t *testing.T) {
		for name, base := range map[string][]byte{
			"NotObject":       []byte(`[1]`),
			"NodesNotAnArray": []byte(`{"name":"etl","nodes":5}`),
		} {
			t.Run(name, func(t *testing.T) {
				got, err := readLatest(file(older, base, changeLine(base, validBody(t, st))))
				require.NoError(t, err)
				require.Equal(t, decodedLine(t, older), got.Status)
			})
		}
	})

	t.Run("EmptyTop", func(t *testing.T) {
		got, err := readLatest(file(full, changeLine(full, `{"top":{},"nodes":{}}`)))
		require.NoError(t, err)
		require.Equal(t, decoded(t, ir.DAGRunStatus{Nodes: st.Nodes}), got.Status)
	})

	t.Run("Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		data := file(full)
		_, err := statusfile.ReadLatest(ctx, bytes.NewReader(data), int64(len(data)))
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("ReadError", func(t *testing.T) {
		readErr := errors.New("read failed")
		_, err := statusfile.ReadLatest(context.Background(), failingReader{readErr}, 10)
		require.ErrorIs(t, err, readErr)
	})
}

func TestEncoder(t *testing.T) {
	t.Run("FirstLineIsFullRecord", func(t *testing.T) {
		var enc statusfile.Encoder
		st := testStatus(3)
		require.Equal(t, decoded(t, st), decodedLine(t, encode(t, &enc, st)))
	})

	t.Run("ChangeRecordHoldsChangedNodes", func(t *testing.T) {
		var enc statusfile.Encoder
		st := testStatus(10)
		st.OnExit = ir.NewNodeFromStep(ir.Step{Name: "onExit"})
		first := encode(t, &enc, st)
		st.Nodes[3].Status = ir.NodeRunning
		st.OnExit.Status = ir.NodeRunning
		st.Status = ir.Failed

		top, nodes := parseChange(t, encode(t, &enc, st), first)
		require.ElementsMatch(t, []string{"3", "onExit"}, slices.Collect(maps.Keys(nodes)))
		require.Equal(t, float64(ir.Failed), top["status"])
		require.NotContains(t, top, "nodes")
		require.NotContains(t, top, "onExit")
	})

	t.Run("UnchangedStatus", func(t *testing.T) {
		var enc statusfile.Encoder
		st := testStatus(10)
		first := encode(t, &enc, st)
		_, nodes := parseChange(t, encode(t, &enc, st), first)
		require.Empty(t, nodes)
	})

	t.Run("NewShapeIsFullRecord", func(t *testing.T) {
		for name, reshape := range map[string]func(*ir.DAGRunStatus){
			"NodeAdded":      func(st *ir.DAGRunStatus) { st.Nodes = append(st.Nodes, ir.NewNodeFromStep(ir.Step{Name: "added"})) },
			"NodeRemoved":    func(st *ir.DAGRunStatus) { st.Nodes = st.Nodes[1:] },
			"HandlerAdded":   func(st *ir.DAGRunStatus) { st.OnWait = ir.NewNodeFromStep(ir.Step{Name: "onWait"}) },
			"HandlerRemoved": func(st *ir.DAGRunStatus) { st.OnExit = nil },
		} {
			t.Run(name, func(t *testing.T) {
				var enc statusfile.Encoder
				st := testStatus(10)
				st.OnExit = ir.NewNodeFromStep(ir.Step{Name: "onExit"})
				encode(t, &enc, st)
				reshape(&st)
				require.Equal(t, decoded(t, st), decodedLine(t, encode(t, &enc, st)))
			})
		}
	})

	t.Run("ResetStartsFullRecord", func(t *testing.T) {
		var enc statusfile.Encoder
		st := testStatus(10)
		encode(t, &enc, st)
		enc.Reset()
		require.Equal(t, decoded(t, st), decodedLine(t, encode(t, &enc, st)))
	})

	t.Run("ChainStaysWithinHalfAFullRecord", func(t *testing.T) {
		var enc statusfile.Encoder
		st := testStatus(20)
		var fullSize, chainSize, fulls int
		for i := range 200 {
			st.Nodes[i%len(st.Nodes)].RetryCount++
			line := encode(t, &enc, st)
			if line[0] != '~' {
				fullSize, chainSize = len(line), 0
				fulls++
				continue
			}
			chainSize += len(line)
			require.LessOrEqual(t, chainSize, fullSize/2)
		}
		require.Greater(t, fulls, 1)
	})

	t.Run("UnencodableStatus", func(t *testing.T) {
		bad := &ir.Node{HumanTaskInput: json.RawMessage("{")}
		for name, st := range map[string]ir.DAGRunStatus{
			"Top":     {AutoRetryBackoff: math.NaN()},
			"Node":    {Nodes: []*ir.Node{bad}},
			"Handler": {OnExit: bad},
		} {
			t.Run(name, func(t *testing.T) {
				var enc statusfile.Encoder
				_, err := enc.Encode(st)
				require.Error(t, err)
			})
		}
	})
}

func FuzzReadLatest(f *testing.F) {
	for seed := range uint64(4) {
		h := newHistory(f, seed)
		for range 40 {
			h.step()
		}
		f.Add(h.data)
	}
	f.Add([]byte("~0000000000000000 {\"top\":{}}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := readLatest(data)
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			return
		}
		require.NotNil(t, got.Status)
	})
}

// history simulates a status file written by a runner through an Encoder,
// with the other lines that real files hold, and knows what each line records.
type history struct {
	t     testing.TB
	seed  uint64
	r     *rand.Rand
	enc   statusfile.Encoder
	st    ir.DAGRunStatus
	data  []byte
	lines []historyLine
	// lastChange reports that the runner's last line was a change record.
	lastChange       bool
	changes, chained int
}

// historyLine is a line of the file. Status is what the line records, nil for
// a line that no reader can decode. Base is the checksum that a change record
// builds on.
type historyLine struct {
	start, end int // end excludes the newline
	status     *ir.DAGRunStatus
	base       *uint64
}

func newHistory(t testing.TB, seed uint64) *history {
	r := rand.New(rand.NewPCG(seed, 0))
	h := &history{t: t, seed: seed, r: r, st: testStatus(r.IntN(12))}
	for _, field := range handlers(&h.st) {
		if r.IntN(2) == 0 {
			*field = ir.NewNodeFromStep(ir.Step{Name: "handler"})
		}
	}
	return h
}

func (h *history) step() {
	switch n := h.r.IntN(100); {
	case n < 60:
		h.mutate()
		h.write()
	case n < 68:
		h.foreign()
	case n < 74:
		h.mutate()
		h.cutOffWrite()
	case n < 78:
		h.appendLine(nil, nil, nil)
	case n < 83:
		h.compact()
	case n < 88:
		h.enc.Reset()
		h.lastChange = false
	default:
		h.write()
	}
}

func (h *history) mutate() {
	ts := fmt.Sprintf("2026-11-15T00:%02d:%02dZ", h.r.IntN(60), h.r.IntN(60))
	switch n := h.r.IntN(100); {
	case n < 70:
		nodes := slices.Clone(h.st.Nodes)
		for _, field := range handlers(&h.st) {
			if *field != nil {
				nodes = append(nodes, *field)
			}
		}
		if len(nodes) == 0 {
			return
		}
		node := nodes[h.r.IntN(len(nodes))]
		node.Status = ir.NodeStatus(h.r.IntN(5))
		node.StartedAt = ts
		node.RetryCount++
		if h.r.IntN(3) == 0 {
			// Outputs that repeat each other are stored once per node.
			out := fmt.Sprintf("<out %d & more>", h.r.IntN(1000))
			outs, stepOuts := out, out
			node.OutputValue, node.OutputsValue, node.StepOutputsValue = &out, &outs, &stepOuts
		}
	case n < 85:
		h.st.Status = ir.Status(h.r.IntN(5))
		h.st.FinishedAt = ts
		h.st.Error = fmt.Sprintf("error %d", h.r.IntN(1000))
	case n < 90:
		h.st.Nodes = append(h.st.Nodes, ir.NewNodeFromStep(ir.Step{Name: "added-" + ts}))
	case n < 93 && len(h.st.Nodes) > 0:
		h.st.Nodes = h.st.Nodes[:len(h.st.Nodes)-1]
	default:
		field := handlers(&h.st)[h.r.IntN(6)]
		if *field == nil {
			*field = ir.NewNodeFromStep(ir.Step{Name: "handler-" + ts})
		} else {
			*field = nil
		}
	}
}

// write appends the runner's next line.
func (h *history) write() {
	line := encode(h.t, &h.enc, h.st)
	var base *uint64
	if line[0] == '~' {
		require.False(h.t, json.Valid(line), "a change record must not be JSON")
		sum, err := strconv.ParseUint(string(line[1:17]), 16, 64)
		require.NoError(h.t, err)
		base = &sum
		h.changes++
		if h.lastChange {
			h.chained++
		}
	}
	h.lastChange = base != nil
	h.appendLine(line, decoded(h.t, h.st), base)
}

// foreign appends a full record from another writer, such as an approval
// that changes a node, in the format of an older or of the current writer.
func (h *history) foreign() {
	other := h.st
	other.Status = ir.Queued
	other.Error = fmt.Sprintf("other writer %d", h.r.IntN(1000))
	if len(other.Nodes) > 0 {
		other.Nodes = slices.Clone(other.Nodes)
		i := h.r.IntN(len(other.Nodes))
		node := *other.Nodes[i]
		node.ApprovedBy = fmt.Sprintf("user-%d", h.r.IntN(1000))
		other.Nodes[i] = &node
	}
	line := storedLine(h.t, other)
	if h.r.IntN(2) == 0 {
		var enc statusfile.Encoder
		line = encode(h.t, &enc, other)
	}
	h.appendLine(line, decoded(h.t, other), nil)
}

// cutOffWrite appends part of the runner's next line, as a crash or a full disk
// leaves it. The writer then starts over with a full record.
func (h *history) cutOffWrite() {
	line := encode(h.t, &h.enc, h.st)
	h.enc.Reset()
	h.lastChange = false
	cut := h.r.IntN(len(line))
	if cut == 0 {
		return
	}
	h.endPartialLine()
	start := len(h.data)
	h.data = append(h.data, line[:cut]...)
	h.lines = append(h.lines, historyLine{start: start, end: len(h.data)})
}

// compact replaces the file with one full record of its newest status.
func (h *history) compact() {
	latest, _ := h.expected()
	h.data, h.lines = nil, nil
	h.enc.Reset()
	h.lastChange = false
	if latest == nil {
		return
	}
	var enc statusfile.Encoder
	h.appendLine(encode(h.t, &enc, *latest), decoded(h.t, *latest), nil)
}

// damage makes one line that a reader could decode unreadable, and may leave a
// cut-off line at the end.
func (h *history) damage() {
	var candidates []int
	for i, l := range h.lines {
		if l.status != nil {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) > 0 {
		i := candidates[h.r.IntN(len(candidates))]
		h.data[h.lines[i].start] = '#'
		h.lines[i].status = nil
	}
	if h.r.IntN(2) == 0 {
		h.cutOffWrite()
	}
}

func (h *history) appendLine(line []byte, status *ir.DAGRunStatus, base *uint64) {
	h.endPartialLine()
	start := len(h.data)
	h.data = append(h.data, line...)
	h.lines = append(h.lines, historyLine{start: start, end: len(h.data), status: status, base: base})
	h.data = append(h.data, '\n')
}

// endPartialLine ends a cut-off line before the next is appended, as the
// writer does.
func (h *history) endPartialLine() {
	if len(h.data) > 0 && h.data[len(h.data)-1] != '\n' {
		h.data = append(h.data, '\n')
	}
}

// expected returns the status of the newest line that leads back to a full
// record, and whether that line is the file's one full record.
func (h *history) expected() (*ir.DAGRunStatus, bool) {
	resolved := make([]bool, len(h.lines))
	newest := -1
	for j, l := range h.lines {
		if l.status == nil || l.end == len(h.data) {
			continue
		}
		if l.base != nil {
			i := h.nearestLine(j, *l.base)
			if i < 0 || !resolved[i] {
				continue
			}
		}
		resolved[j] = true
		newest = j
	}
	if newest < 0 {
		return nil, false
	}
	l := h.lines[newest]
	return l.status, len(h.lines) == 1 && l.base == nil && l.end == len(h.data)-1
}

// nearestLine returns the last line before line j whose checksum is sum, or -1.
func (h *history) nearestLine(j int, sum uint64) int {
	for i := j - 1; i >= 0; i-- {
		l := h.lines[i]
		if checksum(h.data[l.start:l.end]) == sum {
			return i
		}
	}
	return -1
}

func (h *history) check() {
	h.t.Helper()
	want, only := h.expected()
	got, err := readLatest(h.data)
	if want == nil {
		require.ErrorIs(h.t, err, io.EOF, "seed %d", h.seed)
		return
	}
	require.NoError(h.t, err, "seed %d", h.seed)
	require.Equal(h.t, want, got.Status, "seed %d", h.seed)
	require.Equal(h.t, only, got.OnlyRecord, "seed %d", h.seed)
}

func handlers(st *ir.DAGRunStatus) [6]**ir.Node {
	return [...]**ir.Node{&st.OnInit, &st.OnExit, &st.OnSuccess, &st.OnFailure, &st.OnAbort, &st.OnWait}
}

// testStatus returns a running status with the given number of steps.
func testStatus(steps int) ir.DAGRunStatus {
	st := ir.DAGRunStatus{
		Name:      "etl",
		DAGRunID:  "run-1",
		AttemptID: "att-1",
		Status:    ir.Running,
		StartedAt: "2026-11-15T00:00:00Z",
	}
	for i := range steps {
		name := fmt.Sprintf("step-%d", i)
		node := ir.NewNodeFromStep(ir.Step{Name: name, Dir: "/srv/work/" + name})
		node.Stdout = "/var/log/dagu/etl/" + name + ".out"
		node.Stderr = "/var/log/dagu/etl/" + name + ".err"
		st.Nodes = append(st.Nodes, node)
	}
	return st
}

// storedLine encodes st as a writer of full records only does.
func storedLine(t testing.TB, st ir.DAGRunStatus) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(ir.StoredStatus(st)))
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// decoded returns st as a reader of its full record sees it.
func decoded(t testing.TB, st ir.DAGRunStatus) *ir.DAGRunStatus {
	t.Helper()
	return decodedLine(t, storedLine(t, st))
}

func decodedLine(t testing.TB, line []byte) *ir.DAGRunStatus {
	t.Helper()
	var st ir.DAGRunStatus
	require.NoError(t, json.Unmarshal(line, &st))
	return &st
}

func encode(t testing.TB, enc *statusfile.Encoder, st ir.DAGRunStatus) []byte {
	t.Helper()
	line, err := enc.Encode(st)
	require.NoError(t, err)
	return line
}

// parseChange decodes a change record and checks that it builds on base.
func parseChange(t *testing.T, line, base []byte) (map[string]any, map[string]json.RawMessage) {
	t.Helper()
	header := fmt.Sprintf("~%016x ", checksum(base))
	require.Equal(t, header, string(line[:len(header)]))
	var body struct {
		Top   map[string]any             `json:"top"`
		Nodes map[string]json.RawMessage `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(line[len(header):], &body))
	return body.Top, body.Nodes
}

// validBody returns the body of a change record that records st on a base of
// the same shape and changes no node.
func validBody(t *testing.T, st ir.DAGRunStatus) string {
	t.Helper()
	top := st
	top.Nodes = nil
	return fmt.Sprintf(`{"top":%s,"nodes":{}}`, storedLine(t, top))
}

func changeLine(base []byte, body string) []byte {
	return fmt.Appendf(nil, "~%016x %s", checksum(base), body)
}

func checksum(line []byte) uint64 {
	return crc64.Checksum(line, crc64.MakeTable(crc64.ECMA))
}

// file joins lines into a status file.
func file(lines ...[]byte) []byte {
	var data []byte
	for _, line := range lines {
		data = append(append(data, line...), '\n')
	}
	return data
}

func readLatest(data []byte) (statusfile.Latest, error) {
	return statusfile.ReadLatest(context.Background(), bytes.NewReader(data), int64(len(data)))
}

type failingReader struct{ err error }

func (r failingReader) ReadAt([]byte, int64) (int, error) {
	return 0, r.err
}
