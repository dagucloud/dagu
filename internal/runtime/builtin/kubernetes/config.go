// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ErrImageRequired             = errors.New("image is required")
	ErrInvalidCleanupPolicy      = errors.New("cleanup_policy must be either delete or keep")
	ErrInvalidImagePullPolicy    = errors.New("image_pull_policy must be one of Always, IfNotPresent, or Never")
	ErrNegativeActiveDeadline    = errors.New("active_deadline must be >= 0")
	ErrNegativeBackoffLimit      = errors.New("backoff_limit must be >= 0")
	ErrNegativeTTLAfterFinished  = errors.New("ttl_after_finished must be >= 0")
	ErrNegativeTerminationGrace  = errors.New("termination_grace_period_seconds must be >= 0")
	ErrNegativeQuantity          = errors.New("resource quantity must be >= 0")
	ErrInvalidVolumeSource       = errors.New("volume must define exactly one source")
	ErrUnsupportedFallbackPolicy = errors.New("fallback is not supported for kubernetes executor (use Always, IfNotPresent, or Never)")
)

const (
	cleanupPolicyDelete = "delete"
	cleanupPolicyKeep   = "keep"
)

// Config holds the configuration for creating a Kubernetes Job.
type Config struct {
	// Kubeconfig is the path to the kubeconfig file.
	// If empty, uses default discovery (KUBECONFIG env, ~/.kube/config, in-cluster).
	Kubeconfig string `mapstructure:"kubeconfig"`
	// Context is the kubeconfig context to use. If empty, uses current-context.
	Context string `mapstructure:"context"`

	// Namespace is the Kubernetes namespace. Default: "default".
	Namespace string `mapstructure:"namespace"`
	// Image is the container image to use (required).
	Image string `mapstructure:"image"`
	// ImagePullPolicy is the image pull policy (Always, IfNotPresent, Never).
	ImagePullPolicy string `mapstructure:"image_pull_policy"`
	// ImagePullSecrets are the names of secrets for pulling images from private registries.
	ImagePullSecrets []string `mapstructure:"image_pull_secrets"`

	// WorkingDir is the working directory inside the container.
	WorkingDir string `mapstructure:"working_dir"`
	// Env specifies environment variables for the container.
	Env []EnvVar `mapstructure:"env"`
	// EnvFrom specifies sources to populate environment variables.
	EnvFrom []EnvFromSource `mapstructure:"env_from"`

	// Resources specifies CPU/memory requests and limits.
	Resources *ResourceRequirements `mapstructure:"resources"`
	// ServiceAccount is the service account to use for the pod.
	ServiceAccount string `mapstructure:"service_account"`
	// NodeSelector constrains which nodes the pod can run on.
	NodeSelector map[string]string `mapstructure:"node_selector"`
	// Tolerations allow the pod to schedule onto nodes with matching taints.
	Tolerations []Toleration `mapstructure:"tolerations"`
	// SecurityContext configures container-level Linux security settings.
	SecurityContext *SecurityContext `mapstructure:"security_context"`
	// PodSecurityContext configures Pod-level Linux security defaults.
	PodSecurityContext *PodSecurityContext `mapstructure:"pod_security_context"`
	// Affinity configures node and pod scheduling affinity.
	Affinity *Affinity `mapstructure:"affinity"`
	// TerminationGracePeriodSeconds controls graceful shutdown time for the Pod.
	TerminationGracePeriodSeconds *int64 `mapstructure:"termination_grace_period_seconds"`
	// PriorityClassName sets the Pod priority class.
	PriorityClassName string `mapstructure:"priority_class_name"`

	// Labels are applied to the Job and Pod.
	Labels map[string]string `mapstructure:"labels"`
	// Annotations are applied to the Job and Pod.
	Annotations map[string]string `mapstructure:"annotations"`

	// Volumes defines volumes available to the pod.
	Volumes []Volume `mapstructure:"volumes"`
	// VolumeMounts defines mount points for volumes in the container.
	VolumeMounts []VolumeMount `mapstructure:"volume_mounts"`

	// ActiveDeadlineSeconds is the Kubernetes-native timeout for the Job in seconds.
	ActiveDeadlineSeconds *int64 `mapstructure:"active_deadline"`
	// BackoffLimit is the number of retries before considering the Job failed. Default: 0.
	BackoffLimit *int32 `mapstructure:"backoff_limit"`
	// TTLSecondsAfterFinished controls automatic cleanup by Kubernetes.
	TTLSecondsAfterFinished *int32 `mapstructure:"ttl_after_finished"`

	// CleanupPolicy controls whether to delete the Job after completion.
	// "delete" (default) or "keep".
	CleanupPolicy string `mapstructure:"cleanup_policy"`
	// PodFailurePolicy configures Kubernetes-native Job failure handling.
	PodFailurePolicy *PodFailurePolicy `mapstructure:"pod_failure_policy"`
}

// EnvVar represents a Kubernetes environment variable.
type EnvVar struct {
	Name      string        `mapstructure:"name"`
	Value     string        `mapstructure:"value"`
	ValueFrom *EnvVarSource `mapstructure:"value_from"`
}

// EnvVarSource represents a source for an environment variable's value.
type EnvVarSource struct {
	SecretKeyRef    *KeySelector `mapstructure:"secret_key_ref"`
	ConfigMapKeyRef *KeySelector `mapstructure:"config_map_key_ref"`
	FieldRef        *FieldRef    `mapstructure:"field_ref"`
}

// KeySelector selects a key from a ConfigMap or Secret.
type KeySelector struct {
	Name string `mapstructure:"name"`
	Key  string `mapstructure:"key"`
}

// FieldRef selects a field of the pod.
type FieldRef struct {
	FieldPath string `mapstructure:"field_path"`
}

// EnvFromSource represents a source to populate environment variables.
type EnvFromSource struct {
	ConfigMapRef *EnvFromRef `mapstructure:"config_map_ref"`
	SecretRef    *EnvFromRef `mapstructure:"secret_ref"`
	Prefix       string      `mapstructure:"prefix"`
}

// EnvFromRef references a ConfigMap or Secret for envFrom.
type EnvFromRef struct {
	Name string `mapstructure:"name"`
}

// ResourceRequirements specifies CPU and memory requests and limits.
type ResourceRequirements struct {
	Requests map[string]string `mapstructure:"requests"`
	Limits   map[string]string `mapstructure:"limits"`
}

// Toleration represents a Kubernetes toleration.
type Toleration struct {
	Key      string `mapstructure:"key"`
	Operator string `mapstructure:"operator"`
	Value    string `mapstructure:"value"`
	Effect   string `mapstructure:"effect"`
}

// SecurityContext configures container-level Linux security settings.
type SecurityContext struct {
	RunAsUser                *int64          `mapstructure:"run_as_user"`
	RunAsGroup               *int64          `mapstructure:"run_as_group"`
	RunAsNonRoot             *bool           `mapstructure:"run_as_non_root"`
	Privileged               *bool           `mapstructure:"privileged"`
	ReadOnlyRootFilesystem   *bool           `mapstructure:"read_only_root_filesystem"`
	AllowPrivilegeEscalation *bool           `mapstructure:"allow_privilege_escalation"`
	Capabilities             *Capabilities   `mapstructure:"capabilities"`
	SeccompProfile           *SeccompProfile `mapstructure:"seccomp_profile"`
}

// Capabilities configures Linux capabilities to add or drop.
type Capabilities struct {
	Add  []string `mapstructure:"add"`
	Drop []string `mapstructure:"drop"`
}

// SeccompProfile configures Linux seccomp behavior.
type SeccompProfile struct {
	Type             string `mapstructure:"type"`
	LocalhostProfile string `mapstructure:"localhost_profile"`
}

// PodSecurityContext configures Pod-level Linux security defaults.
type PodSecurityContext struct {
	RunAsUser           *int64          `mapstructure:"run_as_user"`
	RunAsGroup          *int64          `mapstructure:"run_as_group"`
	RunAsNonRoot        *bool           `mapstructure:"run_as_non_root"`
	FSGroup             *int64          `mapstructure:"fs_group"`
	FSGroupChangePolicy string          `mapstructure:"fs_group_change_policy"`
	SupplementalGroups  []int64         `mapstructure:"supplemental_groups"`
	Sysctls             []Sysctl        `mapstructure:"sysctls"`
	SeccompProfile      *SeccompProfile `mapstructure:"seccomp_profile"`
}

// Sysctl configures a namespaced Linux sysctl for the Pod.
type Sysctl struct {
	Name  string `mapstructure:"name"`
	Value string `mapstructure:"value"`
}

// Affinity configures node and pod scheduling rules.
type Affinity struct {
	NodeAffinity    *NodeAffinity `mapstructure:"node_affinity"`
	PodAffinity     *PodAffinity  `mapstructure:"pod_affinity"`
	PodAntiAffinity *PodAffinity  `mapstructure:"pod_anti_affinity"`
}

// NodeAffinity configures node selector affinity.
type NodeAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution  *NodeSelector             `mapstructure:"required_during_scheduling_ignored_during_execution"`
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredSchedulingTerm `mapstructure:"preferred_during_scheduling_ignored_during_execution"`
}

// NodeSelector is a disjunction of node selector terms.
type NodeSelector struct {
	NodeSelectorTerms []NodeSelectorTerm `mapstructure:"node_selector_terms"`
}

