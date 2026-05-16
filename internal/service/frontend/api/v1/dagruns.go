// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	spectypes "github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
)

var filenameUnsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

const dagRunReadTimeout = 10 * time.Second
const statusClientClosedRequest = 499
const artifactTextPreviewMaxBytes int64 = 2 * 1024 * 1024
const artifactImagePreviewMaxBytes int64 = 5 * 1024 * 1024

var errArtifactUnavailable = errors.New("artifact directory not found")

const (
	manualStepSettleTimeout      = 5 * time.Second
	manualStepSettlePollInterval = 50 * time.Millisecond
)

type dagRunReadRequestInfo struct {
	endpoint    string
	dagName     string
	dagRunID    string
	subDAGRunID string
}

func sanitizeFilename(s string) string {
	return filenameUnsafeChars.ReplaceAllString(s, "_")
}

func (info dagRunReadRequestInfo) attrs(duration time.Duration) []slog.Attr {
	attrs := []slog.Attr{
		tag.Endpoint(info.endpoint),
		tag.Duration(duration),
	}
	if info.dagName != "" {
		attrs = append(attrs, tag.DAG(info.dagName))
	}
	if info.dagRunID != "" {
		attrs = append(attrs, tag.RunID(info.dagRunID))
	}
	if info.subDAGRunID != "" {
		attrs = append(attrs, tag.SubRunID(info.subDAGRunID))
	}
	return attrs
}

func withDAGRunReadTimeout[T any](
	ctx context.Context,
	info dagRunReadRequestInfo,
	read func(context.Context) (T, error),
) (T, error) {
	readCtx, cancel := context.WithTimeout(ctx, dagRunReadTimeout)
	defer cancel()

	startedAt := time.Now()
	result, err := read(readCtx)
	if readErr := readCtx.Err(); readErr != nil {
		duration := time.Since(startedAt)
		switch {
		case errors.Is(readErr, context.DeadlineExceeded):
			logger.Warn(ctx, "DAG run read timed out", info.attrs(duration)...)
			var zero T
			return zero, context.DeadlineExceeded
		case errors.Is(readErr, context.Canceled):
			logger.Warn(ctx, "DAG run read canceled", info.attrs(duration)...)
			var zero T
			return zero, context.Canceled
		}
	}
	if err == nil {
		return result, nil
	}

	duration := time.Since(startedAt)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(readCtx.Err(), context.DeadlineExceeded):
		logger.Warn(ctx, "DAG run read timed out", info.attrs(duration)...)
		var zero T
		return zero, context.DeadlineExceeded
	case errors.Is(err, context.Canceled), errors.Is(readCtx.Err(), context.Canceled):
		logger.Warn(ctx, "DAG run read canceled", info.attrs(duration)...)
		var zero T
		return zero, context.Canceled
	default:
		return result, err
	}
}

func dagRunReadTimeoutResponse(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeTimeout,
		Message: message,
	}
}

func dagRunReadCanceledResponse(message string) api.Error {
	return api.Error{
		Code:    api.ErrorCodeInternalError,
		Message: message,
	}
}

// buildLogReadOptions constructs LogReadOptions from request parameters.
func (a *API) buildLogReadOptions(head, tail, offset, limit *int) fileutil.LogReadOptions {
	return fileutil.LogReadOptions{
		Head:     valueOf(head),
		Tail:     valueOf(tail),
		Offset:   valueOf(offset),
		Limit:    valueOf(limit),
		Encoding: a.logEncodingCharset,
	}
}

// ExecuteDAGRunFromSpec implements api.StrictServerInterface.
func (a *API) ExecuteDAGRunFromSpec(ctx context.Context, request api.ExecuteDAGRunFromSpecRequestObject) (api.ExecuteDAGRunFromSpecResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	if request.Body == nil || request.Body.Spec == "" {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "spec is required",
		}
	}

	// Determine dagRunId upfront (used for unique temp dir path)
	var dagRunId, params string
	var singleton bool
	if request.Body.DagRunId != nil {
		dagRunId = *request.Body.DagRunId
	}
	if err := validateDAGRunID(dagRunId); err != nil {
		return nil, err
	}
	if dagRunId == "" {
		var genErr error
		dagRunId, genErr = a.dagRunMgr.GenDAGRunID(ctx)
		if genErr != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", genErr)
		}
	}
	if request.Body.Params != nil {
		params = *request.Body.Params
	}
	if request.Body.Singleton != nil {
		singleton = *request.Body.Singleton
	}

	dag, cleanup, err := a.loadInlineDAG(ctx, request.Body.Spec, request.Body.Name, dagRunId)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := a.ensureDAGRunIDUnique(ctx, dag, dagRunId); err != nil {
		return nil, err
	}

	if singleton {
		if err := a.checkSingletonRunning(ctx, dag); err != nil {
			return nil, err
		}
	}

	labels, err := extractLabelsParam(request.Body.Labels, request.Body.Tags)
	if err != nil {
		return nil, err
	}
	if err := a.requireExecuteForWorkspace(ctx, runtimeWorkspaceName(dag, labels)); err != nil {
		return nil, err
	}

	if err := a.startDAGRun(ctx, dag, params, dagRunId, valueOf(request.Body.Name), labels); err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    fmt.Sprintf("failed to start dag-run: %s", err.Error()),
		}
	}

	detailsMap := map[string]any{
		"dag_name":   dag.Name,
		"dag_run_id": dagRunId,
		"inline":     true,
	}
	if params != "" {
		detailsMap["params"] = params
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_execute", detailsMap)

	return api.ExecuteDAGRunFromSpec200JSONResponse{
		DagRunId: dagRunId,
	}, nil
}

// EnqueueDAGRunFromSpec implements api.StrictServerInterface.
func (a *API) EnqueueDAGRunFromSpec(ctx context.Context, request api.EnqueueDAGRunFromSpecRequestObject) (api.EnqueueDAGRunFromSpecResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	if request.Body == nil || request.Body.Spec == "" {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "spec is required",
		}
	}

	var dagRunId, params string
	var singleton bool
	if request.Body.DagRunId != nil {
		dagRunId = *request.Body.DagRunId
	}
	if err := validateDAGRunID(dagRunId); err != nil {
		return nil, err
	}
	if dagRunId == "" {
		var genErr error
		dagRunId, genErr = a.dagRunMgr.GenDAGRunID(ctx)
		if genErr != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", genErr)
		}
	}
	if request.Body.Params != nil {
		params = *request.Body.Params
	}
	if request.Body.Singleton != nil {
		singleton = *request.Body.Singleton
	}
	dag, cleanup, err := a.loadInlineDAG(ctx, request.Body.Spec, request.Body.Name, dagRunId)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if request.Body.Queue != nil && *request.Body.Queue != "" {
		dag.Queue = *request.Body.Queue
	}

	if _, err := a.dagRunStore.FindAttempt(ctx, exec.DAGRunRef{Name: dag.Name, ID: dagRunId}); !errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return nil, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("dag-run ID %s already exists for DAG %s", dagRunId, dag.Name),
		}
	}

	if singleton {
		if err := a.checkSingletonRunning(ctx, dag); err != nil {
			return nil, err
		}
		if err := a.checkSingletonQueued(ctx, dag); err != nil {
			return nil, err
		}
	}

	labels, err := extractLabelsParam(request.Body.Labels, request.Body.Tags)
	if err != nil {
		return nil, err
	}
	if err := a.requireExecuteForWorkspace(ctx, runtimeWorkspaceName(dag, labels)); err != nil {
		return nil, err
	}

	if err := persistInlineEnqueueLabels(dag, labels); err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    fmt.Sprintf("failed to prepare queued dag-run spec: %s", err.Error()),
		}
	}

	if err := a.enqueueDAGRun(ctx, dag, params, dagRunId, valueOf(request.Body.Name), core.TriggerTypeManual, ""); err != nil {
		return nil, fmt.Errorf("error enqueuing dag-run: %w", err)
	}

	detailsMap := map[string]any{
		"dag_name":   dag.Name,
		"dag_run_id": dagRunId,
		"inline":     true,
	}
	if params != "" {
		detailsMap["params"] = params
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_enqueue", detailsMap)

	return api.EnqueueDAGRunFromSpec200JSONResponse{
		DagRunId: dagRunId,
	}, nil
}

// persistInlineEnqueueLabels patches the inline temp spec file so queued DAG runs
// persist the effective label set without relying on the generic CLI sync path.
func persistInlineEnqueueLabels(dag *core.DAG, labels string) error {
	if labels == "" || len(dag.YamlData) == 0 {
		return nil
	}

	patched, err := applyInlineEnqueueLabels(dag.YamlData, labels)
	if err != nil {
		return err
	}

	dag.YamlData = patched
	if dag.Location == "" {
		return nil
	}

	if err := os.WriteFile(dag.Location, patched, 0o600); err != nil {
		return fmt.Errorf("write patched inline spec: %w", err)
	}

	return nil
}

