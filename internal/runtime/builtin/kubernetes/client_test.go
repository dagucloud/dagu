// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForPodUsesCurrentState(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "default",
			Labels: map[string]string{
				"job-name": "job-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	client := &Client{
		clientset: fake.NewSimpleClientset(pod),
		namespace: "default",
	}
	client.setJobName("job-1")

	podName, err := client.WaitForPod(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "job-pod", podName)
}

func TestWaitForPodReturnsSchedulingFailure(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "default",
			Labels: map[string]string{
				"job-name": "job-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Message: "0/3 nodes are available",
			}},
		},
	}

	client := &Client{
		clientset: fake.NewSimpleClientset(pod),
		namespace: "default",
	}
	client.setJobName("job-1")

	_, err := client.WaitForPod(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unschedulable")
}

func TestWaitForCompletionUsesCurrentState(t *testing.T) {
	tests := []struct {
		name    string
		job     *batchv1.Job
		wantErr string
	}{
		{
			name: "Completed",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "job-1", Namespace: "default"},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionTrue,
					}},
				},
			},
		},
		{
			name: "Failed",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "job-1", Namespace: "default"},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{{
						Type:    batchv1.JobFailed,
						Status:  corev1.ConditionTrue,
						Message: "container exited 1",
					}},
				},
			},
			wantErr: "container exited 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				clientset: fake.NewSimpleClientset(tt.job),
				namespace: "default",
			}
			client.setJobName("job-1")

			err := client.WaitForCompletion(context.Background())
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDeleteJobIgnoresNotFound(t *testing.T) {
	client := &Client{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
	}
	client.setJobName("job-1")

	err := client.DeleteJob(context.Background())
	require.NoError(t, err)
	assert.Empty(t, client.GetJobName())
}

func TestCurrentPodNamePrefersPendingRetryOverFailedPod(t *testing.T) {
	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "job-pod-attempt-1",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Unix(1, 0)),
			Labels: map[string]string{
				"job-name": "job-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}
	retryPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "job-pod-attempt-2",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Unix(2, 0)),
			Labels: map[string]string{
				"job-name": "job-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	client := &Client{
		clientset: fake.NewSimpleClientset(failedPod, retryPod),
		namespace: "default",
	}
	client.setJobName("job-1")

	podName, err := client.currentPodName(context.Background())
	require.NoError(t, err)
	assert.Empty(t, podName)
}