// NodeSelectorTerm matches nodes by expressions.
type NodeSelectorTerm struct {
	MatchExpressions []NodeSelectorRequirement `mapstructure:"match_expressions"`
}

// NodeSelectorRequirement matches a node label requirement.
type NodeSelectorRequirement struct {
	Key      string   `mapstructure:"key"`
	Operator string   `mapstructure:"operator"`
	Values   []string `mapstructure:"values"`
}

// PodAffinity configures pod affinity or anti-affinity rules.
type PodAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution  []PodAffinityTerm         `mapstructure:"required_during_scheduling_ignored_during_execution"`
	PreferredDuringSchedulingIgnoredDuringExecution []WeightedPodAffinityTerm `mapstructure:"preferred_during_scheduling_ignored_during_execution"`
}

// PodAffinityTerm selects pods relative to topology.
type PodAffinityTerm struct {
	LabelSelector     *LabelSelector `mapstructure:"label_selector"`
	Namespaces        []string       `mapstructure:"namespaces"`
	NamespaceSelector *LabelSelector `mapstructure:"namespace_selector"`
	TopologyKey       string         `mapstructure:"topology_key"`
}

// WeightedPodAffinityTerm gives a pod affinity term a weight.
type WeightedPodAffinityTerm struct {
	Weight          int32           `mapstructure:"weight"`
	PodAffinityTerm PodAffinityTerm `mapstructure:"pod_affinity_term"`
}

// LabelSelector is a typed subset of metav1.LabelSelector.
type LabelSelector struct {
	MatchLabels      map[string]string          `mapstructure:"match_labels"`
	MatchExpressions []LabelSelectorRequirement `mapstructure:"match_expressions"`
}

// LabelSelectorRequirement matches Kubernetes label-selector expressions.
type LabelSelectorRequirement struct {
	Key      string   `mapstructure:"key"`
	Operator string   `mapstructure:"operator"`
	Values   []string `mapstructure:"values"`
}

// PreferredSchedulingTerm gives a node selector preference a weight.
type PreferredSchedulingTerm struct {
	Weight     int32            `mapstructure:"weight"`
	Preference NodeSelectorTerm `mapstructure:"preference"`
}

// PodFailurePolicy configures Kubernetes-native Job failure handling.
type PodFailurePolicy struct {
	Rules []PodFailurePolicyRule `mapstructure:"rules"`
}

// PodFailurePolicyRule matches either exit codes or pod conditions.
type PodFailurePolicyRule struct {
	Action          string                                   `mapstructure:"action"`
	OnExitCodes     *PodFailurePolicyOnExitCodesRequirement  `mapstructure:"on_exit_codes"`
	OnPodConditions []PodFailurePolicyOnPodConditionsPattern `mapstructure:"on_pod_conditions"`
}

// PodFailurePolicyOnExitCodesRequirement matches failed container exit codes.
type PodFailurePolicyOnExitCodesRequirement struct {
	ContainerName string  `mapstructure:"container_name"`
	Operator      string  `mapstructure:"operator"`
	Values        []int32 `mapstructure:"values"`
}

// PodFailurePolicyOnPodConditionsPattern matches pod conditions.
type PodFailurePolicyOnPodConditionsPattern struct {
	Type   string `mapstructure:"type"`
	Status string `mapstructure:"status"`
}

// Volume defines a volume available to the pod.
type Volume struct {
	Name                  string     `mapstructure:"name"`
	EmptyDir              *EmptyDir  `mapstructure:"empty_dir"`
	HostPath              *HostPath  `mapstructure:"host_path"`
	ConfigMap             *ConfigMap `mapstructure:"config_map"`
	Secret                *SecretVol `mapstructure:"secret"`
	PersistentVolumeClaim *PVCVol    `mapstructure:"persistent_volume_claim"`
}

// EmptyDir represents an emptyDir volume source.
type EmptyDir struct {
	Medium    string `mapstructure:"medium"`
	SizeLimit string `mapstructure:"size_limit"`
}

// HostPath represents a hostPath volume source.
type HostPath struct {
	Path string `mapstructure:"path"`
	Type string `mapstructure:"type"`
}

// ConfigMap represents a configMap volume source.
type ConfigMap struct {
	Name string `mapstructure:"name"`
}

// SecretVol represents a secret volume source.
type SecretVol struct {
	SecretName string `mapstructure:"secret_name"`
}

// PVCVol represents a persistentVolumeClaim volume source.
type PVCVol struct {
	ClaimName string `mapstructure:"claim_name"`
	ReadOnly  bool   `mapstructure:"read_only"`
}

// VolumeMount defines a mount point for a volume in a container.
type VolumeMount struct {
	Name      string `mapstructure:"name"`
	MountPath string `mapstructure:"mount_path"`
	SubPath   string `mapstructure:"sub_path"`
	ReadOnly  bool   `mapstructure:"read_only"`
}

// LoadConfigFromMap decodes a config map into a Config struct and applies defaults.
func LoadConfigFromMap(data map[string]any) (*Config, error) {
	cfg := &Config{}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := decoder.Decode(data); err != nil {
		return nil, fmt.Errorf("failed to decode kubernetes config: %w", err)
	}

	normalizeConfig(cfg)
	applyDefaults(cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func normalizeConfig(cfg *Config) {
	cfg.Kubeconfig = strings.TrimSpace(cfg.Kubeconfig)
	cfg.Context = strings.TrimSpace(cfg.Context)
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)
	cfg.Image = strings.TrimSpace(cfg.Image)
	cfg.ImagePullPolicy = strings.TrimSpace(cfg.ImagePullPolicy)
	cfg.PriorityClassName = strings.TrimSpace(cfg.PriorityClassName)
	cfg.CleanupPolicy = strings.ToLower(strings.TrimSpace(cfg.CleanupPolicy))
}

func applyDefaults(cfg *Config) {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.CleanupPolicy == "" {
		cfg.CleanupPolicy = cleanupPolicyDelete
	}
	if cfg.BackoffLimit == nil {
		zero := int32(0)
		cfg.BackoffLimit = &zero
	}
}

func validateConfig(cfg *Config) error {
	if cfg.Image == "" {
		return ErrImageRequired
	}
	if _, err := cfg.toK8sImagePullPolicy(); err != nil {
		return err
	}
	switch cfg.CleanupPolicy {
	case cleanupPolicyDelete, cleanupPolicyKeep:
	default:
		return ErrInvalidCleanupPolicy
	}
	if cfg.ActiveDeadlineSeconds != nil && *cfg.ActiveDeadlineSeconds < 0 {
		return ErrNegativeActiveDeadline
	}
	if cfg.BackoffLimit != nil && *cfg.BackoffLimit < 0 {
		return ErrNegativeBackoffLimit
	}
	if cfg.TTLSecondsAfterFinished != nil && *cfg.TTLSecondsAfterFinished < 0 {
		return ErrNegativeTTLAfterFinished
	}
	if cfg.TerminationGracePeriodSeconds != nil && *cfg.TerminationGracePeriodSeconds < 0 {
		return ErrNegativeTerminationGrace
	}
	if _, err := cfg.toK8sResourceRequirements(); err != nil {
		return err
	}
	if err := validateEnvVars(cfg.Env); err != nil {
		return err
	}
	if err := validateEnvFromSources(cfg.EnvFrom); err != nil {
		return err
	}
	if _, err := cfg.toK8sVolumes(); err != nil {
		return err
	}
	if _, err := cfg.toK8sSecurityContext(); err != nil {
		return err
	}
	if _, err := cfg.toK8sPodSecurityContext(); err != nil {
		return err
	}
	if _, err := cfg.toK8sAffinity(); err != nil {
		return err
	}
	if _, err := cfg.toK8sPodFailurePolicy(); err != nil {
		return err
	}
	return nil
}

func validateEnvVars(env []EnvVar) error {
	for i, entry := range env {
		field := fmt.Sprintf("env[%d]", i)
		if strings.TrimSpace(entry.Name) == "" {
			return fmt.Errorf("%s.name: required", field)
		}
		if entry.Value != "" && entry.ValueFrom != nil {
			return fmt.Errorf("%s: value and value_from are mutually exclusive", field)
		}
		if entry.ValueFrom == nil {
			continue
		}
		if err := validateEnvVarSource(field+".value_from", entry.ValueFrom); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvVarSource(field string, src *EnvVarSource) error {
	switch countEnvVarSources(src) {
	case 0:
		return fmt.Errorf("%s: must define exactly one source", field)
	case 1:
	default:
		return fmt.Errorf("%s: must define exactly one source", field)
	}

	if src.SecretKeyRef != nil {
		if strings.TrimSpace(src.SecretKeyRef.Name) == "" {
			return fmt.Errorf("%s.secret_key_ref.name: required", field)
		}
		if strings.TrimSpace(src.SecretKeyRef.Key) == "" {
			return fmt.Errorf("%s.secret_key_ref.key: required", field)
		}
	}
	if src.ConfigMapKeyRef != nil {
		if strings.TrimSpace(src.ConfigMapKeyRef.Name) == "" {
			return fmt.Errorf("%s.config_map_key_ref.name: required", field)
		}
		if strings.TrimSpace(src.ConfigMapKeyRef.Key) == "" {
			return fmt.Errorf("%s.config_map_key_ref.key: required", field)
		}
	}
	if src.FieldRef != nil && strings.TrimSpace(src.FieldRef.FieldPath) == "" {
		return fmt.Errorf("%s.field_ref.field_path: required", field)
	}
	return nil
}

func countEnvVarSources(src *EnvVarSource) int {
	count := 0
	if src == nil {
		return count
	}
	if src.SecretKeyRef != nil {
		count++
	}
	if src.ConfigMapKeyRef != nil {
		count++
	}
	if src.FieldRef != nil {
		count++
	}
	return count
}

func validateEnvFromSources(sources []EnvFromSource) error {
	for i, source := range sources {
		field := fmt.Sprintf("env_from[%d]", i)
		switch countEnvFromSources(source) {
		case 0:
			return fmt.Errorf("%s: must define exactly one source", field)
		case 1:
		default:
			return fmt.Errorf("%s: must define exactly one source", field)
		}
		if source.ConfigMapRef != nil && strings.TrimSpace(source.ConfigMapRef.Name) == "" {
			return fmt.Errorf("%s.config_map_ref.name: required", field)
		}
		if source.SecretRef != nil && strings.TrimSpace(source.SecretRef.Name) == "" {
			return fmt.Errorf("%s.secret_ref.name: required", field)
		}
	}
	return nil
}

func countEnvFromSources(source EnvFromSource) int {
	count := 0
	if source.ConfigMapRef != nil {
		count++
	}
	if source.SecretRef != nil {
		count++
	}
	return count
}

// toK8sEnvVars converts config EnvVars to Kubernetes API types.
func (cfg *Config) toK8sEnvVars() []corev1.EnvVar {
	if len(cfg.Env) == 0 {
		return nil
	}
	envVars := make([]corev1.EnvVar, 0, len(cfg.Env))
	for _, e := range cfg.Env {
		ev := corev1.EnvVar{
			Name:  e.Name,
			Value: e.Value,
		}
		if e.ValueFrom != nil {
			ev.ValueFrom = &corev1.EnvVarSource{}
			if e.ValueFrom.SecretKeyRef != nil {
				ev.ValueFrom.SecretKeyRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.ValueFrom.SecretKeyRef.Name},
					Key:                  e.ValueFrom.SecretKeyRef.Key,
				}
			}
			if e.ValueFrom.ConfigMapKeyRef != nil {
				ev.ValueFrom.ConfigMapKeyRef = &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.ValueFrom.ConfigMapKeyRef.Name},
					Key:                  e.ValueFrom.ConfigMapKeyRef.Key,
				}
			}
			if e.ValueFrom.FieldRef != nil {
				ev.ValueFrom.FieldRef = &corev1.ObjectFieldSelector{
					FieldPath: e.ValueFrom.FieldRef.FieldPath,
				}
			}
		}
		envVars = append(envVars, ev)
	}
	return envVars
}

