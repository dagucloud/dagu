// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock implementations
type mockDAGLister struct {
	mock.Mock
}

func (m *mockDAGLister) List(ctx context.Context, params persis.DAGListOptions) (pagination.PaginatedResult[persis.DAGListItem], []string, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(pagination.PaginatedResult[persis.DAGListItem]), args.Get(1).([]string), args.Error(2)
}

type mockDAGRunStore struct {
	testutil.DAGRunStoreStub
	mock.Mock
}

func (m *mockDAGRunStore) repository() *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(m, nil, persis.DAGRunRepositoryOptions{})
}

func newMockDAGRunRepository() *persis.DAGRunRepository {
	return (&mockDAGRunStore{}).repository()
}

func (m *mockDAGRunStore) QueryStatuses(ctx context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	args := m.MethodCalled("ListStatuses", ctx, query)
	if args.Get(0) == nil {
		return persis.DAGRunStatusPage{}, args.Error(1)
	}
	return persis.DAGRunStatusPage{Items: args.Get(0).([]*ir.DAGRunStatus)}, args.Error(1)
}

type mockQueueStore struct {
	mock.Mock
}

var _ queue.QueueStore = (*mockQueueStore)(nil)

// QueueWatcher implements execution.QueueStore.
func (m *mockQueueStore) QueueWatcher(_ context.Context) queue.QueueWatcher {
	panic("unimplemented")
}

// QueueList implements execution.QueueStore.
func (m *mockQueueStore) QueueList(_ context.Context) ([]string, error) {
	panic("unimplemented")
}

// ListByDAGName implements models.QueueStore.
func (m *mockQueueStore) ListByDAGName(_ context.Context, _, _ string) ([]queue.QueuedItemData, error) {
	return nil, nil
}

func (m *mockQueueStore) Enqueue(ctx context.Context, name string, priority queue.QueuePriority, dagRun ir.DAGRunRef) error {
	args := m.Called(ctx, name, priority, dagRun)
	return args.Error(0)
}

func (m *mockQueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun ir.DAGRunRef) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx, name, dagRun)
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	args := m.Called(ctx, name, itemIDs)
	return args.Int(0), args.Error(1)
}

func (m *mockQueueStore) Len(ctx context.Context, name string) (int, error) {
	args := m.Called(ctx, name)
	return args.Int(0), args.Error(1)
}

func (m *mockQueueStore) List(ctx context.Context, name string) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx, name)
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) GetByItemID(ctx context.Context, name, itemID string) (queue.QueuedItemData, error) {
	args := m.Called(ctx, name, itemID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(queue.QueuedItemData), args.Error(1)
}

func (m *mockQueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (pagination.CursorResult[queue.QueuedItemData], error) {
	args := m.Called(ctx, name, cursor, limit)
	return args.Get(0).(pagination.CursorResult[queue.QueuedItemData]), args.Error(1)
}

func (m *mockQueueStore) Revision(context.Context, string) (int64, error) {
	return 0, nil
}

func (m *mockQueueStore) All(ctx context.Context) ([]queue.QueuedItemData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]queue.QueuedItemData), args.Error(1)
}

var _ serviceregistry.ServiceRegistry = (*mockServiceRegistry)(nil)

type mockServiceRegistry struct {
	mock.Mock
}

var _ serviceregistry.ServiceRegistry = (*mockServiceRegistry)(nil)

func (m *mockServiceRegistry) Register(ctx context.Context, serviceName serviceregistry.ServiceName, hostInfo serviceregistry.HostInfo) error {
	args := m.Called(ctx, serviceName, hostInfo)
	return args.Error(0)
}

func (m *mockServiceRegistry) Unregister(ctx context.Context) {
	m.Called(ctx)
}

func (m *mockServiceRegistry) GetServiceMembers(ctx context.Context, serviceName serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	args := m.Called(ctx, serviceName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]serviceregistry.HostInfo), args.Error(1)
}

func (m *mockServiceRegistry) UpdateStatus(ctx context.Context, serviceName serviceregistry.ServiceName, status serviceregistry.ServiceStatus) error {
	args := m.Called(ctx, serviceName, status)
	return args.Error(0)
}

type mockWorkerHeartbeatStore struct {
	records []dispatch.WorkerHeartbeatRecord
	err     error
}

var _ dispatch.WorkerHeartbeatStore = (*mockWorkerHeartbeatStore)(nil)

func (m *mockWorkerHeartbeatStore) Upsert(context.Context, dispatch.WorkerHeartbeatRecord) error {
	panic("unimplemented")
}

func (m *mockWorkerHeartbeatStore) Get(context.Context, string) (*dispatch.WorkerHeartbeatRecord, error) {
	panic("unimplemented")
}

func (m *mockWorkerHeartbeatStore) List(context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
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
	serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return([]serviceregistry.HostInfo{{Host: "localhost", Status: serviceregistry.ServiceStatusActive}}, nil)

	collector := NewCollector(
		"1.0.0",
		&mockDAGLister{},
		newMockDAGRunRepository(),
		&mockQueueStore{},
		serviceRegistry,
	)

	assert.NotNil(t, collector)
	assert.Equal(t, "1.0.0", collector.version)
}

