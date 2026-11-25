// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestActivityWindowFromSeconds(t *testing.T) {
	t.Parallel()
	for seconds, want := range map[int]time.Duration{60: time.Minute, 604800: 7 * 24 * time.Hour} {
		window, err := activityWindowFromSeconds(seconds)
		assert.NoError(t, err, seconds)
		assert.Equal(t, want, window, seconds)
	}
	for _, bad := range []int{-1, 59, 604801} {
		_, err := activityWindowFromSeconds(bad)
		assertBadRequest(t, err, strconv.Itoa(bad))
	}
}

// A request names each DAG once, in the order it first names it, and names
// from 1 to 100 DAGs.
func TestActivityFileNames(t *testing.T) {
	t.Parallel()
	names, err := activityFileNames([]string{"c", "a", "c", "b"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"c", "a", "b"}, names)

	tooMany := make([]string, maxActivityFileNames+1)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i)
	}
	for _, bad := range [][]string{nil, tooMany} {
		_, err := activityFileNames(bad)
		assertBadRequest(t, err, strconv.Itoa(len(bad)))
	}
}

func assertBadRequest(t *testing.T, err error, msg string) {
	t.Helper()
	var apiErr *Error
	if assert.ErrorAs(t, err, &apiErr, msg) {
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, msg)
	}
}