// toK8sEnvFromSources converts config EnvFromSources to Kubernetes API types.
func (cfg *Config) toK8sEnvFromSources() []corev1.EnvFromSource {
	if len(cfg.EnvFrom) == 0 {
		return nil
	}
	sources := make([]corev1.EnvFromSource, 0, len(cfg.EnvFrom))
	for _, ef := range cfg.EnvFrom {
		s := corev1.EnvFromSource{
			Prefix: ef.Prefix,
		}
		if ef.ConfigMapRef != nil {
			s.ConfigMapRef = &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: ef.ConfigMapRef.Name},
			}
		}
		if ef.SecretRef != nil {
			s.SecretRef = &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: ef.SecretRef.Name},
			}
		}
		sources = append(sources, s)
	}
	return sources
}

// toK8sResourceRequirements converts config resources to Kubernetes API types.
func (cfg *Config) toK8sResourceRequirements() (corev1.ResourceRequirements, error) {
	reqs := corev1.ResourceRequirements{}
	if cfg.Resources == nil {
		return reqs, nil
	}
	if len(cfg.Resources.Requests) > 0 {
		reqs.Requests = make(corev1.ResourceList)
		for k, v := range cfg.Resources.Requests {
			qty, err := parseQuantity(fmt.Sprintf("resources.requests.%s", k), v)
			if err != nil {
				return corev1.ResourceRequirements{}, err
			}
			reqs.Requests[corev1.ResourceName(k)] = qty
		}
	}
	if len(cfg.Resources.Limits) > 0 {
		reqs.Limits = make(corev1.ResourceList)
		for k, v := range cfg.Resources.Limits {
			qty, err := parseQuantity(fmt.Sprintf("resources.limits.%s", k), v)
			if err != nil {
				return corev1.ResourceRequirements{}, err
			}
			reqs.Limits[corev1.ResourceName(k)] = qty
		}
	}
	return reqs, nil
}

// toK8sTolerations converts config tolerations to Kubernetes API types.
func (cfg *Config) toK8sTolerations() []corev1.Toleration {
	if len(cfg.Tolerations) == 0 {
		return nil
	}
	tols := make([]corev1.Toleration, 0, len(cfg.Tolerations))
	for _, t := range cfg.Tolerations {
		tols = append(tols, corev1.Toleration{
			Key:      t.Key,
			Operator: corev1.TolerationOperator(t.Operator),
			Value:    t.Value,
			Effect:   corev1.TaintEffect(t.Effect),
		})
	}
	return tols
}

// toK8sSecurityContext converts config container security context to Kubernetes API types.
func (cfg *Config) toK8sSecurityContext() (*corev1.SecurityContext, error) {
	if isEmptySecurityContext(cfg.SecurityContext) {
		return nil, nil
	}
	if err := validateSecurityContext("security_context", cfg.SecurityContext); err != nil {
		return nil, err
	}

	seccompProfile, err := toK8sSeccompProfile("security_context.seccomp_profile", cfg.SecurityContext.SeccompProfile)
	if err != nil {
		return nil, err
	}

	securityContext := &corev1.SecurityContext{
		RunAsUser:                cfg.SecurityContext.RunAsUser,
		RunAsGroup:               cfg.SecurityContext.RunAsGroup,
		RunAsNonRoot:             cfg.SecurityContext.RunAsNonRoot,
		Privileged:               cfg.SecurityContext.Privileged,
		ReadOnlyRootFilesystem:   cfg.SecurityContext.ReadOnlyRootFilesystem,
		AllowPrivilegeEscalation: cfg.SecurityContext.AllowPrivilegeEscalation,
		SeccompProfile:           seccompProfile,
	}
	if caps := toK8sCapabilities(cfg.SecurityContext.Capabilities); caps != nil {
		securityContext.Capabilities = caps
	}
	return securityContext, nil
}

// toK8sPodSecurityContext converts config pod security context to Kubernetes API types.
func (cfg *Config) toK8sPodSecurityContext() (*corev1.PodSecurityContext, error) {
	if isEmptyPodSecurityContext(cfg.PodSecurityContext) {
		return nil, nil
	}
	if err := validatePodSecurityContext("pod_security_context", cfg.PodSecurityContext); err != nil {
		return nil, err
	}

	seccompProfile, err := toK8sSeccompProfile("pod_security_context.seccomp_profile", cfg.PodSecurityContext.SeccompProfile)
	if err != nil {
		return nil, err
	}

	podSecurityContext := &corev1.PodSecurityContext{
		RunAsUser:          cfg.PodSecurityContext.RunAsUser,
		RunAsGroup:         cfg.PodSecurityContext.RunAsGroup,
		RunAsNonRoot:       cfg.PodSecurityContext.RunAsNonRoot,
		FSGroup:            cfg.PodSecurityContext.FSGroup,
		SupplementalGroups: append([]int64(nil), cfg.PodSecurityContext.SupplementalGroups...),
		SeccompProfile:     seccompProfile,
	}
	if len(cfg.PodSecurityContext.Sysctls) > 0 {
		podSecurityContext.Sysctls = make([]corev1.Sysctl, 0, len(cfg.PodSecurityContext.Sysctls))
		for _, sysctl := range cfg.PodSecurityContext.Sysctls {
			podSecurityContext.Sysctls = append(podSecurityContext.Sysctls, corev1.Sysctl{
				Name:  strings.TrimSpace(sysctl.Name),
				Value: strings.TrimSpace(sysctl.Value),
			})
		}
	}
	if policy := strings.TrimSpace(cfg.PodSecurityContext.FSGroupChangePolicy); policy != "" {
		fsGroupPolicy := corev1.PodFSGroupChangePolicy(policy)
		podSecurityContext.FSGroupChangePolicy = &fsGroupPolicy
	}
	return podSecurityContext, nil
}

