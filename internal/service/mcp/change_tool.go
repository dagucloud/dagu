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

	"github.com/dagucloud/dagu/v2/internal/core/docs"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	changeModePreview = "preview"
	changeModeApply   = "apply"

	changeTypeUpsertDAG = "upsert_dag"
	changeTypeUpsertDoc = "upsert_doc"
	changeTypeRenameDoc = "rename_doc"
	changeTypeDeleteDoc = "delete_doc"

	changeErrorUnauthenticated       = "unauthenticated"
	changeErrorUnauthorized          = "unauthorized"
	changeErrorInvalidToolInput      = "invalid_tool_input"
	changeErrorUnsupportedChangeMode = "unsupported_change_mode"
	changeErrorUnsupportedChangeType = "unsupported_change_type"
	changeErrorResourceNotFound      = "resource_not_found"
	changeErrorConflict              = "conflict"
	changeErrorResourceUnavailable   = "resource_unavailable"
	changeErrorInternal              = "internal_error"
	changeFieldMode                  = "mode"
	changeFieldType                  = "type"
	changeFieldName                  = "name"
	changeFieldSpec                  = "spec"
	changeFieldWorkspace             = "workspace"
	changeFieldPath                  = "path"
	changeFieldContent               = "content"
	changeFieldNewPath               = "newPath"
)

type changeToolError struct {
	Code      string
	Message   string
	Mode      string
	Type      string
	DAGName   string
	Workspace string
	DocPath   string
	Field     string
	DAGURI    string
	DocURI    string
	Details   map[string]any
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
				"description": "Change type: upsert_dag, upsert_doc, rename_doc, or delete_doc."
			},
			"name": {
				"type": "string",
				"description": "Target DAG name."
			},
			"spec": {
				"type": "string",
				"description": "DAG YAML document to validate and optionally store."
			},
			"workspace": {
				"type": "string",
				"description": "Document workspace: default or a named workspace. Required for document changes."
			},
			"path": {
				"type": "string",
				"description": "Document or directory path without .md. Required for document changes."
			},
			"content": {
				"type": "string",
				"description": "Full Markdown content for upsert_doc. Empty content is allowed."
			},
			"newPath": {
				"type": "string",
				"description": "Destination document or directory path for rename_doc."
			}
		},
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
	switch input.Type {
	case changeTypeUpsertDAG:
		return svc.changeDAG(ctx, input)
	case changeTypeUpsertDoc:
		return svc.changeUpsertDoc(ctx, input)
	case changeTypeRenameDoc:
		return svc.changeRenameDoc(ctx, input)
	case changeTypeDeleteDoc:
		return svc.changeDeleteDoc(ctx, input)
	default:
		return nil, nil, errors.New("unsupported change type")
	}
}

