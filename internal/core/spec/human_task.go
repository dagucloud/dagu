// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
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
		return core.NewValidationError("id", normalized["id"], fmt.Errorf("action human.task requires an explicit id"))
	}
	if _, exists := normalized["outputs"]; exists {
		return core.NewValidationError("outputs", normalized["outputs"], fmt.Errorf("action human.task derives outputs from its form"))
	}
	for _, field := range humanTaskIncompatibleStepFields {
		if value, exists := normalized[field]; exists {
			return core.NewValidationError(field, value, fmt.Errorf("action human.task does not support %s", field))
		}
	}
	if with == nil {
		return core.NewValidationError("with", nil, fmt.Errorf("with.prompt is required"))
	}
	for name := range with {
		if name != "prompt" && name != "form" {
			return core.NewValidationError("with", with, fmt.Errorf("human.task does not support with.%s", name))
		}
	}
	prompt, ok := with["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return core.NewValidationError("with.prompt", with["prompt"], fmt.Errorf("with.prompt must be a non-empty string"))
	}
	if form, exists := with["form"]; exists {
		normalizedForm, _, err := buildHumanTaskForm(form)
		if err != nil {
			return core.NewValidationError("with.form", form, err)
		}
		var formMap map[string]any
		if err := json.Unmarshal(normalizedForm, &formMap); err != nil {
			return fmt.Errorf("decode normalized human task form: %w", err)
		}
		with["form"] = formMap
	}
	normalized["with"] = with
	return nil
}

func buildStepHumanTask(_ StepBuildContext, s *step, result *core.Step) error {
	if strings.TrimSpace(s.Action) != "human.task" {
		return nil
	}

	form, outputs, err := buildHumanTaskForm(s.With["form"])
	if err != nil {
		return err
	}
	prompt, _ := s.With["prompt"].(string)
	result.HumanTask = &core.HumanTaskConfig{
		Prompt: prompt,
		Form:   form,
	}
	result.Outputs = outputs
	return nil
}