// toK8sAffinity converts config affinity to Kubernetes API types.
func (cfg *Config) toK8sAffinity() (*corev1.Affinity, error) {
	if isEmptyAffinity(cfg.Affinity) {
		return nil, nil
	}
	if err := validateAffinity("affinity", cfg.Affinity); err != nil {
		return nil, err
	}

	affinity := &corev1.Affinity{}
	if cfg.Affinity.NodeAffinity != nil {
		nodeAffinity := &corev1.NodeAffinity{}
		if selector := cfg.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution; selector != nil && len(selector.NodeSelectorTerms) > 0 {
			nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
				NodeSelectorTerms: toK8sNodeSelectorTerms(selector.NodeSelectorTerms),
			}
		}
		if len(cfg.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = make([]corev1.PreferredSchedulingTerm, 0, len(cfg.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution))
			for _, term := range cfg.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
				nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, corev1.PreferredSchedulingTerm{
					Weight:     term.Weight,
					Preference: toK8sNodeSelectorTerm(term.Preference),
				})
			}
		}
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil || len(nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			affinity.NodeAffinity = nodeAffinity
		}
	}
	if cfg.Affinity.PodAffinity != nil {
		podAffinity := &corev1.PodAffinity{}
		if len(cfg.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 {
			podAffinity.RequiredDuringSchedulingIgnoredDuringExecution = make([]corev1.PodAffinityTerm, 0, len(cfg.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution))
			for _, term := range cfg.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
				podAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(podAffinity.RequiredDuringSchedulingIgnoredDuringExecution, toK8sPodAffinityTerm(term))
			}
		}
		if len(cfg.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			podAffinity.PreferredDuringSchedulingIgnoredDuringExecution = make([]corev1.WeightedPodAffinityTerm, 0, len(cfg.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution))
			for _, term := range cfg.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
				podAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(podAffinity.PreferredDuringSchedulingIgnoredDuringExecution, corev1.WeightedPodAffinityTerm{
					Weight:          term.Weight,
					PodAffinityTerm: toK8sPodAffinityTerm(term.PodAffinityTerm),
				})
			}
		}
		if len(podAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 || len(podAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			affinity.PodAffinity = podAffinity
		}
	}
	if cfg.Affinity.PodAntiAffinity != nil {
		podAntiAffinity := &corev1.PodAntiAffinity{}
		if len(cfg.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 {
			podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = make([]corev1.PodAffinityTerm, 0, len(cfg.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution))
			for _, term := range cfg.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
				podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, toK8sPodAffinityTerm(term))
			}
		}
		if len(cfg.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = make([]corev1.WeightedPodAffinityTerm, 0, len(cfg.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution))
			for _, term := range cfg.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
				podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, corev1.WeightedPodAffinityTerm{
					Weight:          term.Weight,
					PodAffinityTerm: toK8sPodAffinityTerm(term.PodAffinityTerm),
				})
			}
		}
		if len(podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) > 0 || len(podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) > 0 {
			affinity.PodAntiAffinity = podAntiAffinity
		}
	}
	if affinity.NodeAffinity == nil && affinity.PodAffinity == nil && affinity.PodAntiAffinity == nil {
		return nil, nil
	}
	return affinity, nil
}

// toK8sPodFailurePolicy converts config pod failure policy to Kubernetes API types.
func (cfg *Config) toK8sPodFailurePolicy() (*batchv1.PodFailurePolicy, error) {
	if cfg.PodFailurePolicy == nil || len(cfg.PodFailurePolicy.Rules) == 0 {
		return nil, nil
	}
	if err := validatePodFailurePolicy("pod_failure_policy", cfg.PodFailurePolicy); err != nil {
		return nil, err
	}

	rules := make([]batchv1.PodFailurePolicyRule, 0, len(cfg.PodFailurePolicy.Rules))
	for _, rule := range cfg.PodFailurePolicy.Rules {
		k8sRule := batchv1.PodFailurePolicyRule{
			Action: batchv1.PodFailurePolicyAction(strings.TrimSpace(rule.Action)),
		}
		if rule.OnExitCodes != nil {
			k8sRule.OnExitCodes = &batchv1.PodFailurePolicyOnExitCodesRequirement{
				Operator: batchv1.PodFailurePolicyOnExitCodesOperator(strings.TrimSpace(rule.OnExitCodes.Operator)),
				Values:   append([]int32(nil), rule.OnExitCodes.Values...),
			}
			if name := strings.TrimSpace(rule.OnExitCodes.ContainerName); name != "" {
				k8sRule.OnExitCodes.ContainerName = &name
			}
		}
		if len(rule.OnPodConditions) > 0 {
			k8sRule.OnPodConditions = make([]batchv1.PodFailurePolicyOnPodConditionsPattern, 0, len(rule.OnPodConditions))
			for _, pattern := range rule.OnPodConditions {
				status := corev1.ConditionTrue
				if trimmedStatus := strings.TrimSpace(pattern.Status); trimmedStatus != "" {
					status = corev1.ConditionStatus(trimmedStatus)
				}
				k8sRule.OnPodConditions = append(k8sRule.OnPodConditions, batchv1.PodFailurePolicyOnPodConditionsPattern{
					Type:   corev1.PodConditionType(strings.TrimSpace(pattern.Type)),
					Status: status,
				})
			}
		}
		rules = append(rules, k8sRule)
	}

	return &batchv1.PodFailurePolicy{Rules: rules}, nil
}

func validateSecurityContext(field string, securityContext *SecurityContext) error {
	if isEmptySecurityContext(securityContext) {
		return nil
	}
	if err := validateNonNegativeInt64Ptr(field+".run_as_user", securityContext.RunAsUser); err != nil {
		return err
	}
	if err := validateNonNegativeInt64Ptr(field+".run_as_group", securityContext.RunAsGroup); err != nil {
		return err
	}
	if err := validateCapabilities(field+".capabilities", securityContext.Capabilities); err != nil {
		return err
	}
	if err := validateSeccompProfile(field+".seccomp_profile", securityContext.SeccompProfile); err != nil {
		return err
	}
	return nil
}

func validatePodSecurityContext(field string, podSecurityContext *PodSecurityContext) error {
	if isEmptyPodSecurityContext(podSecurityContext) {
		return nil
	}
	if err := validateNonNegativeInt64Ptr(field+".run_as_user", podSecurityContext.RunAsUser); err != nil {
		return err
	}
	if err := validateNonNegativeInt64Ptr(field+".run_as_group", podSecurityContext.RunAsGroup); err != nil {
		return err
	}
	if err := validateNonNegativeInt64Ptr(field+".fs_group", podSecurityContext.FSGroup); err != nil {
		return err
	}
	for i, group := range podSecurityContext.SupplementalGroups {
		if group < 0 {
			return fmt.Errorf("%s.supplemental_groups[%d]: must be >= 0", field, i)
		}
	}
	if policy := strings.TrimSpace(podSecurityContext.FSGroupChangePolicy); policy != "" {
		switch policy {
		case string(corev1.FSGroupChangeAlways), string(corev1.FSGroupChangeOnRootMismatch):
		default:
			return fmt.Errorf("%s.fs_group_change_policy: must be Always or OnRootMismatch", field)
		}
	}
	for i, sysctl := range podSecurityContext.Sysctls {
		if strings.TrimSpace(sysctl.Name) == "" {
			return fmt.Errorf("%s.sysctls[%d].name: required", field, i)
		}
		if strings.TrimSpace(sysctl.Value) == "" {
			return fmt.Errorf("%s.sysctls[%d].value: required", field, i)
		}
	}
	if err := validateSeccompProfile(field+".seccomp_profile", podSecurityContext.SeccompProfile); err != nil {
		return err
	}
	return nil
}

func validateAffinity(field string, affinity *Affinity) error {
	if isEmptyAffinity(affinity) {
		return nil
	}
	if affinity.NodeAffinity != nil {
		if selector := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution; selector != nil {
			for i, term := range selector.NodeSelectorTerms {
				if err := validateNodeSelectorTerm(fmt.Sprintf("%s.node_affinity.required_during_scheduling_ignored_during_execution.node_selector_terms[%d]", field, i), term); err != nil {
					return err
				}
			}
		}
		for i, term := range affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			if term.Weight < 1 || term.Weight > 100 {
				return fmt.Errorf("%s.node_affinity.preferred_during_scheduling_ignored_during_execution[%d].weight: must be between 1 and 100", field, i)
			}
			if err := validateNodeSelectorTerm(fmt.Sprintf("%s.node_affinity.preferred_during_scheduling_ignored_during_execution[%d].preference", field, i), term.Preference); err != nil {
				return err
			}
		}
	}
	if err := validatePodAffinityRules(field+".pod_affinity", affinity.PodAffinity); err != nil {
		return err
	}
	if err := validatePodAffinityRules(field+".pod_anti_affinity", affinity.PodAntiAffinity); err != nil {
		return err
	}
	return nil
}

func validatePodAffinityRules(field string, affinity *PodAffinity) error {
	if affinity == nil {
		return nil
	}
	for i, term := range affinity.RequiredDuringSchedulingIgnoredDuringExecution {
		if err := validatePodAffinityTerm(fmt.Sprintf("%s.required_during_scheduling_ignored_during_execution[%d]", field, i), term); err != nil {
			return err
		}
	}
	for i, term := range affinity.PreferredDuringSchedulingIgnoredDuringExecution {
		if term.Weight < 1 || term.Weight > 100 {
			return fmt.Errorf("%s.preferred_during_scheduling_ignored_during_execution[%d].weight: must be between 1 and 100", field, i)
		}
		if err := validatePodAffinityTerm(fmt.Sprintf("%s.preferred_during_scheduling_ignored_during_execution[%d].pod_affinity_term", field, i), term.PodAffinityTerm); err != nil {
			return err
		}
	}
	return nil
}

func validateNodeSelectorTerm(field string, term NodeSelectorTerm) error {
	for i, expression := range term.MatchExpressions {
		if err := validateNodeSelectorRequirement(fmt.Sprintf("%s.match_expressions[%d]", field, i), expression); err != nil {
			return err
		}
	}
	return nil
}

