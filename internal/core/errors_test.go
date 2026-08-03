// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorList_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		errList  ErrorList
		expected string
	}{
		{
			name:     "empty list returns empty string",
			errList:  ErrorList{},
			expected: "",
		},
		{
			name:     "single error returns error message",
			errList:  ErrorList{errors.New("first error")},
			expected: "first error",
		},
		{
			name:     "multiple errors joined with semicolon",
			errList:  ErrorList{errors.New("first"), errors.New("second"), errors.New("third")},
			expected: "first; second; third",
		},
		{
			name:     "two errors joined with semicolon",
			errList:  ErrorList{errors.New("error1"), errors.New("error2")},
			expected: "error1; error2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.errList.Error()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestErrorList_Dedupe(t *testing.T) {
	t.Parallel()

	shared := NewValidationError("params", "x", errors.New("eval failed"))

	t.Run("identical instances collapse to one", func(t *testing.T) {
		t.Parallel()
		deduped := ErrorList{shared, shared, shared}.Dedupe()
		require.Len(t, deduped, 1)
		assert.Same(t, shared, deduped[0])
	})

	t.Run("distinct errors with equal messages are kept", func(t *testing.T) {
		t.Parallel()
		deduped := ErrorList{errors.New("same text"), errors.New("same text")}.Dedupe()
		assert.Len(t, deduped, 2)
	})

	t.Run("non-comparable members pass through", func(t *testing.T) {
		t.Parallel()
		nested := ErrorList{errors.New("inner")}
		deduped := ErrorList{nested, nested, shared}.Dedupe()
		assert.Len(t, deduped, 3)
	})

	t.Run("order of first occurrences is preserved", func(t *testing.T) {
		t.Parallel()
		other := errors.New("other")
		deduped := ErrorList{shared, other, shared}.Dedupe()
		require.Len(t, deduped, 2)
		assert.Same(t, shared, deduped[0])
		assert.Equal(t, other, deduped[1])
	})

	t.Run("empty and single stay unchanged", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, ErrorList{}.Dedupe())
		assert.Len(t, ErrorList{shared}.Dedupe(), 1)
	})
}

func TestErrorList_ToStringList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		errList  ErrorList
		expected []string
	}{
		{
			name:     "empty list returns empty slice",
			errList:  ErrorList{},
			expected: []string{},
		},
		{
			name:     "single error returns slice with one string",
			errList:  ErrorList{errors.New("single error")},
			expected: []string{"single error"},
		},
		{
			name:     "multiple errors returns slice with all strings",
			errList:  ErrorList{errors.New("first"), errors.New("second"), errors.New("third")},
			expected: []string{"first", "second", "third"},
		},
		{
			name:     "preserves order of errors",
			errList:  ErrorList{errors.New("a"), errors.New("b"), errors.New("c")},
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.errList.ToStringList()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestErrorList_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns nil", func(t *testing.T) {
		t.Parallel()
		errList := ErrorList{}
		result := errList.Unwrap()
		assert.Nil(t, result)
	})

	t.Run("single error returns slice with one error", func(t *testing.T) {
		t.Parallel()
		err := errors.New("test error")
		errList := ErrorList{err}
		result := errList.Unwrap()
		require.Len(t, result, 1)
		assert.Equal(t, err, result[0])
	})

	t.Run("multiple errors returns slice with all errors", func(t *testing.T) {
		t.Parallel()
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		err3 := errors.New("error 3")
		errList := ErrorList{err1, err2, err3}
		result := errList.Unwrap()
		require.Len(t, result, 3)
		assert.Equal(t, err1, result[0])
		assert.Equal(t, err2, result[1])
		assert.Equal(t, err3, result[2])
	})

	t.Run("errors.Is works with unwrapped errors", func(t *testing.T) {
		t.Parallel()
		targetErr := ErrStepNameRequired
		errList := ErrorList{errors.New("other error"), targetErr}

		// errors.Is should find the target error in the list
		assert.True(t, errors.Is(errList, targetErr))
	})

	t.Run("errors.Is returns false for non-existent error", func(t *testing.T) {
		t.Parallel()
		errList := ErrorList{errors.New("other error")}

		// errors.Is should not find ErrStepNameRequired
		assert.False(t, errors.Is(errList, ErrStepNameRequired))
	})
}

