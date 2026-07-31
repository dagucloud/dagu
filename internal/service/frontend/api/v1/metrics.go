// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (a *API) GetMetrics(_ context.Context, _ api.GetMetricsRequestObject) (api.GetMetricsResponseObject, error) {
	// Use promhttp handler to write metrics
	handler := promhttp.HandlerFor(a.metricsRegistry, promhttp.HandlerOpts{})

	// Create a custom response writer that implements the API response interface
	return &MetricsTextResponse{
		handler: handler,
	}, nil
}

// MetricsTextResponse implements the response interface for metrics
type MetricsTextResponse struct {
	handler http.Handler
}

func (r *MetricsTextResponse) VisitGetMetricsResponse(w http.ResponseWriter) error {
	// Set proper content type for Prometheus metrics
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Use the promhttp handler to write the metrics
	r.handler.ServeHTTP(w, &http.Request{})

	return nil
}
