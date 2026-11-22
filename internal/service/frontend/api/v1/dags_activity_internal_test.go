// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestReusedTimeMs(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	step := func(took time.Duration) ir.ReusedInput {
		return ir.ReusedInput{StartedAt: stringutil.FormatTime(at), FinishedAt: stringutil.FormatTime(at.Add(took))}
	}

	sum, ok := reusedTimeMs([]ir.ReusedInput{step(4 * time.Second), step(1500 * time.Millisecond)})
	assert.True(t, ok)
	assert.Equal(t, int64(5500), sum)

	// A step that a client supplied has no times, and a negative span is unknown.
	_, ok = reusedTimeMs([]ir.ReusedInput{{Step: "supplied"}, {StartedAt: stringutil.FormatTime(at), FinishedAt: stringutil.FormatTime(at.Add(-time.Second))}})
	assert.False(t, ok)
	_, ok = reusedTimeMs(nil)
	assert.False(t, ok)
}

func TestActivityWindowFromQuery(t *testing.T) {
	t.Parallel()
	window, err := activityWindowFromQuery(url.Values{})
	assert.NoError(t, err)
	assert.Zero(t, window)

	window, err = activityWindowFromQuery(url.Values{"activityWindow": {"86400"}})
	assert.NoError(t, err)
	assert.Equal(t, 24*time.Hour, window)

	for _, bad := range []string{"day", "59", "604801", "-1"} {
		_, err = activityWindowFromQuery(url.Values{"activityWindow": {bad}})
		var apiErr *Error
		if assert.ErrorAs(t, err, &apiErr, bad) {
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, bad)
		}
	}
}