func TestValidationError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		field    string
		value    any
		err      error
		expected string
	}{
		{
			name:     "nil value formats without value",
			field:    "testField",
			value:    nil,
			err:      errors.New("test error"),
			expected: "field 'testField': test error",
		},
		{
			name:     "string value formats with value",
			field:    "name",
			value:    "my-dag",
			err:      errors.New("name too long"),
			expected: "field 'name': name too long (value: my-dag)",
		},
		{
			name:     "int value formats with value",
			field:    "maxRetries",
			value:    5,
			err:      errors.New("value out of range"),
			expected: "field 'maxRetries': value out of range (value: 5)",
		},
		{
			name:     "empty field name",
			field:    "",
			value:    "test",
			err:      errors.New("invalid"),
			expected: "field '': invalid (value: test)",
		},
		{
			name:     "struct value uses %+v format",
			field:    "config",
			value:    struct{ Name string }{Name: "test"},
			err:      errors.New("invalid config"),
			expected: "field 'config': invalid config (value: {Name:test})",
		},
		{
			name:     "slice value",
			field:    "labels",
			value:    []string{"a", "b"},
			err:      errors.New("invalid labels"),
			expected: "field 'labels': invalid labels (value: [a b])",
		},
		{
			name:     "bool value",
			field:    "enabled",
			value:    true,
			err:      errors.New("cannot enable"),
			expected: "field 'enabled': cannot enable (value: true)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ve := &ValidationError{
				Field: tt.field,
				Value: tt.value,
				Err:   tt.err,
			}
			result := ve.Error()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidationError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("returns underlying error", func(t *testing.T) {
		t.Parallel()
		underlyingErr := errors.New("underlying error")
		ve := &ValidationError{
			Field: "test",
			Value: nil,
			Err:   underlyingErr,
		}
		result := ve.Unwrap()
		assert.Equal(t, underlyingErr, result)
	})

	t.Run("works with errors.Is", func(t *testing.T) {
		t.Parallel()
		ve := &ValidationError{
			Field: "name",
			Value: "test",
			Err:   ErrStepNameTooLong,
		}
		assert.True(t, errors.Is(ve, ErrStepNameTooLong))
	})

	t.Run("works with errors.As", func(t *testing.T) {
		t.Parallel()
		ve := &ValidationError{
			Field: "test",
			Value: "value",
			Err:   errors.New("test"),
		}

		var targetErr *ValidationError
		assert.True(t, errors.As(ve, &targetErr))
		assert.Equal(t, "test", targetErr.Field)
	})

	t.Run("nil underlying error", func(t *testing.T) {
		t.Parallel()
		ve := &ValidationError{
			Field: "test",
			Value: nil,
			Err:   nil,
		}
		result := ve.Unwrap()
		assert.Nil(t, result)
	})
}

func TestNewValidationError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		field         string
		value         any
		err           error
		expectedField string
		expectedValue any
	}{
		{
			name:          "creates validation error with all fields",
			field:         "steps",
			value:         "step1",
			err:           ErrStepNameDuplicate,
			expectedField: "steps",
			expectedValue: "step1",
		},
		{
			name:          "creates validation error with nil value",
			field:         "name",
			value:         nil,
			err:           ErrStepNameRequired,
			expectedField: "name",
			expectedValue: nil,
		},
		{
			name:          "creates validation error with empty field",
			field:         "",
			value:         123,
			err:           errors.New("test"),
			expectedField: "",
			expectedValue: 123,
		},
		{
			name:          "creates validation error with nil error",
			field:         "test",
			value:         "value",
			err:           nil,
			expectedField: "test",
			expectedValue: "value",
		},
		{
			name:          "creates validation error with complex value",
			field:         "config",
			value:         map[string]int{"a": 1, "b": 2},
			err:           errors.New("invalid"),
			expectedField: "config",
			expectedValue: map[string]int{"a": 1, "b": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := NewValidationError(tt.field, tt.value, tt.err)

			// Assert it returns a ValidationError
			var ve *ValidationError
			require.True(t, errors.As(err, &ve))

			assert.Equal(t, tt.expectedField, ve.Field)
			assert.Equal(t, tt.expectedValue, ve.Value)
			assert.Equal(t, tt.err, ve.Err)
		})
	}
}

