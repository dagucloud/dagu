// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/stretchr/testify/require"
)

func TestDAGRunInvalidatorRefreshesListsOnlyForLifecycleEvents(t *testing.T) {
	tests := []struct {
		name              string
		eventType         eventstore.EventType
		currentStatus     ir.Status
		filterStatus      ir.Status
		wantListRefreshes int64
	}{
		{
			name:              "progress update",
			eventType:         eventstore.TypeDAGRunUpdated,
			currentStatus:     ir.Running,
			filterStatus:      ir.Running,
			wantListRefreshes: 0,
		},
		{
			name:              "lifecycle update",
			eventType:         eventstore.TypeDAGRunRunning,
			currentStatus:     ir.Running,
			filterStatus:      ir.Running,
			wantListRefreshes: 1,
		},
		{
			name:              "human task resumed",
			eventType:         eventstore.TypeDAGRunQueued,
			currentStatus:     ir.Queued,
			filterStatus:      ir.Waiting,
			wantListRefreshes: 1,
		},
		{
			name:              "failed auto retry canceled",
			eventType:         eventstore.TypeDAGRunAborted,
			currentStatus:     ir.Aborted,
			filterStatus:      ir.Failed,
			wantListRefreshes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewMultiplexer(StreamConfig{}, nil)
			t.Cleanup(mux.Shutdown)

			var detailRefreshes atomic.Int64
			mux.RegisterFetcher(TopicTypeDAGRun, func(_ context.Context, _ string) (any, error) {
				detailRefreshes.Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAGRun, TopicRefreshModeOnDemand)

			var listRefreshes atomic.Int64
			mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, _ string) (any, error) {
				listRefreshes.Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

			// Activity has no status filter, so every lifecycle event wakes it.
			var activityRefreshes atomic.Int64
			mux.RegisterFetcher(TopicTypeDAGRunActivity, func(_ context.Context, _ string) (any, error) {
				activityRefreshes.Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAGRunActivity, TopicRefreshModeOnDemand)

			result, err := mux.createSession(
				context.Background(),
				httptest.NewRecorder(),
				[]string{
					"dagrun:test/run-1",
					"dagruns:status=" + strconv.Itoa(int(tt.filterStatus)),
					"dagrunactivity:hours=24",
				},
				0,
			)
			require.NoError(t, err)
			require.NotNil(t, result.session)
			defer mux.removeSession(result.session)

			status := &ir.DAGRunStatus{
				Name:      "test",
				DAGRunID:  "run-1",
				AttemptID: "attempt-1",
				Status:    tt.currentStatus,
			}
			event := eventstore.NewDAGRunEvent(
				eventstore.Source{Service: eventstore.SourceServiceServer},
				tt.eventType,
				status,
				nil,
			)

			wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{event}, nil)

			require.Eventually(t, func() bool {
				return detailRefreshes.Load() == 1 &&
					listRefreshes.Load() == tt.wantListRefreshes &&
					activityRefreshes.Load() == tt.wantListRefreshes
			}, time.Second, 10*time.Millisecond)
			if tt.wantListRefreshes == 0 {
				require.Never(t, func() bool {
					return listRefreshes.Load() != 0 || activityRefreshes.Load() != 0
				}, 200*time.Millisecond, 20*time.Millisecond)
			}
		})
	}
}

