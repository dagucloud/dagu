// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var _ exec.DAGStore = (*mockDAGStore)(nil)

// Mock implementations
type mockDAGStore struct {
	mock.Mock
}

var _ exec.DAGStore = (*mockDAGStore)(nil)

func (m *mockDAGStore) Create(ctx context.Context, fileName string, spec []byte) error {
	args := m.Called(ctx, fileName, spec)
	return args.Error(0)
}

func (m *mockDAGStore) Delete(ctx context.Context, fileName string) error {
	args := m.Called(ctx, fileName)
	return args.Error(0)
}

func (m *mockDAGStore) List(ctx context.Context, params exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(exec.PaginatedResult[*core.DAG]), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) GetMetadata(ctx context.Context, fileName string) (*core.DAG, error) {
	args := m.Called(ctx, fileName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.DAG), args.Error(1)
}

func (m *mockDAGStore) GetDetails(ctx context.Context, fileName string, opts ...spec.LoadOption) (*core.DAG, error) {
	args := m.Called(ctx, fileName, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.DAG), args.Error(1)
}

func (m *mockDAGStore) Grep(ctx context.Context, pattern string) ([]*exec.GrepDAGsResult, []string, error) {
	args := m.Called(ctx, pattern)
	return args.Get(0).([]*exec.GrepDAGsResult), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) SearchCursor(ctx context.Context, opts exec.SearchDAGsOptions) (*exec.CursorResult[exec.SearchDAGResult], []string, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Get(1).([]string), args.Error(2)
	}
	return args.Get(0).(*exec.CursorResult[exec.SearchDAGResult]), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) SearchMatches(ctx context.Context, fileName string, opts exec.SearchDAGMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	args := m.Called(ctx, fileName, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*exec.CursorResult[*exec.Match]), args.Error(1)
}

func (m *mockDAGStore) Rename(ctx context.Context, oldID, newID string) error {
	args := m.Called(ctx, oldID, newID)
	return args.Error(0)
}

func (m *mockDAGStore) GetSpec(ctx context.Context, fileName string) (string, error) {
	args := m.Called(ctx, fileName)
	return args.String(0), args.Error(1)
}

func (m *mockDAGStore) UpdateSpec(ctx context.Context, fileName string, spec []byte) error {
	args := m.Called(ctx, fileName, spec)
	return args.Error(0)
}

func (m *mockDAGStore) LoadSpec(ctx context.Context, spec []byte, opts ...spec.LoadOption) (*core.DAG, error) {
	args := m.Called(ctx, spec, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.DAG), args.Error(1)
}

func (m *mockDAGStore) LabelList(ctx context.Context) ([]string, []string, error) {
	args := m.Called(ctx)
	return args.Get(0).([]string), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) ToggleSuspend(ctx context.Context, fileName string, suspend bool) error {
	args := m.Called(ctx, fileName, suspend)
	return args.Error(0)
}

func (m *mockDAGStore) IsSuspended(ctx context.Context, fileName string) bool {
	args := m.Called(ctx, fileName)
	return args.Bool(0)
}

var _ exec.DAGRunStore = (*mockDAGRunStore)(nil)

type mockDAGRunStore struct {
	mock.Mock
}

var _ exec.DAGRunStore = (*mockDAGRunStore)(nil)

// RemoveDAGRun implements models.DAGRunStore.
func (m *mockDAGRunStore) RemoveDAGRun(_ context.Context, _ exec.DAGRunRef, _ ...exec.RemoveDAGRunOption) error {
	panic("unimplemented")
}

func (m *mockDAGRunStore) CreateAttempt(ctx context.Context, dag *core.DAG, ts time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dag, ts, dagRunID, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RecentAttempts(ctx context.Context, name string, itemLimit int) []exec.DAGRunAttempt {
	args := m.Called(ctx, name, itemLimit)
	return args.Get(0).([]exec.DAGRunAttempt)
}

func (m *mockDAGRunStore) LatestAttempt(ctx context.Context, name string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*exec.DAGRunStatus), args.Error(1)
}

