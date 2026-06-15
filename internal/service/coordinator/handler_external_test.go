// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
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

func TestHandlerPollDistributedAdaptiveWaitBacksOff(t *testing.T) {
	t.Parallel()

	store := &pollingDispatchStore{}
	handler := coord.NewHandler(coord.HandlerConfig{DispatchTaskStore: store})
	coord.SetDispatchPollWaitForTest(t, handler, 5*time.Millisecond, 40*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Millisecond)
	defer cancel()
	resp, err := handler.Poll(ctx, &coordinatorv1.PollRequest{
		WorkerId: "worker-idle",
		PollerId: "poller-idle",
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, resp)
	assert.LessOrEqual(t, store.ClaimCalls(), int64(8), "idle pollers should back off instead of polling at the initial interval forever")
	assert.GreaterOrEqual(t, store.ClaimCalls(), int64(3), "pollers should continue using the fallback timer")
}

func TestHandlerPollDistributedWakeClaimsWithoutTimer(t *testing.T) {
	t.Parallel()

	store := &pollingDispatchStore{}
	handler := coord.NewHandler(coord.HandlerConfig{DispatchTaskStore: store})
	coord.SetDispatchPollWaitForTest(t, handler, time.Hour, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan *coordinatorv1.PollResponse, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := handler.Poll(ctx, &coordinatorv1.PollRequest{
			WorkerId: "worker-wake",
			PollerId: "poller-wake",
		})
		if err != nil {
			errs <- err
			return
		}
		done <- resp
	}()

	store.WaitForClaimCalls(t, 1)
	store.SetClaimed(&exec.ClaimedDispatchTask{
		Task: &exec.DispatchTask{
			DAGRunID:   "run-wake",
			Target:     "dag-wake",
			Definition: "name: dag-wake\nsteps:\n  - name: step\n    run: echo wake",
		},
		ClaimToken: "claim-wake",
	})
	coord.NotifyDispatchAvailableForTest(handler)

	select {
	case resp := <-done:
		require.NotNil(t, resp)
		require.NotNil(t, resp.Task)
		assert.Equal(t, "run-wake", resp.Task.DagRunId)
	case err := <-errs:
		t.Fatalf("Poll failed: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Poll did not wake after dispatch signal")
	}
}

func TestHandlerPollDistributedFallbackTimerClaimsWithoutWake(t *testing.T) {
	t.Parallel()

	store := &pollingDispatchStore{}
	handler := coord.NewHandler(coord.HandlerConfig{DispatchTaskStore: store})
	coord.SetDispatchPollWaitForTest(t, handler, 5*time.Millisecond, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan *coordinatorv1.PollResponse, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := handler.Poll(ctx, &coordinatorv1.PollRequest{
			WorkerId: "worker-fallback",
			PollerId: "poller-fallback",
		})
		if err != nil {
			errs <- err
			return
		}
		done <- resp
	}()

	store.WaitForClaimCalls(t, 1)
	store.SetClaimed(&exec.ClaimedDispatchTask{
		Task: &exec.DispatchTask{
			DAGRunID:   "run-fallback",
			Target:     "dag-fallback",
			Definition: "name: dag-fallback\nsteps:\n  - name: step\n    run: echo fallback",
		},
		ClaimToken: "claim-fallback",
	})

	select {
	case resp := <-done:
		require.NotNil(t, resp)
		require.NotNil(t, resp.Task)
		assert.Equal(t, "run-fallback", resp.Task.DagRunId)
	case err := <-errs:
		t.Fatalf("Poll failed: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Poll did not claim through fallback timer")
	}
}

func TestHandlerPollDistributedCancellationExitsWhileWaiting(t *testing.T) {
	t.Parallel()

	store := &pollingDispatchStore{}
	handler := coord.NewHandler(coord.HandlerConfig{DispatchTaskStore: store})
	coord.SetDispatchPollWaitForTest(t, handler, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := handler.Poll(ctx, &coordinatorv1.PollRequest{
			WorkerId: "worker-cancel",
			PollerId: "poller-cancel",
		})
		errs <- err
	}()

	store.WaitForClaimCalls(t, 1)
	cancel()

	select {
	case err := <-errs:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Poll did not exit after context cancellation")
	}
}

func TestHandlerPollDistributedWakeDoesNotBlockManyPollers(t *testing.T) {
	t.Parallel()

	store := &pollingDispatchStore{}
	handler := coord.NewHandler(coord.HandlerConfig{DispatchTaskStore: store})
	coord.SetDispatchPollWaitForTest(t, handler, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const pollers = 20
	var wg sync.WaitGroup
	wg.Add(pollers)
	for i := range pollers {
		go func(i int) {
			defer wg.Done()
			_, _ = handler.Poll(ctx, &coordinatorv1.PollRequest{
				WorkerId: "worker-many",
				PollerId: "poller-many-" + strconv.Itoa(i),
			})
		}(i)
	}

	store.WaitForClaimCalls(t, pollers)
	for range 3 {
		coord.NotifyDispatchAvailableForTest(handler)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pollers did not exit after wake and cancellation")
	}
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

type pollingDispatchStore struct {
	claimed    atomic.Pointer[exec.ClaimedDispatchTask]
	claimCalls atomic.Int64
}

func (s *pollingDispatchStore) Enqueue(context.Context, *exec.DispatchTask) error {
	return nil
}

func (s *pollingDispatchStore) ClaimNext(context.Context, exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	s.claimCalls.Add(1)
	return s.claimed.Swap(nil), nil
}

func (s *pollingDispatchStore) GetClaim(context.Context, string) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
}

func (s *pollingDispatchStore) ReleaseClaim(context.Context, string) error {
	return nil
}

func (s *pollingDispatchStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (s *pollingDispatchStore) CountOutstandingByQueue(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

func (s *pollingDispatchStore) HasOutstandingAttempt(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func (s *pollingDispatchStore) SetClaimed(claimed *exec.ClaimedDispatchTask) {
	s.claimed.Store(claimed)
}

func (s *pollingDispatchStore) ClaimCalls() int64 {
	return s.claimCalls.Load()
}

func (s *pollingDispatchStore) WaitForClaimCalls(t *testing.T, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return s.ClaimCalls() >= int64(want)
	}, time.Second, time.Millisecond)
}
