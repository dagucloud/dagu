// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	executeActionStart   = "start"
	executeActionEnqueue = "enqueue"
	executeActionRetry   = "retry"
	executeActionStop    = "stop"

	executeTargetTypeDAG        = "dag"
	executeTargetTypeInlineSpec = "inline_spec"
	executeTargetTypeRun        = "run"

	executeErrorUnauthenticated     = "unauthenticated"
	executeErrorUnauthorized        = "unauthorized"
	executeErrorInvalidToolInput    = "invalid_tool_input"
	executeErrorResourceNotFound    = "resource_not_found"
	executeErrorConflict            = "conflict"
	executeErrorResourceUnavailable = "resource_unavailable"
	executeErrorInternal            = "internal_error"

	executeFieldAction             = "action"
	executeFieldTargetType         = "targetType"
	executeFieldName               = "name"
	executeFieldSpec               = "spec"
	executeFieldDAGRunID           = "dagRunId"
	executeFieldParams             = "params"
	executeFieldQueue              = "queue"
	executeFieldSingleton          = "singleton"
	executeFieldNoReuse            = "noReuse"
	executeFieldLabels             = "labels"
	executeFieldStepName           = "stepName"
	executeFieldIncludeDownstream  = "includeDownstream"
	executeFieldWait               = "wait"
	executeFieldWaitTimeoutSeconds = "waitTimeoutSeconds"

	executeWaitDefaultTimeoutSeconds = 60
	executeWaitMaxTimeoutSeconds     = 300
)

type executeInput struct {
	Action             string
	TargetType         string
	Name               string
	Spec               string
	DAGRunID           string
	Params             string
	Queue              string
	Singleton          bool
	NoReuse            bool
	Labels             []string
	StepName           string
	IncludeDownstream  bool
	Wait               bool
	WaitTimeoutSeconds int
}

func executeToolInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["start", "enqueue", "retry", "stop"],
				"description": "Execution action."
			},
			"targetType": {
				"type": "string",
				"enum": ["dag", "inline_spec", "run"],
				"description": "Target type. Defaults to run for retry and stop, inline_spec when spec is present, otherwise dag."
			},
			"name": {
				"type": "string",
				"description": "DAG name. Required for every execution action."
			},
			"spec": {
				"type": "string",
				"description": "Inline DAG YAML spec for start or enqueue with targetType inline_spec."
			},
			"dagRunId": {
				"type": "string",
				"description": "DAG-run ID. Required for retry and stop; optional override for start and enqueue."
			},
			"params": {
				"type": ["string", "object"],
				"description": "Runtime parameters as a JSON object or a JSON-encoded string."
			},
			"queue": {
				"type": "string",
				"description": "Queue override for enqueue."
			},
			"singleton": {
				"type": "boolean",
				"description": "Prevent duplicate running or queued DAG-runs when supported by the action."
			},
			"noReuse": {
				"type": "boolean",
				"description": "Execute eligible build steps without reusing prior materializations for start or enqueue."
			},
			"labels": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Additional labels, each as key=value or key-only."
			},
			"stepName": {
				"type": "string",
				"description": "Optional step name for retry."
			},
			"includeDownstream": {
				"type": "boolean",
				"description": "When true, retry the selected step and every reachable descendant. Requires stepName."
			},
			"wait": {
				"type": "boolean",
				"description": "When true, wait for the identified run to reach a terminal state and include its result summary in the output."
			},
			"waitTimeoutSeconds": {
				"type": "integer",
				"minimum": 1,
				"maximum": 300,
				"description": "Maximum seconds to wait when wait is true. Defaults to 60. On timeout the output has completed=false and the run keeps executing."
			}
		},
		"required": ["action", "name"],
		"additionalProperties": false
	}`)
}

type executeToolError struct {
	Code       string
	Message    string
	Action     string
	TargetType string
	DAGName    string
	DAGRunID   string
	Field      string
	Details    map[string]any
}

func (e *executeToolError) Error() string {
	return e.Message
}

func (svc *Service) executeTool(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	raw := json.RawMessage(nil)
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}

	input, executeErr := parseExecuteToolInput(raw)
	if executeErr != nil {
		return executeErrorResult(executeErr), nil
	}

	result, output, err := auditToolCall(ctx, svc.api, req, toolExecute, executeAuditMetadata(input), func(ctx context.Context) (*mcpsdk.CallToolResult, map[string]any, error) {
		return svc.executeToolImpl(ctx, input)
	})
	if err != nil {
		executeErr := classifyExecuteToolError(input, err)
		if executeErr.Details == nil && executeErr.Code == executeErrorResourceNotFound && input.TargetType == executeTargetTypeDAG {
			executeErr.Details = svc.didYouMeanDetails(ctx, input.Name)
		}
		return executeErrorResult(executeErr), nil
	}
	result.StructuredContent = output
	return result, nil
}

func (svc *Service) executeToolImpl(ctx context.Context, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.requireAPI(); err != nil {
		return nil, nil, err
	}

	var (
		dagRunID string
		err      error
	)

	switch input.Action {
	case executeActionStart:
		dagRunID, err = svc.startDAG(ctx, input.TargetType, input)
	case executeActionEnqueue:
		dagRunID, err = svc.enqueueDAG(ctx, input.TargetType, input)
	case executeActionRetry:
		err = svc.retryDAGRun(ctx, input)
		dagRunID = input.DAGRunID
	case executeActionStop:
		err = svc.stopDAGRun(ctx, input)
		dagRunID = input.DAGRunID
	}
	if err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"action":     input.Action,
		"targetType": input.TargetType,
		"dagName":    input.Name,
		"dagRunId":   dagRunID,
		"references": defaultReferenceURIs(),
	}
	links := []resourceLink{}
	message := "Dagu execute action completed."
	if input.Name != "" && dagRunID != "" {
		run := runURI(input.Name, dagRunID)
		logs := runLogsURI(input.Name, dagRunID)
		output["runUri"] = run
		output["logsUri"] = logs
		links = append(links, resourceLink{
			uri:         run,
			name:        "dag_run",
			title:       "DAG-run details",
			description: "Subscribe to this resource for completion notification.",
			mimeType:    resourceMIMEJSON,
		}, resourceLink{
			uri:         logs,
			name:        "dag_run_logs",
			title:       "DAG-run logs",
			description: "Logs for this DAG-run.",
			mimeType:    resourceMIMEJSON,
		})
		if input.Wait {
			message = svc.waitForRun(ctx, input, dagRunID, output)
		} else {
			output["subscribe"] = "Subscribe to " + run + " to receive an MCP resource update notification when the run reaches a terminal state."
		}
	}

	return resultWithLinks(message, links...), output, nil
}

// waitForRun polls the identified run until it reaches a terminal state or the
// requested timeout elapses, records the outcome in output, and returns the
// result message. Poll errors end the wait but never fail the already
// successful execute action.
func (svc *Service) waitForRun(ctx context.Context, input executeInput, dagRunID string, output map[string]any) string {
	timeoutSeconds := input.WaitTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = executeWaitDefaultTimeoutSeconds
	}
	pollInterval := svc.watchPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultRunWatchPollInterval
	}

	deadline := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	output["completed"] = false
	for {
		raw, err := svc.api.GetDAGRunDetailsData(ctx, input.Name+"/"+dagRunID)
		if err == nil {
			run, detailsErr := dagRunDetailsFromResponse(raw)
			if detailsErr != nil {
				output["waitError"] = detailsErr.Error()
				return "Dagu execute action completed, but waiting for the run failed. Poll the runUri resource for completion."
			}
			output["status"] = run.Status
			output["statusLabel"] = run.StatusLabel
			if isTerminalStatus(int(run.Status)) {
				details, normalizeErr := normalizeRunDetails(raw, runAddress{name: input.Name, dagRunID: dagRunID})
				if normalizeErr != nil {
					output["waitError"] = normalizeErr.Error()
					return "Dagu execute action completed, but waiting for the run failed. Poll the runUri resource for completion."
				}
				output["completed"] = true
				output["run"] = details
				return "Dagu execute action completed. The run finished with status " + string(run.StatusLabel) + "."
			}
		} else if !isTransientWaitError(input.Action, err) {
			output["waitError"] = err.Error()
			return "Dagu execute action completed, but waiting for the run failed. Poll the runUri resource for completion."
		}

		select {
		case <-ctx.Done():
			output["waitError"] = ctx.Err().Error()
			return "Dagu execute action completed, but waiting was interrupted. Poll the runUri resource for completion."
		case <-deadline.C:
			return "Dagu execute action completed. The run did not reach a terminal state within " +
				strconv.Itoa(timeoutSeconds) + "s; poll the runUri resource for completion."
		case <-ticker.C:
		}
	}
}

// isTransientWaitError reports whether a run-details poll error can resolve on
// a later poll: an enqueued run has no status data until a worker picks it up.
func isTransientWaitError(action string, err error) bool {
	if action != executeActionEnqueue {
		return false
	}
	return isDAGNotFound(err) ||
		errors.Is(err, dagrun.ErrDAGRunIDNotFound) ||
		errors.Is(err, dagrun.ErrNoStatusData)
}

func parseExecuteToolInput(raw json.RawMessage) (executeInput, *executeToolError) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return executeInput{}, &executeToolError{
			Code:    executeErrorInvalidToolInput,
			Message: "Tool input must be a JSON object.",
		}
	}

	var input executeInput
	provided := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	for field := range fields {
		keys = append(keys, field)
	}
	sort.Strings(keys)

	for _, field := range keys {
		value := fields[field]
		if string(value) == "null" {
			continue
		}
		provided[field] = true
		var parseErr *executeToolError
		switch field {
		case executeFieldAction:
			input.Action, parseErr = executeStringField(field, value)
		case executeFieldTargetType:
			input.TargetType, parseErr = executeStringField(field, value)
		case executeFieldName:
			input.Name, parseErr = executeStringField(field, value)
		case executeFieldSpec:
			input.Spec, parseErr = executeStringField(field, value)
		case executeFieldDAGRunID:
			input.DAGRunID, parseErr = executeStringField(field, value)
		case executeFieldParams:
			input.Params, parseErr = executeParamsField(value)
		case executeFieldQueue:
			input.Queue, parseErr = executeStringField(field, value)
		case executeFieldSingleton:
			input.Singleton, parseErr = executeBoolField(field, value)
		case executeFieldNoReuse:
			input.NoReuse, parseErr = executeBoolField(field, value)
		case executeFieldLabels:
			if err := json.Unmarshal(value, &input.Labels); err != nil {
				parseErr = executeInputError("Field labels must be an array of strings.", executeFieldLabels)
			}
		case executeFieldStepName:
			input.StepName, parseErr = executeStringField(field, value)
		case executeFieldIncludeDownstream:
			input.IncludeDownstream, parseErr = executeBoolField(field, value)
		case executeFieldWait:
			input.Wait, parseErr = executeBoolField(field, value)
		case executeFieldWaitTimeoutSeconds:
			if err := json.Unmarshal(value, &input.WaitTimeoutSeconds); err != nil {
				parseErr = executeInputError("Field waitTimeoutSeconds must be an integer.", executeFieldWaitTimeoutSeconds)
			}
		default:
			parseErr = executeInputError("Unknown field "+field+".", field)
		}
		if parseErr != nil {
			return executeInput{}, parseErr
		}
	}

	if executeErr := validateExecuteInput(&input, provided); executeErr != nil {
		return executeInput{}, executeErr
	}
	return input, nil
}

func executeStringField(field string, value json.RawMessage) (string, *executeToolError) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", executeInputError("Field "+field+" must be a string.", field)
	}
	if field == executeFieldSpec {
		return text, nil
	}
	return strings.TrimSpace(text), nil
}

func executeBoolField(field string, value json.RawMessage) (bool, *executeToolError) {
	var flag bool
	if err := json.Unmarshal(value, &flag); err != nil {
		return false, executeInputError("Field "+field+" must be a boolean.", field)
	}
	return flag, nil
}

// executeParamsField accepts runtime parameters as a JSON object or a string
// and canonicalizes the object form to its compact JSON encoding.
func executeParamsField(value json.RawMessage) (string, *executeToolError) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text), nil
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil {
		return "", executeInputError("Field params must be a string or an object.", executeFieldParams)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", executeInputError("Field params could not be encoded as JSON.", executeFieldParams)
	}
	return string(encoded), nil
}

func validateExecuteInput(input *executeInput, provided map[string]bool) *executeToolError {
	switch input.Action {
	case "":
		return executeInputError("The action field is required.", executeFieldAction)
	case executeActionStart, executeActionEnqueue, executeActionRetry, executeActionStop:
	default:
		return executeInputError("Unsupported execute action.", executeFieldAction)
	}

	if input.TargetType == "" {
		switch {
		case input.Action == executeActionRetry || input.Action == executeActionStop:
			input.TargetType = executeTargetTypeRun
		case strings.TrimSpace(input.Spec) != "":
			input.TargetType = executeTargetTypeInlineSpec
		default:
			input.TargetType = executeTargetTypeDAG
		}
	}
	switch input.TargetType {
	case executeTargetTypeDAG, executeTargetTypeInlineSpec, executeTargetTypeRun:
	default:
		return executeInputError("Unsupported targetType.", executeFieldTargetType)
	}

	switch input.Action {
	case executeActionStart, executeActionEnqueue:
		switch input.TargetType {
		case executeTargetTypeDAG:
			if provided[executeFieldSpec] {
				return executeInputError("The spec field is only valid for targetType inline_spec.", executeFieldSpec)
			}
		case executeTargetTypeInlineSpec:
			if strings.TrimSpace(input.Spec) == "" {
				return executeInputError("The spec field is required for targetType inline_spec.", executeFieldSpec)
			}
		default:
			return executeInputError("The run targetType is only valid for retry and stop.", executeFieldTargetType)
		}
	case executeActionRetry, executeActionStop:
		if input.TargetType != executeTargetTypeRun {
			return executeInputError("The "+input.Action+" action requires targetType run.", executeFieldTargetType)
		}
	}

	if input.Name == "" {
		return executeInputError("The name field is required.", executeFieldName)
	}
	if input.Action != executeActionEnqueue && input.Queue != "" {
		return executeInputError("The queue field is only valid for enqueue.", executeFieldQueue)
	}
	launchAction := input.Action == executeActionStart || input.Action == executeActionEnqueue
	if !launchAction {
		switch {
		case provided[executeFieldSpec]:
			return executeInputError("The spec field is only valid for start and enqueue.", executeFieldSpec)
		case input.Params != "":
			return executeInputError("The params field is only valid for start and enqueue.", executeFieldParams)
		case provided[executeFieldSingleton]:
			return executeInputError("The singleton field is only valid for start and enqueue.", executeFieldSingleton)
		case provided[executeFieldNoReuse]:
			return executeInputError("The noReuse field is only valid for start and enqueue.", executeFieldNoReuse)
		case provided[executeFieldLabels]:
			return executeInputError("The labels field is only valid for start and enqueue.", executeFieldLabels)
		}
	}
	if input.Action != executeActionRetry {
		if input.StepName != "" {
			return executeInputError("The stepName field is only valid for retry.", executeFieldStepName)
		}
		if provided[executeFieldIncludeDownstream] {
			return executeInputError("The includeDownstream field is only valid for retry.", executeFieldIncludeDownstream)
		}
	}
	if !launchAction && input.DAGRunID == "" {
		return executeInputError("The dagRunId field is required.", executeFieldDAGRunID)
	}
	if input.IncludeDownstream && strings.TrimSpace(input.StepName) == "" {
		return executeInputError("includeDownstream requires stepName", executeFieldStepName)
	}
	if provided[executeFieldWaitTimeoutSeconds] {
		if !input.Wait {
			return executeInputError("waitTimeoutSeconds requires wait.", executeFieldWaitTimeoutSeconds)
		}
		if input.WaitTimeoutSeconds < 1 || input.WaitTimeoutSeconds > executeWaitMaxTimeoutSeconds {
			return executeInputError("waitTimeoutSeconds must be between 1 and 300.", executeFieldWaitTimeoutSeconds)
		}
	}
	return nil
}

func executeInputError(message, field string) *executeToolError {
	return &executeToolError{
		Code:    executeErrorInvalidToolInput,
		Message: message,
		Field:   field,
	}
}

func classifyExecuteToolError(input executeInput, err error) *executeToolError {
	out := &executeToolError{
		Code:       executeErrorInternal,
		Message:    "Internal MCP execute error.",
		Action:     input.Action,
		TargetType: input.TargetType,
		DAGName:    input.Name,
		DAGRunID:   input.DAGRunID,
	}

	if apiErr, ok := errors.AsType[*frontendapi.Error](err); ok {
		out.Message = apiErr.Message
		switch apiErr.HTTPStatus {
		case http.StatusUnauthorized:
			out.Code = executeErrorUnauthenticated
		case http.StatusForbidden:
			out.Code = executeErrorUnauthorized
		case http.StatusBadRequest:
			out.Code = executeErrorInvalidToolInput
		case http.StatusNotFound:
			out.Code = executeErrorResourceNotFound
		case http.StatusConflict:
			out.Code = executeErrorConflict
		default:
			out.Code = executeErrorResourceUnavailable
		}
		return out
	}

	if isReadResourceNotFound(err) {
		out.Code = executeErrorResourceNotFound
		out.Message = err.Error()
		return out
	}

	if err != nil {
		out.Message = err.Error()
	}
	return out
}

func executeErrorResult(err *executeToolError) *mcpsdk.CallToolResult {
	output := map[string]any{
		"code":    err.Code,
		"message": err.Message,
	}
	if err.Action != "" {
		output["action"] = err.Action
	}
	if err.TargetType != "" {
		output["targetType"] = err.TargetType
	}
	if err.DAGName != "" {
		output["dagName"] = err.DAGName
	}
	if err.DAGRunID != "" {
		output["dagRunId"] = err.DAGRunID
	}
	if err.Field != "" {
		output["field"] = err.Field
	}
	if err.Details != nil {
		output["details"] = err.Details
	}
	return &mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: err.Message}},
		StructuredContent: output,
		IsError:           true,
	}
}