func (m *mockDAGRunStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return exec.DAGRunStatusPage{}, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunStatusPage), args.Error(1)
}

func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	args := m.Called(ctx, dagRun, expectedAttemptID, expectedStatus, mutate)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*exec.DAGRunStatus), args.Bool(1), args.Error(2)
}

func (m *mockDAGRunStore) FindAttempt(ctx context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dagRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) FindSubAttempt(ctx context.Context, dagRun exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dagRun, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, rootRef, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	args := m.Called(ctx, name, retentionDays, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockDAGRunStore) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	args := m.Called(ctx, oldName, newName)
	return args.Error(0)
}

type mockQueueStore struct {
	mock.Mock
}

var _ exec.QueueStore = (*mockQueueStore)(nil)

// QueueWatcher implements execution.QueueStore.
func (m *mockQueueStore) QueueWatcher(_ context.Context) exec.QueueWatcher {
	panic("unimplemented")
}

// QueueList implements execution.QueueStore.
func (m *mockQueueStore) QueueList(_ context.Context) ([]string, error) {
	panic("unimplemented")
}

// ListByDAGName implements models.QueueStore.
func (m *mockQueueStore) ListByDAGName(_ context.Context, _, _ string) ([]exec.QueuedItemData, error) {
	return nil, nil
}

func (m *mockQueueStore) Enqueue(ctx context.Context, name string, priority exec.QueuePriority, dagRun exec.DAGRunRef) error {
	args := m.Called(ctx, name, priority, dagRun)
	return args.Error(0)
}

func (m *mockQueueStore) DequeueByName(ctx context.Context, name string) (exec.QueuedItemData, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	args := m.Called(ctx, name, dagRun)
	return args.Get(0).([]exec.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	args := m.Called(ctx, name, itemIDs)
	return args.Int(0), args.Error(1)
}

func (m *mockQueueStore) Len(ctx context.Context, name string) (int, error) {
	args := m.Called(ctx, name)
	return args.Int(0), args.Error(1)
}

func (m *mockQueueStore) List(ctx context.Context, name string) ([]exec.QueuedItemData, error) {
	args := m.Called(ctx, name)
	return args.Get(0).([]exec.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (exec.CursorResult[exec.QueuedItemData], error) {
	args := m.Called(ctx, name, cursor, limit)
	return args.Get(0).(exec.CursorResult[exec.QueuedItemData]), args.Error(1)
}

func (m *mockQueueStore) All(ctx context.Context) ([]exec.QueuedItemData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]exec.QueuedItemData), args.Error(1)
}

var _ exec.ServiceRegistry = (*mockServiceRegistry)(nil)

type mockServiceRegistry struct {
	mock.Mock
}

var _ exec.ServiceRegistry = (*mockServiceRegistry)(nil)

func (m *mockServiceRegistry) Register(ctx context.Context, serviceName exec.ServiceName, hostInfo exec.HostInfo) error {
	args := m.Called(ctx, serviceName, hostInfo)
	return args.Error(0)
}

func (m *mockServiceRegistry) Unregister(ctx context.Context) {
	m.Called(ctx)
}

func (m *mockServiceRegistry) GetServiceMembers(ctx context.Context, serviceName exec.ServiceName) ([]exec.HostInfo, error) {
	args := m.Called(ctx, serviceName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]exec.HostInfo), args.Error(1)
}

func (m *mockServiceRegistry) UpdateStatus(ctx context.Context, serviceName exec.ServiceName, status exec.ServiceStatus) error {
	args := m.Called(ctx, serviceName, status)
	return args.Error(0)
}

type mockWorkerHeartbeatStore struct {
	records []exec.WorkerHeartbeatRecord
	err     error
}

var _ exec.WorkerHeartbeatStore = (*mockWorkerHeartbeatStore)(nil)

