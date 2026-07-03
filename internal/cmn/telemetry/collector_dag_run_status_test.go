// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestCollectorEmitsDAGRunStatusGaugeFromAllHistory(t *testing.T) {
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
		"dag":    "daily",
		"status": "succeeded",
	}, 1)
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag":    "daily",
		"status": "failed",
	}, 0)
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag":    "fresh",
		"status": "not_started",
	}, 1)
	assertGaugeValue(t, statusFamily, map[string]string{
		"dag":    "fresh",
		"status": "running",
	}, 0)

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

func (s *dagRunStoreStub) ListStatuses(_ context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	var options exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&options)
	}
	if options.ExactName == "" || !options.AllHistory {
		return nil, nil
	}
	statuses := s.statuses[options.ExactName]
	if options.Limit > 0 && len(statuses) > options.Limit {
		return statuses[:options.Limit], nil
	}
	return statuses, nil
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