func (svc *Service) changeDAG(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
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

func (svc *Service) changeUpsertDoc(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	created := false
	node, err := svc.inspectDocPath(ctx, input.Workspace, input.Path)
	if err != nil {
		if !errors.Is(err, errDocPathNotFound) {
			return nil, nil, err
		}
		if err := svc.ensureDocPathAvailable(ctx, input.Workspace, input.Path); err != nil {
			return nil, nil, err
		}
		created = true
	} else if node.Type != "file" {
		return nil, nil, docPathConflict("Document path identifies a directory")
	}

	output := docChangeOutput(input)
	output["created"] = created
	output["updated"] = !created
	output["contentBytes"] = len(input.Content)
	if input.Mode == changeModePreview {
		return resultWithLinks("Document change is valid. Re-run with mode=apply to write it.", linkForDoc(input.Workspace, input.Path)), output, nil
	}

	if created {
		err = svc.createDoc(ctx, input.Workspace, input.Path, input.Content)
	} else {
		err = svc.updateDoc(ctx, input.Workspace, input.Path, input.Content)
	}
	if err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	return resultWithLinks("Document change applied.", linkForDoc(input.Workspace, input.Path)), output, nil
}

func (svc *Service) changeRenameDoc(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	node, err := svc.inspectDocPath(ctx, input.Workspace, input.Path)
	if err != nil {
		return nil, nil, err
	}
	if err := svc.ensureDocPathAvailable(ctx, input.Workspace, input.NewPath); err != nil {
		return nil, nil, err
	}

	output := docChangeOutput(input)
	output["nodeType"] = node.Type
	output["newPath"] = input.NewPath
	links := []resourceLink{}
	if node.Type == "file" {
		output["newDocUri"] = docURI(input.Workspace, input.NewPath)
		links = append(links, linkForDoc(input.Workspace, input.NewPath))
	} else {
		delete(output, "docUri")
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("Document rename is valid. Re-run with mode=apply to write it.", links...), output, nil
	}
	if err := svc.renameDoc(ctx, input.Workspace, input.Path, input.NewPath); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	return resultWithLinks("Document rename applied.", links...), output, nil
}

func (svc *Service) changeDeleteDoc(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	node, err := svc.inspectDocPath(ctx, input.Workspace, input.Path)
	if err != nil {
		return nil, nil, err
	}
	output := docChangeOutput(input)
	output["nodeType"] = node.Type
	links := []resourceLink{}
	if node.Type == "file" {
		links = append(links, linkForDoc(input.Workspace, input.Path))
	} else {
		delete(output, "docUri")
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("Document deletion is valid. Re-run with mode=apply to delete it.", links...), output, nil
	}
	if err := svc.deleteDoc(ctx, input.Workspace, input.Path); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	return resultWithLinks("Document deletion applied."), output, nil
}

func docChangeOutput(input changeInput) map[string]any {
	return map[string]any{
		"mode":       input.Mode,
		"type":       input.Type,
		"workspace":  input.Workspace,
		"path":       input.Path,
		"docUri":     docURI(input.Workspace, input.Path),
		"valid":      true,
		"applied":    false,
		"references": defaultReferenceURIs(),
	}
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
		case changeFieldWorkspace:
			input.Workspace = strings.TrimSpace(text)
		case changeFieldPath:
			input.Path = strings.TrimSpace(text)
		case changeFieldContent:
			input.Content = text
		case changeFieldNewPath:
			input.NewPath = strings.TrimSpace(text)
		}
	}

	if input.Mode == "" {
		input.Mode = changeModePreview
	}
	if input.Type == "" {
		input.Type = changeTypeUpsertDAG
	}
	if input.Mode != changeModePreview && input.Mode != changeModeApply {
		err := changeInputError(input, "Unsupported change mode.", changeFieldMode)
		err.Code = changeErrorUnsupportedChangeMode
		return input, err
	}
	if !isSupportedChangeType(input.Type) {
		err := changeInputError(input, "Unsupported change type.", changeFieldType)
		err.Code = changeErrorUnsupportedChangeType
		return input, err
	}
	if err := validateChangeInput(input, fields); err != nil {
		return input, err
	}

	return input, nil
}

func isChangeInputField(field string) bool {
	switch field {
	case changeFieldMode,
		changeFieldType,
		changeFieldName,
		changeFieldSpec,
		changeFieldWorkspace,
		changeFieldPath,
		changeFieldContent,
		changeFieldNewPath:
		return true
	default:
		return false
	}
}

func isSupportedChangeType(changeType string) bool {
	switch changeType {
	case changeTypeUpsertDAG, changeTypeUpsertDoc, changeTypeRenameDoc, changeTypeDeleteDoc:
		return true
	default:
		return false
	}
}