func (m *mockWorkerHeartbeatStore) Upsert(context.Context, exec.WorkerHeartbeatRecord) error {
	panic("unimplemented")
}

func (m *mockWorkerHeartbeatStore) Get(context.Context, string) (*exec.WorkerHeartbeatRecord, error) {
	panic("unimplemented")
}

func (m *mockWorkerHeartbeatStore) List(context.Context) ([]exec.WorkerHeartbeatRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.records, nil
}

func (m *mockWorkerHeartbeatStore) DeleteStale(context.Context, time.Time) (int, error) {
	panic("unimplemented")
}

// Tests

func TestNewCollector(t *testing.T) {
	serviceRegistry := &mockServiceRegistry{}
	serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return([]exec.HostInfo{{Host: "localhost", Status: exec.ServiceStatusActive}}, nil)

	collector := NewCollector(
		"1.0.0",
		&mockDAGStore{},
		&mockDAGRunStore{},
		&mockQueueStore{},
		serviceRegistry,
	)

	assert.NotNil(t, collector)
	assert.Equal(t, "1.0.0", collector.version)
}

func TestCollector_Describe(t *testing.T) {
	collector := NewCollector(
		"1.0.0",
		&mockDAGStore{},
		&mockDAGRunStore{},
		&mockQueueStore{},
		nil,
	)

	ch := make(chan *prometheus.Desc, 24)
	collector.Describe(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}

	// 9 aggregate/info + 5 per-DAG + 5 per-worker + 1 cache metrics
	assert.Equal(t, 20, count)
}

func TestCollector_Collect_BasicMetrics(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{},
		[]string{},
		nil,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{}, nil)

	serviceRegistry := &mockServiceRegistry{}
	serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return([]exec.HostInfo{{Host: "localhost", Status: exec.ServiceStatusActive}}, nil).Maybe()

	collector := NewCollector(
		"1.0.0",
		dagStore,
		dagRunStore,
		queueStore,
		serviceRegistry,
	)

	ch := make(chan prometheus.Metric, 100)
	collector.Collect(ch)
	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}
	assert.Greater(t, metricsCount, 0)
}

func TestCollector_Collect_WithDAGRuns(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Mock DAG store response
	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{
			Items:      []*core.DAG{{}, {}, {}},
			TotalCount: 3,
		},
		[]string{},
		nil,
	)

	// Mock DAG run store response
	statuses := []*exec.DAGRunStatus{
		{Status: core.Succeeded},
		{Status: core.Succeeded},
		{Status: core.Failed},
		{Status: core.Running},
		{Status: core.Queued},
		{Status: core.Aborted},
	}
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return(statuses, nil)

	// Mock queue store response
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{nil, nil}, nil)

	serviceRegistry := &mockServiceRegistry{}
	serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return([]exec.HostInfo{{Host: "localhost", Status: exec.ServiceStatusActive}}, nil).Maybe()

	collector := NewCollector(
		"1.0.0",
		dagStore,
		dagRunStore,
		queueStore,
		serviceRegistry,
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	// Collect metrics
	metrics, err := registry.Gather()
	assert.NoError(t, err)

	// Verify metrics
	metricMap := make(map[string]*dto.MetricFamily)
	for _, m := range metrics {
		metricMap[*m.Name] = m
	}

	// Check dagu_info
	assert.Contains(t, metricMap, "dagu_info")
	assert.Equal(t, float64(1), *metricMap["dagu_info"].Metric[0].Gauge.Value)

	// Check dagu_uptime_seconds
	assert.Contains(t, metricMap, "dagu_uptime_seconds")
	assert.GreaterOrEqual(t, *metricMap["dagu_uptime_seconds"].Metric[0].Gauge.Value, float64(0))

	// Check dagu_scheduler_running
	assert.Contains(t, metricMap, "dagu_scheduler_running")
	assert.Equal(t, float64(1), *metricMap["dagu_scheduler_running"].Metric[0].Gauge.Value)

	// Check dagu_dags_total
	assert.Contains(t, metricMap, "dagu_dags_total")
	assert.Equal(t, float64(3), *metricMap["dagu_dags_total"].Metric[0].Gauge.Value)

	// Check dagu_dag_runs_currently_running
	assert.Contains(t, metricMap, "dagu_dag_runs_currently_running")
	assert.Equal(t, float64(1), *metricMap["dagu_dag_runs_currently_running"].Metric[0].Gauge.Value)

	// Check dagu_dag_runs_queued_total
	assert.Contains(t, metricMap, "dagu_dag_runs_queued_total")
	assert.Equal(t, float64(2), *metricMap["dagu_dag_runs_queued_total"].Metric[0].Gauge.Value)

	// Check dagu_dag_runs_total by status
	assert.Contains(t, metricMap, "dagu_dag_runs_total")
	for _, metric := range metricMap["dagu_dag_runs_total"].Metric {
		for _, label := range metric.Label {
			if *label.Name == "status" {
				switch *label.Value {
				case "succeeded":
					assert.Equal(t, float64(2), *metric.Counter.Value)
				case "failed":
					assert.Equal(t, float64(1), *metric.Counter.Value)
				case "aborted":
					assert.Equal(t, float64(1), *metric.Counter.Value)
				case "running":
					assert.Equal(t, float64(1), *metric.Counter.Value)
				case "queued":
					assert.Equal(t, float64(1), *metric.Counter.Value)
				}
			}
		}
	}
}