// A run event names the DAG file without its extension, while a page opened
// as /dags/<name>.yaml subscribes with the extension. Both forms of the name
// must be woken; another DAG must not be.
func TestDAGRunInvalidatorWakesDAGFileTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var counts sync.Map
	count := func(key string) int64 {
		counter, ok := counts.Load(key)
		if !ok {
			return 0
		}
		return counter.(*atomic.Int64).Load()
	}
	for _, topicType := range []TopicType{TopicTypeDAG, TopicTypeDAGHistory} {
		mux.RegisterFetcher(topicType, func(_ context.Context, identifier string) (any, error) {
			counter, _ := counts.LoadOrStore(string(topicType)+":"+identifier, &atomic.Int64{})
			counter.(*atomic.Int64).Add(1)
			return nil, nil
		})
		mux.SetRefreshMode(topicType, TopicRefreshModeOnDemand)
	}

	woken := []string{"dag:test", "dag:test.yaml", "daghistory:test.yaml", "daghistory:test.YML", "daghistory:test?includeBackfill=true"}
	other := []string{"dag:other.yaml", "daghistory:other"}
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), append(woken, other...), 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	event := eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer},
		eventstore.TypeDAGRunSucceeded,
		&ir.DAGRunStatus{
			Name:      "test",
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
			Status:    ir.Succeeded,
		},
		map[string]any{eventstore.DAGFileNameDataKey: "test"},
	)
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{event}, nil)

	require.Eventually(t, func() bool {
		for _, key := range woken {
			if count(key) != 1 {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		for _, key := range other {
			if count(key) != 0 {
				return true
			}
		}
		return false
	}, 200*time.Millisecond, 20*time.Millisecond)
}

// A backfill run's event wakes the topic of its backfill only.
func TestDAGRunInvalidatorWakesBackfillTopic(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var counts sync.Map
	count := func(id string) int64 {
		counter, ok := counts.Load(id)
		if !ok {
			return 0
		}
		return counter.(*atomic.Int64).Load()
	}
	mux.RegisterFetcher(TopicTypeBackfill, func(_ context.Context, identifier string) (any, error) {
		counter, _ := counts.LoadOrStore(identifier, &atomic.Int64{})
		counter.(*atomic.Int64).Add(1)
		return nil, nil
	})
	mux.SetRefreshMode(TopicTypeBackfill, TopicRefreshModeOnDemand)
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{"backfill:bf123456", "backfill:bf654321"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	event := eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer},
		eventstore.TypeDAGRunSucceeded,
		&ir.DAGRunStatus{Name: "etl", DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Succeeded, BackfillID: "bf123456"},
		nil,
	)
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{event}, nil)

	require.Eventually(t, func() bool { return count("bf123456") == 1 }, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return count("bf654321") != 0 }, 200*time.Millisecond, 20*time.Millisecond)
}

// An API write names a DAG file as its request did, with or without a YAML
// extension; it wakes the topics of the file in either form. A file named
// only by its extension is a DAG file too.
func TestWakeTopicMatchesDAGFileForms(t *testing.T) {
	tests := []struct {
		name   string
		wake   string
		topics []string
		woken  []string
	}{
		{
			name:   "WithExtension",
			wake:   "test.yaml",
			topics: []string{"test", "test.yaml", "other"},
			woken:  []string{"test", "test.yaml"},
		},
		{
			name:   "ExtensionOnly",
			wake:   ".yaml",
			topics: []string{".yaml", ".yml", "yaml", "other"},
			woken:  []string{".yaml", ".yml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewMultiplexer(StreamConfig{}, nil)
			t.Cleanup(mux.Shutdown)

			var counts sync.Map
			count := func(key string) int64 {
				counter, ok := counts.Load(key)
				if !ok {
					return 0
				}
				return counter.(*atomic.Int64).Load()
			}
			mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
				counter, _ := counts.LoadOrStore(identifier, &atomic.Int64{})
				counter.(*atomic.Int64).Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)

			keys := make([]string, 0, len(tt.topics))
			for _, topic := range tt.topics {
				keys = append(keys, "dag:"+topic)
			}
			result, err := mux.createSession(context.Background(), httptest.NewRecorder(), keys, 0)
			require.NoError(t, err)
			require.NotNil(t, result.session)
			defer mux.removeSession(result.session)

			mux.WakeTopic(TopicTypeDAG, tt.wake)

			require.Eventually(t, func() bool {
				for _, topic := range tt.woken {
					if count(topic) != 1 {
						return false
					}
				}
				return true
			}, time.Second, 10*time.Millisecond)
			require.Never(t, func() bool {
				for _, topic := range tt.topics {
					if !slices.Contains(tt.woken, topic) && count(topic) != 0 {
						return true
					}
				}
				return false
			}, 200*time.Millisecond, 20*time.Millisecond)
		})
	}
}

