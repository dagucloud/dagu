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

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	changeModePreview = "preview"
	changeModeApply   = "apply"

	changeTypeUpsertDAG      = "upsert_dag"
	changeTypeRenameDAG      = "rename_dag"
	changeTypeDeleteDAG      = "delete_dag"
	changeTypeUpsertWikiPage = "upsert_wiki_page"
	changeTypeRenameWikiPage = "rename_wiki_page"
	changeTypeDeleteWikiPage = "delete_wiki_page"

	legacyChangeTypeUpsertDoc = "upsert_doc"
	legacyChangeTypeRenameDoc = "rename_doc"
	legacyChangeTypeDeleteDoc = "delete_doc"

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
	changeFieldNewName               = "newName"
	changeFieldSpec                  = "spec"
	changeFieldWorkspace             = "workspace"
	changeFieldPath                  = "path"
	changeFieldContent               = "content"
	changeFieldNewPath               = "newPath"
)

type changeToolError struct {
	Code         string
	Message      string
	Mode         string
	Type         string
	DAGName      string
	Workspace    string
	WikiPagePath string
	Field        string
	DAGURI       string
	WikiPageURI  string
	Details      map[string]any
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
				"enum": ["preview", "apply"],
				"description": "Change execution mode. Defaults to preview."
			},
			"type": {
				"type": "string",
				"enum": ["upsert_dag", "rename_dag", "delete_dag", "upsert_wiki_page", "rename_wiki_page", "delete_wiki_page"],
				"description": "Change type. Defaults to upsert_dag."
			},
			"name": {
				"type": "string",
				"description": "Target DAG name."
			},
			"spec": {
				"type": "string",
				"description": "DAG YAML document to validate and optionally store."
			},
			"newName": {
				"type": "string",
				"description": "Destination DAG name for rename_dag."
			},
			"workspace": {
				"type": "string",
				"description": "Wiki workspace: default or a named workspace. Required for Wiki changes."
			},
			"path": {
				"type": "string",
				"description": "Wiki page or directory path without .md. Required for Wiki changes."
			},
			"content": {
				"type": "string",
				"description": "Full Markdown content for upsert_wiki_page. Empty content is allowed."
			},
			"newPath": {
				"type": "string",
				"description": "Destination Wiki page or directory path for rename_wiki_page."
			}
		},
		"oneOf": [
			{
				"properties": {"type": {"enum": ["upsert_dag"]}},
				"required": ["name", "spec"]
			},
			{
				"properties": {"type": {"enum": ["rename_dag"]}},
				"required": ["type", "name", "newName"]
			},
			{
				"properties": {"type": {"enum": ["delete_dag"]}},
				"required": ["type", "name"]
			},
			{
				"properties": {"type": {"enum": ["upsert_wiki_page"]}},
				"required": ["type", "workspace", "path", "content"]
			},
			{
				"properties": {"type": {"enum": ["rename_wiki_page"]}},
				"required": ["type", "workspace", "path", "newPath"]
			},
			{
				"properties": {"type": {"enum": ["delete_wiki_page"]}},
				"required": ["type", "workspace", "path"]
			}
		],
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
		changeErr := classifyChangeToolError(input, err)
		if changeErr.Details == nil && isDAGNotFound(err) {
			changeErr.Details = svc.didYouMeanDetails(ctx, input.Name)
		}
		return changeErrorResult(changeErr), nil
	}
	result.StructuredContent = output
	return result, nil
}

