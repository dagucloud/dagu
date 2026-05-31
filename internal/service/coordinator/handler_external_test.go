// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	coord "github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerPollReleasesClaimWhenTaskEncodingFails(t *testing.T) {
	t.Parallel()

	store := &claimReleaseStore{
		claimed: &exec.ClaimedDispatchTask{
			Task: &exec.DispatchTask{
				DAGRunID: "run-invalid-port",
				Target:   "dag-invalid-port",
				Owner:    exec.CoordinatorEndpoint{Port: -1},
			},
			ClaimToken: "claim-invalid-port",
		},
	}
	handler := coord.NewHandler(coord.HandlerConfig{
		DispatchTaskStore: store,
		Owner:             exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
	})

	resp, err := handler.Poll(context.Background(), &coordinatorv1.PollRequest{
		WorkerId: "worker-1",
		PollerId: "poller-1",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to encode claimed task")
	assert.Equal(t, "claim-invalid-port", store.releasedToken)
}

type claimReleaseStore struct {
	claimed       *exec.ClaimedDispatchTask
	releasedToken string
}

func (s *claimReleaseStore) Enqueue(context.Context, *exec.DispatchTask) error {
	return nil
}

func (s *claimReleaseStore) ClaimNext(context.Context, exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	claimed := s.claimed
	s.claimed = nil
	return claimed, nil
}

func (s *claimReleaseStore) GetClaim(context.Context, string) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
}

func (s *claimReleaseStore) ReleaseClaim(_ context.Context, claimToken string) error {
	s.releasedToken = claimToken
	return nil
}

func (s *claimReleaseStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (s *claimReleaseStore) CountOutstandingByQueue(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

func (s *claimReleaseStore) HasOutstandingAttempt(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}
