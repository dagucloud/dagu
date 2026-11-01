// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// isStepDecisionAction reports whether action decides a step that waits for a
// person.
func isStepDecisionAction(action string) bool {
	switch action {
	case executeActionApprove, executeActionReject, executeActionPushBack:
		return true
	default:
		return false
	}
}

func stepDecisionMessage(input executeInput) string {
	verb := "approved"
	switch input.Action {
	case executeActionReject:
		verb = "rejected"
	case executeActionPushBack:
		verb = "pushed back"
	}
	return "Dagu execute action completed. Step " + input.StepName + " was " + verb + "."
}

// allowRemoteDecision refuses a decision from Dagu Cloud unless this server
// allows remote approvals, and one not tied to the attempt it was read from.
func (svc *Service) allowRemoteDecision(ctx context.Context, input executeInput) error {
	if svc.remote == nil {
		return nil
	}
	if !svc.remote.approvals(ctx) {
		return &frontendapi.Error{
			HTTPStatus: http.StatusForbidden,
			Code:       daguapi.ErrorCodeForbidden,
			Message:    "Remote approvals are off on this server. A Dagu administrator turns them on under Plan & features.",
		}
	}
	if input.AttemptID == "" {
		return executeInputError("The attemptId field is required for "+input.Action+" from Dagu Cloud.", executeFieldAttemptID)
	}
	return nil
}

// decideWaitingStep applies an approve, reject, or push_back decision to the
// named step through the REST handler that owns the step's kind of wait: its
// approval gate or its human task.
func (svc *Service) decideWaitingStep(ctx context.Context, input executeInput) error {
	node, err := svc.waitingStep(ctx, input)
	if err != nil {
		return err
	}
	if node.Step.HumanTask != nil {
		stepID := ""
		if node.Step.Id != nil {
			stepID = *node.Step.Id
		}
		return svc.decideHumanTask(ctx, input, stepID)
	}
	return svc.decideApproval(ctx, input)
}

// waitingStep reads the run that owns the step and returns the step once it is
// known to wait for a person in the attempt the caller expects.
func (svc *Service) waitingStep(ctx context.Context, input executeInput) (daguapi.Node, error) {
	var (
		raw any
		err error
	)
	if input.SubDAGRunID == "" {
		raw, err = svc.api.GetDAGRunDetailsData(ctx, input.Name+"/"+input.DAGRunID)
	} else {
		raw, err = svc.api.GetSubDAGRunDetailsData(ctx, input.Name+"/"+input.DAGRunID+"/"+input.SubDAGRunID)
	}
	if err != nil {
		return daguapi.Node{}, err
	}
	run, err := dagRunDetailsFromResponse(raw)
	if err != nil {
		return daguapi.Node{}, err
	}

	if input.AttemptID != "" {
		current := ""
		if run.AttemptId != nil {
			current = *run.AttemptId
		}
		if current != input.AttemptID {
			return daguapi.Node{}, &executeToolError{
				Code:    executeErrorConflict,
				Message: fmt.Sprintf("The DAG-run is at attempt %s, not %s; read the run again before deciding.", current, input.AttemptID),
				Details: map[string]any{"attemptId": current},
			}
		}
	}

	for _, node := range run.Nodes {
		if node.Step.Name != input.StepName {
			continue
		}
		if node.Status != daguapi.NodeStatusWaiting {
			return daguapi.Node{}, &executeToolError{
				Code:    executeErrorConflict,
				Message: fmt.Sprintf("Step %s is not waiting for a person (status: %s).", input.StepName, node.StatusLabel),
				Details: map[string]any{"stepStatus": node.Status, "stepStatusLabel": node.StatusLabel},
			}
		}
		return node, nil
	}
	return daguapi.Node{}, &executeToolError{
		Code:    executeErrorResourceNotFound,
		Message: fmt.Sprintf("Step %s was not found in the DAG-run.", input.StepName),
		Field:   executeFieldStepName,
	}
}

func (svc *Service) decideApproval(ctx context.Context, input executeInput) error {
	name := daguapi.DAGName(input.Name)
	dagRunID := daguapi.DAGRunId(input.DAGRunID)
	stepName := daguapi.StepName(input.StepName)

	var (
		resp any
		err  error
	)
	switch input.Action {
	case executeActionApprove:
		inputs, inputErr := approvalInputs(input.Inputs)
		if inputErr != nil {
			return inputErr
		}
		body := &daguapi.ApproveStepRequest{Inputs: inputs}
		if input.SubDAGRunID == "" {
			resp, err = svc.api.ApproveDAGRunStep(ctx, daguapi.ApproveDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, StepName: stepName, Body: body,
			})
		} else {
			resp, err = svc.api.ApproveSubDAGRunStep(ctx, daguapi.ApproveSubDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, SubDAGRunId: input.SubDAGRunID, StepName: stepName, Body: body,
			})
		}
	case executeActionReject:
		body := &daguapi.RejectStepRequest{Reason: stringPtr(input.Reason)}
		if input.SubDAGRunID == "" {
			resp, err = svc.api.RejectDAGRunStep(ctx, daguapi.RejectDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, StepName: stepName, Body: body,
			})
		} else {
			resp, err = svc.api.RejectSubDAGRunStep(ctx, daguapi.RejectSubDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, SubDAGRunId: input.SubDAGRunID, StepName: stepName, Body: body,
			})
		}
	case executeActionPushBack:
		inputs, inputErr := approvalInputs(input.Inputs)
		if inputErr != nil {
			return inputErr
		}
		body := &daguapi.PushBackStepRequest{Inputs: inputs}
		if input.SubDAGRunID == "" {
			resp, err = svc.api.PushBackDAGRunStep(ctx, daguapi.PushBackDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, StepName: stepName, Body: body,
			})
		} else {
			resp, err = svc.api.PushBackSubDAGRunStep(ctx, daguapi.PushBackSubDAGRunStepRequestObject{
				Name: name, DagRunId: dagRunID, SubDAGRunId: input.SubDAGRunID, StepName: stepName, Body: body,
			})
		}
	}
	if err != nil {
		return err
	}
	return stepDecisionResult(resp)
}

