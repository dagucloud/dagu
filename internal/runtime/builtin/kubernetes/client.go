// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var clientPollInterval = 500 * time.Millisecond

// Client wraps the Kubernetes clientset and manages Job lifecycle.
type Client struct {
	clientset kubernetes.Interface
	cfg       *Config
	jobName   string
	namespace string
	mu        sync.Mutex
}

// NewClient creates a Kubernetes client using the given config.
// It discovers the kubeconfig in this order:
// 1. Explicit kubeconfig path from config
// 2. KUBECONFIG env / ~/.kube/config (default loading rules)
// 3. In-cluster config
func NewClient(cfg *Config) (*Client, error) {
	restCfg, err := buildRESTConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Client{
		clientset: clientset,
		cfg:       cfg,
		namespace: cfg.Namespace,
	}, nil
}

func buildRESTConfig(cfg *Config) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	explicitKubeconfig := cfg.Kubeconfig != ""
	explicitKubeconfigEnv := hasExplicitKubeconfigEnv()
	if explicitKubeconfig {
		loadingRules.ExplicitPath = cfg.Kubeconfig
	}

	overrides := &clientcmd.ConfigOverrides{}
	explicitContext := cfg.Context != ""
	if explicitContext {
		overrides.CurrentContext = cfg.Context
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := kubeConfig.ClientConfig()
	if err != nil {
		if explicitKubeconfig || explicitKubeconfigEnv || explicitContext || hasAnyKubeconfigFile(loadingRules.GetLoadingPrecedence()) {
			return nil, fmt.Errorf("kubeconfig error: %w", err)
		}

		restCfg, inClusterErr := rest.InClusterConfig()
		if inClusterErr != nil {
			return nil, fmt.Errorf("kubeconfig error: %w; in-cluster error: %w", err, inClusterErr)
		}
		return restCfg, nil
	}
	return restCfg, nil
}

func hasExplicitKubeconfigEnv() bool {
	return strings.TrimSpace(os.Getenv(clientcmd.RecommendedConfigPathEnvVar)) != ""
}

func hasAnyKubeconfigFile(paths []string) bool {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// CreateJob creates a Kubernetes Job from the config.
// The command parameter overrides the container command if non-empty.
func (c *Client) CreateJob(ctx context.Context, stepName string, command []string) error {
	job, err := c.buildJob(stepName, command)
	if err != nil {
		return err
	}

	created, err := c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create kubernetes job: %w", err)
	}

	c.setJobName(created.Name)

	return nil
}

func (c *Client) buildJob(stepName string, command []string) (*batchv1.Job, error) {
	imagePullPolicy, err := c.cfg.toK8sImagePullPolicy()
	if err != nil {
		return nil, err
	}
	resources, err := c.cfg.toK8sResourceRequirements()
	if err != nil {
		return nil, err
	}
	securityContext, err := c.cfg.toK8sSecurityContext()
	if err != nil {
		return nil, err
	}
	podSecurityContext, err := c.cfg.toK8sPodSecurityContext()
	if err != nil {
		return nil, err
	}
	affinity, err := c.cfg.toK8sAffinity()
	if err != nil {
		return nil, err
	}
	volumes, err := c.cfg.toK8sVolumes()
	if err != nil {
		return nil, err
	}
	podFailurePolicy, err := c.cfg.toK8sPodFailurePolicy()
	if err != nil {
		return nil, err
	}

	// Build container spec
	container := corev1.Container{
		Name:            "step",
		Image:           c.cfg.Image,
		ImagePullPolicy: imagePullPolicy,
		Env:             c.cfg.toK8sEnvVars(),
		EnvFrom:         c.cfg.toK8sEnvFromSources(),
		Resources:       resources,
		VolumeMounts:    c.cfg.toK8sVolumeMounts(),
		SecurityContext: securityContext,
	}

	if c.cfg.WorkingDir != "" {
		container.WorkingDir = c.cfg.WorkingDir
	}

	if len(command) > 0 {
		container.Command = command
	}

	// Generate a safe name prefix from step name
	namePrefix := sanitizeName(stepName) + "-"

	// Build pod spec
	podSpec := corev1.PodSpec{
		Containers:       []corev1.Container{container},
		RestartPolicy:    corev1.RestartPolicyNever,
		Volumes:          volumes,
		ImagePullSecrets: c.cfg.toK8sImagePullSecrets(),
		SecurityContext:  podSecurityContext,
		Affinity:         affinity,
	}

	if c.cfg.ServiceAccount != "" {
		podSpec.ServiceAccountName = c.cfg.ServiceAccount
	}
	if len(c.cfg.NodeSelector) > 0 {
		podSpec.NodeSelector = c.cfg.NodeSelector
	}
	if tols := c.cfg.toK8sTolerations(); len(tols) > 0 {
		podSpec.Tolerations = tols
	}
	if c.cfg.TerminationGracePeriodSeconds != nil {
		podSpec.TerminationGracePeriodSeconds = c.cfg.TerminationGracePeriodSeconds
	}
	if c.cfg.PriorityClassName != "" {
		podSpec.PriorityClassName = c.cfg.PriorityClassName
	}

	// Build Job spec
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: namePrefix,
			Namespace:    c.namespace,
			Labels:       c.cfg.Labels,
			Annotations:  c.cfg.Annotations,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:     c.cfg.BackoffLimit,
			PodFailurePolicy: podFailurePolicy,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      c.cfg.Labels,
					Annotations: c.cfg.Annotations,
				},
				Spec: podSpec,
			},
		},
	}

	if c.cfg.ActiveDeadlineSeconds != nil {
		job.Spec.ActiveDeadlineSeconds = c.cfg.ActiveDeadlineSeconds
	}
	if c.cfg.TTLSecondsAfterFinished != nil {
		job.Spec.TTLSecondsAfterFinished = c.cfg.TTLSecondsAfterFinished
	}

	return job, nil
}

