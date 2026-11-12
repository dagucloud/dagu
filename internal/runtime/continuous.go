// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// A continuous step is a step that repeats without a limit. See
// specs/083-continuous-runs.md.
const (
	// continuousStatusInterval is the longest time between two status writes
	// while a continuous step repeats without a change of state.
	continuousStatusInterval = time.Minute
	// continuousSummaryInterval is the time between two summary log lines.
	continuousSummaryInterval = time.Hour
	// sampleMessageMax is the longest message of a sample, in bytes.
	sampleMessageMax = 256
	// stderrTailMax is the count of bytes that the runner reads to find the
	// last line of the standard error of an execution.
	stderrTailMax = 4096
)

// isContinuousStep reports whether the repeat policy of a step has a repeat
// mode and no limit. The limit is the resolved one.
func isContinuousStep(step ir.Step) bool {
	rp := step.RepeatPolicy
	return rp.RepeatMode != "" && rp.Limit <= 0
}

// sampleRecorderOpener opens the store of samples of one step.
type sampleRecorderOpener func(ctx context.Context, dir string, opts samples.Options) (samples.Recorder, error)

// continuousStep holds the state that the runner keeps for one continuous
// node during one run of the node. The goroutine of the node owns it.
type continuousStep struct {
	runner *Runner
	name   string
	dir    string // directory of the sample store; empty when no store

	rec      samples.Recorder
	recState recorderState
	// recordErrs counts the samples in a row that the store failed to record.
	recordErrs uint64

	// execStart is the start of the execution that is running.
	execStart time.Time

	// lastWrite is the time of the last status write for this node.
	lastWrite time.Time
	// writeDue is set when the state changed since the last write.
	writeDue bool
	// seen is true after the first execution; failed is the outcome of the last.
	seen   bool
	failed bool
	// failedSince is the start of the current outage; failedCount its count.
	failedSince time.Time
	failedCount uint64

	// Counters of the current summary window.
	summaryAt time.Time
	samples   uint64
	failures  uint64

	logs boundedLogs
}

type recorderState int

const (
	recorderUnopened recorderState = iota
	recorderOpen
	recorderOff
)

// newContinuousStep returns the state of a continuous node, or nil when the
// node does not repeat without a limit.
func (r *Runner) newContinuousStep(ctx context.Context, node *Node) *continuousStep {
	if r.dry || !isContinuousStep(node.Step()) {
		return nil
	}
	node.SetContinuous(true)
	c := &continuousStep{runner: r, name: node.Name(), summaryAt: r.clock()}
	if r.attemptDir != "" {
		c.dir = samples.Dir(r.attemptDir, node.Name())
	}
	logger.Info(ctx, "Continuous step started",
		slog.Duration("interval", node.Step().RepeatPolicy.Interval),
	)
	return c
}

// beforeExecution prepares the logs of the node for an execution.
func (c *continuousStep) beforeExecution(node *Node) {
	if c == nil {
		return
	}
	c.execStart = c.runner.clock()
	// The output of an execution that succeeded does not stay once the next
	// one has started. Executions that failed stay.
	c.logs.begin(node.outputs.localLogPaths(), c.seen && !c.failed)
}

// afterExecution records the sample of an execution that ended, and decides
// whether the status must be written at once. A stop is not a sample.
func (c *continuousStep) afterExecution(ctx context.Context, node *Node, execErr error) {
	if c == nil {
		return
	}
	now := c.runner.clock()
	paths := node.outputs.localLogPaths()
	defer c.logs.end(paths)

	if c.runner.isCanceled() || node.State().Status == ir.NodeAborted {
		return
	}

	exitCode := node.State().ExitCode
	var setupErr *stepSetupError
	switch {
	case errors.As(execErr, &setupErr):
		exitCode = -1
	case execErr != nil && exitCode == 0:
		exitCode = 1
	}
	sample := samples.Sample{
		Time:     c.execStart.UTC().Truncate(time.Millisecond),
		Duration: now.Sub(c.execStart),
		ExitCode: exitCode,
	}
	if sample.Failed() {
		sample.Message = c.logs.lastStderrLine(node.outputs.localStderrPath())
		if sample.Message == "" && execErr != nil {
			sample.Message = truncateUTF8(firstLine(node.outputs.maskSecrets(execErr.Error())), sampleMessageMax)
		}
	}
	c.record(ctx, sample)
	c.track(ctx, sample, now)
}