// approvalInputs decodes approval-gate values, which the gate receives as
// strings.
func approvalInputs(raw json.RawMessage) (*map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var inputs map[string]string
	if err := json.Unmarshal(raw, &inputs); err != nil {
		return nil, executeInputError("Approval gate inputs must be string values.", executeFieldInputs)
	}
	return &inputs, nil
}

// decideHumanTask completes or pushes back a human task. Human tasks have no
// reject operation.
func (svc *Service) decideHumanTask(ctx context.Context, input executeInput, stepID string) error {
	if input.Action == executeActionReject {
		return executeInputError(
			"Step "+input.StepName+" is a human task, which cannot be rejected; use approve to complete it or push_back to send it back.",
			executeFieldAction,
		)
	}

	raw := input.Inputs
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	taskInput, err := humantask.ParseJSONInput(raw)
	if err != nil {
		return executeInputError(err.Error(), executeFieldInputs)
	}
	ctx = frontendapi.WithHumanTaskInput(ctx, taskInput)
	body := daguapi.HumanTaskInput(taskInput.Values)

	var resp any
	if input.Action == executeActionApprove {
		resp, err = svc.api.CompleteHumanTask(ctx, daguapi.CompleteHumanTaskRequestObject{
			Name: input.Name, DagRunId: input.DAGRunID, StepId: stepID, Body: &body,
		})
	} else {
		resp, err = svc.api.PushBackHumanTask(ctx, daguapi.PushBackHumanTaskRequestObject{
			Name: input.Name, DagRunId: input.DAGRunID, StepId: stepID, Body: &body,
		})
	}
	if err != nil {
		return err
	}
	return stepDecisionResult(resp)
}

// stepDecisionResult turns a step decision handler response into nil for
// success or into an API error carrying the response's HTTP status.
func stepDecisionResult(resp any) error {
	switch r := resp.(type) {
	case *daguapi.ApproveDAGRunStep200JSONResponse, daguapi.ApproveDAGRunStep200JSONResponse,
		*daguapi.ApproveSubDAGRunStep200JSONResponse, daguapi.ApproveSubDAGRunStep200JSONResponse,
		*daguapi.RejectDAGRunStep200JSONResponse, daguapi.RejectDAGRunStep200JSONResponse,
		*daguapi.RejectSubDAGRunStep200JSONResponse, daguapi.RejectSubDAGRunStep200JSONResponse,
		*daguapi.PushBackDAGRunStep200JSONResponse, daguapi.PushBackDAGRunStep200JSONResponse,
		*daguapi.PushBackSubDAGRunStep200JSONResponse, daguapi.PushBackSubDAGRunStep200JSONResponse,
		*daguapi.CompleteHumanTask200JSONResponse, daguapi.CompleteHumanTask200JSONResponse,
		*daguapi.PushBackHumanTask200JSONResponse, daguapi.PushBackHumanTask200JSONResponse:
		return nil
	case *daguapi.ApproveDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.ApproveDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.ApproveDAGRunStep503JSONResponse:
		return stepDecisionError(http.StatusServiceUnavailable, daguapi.Error(*r))
	case *daguapi.ApproveSubDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.ApproveSubDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.RejectDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.RejectDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.RejectSubDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.RejectSubDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.PushBackDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.PushBackDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.PushBackSubDAGRunStep400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.PushBackSubDAGRunStep404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.CompleteHumanTask400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.CompleteHumanTask404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.CompleteHumanTask409JSONResponse:
		return stepDecisionError(http.StatusConflict, daguapi.Error(*r))
	case *daguapi.CompleteHumanTask503JSONResponse:
		return stepDecisionError(http.StatusServiceUnavailable, daguapi.Error(*r))
	case *daguapi.PushBackHumanTask400JSONResponse:
		return stepDecisionError(http.StatusBadRequest, daguapi.Error(*r))
	case *daguapi.PushBackHumanTask404JSONResponse:
		return stepDecisionError(http.StatusNotFound, daguapi.Error(*r))
	case *daguapi.PushBackHumanTask409JSONResponse:
		return stepDecisionError(http.StatusConflict, daguapi.Error(*r))
	case *daguapi.PushBackHumanTask503JSONResponse:
		return stepDecisionError(http.StatusServiceUnavailable, daguapi.Error(*r))
	default:
		return fmt.Errorf("unexpected step decision response %T", resp)
	}
}

func stepDecisionError(status int, body daguapi.Error) error {
	return &frontendapi.Error{HTTPStatus: status, Code: body.Code, Message: body.Message}
}

// recordRunStatus adds the run's current status to output. A failed read
// leaves output unchanged, since the decision itself already succeeded.
func (svc *Service) recordRunStatus(ctx context.Context, name, dagRunID string, output map[string]any) {
	raw, err := svc.api.GetDAGRunDetailsData(ctx, name+"/"+dagRunID)
	if err != nil {
		return
	}
	run, err := dagRunDetailsFromResponse(raw)
	if err != nil {
		return
	}
	output["status"] = run.Status
	output["statusLabel"] = run.StatusLabel
}