func TestDAGRunInvalidatorBatchesAndTargetsLifecycleListRefreshes(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var counts sync.Map
	mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, identifier string) (any, error) {
		counter, _ := counts.LoadOrStore(identifier, &atomic.Int64{})
		counter.(*atomic.Int64).Add(1)
		return nil, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

	tests := []struct {
		topic         string
		wantRefreshes int64
	}{
		{topic: "dagruns:status=0&status=5", wantRefreshes: 1},
		{topic: "dagruns:status=1", wantRefreshes: 1},
		{topic: "dagruns:status=7", wantRefreshes: 1},
		{topic: "dagruns:status=4&status=6"},
		{topic: "dagruns:status=2&status=3&status=8"},
		{topic: "dagruns:status=1%2C4", wantRefreshes: 1},
		{topic: "dagruns:status=4%2C6"},
		{topic: "dagruns:status=+1+", wantRefreshes: 1},
		{topic: "dagruns:status=running", wantRefreshes: 1},
		{topic: "dagruns:status=999", wantRefreshes: 1},
		{topic: "dagruns:workspace=all", wantRefreshes: 1},
	}
	topics := make([]string, 0, len(tests))
	identifiers := make([]string, 0, len(tests))
	for _, tt := range tests {
		topics = append(topics, tt.topic)
		parsed, err := ParseTopic(tt.topic)
		require.NoError(t, err)
		identifiers = append(identifiers, parsed.Identifier)
	}
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), topics, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	events := make([]*eventstore.Event, 0, 2)
	for _, runID := range []string{"run-1", "run-2"} {
		events = append(events, eventstore.NewDAGRunEvent(
			eventstore.Source{Service: eventstore.SourceServiceServer},
			eventstore.TypeDAGRunRunning,
			&ir.DAGRunStatus{
				Name:      "test",
				DAGRunID:  runID,
				AttemptID: "attempt-1",
				Status:    ir.Running,
			},
			nil,
		))
	}

	wakeTopicsForDAGRunEvents(mux, events, nil)

	count := func(identifier string) int64 {
		counter, ok := counts.Load(identifier)
		if !ok {
			return 0
		}
		return counter.(*atomic.Int64).Load()
	}
	require.Eventually(t, func() bool {
		for i, tt := range tests {
			if tt.wantRefreshes > 0 && count(identifiers[i]) != tt.wantRefreshes {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		for i, tt := range tests {
			if count(identifiers[i]) != tt.wantRefreshes {
				return true
			}
		}
		return false
	}, 200*time.Millisecond, 20*time.Millisecond)
}

// fakeWakeClock drives a wakeLimiter by hand.
type fakeWakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeWakeTimer
}

type fakeWakeTimer struct {
	at      time.Time
	fire    func()
	stopped bool
}

func (c *fakeWakeClock) time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeWakeClock) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeWakeTimer{at: c.now.Add(d), fire: f}
	c.timers = append(c.timers, timer)
	return func() bool { timer.stopped = true; return true }
}

// advance moves the time and fires the timers that are due, in order.
func (c *fakeWakeClock) advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeWakeTimer
		for _, timer := range c.timers {
			if !timer.stopped && !timer.at.After(target) && (next == nil || timer.at.Before(next.at)) {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.stopped = true
		c.now = next.at
		c.mu.Unlock()
		next.fire()
	}
}

