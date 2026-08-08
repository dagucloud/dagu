// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import (
	"sync"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// StepValidator validates an executor's step configuration.
type StepValidator func(step ir.Step) error

var stepValidators = struct {
	sync.RWMutex
	entries map[string]StepValidator
}{entries: make(map[string]StepValidator)}

// RegisterStepValidator registers a validator for an executor type.
func RegisterStepValidator(executorType string, validator StepValidator) {
	stepValidators.Lock()
	defer stepValidators.Unlock()
	stepValidators.entries[executorType] = validator
}

// UnregisterStepValidator removes the validator for an executor type.
func UnregisterStepValidator(executorType string) {
	stepValidators.Lock()
	defer stepValidators.Unlock()
	delete(stepValidators.entries, executorType)
}

// ValidateStep runs the registered validator, if any.
func ValidateStep(step ir.Step) error {
	stepValidators.RLock()
	validator := stepValidators.entries[step.ExecutorConfig.Type]
	stepValidators.RUnlock()
	if validator == nil {
		return nil
	}
	return validator(step)
}
