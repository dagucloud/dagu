// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	gort "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock moves only when a test moves it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeRecorder keeps the samples in memory. After stopAfter samples it asks
// the runner to stop, so a test ends a step that has no limit.
type fakeRecorder struct {
	mu        sync.Mutex
	samples   []samples.Sample
	closed    int
	stopAfter int
	stop      func()
	err       error // returned by Record when set
}

func (f *fakeRecorder) Record(_ context.Context, s samples.Sample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.samples = append(f.samples, s)
	if f.stopAfter > 0 && len(f.samples) == f.stopAfter {
		f.stop()
	}
	return nil
}

func (f *fakeRecorder) Flush(context.Context) error { return nil }

func (f *fakeRecorder) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// syncBuffer is a log destination that several goroutines can write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newContinuousStepForTest(clock *fakeClock) *continuousStep {
	r := &Runner{now: clock.now}
	return &continuousStep{runner: r, summaryAt: clock.now()}
}

func observeSample(c *continuousStep, clock *fakeClock, exitCode int) {
	s := samples.Sample{Time: clock.now(), ExitCode: exitCode}
	c.track(context.Background(), s, clock.now())
}

// 1 000 executions, one each second, write the status about once a minute.
func TestContinuousStatusWritesAreCoalesced(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)

	writes := 0
	for range 1000 {
		observeSample(c, clock, 0)
		if c.reportDue() {
			writes++
		}
		clock.advance(time.Second)
	}
	// The first execution, then 1000 s / 60 s.
	assert.InDelta(t, 1+1000/60, writes, 2)
}

func TestContinuousStatusWritesAtOnceOnChange(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)

	observeSample(c, clock, 0)
	require.True(t, c.reportDue(), "the first execution writes")
	clock.advance(time.Second)

	observeSample(c, clock, 0)
	assert.False(t, c.reportDue())
	clock.advance(time.Second)

	observeSample(c, clock, 7) // failed after a success
	assert.True(t, c.reportDue())
	clock.advance(time.Second)

	observeSample(c, clock, 7) // still failed
	assert.False(t, c.reportDue())
	clock.advance(time.Second)

	observeSample(c, clock, 0) // recovered
	assert.True(t, c.reportDue())
	clock.advance(time.Second)

	c.retried()
	assert.True(t, c.reportDue(), "a retry writes")
}

// reportsBeforeRepeatWait returns the count of status writes that the runner
// makes before the wait for the next execution. The wait ends at once, so only
// a write made before it is counted.
func reportsBeforeRepeatWait(c *continuousStep) int {
	node := NewNode(ir.Step{Name: "beat", RepeatPolicy: ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeWhile, Interval: 10 * time.Minute,
	}}, NodeState{Status: ir.NodeFailed})
	progressCh := make(chan ProgressUpdate, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.runner.prepareNodeForRepeat(ctx, node, progressCh, c)
	return len(progressCh)
}

// A failed execution is written before the wait for the next one, not after.
func TestContinuousStatusWritesChangeBeforeTheWait(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)
	observeSample(c, clock, 0)
	require.True(t, c.reportDue())
	clock.advance(time.Second)
	observeSample(c, clock, 7)

	assert.Equal(t, 1, reportsBeforeRepeatWait(c))
}

// A first execution that fails is a change of state too.
func TestContinuousStatusWritesFirstFailureBeforeTheWait(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)
	observeSample(c, clock, 7)

	assert.Equal(t, 1, reportsBeforeRepeatWait(c))
}