func validateChangeInput(input changeInput, fields map[string]json.RawMessage) *changeToolError {
	allowed := map[string]bool{
		changeFieldMode: true,
		changeFieldType: true,
	}
	required := []string{}
	switch input.Type {
	case changeTypeUpsertDAG:
		allowed[changeFieldName] = true
		allowed[changeFieldSpec] = true
		required = []string{changeFieldName, changeFieldSpec}
	case changeTypeUpsertDoc:
		allowed[changeFieldWorkspace] = true
		allowed[changeFieldPath] = true
		allowed[changeFieldContent] = true
		required = []string{changeFieldWorkspace, changeFieldPath, changeFieldContent}
	case changeTypeRenameDoc:
		allowed[changeFieldWorkspace] = true
		allowed[changeFieldPath] = true
		allowed[changeFieldNewPath] = true
		required = []string{changeFieldWorkspace, changeFieldPath, changeFieldNewPath}
	case changeTypeDeleteDoc:
		allowed[changeFieldWorkspace] = true
		allowed[changeFieldPath] = true
		required = []string{changeFieldWorkspace, changeFieldPath}
	}

	keys := make([]string, 0, len(fields))
	for field, raw := range fields {
		if string(raw) != "null" && !allowed[field] {
			keys = append(keys, field)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return changeInputError(input, "The "+keys[0]+" field is not allowed for change type "+input.Type+".", keys[0])
	}

	for _, field := range required {
		raw, ok := fields[field]
		if !ok || string(raw) == "null" {
			return changeInputError(input, "The "+field+" field is required.", field)
		}
	}

	switch input.Type {
	case changeTypeUpsertDAG:
		if input.Name == "" {
			return changeInputError(input, "The name field is required.", changeFieldName)
		}
		if strings.TrimSpace(input.Spec) == "" {
			return changeInputError(input, "The spec field is required.", changeFieldSpec)
		}
	default:
		if input.Workspace == "" {
			return changeInputError(input, "The workspace field is required.", changeFieldWorkspace)
		}
		if input.Workspace == "all" {
			return changeInputError(input, "Document changes require default or one named workspace.", changeFieldWorkspace)
		}
		if err := docs.ValidateDocID(input.Path); err != nil {
			return changeInputError(input, "Invalid document path: "+err.Error(), changeFieldPath)
		}
		if input.Type == changeTypeRenameDoc {
			if err := docs.ValidateDocID(input.NewPath); err != nil {
				return changeInputError(input, "Invalid destination path: "+err.Error(), changeFieldNewPath)
			}
			if input.Path == input.NewPath {
				return changeInputError(input, "The newPath field must differ from path.", changeFieldNewPath)
			}
			if strings.HasPrefix(input.NewPath, input.Path+"/") {
				return changeInputError(input, "The newPath field must not be inside path.", changeFieldNewPath)
			}
		}
	}
	return nil
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
	if input.Workspace != "" {
		err.Workspace = input.Workspace
	}
	if input.Path != "" {
		err.DocPath = input.Path
		if input.Workspace != "" && input.Workspace != "all" {
			err.DocURI = docURI(input.Workspace, input.Path)
		}
	}
	return err
}

func classifyChangeToolError(input changeInput, err error) *changeToolError {
	out := &changeToolError{
		Code:      changeErrorInternal,
		Message:   "Internal MCP change error.",
		Mode:      input.Mode,
		Type:      input.Type,
		DAGName:   input.Name,
		Workspace: input.Workspace,
		DocPath:   input.Path,
	}
	if input.Name != "" {
		out.DAGURI = dagSpecURI(input.Name)
	}
	if input.Path != "" && input.Workspace != "" && input.Workspace != "all" {
		out.DocURI = docURI(input.Workspace, input.Path)
	}

	var apiErr *frontendapi.Error
	if errors.As(err, &apiErr) {
		out.Message = apiErr.Message
		switch apiErr.HTTPStatus {
		case http.StatusUnauthorized:
			out.Code = changeErrorUnauthenticated
		case http.StatusForbidden:
			out.Code = changeErrorUnauthorized
		case http.StatusBadRequest:
			out.Code = changeErrorInvalidToolInput
		case http.StatusNotFound:
			out.Code = changeErrorResourceNotFound
		case http.StatusConflict:
			out.Code = changeErrorConflict
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
	if err.Workspace != "" {
		output["workspace"] = err.Workspace
	}
	if err.DocPath != "" {
		output["path"] = err.DocPath
	}
	if err.DocURI != "" {
		output["docUri"] = err.DocURI
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
