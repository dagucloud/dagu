// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
)

func TestActivityWindowFromQuery(t *testing.T) {
	t.Parallel()
	window, err := activityWindowFromQuery(url.Values{"window": {"86400"}})
	assert.NoError(t, err)
	assert.Equal(t, 24*time.Hour, window)

	for _, bad := range []string{"", "day", "59", "604801", "-1"} {
		_, err = activityWindowFromQuery(url.Values{"window": {bad}})
		var apiErr *Error
		if assert.ErrorAs(t, err, &apiErr, bad) {
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, bad)
		}
	}
}

// An activity topic needs a window, a known sort and order, and a page that
// a DAG list can have.
func TestValidateDAGListActivityQuery(t *testing.T) {
	t.Parallel()
	a := &API{config: &config.Config{}}
	assert.NoError(t, a.ValidateDAGListActivityQuery(t.Context(), "window=86400&page=1&perPage=100&sort=lastRun&order=desc"))

	for _, bad := range []string{
		"page=1&perPage=100",
		"window=86400&sort=size",
		"window=86400&order=up",
	} {
		err := a.ValidateDAGListActivityQuery(t.Context(), bad)
		var apiErr *Error
		if assert.ErrorAs(t, err, &apiErr, bad) {
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, bad)
		}
	}
}

// The activity reads at most the 200 DAGs of the largest list page, as the
// list limits a page, also for the largest integer, which the list reads as
// all DAGs.
func TestDAGListActivityQueryLimitsThePage(t *testing.T) {
	t.Parallel()
	a := &API{config: &config.Config{}}
	for _, perPage := range []string{"201", "9223372036854775807"} {
		listOpts, _, err := a.dagListActivityQuery(t.Context(), "window=86400&perPage="+perPage)
		if assert.NoError(t, err, perPage) {
			assert.Equal(t, 200, listOpts.Paginator.Limit(), perPage)
		}
	}
}
