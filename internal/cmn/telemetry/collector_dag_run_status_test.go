// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestCollectorEmitsDAGRunStatusGaugeFromLatestAttempt(t *testing.T) {
	dagRunStore := &dagRunStoreStub{
		statuses: map[string][]*exec.DAGRunStatus{
			"daily": {
				{Name: "daily", Status: core.Succeeded},
			},
		},
	}
	collector := telemetry.NewCollector(
		"test",
		&dagStoreStub{
			dags: []*core.DAG{
				{Name: "daily"},
				{Name: "fresh"},
				{Name: "empty"},
				{Name: "broken"},
			},
		},
		dagRunStore,
		queueStoreStub{},
		nil,
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	metrics, err := registry.Gather()
	require.NoError(t, err)
	metricMap := metricFamilyMap(metrics)

	statusFamily := metricMap["dagu_dag_run_status"]
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag": "daily",
	}, float64(core.Succeeded))
	assertMetricDoesNotHaveLabel(t, statusFamily, "daily", "status")
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag": "fresh",
	}, float64(core.NotStarted))
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag": "empty",
	}, float64(core.NotStarted))
	assertNoDAGMetrics(t, statusFamily, "broken")

	statusInfoFamily := metricMap["dagu_dag_run_status_info"]
	assertGaugeValue(t, statusInfoFamily, map[string]string{
		"status": "succeeded",
		"code":   "4",
	}, 1)
	assertGaugeValue(t, statusInfoFamily, map[string]string{
		"status": "failed",
		"code":   "2",
	}, 1)
}

type dagStoreStub struct {
	exec.DAGStore
	dags []*core.DAG
}

func (s *dagStoreStub) List(context.Context, exec.ListDAGsOptions) (exec.PaginatedResult[*core.DAG], []string, error) {
	return exec.PaginatedResult[*core.DAG]{
		Items:      s.dags,
		TotalCount: len(s.dags),
	}, nil, nil
}

type dagRunStoreStub struct {
	exec.DAGRunStore
	statuses map[string][]*exec.DAGRunStatus
}

func (s *dagRunStoreStub) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *dagRunStoreStub) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrNoStatusData
}

func (s *dagRunStoreStub) LatestAttemptAllHistory(_ context.Context, name string) (exec.DAGRunAttempt, error) {
	if name == "broken" {
		return nil, errors.New("boom")
	}
	if name == "empty" {
		attempt := &exec.MockDAGRunAttempt{}
		attempt.On("ReadStatus", mock.Anything).Return(nil, exec.ErrNoStatusData)
		return attempt, nil
	}
	statuses := s.statuses[name]
	if len(statuses) == 0 {
		return nil, exec.ErrNoStatusData
	}
	return &exec.MockDAGRunAttempt{Status: statuses[0]}, nil
}

type queueStoreStub struct {
	exec.QueueStore
}

func (queueStoreStub) All(context.Context) ([]exec.QueuedItemData, error) {
	return nil, nil
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

func assertNoDAGMetrics(t *testing.T, family *dto.MetricFamily, dagName string) {
	t.Helper()
	require.NotNil(t, family)
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "dag" && label.GetValue() == dagName {
				require.Failf(t, "metric found", "metric %s with dag %q found", family.GetName(), dagName)
			}
		}
	}
}

func assertMetricDoesNotHaveLabel(t *testing.T, family *dto.MetricFamily, dagName, labelName string) {
	t.Helper()
	metric := findMetric(t, family, map[string]string{"dag": dagName})
	for _, label := range metric.GetLabel() {
		require.NotEqual(t, labelName, label.GetName())
	}
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
