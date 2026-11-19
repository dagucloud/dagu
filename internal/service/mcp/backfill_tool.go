// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	executeActionBackfill       = "backfill"
	executeActionBackfillResume = "backfill_resume"
	executeActionBackfillCancel = "backfill_cancel"

	executeFieldBackfillID     = "backfillId"
	executeFieldFrom           = "from"
	executeFieldTo             = "to"
	executeFieldBy             = "by"
	executeFieldSlots          = "slots"
	executeFieldRerun          = "rerun"
	executeFieldMaxConcurrency = "maxConcurrency"
	executeFieldOnFailure      = "onFailure"
	executeFieldDryRun         = "dryRun"
	executeFieldRevision       = "revision"
	executeFieldStopRunning    = "stopRunning"
)

// backfillInput holds the fields of the backfill actions.
type backfillInput struct {
	ID             string
	From           string
	To             string
	By             string
	Slots          []string
	Rerun          string
	MaxConcurrency int
	OnFailure      string
	DryRun         bool
	Revision       string
	StopRunning    bool
}

// backfillFields lists, per backfill action, the fields it takes besides
// action and name.
var backfillFields = map[string][]string{
	executeActionBackfill: {
		executeFieldFrom, executeFieldTo, executeFieldBy, executeFieldSlots, executeFieldRerun, executeFieldMaxConcurrency,
		executeFieldOnFailure, executeFieldParams, executeFieldProfile, executeFieldDryRun, executeFieldRevision,
	},
	executeActionBackfillResume: {
		executeFieldBackfillID, executeFieldMaxConcurrency, executeFieldOnFailure, executeFieldDryRun, executeFieldRevision,
	},
	executeActionBackfillCancel: {executeFieldBackfillID, executeFieldStopRunning},
}

func isBackfillAction(action string) bool {
	_, ok := backfillFields[action]
	return ok
}

// isBackfillField reports whether a field belongs to the backfill actions
// only.
func isBackfillField(field string) bool {
	switch field {
	case executeFieldBackfillID, executeFieldFrom, executeFieldTo, executeFieldBy, executeFieldSlots, executeFieldRerun,
		executeFieldMaxConcurrency, executeFieldOnFailure, executeFieldDryRun, executeFieldRevision, executeFieldStopRunning:
		return true
	}
	return false
}

// parseBackfillField reads a field of the backfill actions into input.
func parseBackfillField(input *backfillInput, field string, value json.RawMessage) *executeToolError {
	var err *executeToolError
	switch field {
	case executeFieldBackfillID:
		input.ID, err = executeStringField(field, value)
	case executeFieldFrom:
		input.From, err = executeStringField(field, value)
	case executeFieldTo:
		input.To, err = executeStringField(field, value)
	case executeFieldBy:
		input.By, err = executeStringField(field, value)
	case executeFieldSlots:
		if json.Unmarshal(value, &input.Slots) != nil {
			err = executeInputError("Field slots must be an array of strings.", field)
		}
	case executeFieldRerun:
		input.Rerun, err = executeStringField(field, value)
	case executeFieldMaxConcurrency:
		if json.Unmarshal(value, &input.MaxConcurrency) != nil || input.MaxConcurrency < 1 {
			err = executeInputError("Field maxConcurrency must be a positive integer.", field)
		}
	case executeFieldOnFailure:
		input.OnFailure, err = executeStringField(field, value)
	case executeFieldDryRun:
		input.DryRun, err = executeBoolField(field, value)
	case executeFieldRevision:
		input.Revision, err = executeStringField(field, value)
	case executeFieldStopRunning:
		input.StopRunning, err = executeBoolField(field, value)
	}
	return err
}

// validateBackfillInput checks the fields of a backfill action.
func validateBackfillInput(input *executeInput, provided map[string]bool) *executeToolError {
	if input.Name == "" {
		return executeInputError("The name field is required.", executeFieldName)
	}
	allowed := backfillFields[input.Action]
	for field := range provided {
		if field == executeFieldAction || field == executeFieldName || slices.Contains(allowed, field) {
			continue
		}
		return executeInputError(fmt.Sprintf("The %s field is not valid for %s.", field, input.Action), field)
	}
	if input.Action != executeActionBackfill && input.Backfill.ID == "" {
		return executeInputError("The backfillId field is required.", executeFieldBackfillID)
	}
	return nil
}

