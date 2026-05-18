// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testExecConfig provides a non-empty executor type to avoid triggering
// command validators that may be registered via init() from other packages.
var testExecConfig = ExecutorConfig{Type: "test-no-validator"}

func TestIsValidStepID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		id       string
		expected bool
	}{
		// Valid cases - starts with letter, followed by alphanumeric/dash/underscore
		{name: "single letter", id: "a", expected: true},
		{name: "simple word", id: "step", expected: true},
		{name: "word with number", id: "step1", expected: true},
		{name: "word with underscore", id: "my_step", expected: true},
		{name: "mixed case", id: "MyStep", expected: true},
		{name: "uppercase", id: "STEP", expected: true},
		{name: "complex valid id", id: "Step123_test_id", expected: true},
		{name: "letters and numbers", id: "step123abc", expected: true},
		{name: "uppercase with numbers", id: "STEP123", expected: true},

		// Invalid cases
		{name: "contains hyphen", id: "my-step", expected: false},
		{name: "starts with number", id: "1step", expected: false},
		{name: "starts with dash", id: "-step", expected: false},
		{name: "starts with underscore", id: "_step", expected: false},
		{name: "contains space", id: "step name", expected: false},
		{name: "contains exclamation", id: "step!", expected: false},
		{name: "contains at sign", id: "step@test", expected: false},
		{name: "contains dot", id: "step.name", expected: false},
		{name: "empty string", id: "", expected: false},
		{name: "only numbers", id: "123", expected: false},
		{name: "contains slash", id: "step/name", expected: false},
		{name: "contains colon", id: "step:name", expected: false},
		{name: "contains equals", id: "step=value", expected: false},
		{name: "unicode characters", id: "step日本語", expected: false},
		{name: "emoji", id: "step🚀", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isValidStepID(tt.id)
			assert.Equal(t, tt.expected, result,
				"isValidStepID(%q) = %v, want %v", tt.id, result, tt.expected)
		})
	}
}

func TestIsReservedWord(t *testing.T) {
	t.Parallel()

	// All reserved words (case insensitive)
	reservedWords := []string{"env", "params", "args", "stdout", "stderr", "output", "outputs"}

	t.Run("reserved words are detected", func(t *testing.T) {
		t.Parallel()
		for _, word := range reservedWords {
			assert.True(t, isReservedWord(word),
				"isReservedWord(%q) should return true", word)
		}
	})

	t.Run("reserved words uppercase are detected", func(t *testing.T) {
		t.Parallel()
		for _, word := range reservedWords {
			upper := strings.ToUpper(word)
			assert.True(t, isReservedWord(upper),
				"isReservedWord(%q) should return true", upper)
		}
	})

	t.Run("reserved words mixed case are detected", func(t *testing.T) {
		t.Parallel()
		mixedCases := []string{"Env", "PARAMS", "Args", "StdOut", "StdErr", "Output", "Outputs"}
		for _, word := range mixedCases {
			assert.True(t, isReservedWord(word),
				"isReservedWord(%q) should return true", word)
		}
	})

	t.Run("non-reserved words are not detected", func(t *testing.T) {
		t.Parallel()
		nonReserved := []string{
			"environment",
			"parameter",
			"arguments",
			"step",
			"run",
			"execute",
			"command",
			"envs",
			"param",
			"arg",
			"out",
			"err",
			"myenv",
			"test-stdout",
		}
		for _, word := range nonReserved {
			assert.False(t, isReservedWord(word),
				"isReservedWord(%q) should return false", word)
		}
	})

	t.Run("empty string is not reserved", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isReservedWord(""))
	})
}