// The runs of a service DAG wake the DAG list at most once in a window. A
// wake inside the window fires at its end. The DAG topics, and the list for a
// normal DAG, wake at once.
func TestDAGRunInvalidatorLimitsServiceDAGWakes(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)
	clock := &fakeWakeClock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	mux.serviceWakes.now = clock.time
	mux.serviceWakes.after = clock.after

	var lists, dags, histories atomic.Int64
	for topicType, counter := range map[TopicType]*atomic.Int64{
		TopicTypeDAGsList: &lists, TopicTypeDAG: &dags, TopicTypeDAGHistory: &histories,
	} {
		mux.RegisterFetcher(topicType, func(_ context.Context, _ string) (any, error) {
			counter.Add(1)
			return nil, nil
		})
		mux.SetRefreshMode(topicType, TopicRefreshModeOnDemand)
	}
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(),
		[]string{"dagslist:page=1", "dag:svc.yaml", "daghistory:svc.yaml"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)
	newEvent := func(runID string, data map[string]any) *eventstore.Event {
		return eventstore.NewDAGRunEvent(
			eventstore.Source{Service: eventstore.SourceServiceServer},
			eventstore.TypeDAGRunSucceeded,
			&ir.DAGRunStatus{Name: "svc", DAGRunID: runID, AttemptID: "a", Status: ir.Succeeded},
			data,
		)
	}
	service := map[string]any{eventstore.DAGFileNameDataKey: "svc", eventstore.ServiceDAGDataKey: true}
	normal := map[string]any{eventstore.DAGFileNameDataKey: "svc"}
	// Each event wakes the DAG topics; the list counts as given.
	waitCounts := func(list, events int64) {
		t.Helper()
		require.Eventually(t, func() bool {
			return lists.Load() == list && dags.Load() == events && histories.Load() == events
		}, time.Second, 10*time.Millisecond)
	}
	holdList := func(want int64) {
		t.Helper()
		require.Never(t, func() bool { return lists.Load() != want }, 150*time.Millisecond, 20*time.Millisecond)
	}

	// The first wake passes. The two next ones stay pending, as one wake.
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r1", service)}, nil)
	waitCounts(1, 1)
	clock.advance(3 * time.Second)
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r2", service)}, nil)
	waitCounts(1, 2)
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r3", service)}, nil)
	waitCounts(1, 3)
	holdList(1)

	// The pending wake fires at the end of the window of the first wake.
	clock.advance(6 * time.Second)
	holdList(1)
	clock.advance(time.Second)
	waitCounts(2, 3)

	// A wake after the window passes at once.
	clock.advance(10 * time.Second)
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r4", service)}, nil)
	waitCounts(3, 4)

	// A normal DAG wakes at once, also inside the window.
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r5", normal)}, nil)
	waitCounts(4, 5)

	// A known set of service DAGs without svc wins over the flag of the event.
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{newEvent("r6", service)}, map[string]struct{}{})
	waitCounts(5, 6)
}

// countingTopics subscribes to the topics, all of one type, and returns the
// multiplexer with a count of the refetches of each topic.
func countingTopics(t *testing.T, topicType TopicType, topics ...string) (*Multiplexer, func(topic string) int64) {
	t.Helper()
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)
	var counts sync.Map
	mux.RegisterFetcher(topicType, func(_ context.Context, identifier string) (any, error) {
		counter, _ := counts.LoadOrStore(identifier, &atomic.Int64{})
		counter.(*atomic.Int64).Add(1)
		return nil, nil
	})
	mux.SetRefreshMode(topicType, TopicRefreshModeOnDemand)
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), topics, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	t.Cleanup(func() { mux.removeSession(result.session) })
	return mux, func(topic string) int64 {
		parsed, err := ParseTopic(topic)
		require.NoError(t, err)
		counter, ok := counts.Load(parsed.Identifier)
		if !ok {
			return 0
		}
		return counter.(*atomic.Int64).Load()
	}
}

// runEvent is an event of a run of name with the given type, under the root
// run of rootName when it is set.
func runEvent(eventType eventstore.EventType, status ir.Status, name, runID, rootName string) *eventstore.Event {
	runStatus := &ir.DAGRunStatus{
		Name:      name,
		DAGRunID:  runID,
		AttemptID: "attempt-1",
		Status:    status,
		// A hand start, so only the DAG marks the run as a service DAG run.
		TriggerType: ir.TriggerTypeManual,
	}
	if rootName != "" {
		runStatus.Root = ir.NewDAGRunRef(rootName, "root-run")
	}
	return eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceServer}, eventType, runStatus, nil)
}

var serviceDAGSet = map[string]struct{}{"svc": {}}

// A run of a service DAG changes only the lists that show service DAG runs,
// so a service DAG that restarts all the time does not refresh the lists
// that leave them out, whoever started the run.
func TestDAGRunInvalidatorSkipsServiceDAGRunsForListsThatExcludeThem(t *testing.T) {
	const (
		withServiceDAGs    = "dagruns:status=1"
		withoutServiceDAGs = "dagruns:status=1&excludeServiceDAGRuns=true"
		// The default run list has no status filter.
		withoutServiceDAGsAnyStatus = "dagruns:excludeServiceDAGRuns=true"
	)
	mux, refreshes := countingTopics(t, TopicTypeDAGRuns, withServiceDAGs, withoutServiceDAGs, withoutServiceDAGsAnyStatus)

	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{runEvent(eventstore.TypeDAGRunRunning, ir.Running, "svc", "run-1", "")}, serviceDAGSet)
	require.Eventually(t, func() bool { return refreshes(withServiceDAGs) == 1 }, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		return refreshes(withoutServiceDAGs) != 0 || refreshes(withoutServiceDAGsAnyStatus) != 0
	}, 200*time.Millisecond, 20*time.Millisecond)

	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{runEvent(eventstore.TypeDAGRunRunning, ir.Running, "job", "run-2", "")}, serviceDAGSet)
	require.Eventually(t, func() bool {
		return refreshes(withServiceDAGs) == 2 && refreshes(withoutServiceDAGs) == 1 && refreshes(withoutServiceDAGsAnyStatus) == 1
	}, time.Second, 10*time.Millisecond)
}

