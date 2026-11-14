// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Command synth fills the sample store of a continuous step with years of
// synthetic data, and creates the DAG and the run that the web UI needs to
// show it.
//
// Usage:
//
//	go run ./internal/samples/cmd/synth --dir <dagu home> --dag <name> \
//	    --years 3 --interval 1s --seed 1 [--status running|succeeded]
//
// The directory is a Dagu home: the command writes <dir>/dags/<name>.yaml (one
// continuous step, named ping) and one run under <dir>/data/dag-runs. Start the
// server with DAGU_HOME=<dir> and open the run of the DAG.
//
// The data covers the last --years years, ending now. The older part is written
// into the minute, hour and day tiers directly, the last 26 hours are recorded
// through a Recorder with a clock that the command moves, so that the raw tier
// and the tiers have the layout of a real run. The latency is about 40 ms with
// a daily rhythm and rare spikes. The availability is 99.9x percent, with
// isolated failures and a few outages of seconds, minutes and hours.
//
// With --status succeeded the Recorder is closed (a stopped event), and the run
// is a finished run. With --status running (the default) the store is flushed
// and left open as a run that is still going.
//
// The run ID is "synth-<seed>". The command stops with an error when the run
// or the store exists already.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

const stepName = "ping"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synth:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dir      = flag.String("dir", "", "Dagu home directory (required)")
		dagName  = flag.String("dag", "", "name of the DAG (required)")
		years    = flag.Float64("years", 3, "years of data, ending now")
		interval = flag.Duration("interval", time.Second, "time between two samples: a divisor of one minute or whole minutes")
		seed     = flag.Uint64("seed", 1, "seed of the random generator")
		status   = flag.String("status", "running", "status of the run: running or succeeded")
	)
	flag.Parse()
	if *dir == "" || *dagName == "" {
		return fmt.Errorf("--dir and --dag are required")
	}
	if *status != "running" && *status != "succeeded" {
		return fmt.Errorf("--status must be running or succeeded, got %q", *status)
	}
	if *years <= 0 || *years > 30 {
		return fmt.Errorf("--years must be in (0, 30], got %v", *years)
	}
	// Backfill checks the interval and the window too, but only after the DAG
	// and the run are written.
	iv := *interval
	if iv <= 0 || iv%time.Millisecond != 0 {
		return fmt.Errorf("--interval must be a positive whole count of milliseconds, got %v", iv)
	}
	if iv < time.Minute && time.Minute%iv != 0 || iv >= time.Minute && iv%time.Minute != 0 {
		return fmt.Errorf("--interval must divide a minute or be a whole count of minutes, got %v", iv)
	}
	ctx := context.Background()
	begin := time.Now()

	now := time.Now().UTC().Truncate(time.Second)
	rawStart := now.Add(-26 * time.Hour).Truncate(time.Hour)
	start := now.Add(-time.Duration(*years * 365 * 24 * float64(time.Hour))).Truncate(time.Hour)
	runID := fmt.Sprintf("synth-%d", *seed)
	if !start.Before(rawStart) {
		return fmt.Errorf("--years %v leaves no history before the last 26 hours", *years)
	}

	dagFile := filepath.Join(*dir, "dags", *dagName+".yaml")
	if err := writeDAG(dagFile, *dagName, *interval); err != nil {
		return err
	}
	attemptDir, attempt, dag, err := createRun(ctx, *dir, *dagName, dagFile, runID, start, *interval)
	if err != nil {
		return err
	}
	storeDir := samples.Dir(attemptDir, stepName)

	g := newGenerator(*seed, start, now.Add(time.Hour))
	var total int64
	count := func(ms int64) (time.Duration, int, string) {
		total++
		return g.next(ms)
	}
	err = samples.Backfill(ctx, storeDir, samples.BackfillOptions{
		Start: start, End: rawStart, Interval: *interval, Now: now, Next: count,
	})
	if err != nil {
		return err
	}
	clock := rawStart
	rec, err := samples.OpenRecorder(ctx, storeDir, samples.Options{Now: func() time.Time { return clock }})
	if err != nil {
		return err
	}
	for clock = rawStart; clock.Before(now); clock = clock.Add(*interval) {
		d, exit, msg := count(clock.UnixMilli())
		if err := rec.Record(ctx, samples.Sample{Time: clock, Duration: d, ExitCode: exit, Message: msg}); err != nil {
			return err
		}
	}
	clock = now
	if *status == "succeeded" {
		err = rec.Close(ctx)
	} else {
		err = rec.Flush(ctx)
	}
	if err != nil {
		return err
	}
	if err := writeStatus(ctx, attempt, dag, runID, *status, start, now, total); err != nil {
		return err
	}
	fmt.Printf("dag:     %s\nrun:     %s\nstore:   %s\nsamples: %d\ntime:    %s\n",
		dagFile, runID, storeDir, total, time.Since(begin).Round(time.Millisecond))
	return nil
}

func writeDAG(path, name string, interval time.Duration) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s exists already", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	sec := max(int(interval/time.Second), 1)
	body := fmt.Sprintf(`name: %s
steps:
  - name: %s
    command: curl -fsS --max-time 5 http://localhost:8080/api/v1/health
    repeat_policy:
      repeat: while
      interval_sec: %d
`, name, stepName, sec)
	return os.WriteFile(path, []byte(body), 0o600)
}

