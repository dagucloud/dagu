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

// rejectUnsupportedAIKeys refuses a step that names an AI endpoint (Spec 094)
// or a MCP server entry of the AI page (Spec 095). Both need the AI gateway,
// which this version of Dagu does not start, so the step would fail when it
// starts.
func rejectUnsupportedAIKeys(result *ir.Step) ir.ErrorList {
	if result.Agent != nil {
		// rejectUnsupportedStepKeys refuses the whole action.
		return nil
	}
	var errs ir.ErrorList
	if endpoints := result.AIEndpoints(); len(endpoints) > 0 {
		errs = append(errs, unsupportedKey("endpoint", endpoints[0],
			fmt.Sprintf("the AI endpoint %q", endpoints[0])))
	}
	if servers := result.AIMCPServers(); len(servers) > 0 {
		errs = append(errs, unsupportedKey("tools.mcp", servers[0],
			fmt.Sprintf("the mcp entry %q, which has no url,", servers[0])))
	}
	return errs
}

func unsupportedKey(field string, value any, what string) error {
	return ir.NewValidationError(field, value, fmt.Errorf("%s is not supported in this version of Dagu", what))
}