func TestCollector_Describe(t *testing.T) {
	collector := NewCollector(
		"1.0.0",
		&mockDAGLister{},
		newMockDAGRunRepository(),
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{},
		[]string{},
		nil,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{}, nil)

	serviceRegistry := &mockServiceRegistry{}
	serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return([]serviceregistry.HostInfo{{Host: "localhost", Status: serviceregistry.ServiceStatusActive}}, nil).Maybe()

	collector := NewCollector(
		"1.0.0",
		dagRepository,
		dagRunRepository.repository(),
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Mock DAG store response
	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{
			Items:      []persis.DAGListItem{{DAG: &ir.DAG{}}, {DAG: &ir.DAG{}}, {DAG: &ir.DAG{}}},
			TotalCount: 3,
		},
		[]string{},
		nil,
	)

	// Mock DAG-run repository response.
	statuses := []*ir.DAGRunStatus{
		{Status: ir.Succeeded},
		{Status: ir.Succeeded},
		{Status: ir.Failed},
		{Status: ir.Running},
		{Status: ir.Queued},
		{Status: ir.Aborted},
	}
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return(statuses, nil)

	// Mock queue store response
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{nil, nil}, nil)

	serviceRegistry := &mockServiceRegistry{}
	serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return([]serviceregistry.HostInfo{{Host: "localhost", Status: serviceregistry.ServiceStatusActive}}, nil).Maybe()

	collector := NewCollector(
		"1.0.0",
		dagRepository,
		dagRunRepository.repository(),
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{},
		[]string{},
		nil,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{}, nil)

	now := time.Now().UTC()
	collector := NewCollector("1.0.0", dagRepository, dagRunRepository.repository(), queueStore, nil)
	collector.now = func() time.Time { return now }
	collector.SetWorkerHeartbeatStore(&mockWorkerHeartbeatStore{
		records: []dispatch.WorkerHeartbeatRecord{
			{
				WorkerID: "worker-a",
				Labels: map[string]string{
					"pool":   "gpu",
					"region": "ap-northeast-1",
				},
				Stats: &dispatch.WorkerStats{
					TotalPollers: 4,
					BusyPollers:  2,
					RunningTasks: []*dispatch.RunningTask{
						{DAGRunID: "run-1", DAGName: "dag-1", StartedAt: now.Add(-2 * time.Minute).Unix()},
						{DAGRunID: "run-2", DAGName: "dag-2", StartedAt: now.Add(-30 * time.Second).Unix()},
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{},
		[]string{},
		nil,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{}, nil)

	now := time.Now().UTC()
	collector := NewCollector("1.0.0", dagRepository, dagRunRepository.repository(), queueStore, nil)
	collector.now = func() time.Time { return now }
	collector.SetWorkerHeartbeatStore(&mockWorkerHeartbeatStore{
		records: []dispatch.WorkerHeartbeatRecord{
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{},
		[]string{},
		assert.AnError,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus(nil), assert.AnError)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData(nil), assert.AnError)

	collector := NewCollector(
		"1.0.0",
		dagRepository,
		dagRunRepository.repository(),
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Setup mocks
	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{},
		[]string{},
		nil,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{}, nil)

	collector := NewCollector(
		"1.0.0",
		dagRepository,
		dagRunRepository.repository(),
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
	dagRepository := &mockDAGLister{}
	dagRunRepository := &mockDAGRunStore{}
	queueStore := &mockQueueStore{}

	// Set up default mock responses
	dagRepository.On("List", mock.Anything, mock.Anything).Return(
		pagination.PaginatedResult[persis.DAGListItem]{Items: []persis.DAGListItem{}, TotalCount: 0},
		[]string{},
		nil,
	)
	dagRunRepository.On("ListStatuses", mock.Anything, mock.Anything).Return([]*ir.DAGRunStatus{}, nil)
	queueStore.On("All", mock.Anything).Return([]queue.QueuedItemData{}, nil)

	t.Run("ActiveScheduler", func(t *testing.T) {
		serviceRegistry := &mockServiceRegistry{}
		serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return(
			[]serviceregistry.HostInfo{{Host: "localhost", Status: serviceregistry.ServiceStatusActive}},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagRepository, dagRunRepository.repository(), queueStore, serviceRegistry)

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
		serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return(
			[]serviceregistry.HostInfo{{Host: "localhost", Status: serviceregistry.ServiceStatusInactive}},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagRepository, dagRunRepository.repository(), queueStore, serviceRegistry)

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
		serviceRegistry.On("GetServiceMembers", mock.Anything, serviceregistry.ServiceNameScheduler).Return(
			[]serviceregistry.HostInfo{},
			nil,
		).Maybe()

		collector := NewCollector("1.0.0", dagRepository, dagRunRepository.repository(), queueStore, serviceRegistry)

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
