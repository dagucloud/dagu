// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// validateAgentRouting checks the rules of Spec 092, "Routing": one agent
// worker runs the whole run, and Dagu sets the labels that start with agent.
func validateAgentRouting(d *ir.DAG) error {
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(d.WorkerSelector)) {
		if err := ir.CheckReservedSelectorKey(key); err != nil {
			errs = append(errs, ir.NewValidationError("worker_selector", d.WorkerSelector, err))
		}
	}

	providers := d.AgentProviders()
	if len(providers) > 1 {
		errs = append(errs, ir.NewValidationError("steps", nil, ir.TwoProvidersError(providers)))
	}
	if len(providers) > 0 {
		need := fmt.Sprintf("the DAG needs the provider %s, so a run goes to an agent worker", strconv.Quote(providers[0]))
		if d.ForceLocal {
			// "local" would run the CLI agent as the user of the scheduler host.
			errs = append(errs, ir.NewValidationError("worker_selector", "local",
				fmt.Errorf("worker_selector %q is not valid: %s", "local", need)))
		}
		if d.HasApprovalSteps() {
			errs = append(errs, ir.NewValidationError("steps", nil,
				fmt.Errorf("%s, and a DAG with approval steps cannot be dispatched to workers", need)))
		}
	}
	return errors.Join(errs...)
}