func validateNodeSelectorRequirement(field string, requirement NodeSelectorRequirement) error {
	if strings.TrimSpace(requirement.Key) == "" {
		return fmt.Errorf("%s.key: required", field)
	}
	operator := strings.TrimSpace(requirement.Operator)
	switch operator {
	case "In", "NotIn":
		if len(requirement.Values) == 0 {
			return fmt.Errorf("%s.values: must contain at least one value for operator %s", field, operator)
		}
	case "Exists", "DoesNotExist":
		if len(requirement.Values) != 0 {
			return fmt.Errorf("%s.values: must be empty for operator %s", field, operator)
		}
	case "Gt", "Lt":
		if len(requirement.Values) != 1 {
			return fmt.Errorf("%s.values: must contain exactly one value for operator %s", field, operator)
		}
		if _, err := strconv.Atoi(strings.TrimSpace(requirement.Values[0])); err != nil {
			return fmt.Errorf("%s.values[0]: must be an integer for operator %s", field, operator)
		}
	default:
		return fmt.Errorf("%s.operator: must be one of In, NotIn, Exists, DoesNotExist, Gt, or Lt", field)
	}
	return nil
}

func validatePodAffinityTerm(field string, term PodAffinityTerm) error {
	if strings.TrimSpace(term.TopologyKey) == "" {
		return fmt.Errorf("%s.topology_key: required", field)
	}
	if err := validateLabelSelector(field+".label_selector", term.LabelSelector); err != nil {
		return err
	}
	if err := validateLabelSelector(field+".namespace_selector", term.NamespaceSelector); err != nil {
		return err
	}
	for i, namespace := range term.Namespaces {
		if strings.TrimSpace(namespace) == "" {
			return fmt.Errorf("%s.namespaces[%d]: required", field, i)
		}
	}
	return nil
}

func validateLabelSelector(field string, selector *LabelSelector) error {
	if selector == nil {
		return nil
	}
	for i, requirement := range selector.MatchExpressions {
		if err := validateLabelSelectorRequirement(fmt.Sprintf("%s.match_expressions[%d]", field, i), requirement); err != nil {
			return err
		}
	}
	return nil
}

func validateLabelSelectorRequirement(field string, requirement LabelSelectorRequirement) error {
	if strings.TrimSpace(requirement.Key) == "" {
		return fmt.Errorf("%s.key: required", field)
	}
	operator := strings.TrimSpace(requirement.Operator)
	switch operator {
	case "In", "NotIn":
		if len(requirement.Values) == 0 {
			return fmt.Errorf("%s.values: must contain at least one value for operator %s", field, operator)
		}
	case "Exists", "DoesNotExist":
		if len(requirement.Values) != 0 {
			return fmt.Errorf("%s.values: must be empty for operator %s", field, operator)
		}
	default:
		return fmt.Errorf("%s.operator: must be one of In, NotIn, Exists, or DoesNotExist", field)
	}
	return nil
}

func validatePodFailurePolicy(field string, policy *PodFailurePolicy) error {
	if policy == nil || len(policy.Rules) == 0 {
		return nil
	}
	if len(policy.Rules) > 20 {
		return fmt.Errorf("%s.rules: must contain at most 20 rules", field)
	}
	for i, rule := range policy.Rules {
		ruleField := fmt.Sprintf("%s.rules[%d]", field, i)
		switch strings.TrimSpace(rule.Action) {
		case string(batchv1.PodFailurePolicyActionFailJob), string(batchv1.PodFailurePolicyActionIgnore), string(batchv1.PodFailurePolicyActionCount):
		case string(batchv1.PodFailurePolicyActionFailIndex):
			return fmt.Errorf("%s.action: FailIndex is not supported for non-indexed Jobs", ruleField)
		default:
			return fmt.Errorf("%s.action: must be one of FailJob, Ignore, or Count", ruleField)
		}

		hasExitCodes := rule.OnExitCodes != nil
		hasPodConditions := len(rule.OnPodConditions) > 0
		if hasExitCodes == hasPodConditions {
			return fmt.Errorf("%s: must define exactly one of on_exit_codes or on_pod_conditions", ruleField)
		}

		if hasExitCodes {
			if err := validatePodFailurePolicyOnExitCodes(ruleField+".on_exit_codes", rule.OnExitCodes); err != nil {
				return err
			}
		}
		if hasPodConditions {
			if len(rule.OnPodConditions) > 20 {
				return fmt.Errorf("%s.on_pod_conditions: must contain at most 20 patterns", ruleField)
			}
			for j, pattern := range rule.OnPodConditions {
				if err := validatePodFailurePolicyOnPodCondition(fmt.Sprintf("%s.on_pod_conditions[%d]", ruleField, j), pattern); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validatePodFailurePolicyOnExitCodes(field string, requirement *PodFailurePolicyOnExitCodesRequirement) error {
	if requirement == nil {
		return fmt.Errorf("%s: required", field)
	}
	if name := strings.TrimSpace(requirement.ContainerName); requirement.ContainerName != "" && name == "" {
		return fmt.Errorf("%s.container_name: required", field)
	}
	operator := strings.TrimSpace(requirement.Operator)
	switch operator {
	case string(batchv1.PodFailurePolicyOnExitCodesOpIn), string(batchv1.PodFailurePolicyOnExitCodesOpNotIn):
	default:
		return fmt.Errorf("%s.operator: must be In or NotIn", field)
	}
	if len(requirement.Values) == 0 {
		return fmt.Errorf("%s.values: must contain at least one value", field)
	}
	if len(requirement.Values) > 255 {
		return fmt.Errorf("%s.values: must contain at most 255 values", field)
	}
	seen := make(map[int32]struct{}, len(requirement.Values))
	for i, value := range requirement.Values {
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s.values[%d]: duplicate value %d", field, i, value)
		}
		if operator == string(batchv1.PodFailurePolicyOnExitCodesOpIn) && value == 0 {
			return fmt.Errorf("%s.values[%d]: value 0 is not allowed with operator In", field, i)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validatePodFailurePolicyOnPodCondition(field string, pattern PodFailurePolicyOnPodConditionsPattern) error {
	if strings.TrimSpace(pattern.Type) == "" {
		return fmt.Errorf("%s.type: required", field)
	}
	if status := strings.TrimSpace(pattern.Status); status != "" {
		switch corev1.ConditionStatus(status) {
		case corev1.ConditionTrue, corev1.ConditionFalse, corev1.ConditionUnknown:
		default:
			return fmt.Errorf("%s.status: must be True, False, or Unknown", field)
		}
	}
	return nil
}

func validateCapabilities(field string, capabilities *Capabilities) error {
	if capabilities == nil {
		return nil
	}
	for i, value := range capabilities.Add {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s.add[%d]: required", field, i)
		}
	}
	for i, value := range capabilities.Drop {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s.drop[%d]: required", field, i)
		}
	}
	return nil
}

func validateSeccompProfile(field string, profile *SeccompProfile) error {
	if isEmptySeccompProfile(profile) {
		return nil
	}

	profileType := strings.TrimSpace(profile.Type)
	switch profileType {
	case string(corev1.SeccompProfileTypeRuntimeDefault), string(corev1.SeccompProfileTypeUnconfined):
		if strings.TrimSpace(profile.LocalhostProfile) != "" {
			return fmt.Errorf("%s.localhost_profile: only allowed when type is Localhost", field)
		}
	case string(corev1.SeccompProfileTypeLocalhost):
		if strings.TrimSpace(profile.LocalhostProfile) == "" {
			return fmt.Errorf("%s.localhost_profile: required when type is Localhost", field)
		}
	default:
		return fmt.Errorf("%s.type: must be RuntimeDefault, Unconfined, or Localhost", field)
	}
	return nil
}

func validateNonNegativeInt64Ptr(field string, value *int64) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s: must be >= 0", field)
	}
	return nil
}

func isEmptySecurityContext(securityContext *SecurityContext) bool {
	return securityContext == nil ||
		securityContext.RunAsUser == nil &&
			securityContext.RunAsGroup == nil &&
			securityContext.RunAsNonRoot == nil &&
			securityContext.Privileged == nil &&
			securityContext.ReadOnlyRootFilesystem == nil &&
			securityContext.AllowPrivilegeEscalation == nil &&
			isEmptyCapabilities(securityContext.Capabilities) &&
			isEmptySeccompProfile(securityContext.SeccompProfile)
}

func isEmptyCapabilities(capabilities *Capabilities) bool {
	return capabilities == nil || len(capabilities.Add) == 0 && len(capabilities.Drop) == 0
}

func isEmptySeccompProfile(profile *SeccompProfile) bool {
	return profile == nil || strings.TrimSpace(profile.Type) == "" && strings.TrimSpace(profile.LocalhostProfile) == ""
}

func isEmptyPodSecurityContext(podSecurityContext *PodSecurityContext) bool {
	return podSecurityContext == nil ||
		podSecurityContext.RunAsUser == nil &&
			podSecurityContext.RunAsGroup == nil &&
			podSecurityContext.RunAsNonRoot == nil &&
			podSecurityContext.FSGroup == nil &&
			strings.TrimSpace(podSecurityContext.FSGroupChangePolicy) == "" &&
			len(podSecurityContext.SupplementalGroups) == 0 &&
			len(podSecurityContext.Sysctls) == 0 &&
			isEmptySeccompProfile(podSecurityContext.SeccompProfile)
}

func isEmptyAffinity(affinity *Affinity) bool {
	return affinity == nil ||
		affinity.NodeAffinity == nil &&
			affinity.PodAffinity == nil &&
			affinity.PodAntiAffinity == nil
}

func toK8sCapabilities(capabilities *Capabilities) *corev1.Capabilities {
	if isEmptyCapabilities(capabilities) {
		return nil
	}
	k8sCapabilities := &corev1.Capabilities{}
	if len(capabilities.Add) > 0 {
		k8sCapabilities.Add = make([]corev1.Capability, 0, len(capabilities.Add))
		for _, capability := range capabilities.Add {
			k8sCapabilities.Add = append(k8sCapabilities.Add, corev1.Capability(strings.TrimSpace(capability)))
		}
	}
	if len(capabilities.Drop) > 0 {
		k8sCapabilities.Drop = make([]corev1.Capability, 0, len(capabilities.Drop))
		for _, capability := range capabilities.Drop {
			k8sCapabilities.Drop = append(k8sCapabilities.Drop, corev1.Capability(strings.TrimSpace(capability)))
		}
	}
	return k8sCapabilities
}

func toK8sSeccompProfile(field string, profile *SeccompProfile) (*corev1.SeccompProfile, error) {
	if isEmptySeccompProfile(profile) {
		return nil, nil
	}
	if err := validateSeccompProfile(field, profile); err != nil {
		return nil, err
	}
	k8sProfile := &corev1.SeccompProfile{
		Type: corev1.SeccompProfileType(strings.TrimSpace(profile.Type)),
	}
	if strings.TrimSpace(profile.LocalhostProfile) != "" {
		localhostProfile := strings.TrimSpace(profile.LocalhostProfile)
		k8sProfile.LocalhostProfile = &localhostProfile
	}
	return k8sProfile, nil
}

func toK8sNodeSelectorTerms(terms []NodeSelectorTerm) []corev1.NodeSelectorTerm {
	if len(terms) == 0 {
		return nil
	}
	k8sTerms := make([]corev1.NodeSelectorTerm, 0, len(terms))
	for _, term := range terms {
		k8sTerms = append(k8sTerms, toK8sNodeSelectorTerm(term))
	}
	return k8sTerms
}

func toK8sNodeSelectorTerm(term NodeSelectorTerm) corev1.NodeSelectorTerm {
	k8sTerm := corev1.NodeSelectorTerm{}
	if len(term.MatchExpressions) > 0 {
		k8sTerm.MatchExpressions = make([]corev1.NodeSelectorRequirement, 0, len(term.MatchExpressions))
		for _, expression := range term.MatchExpressions {
			k8sTerm.MatchExpressions = append(k8sTerm.MatchExpressions, corev1.NodeSelectorRequirement{
				Key:      strings.TrimSpace(expression.Key),
				Operator: corev1.NodeSelectorOperator(strings.TrimSpace(expression.Operator)),
				Values:   trimStringSlice(expression.Values),
			})
		}
	}
	return k8sTerm
}

func toK8sPodAffinityTerm(term PodAffinityTerm) corev1.PodAffinityTerm {
	return corev1.PodAffinityTerm{
		LabelSelector:     toK8sLabelSelector(term.LabelSelector),
		Namespaces:        trimStringSlice(term.Namespaces),
		TopologyKey:       strings.TrimSpace(term.TopologyKey),
		NamespaceSelector: toK8sLabelSelector(term.NamespaceSelector),
	}
}

func toK8sLabelSelector(selector *LabelSelector) *metav1.LabelSelector {
	if selector == nil {
		return nil
	}
	k8sSelector := &metav1.LabelSelector{}
	if len(selector.MatchLabels) > 0 {
		k8sSelector.MatchLabels = make(map[string]string, len(selector.MatchLabels))
		maps.Copy(k8sSelector.MatchLabels, selector.MatchLabels)
	}
	if len(selector.MatchExpressions) > 0 {
		k8sSelector.MatchExpressions = make([]metav1.LabelSelectorRequirement, 0, len(selector.MatchExpressions))
		for _, expression := range selector.MatchExpressions {
			k8sSelector.MatchExpressions = append(k8sSelector.MatchExpressions, metav1.LabelSelectorRequirement{
				Key:      strings.TrimSpace(expression.Key),
				Operator: metav1.LabelSelectorOperator(strings.TrimSpace(expression.Operator)),
				Values:   trimStringSlice(expression.Values),
			})
		}
	}
	return k8sSelector
}

func trimStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		trimmed = append(trimmed, strings.TrimSpace(value))
	}
	return trimmed
}