// An error that names a secret, such as a URL with a token, is masked in the
// sample and in the scheduler log, as in the step log.
func TestContinuousStepMasksErrorMessage(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)
	rec := &fakeRecorder{}
	c.rec, c.recState = rec, recorderOpen
	logs := &syncBuffer{}
	ctx := logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(logs)))
	node := NewNode(ir.Step{Name: "probe"}, NodeState{})
	node.outputs.masker = masking.NewMasker(masking.SourcedEnvVars{Secrets: []string{"TOKEN=s3cr3t"}})

	c.beforeExecution(node)
	c.afterExecution(ctx, node, errors.New(`Get "http://127.0.0.1:1/?token=s3cr3t": connection refused`))

	require.Len(t, rec.samples, 1)
	assert.Equal(t, 1, rec.samples[0].ExitCode)
	assert.Contains(t, rec.samples[0].Message, "connection refused")
	assert.NotContains(t, rec.samples[0].Message, "s3cr3t")
	assert.NotContains(t, logs.String(), "s3cr3t")
}

// A store that keeps failing, such as on a full disk, warns once, not for each
// sample.
func TestContinuousStepWarnsOnceWhileRecordsFail(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)
	rec := &fakeRecorder{err: errors.New("no space left on device")}
	c.rec, c.recState = rec, recorderOpen
	logs := &syncBuffer{}
	ctx := logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(logs)))

	for range 5 {
		c.record(ctx, samples.Sample{Time: clock.now()})
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "Failed to record sample"))

	rec.err = nil
	c.record(ctx, samples.Sample{Time: clock.now()})
	assert.Contains(t, logs.String(), "Samples are recorded again")
}

// The recovered log counts the failed samples of the outage.
func TestContinuousStepRecoveredLogCountsFailures(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := newContinuousStepForTest(clock)
	logs := &syncBuffer{}
	ctx := logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(logs)))

	for _, exitCode := range []int{0, 7, 7, 0} {
		c.track(ctx, samples.Sample{Time: clock.now(), ExitCode: exitCode}, clock.now())
		clock.advance(time.Second)
	}
	assert.Contains(t, logs.String(), `"failed-samples":2,`)
}

func TestContinuousStepDetection(t *testing.T) {
	cases := []struct {
		name string
		rp   ir.RepeatPolicy
		want bool
	}{
		{"none", ir.RepeatPolicy{}, false},
		{"while", ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile}, true},
		{"until", ir.RepeatPolicy{RepeatMode: ir.RepeatModeUntil}, true},
		{"limit", ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Limit: 3}, false},
		{"limit only", ir.RepeatPolicy{Limit: 3}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isContinuousStep(ir.Step{RepeatPolicy: tc.rp}))
		})
	}
}

// runContinuousNode runs one node of a plan until the fake recorder stops it.
func runContinuousNode(t *testing.T, step ir.Step, rec *fakeRecorder, openCalls *int) (*Node, *syncBuffer, int) {
	t.Helper()
	if gort.GOOS == "windows" {
		t.Skip("uses sh")
	}
	plan, err := NewPlan(step)
	require.NoError(t, err)
	node := plan.GetNodeByName(step.Name)
	require.NotNil(t, node)

	logDir := t.TempDir()
	attemptDir := t.TempDir()
	runner := New(&Config{DAGRunID: "run-1", LogDir: logDir, AttemptDir: attemptDir})
	runner.openRecorder = func(_ context.Context, dir string, _ samples.Options) (samples.Recorder, error) {
		*openCalls++
		assert.Equal(t, samples.Dir(attemptDir, step.Name), dir)
		return rec, nil
	}
	if rec != nil {
		rec.stop = runner.setCanceled
	}

	logs := &syncBuffer{}
	ctx := NewContext(
		logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(logs))),
		&ir.DAG{Name: "continuous", WorkingDir: logDir},
		"run-1",
		filepath.Join(logDir, "dag.log"),
	)
	require.NoError(t, node.Prepare(ctx, logDir, "run-1"))

	// The progress channel counts the status writes.
	progressCh := make(chan ProgressUpdate)
	var reports int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for u := range progressCh {
			reports++
			u.Ack(nil)
		}
	}()

	runner.runNodeExecution(ctx, plan, node, progressCh)
	require.NoError(t, node.Teardown())
	close(progressCh)
	<-done
	return node, logs, reports
}