func TestCollector_Collect_WithWorkerHeartbeatMetrics(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{},
		[]string{},
		nil,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{}, nil)

	now := time.Now().UTC()
	collector := NewCollector("1.0.0", dagStore, dagRunStore, queueStore, nil)
	collector.now = func() time.Time { return now }
	collector.SetWorkerHeartbeatStore(&mockWorkerHeartbeatStore{
		records: []exec.WorkerHeartbeatRecord{
			{
				WorkerID: "worker-a",
				Labels: map[string]string{
					"pool":   "gpu",
					"region": "ap-northeast-1",
				},
				Stats: &coordinatorv1.WorkerStats{
					TotalPollers: 4,
					BusyPollers:  2,
					RunningTasks: []*coordinatorv1.RunningTask{
						{DagRunId: "run-1", DagName: "dag-1", StartedAt: now.Add(-2 * time.Minute).Unix()},
						{DagRunId: "run-2", DagName: "dag-2", StartedAt: now.Add(-30 * time.Second).Unix()},
					},
				},
				LastHeartbeatAt: now.Add(-2 * time.Second).UnixMilli(),
			},
		},
	})

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	metrics, err := registry.Gather()
	require.NoError(t, err)
	metricMap := metricFamilyMap(metrics)

	assertGaugeValue(t, metricMap["dagu_workers_registered"], nil, 1)
	assertGaugeValue(t, metricMap["dagu_worker_info"], map[string]string{
		"worker_id":   "worker-a",
		"label_name":  "pool",
		"label_value": "gpu",
	}, 1)
	assertGaugeValue(t, metricMap["dagu_worker_info"], map[string]string{
		"worker_id":   "worker-a",
		"label_name":  "region",
		"label_value": "ap-northeast-1",
	}, 1)
	assertGaugeValue(t, metricMap["dagu_worker_heartbeat_timestamp_seconds"], map[string]string{
		"worker_id": "worker-a",
	}, float64(now.Add(-2*time.Second).UnixMilli())/1000)
	assertGaugeValue(t, metricMap["dagu_worker_health_status"], map[string]string{
		"worker_id": "worker-a",
		"status":    "healthy",
	}, 1)
	assertGaugeValue(t, metricMap["dagu_worker_health_status"], map[string]string{
		"worker_id": "worker-a",
		"status":    "warning",
	}, 0)
	assertGaugeValue(t, metricMap["dagu_worker_pollers"], map[string]string{
		"worker_id": "worker-a",
		"state":     "total",
	}, 4)
	assertGaugeValue(t, metricMap["dagu_worker_pollers"], map[string]string{
		"worker_id": "worker-a",
		"state":     "busy",
	}, 2)
	assertGaugeValue(t, metricMap["dagu_worker_pollers"], map[string]string{
		"worker_id": "worker-a",
		"state":     "idle",
	}, 2)
	assertGaugeValue(t, metricMap["dagu_worker_running_tasks"], map[string]string{
		"worker_id": "worker-a",
	}, 2)
	assertGaugeAtLeast(t, metricMap["dagu_worker_oldest_running_task_age_seconds"], map[string]string{
		"worker_id": "worker-a",
	}, 119)
}

