// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	readTargetReferences = "references"
	readTargetReference  = "reference"
	readTargetDAGs       = "dags"
	readTargetDAG        = "dag"
	readTargetDAGSpec    = "dag_spec"
	readTargetWiki       = "wiki"
	readTargetWikiPage   = "wiki_page"
	readTargetWikiSearch = "wiki_search"

	legacyReadTargetDocs      = "docs"
	legacyReadTargetDoc       = "doc"
	legacyReadTargetDocSearch = "doc_search"
	readTargetRuns            = "runs"
	readTargetRun             = "run"
	readTargetRunLogs         = "run_logs"
	readTargetStepLog         = "step_log"

	readErrorInvalidToolInput      = "invalid_tool_input"
	readErrorInvalidResourceURI    = "invalid_resource_uri"
	readErrorUnsupportedReadTarget = "unsupported_read_target"
	readErrorUnsupportedResource   = "unsupported_resource"
	readErrorResourceNotFound      = "resource_not_found"
	readErrorResourceUnavailable   = "resource_unavailable"
	readErrorInternal              = "internal_error"

	readFieldTarget    = "target"
	readFieldName      = "name"
	readFieldDAGRunID  = "dagRunId"
	readFieldStepName  = "stepName"
	readFieldQuery     = "query"
	readFieldWorkspace = "workspace"
	readFieldPath      = "path"
	readFieldSearch    = "search"
	readFieldPrefix    = "prefix"
	readFieldCursor    = "cursor"
	readFieldLimit     = "limit"
	readFieldURI       = "uri"

	readResourceScheme = "dagu"

	readResourceReferenceCollectionURI  = "dagu://reference"
	readResourceDAGsCollectionURI       = "dagu://dags"
	readResourceWikiCollectionURI       = "dagu://wiki"
	legacyReadResourceDocsCollectionURI = "dagu://docs"
	readResourceRunsCollectionURI       = "dagu://runs"

	readWikiSearchMaxLimit = 50
)

type readInput struct {
	Target    string `json:"target" jsonschema:"Read target: dags, dag, dag_spec, wiki, wiki_page, wiki_search, runs, run, run_logs, step_log, or reference."`
	Name      string `json:"name,omitempty" jsonschema:"DAG name for dag, dag_spec, run, run_logs, and step_log targets."`
	DAGRunID  string `json:"dagRunId,omitempty" jsonschema:"DAG-run ID for run, run_logs, and step_log targets. The value latest is accepted where Dagu accepts it."`
	StepName  string `json:"stepName,omitempty" jsonschema:"Step name for the step_log target."`
	Query     string `json:"query,omitempty" jsonschema:"URL query string for list targets, for example page=1&perPage=100 or status=running."`
	Workspace string `json:"workspace,omitempty" jsonschema:"Wiki workspace: all, default, or a workspace name. Required for wiki_page and optional for wiki and wiki_search."`
	Path      string `json:"path,omitempty" jsonschema:"Wiki page path without the .md extension. Required for wiki_page."`
	Search    string `json:"search,omitempty" jsonschema:"Wiki search text. Required for wiki_search."`
	Prefix    string `json:"prefix,omitempty" jsonschema:"Wiki page path prefix. Optional for wiki and wiki_search."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by wiki_search."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum wiki_search results to return, from 1 to 50."`
	URI       string `json:"uri,omitempty" jsonschema:"Resource URI to read directly, for example dagu://reference/authoring."`
}

func readToolInputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "Read target: references, reference, dags, dag, dag_spec, wiki, wiki_page, wiki_search, runs, run, run_logs, or step_log. The docs, doc, and doc_search aliases are deprecated."
			},
			"name": {
				"type": "string",
				"description": "DAG name or reference topic name."
			},
			"dagRunId": {
				"type": "string",
				"description": "DAG-run identifier for run, run_logs, and step_log targets."
			},
			"stepName": {
				"type": "string",
				"description": "Step name for the step_log target."
			},
			"query": {
				"type": "string",
				"description": "URL query string without a leading question mark."
			},
			"workspace": {
				"type": "string",
				"description": "Wiki workspace: all, default, or a workspace name. Required for wiki_page and optional for wiki and wiki_search."
			},
			"path": {
				"type": "string",
				"description": "Wiki page path without the .md extension. Required for wiki_page."
			},
			"search": {
				"type": "string",
				"description": "Wiki search text. Required for wiki_search."
			},
			"prefix": {
				"type": "string",
				"description": "Wiki page path prefix. Optional for wiki and wiki_search."
			},
			"cursor": {
				"type": "string",
				"description": "Opaque cursor returned by wiki_search."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50,
				"description": "Maximum number of wiki_search results to return."
			},
			"uri": {
				"type": "string",
				"description": "dagu:// resource URI to read directly."
			}
		},
		"additionalProperties": false
	}`)
}

type readToolError struct {
	Code    string
	Message string
	Target  string
	Field   string
	URI     string
	Details map[string]any
}

func (e *readToolError) Error() string {
	return e.Message
}

type readResourcePath struct {
	rawURI   string
	host     string
	segments []string
	query    string
}

func (svc *Service) readTool(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	input, readErr := parseReadToolInput(req.Params.Arguments)
	if readErr != nil {
		return readErrorResult(readErr), nil
	}

	result, output, err := auditToolCall(ctx, svc.api, req, toolRead, readAuditMetadata(input), func(ctx context.Context) (*mcpsdk.CallToolResult, map[string]any, error) {
		return svc.readToolImpl(ctx, input)
	})
	if err != nil {
		return readErrorResult(classifyReadToolError(input, err)), nil
	}
	result.StructuredContent = output
	return result, nil
}

func (svc *Service) readToolImpl(ctx context.Context, input readInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	var (
		data any
		err  error
	)

	switch input.Target {
	case readTargetReferences:
		data = readReferenceCollection()
	case readTargetReference:
		ref, ok := referenceByTopic(input.Name)
		if !ok {
			return nil, nil, resourceNotFoundReadError(input, "reference topic not found")
		}
		data = map[string]any{"text": ref.text, "mimeType": resourceMIMEText}
	case readTargetDAGs:
		if err = svc.requireAPI(); err == nil {
			var raw any
			raw, err = svc.api.GetDAGsListDataIncludingAltDirs(ctx, input.Query)
			if err == nil {
				data, err = normalizeDAGList(raw)
			}
		}
	case readTargetDAG:
		if err = svc.requireAPI(); err == nil {
			var raw any
			raw, err = svc.api.GetDAGDetailsData(ctx, input.Name)
			if err == nil {
				data, err = normalizeDAGDetails(raw, input.Name)
			}
		}
	case readTargetDAGSpec:
		if err = svc.requireAPI(); err == nil {
			var raw map[string]any
			raw, err = svc.getDAGSpec(ctx, input.Name)
			if err == nil {
				data = normalizeDAGSpec(raw, input.Name)
			}
		}
	case readTargetWiki:
		if err = svc.requireAPI(); err == nil {
			data, err = svc.listWikiPages(ctx, input.Workspace, input.Query)
		}
	case readTargetWikiPage:
		if err = svc.requireAPI(); err == nil {
			var page daguapi.WikiPageResponse
			page, err = svc.getWikiPage(ctx, input.Workspace, input.Path)
			if err == nil {
				data = normalizeWikiPage(page, input.Workspace)
			}
		}
	case readTargetWikiSearch:
		if err = svc.requireAPI(); err == nil {
			data, err = svc.searchWikiPages(ctx, input.Workspace, input.Search, input.Prefix, input.Cursor, input.Limit)
		}
	case readTargetRuns:
		if err = svc.requireAPI(); err == nil {
			var raw any
			raw, err = svc.api.GetDAGRunsListData(ctx, input.Query)
			if err == nil {
				data, err = normalizeRunList(raw)
			}
		}
	case readTargetRun:
		if err = svc.requireAPI(); err == nil {
			var raw any
			raw, err = svc.api.GetDAGRunDetailsData(ctx, input.Name+"/"+input.DAGRunID)
			if err == nil {
				data, err = normalizeRunDetails(raw, input.Name, input.DAGRunID)
			}
		}
	case readTargetRunLogs:
		if err = svc.requireAPI(); err == nil {
			identifier := input.Name + "/" + input.DAGRunID
			if input.Query != "" {
				identifier += "?" + input.Query
			}
			data, err = svc.api.GetDAGRunLogsData(ctx, identifier)
		}
	case readTargetStepLog:
		if err = svc.requireAPI(); err == nil {
			data, err = svc.api.GetStepLogDataByRef(
				ctx,
				ir.NewDAGRunRef(input.Name, input.DAGRunID),
				input.StepName,
			)
		}
	default:
		return nil, nil, unsupportedReadTargetError(input.Target)
	}
	if err != nil {
		return nil, nil, err
	}

	output := map[string]any{
		"target":     input.Target,
		"data":       data,
		"references": defaultReferenceURIs(),
	}
	if input.Target == readTargetStepLog {
		output["name"] = input.Name
		output["dagRunId"] = input.DAGRunID
		output["stepName"] = input.StepName
	}
	if input.Workspace != "" {
		output["workspace"] = input.Workspace
	}
	if input.Path != "" {
		output["path"] = input.Path
	}
	if input.Prefix != "" {
		output["prefix"] = input.Prefix
	}
	if input.URI != "" {
		output["uri"] = input.URI
	}

	return resultWithLinks(readResultMessage(input, data), readResourceLinks(input.URI)...), output, nil
}

func readResultMessage(input readInput, data any) string {
	if input.Target != readTargetDAGs {
		return "Dagu read completed."
	}

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "Dagu read completed."
	}
	return "Dagu read completed.\n\n" + string(payload)
}

func parseReadToolInput(raw json.RawMessage) (readInput, *readToolError) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return readInput{}, invalidToolInput("Tool input must be a JSON object.", "")
	}

	values := make(map[string]string, len(fields))
	limit := 0
	emptyTarget := false
	for field, value := range fields {
		if !isReadInputField(field) {
			return readInput{}, &readToolError{
				Code:    readErrorInvalidToolInput,
				Message: "Unknown field " + field + ".",
				Field:   field,
			}
		}

		if string(value) == "null" {
			continue
		}
		if field == readFieldLimit {
			if err := json.Unmarshal(value, &limit); err != nil {
				return readInput{}, invalidToolInput("Field limit must be an integer.", readFieldLimit)
			}
			if limit < 1 || limit > readWikiSearchMaxLimit {
				return readInput{}, invalidToolInput("Field limit must be between 1 and 50.", readFieldLimit)
			}
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return readInput{}, invalidToolInput("Field "+field+" must be a string.", field)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			if field == readFieldTarget {
				emptyTarget = true
			}
			continue
		}
		values[field] = text
	}

	if values[readFieldURI] != "" {
		var mixed []string
		for _, field := range []string{
			readFieldTarget,
			readFieldName,
			readFieldDAGRunID,
			readFieldStepName,
			readFieldQuery,
			readFieldWorkspace,
			readFieldPath,
			readFieldSearch,
			readFieldPrefix,
			readFieldCursor,
		} {
			if values[field] != "" {
				mixed = append(mixed, field)
			}
		}
		if len(mixed) > 0 {
			readErr := &readToolError{
				Code:    readErrorInvalidToolInput,
				Message: "URI mode cannot be combined with target-mode fields.",
				Details: map[string]any{
					"fields": mixed,
				},
			}
			if len(mixed) == 1 {
				readErr.Field = mixed[0]
			}
			return readInput{}, readErr
		}
		if limit != 0 {
			return readInput{}, invalidToolInput("URI mode cannot be combined with target-mode fields.", readFieldLimit)
		}
		return parseReadResourceURI(values[readFieldURI])
	}

	target := canonicalReadTarget(values[readFieldTarget])
	if target == "" {
		if emptyTarget {
			return readInput{}, invalidToolInput("The target field is required for target mode.", readFieldTarget)
		}
		return readInput{}, invalidToolInput("Either target or uri is required.", "")
	}

	input := readInput{
		Target:    target,
		Name:      values[readFieldName],
		DAGRunID:  values[readFieldDAGRunID],
		StepName:  values[readFieldStepName],
		Query:     values[readFieldQuery],
		Workspace: values[readFieldWorkspace],
		Path:      values[readFieldPath],
		Search:    values[readFieldSearch],
		Prefix:    values[readFieldPrefix],
		Cursor:    values[readFieldCursor],
		Limit:     limit,
	}
	if err := validateTargetReadInput(&input); err != nil {
		return readInput{}, err
	}
	return input, nil
}

func isReadInputField(field string) bool {
	switch field {
	case readFieldTarget,
		readFieldName,
		readFieldDAGRunID,
		readFieldStepName,
		readFieldQuery,
		readFieldWorkspace,
		readFieldPath,
		readFieldSearch,
		readFieldPrefix,
		readFieldCursor,
		readFieldLimit,
		readFieldURI:
		return true
	default:
		return false
	}
}

func canonicalReadTarget(target string) string {
	switch target {
	case legacyReadTargetDocs:
		return readTargetWiki
	case legacyReadTargetDoc:
		return readTargetWikiPage
	case legacyReadTargetDocSearch:
		return readTargetWikiSearch
	default:
		return target
	}
}

func parseReadResourceURI(rawURI string) (readInput, *readToolError) {
	resource, readErr := parseReadResourcePath(rawURI)
	if readErr != nil {
		return readInput{}, readErr
	}

	switch resource.host {
	case readTargetReference:
		if resource.query != "" {
			return readInput{}, invalidResourceURI(rawURI, "Reference resources do not support query parameters.")
		}
		switch len(resource.segments) {
		case 0:
			return readInput{Target: readTargetReferences, URI: readResourceReferenceCollectionURI}, nil
		case 1:
			return readInput{
				Target: readTargetReference,
				Name:   resource.segments[0],
				URI:    readReferenceURI(resource.segments[0]),
			}, nil
		default:
			return readInput{}, invalidResourceURI(rawURI, "Unsupported reference resource path.")
		}
	case readTargetDAGs:
		switch {
		case len(resource.segments) == 0:
			if err := validateReadQuery(readTargetDAGs, resource.query, true, rawURI); err != nil {
				return readInput{}, err
			}
			return readInput{
				Target: readTargetDAGs,
				Query:  resource.query,
				URI:    uriWithQuery(readResourceDAGsCollectionURI, resource.query),
			}, nil
		case len(resource.segments) == 2 && resource.segments[1] == "spec":
			if resource.query != "" {
				return readInput{}, invalidResourceURI(rawURI, "DAG spec resources do not support query parameters.")
			}
			return readInput{
				Target: readTargetDAGSpec,
				Name:   resource.segments[0],
				URI:    dagSpecURI(resource.segments[0]),
			}, nil
		default:
			return readInput{}, invalidResourceURI(rawURI, "Unsupported DAG resource path.")
		}
	case readTargetWiki, legacyReadTargetDocs:
		switch len(resource.segments) {
		case 0:
			if err := validateReadQuery(readTargetWiki, resource.query, true, rawURI); err != nil {
				return readInput{}, err
			}
			return readInput{
				Target:    readTargetWiki,
				Workspace: "all",
				Query:     resource.query,
				URI:       uriWithQuery(readResourceWikiCollectionURI, resource.query),
			}, nil
		case 1:
			if resource.segments[0] == "all" {
				return readInput{}, invalidResourceURI(rawURI, "Use dagu://wiki for the all-workspaces collection.")
			}
			if err := validateReadQuery(readTargetWiki, resource.query, true, rawURI); err != nil {
				return readInput{}, err
			}
			return readInput{
				Target:    readTargetWiki,
				Workspace: resource.segments[0],
				Query:     resource.query,
				URI:       uriWithQuery(wikiCollectionURI(resource.segments[0]), resource.query),
			}, nil
		case 2:
			if resource.query != "" {
				return readInput{}, invalidResourceURI(rawURI, "Wiki page resources do not support query parameters.")
			}
			if resource.segments[0] == "all" {
				return readInput{}, invalidResourceURI(rawURI, "A Wiki page resource requires a concrete workspace.")
			}
			input := readInput{
				Target:    readTargetWikiPage,
				Workspace: resource.segments[0],
				Path:      resource.segments[1],
				URI:       wikiPageURI(resource.segments[0], resource.segments[1]),
			}
			if err := validateTargetReadInput(&input); err != nil {
				err.Code = readErrorInvalidResourceURI
				err.URI = rawURI
				err.Field = ""
				return readInput{}, err
			}
			return input, nil
		default:
			return readInput{}, invalidResourceURI(rawURI, "Unsupported Wiki resource path.")
		}
	case readTargetRuns:
		switch {
		case len(resource.segments) == 0:
			if err := validateReadQuery(readTargetRuns, resource.query, true, rawURI); err != nil {
				return readInput{}, err
			}
			return readInput{
				Target: readTargetRuns,
				Query:  resource.query,
				URI:    uriWithQuery(readResourceRunsCollectionURI, resource.query),
			}, nil
		case len(resource.segments) == 2:
			if resource.query != "" {
				return readInput{}, invalidResourceURI(rawURI, "DAG-run resources do not support query parameters.")
			}
			return readInput{
				Target:   readTargetRun,
				Name:     resource.segments[0],
				DAGRunID: resource.segments[1],
				URI:      runURI(resource.segments[0], resource.segments[1]),
			}, nil
		case len(resource.segments) == 3 && resource.segments[2] == "logs":
			if err := validateReadQuery(readTargetRunLogs, resource.query, true, rawURI); err != nil {
				return readInput{}, err
			}
			return readInput{
				Target:   readTargetRunLogs,
				Name:     resource.segments[0],
				DAGRunID: resource.segments[1],
				Query:    resource.query,
				URI:      runLogsURIWithQuery(resource.segments[0], resource.segments[1], resource.query),
			}, nil
		case len(resource.segments) == 5 && resource.segments[2] == "steps" && resource.segments[4] == "logs":
			if resource.query != "" {
				return readInput{}, invalidResourceURI(rawURI, "Step log resources do not support query parameters.")
			}
			return readInput{
				Target:   readTargetStepLog,
				Name:     resource.segments[0],
				DAGRunID: resource.segments[1],
				StepName: resource.segments[3],
				URI:      stepLogURI(resource.segments[0], resource.segments[1], resource.segments[3]),
			}, nil
		default:
			return readInput{}, invalidResourceURI(rawURI, "Unsupported DAG-run resource path.")
		}
	default:
		return readInput{}, &readToolError{
			Code:    readErrorUnsupportedResource,
			Message: "Unsupported resource family.",
			URI:     rawURI,
		}
	}
}

func parseReadResourcePath(rawURI string) (readResourcePath, *readToolError) {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != readResourceScheme || parsed.Host == "" {
		return readResourcePath{}, invalidResourceURI(rawURI, "Invalid dagu resource URI.")
	}
	segments, err := uriPathSegments(parsed)
	if err != nil {
		return readResourcePath{}, invalidResourceURI(rawURI, "Invalid dagu resource URI path.")
	}
	return readResourcePath{
		rawURI:   rawURI,
		host:     parsed.Host,
		segments: segments,
		query:    parsed.RawQuery,
	}, nil
}

func validateTargetReadInput(input *readInput) *readToolError {
	if input.StepName != "" && input.Target != readTargetStepLog {
		return invalidTargetField(input.Target, readFieldStepName)
	}
	wikiTarget := input.Target == readTargetWiki || input.Target == readTargetWikiPage || input.Target == readTargetWikiSearch
	if input.Workspace != "" && !wikiTarget {
		return invalidTargetField(input.Target, readFieldWorkspace)
	}
	if input.Path != "" && input.Target != readTargetWikiPage {
		return invalidTargetField(input.Target, readFieldPath)
	}
	if input.Search != "" && input.Target != readTargetWikiSearch {
		return invalidTargetField(input.Target, readFieldSearch)
	}
	if input.Prefix != "" && input.Target != readTargetWiki && input.Target != readTargetWikiSearch {
		return invalidTargetField(input.Target, readFieldPrefix)
	}
	if input.Cursor != "" && input.Target != readTargetWikiSearch {
		return invalidTargetField(input.Target, readFieldCursor)
	}
	if input.Limit != 0 && input.Target != readTargetWikiSearch {
		return invalidTargetField(input.Target, readFieldLimit)
	}
	if input.Prefix != "" {
		if err := wiki.ValidatePageID(input.Prefix); err != nil {
			return invalidTargetValue(input.Target, readFieldPrefix, err.Error())
		}
	}
	switch input.Target {
	case readTargetReferences:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
	case readTargetReference:
		if input.Name == "" {
			input.Name = "authoring"
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		input.URI = readReferenceURI(input.Name)
	case readTargetDAGs:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if err := validateReadQuery(input.Target, input.Query, false, ""); err != nil {
			return err
		}
	case readTargetDAG:
		if input.Name == "" {
			return missingTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
	case readTargetDAGSpec:
		if input.Name == "" {
			return missingTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		input.URI = dagSpecURI(input.Name)
	case readTargetWiki:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Workspace == "" {
			input.Workspace = "all"
		}
		if input.Prefix != "" {
			values, err := url.ParseQuery(input.Query)
			if err != nil {
				return readQueryError(input.Target, false, "", "Query contains malformed URL encoding.")
			}
			if values.Has(readFieldPrefix) {
				return invalidTargetValue(input.Target, readFieldPrefix, "The prefix field and query parameter cannot both be set.")
			}
			values.Set(readFieldPrefix, input.Prefix)
			input.Query = values.Encode()
		}
		if err := validateReadQuery(input.Target, input.Query, false, ""); err != nil {
			return err
		}
		input.URI = uriWithQuery(wikiCollectionURI(input.Workspace), input.Query)
	case readTargetWikiPage:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		if input.Workspace == "" {
			return missingTargetField(input.Target, readFieldWorkspace)
		}
		if input.Workspace == "all" {
			return invalidTargetValue(input.Target, readFieldWorkspace, "The workspace field must identify default or one named workspace for target wiki_page.")
		}
		if input.Path == "" {
			return missingTargetField(input.Target, readFieldPath)
		}
		if err := wiki.ValidatePageID(input.Path); err != nil {
			return invalidTargetValue(input.Target, readFieldPath, err.Error())
		}
		input.URI = wikiPageURI(input.Workspace, input.Path)
	case readTargetWikiSearch:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		if input.Workspace == "" {
			input.Workspace = "all"
		}
		if input.Search == "" {
			return missingTargetField(input.Target, readFieldSearch)
		}
	case readTargetRuns:
		if input.Name != "" {
			return invalidTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID != "" {
			return invalidTargetField(input.Target, readFieldDAGRunID)
		}
		if err := validateReadQuery(input.Target, input.Query, false, ""); err != nil {
			return err
		}
	case readTargetRun:
		if input.Name == "" {
			return missingTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID == "" {
			return missingTargetField(input.Target, readFieldDAGRunID)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		input.URI = runURI(input.Name, input.DAGRunID)
	case readTargetRunLogs:
		if input.Name == "" {
			return missingTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID == "" {
			return missingTargetField(input.Target, readFieldDAGRunID)
		}
		if err := validateReadQuery(input.Target, input.Query, false, ""); err != nil {
			return err
		}
		input.URI = runLogsURIWithQuery(input.Name, input.DAGRunID, input.Query)
	case readTargetStepLog:
		if input.Name == "" {
			return missingTargetField(input.Target, readFieldName)
		}
		if input.DAGRunID == "" {
			return missingTargetField(input.Target, readFieldDAGRunID)
		}
		if input.StepName == "" {
			return missingTargetField(input.Target, readFieldStepName)
		}
		if input.Query != "" {
			return invalidTargetField(input.Target, readFieldQuery)
		}
		input.URI = stepLogURI(input.Name, input.DAGRunID, input.StepName)
	default:
		return unsupportedReadTargetError(input.Target)
	}
	return nil
}

func validateReadQuery(target, rawQuery string, uriMode bool, rawURI string) *readToolError {
	rawQuery = strings.TrimSpace(rawQuery)
	if rawQuery == "" {
		return nil
	}
	if strings.HasPrefix(rawQuery, "?") {
		return readQueryError(target, uriMode, rawURI, "Query must not start with '?'.")
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return readQueryError(target, uriMode, rawURI, "Query contains malformed URL encoding.")
	}

	for key, rawValues := range values {
		if !isAllowedReadQueryParam(target, key) {
			return readQueryError(target, uriMode, rawURI, "Unsupported query parameter.")
		}
		if len(rawValues) > 1 && (target != readTargetRuns || key != "status") {
			return readQueryError(target, uriMode, rawURI, "Query parameter must not be repeated.")
		}
		for _, rawValue := range rawValues {
			value := strings.TrimSpace(rawValue)
			if value == "" {
				return readQueryError(target, uriMode, rawURI, "Query parameter value must not be empty.")
			}
			if !validReadQueryValue(target, key, value) {
				return readQueryError(target, uriMode, rawURI, "Query parameter value is outside the allowed range.")
			}
		}
	}
	return nil
}

func isAllowedReadQueryParam(target, key string) bool {
	switch target {
	case readTargetDAGs:
		switch key {
		case "page", "perPage", "name", "labels", "active", "sort", "order":
			return true
		}
	case readTargetWiki:
		switch key {
		case "page", "perPage", "flat", "sort", "order", "prefix":
			return true
		}
	case readTargetRuns:
		switch key {
		case "name", "dagRunId", "status", "fromDate", "toDate", "limit", "cursor", "labels":
			return true
		}
	case readTargetRunLogs:
		switch key {
		case "tail":
			return true
		}
	}
	return false
}

func validReadQueryValue(target, key, value string) bool {
	switch target {
	case readTargetDAGs:
		switch key {
		case "page":
			return validIntRange(value, 1, 0)
		case "perPage":
			return validIntRange(value, 1, 1000)
		case "name":
			return value != ""
		case "labels":
			return validCommaList(value)
		case "active":
			return value == "true" || value == "false"
		case "sort":
			return value == "name" || value == "nextRun"
		case "order":
			return value == "asc" || value == "desc"
		}
	case readTargetWiki:
		switch key {
		case "page":
			return validIntRange(value, 1, 0)
		case "perPage":
			return validIntRange(value, 1, 200)
		case "flat":
			return value == "true" || value == "false"
		case "sort":
			return value == "name" || value == "type" || value == "mtime"
		case "order":
			return value == "asc" || value == "desc"
		case "prefix":
			return wiki.ValidatePageID(value) == nil
		}
	case readTargetRuns:
		switch key {
		case "name", "dagRunId", "cursor":
			return value != ""
		case "status":
			return validIntRange(value, 0, 8)
		case "fromDate", "toDate":
			_, err := strconv.ParseInt(value, 10, 64)
			return err == nil
		case "limit":
			return validIntRange(value, 1, 500)
		case "labels":
			return validCommaList(value)
		}
	case readTargetRunLogs:
		switch key {
		case "tail":
			return validIntRange(value, 1, 0)
		}
	}
	return false
}

func validIntRange(value string, minValue, maxValue int) bool {
	n, err := strconv.Atoi(value)
	if err != nil {
		return false
	}
	if n < minValue {
		return false
	}
	return maxValue == 0 || n <= maxValue
}

func validCommaList(value string) bool {
	for item := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(item) == "" {
			return false
		}
	}
	return true
}

func readReferenceCollection() map[string]any {
	items := make([]map[string]any, 0, len(referenceResources()))
	for _, ref := range referenceResources() {
		items = append(items, map[string]any{
			"name":     ref.topic,
			"uri":      ref.uri,
			"mimeType": resourceMIMEText,
		})
	}
	return map[string]any{"items": items}
}

func normalizeDAGList(raw any) (map[string]any, error) {
	var dags []daguapi.DAGFile
	switch data := raw.(type) {
	case daguapi.ListDAGs200JSONResponse:
		dags = data.Dags
	case *daguapi.ListDAGs200JSONResponse:
		dags = data.Dags
	default:
		return nil, fmt.Errorf("unexpected DAG list response %T", raw)
	}

	items := make([]map[string]any, 0, len(dags))
	for _, dag := range dags {
		name := dag.FileName
		if name == "" {
			name = dag.Dag.Name
		}
		items = append(items, map[string]any{
			"name": name,
			"uri":  dagSpecURI(name),
		})
	}
	return map[string]any{"items": items}, nil
}

func normalizeDAGDetails(raw any, fallbackName string) (map[string]any, error) {
	var dag *daguapi.DAGDetails
	switch data := raw.(type) {
	case daguapi.GetDAGDetails200JSONResponse:
		dag = data.Dag
	case *daguapi.GetDAGDetails200JSONResponse:
		dag = data.Dag
	default:
		return nil, fmt.Errorf("unexpected DAG details response %T", raw)
	}

	name := fallbackName
	if dag != nil && dag.Name != "" {
		name = dag.Name
	}
	return map[string]any{
		"name":    name,
		"specUri": dagSpecURI(name),
	}, nil
}

func normalizeDAGSpec(raw map[string]any, name string) map[string]any {
	errorsValue := []string{}
	switch values := raw["errors"].(type) {
	case []string:
		errorsValue = append(errorsValue, values...)
	case []any:
		for _, value := range values {
			text, ok := value.(string)
			if ok {
				errorsValue = append(errorsValue, text)
			}
		}
	}
	return map[string]any{
		"name":     name,
		"mimeType": resourceMIMEYAML,
		"spec":     raw["spec"],
		"errors":   errorsValue,
	}
}

func normalizeRunList(raw any) (map[string]any, error) {
	var runs []daguapi.DAGRunSummary
	switch data := raw.(type) {
	case daguapi.DAGRunsPageResponse:
		runs = data.DagRuns
	case *daguapi.DAGRunsPageResponse:
		runs = data.DagRuns
	case daguapi.ListDAGRuns200JSONResponse:
		page := daguapi.DAGRunsPageResponse(data)
		runs = page.DagRuns
	case *daguapi.ListDAGRuns200JSONResponse:
		page := daguapi.DAGRunsPageResponse(*data)
		runs = page.DagRuns
	default:
		return nil, fmt.Errorf("unexpected DAG-run list response %T", raw)
	}

	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		name := string(run.Name)
		dagRunID := string(run.DagRunId)
		items = append(items, map[string]any{
			"name":        name,
			"dagRunId":    dagRunID,
			"uri":         runURI(name, dagRunID),
			"status":      run.Status,
			"statusLabel": run.StatusLabel,
		})
	}
	return map[string]any{"items": items}, nil
}

func normalizeRunDetails(raw any, fallbackName, fallbackDAGRunID string) (map[string]any, error) {
	var run daguapi.DAGRunDetails
	switch data := raw.(type) {
	case daguapi.GetDAGRunDetails200JSONResponse:
		run = data.DagRunDetails
	case *daguapi.GetDAGRunDetails200JSONResponse:
		run = data.DagRunDetails
	default:
		return nil, fmt.Errorf("unexpected DAG-run details response %T", raw)
	}

	name := string(run.Name)
	if name == "" {
		name = fallbackName
	}
	dagRunID := string(run.DagRunId)
	if dagRunID == "" {
		dagRunID = fallbackDAGRunID
	}
	return map[string]any{
		"name":        name,
		"dagRunId":    dagRunID,
		"uri":         runURI(name, dagRunID),
		"status":      run.Status,
		"statusLabel": run.StatusLabel,
	}, nil
}

func classifyReadToolError(input readInput, err error) *readToolError {
	if readErr, ok := errors.AsType[*readToolError](err); ok {
		return readErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &readToolError{
			Code:    readErrorResourceUnavailable,
			Message: err.Error(),
			Target:  input.Target,
			URI:     resourceURIForReadError(input),
		}
	}
	if apiErr, ok := errors.AsType[*frontendapi.Error](err); ok {
		code := readErrorResourceUnavailable
		switch apiErr.HTTPStatus {
		case http.StatusBadRequest:
			code = readErrorInvalidToolInput
		case http.StatusUnauthorized, http.StatusForbidden:
			code = readErrorResourceUnavailable
		case http.StatusNotFound:
			code = readErrorResourceNotFound
		}
		return &readToolError{
			Code:    code,
			Message: apiErr.Message,
			Target:  input.Target,
			URI:     resourceURIForReadError(input),
		}
	}
	if isReadResourceNotFound(err) {
		return resourceNotFoundReadError(input, err.Error())
	}
	return &readToolError{
		Code:    readErrorInternal,
		Message: err.Error(),
		Target:  input.Target,
		URI:     resourceURIForReadError(input),
	}
}

func isReadResourceNotFound(err error) bool {
	return isDAGNotFound(err) ||
		errors.Is(err, dagrun.ErrDAGRunIDNotFound) ||
		errors.Is(err, dagrun.ErrNoStatusData) ||
		looksNotFound(err)
}

func looksNotFound(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "no dag-runs found")
}

func resourceNotFoundReadError(input readInput, message string) *readToolError {
	return &readToolError{
		Code:    readErrorResourceNotFound,
		Message: message,
		Target:  input.Target,
		URI:     resourceURIForReadError(input),
	}
}

func resourceURIForReadError(input readInput) string {
	if input.URI != "" {
		return input.URI
	}
	switch input.Target {
	case readTargetReference:
		return readReferenceURI(input.Name)
	case readTargetDAGSpec:
		return dagSpecURI(input.Name)
	case readTargetWiki:
		return uriWithQuery(wikiCollectionURI(input.Workspace), input.Query)
	case readTargetWikiPage:
		return wikiPageURI(input.Workspace, input.Path)
	case readTargetRun:
		return runURI(input.Name, input.DAGRunID)
	case readTargetRunLogs:
		return runLogsURIWithQuery(input.Name, input.DAGRunID, input.Query)
	default:
		return ""
	}
}

func invalidToolInput(message, field string) *readToolError {
	return &readToolError{
		Code:    readErrorInvalidToolInput,
		Message: message,
		Field:   field,
	}
}

func invalidTargetField(target, field string) *readToolError {
	return &readToolError{
		Code:    readErrorInvalidToolInput,
		Message: "The " + field + " field is not allowed for target " + target + ".",
		Target:  target,
		Field:   field,
	}
}

func missingTargetField(target, field string) *readToolError {
	return &readToolError{
		Code:    readErrorInvalidToolInput,
		Message: "The " + field + " field is required for target " + target + ".",
		Target:  target,
		Field:   field,
	}
}

func invalidTargetValue(target, field, message string) *readToolError {
	return &readToolError{
		Code:    readErrorInvalidToolInput,
		Message: message,
		Target:  target,
		Field:   field,
	}
}

func readQueryError(target string, uriMode bool, rawURI, message string) *readToolError {
	if uriMode {
		return &readToolError{
			Code:    readErrorInvalidResourceURI,
			Message: message,
			URI:     rawURI,
		}
	}
	return &readToolError{
		Code:    readErrorInvalidToolInput,
		Message: message,
		Target:  target,
		Field:   readFieldQuery,
	}
}

func invalidResourceURI(rawURI, message string) *readToolError {
	return &readToolError{
		Code:    readErrorInvalidResourceURI,
		Message: message,
		URI:     rawURI,
	}
}

func unsupportedReadTargetError(target string) *readToolError {
	return &readToolError{
		Code:    readErrorUnsupportedReadTarget,
		Message: "Unsupported read target.",
		Target:  target,
		Field:   readFieldTarget,
	}
}

func readErrorResult(err *readToolError) *mcpsdk.CallToolResult {
	output := map[string]any{
		"code":    err.Code,
		"message": err.Message,
	}
	if err.Target != "" {
		output["target"] = err.Target
	}
	if err.Field != "" {
		output["field"] = err.Field
	}
	if err.URI != "" {
		output["uri"] = err.URI
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

func readReferenceURI(topic string) string {
	return readResourceReferenceCollectionURI + "/" + pathEscape(topic)
}

func uriWithQuery(base, rawQuery string) string {
	if rawQuery == "" {
		return base
	}
	return base + "?" + rawQuery
}

func readResourceLinks(uri string) []resourceLink {
	if uri == "" {
		return nil
	}
	resource, readErr := parseReadResourcePath(uri)
	if readErr != nil {
		return nil
	}
	switch resource.host {
	case readTargetReference:
		if len(resource.segments) == 0 {
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "dagu_references",
				title:       "Dagu references",
				description: "Dagu MCP reference collection.",
				mimeType:    resourceMIMEJSON,
			}}
		}
		if len(resource.segments) == 1 {
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "dagu_reference",
				title:       "Dagu reference",
				description: "Dagu MCP reference.",
				mimeType:    resourceMIMEText,
			}}
		}
	case readTargetDAGs:
		if len(resource.segments) == 0 {
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "dags",
				title:       "DAGs",
				description: "DAG collection.",
				mimeType:    resourceMIMEJSON,
			}}
		}
		if len(resource.segments) == 2 {
			return []resourceLink{linkForDAGSpec(resource.segments[0])}
		}
	case readTargetWiki:
		if len(resource.segments) <= 1 {
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "wiki",
				title:       "Wiki",
				description: "Workspace-aware Wiki collection.",
				mimeType:    resourceMIMEJSON,
			}}
		}
		if len(resource.segments) == 2 {
			return []resourceLink{linkForWikiPage(resource.segments[0], resource.segments[1])}
		}
	case readTargetRuns:
		if len(resource.segments) == 0 {
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "dag_runs",
				title:       "DAG-runs",
				description: "DAG-run collection.",
				mimeType:    resourceMIMEJSON,
			}}
		}
		if len(resource.segments) >= 2 {
			if len(resource.segments) == 5 && resource.segments[2] == "steps" && resource.segments[4] == "logs" {
				return []resourceLink{{
					uri:         resource.rawURI,
					name:        "dag_run_step_log",
					title:       "DAG-run step log",
					description: "Log output for this DAG-run step.",
					mimeType:    resourceMIMEJSON,
				}}
			}
			if len(resource.segments) == 3 && resource.segments[2] == "logs" {
				return []resourceLink{{
					uri:         resource.rawURI,
					name:        "dag_run_logs",
					title:       "DAG-run logs",
					description: "Logs for this DAG-run.",
					mimeType:    resourceMIMEJSON,
				}}
			}
			return []resourceLink{{
				uri:         resource.rawURI,
				name:        "dag_run",
				title:       "DAG-run details",
				description: "DAG-run details.",
				mimeType:    resourceMIMEJSON,
			}}
		}
	}
	return nil
}