// WaitForPod waits until a Pod created by the Job reaches Running,
// Succeeded, or Failed state and returns its name.
func (c *Client) WaitForPod(ctx context.Context) (string, error) {
	ticker := time.NewTicker(clientPollInterval)
	defer ticker.Stop()

	for {
		podName, err := c.currentPodName(ctx)
		if err != nil {
			return "", err
		}
		if podName != "" {
			return podName, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// StreamLogs streams logs from the given pod to the stdout writer.
// Kubernetes merges stdout and stderr into a single log stream.
func (c *Client) StreamLogs(ctx context.Context, podName string, stdout io.Writer) error {
	req := c.clientset.CoreV1().Pods(c.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		Container: "step",
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to open log stream: %w", err)
	}
	defer stream.Close()

	if _, err := io.Copy(stdout, stream); err != nil {
		// Context cancellation during streaming is expected during Kill
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("log streaming error: %w", err)
	}
	return nil
}

// WaitForCompletion polls the Job until it completes or fails.
func (c *Client) WaitForCompletion(ctx context.Context) error {
	ticker := time.NewTicker(clientPollInterval)
	defer ticker.Stop()

	for {
		done, err := c.currentJobCompletion(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) currentPodName(ctx context.Context) (string, error) {
	jobName := c.GetJobName()
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", nil
	}

	pod := selectPod(pods.Items)
	switch pod.Status.Phase {
	case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
		return pod.Name, nil
	case corev1.PodPending:
		if reason := getPodFailureReason(&pod); reason != "" {
			return "", fmt.Errorf("pod scheduling failed: %s", reason)
		}
	}

	return "", nil
}

func (c *Client) currentJobCompletion(ctx context.Context) (bool, error) {
	jobName := c.GetJobName()
	job, err := c.clientset.BatchV1().Jobs(c.namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get job: %w", err)
	}

	return evaluateJobCompletion(job)
}

func evaluateJobCompletion(job *batchv1.Job) (bool, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			msg := strings.TrimSpace(cond.Message)
			if msg == "" {
				msg = strings.TrimSpace(cond.Reason)
			}
			if msg == "" {
				msg = "job failed"
			}
			return false, fmt.Errorf("job failed: %s", msg)
		}
	}
	return false, nil
}

func selectPod(pods []corev1.Pod) corev1.Pod {
	best := pods[0]
	for _, pod := range pods[1:] {
		if podPriority(pod) > podPriority(best) {
			best = pod
			continue
		}
		if podPriority(pod) == podPriority(best) && pod.CreationTimestamp.After(best.CreationTimestamp.Time) {
			best = pod
		}
	}
	return best
}

func podPriority(pod corev1.Pod) int {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		return 5
	case corev1.PodPending:
		return 4
	case corev1.PodSucceeded:
		return 3
	case corev1.PodFailed:
		return 2
	default:
		return 1
	}
}

// GetExitCode retrieves the exit code from the terminated container in the pod.
func (c *Client) GetExitCode(ctx context.Context, podName string) (int, error) {
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return -1, fmt.Errorf("failed to get pod: %w", err)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "step" && cs.State.Terminated != nil {
			return int(cs.State.Terminated.ExitCode), nil
		}
	}

	return -1, fmt.Errorf("container exit code not available")
}

// DeleteJob deletes the Job and its Pods using background propagation.
func (c *Client) DeleteJob(ctx context.Context) error {
	jobName := c.GetJobName()

	if jobName == "" {
		return nil
	}

	propagation := metav1.DeletePropagationBackground
	err := c.clientset.BatchV1().Jobs(c.namespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.setJobName("")
			return nil
		}
		return fmt.Errorf("failed to delete job %s: %w", jobName, err)
	}
	c.setJobName("")
	return nil
}

// GetJobName returns the name of the created Job.
func (c *Client) GetJobName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jobName
}

func (c *Client) setJobName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobName = name
}

// getPodFailureReason checks for known failure conditions in pod status.
func getPodFailureReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
				return fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
		}
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			return fmt.Sprintf("unschedulable: %s", cond.Message)
		}
	}
	return ""
}

// sanitizeName converts a step name to a valid Kubernetes resource name prefix.
// Kubernetes names must be lowercase, alphanumeric, or '-', and start with a letter.
func sanitizeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == '_' || r == ' ' {
			b.WriteRune('-')
		}
	}
	result := b.String()
	// Trim leading non-alpha characters
	for len(result) > 0 && (result[0] < 'a' || result[0] > 'z') {
		result = result[1:]
	}
	// Trim trailing hyphens
	result = strings.TrimRight(result, "-")
	if result == "" {
		result = "dagu-k8s"
	}
	// Kubernetes GenerateName adds a suffix, keep prefix short
	const maxLen = 50
	if len(result) > maxLen {
		result = result[:maxLen]
	}
	return result
}

// WaitForPodTermination polls the pod until its container terminates.
// This is used after log streaming completes to ensure we can read the exit code.
func (c *Client) WaitForPodTermination(ctx context.Context, podName string) error {
	ticker := time.NewTicker(clientPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get pod status: %w", err)
			}
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				return nil
			}
		}
	}
}