// record writes the sample to the store. The store opens at the first sample.
// A store that cannot open is skipped for the rest of the run of the node.
func (c *continuousStep) record(ctx context.Context, s samples.Sample) {
	ctx = context.WithoutCancel(ctx)
	if c.recState == recorderUnopened {
		c.recState = recorderOff
		switch c.dir {
		case "":
			logger.Debug(ctx, "No sample store for this run; samples are not recorded")
		default:
			open := c.runner.openRecorder
			if open == nil {
				open = samples.OpenRecorder
			}
			rec, err := open(ctx, c.dir, samples.Options{Now: c.runner.clock})
			if err != nil {
				logger.Warn(ctx, "Samples are not recorded", tag.Error(err))
				break
			}
			c.rec, c.recState = rec, recorderOpen
		}
	}
	if c.recState != recorderOpen {
		return
	}
	if err := c.rec.Record(ctx, s); err != nil {
		// A store that fails, such as on a full disk, fails for each sample:
		// warn once until it records again.
		if c.recordErrs == 0 {
			logger.Warn(ctx, "Failed to record sample", tag.Error(err))
		} else {
			logger.Debug(ctx, "Failed to record sample", tag.Error(err))
		}
		c.recordErrs++
		return
	}
	if c.recordErrs > 0 {
		logger.Info(ctx, "Samples are recorded again", slog.Uint64("failed-samples", c.recordErrs))
		c.recordErrs = 0
	}
}

// track updates the outage state, the counters and the scheduler log.
func (c *continuousStep) track(ctx context.Context, s samples.Sample, now time.Time) {
	failed := s.Failed()
	c.samples++
	if failed {
		c.failures++
	}
	switch {
	case failed && !c.failed:
		c.failedSince, c.failedCount = s.Time, 0
		c.writeDue = true
		logger.Info(ctx, "Continuous step failed",
			tag.ExitCode(s.ExitCode), slog.String("message", s.Message))
	case !failed && c.failed:
		c.writeDue = true
		logger.Info(ctx, "Continuous step recovered",
			slog.Uint64("failed-samples", c.failedCount),
			slog.Duration("outage", now.Sub(c.failedSince)))
	}
	if failed {
		c.failedCount++
	}
	c.seen, c.failed = true, failed

	logger.Debug(ctx, "Continuous step executed",
		tag.ExitCode(s.ExitCode), slog.Duration("duration", s.Duration))

	if now.Sub(c.summaryAt) >= continuousSummaryInterval {
		logger.Info(ctx, "Continuous step summary",
			slog.Uint64("samples", c.samples), slog.Uint64("failed-samples", c.failures))
		c.summaryAt, c.samples, c.failures = now, 0, 0
	}
}

// retried marks a retry of the execution: the status is written at once.
func (c *continuousStep) retried() {
	if c != nil {
		c.writeDue = true
	}
}

// reportDue reports whether the status must be written now, and starts the
// next interval when it must.
func (c *continuousStep) reportDue() bool {
	if c == nil {
		return true
	}
	now := c.runner.clock()
	if !c.writeDue && !c.lastWrite.IsZero() && now.Sub(c.lastWrite) < continuousStatusInterval {
		return false
	}
	c.writeDue = false
	c.lastWrite = now
	return true
}

// close flushes the store and records the stop of the step.
func (c *continuousStep) close(ctx context.Context) {
	if c == nil {
		return
	}
	logger.Info(ctx, "Continuous step stopped",
		slog.Uint64("samples", c.samples), slog.Uint64("failed-samples", c.failures))
	if c.recState != recorderOpen {
		return
	}
	if err := c.rec.Close(context.WithoutCancel(ctx)); err != nil {
		logger.Warn(ctx, "Failed to close sample store", tag.Error(err))
	}
	c.recState = recorderOff
}

