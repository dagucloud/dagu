// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var humanTaskIncompatibleStepFields = []string{
	"approval",
	"container",
	"foreach",
	"log_output",
	"mail_on_error",
	"output",
	"output_schema",
	"parallel",
	"repeat_policy",
	"retry_policy",
	"signal_on_stop",
	"stderr",
	"stdout",
	"timeout_sec",
	"worker_selector",
}

func normalizeHumanTaskAction(normalized map[string]any, with map[string]any) error {
	id, ok := normalized["id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return ir.NewValidationError("id", normalized["id"], fmt.Errorf("action human.task requires an explicit id"))
	}
	if _, exists := normalized["outputs"]; exists {
		return ir.NewValidationError("outputs", normalized["outputs"], fmt.Errorf("action human.task derives outputs from its form"))
	}
	for _, field := range humanTaskIncompatibleStepFields {
		if value, exists := normalized[field]; exists {
			return ir.NewValidationError(field, value, fmt.Errorf("action human.task does not support %s", field))
		}
	}
	if with == nil {
		return ir.NewValidationError("with", nil, fmt.Errorf("with.prompt is required"))
	}
	for name := range with {
		if name != "prompt" && name != "form" && name != "artifacts" {
			return ir.NewValidationError("with", with, fmt.Errorf("human.task does not support with.%s", name))
		}
	}
	if form, exists := with["form"]; exists && form == nil {
		return ir.NewValidationError("with.form", nil, fmt.Errorf("with.form must be an object schema"))
	}
	prompt, ok := with["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return ir.NewValidationError("with.prompt", with["prompt"], fmt.Errorf("with.prompt must be a non-empty string"))
	}
	normalized["with"] = with
	return nil
}

func buildStepHumanTask(_ stepBuildContext, s *step, result *ir.Step) error {
	if strings.TrimSpace(s.Action) != "human.task" {
		return nil
	}

	form, outputs, err := buildHumanTaskForm(s.With["form"])
	if err != nil {
		return ir.NewValidationError("with.form", s.With["form"], err)
	}
	var artifacts []string
	if value, exists := s.With["artifacts"]; exists {
		artifacts, err = buildHumanTaskArtifacts(value)
		if err != nil {
			return ir.NewValidationError("with.artifacts", value, err)
		}
	}
	prompt, _ := s.With["prompt"].(string)
	result.HumanTask = &ir.HumanTaskConfig{
		Prompt:    prompt,
		Form:      form,
		Artifacts: artifacts,
	}
	result.Outputs = outputs
	return nil
}

func buildHumanTaskArtifacts(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("with.artifacts must be an array of artifact paths")
	}

	artifacts := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		artifact, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("with.artifacts entries must be strings")
		}
		artifact, err := cleanStepArtifactPath(artifact)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[artifact]; exists {
			return nil, fmt.Errorf("with.artifacts contains duplicate path %q", artifact)
		}
		seen[artifact] = struct{}{}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}