func TestCollector_Collect_WithWorkerInfoLabels(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{},
		[]string{},
		nil,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{}, nil)

	now := time.Now().UTC()
	collector := NewCollector("1.0.0", dagStore, dagRunStore, queueStore, nil)
	collector.now = func() time.Time { return now }
	collector.SetWorkerHeartbeatStore(&mockWorkerHeartbeatStore{
		records: []exec.WorkerHeartbeatRecord{
			{
				WorkerID: "worker-a",
				Labels: map[string]string{
					"pool-name": "gpu",
					"status":    "spot",
					"9zone":     "a",
					"a-b":       "one",
					"a b":       "two",
					"a_b":       "three",
				},
				LastHeartbeatAt: now.UnixMilli(),
			},
		},
	})

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	metrics, err := registry.Gather()
	require.NoError(t, err)
	metricMap := metricFamilyMap(metrics)

	for name, value := range map[string]string{
		"pool-name": "gpu",
		"status":    "spot",
		"9zone":     "a",
		"a-b":       "one",
		"a b":       "two",
		"a_b":       "three",
	} {
		assertGaugeValue(t, metricMap["dagu_worker_info"], map[string]string{
			"worker_id":   "worker-a",
			"label_name":  name,
			"label_value": value,
		}, 1)
	}
	assertGaugeValue(t, metricMap["dagu_worker_running_tasks"], map[string]string{
		"worker_id": "worker-a",
	}, 0)
}

func TestCollector_Collect_WithErrors(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{},
		[]string{},
		assert.AnError,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus(nil), assert.AnError)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData(nil), assert.AnError)

	collector := NewCollector(
		"1.0.0",
		dagStore,
		dagRunStore,
		queueStore,
		nil,
	)

	ch := make(chan prometheus.Metric, 100)
	collector.Collect(ch)
	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}
	assert.Greater(t, metricsCount, 0)
}

func TestNewRegistry(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Setup mocks
	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{},
		[]string{},
		nil,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{}, nil)

	collector := NewCollector(
		"1.0.0",
		dagStore,
		dagRunStore,
		queueStore,
		nil,
	)

	registry := NewRegistry(collector)
	assert.NotNil(t, registry)

	// Verify it can gather metrics without panic
	metrics, err := registry.Gather()
	assert.NoError(t, err)
	assert.Greater(t, len(metrics), 0)

	// Should include Go runtime metrics
	metricNames := make(map[string]bool)
	for _, m := range metrics {
		metricNames[*m.Name] = true
	}
	assert.True(t, metricNames["go_goroutines"]) // Example Go metric
}