func (svc *Service) changeToolImpl(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.requireAPI(); err != nil {
		return nil, nil, err
	}
	switch canonicalChangeType(input.Type) {
	case changeTypeUpsertDAG:
		return svc.changeDAG(ctx, input)
	case changeTypeRenameDAG:
		return svc.changeRenameDAG(ctx, input)
	case changeTypeDeleteDAG:
		return svc.changeDeleteDAG(ctx, input)
	case changeTypeUpsertWikiPage:
		return svc.changeUpsertWikiPage(ctx, input)
	case changeTypeRenameWikiPage:
		return svc.changeRenameWikiPage(ctx, input)
	case changeTypeDeleteWikiPage:
		return svc.changeDeleteWikiPage(ctx, input)
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

func (svc *Service) changeRenameDAG(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if _, err := svc.getDAGSpec(ctx, input.Name); err != nil {
		return nil, nil, err
	}
	if _, err := svc.getDAGSpec(ctx, input.NewName); err == nil {
		return nil, nil, persis.ErrDAGAlreadyExists
	} else if !isDAGNotFound(err) {
		return nil, nil, err
	}

	output := map[string]any{
		"mode":       input.Mode,
		"type":       input.Type,
		"dagName":    input.Name,
		"newDagName": input.NewName,
		"valid":      true,
		"applied":    false,
		"references": defaultReferenceURIs(),
		"dagUri":     dagSpecURI(input.Name),
		"newDagUri":  dagSpecURI(input.NewName),
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("DAG rename is valid. Re-run with mode=apply to rename it.", linkForDAGSpec(input.Name)), output, nil
	}
	if err := svc.renameDAG(ctx, input.Name, input.NewName); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	delete(output, "dagUri")
	return resultWithLinks("DAG rename applied.", linkForDAGSpec(input.NewName)), output, nil
}

func (svc *Service) changeDeleteDAG(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if _, err := svc.getDAGSpec(ctx, input.Name); err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"mode":       input.Mode,
		"type":       input.Type,
		"dagName":    input.Name,
		"valid":      true,
		"applied":    false,
		"references": defaultReferenceURIs(),
		"dagUri":     dagSpecURI(input.Name),
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("DAG deletion is valid. Re-run with mode=apply to delete it.", linkForDAGSpec(input.Name)), output, nil
	}
	if err := svc.deleteDAG(ctx, input.Name); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	delete(output, "dagUri")
	return resultWithLinks("DAG deletion applied."), output, nil
}

func (svc *Service) changeUpsertWikiPage(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	nodes, err := svc.wikiPageNodes(ctx, input.Workspace)
	if err != nil {
		return nil, nil, err
	}
	created := false
	node, err := inspectWikiPagePath(nodes, input.Path)
	if err != nil {
		if !errors.Is(err, errWikiPagePathNotFound) {
			return nil, nil, err
		}
		if err := ensureWikiPagePathAvailable(nodes, input.Path); err != nil {
			return nil, nil, err
		}
		created = true
	} else if node.Type != "file" {
		return nil, nil, wikiPagePathConflict("Wiki page path identifies a directory")
	}

	output := wikiPageChangeOutput(input)
	output["created"] = created
	output["updated"] = !created
	output["contentBytes"] = len(input.Content)
	if input.Mode == changeModePreview {
		return resultWithLinks("Wiki page change is valid. Re-run with mode=apply to write it.", linkForWikiPage(input.Workspace, input.Path)), output, nil
	}

	if created {
		err = svc.createWikiPage(ctx, input.Workspace, input.Path, input.Content)
	} else {
		err = svc.updateWikiPage(ctx, input.Workspace, input.Path, input.Content)
	}
	if err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	return resultWithLinks("Wiki page change applied.", linkForWikiPage(input.Workspace, input.Path)), output, nil
}

func (svc *Service) changeRenameWikiPage(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	nodes, err := svc.wikiPageNodes(ctx, input.Workspace)
	if err != nil {
		return nil, nil, err
	}
	node, err := inspectWikiPagePath(nodes, input.Path)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureWikiPagePathAvailable(nodes, input.NewPath); err != nil {
		return nil, nil, err
	}

	output := wikiPageChangeOutput(input)
	output["nodeType"] = node.Type
	output["newPath"] = input.NewPath
	links := []resourceLink{}
	if node.Type == "file" {
		output["newWikiPageUri"] = wikiPageURI(input.Workspace, input.NewPath)
		output["newDocUri"] = output["newWikiPageUri"]
		links = append(links, linkForWikiPage(input.Workspace, input.NewPath))
	} else {
		delete(output, "wikiPageUri")
		delete(output, "docUri")
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("Wiki page rename is valid. Re-run with mode=apply to write it.", links...), output, nil
	}
	if err := svc.renameWikiPage(ctx, input.Workspace, input.Path, input.NewPath); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	return resultWithLinks("Wiki page rename applied.", links...), output, nil
}

func (svc *Service) changeDeleteWikiPage(ctx context.Context, input changeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	nodes, err := svc.wikiPageNodes(ctx, input.Workspace)
	if err != nil {
		return nil, nil, err
	}
	node, err := inspectWikiPagePath(nodes, input.Path)
	if err != nil {
		return nil, nil, err
	}
	output := wikiPageChangeOutput(input)
	output["nodeType"] = node.Type
	links := []resourceLink{}
	if node.Type == "file" {
		links = append(links, linkForWikiPage(input.Workspace, input.Path))
	} else {
		delete(output, "wikiPageUri")
		delete(output, "docUri")
	}
	if input.Mode == changeModePreview {
		return resultWithLinks("Wiki page deletion is valid. Re-run with mode=apply to delete it.", links...), output, nil
	}
	if err := svc.deleteWikiPage(ctx, input.Workspace, input.Path); err != nil {
		return nil, nil, err
	}
	output["applied"] = true
	delete(output, "wikiPageUri")
	delete(output, "docUri")
	return resultWithLinks("Wiki page deletion applied."), output, nil
}

func wikiPageChangeOutput(input changeInput) map[string]any {
	uri := wikiPageURI(input.Workspace, input.Path)
	return map[string]any{
		"mode":        input.Mode,
		"type":        input.Type,
		"workspace":   input.Workspace,
		"path":        input.Path,
		"wikiPageUri": uri,
		"docUri":      uri,
		"valid":       true,
		"applied":     false,
		"references":  defaultReferenceURIs(),
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
		case changeFieldNewName:
			input.NewName = strings.TrimSpace(text)
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
		changeFieldNewName,
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
	switch canonicalChangeType(changeType) {
	case changeTypeUpsertDAG,
		changeTypeRenameDAG,
		changeTypeDeleteDAG,
		changeTypeUpsertWikiPage,
		changeTypeRenameWikiPage,
		changeTypeDeleteWikiPage:
		return true
	default:
		return false
	}
}

func canonicalChangeType(changeType string) string {
	switch changeType {
	case legacyChangeTypeUpsertDoc:
		return changeTypeUpsertWikiPage
	case legacyChangeTypeRenameDoc:
		return changeTypeRenameWikiPage
	case legacyChangeTypeDeleteDoc:
		return changeTypeDeleteWikiPage
	default:
		return changeType
	}
}

func validateChangeInput(input changeInput, fields map[string]json.RawMessage) *changeToolError {
	allowed := map[string]bool{
		changeFieldMode: true,
		changeFieldType: true,
	}
	required := []string{}
	changeType := canonicalChangeType(input.Type)
	switch changeType {
	case changeTypeUpsertDAG:
		allowed[changeFieldName] = true
		allowed[changeFieldSpec] = true
		required = []string{changeFieldName, changeFieldSpec}
	case changeTypeRenameDAG:
		allowed[changeFieldName] = true
		allowed[changeFieldNewName] = true
		required = []string{changeFieldName, changeFieldNewName}
	case changeTypeDeleteDAG:
		allowed[changeFieldName] = true
		required = []string{changeFieldName}
	case changeTypeUpsertWikiPage:
		allowed[changeFieldWorkspace] = true
		allowed[changeFieldPath] = true
		allowed[changeFieldContent] = true
		required = []string{changeFieldWorkspace, changeFieldPath, changeFieldContent}
	case changeTypeRenameWikiPage:
		allowed[changeFieldWorkspace] = true
		allowed[changeFieldPath] = true
		allowed[changeFieldNewPath] = true
		required = []string{changeFieldWorkspace, changeFieldPath, changeFieldNewPath}
	case changeTypeDeleteWikiPage:
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

	switch changeType {
	case changeTypeUpsertDAG:
		if input.Name == "" {
			return changeInputError(input, "The name field is required.", changeFieldName)
		}
		if strings.TrimSpace(input.Spec) == "" {
			return changeInputError(input, "The spec field is required.", changeFieldSpec)
		}
	case changeTypeRenameDAG, changeTypeDeleteDAG:
		if input.Name == "" {
			return changeInputError(input, "The name field is required.", changeFieldName)
		}
		if err := ir.ValidateDAGName(input.Name); err != nil {
			return changeInputError(input, "Invalid DAG name: "+err.Error(), changeFieldName)
		}
		if changeType == changeTypeRenameDAG {
			if input.NewName == "" {
				return changeInputError(input, "The newName field is required.", changeFieldNewName)
			}
			if err := ir.ValidateDAGName(input.NewName); err != nil {
				return changeInputError(input, "Invalid destination DAG name: "+err.Error(), changeFieldNewName)
			}
			if input.Name == input.NewName {
				return changeInputError(input, "The newName field must differ from name.", changeFieldNewName)
			}
		}
	default:
		if input.Workspace == "" {
			return changeInputError(input, "The workspace field is required.", changeFieldWorkspace)
		}
		if input.Workspace == "all" {
			return changeInputError(input, "Wiki changes require default or one named workspace.", changeFieldWorkspace)
		}
		if err := wiki.ValidatePageID(input.Path); err != nil {
			return changeInputError(input, "Invalid Wiki page path: "+err.Error(), changeFieldPath)
		}
		if changeType == changeTypeRenameWikiPage {
			if err := wiki.ValidatePageID(input.NewPath); err != nil {
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
		err.WikiPagePath = input.Path
		if input.Workspace != "" && input.Workspace != "all" {
			err.WikiPageURI = wikiPageURI(input.Workspace, input.Path)
		}
	}
	return err
}

func classifyChangeToolError(input changeInput, err error) *changeToolError {
	out := &changeToolError{
		Code:         changeErrorInternal,
		Message:      "Internal MCP change error.",
		Mode:         input.Mode,
		Type:         input.Type,
		DAGName:      input.Name,
		Workspace:    input.Workspace,
		WikiPagePath: input.Path,
	}
	if input.Name != "" {
		out.DAGURI = dagSpecURI(input.Name)
	}
	if input.Path != "" && input.Workspace != "" && input.Workspace != "all" {
		out.WikiPageURI = wikiPageURI(input.Workspace, input.Path)
	}

	if apiErr, ok := errors.AsType[*frontendapi.Error](err); ok {
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
		out.Code = changeErrorResourceNotFound
		out.Message = "The requested DAG was not found."
		return out
	}
	if errors.Is(err, persis.ErrDAGAlreadyExists) {
		out.Code = changeErrorConflict
		out.Message = "A DAG with the requested name already exists."
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
	if err.WikiPagePath != "" {
		output["path"] = err.WikiPagePath
	}
	if err.WikiPageURI != "" {
		output["wikiPageUri"] = err.WikiPageURI
		output["docUri"] = err.WikiPageURI
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
