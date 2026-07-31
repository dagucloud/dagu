// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec031_human_task_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestHumanTaskShapeValidation(t *testing.T) {
	t.Parallel()
	valid := []string{
		"valid_acknowledgement_shape.yaml",
		"valid_form_shape.yaml",
		"child_human.yaml",
		"multi_document_human.yaml",
	}
	for _, file := range valid {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStderr("")
		})
	}

	invalid := []struct {
		file  string
		parts []string
	}{
		{file: "invalid_missing_id.yaml", parts: []string{"id", "explicit"}},
		{file: "invalid_prompt.yaml", parts: []string{"with.prompt", "non-empty string"}},
		{file: "invalid_with_field.yaml", parts: []string{"with.evidence"}},
		{file: "invalid_form_null.yaml", parts: []string{"with.form", "object"}},
		{file: "invalid_form_root.yaml", parts: []string{"form", "type"}},
		{file: "invalid_form_property.yaml", parts: []string{"1invalid", "property"}},
		{file: "invalid_form_required.yaml", parts: []string{"required", "region"}},
		{file: "invalid_form_additional_properties.yaml", parts: []string{"additionalProperties", "boolean"}},
		{file: "invalid_execution_field.yaml", parts: []string{"run", "human.task"}},
		{file: "invalid_lifecycle_field.yaml", parts: []string{"timeout_sec", "human.task"}},
		{file: "invalid_output_field.yaml", parts: []string{"outputs", "human.task"}},
		{file: "invalid_foreach_human_task.yaml", parts: []string{"foreach", "human.task"}},
		{file: "invalid_handler_human_task.yaml", parts: []string{"handler", "human.task"}},
	}
	for _, tc := range invalid {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.parts...)
		})
	}
}

func TestHumanTaskChildDAGBoundary(t *testing.T) {
	t.Parallel()

	root := harness.NewRunner(t)
	rootResult := root.Run("start", "--run-id=spec031-child-root", "child_human.yaml")
	rootResult.ExpectExitCode(0)

	cases := []string{"parent_dag_run.yaml", "parent_dag_enqueue.yaml", "parent_parallel.yaml", "multi_document_human.yaml"}
	for _, file := range cases {
		t.Run(file, func(t *testing.T) {
			dagu := harness.NewRunner(t)
			result := dagu.Run("start", "--run-id=spec031-child-boundary", file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("human task", "sub-DAG")
		})
	}
}