// toK8sVolumes converts config volumes to Kubernetes API types.
func (cfg *Config) toK8sVolumes() ([]corev1.Volume, error) {
	if len(cfg.Volumes) == 0 {
		return nil, nil
	}
	vols := make([]corev1.Volume, 0, len(cfg.Volumes))
	for _, v := range cfg.Volumes {
		if countVolumeSources(v) != 1 {
			return nil, fmt.Errorf("volume %q: %w", v.Name, ErrInvalidVolumeSource)
		}

		vol := corev1.Volume{Name: v.Name}
		switch {
		case v.EmptyDir != nil:
			emptyDir := &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMedium(v.EmptyDir.Medium),
			}
			if v.EmptyDir.SizeLimit != "" {
				qty, err := parseQuantity(fmt.Sprintf("volumes[%s].empty_dir.size_limit", v.Name), v.EmptyDir.SizeLimit)
				if err != nil {
					return nil, err
				}
				emptyDir.SizeLimit = &qty
			}
			vol.VolumeSource = corev1.VolumeSource{EmptyDir: emptyDir}
		case v.HostPath != nil:
			hostPathType := corev1.HostPathUnset
			if v.HostPath.Type != "" {
				hostPathType = corev1.HostPathType(v.HostPath.Type)
			}
			vol.VolumeSource = corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: v.HostPath.Path,
					Type: &hostPathType,
				},
			}
		case v.ConfigMap != nil:
			vol.VolumeSource = corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: v.ConfigMap.Name},
				},
			}
		case v.Secret != nil:
			vol.VolumeSource = corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: v.Secret.SecretName,
				},
			}
		case v.PersistentVolumeClaim != nil:
			vol.VolumeSource = corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: v.PersistentVolumeClaim.ClaimName,
					ReadOnly:  v.PersistentVolumeClaim.ReadOnly,
				},
			}
		}
		vols = append(vols, vol)
	}
	return vols, nil
}

// toK8sVolumeMounts converts config volume mounts to Kubernetes API types.
func (cfg *Config) toK8sVolumeMounts() []corev1.VolumeMount {
	if len(cfg.VolumeMounts) == 0 {
		return nil
	}
	mounts := make([]corev1.VolumeMount, 0, len(cfg.VolumeMounts))
	for _, m := range cfg.VolumeMounts {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      m.Name,
			MountPath: m.MountPath,
			SubPath:   m.SubPath,
			ReadOnly:  m.ReadOnly,
		})
	}
	return mounts
}

// toK8sImagePullPolicy converts config image pull policy to Kubernetes API type.
func (cfg *Config) toK8sImagePullPolicy() (corev1.PullPolicy, error) {
	switch strings.ToLower(cfg.ImagePullPolicy) {
	case "":
		return "", nil
	case "always":
		return corev1.PullAlways, nil
	case "never":
		return corev1.PullNever, nil
	case "ifnotpresent":
		return corev1.PullIfNotPresent, nil
	case "fallback":
		// Kubernetes does not have a native fallback policy equivalent to Docker's
		// "try pull, fall back to local on failure" behavior. IfNotPresent has
		// opposite semantics (prefer local, never try registry if cached).
		return "", ErrUnsupportedFallbackPolicy
	default:
		return "", ErrInvalidImagePullPolicy
	}
}

// toK8sImagePullSecrets converts config image pull secret names to Kubernetes API types.
func (cfg *Config) toK8sImagePullSecrets() []corev1.LocalObjectReference {
	if len(cfg.ImagePullSecrets) == 0 {
		return nil
	}
	refs := make([]corev1.LocalObjectReference, 0, len(cfg.ImagePullSecrets))
	for _, name := range cfg.ImagePullSecrets {
		refs = append(refs, corev1.LocalObjectReference{Name: name})
	}
	return refs
}

func countVolumeSources(v Volume) int {
	count := 0
	if v.EmptyDir != nil {
		count++
	}
	if v.HostPath != nil {
		count++
	}
	if v.ConfigMap != nil {
		count++
	}
	if v.Secret != nil {
		count++
	}
	if v.PersistentVolumeClaim != nil {
		count++
	}
	return count
}

func parseQuantity(field, value string) (resource.Quantity, error) {
	qty, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%s: invalid quantity %q: %w", field, value, err)
	}
	if qty.Sign() < 0 {
		return resource.Quantity{}, fmt.Errorf("%s: %w (%q)", field, ErrNegativeQuantity, value)
	}
	return qty, nil
}

func init() {
	ir.RegisterExecutorConfigSchema("kubernetes", configSchema)
	ir.RegisterExecutorConfigSchema("k8s", configSchema)
	ir.RegisterExecutorConfigSchema("kubernetes_defaults", configDefaultsSchema)
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	Required:             []string{"image"},
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties:           kubernetesConfigProperties,
}

var configDefaultsSchema = &jsonschema.Schema{
	Type:                 "object",
	Properties:           kubernetesConfigProperties,
	AdditionalProperties: configSchema.AdditionalProperties,
}

