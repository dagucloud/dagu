// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// rejectUnsupportedStepKeys refuses the step keys whose runtime this version
// of Dagu does not have. The loader still checks the rules of each key, but a
// run would not apply it: the step would get every secret of the DAG
// (secrets, Spec 092), an untrusted step would run as the process user
// (untrusted, Spec 092), and an agent.run step (Spec 091) would fail when it
// starts.
func rejectUnsupportedStepKeys(s *step) ir.ErrorList {
	var errs ir.ErrorList
	if s.AgentRun != nil {
		errs = append(errs, unsupportedKey("action", "agent.run", "the action agent.run"))
	}
	if s.Secrets != nil {
		errs = append(errs, unsupportedKey("secrets", s.Secrets, "the step key secrets"))
	}
	if s.Untrusted != nil {
		errs = append(errs, unsupportedKey("untrusted", *s.Untrusted, "the step key untrusted"))
	}
	return errs
}

func unsupportedKey(field string, value any, what string) error {
	return ir.NewValidationError(field, value, fmt.Errorf("%s is not supported in this version of Dagu", what))
}
