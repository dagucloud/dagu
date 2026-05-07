// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

func TestNewAttemptReturnsDAGDataError(t *testing.T) {
	_, err := newAttempt(nil, db.DaguDagRunAttempt{
		ID:        uuid.Must(uuid.NewV7()),
		DagName:   "example",
		DagRunID:  "run-1",
		AttemptID: "attempt-1",
		DagData:   []byte("{"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unmarshal DAG data for dag "example" run "run-1" attempt "attempt-1"`)
}
