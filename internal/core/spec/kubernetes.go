// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
)

const kubernetesDefaultsSchemaType = "kubernetes_defaults"

func buildKubernetes(_ buildContext, d *dag) (core.KubernetesConfig, error) {
	if d.Kubernetes == nil {
		return nil, nil
	}

	cfg := cloneKubernetesConfigMap(d.Kubernetes)
	if err := core.ValidateExecutorConfig(kubernetesDefaultsSchemaType, cfg); err != nil {
		return nil, core.NewValidationError("kubernetes", d.Kubernetes, err)
	}

	return core.KubernetesConfig(cfg), nil
}

func isKubernetesExecutorType(executorType string) bool {
	switch strings.TrimSpace(executorType) {
	case "kubernetes", "k8s":
		return true
	default:
		return false
	}
}

func mergeKubernetesExecutorConfig(defaults core.KubernetesConfig, stepConfig map[string]any) map[string]any {
	if defaults == nil {
		return cloneKubernetesConfigMap(stepConfig)
	}
	return mergeKubernetesConfigMaps(map[string]any(defaults), stepConfig)
}

func mergeKubernetesConfigMaps(dst, src map[string]any) map[string]any {
	merged := cloneKubernetesConfigMap(dst)
	for key, srcVal := range src {
		srcMap, srcIsMap := asKubernetesConfigMap(srcVal)
		if srcIsMap {
			if len(srcMap) == 0 {
				merged[key] = map[string]any{}
				continue
			}
			if dstMap, ok := asKubernetesConfigMap(merged[key]); ok {
				merged[key] = mergeKubernetesConfigMaps(dstMap, srcMap)
			} else {
				merged[key] = cloneKubernetesConfigMap(srcMap)
			}
			continue
		}

		merged[key] = cloneKubernetesValue(srcVal)
	}
	return merged
}

func cloneKubernetesConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	cloned := make(map[string]any, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneKubernetesValue(value)
	}
	return cloned
}

func cloneKubernetesValue(value any) any {
	switch v := value.(type) {
	case core.KubernetesConfig:
		return core.KubernetesConfig(cloneKubernetesConfigMap(map[string]any(v)))
	case map[string]any:
		return cloneKubernetesConfigMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneKubernetesValue(v[i])
		}
		return cloned
	case []string:
		return slices.Clone(v)
	case []map[string]any:
		cloned := make([]map[string]any, len(v))
		for i := range v {
			cloned[i] = cloneKubernetesConfigMap(v[i])
		}
		return cloned
	default:
		return value
	}
}

func asKubernetesConfigMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case core.KubernetesConfig:
		return map[string]any(v), true
	case map[string]any:
		return v, true
	default:
		return nil, false
	}
}