func shellStep(name, script string, rp ir.RepeatPolicy) ir.Step {
	return ir.Step{Name: name, Script: script, RepeatPolicy: rp}
}

func TestContinuousStepRecordsSamplesAndCoalescesStatus(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "n")
	// The fifth execution fails with a message on the standard error.
	script := fmt.Sprintf(`n=$(cat %[1]s 2>/dev/null || echo 0); n=$((n+1)); echo $n > %[1]s; `+
		`if [ $n -eq 5 ]; then echo "first line" >&2; echo "boom" >&2; exit 3; fi`, counter)
	step := shellStep("beat", script, ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeUntil,
		ExitCode:   []int{99}, // never reached: repeats without end
		Interval:   time.Millisecond,
	})
	rec := &fakeRecorder{stopAfter: 8}
	opens := 0

	node, logs, reports := runContinuousNode(t, step, rec, &opens)

	require.Len(t, rec.samples, 8)
	for i, s := range rec.samples {
		if i == 4 {
			assert.Equal(t, 3, s.ExitCode)
			assert.Equal(t, "boom", s.Message)
			continue
		}
		assert.Equal(t, 0, s.ExitCode, "sample %d", i)
		assert.Empty(t, s.Message)
		assert.Equal(t, s.Time, s.Time.UTC().Truncate(time.Millisecond))
	}
	assert.Equal(t, 1, opens, "the store opens once for the node")
	assert.Equal(t, 1, rec.closed)
	assert.True(t, node.State().Continuous)
	assert.GreaterOrEqual(t, node.State().DoneCount, 8)

	// First execution, the failure, the recovery and the end of the node.
	// Eight reports would show one for each execution.
	assert.GreaterOrEqual(t, reports, 3)
	assert.LessOrEqual(t, reports, 5)

	out := logs.String()
	for _, want := range []string{"Continuous step started", "Continuous step failed",
		"Continuous step recovered", "Continuous step stopped"} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "Repeating step")
	assert.NotContains(t, out, "Step will be repeated")
}

// A failed execution is recorded before the retry wait, so its duration does
// not include the wait.
func TestContinuousStepRecordsBeforeRetryWait(t *testing.T) {
	const retryInterval = 1500 * time.Millisecond
	step := shellStep("probe", "exit 3", ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeUntil,
		ExitCode:   []int{99}, // never reached: repeats without end
		Interval:   time.Millisecond,
	})
	step.RetryPolicy = ir.RetryPolicy{Limit: 1, Interval: retryInterval}
	rec := &fakeRecorder{stopAfter: 1}
	opens := 0

	runContinuousNode(t, step, rec, &opens)

	require.Len(t, rec.samples, 1)
	assert.Equal(t, 3, rec.samples[0].ExitCode)
	assert.Less(t, rec.samples[0].Duration, retryInterval)
}

// A step with a limit keeps today's behavior.
func TestRepeatWithLimitIsNotContinuous(t *testing.T) {
	step := shellStep("limited", "exit 0", ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeWhile,
		Limit:      3,
		Interval:   time.Millisecond,
	})
	opens := 0
	node, logs, reports := runContinuousNode(t, step, nil, &opens)

	assert.Equal(t, 0, opens, "no store opens")
	assert.False(t, node.State().Continuous)
	assert.Equal(t, 3, node.State().DoneCount)
	// One report for each repeat and one at the end.
	assert.GreaterOrEqual(t, reports, 3)
	out := logs.String()
	assert.Contains(t, out, "Repeating step")
	assert.Contains(t, out, "Step will be repeated")
	assert.NotContains(t, out, "Continuous step")
}