func TestCurrentPodNameReturnsFailedPodWhenNoRetryExists(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "default",
			Labels: map[string]string{
				"job-name": "job-1",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	client := &Client{
		clientset: fake.NewSimpleClientset(pod),
		namespace: "default",
	}
	client.setJobName("job-1")

	podName, err := client.currentPodName(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "job-pod", podName)
}

func TestBuildRESTConfigFailsFastForExplicitKubeconfig(t *testing.T) {
	cfg, err := buildRESTConfig(&Config{
		Kubeconfig: "/definitely/missing/kubeconfig",
	})

	require.Nil(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig error")
}

func TestBuildRESTConfigFailsFastForExplicitKubeconfigEnv(t *testing.T) {
	t.Setenv(clientcmd.RecommendedConfigPathEnvVar, filepath.Join(t.TempDir(), "missing-kubeconfig"))

	cfg, err := buildRESTConfig(&Config{})

	require.Nil(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig error")
}

func TestBuildRESTConfigFailsFastForExplicitContext(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "config")

	raw := clientcmdapi.NewConfig()
	raw.Clusters["cluster-1"] = &clientcmdapi.Cluster{Server: "https://example.com"}
	raw.AuthInfos["user-1"] = &clientcmdapi.AuthInfo{}
	raw.Contexts["ctx-1"] = &clientcmdapi.Context{
		Cluster:  "cluster-1",
		AuthInfo: "user-1",
	}
	raw.CurrentContext = "ctx-1"
	require.NoError(t, clientcmd.WriteToFile(*raw, kubeconfigPath))

	cfg, err := buildRESTConfig(&Config{
		Kubeconfig: kubeconfigPath,
		Context:    "missing-context",
	})

	require.Nil(t, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig error")
	assert.Contains(t, err.Error(), "context")
}

func TestBuildJobAppliesExtendedKubernetesConfig(t *testing.T) {
	cfg := &Config{
		Namespace:    "default",
		Image:        "alpine:3.20",
		BackoffLimit: new(int32(0)),
		SecurityContext: &SecurityContext{
			RunAsNonRoot:             new(true),
			ReadOnlyRootFilesystem:   new(true),
			AllowPrivilegeEscalation: new(false),
			Capabilities: &Capabilities{
				Drop: []string{"ALL"},
			},
			SeccompProfile: &SeccompProfile{
				Type: "RuntimeDefault",
			},
		},
		PodSecurityContext: &PodSecurityContext{
			RunAsUser:           new(int64(1000)),
			RunAsGroup:          new(int64(1000)),
			RunAsNonRoot:        new(true),
			FSGroup:             new(int64(2000)),
			FSGroupChangePolicy: "OnRootMismatch",
			SupplementalGroups:  []int64{3000},
			Sysctls: []Sysctl{{
				Name:  "net.ipv4.ip_unprivileged_port_start",
				Value: "0",
			}},
			SeccompProfile: &SeccompProfile{
				Type:             "Localhost",
				LocalhostProfile: "profiles/pod.json",
			},
		},
		Affinity: &Affinity{
			NodeAffinity: &NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &NodeSelector{
					NodeSelectorTerms: []NodeSelectorTerm{{
						MatchExpressions: []NodeSelectorRequirement{{
							Key:      "kubernetes.io/arch",
							Operator: "In",
							Values:   []string{"amd64"},
						}},
					}},
				},
				PreferredDuringSchedulingIgnoredDuringExecution: []PreferredSchedulingTerm{{
					Weight: 50,
					Preference: NodeSelectorTerm{
						MatchExpressions: []NodeSelectorRequirement{{
							Key:      "topology.kubernetes.io/zone",
							Operator: "In",
							Values:   []string{"ap-northeast-1a"},
						}},
					},
				}},
			},
			PodAntiAffinity: &PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []PodAffinityTerm{{
					TopologyKey: "kubernetes.io/hostname",
					LabelSelector: &LabelSelector{
						MatchLabels: map[string]string{"app": "dagu"},
					},
				}},
			},
		},
		TerminationGracePeriodSeconds: new(int64(30)),
		PriorityClassName:             "batch-high",
		PodFailurePolicy: &PodFailurePolicy{
			Rules: []PodFailurePolicyRule{
				{
					Action: "Count",
					OnExitCodes: &PodFailurePolicyOnExitCodesRequirement{
						ContainerName: "step",
						Operator:      "In",
						Values:        []int32{42},
					},
				},
				{
					Action: "Ignore",
					OnPodConditions: []PodFailurePolicyOnPodConditionsPattern{{
						Type: "DisruptionTarget",
					}},
				},
			},
		},
	}

	client := &Client{cfg: cfg, namespace: "default"}

	job, err := client.buildJob("example-step", []string{"echo", "hello"})
	require.NoError(t, err)

	container := job.Spec.Template.Spec.Containers[0]
	require.NotNil(t, container.SecurityContext)
	require.NotNil(t, container.SecurityContext.RunAsNonRoot)
	assert.True(t, *container.SecurityContext.RunAsNonRoot)
	require.NotNil(t, container.SecurityContext.ReadOnlyRootFilesystem)
	assert.True(t, *container.SecurityContext.ReadOnlyRootFilesystem)
	require.NotNil(t, container.SecurityContext.AllowPrivilegeEscalation)
	assert.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
	require.NotNil(t, container.SecurityContext.Capabilities)
	assert.Equal(t, []corev1.Capability{"ALL"}, container.SecurityContext.Capabilities.Drop)
	require.NotNil(t, container.SecurityContext.SeccompProfile)
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, container.SecurityContext.SeccompProfile.Type)

	require.NotNil(t, job.Spec.Template.Spec.SecurityContext)
	assert.EqualValues(t, 1000, *job.Spec.Template.Spec.SecurityContext.RunAsUser)
	assert.EqualValues(t, 2000, *job.Spec.Template.Spec.SecurityContext.FSGroup)
	assert.Equal(t, []int64{3000}, job.Spec.Template.Spec.SecurityContext.SupplementalGroups)
	require.NotNil(t, job.Spec.Template.Spec.SecurityContext.FSGroupChangePolicy)
	assert.Equal(t, corev1.PodFSGroupChangePolicy("OnRootMismatch"), *job.Spec.Template.Spec.SecurityContext.FSGroupChangePolicy)
	require.Len(t, job.Spec.Template.Spec.SecurityContext.Sysctls, 1)
	assert.Equal(t, "net.ipv4.ip_unprivileged_port_start", job.Spec.Template.Spec.SecurityContext.Sysctls[0].Name)
	require.NotNil(t, job.Spec.Template.Spec.SecurityContext.SeccompProfile)
	assert.Equal(t, corev1.SeccompProfileTypeLocalhost, job.Spec.Template.Spec.SecurityContext.SeccompProfile.Type)
	require.NotNil(t, job.Spec.Template.Spec.SecurityContext.SeccompProfile.LocalhostProfile)
	assert.Equal(t, "profiles/pod.json", *job.Spec.Template.Spec.SecurityContext.SeccompProfile.LocalhostProfile)

	require.NotNil(t, job.Spec.Template.Spec.Affinity)
	require.NotNil(t, job.Spec.Template.Spec.Affinity.NodeAffinity)
	require.NotNil(t, job.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
	require.Len(t, job.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, 1)
	require.Len(t, job.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 1)
	assert.EqualValues(t, 50, job.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].Weight)
	require.NotNil(t, job.Spec.Template.Spec.Affinity.PodAntiAffinity)
	require.Len(t, job.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
	assert.Equal(t, "kubernetes.io/hostname", job.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey)

	require.NotNil(t, job.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.EqualValues(t, 30, *job.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Equal(t, "batch-high", job.Spec.Template.Spec.PriorityClassName)

	require.NotNil(t, job.Spec.PodFailurePolicy)
	require.Len(t, job.Spec.PodFailurePolicy.Rules, 2)
	require.NotNil(t, job.Spec.PodFailurePolicy.Rules[0].OnExitCodes)
	require.NotNil(t, job.Spec.PodFailurePolicy.Rules[0].OnExitCodes.ContainerName)
	assert.Equal(t, "step", *job.Spec.PodFailurePolicy.Rules[0].OnExitCodes.ContainerName)
	assert.Equal(t, []int32{42}, job.Spec.PodFailurePolicy.Rules[0].OnExitCodes.Values)
	require.Len(t, job.Spec.PodFailurePolicy.Rules[1].OnPodConditions, 1)
	assert.Equal(t, corev1.ConditionTrue, job.Spec.PodFailurePolicy.Rules[1].OnPodConditions[0].Status)
}

//go:fix inline
func boolPtr(v bool) *bool {
	return new(v)
}

//go:fix inline
func int64Ptr(v int64) *int64 {
	return new(v)
}

//go:fix inline
func int32Ptr(v int32) *int32 {
	return new(v)
}