func TestErrorConstants(t *testing.T) {
	t.Parallel()

	// Test that all error constants are defined and have meaningful messages
	errorConstants := []struct {
		name string
		err  error
	}{
		{"ErrNameTooLong", ErrNameTooLong},
		{"ErrNameInvalidChars", ErrNameInvalidChars},
		{"ErrInvalidSchedule", ErrInvalidSchedule},
		{"ErrScheduleMustBeStringOrArray", ErrScheduleMustBeStringOrArray},
		{"ErrInvalidScheduleType", ErrInvalidScheduleType},
		{"ErrInvalidKeyType", ErrInvalidKeyType},
		{"ErrExecutorConfigMustBeString", ErrExecutorConfigMustBeString},
		{"ErrDuplicateFunction", ErrDuplicateFunction},
		{"ErrFuncParamsMismatch", ErrFuncParamsMismatch},
		{"ErrInvalidStepData", ErrInvalidStepData},
		{"ErrStepNameRequired", ErrStepNameRequired},
		{"ErrStepNameDuplicate", ErrStepNameDuplicate},
		{"ErrStepNameTooLong", ErrStepNameTooLong},
		{"ErrStepIDTooLong", ErrStepIDTooLong},
		{"ErrStepCommandIsRequired", ErrStepCommandIsRequired},
		{"ErrStepCommandIsEmpty", ErrStepCommandIsEmpty},
		{"ErrStepCommandMustBeArrayOrString", ErrStepCommandMustBeArrayOrString},
		{"ErrInvalidParamValue", ErrInvalidParamValue},
		{"ErrCallFunctionNotFound", ErrCallFunctionNotFound},
		{"ErrNumberOfParamsMismatch", ErrNumberOfParamsMismatch},
		{"ErrRequiredParameterNotFound", ErrRequiredParameterNotFound},
		{"ErrScheduleKeyMustBeString", ErrScheduleKeyMustBeString},
		{"ErrInvalidSignal", ErrInvalidSignal},
		{"ErrInvalidEnvValue", ErrInvalidEnvValue},
		{"ErrArgsMustBeConvertibleToIntOrString", ErrArgsMustBeConvertibleToIntOrString},
		{"ErrExecutorTypeMustBeString", ErrExecutorTypeMustBeString},
		{"ErrExecutorConfigValueMustBeMap", ErrExecutorConfigValueMustBeMap},
		{"ErrExecutorHasInvalidKey", ErrExecutorHasInvalidKey},
		{"ErrExecutorConfigMustBeStringOrMap", ErrExecutorConfigMustBeStringOrMap},
		{"ErrDotEnvMustBeStringOrArray", ErrDotEnvMustBeStringOrArray},
		{"ErrPreconditionMustBeArrayOrString", ErrPreconditionMustBeArrayOrString},
		{"ErrPreconditionValueMustBeString", ErrPreconditionValueMustBeString},
		{"ErrPreconditionHasInvalidKey", ErrPreconditionHasInvalidKey},
		{"ErrContinueOnOutputMustBeStringOrArray", ErrContinueOnOutputMustBeStringOrArray},
		{"ErrContinueOnExitCodeMustBeIntOrArray", ErrContinueOnExitCodeMustBeIntOrArray},
		{"ErrDependsMustBeStringOrArray", ErrDependsMustBeStringOrArray},
		{"ErrStepsMustBeArrayOrMap", ErrStepsMustBeArrayOrMap},
	}

	for _, ec := range errorConstants {
		t.Run(ec.name, func(t *testing.T) {
			t.Parallel()

			// Error should not be nil
			require.NotNil(t, ec.err, "error constant %s should not be nil", ec.name)

			// Error message should not be empty
			msg := ec.err.Error()
			assert.NotEmpty(t, msg, "error constant %s should have a non-empty message", ec.name)

			// Error message should be meaningful (at least 5 characters)
			assert.GreaterOrEqual(t, len(msg), 5,
				"error constant %s message should be meaningful (at least 5 chars): %q", ec.name, msg)
		})
	}
}

func TestErrorList_ImplementsErrorInterface(t *testing.T) {
	t.Parallel()

	// Ensure ErrorList implements the error interface
	var _ error = ErrorList{}
	var _ error = &ErrorList{}

	// Test that it can be used as an error
	errList := ErrorList{errors.New("test")}
	var err error = errList
	assert.NotNil(t, err)
	assert.Equal(t, "test", err.Error())
}

func TestValidationError_ImplementsErrorInterface(t *testing.T) {
	t.Parallel()

	// Ensure ValidationError implements the error interface
	var _ error = &ValidationError{}

	// Test that it can be used as an error
	ve := &ValidationError{Field: "test", Err: errors.New("error")}
	var err error = ve
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "test")
}

func TestErrorList_WithWrappedErrors(t *testing.T) {
	t.Parallel()

	t.Run("contains wrapped validation error", func(t *testing.T) {
		t.Parallel()

		ve := NewValidationError("field", "value", ErrStepNameRequired)
		errList := ErrorList{ve}

		// Should be able to find the wrapped error
		assert.True(t, errors.Is(errList, ErrStepNameRequired))
	})

	t.Run("contains multiple wrapped errors", func(t *testing.T) {
		t.Parallel()

		ve1 := NewValidationError("field1", nil, ErrStepNameRequired)
		ve2 := NewValidationError("field2", nil, ErrStepNameTooLong)
		errList := ErrorList{ve1, ve2}

		// Should find both wrapped errors
		assert.True(t, errors.Is(errList, ErrStepNameRequired))
		assert.True(t, errors.Is(errList, ErrStepNameTooLong))
	})

	t.Run("fmt.Errorf wrapped errors work", func(t *testing.T) {
		t.Parallel()

		wrapped := fmt.Errorf("context: %w", ErrInvalidSchedule)
		errList := ErrorList{wrapped}

		// Should find the wrapped error
		assert.True(t, errors.Is(errList, ErrInvalidSchedule))
	})
}
