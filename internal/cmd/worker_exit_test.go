// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/service/worker"
)

// removedForTest is a removed worker's failure, as the worker returns it.
var removedForTest = fmt.Errorf("%w\n  Get a new command", worker.ErrRemoved)

// A removed worker exits with its failure as is, so the message prints
// without a prefix, and any other end says what failed (Spec 108 §Removed
// Worker).
func TestWorkerEnd(t *testing.T) {
	t.Parallel()
	assert.NoError(t, workerEnd(nil, "worker failed"))
	assert.Equal(t, removedForTest, workerEnd(removedForTest, "failed to stop worker"))
	assert.EqualError(t, workerEnd(errors.New("boom"), "worker failed"), "worker failed: boom")
}

// A removal the worker keeps a record of fails the check whatever the
// coordinators answered, as one that has not reread the revoked set yet still
// accepts the certificate (Spec 108 §dagu worker --check).
func TestWorkerCheckVerdict(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	assert.Equal(t, removedForTest, workerCheckVerdict(&out, removedForTest, 0, 1, false))
	assert.Equal(t, "This worker keeps a record that a coordinator refused its certificate as revoked, so it exits at start\n", out.String())

	out.Reset()
	assert.NoError(t, workerCheckVerdict(&out, nil, 0, 1, false))
	assert.Equal(t, "Every coordinator passed the check\n", out.String())
	assert.EqualError(t, workerCheckVerdict(&out, nil, 1, 2, false), "1 of 2 coordinators did not pass the check")
}