func TestValidateSteps(t *testing.T) {
	t.Parallel()

	t.Run("valid DAG with steps passes validation", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ExecutorConfig: testExecConfig},
				{Name: "step2", Depends: []string{"step1"}, ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("approval rewind_to resolves upstream step IDs", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "prepare", ID: "prepare_id", ExecutorConfig: testExecConfig},
				{
					Name:           "review",
					Depends:        []string{"prepare"},
					ExecutorConfig: testExecConfig,
					Approval: &ApprovalConfig{
						RewindTo: "prepare_id",
					},
				},
			},
		}

		err := ValidateSteps(dag)
		require.NoError(t, err)
		require.NotNil(t, dag.Steps[1].Approval)
		assert.Equal(t, "prepare", dag.Steps[1].Approval.RewindTo)
	})

	t.Run("approval rewind_to rejects non-upstream steps", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "prepare", ExecutorConfig: testExecConfig},
				{
					Name:           "review",
					Depends:        []string{"prepare"},
					ExecutorConfig: testExecConfig,
					Approval: &ApprovalConfig{
						RewindTo: "sidecar",
					},
				},
				{Name: "sidecar", Depends: []string{"prepare"}, ExecutorConfig: testExecConfig},
			},
		}

		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "approval.rewind_to")
	})

	t.Run("empty DAG passes validation", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{Steps: []Step{}}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("step with valid ID passes", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "myStepId", ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("step with invalid ID format fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "1invalid"},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid step ID format")
	})

	t.Run("step with reserved word ID fails", func(t *testing.T) {
		t.Parallel()
		reservedWords := []string{"env", "params", "args", "stdout", "stderr", "output", "outputs"}
		for _, word := range reservedWords {
			dag := &DAG{
				Steps: []Step{
					{Name: "step1", ID: word},
				},
			}
			err := ValidateSteps(dag)
			require.Error(t, err, "ID %q should be rejected as reserved", word)
			assert.Contains(t, err.Error(), "reserved word")
		}
	})

	t.Run("duplicate step names fail", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "duplicate"},
				{Name: "duplicate"},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStepNameDuplicate)
	})

	t.Run("duplicate step IDs fail", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "sameId"},
				{Name: "step2", ID: "sameId"},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate step ID")
	})

	t.Run("step ID conflicts with another step name fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "conflictName", ExecutorConfig: testExecConfig},
				{Name: "step2", ID: "conflictName", ExecutorConfig: testExecConfig},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflicts")
	})

	t.Run("step name conflicts with another step ID fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "conflictId", ExecutorConfig: testExecConfig},
				{Name: "conflictId", ExecutorConfig: testExecConfig},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflicts")
	})

	t.Run("same step has matching name and ID is allowed", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "sameName", ID: "sameName", ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("valid dependencies pass", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ExecutorConfig: testExecConfig},
				{Name: "step2", Depends: []string{"step1"}, ExecutorConfig: testExecConfig},
				{Name: "step3", Depends: []string{"step1", "step2"}, ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("non-existent dependency fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1"},
				{Name: "step2", Depends: []string{"nonexistent"}},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-existent step")
	})

	t.Run("ID reference in depends is resolved to name", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "s1", ExecutorConfig: testExecConfig},
				{Name: "step2", Depends: []string{"s1"}, ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
		assert.Contains(t, dag.Steps[1].Depends, "step1")
	})

	t.Run("step with empty name fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: ""},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "step name")
	})

	t.Run("step name too long fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: strings.Repeat("a", 256)},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStepNameTooLong)
	})

	t.Run("step name at max length passes", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: strings.Repeat("a", 255), ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("parallel config without SubDAG fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: 2,
						Items:         []ParallelItem{{Value: "a"}, {Value: "b"}},
					},
				},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parallel currently requires action: dag.run or dag.enqueue")
	})

	t.Run("parallel config with max_concurrent 0 fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: 0,
						Items:         []ParallelItem{{Value: "a"}, {Value: "b"}},
					},
					SubDAG: &SubDAG{Name: "child"},
				},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_concurrent must be greater than 0")
	})

	t.Run("parallel config with negative max_concurrent fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: -1,
						Items:         []ParallelItem{{Value: "a"}, {Value: "b"}},
					},
					SubDAG: &SubDAG{Name: "child"},
				},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_concurrent must be greater than 0")
	})

	t.Run("parallel config without items or variable fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: 2,
					},
					SubDAG: &SubDAG{Name: "child"},
				},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must have either items array or variable reference")
	})

	t.Run("valid parallel config with items passes", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: 2,
						Items:         []ParallelItem{{Value: "a"}, {Value: "b"}, {Value: "c"}},
					},
					SubDAG: &SubDAG{Name: "child"},
				},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("valid parallel config with variable passes", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{
					Name: "step1",
					Parallel: &ParallelConfig{
						MaxConcurrent: 2,
						Variable:      "ITEMS",
					},
					SubDAG: &SubDAG{Name: "child"},
				},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})
}

