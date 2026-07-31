// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubJobClient struct {
	createJob             func(ctx context.Context, stepName string, command []string) error
	waitForPod            func(ctx context.Context) (string, error)
	streamLogs            func(ctx context.Context, podName string, stdout io.Writer) error
	waitForPodTermination func(ctx context.Context, podName string) error
	getExitCode           func(ctx context.Context, podName string) (int, error)
	waitForCompletion     func(ctx context.Context) error
	deleteJob             func(ctx context.Context) error
	jobName               string
}

func (s *stubJobClient) CreateJob(ctx context.Context, stepName string, command []string) error {
	if s.createJob != nil {
		return s.createJob(ctx, stepName, command)
	}
	return nil
}

func (s *stubJobClient) WaitForPod(ctx context.Context) (string, error) {
	if s.waitForPod != nil {
		return s.waitForPod(ctx)
	}
	return "pod-1", nil
}

func (s *stubJobClient) StreamLogs(ctx context.Context, podName string, stdout io.Writer) error {
	if s.streamLogs != nil {
		return s.streamLogs(ctx, podName, stdout)
	}
	return nil
}

func (s *stubJobClient) WaitForPodTermination(ctx context.Context, podName string) error {
	if s.waitForPodTermination != nil {
		return s.waitForPodTermination(ctx, podName)
	}
	return nil
}

func (s *stubJobClient) GetExitCode(ctx context.Context, podName string) (int, error) {
	if s.getExitCode != nil {
		return s.getExitCode(ctx, podName)
	}
	return 0, nil
}

func (s *stubJobClient) WaitForCompletion(ctx context.Context) error {
	if s.waitForCompletion != nil {
		return s.waitForCompletion(ctx)
	}
	return nil
}

func (s *stubJobClient) DeleteJob(ctx context.Context) error {
	if s.deleteJob != nil {
		return s.deleteJob(ctx)
	}
	return nil
}

func (s *stubJobClient) GetJobName() string {
	return s.jobName
}

func TestKubernetesExecutorRunCancellationUsesBackgroundCleanupContext(t *testing.T) {
	originalFactory := newJobClient
	t.Cleanup(func() {
		newJobClient = originalFactory
	})

	waitStarted := make(chan struct{})
	deleteCalled := make(chan struct{})
	var (
		deleteCtxErr      error
		deleteHasDeadline bool
	)

	client := &stubJobClient{
		jobName: "job-1",
		waitForPodTermination: func(ctx context.Context, _ string) error {
			close(waitStarted)
			<-ctx.Done()
			return ctx.Err()
		},
		deleteJob: func(ctx context.Context) error {
			deleteCtxErr = ctx.Err()
			_, deleteHasDeadline = ctx.Deadline()
			close(deleteCalled)
			return nil
		},
	}
	newJobClient = func(_ *Config) (jobClient, error) {
		return client, nil
	}

	exec, err := newKubernetes(context.Background(), core.Step{
		Name: "run-in-k8s",
		ExecutorConfig: core.ExecutorConfig{
			Type: "kubernetes",
			Config: map[string]any{
				"image": "busybox",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- exec.Run(ctx)
	}()

	<-waitStarted
	cancel()

	require.ErrorIs(t, <-done, context.Canceled)
	<-deleteCalled
	assert.NoError(t, deleteCtxErr)
	assert.True(t, deleteHasDeadline)
}

func TestKubernetesExecutorKillUsesBackgroundCleanupContext(t *testing.T) {
	deleteCalled := false
	var (
		deleteCtxErr      error
		deleteHasDeadline bool
		cancelCalled      bool
	)

	exec := &kubernetesExecutor{
		cfg: &Config{
			CleanupPolicy: cleanupPolicyKeep,
		},
		client: &stubJobClient{
			jobName: "job-1",
			deleteJob: func(ctx context.Context) error {
				deleteCalled = true
				deleteCtxErr = ctx.Err()
				_, deleteHasDeadline = ctx.Deadline()
				return nil
			},
		},
		cancel: func() {
			cancelCalled = true
		},
	}

	require.NoError(t, exec.Kill(os.Interrupt))
	assert.True(t, cancelCalled)
	assert.True(t, deleteCalled)
	assert.NoError(t, deleteCtxErr)
	assert.True(t, deleteHasDeadline)
}

func TestKubernetesExecutorWaitForCompletionContextCancellationForcesCleanup(t *testing.T) {
	originalFactory := newJobClient
	t.Cleanup(func() {
		newJobClient = originalFactory
	})

	deleteCalled := make(chan struct{})
	var deleteCtxErr error
	client := &stubJobClient{
		jobName: "job-1",
		waitForCompletion: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		deleteJob: func(ctx context.Context) error {
			deleteCtxErr = ctx.Err()
			close(deleteCalled)
			return nil
		},
	}
	newJobClient = func(_ *Config) (jobClient, error) {
		return client, nil
	}

	exec, err := newKubernetes(context.Background(), core.Step{
		Name: "run-in-k8s",
		ExecutorConfig: core.ExecutorConfig{
			Type: "kubernetes",
			Config: map[string]any{
				"image": "busybox",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = exec.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	<-deleteCalled
	assert.NoError(t, deleteCtxErr)
}

func TestKubernetesExecutorValidateStepRequiresConfig(t *testing.T) {
	err := validateStep(core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Type: "kubernetes",
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "field 'with.image': image is required")
	assert.NotContains(t, err.Error(), "executor")
}

func TestKubernetesExecutorWaitForCompletionReturnsJobFailure(t *testing.T) {
	originalFactory := newJobClient
	t.Cleanup(func() {
		newJobClient = originalFactory
	})

	client := &stubJobClient{
		jobName: "job-1",
		waitForCompletion: func(context.Context) error {
			return errors.New("job failed: exit 1")
		},
	}
	newJobClient = func(_ *Config) (jobClient, error) {
		return client, nil
	}

	exec, err := newKubernetes(context.Background(), core.Step{
		Name: "run-in-k8s",
		ExecutorConfig: core.ExecutorConfig{
			Type: "kubernetes",
			Config: map[string]any{
				"image":          "busybox",
				"cleanup_policy": cleanupPolicyKeep,
			},
		},
	})
	require.NoError(t, err)

	err = exec.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job failed: exit 1")
}

func TestKubernetesExecutorPodSchedulingFailureRespectsCleanupPolicy(t *testing.T) {
	tests := []struct {
		name          string
		cleanupPolicy string
		wantDelete    bool
	}{
		{name: "DeletePolicy", cleanupPolicy: cleanupPolicyDelete, wantDelete: true},
		{name: "KeepPolicy", cleanupPolicy: cleanupPolicyKeep, wantDelete: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalFactory := newJobClient
			t.Cleanup(func() {
				newJobClient = originalFactory
			})

			deleteCalled := false
			client := &stubJobClient{
				jobName: "job-1",
				waitForPod: func(context.Context) (string, error) {
					return "", errors.New("unschedulable")
				},
				deleteJob: func(context.Context) error {
					deleteCalled = true
					return nil
				},
			}
			newJobClient = func(_ *Config) (jobClient, error) {
				return client, nil
			}

			exec, err := newKubernetes(context.Background(), core.Step{
				Name: "run-in-k8s",
				ExecutorConfig: core.ExecutorConfig{
					Type: "kubernetes",
					Config: map[string]any{
						"image":          "busybox",
						"cleanup_policy": tt.cleanupPolicy,
					},
				},
			})
			require.NoError(t, err)

			err = exec.Run(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pod scheduling failed")
			assert.Equal(t, tt.wantDelete, deleteCalled)
		})
	}
}
