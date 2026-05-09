// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import "errors"

var (
	ErrInvalidSchedule                     = errors.New("invalid schedule")
	ErrScheduleMustBeStringOrArray         = errors.New("schedule must be a string or an array of strings")
	ErrInvalidScheduleType                 = errors.New("invalid schedule type")
	ErrDotEnvMustBeStringOrArray           = errors.New("dotenv must be a string or an array of strings")
	ErrPreconditionValueMustBeString       = errors.New("precondition value must be a string")
	ErrPreconditionNegateMustBeBool        = errors.New("precondition negate must be a boolean")
	ErrPreconditionHasInvalidKey           = errors.New("precondition has invalid key")
	ErrPreconditionMustBeArrayOrString     = errors.New("precondition must be a string or an array of strings")
	ErrInvalidStepData                     = errors.New("invalid step data")
	ErrStepsMustBeArrayOrMap               = errors.New("steps must be an array or a map")
	ErrContinueOnExitCodeMustBeIntOrArray  = errors.New("continue_on.exit_code must be an int or an array of ints")
	ErrContinueOnOutputMustBeStringOrArray = errors.New("continue_on.output must be a string or an array of strings")
	ErrContinueOnMustBeStringOrMap         = errors.New("continue_on must be a string ('skipped' or 'failed') or an object")
	ErrContinueOnInvalidStringValue        = errors.New("continue_on string value must be 'skipped' or 'failed'")
	ErrContinueOnFieldMustBeBool           = errors.New("value must be a boolean")
	ErrInvalidSignal                       = errors.New("invalid signal")
	ErrDependsMustBeStringOrArray          = errors.New("depends must be a string or an array of strings")
	ErrInvalidEnvValue                     = errors.New("env config should be map of strings or array of key=value formatted string")
	ErrInvalidParamValue                   = errors.New("invalid parameter value")
	ErrStepCommandIsEmpty                  = errors.New("step command is empty")
	ErrStepCommandMustBeArrayOrString      = errors.New("step command must be an array of strings or a string")
	ErrTimeoutSecMustBeNonNegative         = errors.New("timeout_sec must be >= 0")
	ErrExecutorDoesNotSupportMultipleCmd   = errors.New("action does not support multiple commands")
)
