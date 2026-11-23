// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// CheckRequiredInputs reports an error naming each required input that inputs
// leave out or give a blank value.
func CheckRequiredInputs(required []string, inputs map[string]string) error {
	var missing []string
	for _, name := range required {
		if strings.TrimSpace(inputs[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required inputs: %v", missing)
	}
	return nil
}

// ApproveStep approves the waiting approval gate of node on behalf of by and
// byID. Each input becomes an output variable and an approval input of the
// step, and replaces a masked output variable of the same name. Nil inputs
// record none.
func ApproveStep(node *ir.Node, inputs map[string]string, by, byID string) {
	node.Status = ir.NodeSucceeded
	node.ApprovedAt = time.Now().Format(time.RFC3339)
	node.ApprovedBy, node.ApprovedByID = by, byID
	if inputs == nil {
		return
	}
	if node.OutputVariables == nil {
		node.OutputVariables = &collections.SyncMap{}
	}
	camelInputs := make(map[string]string, len(inputs))
	for k, v := range inputs {
		node.OutputVariables.Store(k, k+"="+v)
		camelInputs[stringutil.ScreamingSnakeToCamel(k)] = v
	}
	node.ApprovalInputs = camelInputs
	if node.MaskedOutputs != nil {
		node.MaskedOutputs.Variables = slices.DeleteFunc(node.MaskedOutputs.Variables, func(name string) bool {
			_, ok := inputs[name]
			return ok
		})
	}
}

// RejectStep rejects the waiting approval gate of node in status on behalf of
// by and byID, and ends the run rejected. A nil reason keeps the node's
// recorded reason.
func RejectStep(status *ir.DAGRunStatus, node *ir.Node, reason *string, by, byID string) {
	node.Status = ir.NodeRejected
	node.RejectedAt = time.Now().Format(time.RFC3339)
	node.RejectedBy, node.RejectedByID = by, byID
	if reason != nil {
		node.RejectionReason = *reason
	}
	status.Status = ir.Rejected
	status.FinishedAt = stringutil.FormatTime(time.Now())
}