// The activity summary skips service DAG runs on the same terms as the lists.
func TestDAGRunInvalidatorSkipsServiceDAGRunsForActivityThatExcludesThem(t *testing.T) {
	const (
		withServiceDAGs    = "dagrunactivity:hours=24"
		withoutServiceDAGs = "dagrunactivity:hours=24&excludeServiceDAGRuns=true"
	)
	mux, refreshes := countingTopics(t, TopicTypeDAGRunActivity, withServiceDAGs, withoutServiceDAGs)
	failed := func(name, runID string) *eventstore.Event {
		return runEvent(eventstore.TypeDAGRunFailed, ir.Failed, name, runID, "")
	}

	// A batch that mixes both kinds still reaches the topic that excludes.
	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{failed("svc", "run-1"), failed("job", "run-2")}, serviceDAGSet)
	require.Eventually(t, func() bool {
		return refreshes(withServiceDAGs) == 1 && refreshes(withoutServiceDAGs) == 1
	}, time.Second, 10*time.Millisecond)

	wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{failed("svc", "run-3")}, serviceDAGSet)
	require.Eventually(t, func() bool { return refreshes(withServiceDAGs) == 2 }, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return refreshes(withoutServiceDAGs) != 1 }, 200*time.Millisecond, 20*time.Millisecond)
}

// The service DAG list changes only with the runs of the service DAGs, so a busy
// server with other DAGs does not refetch it. A sub-run counts as its root
// DAG, and when the names cannot be read every run wakes the list.
func TestDAGRunInvalidatorWakesServiceDAGListOnlyForItsDAGs(t *testing.T) {
	const topic = "servicedags:"
	mux, refreshes := countingTopics(t, TopicTypeServiceDAGs, topic)
	running := func(name, rootName string) []*eventstore.Event {
		return []*eventstore.Event{runEvent(eventstore.TypeDAGRunRunning, ir.Running, name, "run-"+name, rootName)}
	}

	wakeTopicsForDAGRunEvents(mux, running("job", ""), serviceDAGSet)
	require.Never(t, func() bool { return refreshes(topic) != 0 }, 200*time.Millisecond, 20*time.Millisecond)

	wakeTopicsForDAGRunEvents(mux, running("svc", ""), serviceDAGSet)
	require.Eventually(t, func() bool { return refreshes(topic) == 1 }, time.Second, 10*time.Millisecond)

	wakeTopicsForDAGRunEvents(mux, running("child", "svc"), serviceDAGSet)
	require.Eventually(t, func() bool { return refreshes(topic) == 2 }, time.Second, 10*time.Millisecond)

	wakeTopicsForDAGRunEvents(mux, running("job", ""), nil)
	require.Eventually(t, func() bool { return refreshes(topic) == 3 }, time.Second, 10*time.Millisecond)
}

