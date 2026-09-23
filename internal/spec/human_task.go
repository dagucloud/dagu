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
		if name != "prompt" && name != "form" && name != "artifacts" && name != "push_back" {
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
	var pushBack *ir.HumanTaskPushBackConfig
	if value, exists := s.With["push_back"]; exists {
		pushBack, err = buildHumanTaskPushBack(value)
		if err != nil {
			return err
		}
	}
	prompt, _ := s.With["prompt"].(string)
	result.HumanTask = &ir.HumanTaskConfig{
		Prompt:    prompt,
		Form:      form,
		Artifacts: artifacts,
		PushBack:  pushBack,
	}
	result.Outputs = outputs
	return nil
}

// buildHumanTaskPushBack builds with.push_back. Feedback properties create no
// step outputs, and undeclared feedback is rejected because push-back inputs
// become environment variables of every rewound step.
func buildHumanTaskPushBack(value any) (*ir.HumanTaskPushBackConfig, error) {
	config, ok := value.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError("with.push_back", value, fmt.Errorf("with.push_back must be an object"))
	}
	for name := range config {
		if name != "rewind_to" && name != "form" {
			return nil, ir.NewValidationError("with.push_back", config, fmt.Errorf("with.push_back does not support %s", name))
		}
	}
	rewindTo, ok := config["rewind_to"].(string)
	if !ok || strings.TrimSpace(rewindTo) == "" {
		return nil, ir.NewValidationError("with.push_back.rewind_to", config["rewind_to"], fmt.Errorf("with.push_back.rewind_to must be a non-empty step id or name"))
	}

	rawForm, hasForm := config["form"]
	if hasForm && rawForm == nil {
		return nil, ir.NewValidationError("with.push_back.form", nil, fmt.Errorf("with.push_back.form must be an object schema"))
	}
	form, _, err := buildHumanTaskForm(rawForm)
	if err != nil {
		return nil, ir.NewValidationError("with.push_back.form", rawForm, err)
	}
	if formMap, ok := rawForm.(map[string]any); ok && formMap["additionalProperties"] == true {
		return nil, ir.NewValidationError("with.push_back.form", rawForm, fmt.Errorf("with.push_back.form additionalProperties must be false"))
	}
	return &ir.HumanTaskPushBackConfig{
		RewindTo: strings.TrimSpace(rewindTo),
		Form:     form,
	}, nil
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
