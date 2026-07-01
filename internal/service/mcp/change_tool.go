// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	frontendapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	changeModePreview = "preview"
	changeModeApply   = "apply"

	changeTypeUpsertDAG = "upsert_dag"

	changeErrorUnauthenticated       = "unauthenticated"
	changeErrorUnauthorized          = "unauthorized"
	changeErrorInvalidToolInput      = "invalid_tool_input"
	changeErrorUnsupportedChangeMode = "unsupported_change_mode"
	changeErrorUnsupportedChangeType = "unsupported_change_type"
	changeErrorResourceUnavailable   = "resource_unavailable"
	changeErrorInternal              = "internal_error"
	changeFieldMode                  = "mode"
	changeFieldType                  = "type"
	changeFieldName                  = "name"
	changeFieldSpec                  = "spec"
)

type changeToolError struct {
	Code    string
	Message string
	Mode    string
	Type    string
	DAGName string
	Field   string
	DAGURI  string
	Details map[string]any
}

func (e *changeToolError) Error() string {
	return e.Message
}

func changeToolInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"mode": {
				"type": "string",
				"description": "Change execution mode: preview or apply. Defaults to preview."
			},
			"type": {
				"type": "string",
				"description": "Change type. The only supported value is upsert_dag."
			},
			"name": {
				"type": "string",
				"description": "Target DAG name."
			},
			"spec": {
				"type": "string",
				"description": "DAG YAML document to validate and optionally store."
			}
		},
		"required": ["name", "spec"],
		"additionalProperties": false
	}`)
}

func (svc *Service) changeTool(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	raw := json.RawMessage(nil)
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}

	input, changeErr := parseChangeToolInput(raw)
	if changeErr != nil {
		return changeErrorResult(changeErr), nil
	}

	result, output, err := auditToolCall(ctx, svc.api, req, toolChange, changeAuditMetadata(input), func(ctx context.Context) (*mcpsdk.CallToolResult, map[string]any, error) {
		return svc.changeToolImpl(ctx, input)
	})
	if err != nil {
		return changeErrorResult(classifyChangeToolError(input, err)), nil
	}
	result.StructuredContent = output
	return result, nil
}

func (svc *Service) changeToolImpl(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.requireAPI(); err != nil {
		return nil, nil, err
	}

	validation, err := svc.validateDAGSpec(ctx, input.Name, input.Spec)
	if err != nil {
		return nil, nil, err
	}

	validationErrors := make([]string, 0, len(validation.Errors))
	validationErrors = append(validationErrors, validation.Errors...)

	output := map[string]any{
		"mode":       input.Mode,
		"type":       input.Type,
		"dagName":    input.Name,
		"valid":      validation.Valid,
		"errors":     validationErrors,
		"applied":    false,
		"references": defaultReferenceURIs(),
		"dagUri":     dagSpecURI(input.Name),
	}
	if validation.Valid && validation.Dag != nil {
		output["dag"] = validation.Dag
	}

	if !validation.Valid {
		return resultWithLinks("DAG spec is not valid; no changes were applied.", linkForDAGSpec(input.Name)), output, nil
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("DAG spec is valid. Re-run with mode=apply to write it.", linkForDAGSpec(input.Name)), output, nil
	}

	created, err := svc.upsertDAG(ctx, input.Name, input.Spec)
	if err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	output["created"] = created
	output["updated"] = !created

	return resultWithLinks("DAG change applied.", linkForDAGSpec(input.Name)), output, nil
}

func parseChangeToolInput(raw json.RawMessage) (changeInput, *changeToolError) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return changeInput{}, &changeToolError{
			Code:    changeErrorInvalidToolInput,
			Message: "Tool input must be a JSON object.",
		}
	}

	var input changeInput
	keys := make([]string, 0, len(fields))
	for field := range fields {
		keys = append(keys, field)
	}
	sort.Strings(keys)

	for _, field := range keys {
		value := fields[field]
		if !isChangeInputField(field) {
			return changeInput{}, &changeToolError{
				Code:    changeErrorInvalidToolInput,
				Message: "Unknown field " + field + ".",
				Field:   field,
			}
		}
		if string(value) == "null" {
			continue
		}

		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return changeInput{}, &changeToolError{
				Code:    changeErrorInvalidToolInput,
				Message: "Field " + field + " must be a string.",
				Field:   field,
			}
		}

		switch field {
		case changeFieldMode:
			input.Mode = strings.TrimSpace(text)
		case changeFieldType:
			input.Type = strings.TrimSpace(text)
		case changeFieldName:
			input.Name = strings.TrimSpace(text)
		case changeFieldSpec:
			input.Spec = text
		}
	}

	if input.Mode == "" {
		input.Mode = changeModePreview
	}
	if input.Type == "" {
		input.Type = changeTypeUpsertDAG
	}
	if input.Name == "" {
		return input, changeInputError(input, "The name field is required.", changeFieldName)
	}
	if strings.TrimSpace(input.Spec) == "" {
		return input, changeInputError(input, "The spec field is required.", changeFieldSpec)
	}
	if input.Mode != changeModePreview && input.Mode != changeModeApply {
		return input, &changeToolError{
			Code:    changeErrorUnsupportedChangeMode,
			Message: "Unsupported change mode.",
			Mode:    input.Mode,
			Type:    input.Type,
			DAGName: input.Name,
			Field:   changeFieldMode,
			DAGURI:  dagSpecURI(input.Name),
		}
	}
	if input.Type != changeTypeUpsertDAG {
		return input, &changeToolError{
			Code:    changeErrorUnsupportedChangeType,
			Message: "Unsupported change type.",
			Mode:    input.Mode,
			Type:    input.Type,
			DAGName: input.Name,
			Field:   changeFieldType,
			DAGURI:  dagSpecURI(input.Name),
		}
	}

	return input, nil
}

func isChangeInputField(field string) bool {
	switch field {
	case changeFieldMode, changeFieldType, changeFieldName, changeFieldSpec:
		return true
	default:
		return false
	}
}

func changeInputError(input changeInput, message, field string) *changeToolError {
	err := &changeToolError{
		Code:    changeErrorInvalidToolInput,
		Message: message,
		Mode:    input.Mode,
		Type:    input.Type,
		Field:   field,
	}
	if input.Name != "" {
		err.DAGName = input.Name
		err.DAGURI = dagSpecURI(input.Name)
	}
	return err
}

func classifyChangeToolError(input changeInput, err error) *changeToolError {
	out := &changeToolError{
		Code:    changeErrorInternal,
		Message: "Internal MCP change error.",
		Mode:    input.Mode,
		Type:    input.Type,
		DAGName: input.Name,
	}
	if input.Name != "" {
		out.DAGURI = dagSpecURI(input.Name)
	}

	var apiErr *frontendapi.Error
	if errors.As(err, &apiErr) {
		out.Message = apiErr.Message
		switch apiErr.HTTPStatus {
		case http.StatusUnauthorized:
			out.Code = changeErrorUnauthenticated
		case http.StatusForbidden:
			out.Code = changeErrorUnauthorized
		default:
			out.Code = changeErrorResourceUnavailable
		}
		return out
	}

	if isDAGNotFound(err) {
		out.Code = changeErrorResourceUnavailable
		out.Message = "The requested DAG resource is unavailable."
		return out
	}

	return out
}

func changeErrorResult(err *changeToolError) *mcpsdk.CallToolResult {
	output := map[string]any{
		"code":    err.Code,
		"message": err.Message,
	}
	if err.Mode != "" {
		output["mode"] = err.Mode
	}
	if err.Type != "" {
		output["type"] = err.Type
	}
	if err.DAGName != "" {
		output["dagName"] = err.DAGName
	}
	if err.Field != "" {
		output["field"] = err.Field
	}
	if err.DAGURI != "" {
		output["dagUri"] = err.DAGURI
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