// createRun creates the run and its attempt, and returns the directory of the
// attempt. The caller writes the status later, with the node count.
// nolint:revive
func createRun(ctx context.Context, home, name, dagFile, runID string, started time.Time, interval time.Duration) (string, *filedagrun.Attempt, *ir.DAG, error) {
	dag := &ir.DAG{
		Name:     name,
		Location: dagFile,
		Steps: []ir.Step{{
			Name: stepName,
			Commands: []ir.CommandEntry{{
				Command:     "curl",
				Args:        []string{"-fsS", "--max-time", "5", "http://localhost:8080/api/v1/health"},
				CmdWithArgs: "curl -fsS --max-time 5 http://localhost:8080/api/v1/health",
			}},
			RepeatPolicy: ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile, Interval: interval},
		}},
	}
	runsDir := filepath.Join(home, "data", "dag-runs")
	store := filedagrun.NewStore(runsDir)
	a, err := store.CreateAttempt(ctx, persis.DAGRunCreateAttemptRequest{DAG: dag, Timestamp: started, DAGRunID: runID})
	if err != nil {
		return "", nil, nil, err
	}
	att, ok := a.(*filedagrun.Attempt)
	if !ok {
		return "", nil, nil, fmt.Errorf("unexpected attempt type %T", a)
	}
	if err := att.Open(ctx); err != nil {
		return "", nil, nil, err
	}
	return att.Dir(), att, dag, nil
}

func writeStatus(ctx context.Context, att *filedagrun.Attempt, dag *ir.DAG, runID, status string, started, now time.Time, total int64) error {
	st := ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  runID,
		AttemptID: att.ID(),
		Status:    ir.Running,
		StartedAt: stringutil.FormatTime(started),
		CreatedAt: started.UnixMilli(),
	}
	node := &ir.Node{
		Step:       dag.Steps[0],
		StartedAt:  stringutil.FormatTime(started),
		Status:     ir.NodeRunning,
		DoneCount:  int(total),
		Repeated:   true,
		Continuous: true,
	}
	if status == "succeeded" {
		st.Status = ir.Succeeded
		st.FinishedAt = stringutil.FormatTime(now)
		node.Status = ir.NodeSucceeded
		node.FinishedAt = stringutil.FormatTime(now)
	}
	st.Nodes = []*ir.Node{node}
	if err := att.Write(ctx, st); err != nil {
		return err
	}
	return att.Close(ctx)
}

// generator makes the samples: the latency, the failures and the outages.
type generator struct {
	rng     *rand.Rand
	rhythm  [1440]float64 // factor of the latency for each minute of the day
	outages [][2]int64    // [start, end) in ms, in time order
	cursor  int
}

func newGenerator(seed uint64, from, to time.Time) *generator {
	g := &generator{rng: rand.New(rand.NewPCG(seed, 0x5eed))} //nolint:gosec // seeded synthetic data, not cryptographic
	for m := range g.rhythm {
		// Slowest in the afternoon, fastest at night.
		g.rhythm[m] = 1 + 0.25*math.Sin(2*math.Pi*(float64(m)/1440-14.0/24+0.25))
	}
	// About six outages for each year: two of seconds, three of minutes, one of
	// hours.
	span := to.Sub(from)
	n := max(int(span.Hours()/(365*24)*6+0.5), 3)
	slots := span / time.Duration(n)
	for i := range n {
		var d time.Duration
		switch i % 6 {
		case 0, 3:
			d = time.Duration(3+g.rng.IntN(40)) * time.Second
		case 5:
			d = time.Duration(60+g.rng.IntN(180)) * time.Minute
		default:
			d = time.Duration(2+g.rng.IntN(40)) * time.Minute
		}
		at := from.Add(time.Duration(i)*slots + time.Duration(g.rng.Int64N(int64(slots-d))))
		g.outages = append(g.outages, [2]int64{at.UnixMilli(), at.Add(d).UnixMilli()})
	}
	return g
}

// next gives the sample at a time. The times come in increasing order.
func (g *generator) next(ms int64) (time.Duration, int, string) {
	for g.cursor < len(g.outages) && ms >= g.outages[g.cursor][1] {
		g.cursor++
	}
	if g.cursor < len(g.outages) && ms >= g.outages[g.cursor][0] {
		return g.failure()
	}
	if g.rng.Float64() < 5e-5 {
		return g.failure()
	}
	minuteOfDay := int(ms / 60000 % 1440)
	lat := 40 * g.rhythm[minuteOfDay] * math.Exp(0.12*g.rng.NormFloat64())
	if g.rng.Float64() < 4e-4 {
		lat *= 4 + 26*g.rng.Float64()
	}
	return time.Duration(lat * float64(time.Millisecond)), 0, ""
}

func (g *generator) failure() (time.Duration, int, string) {
	switch g.rng.IntN(4) {
	case 0:
		return 5 * time.Second, 28, "curl: (28) Operation timed out after 5001 milliseconds with 0 bytes received"
	case 1:
		return 38 * time.Millisecond, 22, "curl: (22) The requested URL returned error: 503"
	default:
		return 3 * time.Millisecond, 7, "curl: (7) Failed to connect to localhost port 8080 after 0 ms: Connection refused"
	}
}