func TestRegisterStepValidator(t *testing.T) {
	// Note: These tests modify global state, so they should not run in parallel
	// with each other. Each test should clean up after itself.

	t.Run("register validator for new type", func(t *testing.T) {
		// Clean up after test
		defer UnregisterStepValidator("test-executor")

		validatorCalled := false
		validator := func(_ Step) error {
			validatorCalled = true
			return nil
		}

		RegisterStepValidator("test-executor", validator)

		// Create a DAG with a step using this executor type
		dag := &DAG{
			Steps: []Step{
				{
					Name:           "step1",
					ExecutorConfig: ExecutorConfig{Type: "test-executor"},
				},
			},
		}

		err := ValidateSteps(dag)
		assert.NoError(t, err)
		assert.True(t, validatorCalled, "validator should have been called")
	})

	t.Run("validator returning error propagates", func(t *testing.T) {
		defer UnregisterStepValidator("error-executor")

		expectedErr := errors.New("validation failed")
		validator := func(_ Step) error {
			return expectedErr
		}

		RegisterStepValidator("error-executor", validator)

		dag := &DAG{
			Steps: []Step{
				{
					Name:           "step1",
					ExecutorConfig: ExecutorConfig{Type: "error-executor"},
				},
			},
		}

		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validation failed")
	})

	t.Run("overwrite existing validator", func(t *testing.T) {
		defer UnregisterStepValidator("overwrite-executor")

		firstCalled := false
		secondCalled := false

		first := func(_ Step) error {
			firstCalled = true
			return nil
		}
		second := func(_ Step) error {
			secondCalled = true
			return nil
		}

		RegisterStepValidator("overwrite-executor", first)
		RegisterStepValidator("overwrite-executor", second)

		dag := &DAG{
			Steps: []Step{
				{
					Name:           "step1",
					ExecutorConfig: ExecutorConfig{Type: "overwrite-executor"},
				},
			},
		}

		err := ValidateSteps(dag)
		assert.NoError(t, err)
		assert.False(t, firstCalled, "first validator should not be called")
		assert.True(t, secondCalled, "second validator should be called")
	})

	t.Run("no validator for type does not fail", func(t *testing.T) {
		dag := &DAG{
			Steps: []Step{
				{
					Name:           "step1",
					ExecutorConfig: ExecutorConfig{Type: "unregistered-executor"},
				},
			},
		}

		err := ValidateSteps(dag)
		assert.NoError(t, err)
	})
}