// executeBackfill runs a backfill action through the API.
func (svc *Service) executeBackfill(ctx context.Context, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	in := input.Backfill
	output := map[string]any{
		"action":     input.Action,
		"targetType": executeTargetTypeDAG,
		"dagName":    input.Name,
		"dagRunId":   "",
		"references": defaultReferenceURIs(),
	}
	if input.Action != executeActionBackfill {
		dagName, err := svc.backfillDAGName(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		output["dagName"] = dagName
	}
	switch input.Action {
	case executeActionBackfill:
		body := &daguapi.BackfillRequest{
			From:     optionalString(in.From),
			To:       optionalString(in.To),
			By:       optionalString(in.By),
			Params:   optionalString(input.Params),
			Profile:  optionalString(input.Profile),
			DryRun:   &in.DryRun,
			Revision: optionalString(in.Revision),
		}
		if len(in.Slots) > 0 {
			body.Slots = &in.Slots
		}
		if in.Rerun != "" {
			body.Rerun = new(daguapi.BackfillRerun(in.Rerun))
		}
		if in.MaxConcurrency > 0 {
			body.MaxConcurrency = &in.MaxConcurrency
		}
		if in.OnFailure != "" {
			body.OnFailure = new(daguapi.BackfillOnFailure(in.OnFailure))
		}
		resp, err := svc.api.SubmitBackfill(ctx, daguapi.SubmitBackfillRequestObject{FileName: daguapi.DAGFileName(input.Name), Body: body})
		if err != nil {
			return nil, nil, err
		}
		switch r := resp.(type) {
		case daguapi.SubmitBackfill200JSONResponse:
			addBackfillResponse(output, daguapi.BackfillSubmitResponse(r))
		case daguapi.SubmitBackfill409JSONResponse:
			return nil, nil, staleBackfillPlan(input, daguapi.BackfillSubmitResponse(r))
		default:
			return nil, nil, fmt.Errorf("unexpected backfill response %T", resp)
		}
	case executeActionBackfillResume:
		body := &daguapi.ResumeBackfillJSONRequestBody{DryRun: &in.DryRun, Revision: optionalString(in.Revision)}
		if in.MaxConcurrency > 0 {
			body.MaxConcurrency = &in.MaxConcurrency
		}
		if in.OnFailure != "" {
			body.OnFailure = new(daguapi.BackfillOnFailure(in.OnFailure))
		}
		resp, err := svc.api.ResumeBackfill(ctx, daguapi.ResumeBackfillRequestObject{BackfillId: in.ID, Body: body})
		if err != nil {
			return nil, nil, err
		}
		switch r := resp.(type) {
		case daguapi.ResumeBackfill200JSONResponse:
			addBackfillResponse(output, daguapi.BackfillSubmitResponse(r))
		case daguapi.ResumeBackfill409JSONResponse:
			return nil, nil, staleBackfillPlan(input, daguapi.BackfillSubmitResponse(r))
		default:
			return nil, nil, fmt.Errorf("unexpected backfill resume response %T", resp)
		}
	case executeActionBackfillCancel:
		resp, err := svc.api.CancelBackfill(ctx, daguapi.CancelBackfillRequestObject{
			BackfillId: in.ID,
			Body:       &daguapi.CancelBackfillJSONRequestBody{StopRunning: &in.StopRunning},
		})
		if err != nil {
			return nil, nil, err
		}
		switch r := resp.(type) {
		case daguapi.CancelBackfill200JSONResponse:
			output["backfill"] = daguapi.Backfill(r)
		case daguapi.CancelBackfill409JSONResponse:
			return nil, nil, &executeToolError{Code: executeErrorConflict, Message: r.Message, Action: input.Action, DAGName: input.Name}
		default:
			return nil, nil, fmt.Errorf("unexpected backfill cancel response %T", resp)
		}
	}
	return resultWithLinks(backfillMessage(input, output)), output, nil
}

// backfillDAGName returns the DAG of the backfill that input names. It fails
// when that is not the DAG named by input.Name, its name or its file name, so
// a backfill of one DAG is never changed through another.
func (svc *Service) backfillDAGName(ctx context.Context, input executeInput) (string, error) {
	resp, err := svc.api.GetBackfill(ctx, daguapi.GetBackfillRequestObject{BackfillId: input.Backfill.ID})
	if err != nil {
		return "", err
	}
	found, ok := resp.(daguapi.GetBackfill200JSONResponse)
	if !ok {
		return "", fmt.Errorf("unexpected backfill response %T", resp)
	}
	if found.Dag == input.Name {
		return found.Dag, nil
	}
	details, err := svc.api.GetDAGDetails(ctx, daguapi.GetDAGDetailsRequestObject{FileName: daguapi.DAGFileName(input.Name)})
	if err == nil {
		if named, ok := details.(daguapi.GetDAGDetails200JSONResponse); ok && named.Dag != nil && named.Dag.Name == found.Dag {
			return found.Dag, nil
		}
	}
	return "", executeInputError(fmt.Sprintf("Backfill %s belongs to DAG %s, not %s.", input.Backfill.ID, found.Dag, input.Name), executeFieldBackfillID)
}

func addBackfillResponse(output map[string]any, resp daguapi.BackfillSubmitResponse) {
	output["plan"] = resp.Plan
	if resp.Backfill != nil {
		output["backfill"] = *resp.Backfill
		output["backfillId"] = resp.Backfill.Id
	}
	if resp.Existing != nil && *resp.Existing {
		output["existing"] = true
	}
}

func staleBackfillPlan(input executeInput, resp daguapi.BackfillSubmitResponse) *executeToolError {
	return &executeToolError{
		Code:    executeErrorConflict,
		Message: "The plan changed since its revision; nothing was started. Review the new plan in details.plan.",
		Action:  input.Action,
		DAGName: input.Name,
		Details: map[string]any{"plan": resp.Plan},
	}
}

func backfillMessage(input executeInput, output map[string]any) string {
	switch {
	case input.Action == executeActionBackfillCancel:
		return "Backfill cancelled."
	case input.Backfill.DryRun:
		return "Backfill plan; nothing was started."
	case output["existing"] == true:
		return "A backfill with the same options is already open; nothing was started."
	case output["backfillId"] == nil:
		return "No slot needs a run; nothing was started."
	default:
		return "Backfill submitted. The scheduler queues its runs."
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
