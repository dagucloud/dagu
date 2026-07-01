// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	readTargetReferences = "references"
	readTargetReference  = "reference"
	readTargetDAGs       = "dags"
	readTargetDAG        = "dag"
	readTargetDAGSpec    = "dag_spec"
	readTargetRuns       = "runs"
	readTargetRun        = "run"
	readTargetRunLogs    = "run_logs"

	readErrorInvalidToolInput      = "invalid_tool_input"
	readErrorInvalidResourceURI    = "invalid_resource_uri"
	readErrorUnsupportedReadTarget = "unsupported_read_target"
	readErrorUnsupportedResource   = "unsupported_resource"
	readErrorResourceNotFound      = "resource_not_found"
	readErrorResourceUnavailable   = "resource_unavailable"
	readErrorInternal              = "internal_error"

	readFieldTarget   = "target"
	readFieldName     = "name"
	readFieldDAGRunID = "dagRunId"
	readFieldQuery    = "query"
	readFieldURI      = "uri"

	readResourceScheme = "dagu"

	readResourceReferenceCollectionURI = "dagu://reference"
	readResourceDAGsCollectionURI      = "dagu://dags"
	readResourceRunsCollectionURI      = "dagu://runs"
)

type readInput struct {
	Target   string `json:"target" jsonschema:"Read target: dags, dag, dag_spec, runs, run, run_logs, or reference."`
	Name     string `json:"name,omitempty" jsonschema:"DAG name for dag, dag_spec, run, and run_logs targets."`
	DAGRunID string `json:"dagRunId,omitempty" jsonschema:"DAG-run ID for run and run_logs targets. The value latest is accepted where Dagu accepts it."`
	Query    string `json:"query,omitempty" jsonschema:"URL query string for list targets, for example page=1&perPage=100 or status=running."`
	URI      string `json:"uri,omitempty" jsonschema:"Resource URI to read directly, for example dagu://reference/authoring."`
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
			raw, err = svc.api.GetDAGsListData(ctx, apiQueryString(input))
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
	case readTargetRuns:
		if err = svc.requireAPI(); err == nil {
			var raw any
			raw, err = svc.api.GetDAGRunsListData(ctx, apiQueryString(input))
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
	if input.URI != "" {
		output["uri"] = input.URI
	}

	return resultWithLinks("Dagu read completed.", readResourceLinks(input.URI)...), output, nil
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
		for _, field := range []string{readFieldTarget, readFieldName, readFieldDAGRunID, readFieldQuery} {
			if values[field] != "" {
				mixed = append(mixed, field)
			}
		}
		if len(mixed) > 0 {
			return readInput{}, &readToolError{
				Code:    readErrorInvalidToolInput,
				Message: "URI mode cannot be combined with target-mode fields.",
				Details: map[string]any{
					"fields": mixed,
				},
			}
		}
		return parseReadResourceURI(values[readFieldURI])
	}

	target := values[readFieldTarget]
	if target == "" {
		if emptyTarget {
			return readInput{}, invalidToolInput("The target field is required for target mode.", readFieldTarget)
		}
		return readInput{}, invalidToolInput("Either target or uri is required.", "")
	}

	input := readInput{
		Target:   target,
		Name:     values[readFieldName],
		DAGRunID: values[readFieldDAGRunID],
		Query:    values[readFieldQuery],
	}
	if err := validateTargetReadInput(&input); err != nil {
		return readInput{}, err
	}
	return input, nil
}

func isReadInputField(field string) bool {
	switch field {
	case readFieldTarget, readFieldName, readFieldDAGRunID, readFieldQuery, readFieldURI:
		return true
	default:
		return false
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
		if len(rawValues) > 1 && !(target == readTargetRuns && key == "status") {
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
		case "page", "perPage", "name", "labels", "sort", "order":
			return true
		}
	case readTargetRuns:
		switch key {
		case "name", "dagRunId", "status", "fromDate", "toDate", "limit", "cursor", "labels":
			return true
		}
	case readTargetRunLogs:
		switch key {
		case "tail", "head", "offset", "limit":
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
		case "sort":
			return value == "name" || value == "nextRun"
		case "order":
			return value == "asc" || value == "desc"
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
		case "tail", "head", "offset":
			return validIntRange(value, 1, 0)
		case "limit":
			return validIntRange(value, 1, 10000)
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
	for _, item := range strings.Split(value, ",") {
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

func apiQueryString(input readInput) string {
	if input.Query == "" || input.Target != readTargetRuns {
		return input.Query
	}
	values, err := url.ParseQuery(input.Query)
	if err != nil {
		return input.Query
	}
	values.Del("cursor")
	return values.Encode()
}

func classifyReadToolError(input readInput, err error) *readToolError {
	var readErr *readToolError
	if errors.As(err, &readErr) {
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
	if isDAGNotFound(err) || looksNotFound(err) {
		return resourceNotFoundReadError(input, err.Error())
	}
	return &readToolError{
		Code:    readErrorInternal,
		Message: err.Error(),
		Target:  input.Target,
		URI:     resourceURIForReadError(input),
	}
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