// After each read of events, the root runs whose state changed are told, in
// event order, also when no event came, which tells that the events are read.
// A sub-DAG run is not told, and neither is an update that keeps the state of
// a run, such as a step change. A snapshot keeps the DAG file of its event.
func TestHandleDAGRunEventsTellsTheChangedRuns(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)
	var names [][]string
	var files []string
	changed := func(changes []*eventstore.DAGRunStatusSnapshot) {
		var told []string
		for _, change := range changes {
			told = append(told, change.Name)
			files = append(files, change.DAGFile)
		}
		names = append(names, told)
	}
	withFile := eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceServer},
		eventstore.TypeDAGRunFailed, &ir.DAGRunStatus{Name: "d", DAGRunID: "r5", AttemptID: "attempt-1", Status: ir.Failed},
		map[string]any{eventstore.DAGFileNameDataKey: "d.yaml"})

	handleDAGRunEvents(mux, []*eventstore.Event{
		runEvent(eventstore.TypeDAGRunRunning, ir.Running, "a", "r1", ""),
		runEvent(eventstore.TypeDAGRunSucceeded, ir.Succeeded, "b", "root-run", "b"),
		runEvent(eventstore.TypeDAGRunSucceeded, ir.Succeeded, "a", "r1", ""),
		runEvent(eventstore.TypeDAGRunRunning, ir.Running, "child", "c1", "a"),
		runEvent(eventstore.TypeDAGRunUpdated, ir.Running, "c", "r4", ""),
		withFile,
	}, nil, changed)
	handleDAGRunEvents(mux, nil, nil, changed)

	require.Equal(t, [][]string{{"a", "b", "a", "d"}, nil}, names)
	require.Equal(t, []string{"", "", "", "d.yaml"}, files)
}

// A batch with a lifecycle event of a root run wakes the change feed. A batch
// with no event, or with only updates that keep the state of runs or only
// sub-DAG runs, does not.
func TestHandleDAGRunEventsWakesRunChanges(t *testing.T) {
	const topic = "dagrunchanges:all"
	mux, refreshes := countingTopics(t, TopicTypeDAGRunChanges, topic)

	for _, events := range [][]*eventstore.Event{
		nil,
		{runEvent(eventstore.TypeDAGRunUpdated, ir.Running, "a", "r1", "")},
		{runEvent(eventstore.TypeDAGRunRunning, ir.Running, "child", "c1", "a")},
	} {
		handleDAGRunEvents(mux, events, nil, nil)
	}
	require.Never(t, func() bool { return refreshes(topic) != 0 }, 200*time.Millisecond, 20*time.Millisecond)

	handleDAGRunEvents(mux, []*eventstore.Event{runEvent(eventstore.TypeDAGRunSucceeded, ir.Succeeded, "a", "r1", "")}, nil, nil)
	require.Eventually(t, func() bool { return refreshes(topic) == 1 }, time.Second, 10*time.Millisecond)
}

// A batch of connection events tells of a change once, and the run
// events of the batch still reach the run topics.
func TestConnectionEventsTellChangeOnce(t *testing.T) {
	mux, _ := countingTopics(t, TopicTypeConnectionAttention, "connectionattention:all")
	source := eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"}
	change := eventstore.ConnectionChange{Connections: []eventstore.ConnectionName{{Workspace: "ops", Name: "work"}}}
	run := runEvent(eventstore.TypeDAGRunSucceeded, ir.Succeeded, "a", "r1", "")
	var told int

	remaining := wakeForConnectionEvents(mux, []*eventstore.Event{
		eventstore.NewConnectionEvent(source, eventstore.TypeConnectionNeedsAttention, change, time.Now()),
		run,
		eventstore.NewConnectionEvent(source, eventstore.TypeConnectionRestored, change, time.Now()),
	}, func() { told++ })

	require.Equal(t, []*eventstore.Event{run}, remaining)
	require.Equal(t, 1, told)
}

// The invalidator reads connection events from the event store and wakes
// the attention topic after telling of the change.
func TestInvalidatorWakesConnectionAttention(t *testing.T) {
	const topic = "connectionattention:all"
	mux, refreshes := countingTopics(t, TopicTypeConnectionAttention, topic)
	store, err := fileeventstore.New(t.TempDir())
	require.NoError(t, err)
	service := eventstore.New(store)
	var told atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	StartDAGRunEventInvalidation(ctx, service, mux, nil, nil, func() { told.Add(1) }, nil, 10*time.Millisecond)

	source := eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"}
	change := eventstore.ConnectionChange{Connections: []eventstore.ConnectionName{{Workspace: "ops", Name: "work"}}}
	// The invalidator starts at the head of the store, so an event recorded
	// before it bootstraps is not read; record until one is.
	require.Eventually(t, func() bool {
		require.NoError(t, service.Emit(context.Background(),
			eventstore.NewConnectionEvent(source, eventstore.TypeConnectionNeedsAttention, change, time.Now())))
		return refreshes(topic) > 0
	}, 2*time.Second, 50*time.Millisecond)
	require.Positive(t, told.Load())
}