func applyInlineEnqueueLabels(data []byte, labels string) ([]byte, error) {
	if len(data) == 0 || labels == "" {
		return data, nil
	}

	existingLabels, err := extractInlineEnqueueLabelStrings(data)
	if err != nil {
		return nil, err
	}

	var firstDoc yaml.MapSlice
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&firstDoc); err != nil {
		return nil, fmt.Errorf("decode first document: %w", err)
	}

	merged := append(existingLabels, strings.Split(labels, ",")...)
	deleteInlineEnqueueMapValue(&firstDoc, "tags")
	setInlineEnqueueMapValue(&firstDoc, "labels", core.NewLabels(merged).Strings())

	patched, err := yaml.Marshal(firstDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal patched document: %w", err)
	}

	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, fmt.Errorf("parse yaml documents: %w", err)
	}
	if len(file.Docs) <= 1 {
		return patched, nil
	}

	var buf bytes.Buffer
	buf.Grow(len(data))
	buf.Write(patched)
	for _, doc := range file.Docs[1:] {
		buf.WriteString("---\n")
		buf.WriteString(doc.String())
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

func extractInlineEnqueueLabelStrings(data []byte) ([]string, error) {
	var parsed struct {
		Labels         spectypes.LabelsValue `yaml:"labels"`
		DeprecatedTags spectypes.LabelsValue `yaml:"tags"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode existing labels: %w", err)
	}
	if !parsed.Labels.IsZero() {
		return parsed.Labels.Values(), nil
	}
	return parsed.DeprecatedTags.Values(), nil
}

func getInlineEnqueueMapValue(ms yaml.MapSlice, key string) (any, bool) {
	for _, item := range ms {
		if itemKey, ok := item.Key.(string); ok && itemKey == key {
			return item.Value, true
		}
	}
	return nil, false
}

func setInlineEnqueueMapValue(ms *yaml.MapSlice, key string, value any) {
	for i := range *ms {
		if itemKey, ok := (*ms)[i].Key.(string); ok && itemKey == key {
			(*ms)[i].Value = value
			return
		}
	}

	*ms = append(*ms, yaml.MapItem{Key: key, Value: value})
}

func deleteInlineEnqueueMapValue(ms *yaml.MapSlice, key string) {
	for i := range *ms {
		if itemKey, ok := (*ms)[i].Key.(string); ok && itemKey == key {
			*ms = append((*ms)[:i], (*ms)[i+1:]...)
			return
		}
	}
}

func (a *API) loadInlineDAG(ctx context.Context, specContent string, name *string, dagRunID string, extraLoadOpts ...spec.LoadOption) (*core.DAG, func(), error) {
	nameHint := "inline"
	if name != nil && *name != "" {
		if err := core.ValidateDAGName(*name); err != nil {
			return nil, func() {}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		nameHint = *name
	} else {
		validateOpts := append([]spec.LoadOption{
			spec.WithoutEval(),
		}, extraLoadOpts...)
		dag, err := spec.LoadYAML(
			ctx, []byte(specContent),
			validateOpts...,
		)
		if err != nil {
			return nil, func() {}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		if err := dag.Validate(); err != nil {
			return nil, func() {}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
	}

	tmpDir := filepath.Join(os.TempDir(), nameHint, dagRunID)
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return nil, func() {}, fmt.Errorf("failed to create temp directory: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	tfPath := filepath.Join(tmpDir, fmt.Sprintf("%s.yaml", nameHint))
	if err := os.WriteFile(tfPath, []byte(specContent), 0o600); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("failed to write spec to temp file: %w", err)
	}

	loadOpts := append([]spec.LoadOption{}, extraLoadOpts...)
	if name != nil && *name != "" {
		loadOpts = append(loadOpts, spec.WithName(*name))
	}
	dag, err := spec.Load(ctx, tfPath, loadOpts...)
	if err != nil {
		cleanup()
		return nil, func() {}, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	if !dag.WorkingDirExplicit {
		dag.WorkingDir = ""
	}
	dag.SourceFile = ""

	return dag, cleanup, nil
}

func restoreDAGRunSnapshot(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) (*core.DAG, string, error) {
	quotedParams := spec.QuoteRuntimeParams(status.ParamsList, dag.ParamDefs)
	dag.Params = quotedParams
	dag.LoadDotEnv(ctx)

	restored, err := rebuildDAGRunSnapshotFromYAML(ctx, dag)
	if err != nil {
		return nil, "", err
	}

	return restored, strings.Join(quotedParams, " "), nil
}

func rebuildDAGRunSnapshotFromYAML(ctx context.Context, dag *core.DAG) (*core.DAG, error) {
	if len(dag.YamlData) == 0 {
		return dag, nil
	}

	buildEnvMap := buildenv.ToMap(dag.Env)
	for key, value := range dag.PresolvedBuildEnv {
		if buildEnvMap == nil {
			buildEnvMap = make(map[string]string)
		}
		buildEnvMap[key] = value
	}

	presolvedBuildEnv, err := buildenv.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load presolved build env: %w", err)
	}
	for key, value := range presolvedBuildEnv {
		if buildEnvMap == nil {
			buildEnvMap = make(map[string]string)
		}
		buildEnvMap[key] = value
	}

	loadOpts := []spec.LoadOption{
		spec.WithParams(dag.Params),
		spec.SkipSchemaValidation(),
	}
	if len(buildEnvMap) > 0 {
		loadOpts = append(loadOpts, spec.WithBuildEnv(buildEnvMap))
	}
	if len(dag.BaseConfigData) > 0 {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent(dag.BaseConfigData))
	}
	if dag.Name != "" {
		loadOpts = append(loadOpts, spec.WithName(dag.Name))
	}

	fresh, err := spec.LoadYAML(ctx, dag.YamlData, loadOpts...)
	if err != nil {
		return nil, err
	}
	fresh.SourceFile = dag.SourceFile

	dag.Env = fresh.Env
	dag.Params = fresh.Params
	dag.ParamsJSON = fresh.ParamsJSON
	dag.SMTP = fresh.SMTP
	dag.SSH = fresh.SSH
	dag.RegistryAuths = fresh.RegistryAuths
	dag.Harness = fresh.Harness
	dag.Harnesses = fresh.Harnesses

	core.InitializeDefaults(dag)

	return dag, nil
}

func (a *API) ListDAGRuns(ctx context.Context, request api.ListDAGRunsRequestObject) (api.ListDAGRunsResponseObject, error) {
	labelsParam, err := queryLabelsParam(request.Params.Labels, request.Params.Tags)
	if err != nil {
		return nil, err
	}
	opts := buildDAGRunListOptions(dagRunListFilterInput{
		statuses: request.Params.Status,
		fromDate: request.Params.FromDate,
		toDate:   request.Params.ToDate,
		name:     request.Params.Name,
		dagRunID: request.Params.DagRunId,
		labels:   labelsParam,
		limit:    request.Params.Limit,
		cursor:   request.Params.Cursor,
	})
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	opts.query = append(opts.query, exec.WithWorkspaceFilter(workspaceFilter))
	var dagName, dagRunID string
	if request.Params.Name != nil {
		dagName = *request.Params.Name
	}
	if request.Params.DagRunId != nil {
		dagRunID = *request.Params.DagRunId
	}

	page, err := a.readDAGRunsPage(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs",
		dagName:  dagName,
		dagRunID: dagRunID,
	}, opts.query)
	if err != nil {
		if apiErr := dagRunListBadRequest(err); apiErr != nil {
			return nil, apiErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return api.ListDAGRunsdefaultJSONResponse{
				StatusCode: http.StatusGatewayTimeout,
				Body:       dagRunReadTimeoutResponse("dag-run list request timed out"),
			}, nil
		}
		return nil, fmt.Errorf("error listing dag-runs: %w", err)
	}

	return api.ListDAGRuns200JSONResponse(toDAGRunsPageResponse(page)), nil
}

func (a *API) ListDAGRunsByName(ctx context.Context, request api.ListDAGRunsByNameRequestObject) (api.ListDAGRunsByNameResponseObject, error) {
	opts := buildDAGRunListOptions(dagRunListFilterInput{
		statuses:  request.Params.Status,
		fromDate:  request.Params.FromDate,
		toDate:    request.Params.ToDate,
		dagRunID:  request.Params.DagRunId,
		limit:     request.Params.Limit,
		cursor:    request.Params.Cursor,
		exactName: &request.Name,
	})
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	opts.query = append(opts.query, exec.WithWorkspaceFilter(workspaceFilter))
	var dagRunID string
	if request.Params.DagRunId != nil {
		dagRunID = *request.Params.DagRunId
	}

	page, err := a.readDAGRunsPage(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}",
		dagName:  request.Name,
		dagRunID: dagRunID,
	}, opts.query)
	if err != nil {
		if apiErr := dagRunListBadRequest(err); apiErr != nil {
			return nil, apiErr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return api.ListDAGRunsByNamedefaultJSONResponse{
				StatusCode: http.StatusGatewayTimeout,
				Body:       dagRunReadTimeoutResponse("dag-run list request timed out"),
			}, nil
		}
		return nil, fmt.Errorf("error listing dag-runs: %w", err)
	}

	return api.ListDAGRunsByName200JSONResponse(toDAGRunsPageResponse(page)), nil
}

type dagRunListOptions struct {
	query []exec.ListDAGRunStatusesOption
}

type dagRunListFilterInput struct {
	statuses  *api.StatusList
	fromDate  *int64
	toDate    *int64
	name      *string
	exactName *string
	dagRunID  *string
	labels    *string
	limit     *int
	cursor    *string
}

func buildDAGRunListOptions(input dagRunListFilterInput) dagRunListOptions {
	const (
		defaultLimit = 100
		maxLimit     = 500
	)

	opts := dagRunListOptions{}
	limit := defaultLimit

	if statuses := toCoreStatuses(input.statuses); len(statuses) > 0 {
		opts.query = append(opts.query, exec.WithStatuses(statuses))
	}
	if input.fromDate != nil {
		opts.query = append(opts.query, exec.WithFrom(exec.NewUTC(time.Unix(*input.fromDate, 0))))
	}
	if input.toDate != nil {
		opts.query = append(opts.query, exec.WithTo(exec.NewUTC(time.Unix(*input.toDate, 0))))
	}
	if input.exactName != nil && *input.exactName != "" {
		opts.query = append(opts.query, exec.WithExactName(*input.exactName))
	} else if input.name != nil && *input.name != "" {
		opts.query = append(opts.query, exec.WithName(*input.name))
	}
	if input.dagRunID != nil && *input.dagRunID != "" {
		opts.query = append(opts.query, exec.WithDAGRunID(*input.dagRunID))
	}
	if labels := parseCommaSeparatedLabels(input.labels); len(labels) > 0 {
		opts.query = append(opts.query, exec.WithLabels(labels))
	}
	if input.limit != nil {
		limit = clampInt(*input.limit, 1, maxLimit)
	}
	if input.cursor != nil && *input.cursor != "" {
		opts.query = append(opts.query, exec.WithCursor(*input.cursor))
	}
	opts.query = append(opts.query, exec.WithLimit(limit))
	return opts
}

func (a *API) readDAGRunsPage(
	ctx context.Context,
	info dagRunReadRequestInfo,
	opts []exec.ListDAGRunStatusesOption,
) (exec.DAGRunStatusPage, error) {
	page, err := withDAGRunReadTimeout(ctx, info, func(readCtx context.Context) (exec.DAGRunStatusPage, error) {
		page, listErr := a.dagRunStore.ListStatusesPage(readCtx, opts...)
		if listErr != nil {
			return exec.DAGRunStatusPage{}, fmt.Errorf("error listing dag-runs: %w", listErr)
		}
		return page, nil
	})
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return page, nil
}

func dagRunListBadRequest(err error) *Error {
	if !errors.Is(err, filedagrun.ErrInvalidQueryCursor) {
		return nil
	}
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    err.Error(),
	}
}

func parseCommaSeparatedLabels(labelsParam *string) []string {
	if labelsParam == nil || *labelsParam == "" {
		return nil
	}

	parts := strings.Split(*labelsParam, ",")
	seen := make(map[string]struct{}, len(parts))
	labels := make([]string, 0, len(parts))
	for _, label := range parts {
		normalized := strings.ToLower(strings.TrimSpace(label))
		if normalized != "" {
			if _, exists := seen[normalized]; !exists {
				seen[normalized] = struct{}{}
				labels = append(labels, normalized)
			}
		}
	}
	return labels
}

func queryLabelsParam(labelsParam, deprecatedTagsParam *string) (*string, error) {
	hasLabels := labelsParam != nil && *labelsParam != ""
	hasTags := deprecatedTagsParam != nil && *deprecatedTagsParam != ""
	if hasLabels && hasTags {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "labels and deprecated tags cannot both be set",
		}
	}
	if hasLabels {
		return labelsParam, nil
	}
	if hasTags {
		return deprecatedTagsParam, nil
	}
	return nil, nil
}

func (a *API) GetDAGRunLog(ctx context.Context, request api.GetDAGRunLogRequestObject) (api.GetDAGRunLogResponseObject, error) {
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return api.GetDAGRunLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, err
	}

	options := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(dagStatus.Log, options)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return api.GetDAGRunLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", dagStatus.Log, err)
	}

	return api.GetDAGRunLog200JSONResponse{
		Content:    content,
		LineCount:  ptrOf(lineCount),
		TotalLines: ptrOf(totalLines),
		HasMore:    ptrOf(hasMore),
		IsEstimate: ptrOf(isEstimate),
	}, nil
}

func (a *API) DownloadDAGRunLog(ctx context.Context, request api.DownloadDAGRunLogRequestObject) (api.DownloadDAGRunLogResponseObject, error) {
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return api.DownloadDAGRunLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(dagStatus.Log)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return api.DownloadDAGRunLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", dagStatus.Log, err)
	}

	filename := fmt.Sprintf("%s-%s-scheduler.log", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId))
	return api.DownloadDAGRunLog200TextResponse{
		Body: string(content),
		Headers: api.DownloadDAGRunLog200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", filename),
		},
	}, nil
}

func (a *API) GetDAGRunArtifacts(ctx context.Context, request api.GetDAGRunArtifactsRequestObject) (api.GetDAGRunArtifactsResponseObject, error) {
	status, err := a.getDAGRunArtifactStatus(ctx, request.Name, request.DagRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return api.GetDAGRunArtifacts404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("get dag-run artifact status: %w", err)
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(status)); err != nil {
		return nil, err
	}

	items, err := listArtifactTree(status.ArchiveDir, artifactListRecursive(request.Params.Recursive))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return api.GetDAGRunArtifacts404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("list dag-run artifacts: %w", err)
	}

	return api.GetDAGRunArtifacts200JSONResponse{
		Items: items,
	}, nil
}

func (a *API) GetDAGRunArtifactPreview(ctx context.Context, request api.GetDAGRunArtifactPreviewRequestObject) (api.GetDAGRunArtifactPreviewResponseObject, error) {
	status, err := a.getDAGRunArtifactStatus(ctx, request.Name, request.DagRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return api.GetDAGRunArtifactPreview404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("get dag-run artifact status: %w", err)
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(status)); err != nil {
		return nil, err
	}

	preview, err := buildArtifactPreview(status.ArchiveDir, string(request.Params.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return api.GetDAGRunArtifactPreview404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact file not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("preview dag-run artifact: %w", err)
	}

	return api.GetDAGRunArtifactPreview200JSONResponse(preview), nil
}

func (a *API) DownloadDAGRunArtifact(ctx context.Context, request api.DownloadDAGRunArtifactRequestObject) (api.DownloadDAGRunArtifactResponseObject, error) {
	status, err := a.getDAGRunArtifactStatus(ctx, request.Name, request.DagRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return api.DownloadDAGRunArtifact404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("get dag-run artifact status: %w", err)
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(status)); err != nil {
		return nil, err
	}

	file, info, err := openArtifactFile(status.ArchiveDir, string(request.Params.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return api.DownloadDAGRunArtifact404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact file not found for dag-run %s", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("open dag-run artifact: %w", err)
	}

	return api.DownloadDAGRunArtifact200ApplicationoctetStreamResponse{
		Body: file,
		Headers: api.DownloadDAGRunArtifact200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", sanitizeFilename(info.Name())),
		},
		ContentLength: info.Size(),
	}, nil
}

func (a *API) GetDAGRunOutputs(ctx context.Context, request api.GetDAGRunOutputsRequestObject) (api.GetDAGRunOutputsResponseObject, error) {
	var attempt exec.DAGRunAttempt
	var err error

	if request.DagRunId == "latest" {
		attempt, err = a.dagRunStore.LatestAttempt(ctx, request.Name)
		if err != nil {
			return api.GetDAGRunOutputs404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("no dag-runs found for DAG %s", request.Name),
			}, nil
		}
	} else {
		ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
		attempt, err = a.dagRunStore.FindAttempt(ctx, ref)
		if err != nil {
			return api.GetDAGRunOutputs404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
			}, nil
		}
	}
	workspaceName, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}

	outputs, err := attempt.ReadOutputs(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read outputs",
			tag.Error(err),
			slog.String("dag", request.Name),
			slog.String("dagRunId", request.DagRunId),
		)
		return nil, fmt.Errorf("error reading outputs: %w", err)
	}

	if outputs == nil {
		outputs = &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{},
			Outputs:  make(map[string]string),
		}
	}

	var completedAt time.Time
	if outputs.Metadata.CompletedAt != "" {
		if t, err := time.Parse(time.RFC3339, outputs.Metadata.CompletedAt); err == nil {
			completedAt = t
		}
	}

	return api.GetDAGRunOutputs200JSONResponse{
		Metadata: api.OutputsMetadata{
			DagName:     outputs.Metadata.DAGName,
			DagRunId:    outputs.Metadata.DAGRunID,
			AttemptId:   outputs.Metadata.AttemptID,
			Status:      api.StatusLabel(outputs.Metadata.Status),
			CompletedAt: completedAt,
			Params:      &outputs.Metadata.Params,
		},
		Outputs: outputs.Outputs,
	}, nil
}

func (a *API) GetDAGRunStepLog(ctx context.Context, request api.GetDAGRunStepLogRequestObject) (api.GetDAGRunStepLogResponseObject, error) {
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return api.GetDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return api.GetDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	options := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	logFile := selectLogFile(node, *request.Params.Stream)

	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(logFile, options)
	if err != nil {
		if strings.Contains(err.Error(), "file not found") {
			return api.GetDAGRunStepLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for step %s", request.StepName),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", logFile, err)
	}

	return api.GetDAGRunStepLog200JSONResponse{
		Content:    content,
		LineCount:  ptrOf(lineCount),
		TotalLines: ptrOf(totalLines),
		HasMore:    ptrOf(hasMore),
		IsEstimate: ptrOf(isEstimate),
	}, nil
}

func (a *API) DownloadDAGRunStepLog(ctx context.Context, request api.DownloadDAGRunStepLogRequestObject) (api.DownloadDAGRunStepLogResponseObject, error) {
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return api.DownloadDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return api.DownloadDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	logFile, streamName := node.Stdout, "stdout"
	if request.Params.Stream != nil && *request.Params.Stream == api.StreamStderr {
		logFile, streamName = node.Stderr, "stderr"
	}

	content, err := os.ReadFile(filepath.Clean(logFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return api.DownloadDAGRunStepLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for step %s", request.StepName),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", logFile, err)
	}

	filename := fmt.Sprintf("%s-%s-%s-%s.log", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId), sanitizeFilename(request.StepName), streamName)
	return api.DownloadDAGRunStepLog200TextResponse{
		Body: string(content),
		Headers: api.DownloadDAGRunStepLog200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", filename),
		},
	}, nil
}

func (a *API) UpdateDAGRunStepStatus(ctx context.Context, request api.UpdateDAGRunStepStatusRequestObject) (api.UpdateDAGRunStepStatusResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return &api.UpdateDAGRunStepStatus404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	if dagStatus.Status == core.Running {
		return &api.UpdateDAGRunStepStatus400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("dag-run ID %s for DAG %s is still running", request.DagRunId, request.Name),
		}, nil
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.UpdateDAGRunStepStatus404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	dagStatus.Nodes[stepIdx].Status = nodeStatusMapping[request.Body.Status]
	dagStatus.Status = deriveManualDAGRunStatus(dagStatus.Nodes, dagStatus.Status)

	if err := a.updateDAGRunStatus(ctx, ref, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating status: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_step_status_update", map[string]any{
		"dag_name":   request.Name,
		"dag_run_id": request.DagRunId,
		"step_name":  request.StepName,
		"new_status": nodeStatusMapping[request.Body.Status].String(),
	})

	return &api.UpdateDAGRunStepStatus200Response{}, nil
}

// ApproveDAGRunStep approves a waiting step.
func (a *API) ApproveDAGRunStep(ctx context.Context, request api.ApproveDAGRunStepRequestObject) (api.ApproveDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return &api.ApproveDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return &api.ApproveDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for dag-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.ApproveDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	if dagStatus.Nodes[stepIdx].Status != core.NodeWaiting {
		return &api.ApproveDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, dagStatus.Nodes[stepIdx].Status),
		}, nil
	}

	if err := validateRequiredInputs(dagStatus.Nodes[stepIdx].Step, request.Body); err != nil {
		return &api.ApproveDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	// Apply approval to the node
	applyApproval(ctx, dagStatus.Nodes[stepIdx], request.Body)

	if err := a.updateDAGRunStatus(ctx, ref, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating status: %w", err)
	}

	// Resume DAG if no more waiting steps
	shouldResume := !hasWaitingSteps(dagStatus.Nodes)
	if shouldResume {
		if err := a.resumeDAGRun(ctx, ref, request.DagRunId); err != nil {
			logger.Error(ctx, "Failed to resume DAG", tag.Error(err))
			shouldResume = false
		} else {
			logger.Info(ctx, "DAG resumed after approval",
				slog.String("dagRunId", request.DagRunId),
				slog.String("step", request.StepName),
			)
		}
	}

	a.logStepApproval(ctx, request.Name, request.DagRunId, "", request.StepName, shouldResume)

	return &api.ApproveDAGRunStep200JSONResponse{
		DagRunId: request.DagRunId,
		StepName: request.StepName,
		Resumed:  shouldResume,
	}, nil
}

func (a *API) ApproveSubDAGRunStep(ctx context.Context, request api.ApproveSubDAGRunStepRequestObject) (api.ApproveSubDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	rootRef := exec.NewDAGRunRef(request.Name, request.DagRunId)
	mutationRef, dagStatus, err := a.getReferencedDAGRunStatusWithRef(ctx, rootRef, request.SubDAGRunId, "")
	if err != nil {
		return &api.ApproveSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.getReferencedDAGRunAttempt(ctx, rootRef, request.SubDAGRunId, dagStatus.Name)
	if err != nil {
		return &api.ApproveSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for sub DAG-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.ApproveSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in sub DAG-run %s", request.StepName, request.SubDAGRunId),
		}, nil
	}

	if dagStatus.Nodes[stepIdx].Status != core.NodeWaiting {
		return &api.ApproveSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, dagStatus.Nodes[stepIdx].Status),
		}, nil
	}

	if err := validateRequiredInputs(dagStatus.Nodes[stepIdx].Step, request.Body); err != nil {
		return &api.ApproveSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	applyApproval(ctx, dagStatus.Nodes[stepIdx], request.Body)

	if err := a.updateDAGRunStatus(ctx, mutationRef, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating sub DAG-run status: %w", err)
	}

	// Resume sub-DAG if no more waiting steps
	shouldResume := !hasWaitingSteps(dagStatus.Nodes)
	if shouldResume {
		if err := a.resumeSubDAGRun(ctx, rootRef, request.SubDAGRunId); err != nil {
			logger.Error(ctx, "Failed to resume sub DAG", tag.Error(err))
			shouldResume = false
		} else {
			logger.Info(ctx, "Sub DAG resumed after approval",
				slog.String("subDagRunId", request.SubDAGRunId),
				slog.String("step", request.StepName),
			)
		}
	}

	a.logStepApproval(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName, shouldResume)

	return &api.ApproveSubDAGRunStep200JSONResponse{
		DagRunId: request.SubDAGRunId,
		StepName: request.StepName,
		Resumed:  shouldResume,
	}, nil
}

func (a *API) GetDAGRunStepMessages(ctx context.Context, request api.GetDAGRunStepMessagesRequestObject) (api.GetDAGRunStepMessagesResponseObject, error) {
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return api.GetDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return api.GetDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return api.GetDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run attempt not found for %s/%s", request.Name, request.DagRunId),
		}, nil
	}

	messages, err := attempt.ReadStepMessages(ctx, request.StepName)
	if err != nil {
		return nil, fmt.Errorf("error reading messages: %w", err)
	}

	return api.GetDAGRunStepMessages200JSONResponse{
		Messages:        toChatMessages(messages),
		ToolDefinitions: toToolDefinitions(node.ToolDefinitions),
		StepStatus:      api.NodeStatus(node.Status),
		StepStatusLabel: api.NodeStatusLabel(node.Status.String()),
		HasMore:         node.Status == core.NodeRunning,
	}, nil
}

func (a *API) GetSubDAGRunStepMessages(ctx context.Context, request api.GetSubDAGRunStepMessagesRequestObject) (api.GetSubDAGRunStepMessagesResponseObject, error) {
	rootRef := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, rootRef, request.SubDAGRunId, "")
	if err != nil {
		return api.GetSubDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return api.GetSubDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in sub DAG-run %s", request.StepName, request.SubDAGRunId),
		}, nil
	}

	attempt, err := a.getReferencedDAGRunAttempt(ctx, rootRef, request.SubDAGRunId, "")
	if err != nil {
		return api.GetSubDAGRunStepMessages404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run attempt not found for %s", request.SubDAGRunId),
		}, nil
	}

	messages, err := attempt.ReadStepMessages(ctx, request.StepName)
	if err != nil {
		return nil, fmt.Errorf("error reading messages: %w", err)
	}

	return api.GetSubDAGRunStepMessages200JSONResponse{
		Messages:        toChatMessages(messages),
		ToolDefinitions: toToolDefinitions(node.ToolDefinitions),
		StepStatus:      api.NodeStatus(node.Status),
		StepStatusLabel: api.NodeStatusLabel(node.Status.String()),
		HasMore:         node.Status == core.NodeRunning,
	}, nil
}

// RejectDAGRunStep rejects a waiting step.
func (a *API) RejectDAGRunStep(ctx context.Context, request api.RejectDAGRunStepRequestObject) (api.RejectDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return &api.RejectDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return &api.RejectDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for dag-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.RejectDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	if dagStatus.Nodes[stepIdx].Status != core.NodeWaiting {
		return &api.RejectDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, dagStatus.Nodes[stepIdx].Status),
		}, nil
	}

	var reason *string
	if request.Body != nil {
		reason = request.Body.Reason
	}
	applyRejection(ctx, dagStatus.Nodes[stepIdx], dagStatus, reason)

	if err := a.updateDAGRunStatus(ctx, ref, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating status: %w", err)
	}

	logger.Info(ctx, "Step rejected",
		slog.String("dagRunId", request.DagRunId),
		slog.String("step", request.StepName),
	)

	a.logStepRejection(ctx, request.Name, request.DagRunId, "", request.StepName, reason)

	return &api.RejectDAGRunStep200JSONResponse{
		DagRunId: request.DagRunId,
		StepName: request.StepName,
	}, nil
}

// RejectSubDAGRunStep rejects a waiting step in a sub DAG-run.
func (a *API) RejectSubDAGRunStep(ctx context.Context, request api.RejectSubDAGRunStepRequestObject) (api.RejectSubDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	rootRef := exec.NewDAGRunRef(request.Name, request.DagRunId)
	mutationRef, dagStatus, err := a.getReferencedDAGRunStatusWithRef(ctx, rootRef, request.SubDAGRunId, "")
	if err != nil {
		return &api.RejectSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.getReferencedDAGRunAttempt(ctx, rootRef, request.SubDAGRunId, dagStatus.Name)
	if err != nil {
		return &api.RejectSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for sub DAG-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.RejectSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in sub DAG-run %s", request.StepName, request.SubDAGRunId),
		}, nil
	}

	if dagStatus.Nodes[stepIdx].Status != core.NodeWaiting {
		return &api.RejectSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, dagStatus.Nodes[stepIdx].Status),
		}, nil
	}

	var reason *string
	if request.Body != nil {
		reason = request.Body.Reason
	}
	applyRejection(ctx, dagStatus.Nodes[stepIdx], dagStatus, reason)

	if err := a.updateDAGRunStatus(ctx, mutationRef, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating sub DAG-run status: %w", err)
	}

	logger.Info(ctx, "Sub DAG step rejected",
		slog.String("subDagRunId", request.SubDAGRunId),
		slog.String("step", request.StepName),
	)

	a.logStepRejection(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName, reason)

	return &api.RejectSubDAGRunStep200JSONResponse{
		DagRunId: request.SubDAGRunId,
		StepName: request.StepName,
	}, nil
}

// PushBackDAGRunStep pushes back a waiting step for re-execution with feedback.
func (a *API) PushBackDAGRunStep(ctx context.Context, request api.PushBackDAGRunStepRequestObject) (api.PushBackDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return &api.PushBackDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return &api.PushBackDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for dag-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.PushBackDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	node := dagStatus.Nodes[stepIdx]

	if node.Status != core.NodeWaiting {
		return &api.PushBackDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, node.Status),
		}, nil
	}

	if node.Step.Approval == nil {
		return &api.PushBackDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s does not have approval configuration; push-back requires the approval field", request.StepName),
		}, nil
	}

	if err := validatePushBackInputs(node.Step, request.Body); err != nil {
		return &api.PushBackDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	// Snapshot current state for rollback if resume fails.
	snapshot, err := json.Marshal(dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error serializing status for rollback: %w", err)
	}

	if err := applyPushBack(ctx, node, dagStatus, request.Body); err != nil {
		return &api.PushBackDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	if err := a.updateDAGRunStatus(ctx, ref, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating status: %w", err)
	}

	if err := a.resumeDAGRun(ctx, ref, request.DagRunId); err != nil {
		logger.Error(ctx, "Failed to resume DAG after push-back, rolling back", tag.Error(err))
		var original exec.DAGRunStatus
		if unmarshalErr := json.Unmarshal(snapshot, &original); unmarshalErr == nil {
			if rollbackErr := a.updateDAGRunStatus(ctx, ref, original); rollbackErr != nil {
				logger.Error(ctx, "Failed to rollback push-back state", tag.Error(rollbackErr))
			}
		}
		return nil, fmt.Errorf("failed to resume DAG after push-back: %w", err)
	}

	logger.Info(ctx, "DAG resumed after push-back",
		slog.String("dagRunId", request.DagRunId),
		slog.String("step", request.StepName),
		slog.Int("iteration", node.ApprovalIteration),
	)

	a.logStepPushBack(ctx, request.Name, request.DagRunId, "", request.StepName, node.ApprovalIteration, true)

	return &api.PushBackDAGRunStep200JSONResponse{
		DagRunId:          request.DagRunId,
		StepName:          request.StepName,
		ApprovalIteration: node.ApprovalIteration,
		Resumed:           true,
	}, nil
}

// PushBackSubDAGRunStep pushes back a waiting step in a sub DAG-run for re-execution with feedback.
func (a *API) PushBackSubDAGRunStep(ctx context.Context, request api.PushBackSubDAGRunStepRequestObject) (api.PushBackSubDAGRunStepResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	rootRef := exec.NewDAGRunRef(request.Name, request.DagRunId)
	mutationRef, dagStatus, err := a.getReferencedDAGRunStatusWithRef(ctx, rootRef, request.SubDAGRunId, "")
	if err != nil {
		return &api.PushBackSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	attempt, err := a.getReferencedDAGRunAttempt(ctx, rootRef, request.SubDAGRunId, dagStatus.Name)
	if err != nil {
		return &api.PushBackSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub DAG-run ID %s not found", request.SubDAGRunId),
		}, nil
	}
	dagStatus, err = a.waitForManualStepMutationReady(ctx, attempt, dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error waiting for sub DAG-run to settle: %w", err)
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.PushBackSubDAGRunStep404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in sub DAG-run %s", request.StepName, request.SubDAGRunId),
		}, nil
	}

	node := dagStatus.Nodes[stepIdx]

	if node.Status != core.NodeWaiting {
		return &api.PushBackSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s is not waiting for approval (status: %s)", request.StepName, node.Status),
		}, nil
	}

	if node.Step.Approval == nil {
		return &api.PushBackSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("step %s does not have approval configuration; push-back requires the approval field", request.StepName),
		}, nil
	}

	if err := validatePushBackInputs(node.Step, request.Body); err != nil {
		return &api.PushBackSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	// Snapshot current state for rollback if resume fails.
	snapshot, err := json.Marshal(dagStatus)
	if err != nil {
		return nil, fmt.Errorf("error serializing status for rollback: %w", err)
	}

	if err := applyPushBack(ctx, node, dagStatus, request.Body); err != nil {
		return &api.PushBackSubDAGRunStep400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: err.Error(),
		}, nil
	}

	if err := a.updateDAGRunStatus(ctx, mutationRef, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating sub DAG-run status: %w", err)
	}

	if err := a.resumeSubDAGRun(ctx, rootRef, request.SubDAGRunId); err != nil {
		logger.Error(ctx, "Failed to resume sub DAG after push-back, rolling back", tag.Error(err))
		var original exec.DAGRunStatus
		if unmarshalErr := json.Unmarshal(snapshot, &original); unmarshalErr == nil {
			if rollbackErr := a.updateDAGRunStatus(ctx, mutationRef, original); rollbackErr != nil {
				logger.Error(ctx, "Failed to rollback push-back state", tag.Error(rollbackErr))
			}
		}
		return nil, fmt.Errorf("failed to resume sub DAG after push-back: %w", err)
	}

	logger.Info(ctx, "Sub DAG resumed after push-back",
		slog.String("subDagRunId", request.SubDAGRunId),
		slog.String("step", request.StepName),
		slog.Int("iteration", node.ApprovalIteration),
	)

	a.logStepPushBack(ctx, request.Name, request.DagRunId, request.SubDAGRunId, request.StepName, node.ApprovalIteration, true)

	return &api.PushBackSubDAGRunStep200JSONResponse{
		DagRunId:          request.SubDAGRunId,
		SubDAGRunId:       &request.SubDAGRunId,
		StepName:          request.StepName,
		ApprovalIteration: node.ApprovalIteration,
		Resumed:           true,
	}, nil
}

// GetDAGRunDetails implements api.StrictServerInterface.
func (a *API) GetDAGRunDetails(ctx context.Context, request api.GetDAGRunDetailsRequestObject) (api.GetDAGRunDetailsResponseObject, error) {
	resp, err := withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}/{dagRunId}",
		dagName:  request.Name,
		dagRunID: request.DagRunId,
	}, func(readCtx context.Context) (api.GetDAGRunDetails200JSONResponse, error) {
		return a.getDAGRunDetailsData(readCtx, request.Name, request.DagRunId)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return &api.GetDAGRunDetailsdefaultJSONResponse{
				StatusCode: statusClientClosedRequest,
				Body:       dagRunReadCanceledResponse("dag-run details request canceled"),
			}, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return &api.GetDAGRunDetailsdefaultJSONResponse{
				StatusCode: http.StatusGatewayTimeout,
				Body:       dagRunReadTimeoutResponse("dag-run details request timed out"),
			}, nil
		}
		return &api.GetDAGRunDetails404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: err.Error(),
		}, nil
	}
	return &resp, nil
}

// DeleteDAGRun implements api.StrictServerInterface.
func (a *API) DeleteDAGRun(ctx context.Context, request api.DeleteDAGRunRequestObject) (api.DeleteDAGRunResponseObject, error) {
	if request.DagRunId == "latest" {
		return api.DeleteDAGRun400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "latest cannot be used when deleting a DAG-run; select a concrete dag-run ID",
		}, nil
	}

	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	workspaceName, err := a.workspaceNameForDAGRun(ctx, ref)
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return api.DeleteDAGRun404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("DAG run %s not found", request.DagRunId),
			}, nil
		}
		return nil, fmt.Errorf("resolve DAG run workspace before delete: %w", err)
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	if err := a.dagRunStore.RemoveDAGRun(ctx, ref, exec.WithRejectActiveDAGRun()); err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) {
			return api.DeleteDAGRun404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("DAG run %s not found", request.DagRunId),
			}, nil
		}
		if errors.Is(err, exec.ErrDAGRunActive) {
			status := strings.TrimPrefix(err.Error(), exec.ErrDAGRunActive.Error()+": ")
			if status == err.Error() {
				return api.DeleteDAGRun400JSONResponse{
					Code:    api.ErrorCodeBadRequest,
					Message: fmt.Sprintf("DAG run %s is active; stop or dequeue it before deleting", request.DagRunId),
				}, nil
			}
			return api.DeleteDAGRun400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: fmt.Sprintf("DAG run %s is %s; stop or dequeue it before deleting", request.DagRunId, status),
			}, nil
		}
		return nil, fmt.Errorf("error deleting DAG run: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_run_delete", map[string]any{
		"dag_name":   request.Name,
		"dag_run_id": request.DagRunId,
	})

	return api.DeleteDAGRun204Response{}, nil
}

func isDAGRunLookupNotFound(err error) bool {
	return errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData)
}

// getDAGRunDetailsData returns DAG run details data. Used by both HTTP handler and SSE fetcher.
func (a *API) getDAGRunDetailsData(ctx context.Context, dagName, dagRunId string) (api.GetDAGRunDetails200JSONResponse, error) {
	attempt, dagStatus, err := a.loadRootDAGRunDetailsAttemptAndStatus(ctx, dagName, dagRunId)
	if err != nil {
		return api.GetDAGRunDetails200JSONResponse{}, err
	}
	return api.GetDAGRunDetails200JSONResponse{
		DagRunDetails: a.toDAGRunDetailsWithSpecSource(ctx, attempt, *dagStatus),
	}, nil
}

func (a *API) loadRootDAGRunDetailsAttemptAndStatus(
	ctx context.Context,
	dagName, dagRunId string,
) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	if dagRunId == "latest" {
		attempt, err := a.dagRunStore.LatestAttempt(ctx, dagName)
		if err != nil {
			return nil, nil, fmt.Errorf("no dag-runs found for DAG %s", dagName)
		}

		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting latest status: %w", err)
		}
		if status == nil {
			return nil, nil, fmt.Errorf("latest dag-run status is unavailable for DAG %s", dagName)
		}
		if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(status)); err != nil {
			return nil, nil, err
		}

		ref := exec.NewDAGRunRef(dagName, status.DAGRunID)
		dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting latest status: %w", err)
		}
		if dagStatus == nil {
			return nil, nil, fmt.Errorf("latest dag-run status is unavailable for DAG %s", dagName)
		}

		return attempt, a.repairConfirmedStaleDistributedRunOnRead(ctx, dagStatus, attempt.ID()), nil
	}

	ref := exec.NewDAGRunRef(dagName, dagRunId)
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("dag-run ID %s not found for DAG %s", dagRunId, dagName)
	}

	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("dag-run ID %s not found for DAG %s", dagRunId, dagName)
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return nil, nil, err
	}

	return attempt, a.repairConfirmedStaleDistributedRunOnRead(ctx, dagStatus, attempt.ID()), nil
}

func (a *API) repairConfirmedStaleDistributedRunOnRead(
	ctx context.Context,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) *exec.DAGRunStatus {
	if status == nil || a.dagRunLeaseStore == nil || a.workerHeartbeatStore == nil {
		return status
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	fallbackWorkerID := status.WorkerID
	if fallbackWorkerID == "" {
		fallbackWorkerID = a.distributedFallbackWorkerIDFromLease(ctx, status, attemptID)
	}

	reconciled, _, err := runtime.ConfirmAndRepairStaleDistributedRun(ctx, runtime.DistributedRunRepairConfig{
		DAGRunStore:          a.dagRunStore,
		DAGRunLeaseStore:     a.dagRunLeaseStore,
		WorkerHeartbeatStore: a.workerHeartbeatStore,
		StaleLeaseThreshold:  a.leaseStaleThreshold,
	}, status, attemptID, fallbackWorkerID)
	if err != nil {
		logger.Warn(ctx, "Failed to auto-repair stale distributed run on read",
			tag.DAG(status.Name),
			tag.RunID(status.DAGRunID),
			tag.AttemptID(status.AttemptID),
			tag.Error(err),
		)
		return status
	}
	if reconciled != nil {
		return reconciled
	}
	return status
}

func (a *API) distributedFallbackWorkerIDFromLease(
	ctx context.Context,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) string {
	if status == nil || status.WorkerID != "" || a.dagRunLeaseStore == nil {
		return ""
	}

	attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return ""
	}

	lease, err := a.dagRunLeaseStore.Get(ctx, attemptKey)
	if err != nil || lease == nil || !exec.IsRemoteWorkerID(lease.WorkerID) {
		return ""
	}

	return lease.WorkerID
}

func (a *API) toDAGRunDetailsWithSpecSource(ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus) api.DAGRunDetails {
	details := ToDAGRunDetails(status)
	specFromFile, sourceFileName := a.dagRunSourceInfo(ctx, attempt)
	details.SpecFromFile = ptrOf(specFromFile)
	if sourceFileName != "" {
		details.SourceFileName = ptrOf(sourceFileName)
	}
	return details
}

// GetDAGRunSpec returns the YAML spec used for a specific DAG-run.
// It reads from the DAG-run attempt's YamlData field to ensure we return
// the exact spec used at execution time, not the current spec.
func (a *API) GetDAGRunSpec(ctx context.Context, request api.GetDAGRunSpecRequestObject) (api.GetDAGRunSpecResponseObject, error) {
	var attempt exec.DAGRunAttempt
	var err error
	var notFoundMsg string

	if request.DagRunId == "latest" {
		attempt, err = a.dagRunStore.LatestAttempt(ctx, request.Name)
		notFoundMsg = fmt.Sprintf("no dag-runs found for DAG %s", request.Name)
	} else {
		attempt, err = a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(request.Name, request.DagRunId))
		notFoundMsg = fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name)
	}

	if err != nil {
		return &api.GetDAGRunSpec404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: notFoundMsg,
		}, nil
	}
	workspaceName, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}

	spec, err := a.getSpecFromAttempt(ctx, attempt)
	if err != nil {
		return &api.GetDAGRunSpec404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("DAG spec not found for dag-run %s", request.DagRunId),
		}, nil
	}

	return &api.GetDAGRunSpec200JSONResponse{
		Spec: spec,
	}, nil
}

// GetSubDAGRunDetails implements api.StrictServerInterface.
func (a *API) GetSubDAGRunDetails(ctx context.Context, request api.GetSubDAGRunDetailsRequestObject) (api.GetSubDAGRunDetailsResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint:    "/dag-runs/{name}/{dagRunId}/sub/{subDAGRunId}",
		dagName:     request.Name,
		dagRunID:    request.DagRunId,
		subDAGRunID: request.SubDAGRunId,
	}, func(readCtx context.Context) (*exec.DAGRunStatus, error) {
		return a.getReferencedDAGRunStatus(readCtx, root, request.SubDAGRunId, "")
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return &api.GetSubDAGRunDetailsdefaultJSONResponse{
				StatusCode: statusClientClosedRequest,
				Body:       dagRunReadCanceledResponse("sub dag-run details request canceled"),
			}, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return &api.GetSubDAGRunDetailsdefaultJSONResponse{
				StatusCode: http.StatusGatewayTimeout,
				Body:       dagRunReadTimeoutResponse("sub dag-run details request timed out"),
			}, nil
		}
		return &api.GetSubDAGRunDetails404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}
	return &api.GetSubDAGRunDetails200JSONResponse{
		DagRunDetails: ToDAGRunDetails(*dagStatus),
	}, nil
}

// GetSubDAGRunSpec returns the YAML spec used for a specific sub-DAG run.
func (a *API) GetSubDAGRunSpec(ctx context.Context, request api.GetSubDAGRunSpecRequestObject) (api.GetSubDAGRunSpecResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	attempt, err := a.getReferencedDAGRunAttempt(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.GetSubDAGRunSpec404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	workspaceName, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return nil, err
	}

	spec, err := a.getSpecFromAttempt(ctx, attempt)
	if err != nil {
		return &api.GetSubDAGRunSpec404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("DAG spec not found for sub dag-run %s", request.SubDAGRunId),
		}, nil
	}

	return &api.GetSubDAGRunSpec200JSONResponse{
		Spec: spec,
	}, nil
}

// getSpecFromAttempt reads YAML spec from DAG run attempt.
// Returns spec string and nil error on success, or empty string and error on failure.
func (a *API) getSpecFromAttempt(ctx context.Context, attempt exec.DAGRunAttempt) (string, error) {
	dag, err := attempt.ReadDAG(ctx)
	if err != nil || dag == nil || len(dag.YamlData) == 0 {
		return "", fmt.Errorf("DAG spec not found")
	}
	return string(dag.YamlData), nil
}

func (a *API) GetSubDAGRunLog(ctx context.Context, request api.GetSubDAGRunLogRequestObject) (api.GetSubDAGRunLogResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.GetSubDAGRunLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	options := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(dagStatus.Log, options)
	if err != nil {
		if strings.Contains(err.Error(), "file not found") {
			return &api.GetSubDAGRunLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", dagStatus.Log, err)
	}

	return &api.GetSubDAGRunLog200JSONResponse{
		Content:    content,
		LineCount:  ptrOf(lineCount),
		TotalLines: ptrOf(totalLines),
		HasMore:    ptrOf(hasMore),
		IsEstimate: ptrOf(isEstimate),
	}, nil
}

func (a *API) DownloadSubDAGRunLog(ctx context.Context, request api.DownloadSubDAGRunLogRequestObject) (api.DownloadSubDAGRunLogResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.DownloadSubDAGRunLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(dagStatus.Log)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &api.DownloadSubDAGRunLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", dagStatus.Log, err)
	}

	filename := fmt.Sprintf("%s-%s-sub-%s-scheduler.log", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId), sanitizeFilename(request.SubDAGRunId))
	return &api.DownloadSubDAGRunLog200TextResponse{
		Body: string(content),
		Headers: api.DownloadSubDAGRunLog200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", filename),
		},
	}, nil
}

func (a *API) GetSubDAGRunArtifacts(ctx context.Context, request api.GetSubDAGRunArtifactsRequestObject) (api.GetSubDAGRunArtifactsResponseObject, error) {
	status, err := a.getSubDAGRunArtifactStatus(ctx, request.Name, request.DagRunId, request.SubDAGRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return &api.GetSubDAGRunArtifacts404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("get sub dag-run artifact status: %w", err)
	}
	if err := a.requireDAGRunStatusVisible(ctx, status); err != nil {
		return nil, err
	}

	items, err := listArtifactTree(status.ArchiveDir, artifactListRecursive(request.Params.Recursive))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return &api.GetSubDAGRunArtifacts404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("list sub dag-run artifacts: %w", err)
	}

	return &api.GetSubDAGRunArtifacts200JSONResponse{
		Items: items,
	}, nil
}

func (a *API) GetSubDAGRunArtifactPreview(ctx context.Context, request api.GetSubDAGRunArtifactPreviewRequestObject) (api.GetSubDAGRunArtifactPreviewResponseObject, error) {
	status, err := a.getSubDAGRunArtifactStatus(ctx, request.Name, request.DagRunId, request.SubDAGRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return &api.GetSubDAGRunArtifactPreview404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("get sub dag-run artifact status: %w", err)
	}
	if err := a.requireDAGRunStatusVisible(ctx, status); err != nil {
		return nil, err
	}

	preview, err := buildArtifactPreview(status.ArchiveDir, string(request.Params.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return &api.GetSubDAGRunArtifactPreview404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact file not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("preview sub dag-run artifact: %w", err)
	}

	return api.GetSubDAGRunArtifactPreview200JSONResponse(preview), nil
}

func (a *API) DownloadSubDAGRunArtifact(ctx context.Context, request api.DownloadSubDAGRunArtifactRequestObject) (api.DownloadSubDAGRunArtifactResponseObject, error) {
	status, err := a.getSubDAGRunArtifactStatus(ctx, request.Name, request.DagRunId, request.SubDAGRunId)
	if err != nil {
		if isArtifactStatusNotFound(err) {
			return &api.DownloadSubDAGRunArtifact404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact directory not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("get sub dag-run artifact status: %w", err)
	}
	if err := a.requireDAGRunStatusVisible(ctx, status); err != nil {
		return nil, err
	}

	file, info, err := openArtifactFile(status.ArchiveDir, string(request.Params.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errArtifactUnavailable) {
			return &api.DownloadSubDAGRunArtifact404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("artifact file not found for sub dag-run %s", request.SubDAGRunId),
			}, nil
		}
		return nil, fmt.Errorf("open sub dag-run artifact: %w", err)
	}

	return &api.DownloadSubDAGRunArtifact200ApplicationoctetStreamResponse{
		Body: file,
		Headers: api.DownloadSubDAGRunArtifact200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", sanitizeFilename(info.Name())),
		},
		ContentLength: info.Size(),
	}, nil
}

func (a *API) GetSubDAGRunStepLog(ctx context.Context, request api.GetSubDAGRunStepLogRequestObject) (api.GetSubDAGRunStepLogResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.GetSubDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return &api.GetSubDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	options := a.buildLogReadOptions(request.Params.Head, request.Params.Tail, request.Params.Offset, request.Params.Limit)
	logFile := selectLogFile(node, *request.Params.Stream)

	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(logFile, options)
	if err != nil {
		if strings.Contains(err.Error(), "file not found") {
			return &api.GetSubDAGRunStepLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for step %s", request.StepName),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", logFile, err)
	}

	return &api.GetSubDAGRunStepLog200JSONResponse{
		Content:    content,
		LineCount:  ptrOf(lineCount),
		TotalLines: ptrOf(totalLines),
		HasMore:    ptrOf(hasMore),
		IsEstimate: ptrOf(isEstimate),
	}, nil
}

func (a *API) DownloadSubDAGRunStepLog(ctx context.Context, request api.DownloadSubDAGRunStepLogRequestObject) (api.DownloadSubDAGRunStepLogResponseObject, error) {
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	dagStatus, err := a.getReferencedDAGRunStatus(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.DownloadSubDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	node, err := dagStatus.NodeByName(request.StepName)
	if err != nil {
		return &api.DownloadSubDAGRunStepLog404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	logFile, streamName := node.Stdout, "stdout"
	if request.Params.Stream != nil && *request.Params.Stream == api.StreamStderr {
		logFile, streamName = node.Stderr, "stderr"
	}

	content, err := os.ReadFile(filepath.Clean(logFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &api.DownloadSubDAGRunStepLog404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("log file not found for step %s", request.StepName),
			}, nil
		}
		return nil, fmt.Errorf("error reading %s: %w", logFile, err)
	}

	filename := fmt.Sprintf("%s-%s-sub-%s-%s-%s.log", sanitizeFilename(request.Name), sanitizeFilename(request.DagRunId), sanitizeFilename(request.SubDAGRunId), sanitizeFilename(request.StepName), streamName)
	return &api.DownloadSubDAGRunStepLog200TextResponse{
		Body: string(content),
		Headers: api.DownloadSubDAGRunStepLog200ResponseHeaders{
			ContentDisposition: fmt.Sprintf("attachment; filename=\"%s\"", filename),
		},
	}, nil
}

func (a *API) UpdateSubDAGRunStepStatus(ctx context.Context, request api.UpdateSubDAGRunStepStatusRequestObject) (api.UpdateSubDAGRunStepStatusResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	root := exec.NewDAGRunRef(request.Name, request.DagRunId)
	mutationRef, dagStatus, err := a.getReferencedDAGRunStatusWithRef(ctx, root, request.SubDAGRunId, "")
	if err != nil {
		return &api.UpdateSubDAGRunStepStatus404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("sub dag-run ID %s not found for DAG %s", request.SubDAGRunId, request.Name),
		}, nil
	}
	if err := a.requireDAGRunStatusExecute(ctx, dagStatus); err != nil {
		return nil, err
	}
	if dagStatus.Status == core.Running {
		return &api.UpdateSubDAGRunStepStatus400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: fmt.Sprintf("dag-run ID %s for DAG %s is still running", request.DagRunId, request.Name),
		}, nil
	}

	stepIdx := findStepByName(dagStatus.Nodes, request.StepName)
	if stepIdx < 0 {
		return &api.UpdateSubDAGRunStepStatus404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: fmt.Sprintf("step %s not found in DAG %s", request.StepName, request.Name),
		}, nil
	}

	dagStatus.Nodes[stepIdx].Status = nodeStatusMapping[request.Body.Status]
	dagStatus.Status = deriveManualDAGRunStatus(dagStatus.Nodes, dagStatus.Status)

	if err := a.updateDAGRunStatus(ctx, mutationRef, *dagStatus); err != nil {
		return nil, fmt.Errorf("error updating status: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "sub_dag_step_status_update", map[string]any{
		"dag_name":       request.Name,
		"dag_run_id":     request.DagRunId,
		"sub_dag_run_id": request.SubDAGRunId,
		"step_name":      request.StepName,
		"new_status":     nodeStatusMapping[request.Body.Status].String(),
	})

	return &api.UpdateSubDAGRunStepStatus200Response{}, nil
}

var nodeStatusMapping = map[api.NodeStatus]core.NodeStatus{
	api.NodeStatusNotStarted:     core.NodeNotStarted,
	api.NodeStatusRunning:        core.NodeRunning,
	api.NodeStatusFailed:         core.NodeFailed,
	api.NodeStatusAborted:        core.NodeAborted,
	api.NodeStatusSuccess:        core.NodeSucceeded,
	api.NodeStatusSkipped:        core.NodeSkipped,
	api.NodeStatusPartialSuccess: core.NodePartiallySucceeded,
	api.NodeStatusWaiting:        core.NodeWaiting,
	api.NodeStatusRejected:       core.NodeRejected,
	api.NodeStatusRetrying:       core.NodeRetrying,
}

func deriveManualDAGRunStatus(nodes []*exec.Node, fallback core.Status) core.Status {
	if len(nodes) == 0 {
		return fallback
	}

	var (
		hasRunning              bool
		hasRetrying             bool
		hasWaiting              bool
		hasRejected             bool
		hasFailed               bool
		hasUncontinuableFailure bool
		hasContinuableFailure   bool
		hasAborted              bool
		hasPartial              bool
		hasSuccess              bool
		hasNotStarted           bool
	)

	for _, node := range nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case core.NodeRunning:
			hasRunning = true
		case core.NodeRetrying:
			hasRetrying = true
		case core.NodeWaiting:
			hasWaiting = true
		case core.NodeRejected:
			hasRejected = true
		case core.NodeFailed:
			hasFailed = true
			if node.Step.ContinueOn.Failure {
				hasContinuableFailure = true
			} else {
				hasUncontinuableFailure = true
			}
		case core.NodeAborted:
			hasAborted = true
		case core.NodePartiallySucceeded:
			hasPartial = true
			hasSuccess = true
		case core.NodeSucceeded, core.NodeSkipped:
			hasSuccess = true
		case core.NodeNotStarted:
			hasNotStarted = true
		}
	}

	switch {
	case hasRunning:
		return core.Running
	case hasRetrying:
		return core.Running
	case hasWaiting:
		return core.Waiting
	case hasRejected:
		return core.Rejected
	case hasFailed && hasUncontinuableFailure:
		return core.Failed
	case hasFailed && hasContinuableFailure && hasSuccess:
		return core.PartiallySucceeded
	case hasFailed:
		return core.Failed
	case hasAborted:
		return core.Aborted
	case hasPartial:
		return core.PartiallySucceeded
	case hasNotStarted && !hasSuccess:
		return core.NotStarted
	case hasNotStarted:
		return core.PartiallySucceeded
	default:
		return core.Succeeded
	}
}

func (a *API) RetryDAGRun(ctx context.Context, request api.RetryDAGRunRequestObject) (api.RetryDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	retryDagRunID := request.DagRunId
	stepName := ""
	if request.Body != nil {
		if request.Body.DagRunId != "" && request.DagRunId != "" && request.Body.DagRunId != request.DagRunId {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "dagRunId in the request body must match the path parameter",
			}
		}
		if request.Body.DagRunId != "" {
			retryDagRunID = request.Body.DagRunId
		}
		stepName = valueOf(request.Body.StepName)
	}

	if _, err := a.retryDAGRun(ctx, request.Name, request.DagRunId, retryDagRunID, stepName); err != nil {
		return nil, err
	}

	return api.RetryDAGRun200Response{}, nil
}

type retryDAGRunResult struct {
	queued bool
}

func (a *API) resolveAttemptForDAGRun(
	ctx context.Context,
	dagName, dagRunID string,
) (exec.DAGRunAttempt, string, error) {
	if dagRunID != "latest" {
		attempt, err := a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dagName, dagRunID))
		if err != nil {
			return nil, "", &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("dag-run ID %s not found for DAG %s", dagRunID, dagName),
			}
		}
		return attempt, dagRunID, nil
	}

	attempt, err := a.dagRunStore.LatestAttempt(ctx, dagName)
	if err != nil {
		return nil, "", &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("no dag-runs found for DAG %s", dagName),
		}
	}
	if attempt == nil {
		return nil, "", &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("no dag-runs found for DAG %s", dagName),
		}
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read latest dag-run status: %w", err)
	}
	if status == nil || status.DAGRunID == "" {
		return nil, "", fmt.Errorf("failed to read latest dag-run status: status data is nil")
	}

	return attempt, status.DAGRunID, nil
}

func (a *API) retryDAGRun(ctx context.Context, dagName, dagRunID, retryDagRunID, stepName string) (retryDAGRunResult, error) {
	if retryDagRunID == "" {
		retryDagRunID = dagRunID
	}

	attempt, sourceDagRunID, err := a.resolveAttemptForDAGRun(ctx, dagName, dagRunID)
	if err != nil {
		return retryDAGRunResult{}, err
	}
	workspaceName, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return retryDAGRunResult{}, err
	}
	if err := a.requireExecuteForWorkspace(ctx, workspaceName); err != nil {
		return retryDAGRunResult{}, err
	}

	if retryDagRunID == "" || retryDagRunID == dagRunID {
		retryDagRunID = sourceDagRunID
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return retryDAGRunResult{}, fmt.Errorf("error reading DAG: %w", err)
	}

	// For DAGs using a global queue, enqueue the retry so it respects queue capacity.
	// Step retry is not supported via queue (queue processor does not pass step name).
	if stepName == "" && a.config.FindQueueConfig(dag.ProcGroup()) != nil {
		if err := a.enqueueRetry(ctx, attempt, dag); err != nil {
			return retryDAGRunResult{}, err
		}
		a.logRetryAudit(ctx, dagName, sourceDagRunID, stepName, false)
		return retryDAGRunResult{queued: true}, nil
	}

	// Check if this DAG should be dispatched to the coordinator for distributed execution
	if core.ShouldDispatchToCoordinator(dag, a.coordinatorCli != nil, a.defaultExecMode) {
		// Get previous status for retry context
		prevStatus, err := attempt.ReadStatus(ctx)
		if err != nil {
			return retryDAGRunResult{}, fmt.Errorf("error reading status: %w", err)
		}

		// Create and dispatch retry task to coordinator
		opts := []executor.TaskOption{
			executor.WithWorkerSelector(dag.WorkerSelector),
			executor.WithPreviousStatus(prevStatus),
			executor.WithBaseConfig(executor.ResolveBaseConfig(dag.BaseConfigData, a.config.Paths.BaseConfig)),
		}
		if dag.SourceFile != "" {
			opts = append(opts, executor.WithSourceFile(dag.SourceFile))
		}
		if stepName != "" {
			opts = append(opts, executor.WithStep(stepName))
		}
		if snapshot, err := agentsnapshot.BuildFromPaths(ctx, dag, a.config.Paths, a.dagStore); err != nil {
			return retryDAGRunResult{}, fmt.Errorf("build distributed agent snapshot: %w", err)
		} else if len(snapshot) > 0 {
			opts = append(opts, executor.WithAgentSnapshot(snapshot))
		}

		task := executor.CreateTask(
			dag.Name,
			string(dag.YamlData),
			coordinatorv1.Operation_OPERATION_RETRY,
			retryDagRunID,
			opts...,
		)

		if err := a.coordinatorCli.Dispatch(ctx, task); err != nil {
			return retryDAGRunResult{}, fmt.Errorf("error dispatching retry to coordinator: %w", err)
		}

		a.logRetryAudit(ctx, dagName, sourceDagRunID, stepName, true)
		return retryDAGRunResult{}, nil
	}

	// Local retry path: launch the retry subprocess asynchronously so the API
	// returns immediately instead of blocking until the DAG run completes.
	prevStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return retryDAGRunResult{}, fmt.Errorf("error reading status: %w", err)
	}

	prepared, err := a.prepareRetryDAGForSubprocess(ctx, dag, prevStatus)
	if err != nil {
		return retryDAGRunResult{}, fmt.Errorf("error preparing DAG retry env: %w", err)
	}

	spec := a.subCmdBuilder.Retry(prepared, retryDagRunID, stepName)
	if err := runtime.Start(ctx, spec); err != nil {
		return retryDAGRunResult{}, fmt.Errorf("error retrying DAG: %w", err)
	}

	// Wait briefly for the retry process to start, matching the pattern used
	// by the start endpoint to confirm the subprocess launched successfully.
	a.waitForRetryStarted(ctx, dag, retryDagRunID)

	a.logRetryAudit(ctx, dagName, sourceDagRunID, stepName, false)
	return retryDAGRunResult{}, nil
}

// enqueueRetry enqueues the retry and persists Queued status via exec.EnqueueRetry.
// Retries respect global queue capacity because the queue processor picks them up
// when capacity is available.
func (a *API) enqueueRetry(ctx context.Context, attempt exec.DAGRunAttempt, dag *core.DAG) error {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("error reading status: %w", err)
	}
	eventCtx := a.withEventContext(ctx)
	if err := exec.EnqueueRetry(eventCtx, a.dagRunStore, a.queueStore, dag, status, exec.EnqueueRetryOptions{}); err != nil {
		if errors.Is(err, exec.ErrRetryStaleLatest) {
			return &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "dag-run state changed before retry could be queued",
			}
		}
		return err
	}
	return nil
}

func (a *API) logRetryAudit(ctx context.Context, dagName, dagRunID, stepName string, distributed bool) {
	detailsMap := map[string]any{
		"dag_name":    dagName,
		"dag_run_id":  dagRunID,
		"distributed": distributed,
	}
	if stepName != "" {
		detailsMap["step_name"] = stepName
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_retry", detailsMap)
}

// waitForRetryStarted polls briefly to confirm the retry subprocess has started.
// This is best-effort: if the timeout elapses we still return 200 since the
// subprocess was spawned successfully by runtime.Start.
func (a *API) waitForRetryStarted(ctx context.Context, dag *core.DAG, dagRunID string) {
	const (
		timeout      = 3 * time.Second
		pollInterval = 100 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		default:
			status, _ := a.dagRunMgr.GetCurrentStatus(ctx, dag, dagRunID)
			if status != nil && (status.Status == core.Running || status.Status == core.Queued) {
				return
			}
			time.Sleep(pollInterval)
		}
	}
}

func failedAutoRetryCancelError(status *exec.DAGRunStatus) *Error {
	switch exec.FailedAutoRetryCancelEligibilityOf(status) {
	case exec.FailedAutoRetryCancelEligible:
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed DAG run cannot be canceled",
		}
	case exec.FailedAutoRetryCancelMissingStatus:
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed DAG run cannot be canceled: missing status",
		}
	case exec.FailedAutoRetryCancelNotRoot:
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "only root DAG runs with pending auto-retries can be canceled after failure",
		}
	case exec.FailedAutoRetryCancelNotPending:
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed DAG run is not pending auto-retry and cannot be canceled",
		}
	default:
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "failed DAG run cannot be canceled",
		}
	}
}

func failedAutoRetryCancelStateChangedError(updatedStatus *exec.DAGRunStatus) *Error {
	currentStatus := "unknown"
	if updatedStatus != nil {
		currentStatus = updatedStatus.Status.String()
	}
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message: fmt.Sprintf(
			"dag-run state changed before cancel could be applied; current status is %s. Refresh and try again.",
			currentStatus,
		),
	}
}

func (a *API) TerminateDAGRun(ctx context.Context, request api.TerminateDAGRunRequestObject) (api.TerminateDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := exec.NewDAGRunRef(request.Name, request.DagRunId)
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
		}
	}

	// Get saved status to check if it's a distributed DAG
	savedStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG status not found for %s", request.Name),
		}
	}
	if err := a.requireDAGRunStatusExecute(ctx, savedStatus); err != nil {
		return nil, err
	}

	terminateMode := "running_stop"
	if savedStatus.Status == core.Failed {
		if !exec.CanCancelFailedAutoRetryPendingRun(savedStatus) {
			return nil, failedAutoRetryCancelError(savedStatus)
		}
		if err := exec.CancelFailedAutoRetryPendingRun(ctx, a.dagRunStore, savedStatus); err != nil {
			var stateChangedErr *exec.FailedAutoRetryCancelStateChangedError
			if errors.As(err, &stateChangedErr) {
				return nil, failedAutoRetryCancelStateChangedError(stateChangedErr.CurrentStatus)
			}
			return nil, err
		}
		terminateMode = "failed_auto_retry_cancel"
	} else {
		dag, err := attempt.ReadDAG(ctx)
		if err != nil {
			return nil, fmt.Errorf("error reading DAG: %w", err)
		}

		if core.ShouldDispatchToCoordinator(dag, a.coordinatorCli != nil, a.defaultExecMode) {
			// For distributed DAGs, use saved status for running check
			if savedStatus.Status != core.Running {
				return nil, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeNotRunning,
					Message:    "DAG is not running",
				}
			}
			// Send cancel request via coordinator
			if a.coordinatorCli == nil {
				return nil, &Error{
					HTTPStatus: http.StatusServiceUnavailable,
					Code:       api.ErrorCodeInternalError,
					Message:    "coordinator not configured for distributed DAG cancellation",
				}
			}
			if err := a.coordinatorCli.RequestCancel(ctx, request.Name, request.DagRunId, nil); err != nil {
				return nil, fmt.Errorf("error requesting cancel: %w", err)
			}
		} else {
			// For local DAGs, use existing logic with GetCurrentStatus and socket
			dagStatus, err := a.dagRunMgr.GetCurrentStatus(ctx, dag, request.DagRunId)
			if err != nil {
				return nil, &Error{
					HTTPStatus: http.StatusNotFound,
					Code:       api.ErrorCodeNotFound,
					Message:    fmt.Sprintf("DAG %s not found", request.Name),
				}
			}

			if dagStatus.Status != core.Running {
				return nil, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeNotRunning,
					Message:    "DAG is not running",
				}
			}

			if err := a.dagRunMgr.Stop(ctx, dag, dagStatus.DAGRunID); err != nil {
				return nil, fmt.Errorf("error stopping DAG: %w", err)
			}
		}
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_terminate", map[string]any{
		"dag_name":       request.Name,
		"dag_run_id":     request.DagRunId,
		"terminate_mode": terminateMode,
	})

	return api.TerminateDAGRun200Response{}, nil
}

func (a *API) DequeueDAGRun(ctx context.Context, request api.DequeueDAGRunRequestObject) (api.DequeueDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	dagRun := exec.NewDAGRunRef(request.Name, request.DagRunId)
	workspaceName, err := a.workspaceNameForDAGRun(ctx, dagRun)
	if err != nil {
		return nil, mapAbortQueuedDAGRunAPIError(request.Name, request.DagRunId, err)
	}
	if err := a.requireExecuteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	queueName, err := a.queueNameForDAGRun(ctx, dagRun)
	if err != nil {
		return nil, mapAbortQueuedDAGRunAPIError(request.Name, request.DagRunId, err)
	}

	if err := a.procStore.Lock(ctx, queueName); err != nil {
		return nil, fmt.Errorf("failed to lock process group %s: %w", queueName, err)
	}
	defer a.procStore.Unlock(ctx, queueName)

	if err := exec.AbortQueuedDAGRun(ctx, a.dagRunStore, dagRun); err != nil {
		return nil, mapAbortQueuedDAGRunAPIError(request.Name, request.DagRunId, err)
	}
	if _, err := a.queueStore.DequeueByDAGRunID(ctx, queueName, dagRun); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
		return nil, fmt.Errorf("error dequeueing dag-run: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_dequeue", map[string]any{
		"dag_name":   request.Name,
		"dag_run_id": request.DagRunId,
	})

	return api.DequeueDAGRun200Response{}, nil
}

func (a *API) RescheduleDAGRun(ctx context.Context, request api.RescheduleDAGRunRequestObject) (api.RescheduleDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	var opts rescheduleDAGRunOptions
	if body := request.Body; body != nil {
		if body.DagName != nil {
			opts.nameOverride = *body.DagName
		}
		if body.DagRunId != nil {
			opts.newDagRunID = *body.DagRunId
		}
		if body.UseCurrentDagFile != nil {
			opts.useCurrentDAGFile = *body.UseCurrentDagFile
		}
	}

	result, err := a.rescheduleDAGRun(ctx, request.Name, request.DagRunId, opts)
	if err != nil {
		return nil, err
	}

	return api.RescheduleDAGRun200JSONResponse{
		DagRunId: result.newDagRunID,
		Queued:   result.queued,
	}, nil
}

type rescheduleDAGRunOptions struct {
	nameOverride      string
	newDagRunID       string
	useCurrentDAGFile bool
}

type rescheduleDAGRunResult struct {
	newDagRunID string
	queued      bool
}

func (a *API) rescheduleDAGRun(ctx context.Context, dagName, dagRunID string, opts rescheduleDAGRunOptions) (rescheduleDAGRunResult, error) {
	if !a.config.Queues.Enabled {
		return rescheduleDAGRunResult{}, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "reschedule requires queues to be enabled",
		}
	}

	attempt, sourceDagRunID, err := a.resolveAttemptForDAGRun(ctx, dagName, dagRunID)
	if err != nil {
		return rescheduleDAGRunResult{}, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to read status: %w", err)
	}
	if status == nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to read status: status data is nil")
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return rescheduleDAGRunResult{}, err
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to read DAG snapshot: %w", err)
	}
	if dag == nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to read DAG snapshot: DAG data is nil")
	}
	storedSourceFile := dag.SourceFile

	snapshotDAG, preservedSnapshotParams, err := restoreDAGRunSnapshot(ctx, dag, status)
	if err != nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to restore DAG snapshot: %w", err)
	}

	nameOverride := strings.TrimSpace(opts.nameOverride)
	if nameOverride != "" {
		if err := core.ValidateDAGName(nameOverride); err != nil {
			return rescheduleDAGRunResult{}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
	}
	currentFileParams := status.Params
	if currentFileParams == "" {
		currentFileParams = preservedSnapshotParams
	}

	newDagRunID := strings.TrimSpace(opts.newDagRunID)
	if err := validateDAGRunID(newDagRunID); err != nil {
		return rescheduleDAGRunResult{}, err
	}
	if newDagRunID == "" {
		id, genErr := a.dagRunMgr.GenDAGRunID(ctx)
		if genErr != nil {
			return rescheduleDAGRunResult{}, fmt.Errorf("error generating dag-run ID: %w", genErr)
		}
		newDagRunID = id
	}

	var cleanup func()
	cleanup = func() {}
	defer func() {
		cleanup()
	}()

	if opts.useCurrentDAGFile {
		dag, err = a.loadCurrentRescheduleDAG(ctx, storedSourceFile, nameOverride)
		if err != nil {
			return rescheduleDAGRunResult{}, err
		}
	} else {
		if len(snapshotDAG.YamlData) == 0 {
			return rescheduleDAGRunResult{}, fmt.Errorf("failed to enqueue dag-run: DAG snapshot YAML is missing")
		}
		inlineName := snapshotDAG.Name
		if nameOverride != "" {
			inlineName = nameOverride
		}
		snapshotLoadOpts := []spec.LoadOption{
			spec.WithDAGsDir(a.config.Paths.DAGsDir),
		}
		if len(snapshotDAG.BaseConfigData) > 0 {
			snapshotLoadOpts = append(snapshotLoadOpts, spec.WithBaseConfigContent(snapshotDAG.BaseConfigData))
		} else {
			snapshotLoadOpts = append(snapshotLoadOpts,
				spec.WithBaseConfig(a.config.Paths.BaseConfig),
				spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(a.config.Paths.DAGsDir)),
			)
		}

		dag, cleanup, err = a.loadInlineDAG(ctx, string(snapshotDAG.YamlData), &inlineName, newDagRunID, snapshotLoadOpts...)
		if err != nil {
			return rescheduleDAGRunResult{}, fmt.Errorf("failed to prepare reschedule snapshot: %w", err)
		}
		dag.SourceFile = snapshotDAG.SourceFile
	}

	if err := a.ensureDAGRunIDUnique(ctx, dag, newDagRunID); err != nil {
		return rescheduleDAGRunResult{}, err
	}

	logger.Info(ctx, "Rescheduling dag-run",
		tag.DAG(dag.Name),
		slog.String("from-dag-run-id", sourceDagRunID),
		tag.RunID(newDagRunID))

	paramsToUse := preservedSnapshotParams
	if opts.useCurrentDAGFile {
		paramsToUse = currentFileParams
	}
	var enqueueErr error
	if opts.useCurrentDAGFile {
		enqueueErr = a.enqueueDAGRun(ctx, dag, paramsToUse, newDagRunID, "", core.TriggerTypeManual, "")
	} else {
		enqueueErr = a.enqueuePreparedDAGRun(ctx, dag, paramsToUse, newDagRunID, core.TriggerTypeManual)
	}
	if enqueueErr != nil {
		return rescheduleDAGRunResult{}, fmt.Errorf("failed to enqueue dag-run: %w", enqueueErr)
	}

	queued := false
	if dagStatus, _ := a.dagRunMgr.GetCurrentStatus(ctx, dag, newDagRunID); dagStatus != nil {
		queued = dagStatus.Status == core.Queued
	}

	detailsMap := map[string]any{
		"dag_name":        dagName,
		"from_dag_run_id": sourceDagRunID,
		"new_dag_run_id":  newDagRunID,
		"queued":          queued,
	}
	if nameOverride != "" {
		detailsMap["name_override"] = nameOverride
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_reschedule", detailsMap)

	return rescheduleDAGRunResult{
		newDagRunID: newDagRunID,
		queued:      queued,
	}, nil
}

func (a *API) enqueuePreparedDAGRun(
	ctx context.Context,
	dag *core.DAG,
	params string,
	dagRunID string,
	triggerType core.TriggerType,
) error {
	resolvedDAG, err := spec.ResolveRuntimeParams(ctx, dag, params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	dag = resolvedDAG

	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return err
	}
	if err := core.ValidateStartParams(dag.DefaultParams, core.StartParamInput{
		RawParams: params,
	}); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	queuedDAG := dag.Clone()
	queuedDAG.Location = ""

	now := time.Now()
	attempt, err := a.dagRunStore.CreateAttempt(ctx, queuedDAG, now, dagRunID, exec.NewDAGRunAttemptOptions{})
	if err != nil {
		return fmt.Errorf("failed to create queued dag-run attempt: %w", err)
	}

	logFile, err := logpath.Generate(ctx, a.config.Paths.LogDir, queuedDAG.LogDir, queuedDAG.Name, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to generate queued dag-run log file: %w", err)
	}
	artifactDir, err := artifactDirForQueuedDAGRun(ctx, a.config.Paths.ArtifactDir, queuedDAG, dagRunID)
	if err != nil {
		return err
	}

	statusOpts := []transform.StatusOption{
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(artifactDir),
		transform.WithAttemptID(attempt.ID()),
		transform.WithQueuedAt(stringutil.FormatTime(now)),
		transform.WithPreconditions(queuedDAG.Preconditions),
		transform.WithHierarchyRefs(
			exec.NewDAGRunRef(queuedDAG.Name, dagRunID),
			exec.DAGRunRef{},
		),
		transform.WithTriggerType(triggerType),
	}
	status := transform.NewStatusBuilder(queuedDAG).Create(dagRunID, core.Queued, 0, time.Time{}, statusOpts...)

	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open queued dag-run attempt: %w", err)
	}
	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return fmt.Errorf("failed to save queued dag-run status: %w", err)
	}
	closeErr := attempt.Close(ctx)
	if closeErr != nil {
		logger.Warn(ctx, "Failed to close queued dag-run status before enqueue",
			tag.Error(closeErr))
	}

	dagRun := exec.NewDAGRunRef(queuedDAG.Name, dagRunID)
	if err := a.queueStore.Enqueue(ctx, queuedDAG.ProcGroup(), exec.QueuePriorityLow, dagRun); err != nil {
		if closeErr != nil {
			return errors.Join(
				fmt.Errorf("failed to close queued dag-run attempt: %w", closeErr),
				fmt.Errorf("failed to enqueue dag-run: %w", err),
			)
		}
		return fmt.Errorf("failed to enqueue dag-run: %w", err)
	}

	return nil
}

func artifactDirForQueuedDAGRun(ctx context.Context, baseDir string, dag *core.DAG, dagRunID string) (string, error) {
	if dag == nil || !dag.ArtifactsEnabled() {
		return "", nil
	}

	dagArtifactDir := ""
	if dag.Artifacts != nil {
		dagArtifactDir = dag.Artifacts.Dir
	}
	artifactDir, err := logpath.GenerateDir(ctx, baseDir, dagArtifactDir, dag.Name, dagRunID)
	if err != nil {
		return "", fmt.Errorf("failed to generate queued dag-run artifact directory: %w", err)
	}
	return artifactDir, nil
}

// GetSubDAGRuns returns timing and status information for all sub DAG runs.
// When parentSubDAGRunId is provided, it returns sub-runs of that specific sub DAG run
// (for multi-level nested DAGs).
func (a *API) GetSubDAGRuns(ctx context.Context, request api.GetSubDAGRunsRequestObject) (api.GetSubDAGRunsResponseObject, error) {
	rootRef := exec.NewDAGRunRef(request.Name, request.DagRunId)
	parentSubDAGRunId := request.Params.ParentSubDAGRunId

	var dagStatus *exec.DAGRunStatus
	var err error
	detailParentRef := rootRef

	if parentSubDAGRunId != nil && *parentSubDAGRunId != "" {
		dagStatus, err = a.getReferencedDAGRunStatus(ctx, rootRef, *parentSubDAGRunId, "")
		if err != nil {
			return &api.GetSubDAGRuns404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("sub dag-run ID %s not found for root DAG %s/%s", *parentSubDAGRunId, request.Name, request.DagRunId),
			}, nil
		}
		if dagStatus.Parent.Zero() && !dagStatus.Root.Zero() && dagStatus.Root == dagStatus.DAGRun() {
			detailParentRef = dagStatus.Root
		}
	} else {
		dagStatus, err = a.dagRunMgr.GetSavedStatus(ctx, rootRef)
		if err != nil {
			return &api.GetSubDAGRuns404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: fmt.Sprintf("dag-run ID %s not found for DAG %s", request.DagRunId, request.Name),
			}, nil
		}
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return nil, err
	}

	subRuns := make([]api.SubDAGRunDetail, 0)
	for _, node := range dagStatus.Nodes {
		if len(node.SubRuns) == 0 && len(node.SubRunsRepeated) == 0 {
			continue
		}

		for _, subRun := range node.SubRuns {
			if detail, err := a.getSubDAGRunDetail(ctx, detailParentRef, subRun.DAGRunID, subRun.Params, subRun.DAGName); err == nil {
				subRuns = append(subRuns, detail)
			}
		}

		for _, subRun := range node.SubRunsRepeated {
			if detail, err := a.getSubDAGRunDetail(ctx, detailParentRef, subRun.DAGRunID, subRun.Params, subRun.DAGName); err == nil {
				subRuns = append(subRuns, detail)
			}
		}
	}

	return &api.GetSubDAGRuns200JSONResponse{
		SubRuns: subRuns,
	}, nil
}

// getSubDAGRunDetail fetches timing and status info for a single sub DAG run
func (a *API) getSubDAGRunDetail(ctx context.Context, parentRef exec.DAGRunRef, subRunID string, params string, dagName string) (api.SubDAGRunDetail, error) {
	status, err := a.getReferencedDAGRunStatus(ctx, parentRef, subRunID, dagName)
	if err != nil {
		return api.SubDAGRunDetail{}, err
	}

	detail := api.SubDAGRunDetail{
		DagRunId:    subRunID,
		Status:      api.Status(status.Status),
		StatusLabel: api.StatusLabel(status.Status.String()),
		StartedAt:   status.StartedAt,
		FinishedAt:  &status.FinishedAt,
	}

	if params != "" {
		detail.Params = &params
	}

	if dagName != "" {
		detail.DagName = &dagName
	}

	return detail, nil
}

func (a *API) getReferencedDAGRunStatus(ctx context.Context, parentRef exec.DAGRunRef, subRunID string, dagName string) (*exec.DAGRunStatus, error) {
	_, status, err := a.getReferencedDAGRunStatusWithRef(ctx, parentRef, subRunID, dagName)
	return status, err
}

func (a *API) getReferencedDAGRunStatusWithRef(ctx context.Context, parentRef exec.DAGRunRef, subRunID string, dagName string) (exec.DAGRunRef, *exec.DAGRunStatus, error) {
	status, err := a.dagRunMgr.FindSubDAGRunStatus(ctx, parentRef, subRunID)
	if err == nil {
		return parentRef, status, nil
	}
	subErr := err

	if strings.TrimSpace(dagName) == "" {
		if resolved, ok := a.findReferencedDAGName(ctx, parentRef, subRunID); ok {
			dagName = resolved
		}
	}
	if strings.TrimSpace(dagName) == "" {
		return exec.DAGRunRef{}, nil, subErr
	}

	ref := exec.NewDAGRunRef(dagName, subRunID)
	status, err = a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return exec.DAGRunRef{}, nil, subErr
	}
	return ref, status, nil
}

func (a *API) getReferencedDAGRunAttempt(ctx context.Context, parentRef exec.DAGRunRef, subRunID string, dagName string) (exec.DAGRunAttempt, error) {
	attempt, err := a.dagRunStore.FindSubAttempt(ctx, parentRef, subRunID)
	if err == nil {
		return attempt, nil
	}
	subErr := err

	if strings.TrimSpace(dagName) == "" {
		if resolved, ok := a.findReferencedDAGName(ctx, parentRef, subRunID); ok {
			dagName = resolved
		}
	}
	if strings.TrimSpace(dagName) == "" {
		return nil, subErr
	}

	attempt, err = a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dagName, subRunID))
	if err != nil {
		return nil, subErr
	}
	return attempt, nil
}

func (a *API) findReferencedDAGName(ctx context.Context, parentRef exec.DAGRunRef, subRunID string) (string, bool) {
	status, err := a.dagRunMgr.GetSavedStatus(ctx, parentRef)
	if err != nil || status == nil {
		return "", false
	}
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		if dagName, ok := findDAGNameInSubRuns(node.SubRuns, subRunID); ok {
			return dagName, true
		}
		if dagName, ok := findDAGNameInSubRuns(node.SubRunsRepeated, subRunID); ok {
			return dagName, true
		}
	}
	return "", false
}

func findDAGNameInSubRuns(subRuns []exec.SubDAGRun, subRunID string) (string, bool) {
	for _, subRun := range subRuns {
		if subRun.DAGRunID == subRunID && strings.TrimSpace(subRun.DAGName) != "" {
			return subRun.DAGName, true
		}
	}
	return "", false
}

func (a *API) waitForManualStepMutationReady(
	ctx context.Context,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
) (*exec.DAGRunStatus, error) {
	if status == nil || attempt == nil || a.procStore == nil {
		return status, nil
	}
	if status.Status != core.Waiting || !isLocalManualStepWorker(status.WorkerID) || status.AttemptID == "" {
		return status, nil
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read DAG while waiting for manual step mutation readiness", tag.Error(err))
		return status, nil
	}

	deadline := time.Now().Add(manualStepSettleTimeout)
	for time.Now().Before(deadline) {
		alive, err := a.procStore.IsAttemptAlive(ctx, dag.ProcGroup(), status.DAGRun(), status.AttemptID)
		if err != nil {
			logger.Warn(ctx, "Failed to check manual step attempt liveness", tag.Error(err))
			break
		}
		if !alive {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(manualStepSettlePollInterval):
		}
	}

	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to reload status after waiting for manual step mutation readiness", tag.Error(err))
		return status, nil
	}
	return latest, nil
}

func isLocalManualStepWorker(workerID string) bool {
	return workerID == "" || workerID == "local"
}

func applyApproval(ctx context.Context, node *exec.Node, body *api.ApproveStepRequest) {
	node.Status = core.NodeSucceeded
	node.ApprovedAt = time.Now().Format(time.RFC3339)

	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		node.ApprovedBy = user.Username
	}

	if body != nil && body.Inputs != nil {
		if node.OutputVariables == nil {
			node.OutputVariables = &collections.SyncMap{}
		}
		camelInputs := make(map[string]string, len(*body.Inputs))
		for k, v := range *body.Inputs {
			node.OutputVariables.Store(k, k+"="+v)
			camelInputs[stringutil.ScreamingSnakeToCamel(k)] = v
		}
		node.ApprovalInputs = camelInputs
	}
}

func applyRejection(ctx context.Context, node *exec.Node, status *exec.DAGRunStatus, reason *string) {
	node.Status = core.NodeRejected
	node.RejectedAt = time.Now().Format(time.RFC3339)

	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		node.RejectedBy = user.Username
	}

	if reason != nil {
		node.RejectionReason = *reason
	}

	status.Status = core.Rejected
	status.FinishedAt = time.Now().Format(time.RFC3339)
}

func (a *API) resumeDAGRun(ctx context.Context, ref exec.DAGRunRef, dagRunID string) error {
	attempt, err := a.dagRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return fmt.Errorf("find attempt: %w", err)
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("read DAG: %w", err)
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}

	prepared, err := a.prepareRetryDAGForSubprocess(ctx, dag, status)
	if err != nil {
		return fmt.Errorf("prepare DAG retry env: %w", err)
	}

	retrySpec := a.subCmdBuilder.Retry(prepared, dagRunID, "")
	return runtime.Start(ctx, retrySpec)
}

func (a *API) resumeSubDAGRun(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) error {
	attempt, err := a.getReferencedDAGRunAttempt(ctx, rootRef, subDAGRunID, "")
	if err != nil {
		return fmt.Errorf("find sub-attempt: %w", err)
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("read sub-DAG: %w", err)
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read sub-DAG status: %w", err)
	}

	prepared, err := a.prepareRetryDAGForSubprocess(ctx, dag, status)
	if err != nil {
		return fmt.Errorf("prepare sub-DAG retry env: %w", err)
	}

	retrySpec := a.subCmdBuilder.Retry(prepared, subDAGRunID, "")
	return runtime.Start(ctx, retrySpec)
}

func (a *API) prepareRetryDAGForSubprocess(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) (*core.DAG, error) {
	if dag == nil || status == nil {
		return dag, nil
	}

	env, err := spec.ResolveEnv(ctx, dag, spec.QuoteRuntimeParams(status.ParamsList, dag.ParamDefs), spec.ResolveEnvOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return nil, err
	}

	prepared := dag.Clone()
	prepared.Env = env
	return prepared, nil
}

func (a *API) logStepApproval(ctx context.Context, dagName, dagRunID, subDAGRunID, stepName string, resumed bool) {
	detailsMap := map[string]any{
		"dag_name":   dagName,
		"dag_run_id": dagRunID,
		"step_name":  stepName,
		"resumed":    resumed,
	}
	if subDAGRunID != "" {
		detailsMap["sub_dag_run_id"] = subDAGRunID
	}

	action := "dag_step_approve"
	if subDAGRunID != "" {
		action = "sub_dag_step_approve"
	}

	a.logAudit(ctx, audit.CategoryDAG, action, detailsMap)
}

func (a *API) logStepRejection(ctx context.Context, dagName, dagRunID, subDAGRunID, stepName string, reason *string) {
	detailsMap := map[string]any{
		"dag_name":   dagName,
		"dag_run_id": dagRunID,
		"step_name":  stepName,
	}
	if subDAGRunID != "" {
		detailsMap["sub_dag_run_id"] = subDAGRunID
	}
	if reason != nil {
		detailsMap["reason"] = *reason
	}

	action := "dag_step_reject"
	if subDAGRunID != "" {
		action = "sub_dag_step_reject"
	}

	a.logAudit(ctx, audit.CategoryDAG, action, detailsMap)
}

func findStepByName(nodes []*exec.Node, stepName string) int {
	for idx, n := range nodes {
		if n.Step.Name == stepName {
			return idx
		}
	}
	return -1
}

func hasWaitingSteps(nodes []*exec.Node) bool {
	for _, n := range nodes {
		if n.Status == core.NodeWaiting {
			return true
		}
	}
	return false
}

// checkMissingInputs validates that all required fields are present in the provided inputs.
func checkMissingInputs(required []string, provided map[string]string) error {
	var missing []string
	for _, fieldName := range required {
		if provided == nil || strings.TrimSpace(provided[fieldName]) == "" {
			missing = append(missing, fieldName)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required inputs: %v", missing)
	}
	return nil
}

// requiredFieldsForStep returns the list of required input field names for a step.
func requiredFieldsForStep(step core.Step) []string {
	if step.Approval != nil {
		return step.Approval.Required
	}
	return nil
}

func validateRequiredInputs(step core.Step, body *api.ApproveStepRequest) error {
	required := requiredFieldsForStep(step)
	if len(required) == 0 {
		return nil
	}
	var provided map[string]string
	if body != nil && body.Inputs != nil {
		provided = *body.Inputs
	}
	return checkMissingInputs(required, provided)
}

func validatePushBackInputs(step core.Step, body *api.PushBackStepRequest) error {
	if step.Approval == nil || len(step.Approval.Required) == 0 {
		return nil
	}
	var provided map[string]string
	if body != nil && body.Inputs != nil {
		provided = *body.Inputs
	}
	return checkMissingInputs(step.Approval.Required, provided)
}

func applyPushBack(ctx context.Context, node *exec.Node, status *exec.DAGRunStatus, body *api.PushBackStepRequest) error {
	targetName := node.Step.Name
	if node.Step.Approval != nil && strings.TrimSpace(node.Step.Approval.RewindTo) != "" {
		targetName = strings.TrimSpace(node.Step.Approval.RewindTo)
	}
	targetIdx := findStepByName(status.Nodes, targetName)
	if targetIdx < 0 {
		return fmt.Errorf("step %s approval.rewind_to references non-existent step %s", node.Step.Name, targetName)
	}

	nextIteration := node.ApprovalIteration + 1
	var inputs map[string]string
	if body != nil && body.Inputs != nil {
		inputs = cloneStringMap(*body.Inputs)
	}
	allowedInputs := pushBackAllowedInputs(node.Step)
	filteredInputs := exec.FilterPushBackInputs(allowedInputs, inputs)
	history := buildPushBackHistory(ctx, node, allowedInputs, nextIteration, filteredInputs)

	// Reset the configured rewind target and everything that depends on it.
	rewoundNodes := append([]*exec.Node{status.Nodes[targetIdx]}, findDependentNodes(status.Nodes, targetName)...)
	for _, rewoundNode := range rewoundNodes {
		previousStdout := rewoundNode.Stdout
		resetNodeForManualReexecution(rewoundNode)
		setPushBackContext(rewoundNode, nextIteration, filteredInputs, history, previousStdout)
	}
	return nil
}

func buildPushBackHistory(ctx context.Context, node *exec.Node, allowedInputs []string, nextIteration int, inputs map[string]string) []exec.PushBackEntry {
	history := exec.NormalizePushBackHistory(allowedInputs, node.ApprovalIteration, node.PushBackInputs, node.PushBackHistory)
	var actor string
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		actor = user.Username
	}
	history = append(history, exec.PushBackEntry{
		Iteration: nextIteration,
		By:        actor,
		At:        time.Now().UTC().Format(time.RFC3339),
		Inputs:    cloneStringMap(inputs),
	})
	return history
}

func resetNodeForManualReexecution(node *exec.Node) {
	step := node.Step
	*node = *exec.NewNodeFromStep(step)
}

func setPushBackContext(node *exec.Node, iteration int, inputs map[string]string, history []exec.PushBackEntry, previousStdout string) {
	node.ApprovalIteration = iteration
	node.PushBackInputs = cloneStringMap(inputs)
	node.PushBackHistory = exec.ClonePushBackHistory(history)
	node.PushBackPreviousStdout = previousStdout
}

func pushBackAllowedInputs(step core.Step) []string {
	if step.Approval == nil {
		return nil
	}
	return step.Approval.Input
}

// findDependentNodes returns all nodes that directly or transitively depend on the given step.
func findDependentNodes(nodes []*exec.Node, stepName string) []*exec.Node {
	// Build a set of step names that depend on the given step
	dependentNames := make(map[string]bool)
	dependentNames[stepName] = true

	// Iterate until no new dependents are found (transitive closure)
	changed := true
	for changed {
		changed = false
		for _, n := range nodes {
			if dependentNames[n.Step.Name] {
				continue
			}
			for _, dep := range n.Step.Depends {
				if dependentNames[dep] {
					dependentNames[n.Step.Name] = true
					changed = true
					break
				}
			}
		}
	}

	// Collect dependent nodes (excluding the source step itself)
	var result []*exec.Node
	for _, n := range nodes {
		if dependentNames[n.Step.Name] && n.Step.Name != stepName {
			result = append(result, n)
		}
	}
	return result
}

func (a *API) logStepPushBack(ctx context.Context, dagName, dagRunID, subDAGRunID, stepName string, iteration int, resumed bool) {
	detailsMap := map[string]any{
		"dag_name":           dagName,
		"dag_run_id":         dagRunID,
		"step_name":          stepName,
		"approval_iteration": iteration,
		"resumed":            resumed,
	}
	if subDAGRunID != "" {
		detailsMap["sub_dag_run_id"] = subDAGRunID
	}

	action := "dag_step_push_back"
	if subDAGRunID != "" {
		action = "sub_dag_step_push_back"
	}

	a.logAudit(ctx, audit.CategoryDAG, action, detailsMap)
}

// SSE Data Methods for DAG Runs

// DAGRunLogsResponse represents the response for DAG run logs SSE.
type DAGRunLogsResponse struct {
	SchedulerLog SchedulerLogInfo `json:"schedulerLog"`
	StepLogs     []StepLogInfo    `json:"stepLogs"`
}

// SchedulerLogInfo contains scheduler log metadata.
type SchedulerLogInfo struct {
	Content    string `json:"content"`
	LineCount  int    `json:"lineCount"`
	TotalLines int    `json:"totalLines"`
	HasMore    bool   `json:"hasMore"`
}

// StepLogInfo contains step log metadata.
type StepLogInfo struct {
	StepName    string         `json:"stepName"`
	Status      api.NodeStatus `json:"status"`
	StatusLabel string         `json:"statusLabel"`
	StartedAt   string         `json:"startedAt"`
	FinishedAt  string         `json:"finishedAt"`
	HasStdout   bool           `json:"hasStdout"`
	HasStderr   bool           `json:"hasStderr"`
}

// StepLogResponse represents the response for step log SSE.
type StepLogResponse struct {
	StdoutContent string `json:"stdoutContent"`
	StderrContent string `json:"stderrContent"`
	LineCount     int    `json:"lineCount"`
	TotalLines    int    `json:"totalLines"`
	HasMore       bool   `json:"hasMore"`
}

// GetDAGRunDetailsData returns DAG run details for SSE.
// Identifier format: "dagName/dagRunId"
func (a *API) GetDAGRunDetailsData(ctx context.Context, identifier string) (any, error) {
	dagName, dagRunId, ok := strings.Cut(identifier, "/")
	if !ok {
		return nil, fmt.Errorf("invalid identifier format: %s (expected 'dagName/dagRunId')", identifier)
	}
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}/{dagRunId}",
		dagName:  dagName,
		dagRunID: dagRunId,
	}, func(readCtx context.Context) (api.GetDAGRunDetails200JSONResponse, error) {
		return a.getDAGRunDetailsData(readCtx, dagName, dagRunId)
	})
}

// GetSubDAGRunDetailsData returns sub DAG run details for SSE.
// Identifier format: "dagName/dagRunId/subDAGRunId"
func (a *API) GetSubDAGRunDetailsData(ctx context.Context, identifier string) (any, error) {
	parts := strings.SplitN(identifier, "/", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf(
			"invalid identifier format: %s (expected 'dagName/dagRunId/subDAGRunId')",
			identifier,
		)
	}

	root := exec.NewDAGRunRef(parts[0], parts[1])
	if err := a.requireDAGRunVisible(ctx, root); err != nil {
		return nil, err
	}
	dagStatus, err := withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint:    "/dag-runs/{name}/{dagRunId}/sub/{subDAGRunId}",
		dagName:     parts[0],
		dagRunID:    parts[1],
		subDAGRunID: parts[2],
	}, func(readCtx context.Context) (*exec.DAGRunStatus, error) {
		return a.getReferencedDAGRunStatus(readCtx, root, parts[2], "")
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf(
			"sub dag-run ID %s not found for DAG %s: %w",
			parts[2],
			parts[0],
			err,
		)
	}

	return api.GetSubDAGRunDetails200JSONResponse{
		DagRunDetails: ToDAGRunDetails(*dagStatus),
	}, nil
}

// GetDAGRunLogsData returns DAG run logs for SSE.
// Identifier format: "dagName/dagRunId" or "dagName/dagRunId?tail=N"
func (a *API) GetDAGRunLogsData(ctx context.Context, identifier string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}/{dagRunId}/logs",
	}, func(readCtx context.Context) (DAGRunLogsResponse, error) {
		return a.getDAGRunLogsData(readCtx, identifier)
	})
}

func (a *API) getDAGRunLogsData(ctx context.Context, identifier string) (DAGRunLogsResponse, error) {
	// Parse query params if present
	pathPart := identifier
	var queryParams url.Values
	if before, after, ok := strings.Cut(identifier, "?"); ok {
		pathPart = before
		var err error
		queryParams, err = url.ParseQuery(after)
		if err != nil {
			logger.Warn(ctx, "Failed to parse query string in identifier",
				tag.Error(err),
				slog.String("identifier", identifier),
			)
		}
	}

	dagName, dagRunId, ok := strings.Cut(pathPart, "/")
	if !ok {
		return DAGRunLogsResponse{}, fmt.Errorf("invalid identifier format: %s (expected 'dagName/dagRunId')", identifier)
	}

	ref := exec.NewDAGRunRef(dagName, dagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return DAGRunLogsResponse{}, fmt.Errorf("dag-run ID %s not found for DAG %s", dagRunId, dagName)
	}
	if err := a.requireWorkspaceVisible(ctx, statusWorkspaceName(dagStatus)); err != nil {
		return DAGRunLogsResponse{}, err
	}

	// Parse tail parameter with bounds validation (100-10000, default 500)
	tail := 500
	if queryParams != nil {
		tail = clampInt(parseIntParam(queryParams.Get("tail"), 500), 100, 10000)
	}

	options := fileutil.LogReadOptions{
		Tail:     tail,
		Encoding: a.logEncodingCharset,
	}

	content, lineCount, totalLines, hasMore, _, err := fileutil.ReadLogContent(dagStatus.Log, options)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return DAGRunLogsResponse{}, fmt.Errorf("error reading scheduler log: %w", err)
	}

	schedulerLog := SchedulerLogInfo{
		Content:    content,
		LineCount:  lineCount,
		TotalLines: totalLines,
		HasMore:    hasMore,
	}

	// Build step logs info
	stepLogs := make([]StepLogInfo, 0, len(dagStatus.Nodes))
	for _, node := range dagStatus.Nodes {
		stepLog := StepLogInfo{
			StepName:    node.Step.Name,
			Status:      api.NodeStatus(node.Status),
			StatusLabel: node.Status.String(),
			StartedAt:   node.StartedAt,
			FinishedAt:  node.FinishedAt,
			HasStdout:   node.Stdout != "" && fileExists(node.Stdout),
			HasStderr:   node.Stderr != "" && fileExists(node.Stderr),
		}
		stepLogs = append(stepLogs, stepLog)
	}

	return DAGRunLogsResponse{
		SchedulerLog: schedulerLog,
		StepLogs:     stepLogs,
	}, nil
}

// GetStepLogData returns step log for SSE.
// Identifier format: "dagName/dagRunId/stepName"
func (a *API) GetStepLogData(ctx context.Context, identifier string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}/{dagRunId}/logs/steps/{stepName}",
	}, func(readCtx context.Context) (StepLogResponse, error) {
		return a.getStepLogData(readCtx, identifier)
	})
}

func (a *API) getStepLogData(ctx context.Context, identifier string) (StepLogResponse, error) {
	parts := strings.SplitN(identifier, "/", 3)
	if len(parts) != 3 {
		return StepLogResponse{}, fmt.Errorf("invalid identifier format: %s (expected 'dagName/dagRunId/stepName')", identifier)
	}
	dagName, dagRunId, stepName := parts[0], parts[1], parts[2]

	ref := exec.NewDAGRunRef(dagName, dagRunId)
	dagStatus, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return StepLogResponse{}, fmt.Errorf("dag-run ID %s not found for DAG %s", dagRunId, dagName)
	}
	if err := a.requireDAGRunStatusVisible(ctx, dagStatus); err != nil {
		return StepLogResponse{}, err
	}

	node, err := dagStatus.NodeByName(stepName)
	if err != nil {
		return StepLogResponse{}, fmt.Errorf("step %s not found in DAG %s", stepName, dagName)
	}

	options := fileutil.LogReadOptions{
		Tail:     1000, // Last 1000 lines by default
		Encoding: a.logEncodingCharset,
	}

	// Read stdout
	var stdoutContent string
	var lineCount, totalLines int
	var hasMore bool
	if node.Stdout != "" {
		stdoutContent, lineCount, totalLines, hasMore, _, err = fileutil.ReadLogContent(node.Stdout, options)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return StepLogResponse{}, fmt.Errorf("error reading stdout: %w", err)
		}
	}

	// Read stderr
	var stderrContent string
	if node.Stderr != "" {
		stderrContent, _, _, _, _, err = fileutil.ReadLogContent(node.Stderr, options)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// Log warning for real errors, return empty stderr
			logger.Warn(ctx, "Failed to read stderr log",
				tag.Error(err),
				slog.String("stepName", stepName),
				slog.String("stderrPath", node.Stderr),
			)
			stderrContent = ""
		}
	}

	return StepLogResponse{
		StdoutContent: stdoutContent,
		StderrContent: stderrContent,
		LineCount:     lineCount,
		TotalLines:    totalLines,
		HasMore:       hasMore,
	}, nil
}

func (a *API) getDAGRunArtifactStatus(ctx context.Context, dagName, dagRunID string) (*exec.DAGRunStatus, error) {
	var (
		attempt exec.DAGRunAttempt
		err     error
	)
	if dagRunID == "latest" {
		attempt, err = a.dagRunStore.LatestAttempt(ctx, dagName)
	} else {
		attempt, err = a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dagName, dagRunID))
	}
	if err != nil {
		return nil, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil || status.ArchiveDir == "" {
		return nil, errArtifactUnavailable
	}
	return status, nil
}

func (a *API) getSubDAGRunArtifactStatus(ctx context.Context, dagName, dagRunID, subDAGRunID string) (*exec.DAGRunStatus, error) {
	attempt, err := a.getReferencedDAGRunAttempt(ctx, exec.NewDAGRunRef(dagName, dagRunID), subDAGRunID, "")
	if err != nil {
		return nil, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil || status.ArchiveDir == "" {
		return nil, errArtifactUnavailable
	}
	return status, nil
}

func isArtifactStatusNotFound(err error) bool {
	return errors.Is(err, exec.ErrDAGRunIDNotFound) ||
		errors.Is(err, exec.ErrNoStatusData) ||
		errors.Is(err, errArtifactUnavailable)
}

func artifactListRecursive(recursive *api.ArtifactRecursive) bool {
	return recursive != nil && bool(*recursive)
}

func listArtifactTree(archiveDir string, recursive bool) ([]api.ArtifactTreeNode, error) {
	if archiveDir == "" {
		return nil, errArtifactUnavailable
	}
	info, err := os.Stat(archiveDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errArtifactUnavailable
	}
	return listArtifactTreeNodes(archiveDir, archiveDir, recursive)
}

func listArtifactTreeNodes(rootDir, currentDir string, recursive bool) ([]api.ArtifactTreeNode, error) {
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil, err
	}

	nodes := make([]api.ArtifactTreeNode, 0, len(entries))
	for _, entry := range entries {
		if fileutil.IsSymlinkDirEntry(entry) {
			continue
		}
		node, err := buildArtifactTreeNode(rootDir, currentDir, entry, recursive)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}

	sortArtifactTreeNodes(nodes)
	return nodes, nil
}

func sortArtifactTreeNodes(nodes []api.ArtifactTreeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type == api.ArtifactNodeTypeDirectory
		}
		leftLower := strings.ToLower(nodes[i].Name)
		rightLower := strings.ToLower(nodes[j].Name)
		if leftLower != rightLower {
			return leftLower < rightLower
		}
		return nodes[i].Name < nodes[j].Name
	})
}

func buildArtifactTreeNode(rootDir, currentDir string, entry fs.DirEntry, recursive bool) (api.ArtifactTreeNode, error) {
	fullPath := filepath.Join(currentDir, entry.Name())
	relPath, err := filepath.Rel(rootDir, fullPath)
	if err != nil {
		return api.ArtifactTreeNode{}, err
	}
	relPath = filepath.ToSlash(relPath)

	nodeType := api.ArtifactNodeTypeFile
	if entry.IsDir() {
		nodeType = api.ArtifactNodeTypeDirectory
	}

	node := api.ArtifactTreeNode{
		Name: entry.Name(),
		Path: relPath,
		Type: nodeType,
	}

	info, err := entry.Info()
	if err != nil {
		return api.ArtifactTreeNode{}, err
	}
	if entry.IsDir() && recursive {
		children, err := listArtifactTreeNodes(rootDir, fullPath, recursive)
		if err != nil {
			return api.ArtifactTreeNode{}, err
		}
		if len(children) > 0 {
			node.Children = &children
		}
		return node, nil
	}

	size := info.Size()
	node.Size = &size
	return node, nil
}

func resolveArtifactPath(archiveDir, relPath string) (string, error) {
	if archiveDir == "" {
		return "", errArtifactUnavailable
	}
	resolved, err := fileutil.ResolveExistingPathWithinBase(archiveDir, relPath)
	if errors.Is(err, fileutil.ErrPathEscapesBase) {
		return "", os.ErrNotExist
	}
	return resolved, err
}

func buildArtifactPreview(archiveDir, relPath string) (api.ArtifactPreviewResponse, error) {
	absPath, err := resolveArtifactPath(archiveDir, relPath)
	if err != nil {
		return api.ArtifactPreviewResponse{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return api.ArtifactPreviewResponse{}, err
	}
	if !info.Mode().IsRegular() {
		return api.ArtifactPreviewResponse{}, os.ErrNotExist
	}

	file, err := os.Open(filepath.Clean(absPath))
	if err != nil {
		return api.ArtifactPreviewResponse{}, err
	}
	defer func() {
		_ = file.Close()
	}()

	sniffBytes, err := io.ReadAll(io.LimitReader(file, 512))
	if err != nil {
		return api.ArtifactPreviewResponse{}, err
	}
	mimeType := detectArtifactMimeType(absPath, sniffBytes)
	kind := artifactPreviewKind(relPath, mimeType)
	previewLimit := artifactPreviewLimit(kind)
	tooLarge := previewLimit > 0 && info.Size() > previewLimit

	resp := api.ArtifactPreviewResponse{
		Name:      filepath.Base(absPath),
		Path:      filepath.ToSlash(filepath.Clean(relPath)),
		Kind:      kind,
		MimeType:  mimeType,
		Size:      info.Size(),
		TooLarge:  tooLarge,
		Truncated: false,
	}
	if tooLarge || previewLimit <= 0 {
		return resp, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return api.ArtifactPreviewResponse{}, err
	}

	previewBytes, err := io.ReadAll(io.LimitReader(file, previewLimit+1))
	if err != nil {
		return api.ArtifactPreviewResponse{}, err
	}
	if int64(len(previewBytes)) > previewLimit {
		previewBytes = previewBytes[:previewLimit]
		resp.Truncated = true
	}
	if kind == api.ArtifactPreviewKindMarkdown || kind == api.ArtifactPreviewKindText {
		content := string(previewBytes)
		resp.Content = &content
	}
	return resp, nil
}

func openArtifactFile(archiveDir, relPath string) (*os.File, os.FileInfo, error) {
	absPath, err := resolveArtifactPath(archiveDir, relPath)
	if err != nil {
		return nil, nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, os.ErrNotExist
	}

	file, err := os.Open(filepath.Clean(absPath))
	if err != nil {
		return nil, nil, err
	}
	return file, info, nil
}

func detectArtifactMimeType(path string, previewBytes []byte) string {
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		if detected := mime.TypeByExtension(ext); detected != "" {
			if mediaType, _, err := mime.ParseMediaType(detected); err == nil && mediaType != "" {
				return mediaType
			}
			return detected
		}
	}
	if len(previewBytes) == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(previewBytes)
}

func artifactPreviewKind(path, mimeType string) api.ArtifactPreviewKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return api.ArtifactPreviewKindMarkdown
	}
	if strings.HasPrefix(mimeType, "image/") {
		return api.ArtifactPreviewKindImage
	}
	if strings.HasPrefix(mimeType, "text/") ||
		strings.Contains(mimeType, "json") ||
		strings.Contains(mimeType, "xml") ||
		strings.Contains(mimeType, "yaml") ||
		strings.Contains(mimeType, "toml") ||
		strings.Contains(mimeType, "javascript") {
		return api.ArtifactPreviewKindText
	}
	return api.ArtifactPreviewKindBinary
}

func artifactPreviewLimit(kind api.ArtifactPreviewKind) int64 {
	switch kind {
	case api.ArtifactPreviewKindMarkdown, api.ArtifactPreviewKindText:
		return artifactTextPreviewMaxBytes
	case api.ArtifactPreviewKindImage:
		return artifactImagePreviewMaxBytes
	case api.ArtifactPreviewKindBinary:
		return 0
	}
	return 0
}

// GetDAGRunsListData returns DAG runs list for SSE.
// Identifier format: URL query string (e.g., "status=running&name=mydag")
func (a *API) GetDAGRunsListData(ctx context.Context, queryString string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs",
	}, func(readCtx context.Context) (any, error) {
		opts, err := a.dagRunListOptionsFromQueryString(readCtx, queryString)
		if err != nil {
			return nil, err
		}

		page, err := a.dagRunStore.ListStatusesPage(readCtx, opts.query...)
		if err != nil {
			if errors.Is(err, filedagrun.ErrInvalidQueryCursor) {
				return nil, err
			}
			return nil, fmt.Errorf("error listing dag-runs: %w", err)
		}

		return toDAGRunsPageResponse(page), nil
	})
}

func (a *API) dagRunListOptionsFromQueryString(ctx context.Context, queryString string) (dagRunListOptions, error) {
	params, err := url.ParseQuery(queryString)
	if err != nil {
		logger.Warn(ctx, "Failed to parse query string for DAG runs list",
			tag.Error(err),
			slog.String("queryString", queryString),
		)
	}

	var (
		statusValues *api.StatusList
		fromDate     *int64
		toDate       *int64
		name         *string
		dagRunID     *string
		labels       *string
		limit        *int
		cursor       *string
	)

	if rawStatuses, hasStatus := params["status"]; hasStatus {
		parsed, parseErr := parseStatusListQueryValues(ctx, rawStatuses)
		if parseErr != nil {
			return dagRunListOptions{}, parseErr
		}
		if len(parsed) == 0 {
			return dagRunListOptions{}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "status parameter must include at least one valid status value",
			}
		}
		statusValues = &parsed
	}
	if rawFromDate := params.Get("fromDate"); rawFromDate != "" {
		if ts, convErr := strconv.ParseInt(rawFromDate, 10, 64); convErr == nil {
			fromDate = &ts
		} else {
			logger.Warn(ctx, "Invalid fromDate parameter", slog.String("fromDate", rawFromDate), tag.Error(convErr))
		}
	}
	if rawToDate := params.Get("toDate"); rawToDate != "" {
		if ts, convErr := strconv.ParseInt(rawToDate, 10, 64); convErr == nil {
			toDate = &ts
		} else {
			logger.Warn(ctx, "Invalid toDate parameter", slog.String("toDate", rawToDate), tag.Error(convErr))
		}
	}
	if rawName := params.Get("name"); rawName != "" {
		name = &rawName
	}
	if rawDAGRunID := params.Get("dagRunId"); rawDAGRunID != "" {
		dagRunID = &rawDAGRunID
	}
	if rawLabels := params.Get("labels"); rawLabels != "" {
		labels = &rawLabels
	}
	if rawTags := params.Get("tags"); rawTags != "" {
		if labels != nil {
			return dagRunListOptions{}, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "labels and deprecated tags cannot both be set",
			}
		}
		labels = &rawTags
	}
	if rawLimit := params.Get("limit"); rawLimit != "" {
		if parsed, convErr := strconv.Atoi(rawLimit); convErr == nil {
			limit = &parsed
		} else {
			logger.Warn(ctx, "Invalid limit parameter", slog.String("limit", rawLimit), tag.Error(convErr))
		}
	}
	if rawCursor := params.Get("cursor"); rawCursor != "" {
		cursor = &rawCursor
	}

	opts := buildDAGRunListOptions(dagRunListFilterInput{
		statuses: statusValues,
		fromDate: fromDate,
		toDate:   toDate,
		name:     name,
		dagRunID: dagRunID,
		labels:   labels,
		limit:    limit,
		cursor:   cursor,
	})

	workspaceParam := workspaceParamFromValues(params)
	workspaceFilter, err := a.workspaceFilterForParams(ctx, workspaceParam)
	if err != nil {
		return dagRunListOptions{}, err
	}
	opts.query = append(opts.query, exec.WithWorkspaceFilter(workspaceFilter))

	return opts, nil
}

func toCoreStatuses(statuses *api.StatusList) []core.Status {
	if statuses == nil || len(*statuses) == 0 {
		return nil
	}

	result := make([]core.Status, 0, len(*statuses))
	for _, status := range *statuses {
		result = append(result, core.Status(status))
	}
	return result
}

func parseStatusListQueryValues(ctx context.Context, rawValues []string) (api.StatusList, error) {
	if len(rawValues) == 0 {
		return nil, nil
	}

	result := make(api.StatusList, 0, len(rawValues))
	for _, rawValue := range rawValues {
		for part := range strings.SplitSeq(rawValue, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}

			statusInt, convErr := strconv.Atoi(value)
			if convErr != nil {
				logger.Warn(ctx, "Invalid status parameter", slog.String("status", value), tag.Error(convErr))
				return nil, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeBadRequest,
					Message:    fmt.Sprintf("invalid status parameter: %s", value),
				}
			}

			status := api.Status(statusInt)
			if !isValidAPIStatus(status) {
				logger.Warn(ctx, "Status parameter out of range", slog.String("status", value))
				return nil, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeBadRequest,
					Message:    fmt.Sprintf("invalid status parameter: %s", value),
				}
			}
			result = append(result, status)
		}
	}

	return result, nil
}

func isValidAPIStatus(status api.Status) bool {
	switch status {
	case api.StatusNotStarted,
		api.StatusRunning,
		api.StatusFailed,
		api.StatusAborted,
		api.StatusSuccess,
		api.StatusQueued,
		api.StatusPartialSuccess,
		api.StatusWaiting,
		api.StatusRejected:
		return true
	default:
		return false
	}
}

func clampInt(value, minVal, maxVal int) int {
	return max(minVal, min(value, maxVal))
}

func (a *API) loadCurrentRescheduleDAG(ctx context.Context, sourceFile, nameOverride string) (*core.DAG, error) {
	if sourceFile == "" || !fileExists(sourceFile) {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "original DAG file is not available for this DAG run",
		}
	}

	loadOpts := []spec.LoadOption{
		spec.WithBaseConfig(a.config.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(a.config.Paths.DAGsDir)),
		spec.WithDAGsDir(a.config.Paths.DAGsDir),
	}
	if nameOverride != "" {
		loadOpts = append(loadOpts, spec.WithName(nameOverride))
	}

	dag, err := spec.Load(ctx, sourceFile, loadOpts...)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	return dag, nil
}

func (a *API) dagRunSourceInfo(ctx context.Context, attempt exec.DAGRunAttempt) (specFromFile bool, sourceFileName string) {
	if attempt == nil {
		return false, ""
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil || dag == nil {
		return false, ""
	}
	if dag.SourceFile == "" || !fileExists(dag.SourceFile) {
		return false, ""
	}
	// Only return sourceFileName if the file is under the DAGs directory,
	// since the definition page route only resolves files within it.
	absSource, err := filepath.Abs(dag.SourceFile)
	if err != nil {
		return true, ""
	}
	absDAGsDir, err := filepath.Abs(a.config.Paths.DAGsDir)
	if err != nil {
		return true, ""
	}
	rel, err := filepath.Rel(absDAGsDir, absSource)
	if err != nil {
		return true, ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true, ""
	}
	base := filepath.Base(dag.SourceFile)
	ext := filepath.Ext(base)
	return true, strings.TrimSuffix(base, ext)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func selectLogFile(node *exec.Node, stream api.Stream) string {
	if stream == api.StreamStderr {
		return node.Stderr
	}
	return node.Stdout
}