// Step log bound: 4 segments of 2 MiB.
const (
	logSegmentSize = 2 << 20
	logSegments    = 4
	logMaxSize     = logSegmentSize * logSegments
)

// boundedLogs keeps the step log files of a continuous step within logMaxSize.
// The files are the ones that the node writes to in append mode, so the
// changes are in place: a cut at the end, and a move of the tail to the start.
// The methods run between two executions, when nothing writes to the files.
type boundedLogs struct {
	// starts maps a file to its size when the last execution started.
	starts map[string]int64
}

// begin notes the start of an execution. When dropLast is true it first removes
// the output that the previous execution wrote.
func (b *boundedLogs) begin(paths []string, dropLast bool) {
	if b.starts == nil {
		b.starts = make(map[string]int64)
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		size := info.Size()
		if start, ok := b.starts[p]; ok && dropLast && start < size {
			if os.Truncate(p, start) == nil {
				size = start
			}
		}
		b.starts[p] = size
	}
}

// end drops the oldest segment of a file that is larger than logMaxSize.
func (b *boundedLogs) end(paths []string) {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.Size() <= logMaxSize {
			continue
		}
		cut, err := dropHead(p, info.Size()-(logMaxSize-logSegmentSize))
		if err != nil {
			continue
		}
		if start, ok := b.starts[p]; ok {
			b.starts[p] = max(0, start-cut)
		}
	}
}

// lastStderrLine returns the last non empty line that the current execution
// wrote to the file, cut to sampleMessageMax bytes.
func (b *boundedLogs) lastStderrLine(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	from := max(info.Size()-stderrTailMax, b.starts[path], 0)
	buf := make([]byte, info.Size()-from)
	if _, err := f.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\r\n \t"), []byte("\n"))
	line := strings.TrimSpace(string(lines[len(lines)-1]))
	return truncateUTF8(line, sampleMessageMax)
}

// dropHead removes the first bytes of a file, rounded up to the next line
// start, and returns the count removed. The file is rewritten in place.
func dropHead(path string, want int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	cut := min(want, info.Size())
	// Start the kept data at a line start when a newline is close.
	probe := make([]byte, 64<<10)
	if n, _ := f.ReadAt(probe, cut-1); n > 0 {
		if i := bytes.IndexByte(probe[:n], '\n'); i >= 0 {
			cut += int64(i)
		}
	}
	cut = min(cut, info.Size())
	buf := make([]byte, 1<<20)
	var in, out int64 = cut, 0
	for in < info.Size() {
		n, err := f.ReadAt(buf, in)
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], out); werr != nil {
				return 0, werr
			}
			in += int64(n)
			out += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
	}
	return cut, f.Truncate(out)
}

// localLogPaths returns the log files that the node writes to on this host,
// without a repeat. It returns none when the log goes to a remote writer.
func (oc *OutputCoordinator) localLogPaths() []string {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	var paths []string
	if oc.stdoutFile != nil && oc.stdoutFileName != "" {
		paths = append(paths, oc.stdoutFileName)
	}
	if oc.stderrFile != nil && oc.stderrFileName != "" && oc.stderrFileName != oc.stdoutFileName {
		paths = append(paths, oc.stderrFileName)
	}
	return paths
}

// localStderrPath returns the file that holds the standard error, when local.
func (oc *OutputCoordinator) localStderrPath() string {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	switch {
	case oc.stderrFile != nil:
		return oc.stderrFileName
	case oc.stdoutFile != nil:
		return oc.stdoutFileName // merged log
	}
	return ""
}

// maskSecrets replaces the secret values in s, as in the step log.
func (oc *OutputCoordinator) maskSecrets(s string) string {
	oc.mu.Lock()
	defer oc.mu.Unlock()

	if oc.masker == nil {
		return s
	}
	return oc.masker.MaskString(s)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