func TestResolveStepDependencies(t *testing.T) {
	t.Parallel()

	t.Run("resolves ID references to names", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "firstStep", ID: "first"},
				{Name: "secondStep", ID: "second"},
				{Name: "thirdStep", Depends: []string{"first", "second"}},
			},
		}

		resolveStepDependencies(dag)

		// Dependencies should be resolved to names
		assert.Contains(t, dag.Steps[2].Depends, "firstStep")
		assert.Contains(t, dag.Steps[2].Depends, "secondStep")
	})

	t.Run("preserves name references", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1"},
				{Name: "step2", Depends: []string{"step1"}}, // uses name, not ID
			},
		}

		resolveStepDependencies(dag)

		// Name reference should remain unchanged
		assert.Contains(t, dag.Steps[1].Depends, "step1")
	})

	t.Run("mixed ID and name references", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "s1"},
				{Name: "step2"},
				{Name: "step3", Depends: []string{"s1", "step2"}}, // mix of ID and name
			},
		}

		resolveStepDependencies(dag)

		// ID should be resolved, name should remain
		assert.Contains(t, dag.Steps[2].Depends, "step1") // s1 resolved to step1
		assert.Contains(t, dag.Steps[2].Depends, "step2") // step2 unchanged
	})

	t.Run("empty DAG", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{Steps: []Step{}}

		resolveStepDependencies(dag)
		// No panic is expected
	})

	t.Run("steps without dependencies", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "s1"},
				{Name: "step2", ID: "s2"},
			},
		}

		resolveStepDependencies(dag)

		assert.Empty(t, dag.Steps[0].Depends)
		assert.Empty(t, dag.Steps[1].Depends)
	})
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	t.Run("valid step passes", func(t *testing.T) {
		t.Parallel()
		step := Step{Name: "validStep", ExecutorConfig: testExecConfig}
		assert.Empty(t, validateStep(step))
	})

	t.Run("empty name fails", func(t *testing.T) {
		t.Parallel()
		step := Step{Name: "", ExecutorConfig: testExecConfig}
		errs := validateStep(step)
		require.NotEmpty(t, errs)
		assert.ErrorIs(t, errs, ErrStepNameRequired)
	})

	t.Run("name too long fails", func(t *testing.T) {
		t.Parallel()
		step := Step{Name: strings.Repeat("x", 256), ExecutorConfig: testExecConfig}
		errs := validateStep(step)
		require.NotEmpty(t, errs)
		assert.ErrorIs(t, errs, ErrStepNameTooLong)
	})

	t.Run("name at exactly max length passes", func(t *testing.T) {
		t.Parallel()
		step := Step{Name: strings.Repeat("x", 255), ExecutorConfig: testExecConfig}
		assert.Empty(t, validateStep(step))
	})

	t.Run("promoted ID too long gives clear error", func(t *testing.T) {
		t.Parallel()
		longID := strings.Repeat("a", 256)
		step := Step{Name: longID, ID: longID, ExecutorConfig: testExecConfig}
		errs := validateStep(step)
		require.NotEmpty(t, errs)
		assert.Contains(t, errs.Error(), "step ID")
		assert.Contains(t, errs.Error(), "add an explicit shorter 'name' field")
	})
}

func TestValidateStepIDLength(t *testing.T) {
	t.Parallel()

	t.Run("step ID too long fails", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "short", ID: strings.Repeat("a", 41), ExecutorConfig: testExecConfig},
			},
		}
		err := ValidateSteps(dag)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStepIDTooLong)
	})

	t.Run("step ID at max length passes", func(t *testing.T) {
		t.Parallel()
		dag := &DAG{
			Steps: []Step{
				{Name: "ok", ID: strings.Repeat("a", 40), ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})
}

func TestValidateStepWithValidator(t *testing.T) {
	t.Run("no validator returns nil", func(t *testing.T) {
		step := Step{
			Name:           "step1",
			ExecutorConfig: ExecutorConfig{Type: "unknown-type"},
		}
		assert.NoError(t, validateStepWithValidator(step))
	})

	t.Run("nil validator returns nil", func(t *testing.T) {
		defer UnregisterStepValidator("nil-validator-type")
		RegisterStepValidator("nil-validator-type", nil)

		step := Step{
			Name:           "step1",
			ExecutorConfig: ExecutorConfig{Type: "nil-validator-type"},
		}
		assert.NoError(t, validateStepWithValidator(step))
	})

	t.Run("validator error is wrapped", func(t *testing.T) {
		defer UnregisterStepValidator("wrap-error-type")

		customErr := errors.New("custom validation error")
		RegisterStepValidator("wrap-error-type", func(_ Step) error {
			return customErr
		})

		step := Step{
			Name:           "step1",
			ExecutorConfig: ExecutorConfig{Type: "wrap-error-type"},
		}
		err := validateStepWithValidator(step)
		require.Error(t, err)

		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "type", ve.Field)
		assert.Equal(t, "field 'type': custom validation error", err.Error())
		assert.NotContains(t, err.Error(), "executor")
		assert.NotContains(t, err.Error(), "value:")
		assert.ErrorIs(t, err, customErr)
	})

	t.Run("validator validation error keeps field context", func(t *testing.T) {
		defer UnregisterStepValidator("pre-wrapped-error-type")

		RegisterStepValidator("pre-wrapped-error-type", func(_ Step) error {
			return NewValidationError("command", nil, ErrStepCommandIsRequired)
		})

		step := Step{
			Name:           "step1",
			ExecutorConfig: ExecutorConfig{Type: "pre-wrapped-error-type"},
		}
		err := validateStepWithValidator(step)
		require.Error(t, err)

		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "command", ve.Field)
		assert.Equal(t, "field 'command': step command is required", err.Error())
		assert.NotContains(t, err.Error(), "executor_config")
		assert.ErrorIs(t, err, ErrStepCommandIsRequired)
	})
}