func TestCollector_SchedulerStatus(t *testing.T) {
	dagStore := &mockDAGStore{}
	dagRunStore := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Set up default mock responses
	dagStore.On("List", mock.Anything, mock.Anything).Return(
		exec.PaginatedResult[*core.DAG]{Items: []*core.DAG{}, TotalCount: 0},
		[]string{},
		nil,
	)
	dagRunStore.On("ListStatuses", mock.Anything, mock.Anything).Return([]*exec.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]exec.QueuedItemData{}, nil)

	t.Run("ActiveScheduler", func(t *testing.T) {
		serviceRegistry := &mockServiceRegistry{}
		serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return(
			[]exec.HostInfo{{Host: "localhost", Status: exec.ServiceStatusActive}},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagStore, dagRunStore, queueStore, serviceRegistry)

		ch := make(chan prometheus.Metric, 100)
		collector.Collect(ch)
		close(ch)

		// Check scheduler_running metric is 1
		schedulerRunningFound := false
		for metric := range ch {
			dto := &dto.Metric{}
			_ = metric.Write(dto)
			if strings.Contains(metric.Desc().String(), "scheduler_running") {
				schedulerRunningFound = true
				assert.Equal(t, float64(1), dto.Gauge.GetValue())
			}
		}
		assert.True(t, schedulerRunningFound, "scheduler_running metric not found")
	})

	t.Run("InactiveScheduler", func(t *testing.T) {
		serviceRegistry := &mockServiceRegistry{}
		serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return(
			[]exec.HostInfo{{Host: "localhost", Status: exec.ServiceStatusInactive}},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagStore, dagRunStore, queueStore, serviceRegistry)

		ch := make(chan prometheus.Metric, 100)
		collector.Collect(ch)
		close(ch)

		// Check scheduler_running metric is 0
		schedulerRunningFound := false
		for metric := range ch {
			dto := &dto.Metric{}
			_ = metric.Write(dto)
			if strings.Contains(metric.Desc().String(), "scheduler_running") {
				schedulerRunningFound = true
				assert.Equal(t, float64(0), dto.Gauge.GetValue())
			}
		}
		assert.True(t, schedulerRunningFound, "scheduler_running metric not found")
	})

	t.Run("NoSchedulerInstances", func(t *testing.T) {
		serviceRegistry := &mockServiceRegistry{}
		serviceRegistry.On("GetServiceMembers", mock.Anything, exec.ServiceNameScheduler).Return(
			[]exec.HostInfo{},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagStore, dagRunStore, queueStore, serviceRegistry)

		ch := make(chan prometheus.Metric, 100)
		collector.Collect(ch)
		close(ch)

		// Check scheduler_running metric is 0
		schedulerRunningFound := false
		for metric := range ch {
			dto := &dto.Metric{}
			_ = metric.Write(dto)
			if strings.Contains(metric.Desc().String(), "scheduler_running") {
				schedulerRunningFound = true
				assert.Equal(t, float64(0), dto.Gauge.GetValue())
			}
		}
		assert.True(t, schedulerRunningFound, "scheduler_running metric not found")
	})
}

func metricFamilyMap(metrics []*dto.MetricFamily) map[string]*dto.MetricFamily {
	result := make(map[string]*dto.MetricFamily, len(metrics))
	for _, metric := range metrics {
		result[metric.GetName()] = metric
	}
	return result
}

func assertGaugeValue(t *testing.T, family *dto.MetricFamily, labels map[string]string, expected float64) {
	t.Helper()
	metric := findMetric(t, family, labels)
	require.NotNil(t, metric.Gauge)
	assert.InDelta(t, expected, metric.Gauge.GetValue(), 0.001)
}

func assertGaugeAtLeast(t *testing.T, family *dto.MetricFamily, labels map[string]string, expectedMin float64) {
	t.Helper()
	metric := findMetric(t, family, labels)
	require.NotNil(t, metric.Gauge)
	assert.GreaterOrEqual(t, metric.Gauge.GetValue(), expectedMin)
}

func findMetric(t *testing.T, family *dto.MetricFamily, labels map[string]string) *dto.Metric {
	t.Helper()
	require.NotNil(t, family)
	for _, metric := range family.GetMetric() {
		if metricLabelsMatch(metric, labels) {
			return metric
		}
	}
	require.Failf(t, "metric not found", "metric %s with labels %v not found", family.GetName(), labels)
	return nil
}

func metricLabelsMatch(metric *dto.Metric, expected map[string]string) bool {
	actual := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		actual[label.GetName()] = label.GetValue()
	}
	if len(actual) != len(expected) {
		return false
	}
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}
