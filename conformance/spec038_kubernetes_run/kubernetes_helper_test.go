// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec038_kubernetes_run_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const testNamespace = "default"

// requireKubernetesCluster skips the test unless a cluster is reachable
// through the current user's kubeconfig, and returns both a clientset for
// verification and the extra env the dagu subprocess needs to find the
// same kubeconfig (the harness isolates HOME, so default discovery would
// otherwise find nothing).
//
// Kubeconfig discovery uses client-go's own standard loading rules -- the
// same ones the kubernetes.run executor itself uses (see
// internal/runtime/builtin/kubernetes/client.go) -- so KUBECONFIG (a
// colon-separated list on Unix, semicolon on Windows) is honored, not just
// the default ~/.kube/config path.
func requireKubernetesCluster(t *testing.T) (*kubernetes.Clientset, []string) {
	t.Helper()

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if !anyPathExists(loadingRules.Precedence) {
		t.Skipf("Skipping Kubernetes-backed conformance test: no kubeconfig found in %v", loadingRules.Precedence)
	}

	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Skipf("Skipping Kubernetes-backed conformance test: failed to load kubeconfig: %v", err)
	}
	restCfg.Timeout = 5 * time.Second

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Skipf("Skipping Kubernetes-backed conformance test: failed to create client: %v", err)
	}
	if _, err := clientset.Discovery().ServerVersion(); err != nil {
		t.Skipf("Skipping Kubernetes-backed conformance test: cluster unreachable: %v", err)
	}

	// Reuse the same resolved path list for the dagu subprocess, joined the
	// same way client-go would split a real KUBECONFIG value back apart.
	kubeconfigEnv := "KUBECONFIG=" + strings.Join(loadingRules.Precedence, string(filepath.ListSeparator))
	return clientset, []string{kubeconfigEnv}
}

func anyPathExists(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// findJobByLabel returns the first Job matching the label selector, or nil.
func findJobByLabel(t *testing.T, clientset *kubernetes.Clientset, labelSelector string) *batchv1.Job {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	jobs, err := clientset.BatchV1().Jobs(testNamespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		t.Fatalf("listing jobs matching %q: %v", labelSelector, err)
	}
	if len(jobs.Items) == 0 {
		return nil
	}
	return &jobs.Items[0]
}

// deleteJobsByLabel force-deletes every Job matching the label selector,
// used defensively before a test (in case a previous run crashed
// mid-test). It fails the test immediately if the deletion request itself
// errors, since a stale Job left behind could otherwise corrupt this
// test's own assertions (findJobByLabel would see it and misreport).
func deleteJobsByLabel(t *testing.T, clientset *kubernetes.Clientset, labelSelector string) {
	t.Helper()

	if err := deleteJobCollection(clientset, labelSelector); err != nil {
		t.Fatalf("deleting jobs matching %q: %v", labelSelector, err)
	}
}

// cleanupJobsByLabel force-deletes every Job matching the label selector as
// post-test cleanup (via t.Cleanup). A failure here can't corrupt this
// test's own result -- it already ran -- but is still reported, so a
// leaked Job doesn't go unnoticed.
func cleanupJobsByLabel(t *testing.T, clientset *kubernetes.Clientset, labelSelector string) {
	t.Helper()

	if err := deleteJobCollection(clientset, labelSelector); err != nil {
		t.Errorf("cleanup: deleting jobs matching %q: %v", labelSelector, err)
	}
}

func deleteJobCollection(clientset *kubernetes.Clientset, labelSelector string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	propagation := metav1.DeletePropagationBackground
	return clientset.BatchV1().Jobs(testNamespace).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &propagation},
		metav1.ListOptions{LabelSelector: labelSelector},
	)
}