var kubernetesConfigProperties = map[string]*jsonschema.Schema{
	"image":                {Type: "string", Description: "Container image (required after DAG-level defaults are merged)"},
	"namespace":            {Type: "string", Description: "Kubernetes namespace (default: default)"},
	"kubeconfig":           {Type: "string", Description: "Path to kubeconfig file"},
	"context":              {Type: "string", Description: "Kubeconfig context name"},
	"service_account":      {Type: "string", Description: "Service account for the pod"},
	"working_dir":          {Type: "string", Description: "Working directory inside the container"},
	"image_pull_policy":    {Type: "string", Description: "Image pull policy (Always, IfNotPresent, Never)"},
	"image_pull_secrets":   {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Secret names for private registries"},
	"env":                  {Type: "array", Items: kubernetesEnvVarSchema, Description: "Environment variables"},
	"env_from":             {Type: "array", Items: kubernetesEnvFromSourceSchema, Description: "Environment variable sources"},
	"resources":            kubernetesResourceRequirementsSchema,
	"node_selector":        stringMapSchema("Node selector constraints"),
	"tolerations":          {Type: "array", Items: kubernetesTolerationSchema, Description: "Pod tolerations"},
	"security_context":     kubernetesSecurityContextSchema,
	"pod_security_context": kubernetesPodSecurityContextSchema,
	"affinity":             kubernetesAffinitySchema,
	"termination_grace_period_seconds": {
		Type:        "integer",
		Minimum:     new(float64(0)),
		Description: "Grace period in seconds before the Pod is forcefully terminated",
	},
	"priority_class_name": {Type: "string", Description: "Kubernetes priority class name"},
	"labels":              stringMapSchema("Labels for Job and Pod"),
	"annotations":         stringMapSchema("Annotations for Job and Pod"),
	"volumes":             {Type: "array", Items: kubernetesVolumeSchema, Description: "Pod volumes"},
	"volume_mounts":       {Type: "array", Items: kubernetesVolumeMountSchema, Description: "Container volume mounts"},
	"active_deadline":     {Type: "integer", Minimum: new(float64(0)), Description: "Job timeout in seconds"},
	"backoff_limit":       {Type: "integer", Minimum: new(float64(0)), Description: "Number of retries (default: 0)"},
	"ttl_after_finished":  {Type: "integer", Minimum: new(float64(0)), Description: "TTL for automatic cleanup in seconds"},
	"cleanup_policy":      {Type: "string", Description: "delete (default) or keep"},
	"pod_failure_policy":  kubernetesPodFailurePolicySchema,
}

var kubernetesEnvVarSchema = func() *jsonschema.Schema {
	schema := closedObjectSchema(
		map[string]*jsonschema.Schema{
			"name":       {Type: "string", Description: "Environment variable name"},
			"value":      {Type: "string", Description: "Literal environment variable value"},
			"value_from": kubernetesEnvVarSourceSchema,
		},
		"name",
	)
	schema.Not = &jsonschema.Schema{
		Required: []string{"value", "value_from"},
	}
	return schema
}()

var kubernetesEnvVarSourceSchema = &jsonschema.Schema{
	OneOf: []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"secret_key_ref": newKubernetesKeySelectorSchema(),
		}, "secret_key_ref"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"config_map_key_ref": newKubernetesKeySelectorSchema(),
		}, "config_map_key_ref"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"field_ref": newKubernetesFieldRefSchema(),
		}, "field_ref"),
	},
	Description: "Source configuration for an environment variable value",
}

var kubernetesEnvFromSourceSchema = &jsonschema.Schema{
	OneOf: []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"config_map_ref": newKubernetesEnvFromRefSchema(),
			"prefix":         {Type: "string", Description: "Optional variable prefix"},
		}, "config_map_ref"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"secret_ref": newKubernetesEnvFromRefSchema(),
			"prefix":     {Type: "string", Description: "Optional variable prefix"},
		}, "secret_ref"),
	},
	Description: "Environment variable source import",
}

var kubernetesResourceRequirementsSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"requests": stringMapSchema("CPU/memory requests"),
	"limits":   stringMapSchema("CPU/memory limits"),
})

var kubernetesTolerationSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"key":      {Type: "string", Description: "Taint key to tolerate"},
	"operator": {Type: "string", Description: "Toleration operator"},
	"value":    {Type: "string", Description: "Taint value to match"},
	"effect":   {Type: "string", Description: "Taint effect to tolerate"},
})

var kubernetesSecurityContextSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"run_as_user":                nonNegativeIntegerSchema("Container UID"),
	"run_as_group":               nonNegativeIntegerSchema("Container GID"),
	"run_as_non_root":            {Type: "boolean", Description: "Require the container to run as non-root"},
	"privileged":                 {Type: "boolean", Description: "Run the container in privileged mode"},
	"read_only_root_filesystem":  {Type: "boolean", Description: "Mount the container root filesystem read-only"},
	"allow_privilege_escalation": {Type: "boolean", Description: "Allow the process to gain more privileges"},
	"capabilities":               kubernetesCapabilitiesSchema,
	"seccomp_profile":            kubernetesSeccompProfileSchema.CloneSchemas(),
})

var kubernetesCapabilitiesSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"add":  {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Linux capabilities to add"},
	"drop": {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Linux capabilities to drop"},
})

var kubernetesSeccompProfileSchema = func() *jsonschema.Schema {
	schema := closedObjectSchema(map[string]*jsonschema.Schema{
		"type": {
			Type:        "string",
			Enum:        []any{"RuntimeDefault", "Unconfined", "Localhost"},
			Description: "Seccomp profile type",
		},
		"localhost_profile": {Type: "string", Description: "Localhost seccomp profile path"},
	})
	schema.AllOf = []*jsonschema.Schema{
		{
			If: &jsonschema.Schema{
				Properties: map[string]*jsonschema.Schema{
					"type": constSchema("Localhost"),
				},
				Required: []string{"type"},
			},
			Then: &jsonschema.Schema{Required: []string{"localhost_profile"}},
			Else: &jsonschema.Schema{
				Not: &jsonschema.Schema{Required: []string{"localhost_profile"}},
			},
		},
	}
	return schema
}()

var kubernetesPodSecurityContextSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"run_as_user":            nonNegativeIntegerSchema("Pod default UID"),
	"run_as_group":           nonNegativeIntegerSchema("Pod default GID"),
	"run_as_non_root":        {Type: "boolean", Description: "Require containers to run as non-root by default"},
	"fs_group":               nonNegativeIntegerSchema("Filesystem group for supported volumes"),
	"fs_group_change_policy": {Type: "string", Enum: []any{"Always", "OnRootMismatch"}, Description: "Volume ownership change policy"},
	"supplemental_groups":    nonNegativeIntegerArraySchema("Additional supplemental groups"),
	"sysctls":                {Type: "array", Items: kubernetesSysctlSchema, Description: "Namespaced Linux sysctls"},
	"seccomp_profile":        kubernetesSeccompProfileSchema.CloneSchemas(),
})

var kubernetesSysctlSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"name":  {Type: "string", Description: "Sysctl name"},
	"value": {Type: "string", Description: "Sysctl value"},
}, "name", "value")

var kubernetesAffinitySchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"node_affinity":     kubernetesNodeAffinitySchema,
	"pod_affinity":      kubernetesPodAffinitySchema.CloneSchemas(),
	"pod_anti_affinity": kubernetesPodAffinitySchema.CloneSchemas(),
})

var kubernetesNodeAffinitySchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"required_during_scheduling_ignored_during_execution": kubernetesNodeSelectorSchema,
	"preferred_during_scheduling_ignored_during_execution": {
		Type:        "array",
		Items:       kubernetesPreferredSchedulingTermSchema,
		Description: "Preferred node selector terms",
	},
})

var kubernetesNodeSelectorSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"node_selector_terms": {
		Type:        "array",
		Items:       kubernetesNodeSelectorTermSchema.CloneSchemas(),
		Description: "Node selector terms",
	},
})

var kubernetesNodeSelectorTermSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"match_expressions": {
		Type:        "array",
		Items:       kubernetesNodeSelectorRequirementSchema,
		Description: "Node label match expressions",
	},
})

var kubernetesNodeSelectorRequirementSchema = func() *jsonschema.Schema {
	schema := closedObjectSchema(map[string]*jsonschema.Schema{
		"key":      {Type: "string", Description: "Node label key"},
		"operator": {Type: "string", Enum: []any{"In", "NotIn", "Exists", "DoesNotExist", "Gt", "Lt"}, Description: "Node selector operator"},
		"values":   {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Operator values"},
	}, "key", "operator")
	schema.OneOf = []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("In"),
			"values":   stringArrayWithBoundsSchema(1, -1, "Values for In"),
		}, "key", "operator", "values"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("NotIn"),
			"values":   stringArrayWithBoundsSchema(1, -1, "Values for NotIn"),
		}, "key", "operator", "values"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("Exists"),
			"values":   stringArrayWithBoundsSchema(0, 0, "Values must be empty when operator is Exists"),
		}, "key", "operator"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("DoesNotExist"),
			"values":   stringArrayWithBoundsSchema(0, 0, "Values must be empty when operator is DoesNotExist"),
		}, "key", "operator"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("Gt"),
			"values":   stringArrayWithBoundsSchema(1, 1, "Single integer value for Gt"),
		}, "key", "operator", "values"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("Lt"),
			"values":   stringArrayWithBoundsSchema(1, 1, "Single integer value for Lt"),
		}, "key", "operator", "values"),
	}
	return schema
}()

var kubernetesPreferredSchedulingTermSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"weight":     boundedIntegerSchema(1, 100, "Preference weight"),
	"preference": kubernetesNodeSelectorTermSchema.CloneSchemas(),
}, "weight", "preference")

var kubernetesPodAffinitySchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"required_during_scheduling_ignored_during_execution": {
		Type:        "array",
		Items:       kubernetesPodAffinityTermSchema.CloneSchemas(),
		Description: "Required pod affinity terms",
	},
	"preferred_during_scheduling_ignored_during_execution": {
		Type:        "array",
		Items:       kubernetesWeightedPodAffinityTermSchema,
		Description: "Preferred pod affinity terms",
	},
})

var kubernetesPodAffinityTermSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"label_selector":     kubernetesLabelSelectorSchema.CloneSchemas(),
	"namespaces":         {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Namespaces to match"},
	"namespace_selector": kubernetesLabelSelectorSchema.CloneSchemas(),
	"topology_key":       {Type: "string", Description: "Topology key used for pod affinity"},
}, "topology_key")

var kubernetesWeightedPodAffinityTermSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"weight":            boundedIntegerSchema(1, 100, "Preference weight"),
	"pod_affinity_term": kubernetesPodAffinityTermSchema.CloneSchemas(),
}, "weight", "pod_affinity_term")

var kubernetesLabelSelectorSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"match_labels": stringMapSchema("Exact label matches"),
	"match_expressions": {
		Type:        "array",
		Items:       kubernetesLabelSelectorRequirementSchema,
		Description: "Label selector requirements",
	},
})

var kubernetesLabelSelectorRequirementSchema = func() *jsonschema.Schema {
	schema := closedObjectSchema(map[string]*jsonschema.Schema{
		"key":      {Type: "string", Description: "Label key"},
		"operator": {Type: "string", Enum: []any{"In", "NotIn", "Exists", "DoesNotExist"}, Description: "Label selector operator"},
		"values":   {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Operator values"},
	}, "key", "operator")
	schema.OneOf = []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("In"),
			"values":   stringArrayWithBoundsSchema(1, -1, "Values for In"),
		}, "key", "operator", "values"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("NotIn"),
			"values":   stringArrayWithBoundsSchema(1, -1, "Values for NotIn"),
		}, "key", "operator", "values"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("Exists"),
			"values":   stringArrayWithBoundsSchema(0, 0, "Values must be empty when operator is Exists"),
		}, "key", "operator"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"key":      {Type: "string"},
			"operator": constSchema("DoesNotExist"),
			"values":   stringArrayWithBoundsSchema(0, 0, "Values must be empty when operator is DoesNotExist"),
		}, "key", "operator"),
	}
	return schema
}()

var kubernetesPodFailurePolicySchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"rules": {
		Type:        "array",
		Items:       kubernetesPodFailurePolicyRuleSchema,
		MaxItems:    new(20),
		Description: "Pod failure policy rules",
	},
})

var kubernetesPodFailurePolicyRuleSchema = &jsonschema.Schema{
	OneOf: []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"action": {
				Type:        "string",
				Enum:        []any{"FailJob", "Ignore", "Count"},
				Description: "Action when the rule matches",
			},
			"on_exit_codes": kubernetesPodFailurePolicyOnExitCodesSchema,
		}, "action", "on_exit_codes"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"action": {
				Type:        "string",
				Enum:        []any{"FailJob", "Ignore", "Count"},
				Description: "Action when the rule matches",
			},
			"on_pod_conditions": {
				Type:        "array",
				Items:       kubernetesPodFailurePolicyOnPodConditionSchema,
				MinItems:    new(1),
				MaxItems:    new(20),
				Description: "Pod conditions to match",
			},
		}, "action", "on_pod_conditions"),
	},
	Description: "A single pod failure policy rule",
}

var kubernetesPodFailurePolicyOnExitCodesSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"container_name": {Type: "string", Description: "Optional container name filter"},
	"operator": {
		Type:        "string",
		Enum:        []any{"In", "NotIn"},
		Description: "Exit-code matching operator",
	},
	"values": {
		Type:        "array",
		Items:       &jsonschema.Schema{Type: "integer"},
		MinItems:    new(1),
		MaxItems:    new(255),
		UniqueItems: true,
		Description: "Exit codes to compare against",
	},
}, "operator", "values")

var kubernetesPodFailurePolicyOnPodConditionSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"type": {Type: "string", Description: "Pod condition type"},
	"status": {
		Type:        "string",
		Enum:        []any{"True", "False", "Unknown"},
		Description: "Pod condition status. Defaults to True when omitted",
	},
}, "type")

var kubernetesVolumeSchema = &jsonschema.Schema{
	OneOf: []*jsonschema.Schema{
		closedObjectSchema(map[string]*jsonschema.Schema{
			"name":      {Type: "string", Description: "Volume name"},
			"empty_dir": kubernetesEmptyDirSchema,
		}, "name", "empty_dir"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"name":      {Type: "string", Description: "Volume name"},
			"host_path": kubernetesHostPathSchema,
		}, "name", "host_path"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"name":       {Type: "string", Description: "Volume name"},
			"config_map": kubernetesConfigMapVolumeSchema,
		}, "name", "config_map"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"name":   {Type: "string", Description: "Volume name"},
			"secret": kubernetesSecretVolumeSchema,
		}, "name", "secret"),
		closedObjectSchema(map[string]*jsonschema.Schema{
			"name":                    {Type: "string", Description: "Volume name"},
			"persistent_volume_claim": kubernetesPVCVolumeSchema,
		}, "name", "persistent_volume_claim"),
	},
	Description: "Volume definition. Exactly one source must be specified",
}

var kubernetesEmptyDirSchema = closedObjectSchema(map[string]*jsonschema.Schema{
	"medium":     {Type: "string", Description: "Storage medium, such as Memory"},
	"size_limit": {Type: "string", Description: "Optional size limit using Kubernetes quantity syntax"},
})

var kubernetesHostPathSchema = closedObjectSchema(
	map[string]*jsonschema.Schema{
		"path": {Type: "string", Description: "Host filesystem path"},
		"type": {Type: "string", Description: "Optional hostPath type"},
	},
	"path",
)

var kubernetesConfigMapVolumeSchema = closedObjectSchema(
	map[string]*jsonschema.Schema{
		"name": {Type: "string", Description: "ConfigMap name"},
	},
	"name",
)

var kubernetesSecretVolumeSchema = closedObjectSchema(
	map[string]*jsonschema.Schema{
		"secret_name": {Type: "string", Description: "Secret name"},
	},
	"secret_name",
)

var kubernetesPVCVolumeSchema = closedObjectSchema(
	map[string]*jsonschema.Schema{
		"claim_name": {Type: "string", Description: "PersistentVolumeClaim name"},
		"read_only":  {Type: "boolean", Description: "Mount claim read-only"},
	},
	"claim_name",
)

var kubernetesVolumeMountSchema = closedObjectSchema(
	map[string]*jsonschema.Schema{
		"name":       {Type: "string", Description: "Referenced volume name"},
		"mount_path": {Type: "string", Description: "Mount path inside the container"},
		"sub_path":   {Type: "string", Description: "Optional sub-path within the volume"},
		"read_only":  {Type: "boolean", Description: "Mount the volume read-only"},
	},
	"name", "mount_path",
)

func closedObjectSchema(properties map[string]*jsonschema.Schema, required ...string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func newKubernetesKeySelectorSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"name": {Type: "string", Description: "Secret or ConfigMap name"},
			"key":  {Type: "string", Description: "Key within the referenced object"},
		},
		"name", "key",
	)
}

func newKubernetesFieldRefSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"field_path": {Type: "string", Description: "Pod field path"},
		},
		"field_path",
	)
}

func newKubernetesEnvFromRefSchema() *jsonschema.Schema {
	return closedObjectSchema(
		map[string]*jsonschema.Schema{
			"name": {Type: "string", Description: "Secret or ConfigMap name"},
		},
		"name",
	)
}

func stringMapSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: &jsonschema.Schema{Type: "string"},
		Description:          description,
	}
}

func nonNegativeIntegerSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "integer",
		Minimum:     new(float64(0)),
		Description: description,
	}
}

func nonNegativeIntegerArraySchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Items:       nonNegativeIntegerSchema(description),
		Description: description,
	}
}

func boundedIntegerSchema(min, max float64, description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "integer",
		Minimum:     new(min),
		Maximum:     new(max),
		Description: description,
	}
}

func stringArrayWithBoundsSchema(minItems, maxItems int, description string) *jsonschema.Schema {
	schema := &jsonschema.Schema{
		Type:        "array",
		Items:       &jsonschema.Schema{Type: "string"},
		Description: description,
	}
	if minItems > 0 || maxItems >= 0 {
		schema.MinItems = new(minItems)
	}
	if maxItems >= 0 {
		schema.MaxItems = new(maxItems)
	}
	return schema
}

func constSchema(value any) *jsonschema.Schema {
	return &jsonschema.Schema{
		Const: &value,
	}
}

//go:fix inline
func intPtr(v int) *int {
	return new(v)
}

//go:fix inline
func floatPtr(v float64) *float64 {
	return new(v)
}