func TestValidateSteps_ComplexScenarios(t *testing.T) {
	t.Parallel()

	t.Run("large DAG with many steps", func(t *testing.T) {
		t.Parallel()

		const stepCount = 100
		steps := make([]Step, stepCount)
		for i := range stepCount {
			steps[i] = Step{
				Name:           "step" + strconv.Itoa(i),
				ExecutorConfig: testExecConfig,
			}
			if i > 0 {
				steps[i].Depends = []string{"step" + strconv.Itoa(i-1)}
			}
		}

		dag := &DAG{Steps: steps}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("diamond dependency pattern", func(t *testing.T) {
		t.Parallel()

		dag := &DAG{
			Steps: []Step{
				{Name: "A", ExecutorConfig: testExecConfig},
				{Name: "B", Depends: []string{"A"}, ExecutorConfig: testExecConfig},
				{Name: "C", Depends: []string{"A"}, ExecutorConfig: testExecConfig},
				{Name: "D", Depends: []string{"B", "C"}, ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})

	t.Run("multiple independent chains", func(t *testing.T) {
		t.Parallel()

		dag := &DAG{
			Steps: []Step{
				{Name: "chain1-step1", ExecutorConfig: testExecConfig},
				{Name: "chain1-step2", Depends: []string{"chain1-step1"}, ExecutorConfig: testExecConfig},
				{Name: "chain2-step1", ExecutorConfig: testExecConfig},
				{Name: "chain2-step2", Depends: []string{"chain2-step1"}, ExecutorConfig: testExecConfig},
			},
		}
		assert.NoError(t, ValidateSteps(dag))
	})
}

func TestValidateSteps_MultipleErrors(t *testing.T) {
	t.Parallel()

	t.Run("duplicate_names", func(t *testing.T) {
		t.Parallel()

		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ExecutorConfig: testExecConfig},
				{Name: "step1", ExecutorConfig: testExecConfig},
				{Name: "step2", ExecutorConfig: testExecConfig},
				{Name: "step2", ExecutorConfig: testExecConfig},
			},
		}

		err := ValidateSteps(dag)
		require.Error(t, err)

		var errList ErrorList
		require.ErrorAs(t, err, &errList)
		assert.Len(t, errList, 2, "should collect both duplicate name errors")
	})

	t.Run("missing_dependencies", func(t *testing.T) {
		t.Parallel()

		dag := &DAG{
			Steps: []Step{
				{Name: "step1", Depends: []string{"missing1"}, ExecutorConfig: testExecConfig},
				{Name: "step2", Depends: []string{"missing2"}, ExecutorConfig: testExecConfig},
				{Name: "step3", Depends: []string{"missing3"}, ExecutorConfig: testExecConfig},
			},
		}

		err := ValidateSteps(dag)
		require.Error(t, err)

		var errList ErrorList
		require.ErrorAs(t, err, &errList)
		assert.Len(t, errList, 3, "should collect all 3 missing dependency errors")

		errStr := err.Error()
		assert.Contains(t, errStr, "missing1")
		assert.Contains(t, errStr, "missing2")
		assert.Contains(t, errStr, "missing3")
	})

	t.Run("mixed_validation_errors", func(t *testing.T) {
		t.Parallel()

		dag := &DAG{
			Steps: []Step{
				{Name: "step1", ID: "123invalid", ExecutorConfig: testExecConfig},
				{Name: "step1", ExecutorConfig: testExecConfig},
				{Name: "step2", ID: "env", ExecutorConfig: testExecConfig},
				{Name: "step3", Depends: []string{"missing"}, ExecutorConfig: testExecConfig},
			},
		}

		err := ValidateSteps(dag)
		require.Error(t, err)

		var errList ErrorList
		require.ErrorAs(t, err, &errList)
		assert.GreaterOrEqual(t, len(errList), 3, "should collect multiple validation errors")
	})
}