func TestContinuousStepContinuesWhenStoreIsNotImplemented(t *testing.T) {
	step := shellStep("beat", "exit 0", ir.RepeatPolicy{
		RepeatMode: ir.RepeatModeWhile,
		Interval:   time.Millisecond,
	})
	plan, err := NewPlan(step)
	require.NoError(t, err)
	node := plan.GetNodeByName(step.Name)
	logDir := t.TempDir()
	runner := New(&Config{DAGRunID: "run-1", LogDir: logDir, AttemptDir: t.TempDir()})
	runs := 0
	runner.openRecorder = func(context.Context, string, samples.Options) (samples.Recorder, error) {
		// Stop the step after the first execution.
		runner.setCanceled()
		runs++
		return nil, samples.ErrNotImplemented
	}
	logs := &syncBuffer{}
	ctx := NewContext(
		logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(logs))),
		&ir.DAG{Name: "continuous", WorkingDir: logDir},
		"run-1", filepath.Join(logDir, "dag.log"),
	)
	require.NoError(t, node.Prepare(ctx, logDir, "run-1"))
	runner.runNodeExecution(ctx, plan, node, nil)
	require.NoError(t, node.Teardown())

	assert.Equal(t, 1, runs, "the store is tried once")
	assert.Contains(t, logs.String(), "Samples are not recorded")
	assert.Contains(t, logs.String(), "Continuous step stopped")
}

// The log of 100 000 executions stays within 4 segments of 2 MiB. It keeps
// the last execution and the failed ones that are in the kept part.
func TestBoundedLogsStayWithinLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "step.out")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var b boundedLogs
	paths := []string{path}
	failedBlock := strings.Repeat("F", 100<<10)
	failed := false
	const executions = 100_000
	for i := 1; i <= executions; i++ {
		b.begin(paths, i > 1 && !failed)
		failed = i%100 == 0
		line := fmt.Sprintf("ok %d\n", i)
		if failed {
			line = fmt.Sprintf("FAILED %d\n%s\n", i, failedBlock)
		}
		_, err := f.WriteString(line)
		require.NoError(t, err)
		b.end(paths)
		if i%1000 == 0 {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.LessOrEqual(t, info.Size(), int64(logMaxSize), "after %d executions", i)
		}
	}

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(data), logMaxSize)
	text := string(data)
	assert.Contains(t, text, fmt.Sprintf("FAILED %d\n", executions), "the last execution stays")
	assert.Contains(t, text, fmt.Sprintf("FAILED %d\n", executions-100), "a recent failure stays")
	assert.NotContains(t, text, "FAILED 100\n", "the oldest segment is dropped")
	assert.NotContains(t, text, "ok ", "the output of a success does not stay")
	assert.True(t, strings.HasPrefix(text, "F") || strings.HasPrefix(text, "FAILED"),
		"the kept data starts at a line")
}

func TestBoundedLogsKeepLastSuccessUntilNextStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "step.out")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var b boundedLogs
	paths := []string{path}

	b.begin(paths, false)
	_, _ = f.WriteString("one\n")
	b.end(paths)
	b.begin(paths, true) // one succeeded: dropped
	_, _ = f.WriteString("two\n")
	b.end(paths)
	data, _ := os.ReadFile(path)
	assert.Equal(t, "two\n", string(data))

	b.begin(paths, false) // two failed: stays
	_, _ = f.WriteString("three\n")
	b.end(paths)
	data, _ = os.ReadFile(path)
	assert.Equal(t, "two\nthree\n", string(data))
}

func TestLastStderrLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "step.err")
	require.NoError(t, os.WriteFile(path, []byte("old\nnew one\n\nnew two  \n\n"), 0o600))
	b := boundedLogs{starts: map[string]int64{path: int64(len("old\n"))}}
	assert.Equal(t, "new two", b.lastStderrLine(path))

	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("é", 400)+"\n"), 0o600))
	line := (&boundedLogs{}).lastStderrLine(path)
	assert.LessOrEqual(t, len(line), sampleMessageMax)
	assert.True(t, strings.HasPrefix(line, "éé"))

	assert.Empty(t, (&boundedLogs{}).lastStderrLine(filepath.Join(t.TempDir(), "none")))
}
