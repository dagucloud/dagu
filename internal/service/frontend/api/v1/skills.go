// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// Spec 096: the skills of the DAGs directory. The folder skills/<name>/ is the
// only source of truth. Every call reads the disk.

var (
	errSkillNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Skill not found",
		HTTPStatus: http.StatusNotFound,
	}
	errInvalidSkillName = &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Skill name must be the name of a folder: no path separator, no leading dot, 255 bytes at most",
		HTTPStatus: http.StatusBadRequest,
	}
	errFailedToReadSkill = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to read skill",
		HTTPStatus: http.StatusInternalServerError,
	}
	errFailedToSaveSkill = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to save skill",
		HTTPStatus: http.StatusInternalServerError,
	}
	errSkillPreconditionRequired = &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "A write needs the header If-Match with the hash of the skill that the client read, or If-None-Match: * to create the skill",
		HTTPStatus: http.StatusPreconditionRequired,
	}
	errSkillChanged = &Error{
		Code:       api.ErrorCodeConflict,
		Message:    "The skill changed while it was read; try again",
		HTTPStatus: http.StatusConflict,
	}
	errSkillFileTooLarge = &Error{
		Code:       api.ErrorCodePayloadTooLarge,
		Message:    fmt.Sprintf("A skill file must not be larger than %d bytes", skill.MaxFileBytes),
		HTTPStatus: http.StatusRequestEntityTooLarge,
	}
)

func (a *API) skillsDir() string {
	return filepath.Join(a.config.Paths.DAGsDir, spec.SkillsDirName)
}

func (a *API) skillStore() *skill.Store { return skill.NewStore(a.skillsDir()) }

// ListSkills lists the skill folders of the DAGs directory, sorted by name. The
// list holds the skills that break the format too, with their errors.
func (a *API) ListSkills(ctx context.Context, _ api.ListSkillsRequestObject) (api.ListSkillsResponseObject, error) {
	store := a.skillStore()
	names, truncated, err := store.Names()
	if err != nil {
		logger.Error(ctx, "Failed to list skills", tag.Dir(a.skillsDir()), tag.Error(err))
		return nil, errFailedToReadSkill
	}
	// One skill at a time is read, so the request holds the text of two files of
	// one skill at most beyond the bounded read cache.
	out := make([]api.SkillSummary, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sk, err := skill.Read(store.Dir(name))
		if errors.Is(err, skill.ErrNotFound) {
			continue
		}
		if err != nil {
			logger.Error(ctx, "Failed to read skill", tag.Dir(store.Dir(name)), tag.Error(err))
			return nil, errFailedToReadSkill
		}
		out = append(out, toSkillSummary(sk))
	}
	return api.ListSkills200JSONResponse{Skills: out, Truncated: truncated}, nil
}

// GetSkill returns the raw SKILL.md, the hash, the files and the issues of a skill.
func (a *API) GetSkill(ctx context.Context, request api.GetSkillRequestObject) (api.GetSkillResponseObject, error) {
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	sk, err := a.skillStore().Read(request.Name)
	if err != nil {
		return nil, a.skillReadError(ctx, request.Name, err)
	}
	content, err := sk.Text(skill.FileName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// SKILL.md is missing or is not text. The content stays empty, and the
		// errors of the skill say why.
	case errors.Is(err, skill.ErrFileChanged):
		return nil, errSkillChanged
	case err != nil:
		logger.Error(ctx, "Failed to read skill file", tag.Name(request.Name), tag.Error(err))
		return nil, errFailedToReadSkill
	}
	detail := api.SkillDetail{
		Name:     request.Name,
		Content:  string(content),
		Hash:     sk.Hash,
		Errors:   toSkillIssues(sk.Errors()),
		Warnings: toSkillIssues(sk.Warnings()),
		Files:    toSkillFiles(sk.Files),
		Summary:  toSkillSummary(sk),
	}
	detail.License = ptrIfNotEmpty(sk.Meta.License)
	detail.Compatibility = ptrIfNotEmpty(sk.Meta.Compatibility)
	detail.AllowedTools = ptrIfNotEmpty(sk.Meta.AllowedTools)
	if len(sk.Meta.Metadata) > 0 {
		m := sk.Meta.Metadata
		detail.Metadata = &m
	}
	return api.GetSkill200JSONResponse(detail), nil
}

// UpdateSkill validates and saves SKILL.md. Requires developer role or above.
func (a *API) UpdateSkill(ctx context.Context, request api.UpdateSkillRequestObject) (api.UpdateSkillResponseObject, error) {
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	pre, err := skillPrecondition(request.Params.IfMatch, request.Params.IfNoneMatch, true)
	if err != nil {
		return nil, err
	}
	sk, err := a.skillStore().WriteFile(ctx, request.Name, skill.FileName, []byte(request.Body.Content), pre)
	if err != nil {
		failure := a.classifySkillWrite(ctx, request.Name, err)
		switch failure.status {
		case http.StatusBadRequest:
			return api.UpdateSkill400JSONResponse(failure.body), nil
		case http.StatusConflict:
			return api.UpdateSkill409JSONResponse(failure.body), nil
		}
		return nil, failure.err
	}
	a.logAudit(ctx, audit.CategorySystem, "skill_update", map[string]any{
		"resource_type": "skill",
		"resource_id":   request.Name,
		"hash":          sk.Hash,
	})
	return api.UpdateSkill200JSONResponse(toSkillWriteResult(sk)), nil
}

// DeleteSkill deletes the folder of a skill with every file. Requires developer
// role or above.
func (a *API) DeleteSkill(ctx context.Context, request api.DeleteSkillRequestObject) (api.DeleteSkillResponseObject, error) {
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	pre, err := skillPrecondition(request.Params.IfMatch, request.Params.IfNoneMatch, false)
	if err != nil {
		return nil, err
	}
	err = a.skillStore().DeleteSkill(ctx, request.Name, pre)
	if errors.Is(err, skill.ErrFilesLeft) {
		logger.Warn(ctx, "Some files of a deleted skill remain", tag.Name(request.Name), tag.Error(err))
		err = nil
	}
	if err != nil {
		failure := a.classifySkillWrite(ctx, request.Name, err)
		if failure.status == http.StatusConflict {
			return api.DeleteSkill409JSONResponse(failure.body), nil
		}
		return nil, failure.err
	}
	a.logAudit(ctx, audit.CategorySystem, "skill_delete", map[string]string{"name": request.Name})
	return api.DeleteSkill204Response{}, nil
}

// GetSkillFile streams one file of a skill, exactly as stored. A file that
// changes during the stream ends the stream before its last bytes.
func (a *API) GetSkillFile(ctx context.Context, request api.GetSkillFileRequestObject) (api.GetSkillFileResponseObject, error) {
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	rel, err := skill.CheckExistingPath(request.Params.Path)
	if err != nil {
		return nil, skillInvalidPath(err)
	}
	sk, err := a.skillStore().Read(request.Name)
	if err != nil {
		return nil, a.skillReadError(ctx, request.Name, err)
	}
	if err := unsettledReadError(ctx, sk); err != nil {
		return nil, err
	}
	file, ok := sk.File(rel)
	if !ok {
		return nil, &Error{Code: api.ErrorCodeNotFound, Message: "Skill file not found", HTTPStatus: http.StatusNotFound}
	}
	body, err := sk.Open(rel)
	switch {
	case errors.Is(err, skill.ErrFileTooLarge):
		return nil, errSkillFileTooLarge
	case errors.Is(err, skill.ErrFileChanged):
		return nil, errSkillChanged
	case err != nil:
		logger.Error(ctx, "Failed to open skill file", tag.Name(request.Name), tag.Error(err))
		return nil, errFailedToReadSkill
	}
	return &skillFileResponse{
		ctx:      ctx,
		body:     body,
		size:     file.Size,
		filename: sanitizeFilename(path.Base(rel)),
		etag:     `"` + sk.Hash + `"`,
	}, nil
}

// PutSkillFile writes one file of references/, assets/ or scripts/.
func (a *API) PutSkillFile(ctx context.Context, request api.PutSkillFileRequestObject) (api.PutSkillFileResponseObject, error) {
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	rel := request.Params.Path
	if rel == skill.FileName {
		return nil, &Error{Code: api.ErrorCodeBadRequest, Message: "SKILL.md has its own route: PUT /skills/{name}", HTTPStatus: http.StatusBadRequest}
	}
	pre, err := skillPrecondition(request.Params.IfMatch, request.Params.IfNoneMatch, false)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, skill.MaxFileBytes+1))
	if err != nil {
		return nil, ErrInvalidRequestBody
	}
	if len(data) > skill.MaxFileBytes {
		return nil, errSkillFileTooLarge
	}
	sk, err := a.skillStore().WriteFile(ctx, request.Name, rel, data, pre)
	if err != nil {
		failure := a.classifySkillWrite(ctx, request.Name, err)
		switch failure.status {
		case http.StatusBadRequest:
			return api.PutSkillFile400JSONResponse(failure.body), nil
		case http.StatusConflict:
			return api.PutSkillFile409JSONResponse(failure.body), nil
		}
		return nil, failure.err
	}
	a.logAudit(ctx, audit.CategorySystem, "skill_file_write", map[string]string{"name": request.Name, "path": rel, "hash": sk.Hash})
	return api.PutSkillFile200JSONResponse(toSkillWriteResult(sk)), nil
}

// DeleteSkillFile deletes one file of a skill.
func (a *API) DeleteSkillFile(ctx context.Context, request api.DeleteSkillFileRequestObject) (api.DeleteSkillFileResponseObject, error) {
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	pre, err := skillPrecondition(request.Params.IfMatch, request.Params.IfNoneMatch, false)
	if err != nil {
		return nil, err
	}
	sk, err := a.skillStore().DeleteFile(ctx, request.Name, request.Params.Path, pre)
	if err != nil {
		failure := a.classifySkillWrite(ctx, request.Name, err)
		switch failure.status {
		case http.StatusBadRequest:
			return api.DeleteSkillFile400JSONResponse(failure.body), nil
		case http.StatusConflict:
			return api.DeleteSkillFile409JSONResponse(failure.body), nil
		}
		return nil, failure.err
	}
	a.logAudit(ctx, audit.CategorySystem, "skill_file_delete", map[string]string{"name": request.Name, "path": request.Params.Path, "hash": sk.Hash})
	return api.DeleteSkillFile200JSONResponse(toSkillWriteResult(sk)), nil
}

// skillPrecondition reads the headers If-Match and If-None-Match. Only the
// creation of a skill takes If-None-Match.
func skillPrecondition(ifMatch, ifNoneMatch *string, create bool) (skill.Precondition, error) {
	hasIfMatch := ifMatch != nil && strings.TrimSpace(*ifMatch) != ""
	if ifNoneMatch != nil && strings.TrimSpace(*ifNoneMatch) != "" {
		if !create {
			return skill.Precondition{}, &Error{Code: api.ErrorCodeBadRequest, Message: "If-None-Match applies only to the creation of a skill; send If-Match", HTTPStatus: http.StatusBadRequest}
		}
		if hasIfMatch {
			return skill.Precondition{}, &Error{Code: api.ErrorCodeBadRequest, Message: "send If-Match or If-None-Match, not both", HTTPStatus: http.StatusBadRequest}
		}
		if strings.TrimSpace(*ifNoneMatch) != "*" {
			return skill.Precondition{}, &Error{Code: api.ErrorCodeBadRequest, Message: "If-None-Match must be *", HTTPStatus: http.StatusBadRequest}
		}
		return skill.Precondition{MustNotExist: true}, nil
	}
	if !hasIfMatch {
		return skill.Precondition{}, errSkillPreconditionRequired
	}
	hash := strings.TrimPrefix(strings.TrimSpace(*ifMatch), "W/")
	if len(hash) >= 2 && hash[0] == '"' && hash[len(hash)-1] == '"' {
		hash = hash[1 : len(hash)-1]
	}
	if _, err := hex.DecodeString(hash); err != nil || len(hash) != 64 {
		return skill.Precondition{}, &Error{Code: api.ErrorCodeBadRequest, Message: "If-Match must be the hash of the skill (64 hexadecimal characters)", HTTPStatus: http.StatusBadRequest}
	}
	return skill.Precondition{IfMatch: hash}, nil
}

type skillFailure struct {
	status int
	body   api.SkillError
	err    error
}

// classifySkillWrite maps an error of the store to a response: a typed body for
// a refused content (400) and a conflict (409), an *Error for the rest.
func (a *API) classifySkillWrite(ctx context.Context, name string, err error) skillFailure {
	if conflict, ok := errors.AsType[*skill.ConflictError](err); ok {
		return skillFailure{status: http.StatusConflict, body: api.SkillError{
			Code: api.ErrorCodeConflict, Message: conflict.Message, Hash: ptrIfNotEmpty(conflict.Current),
		}}
	}
	if invalid, ok := errors.AsType[*skill.InvalidError](err); ok {
		issues := toSkillIssues(invalid.Issues)
		return skillFailure{status: http.StatusBadRequest, body: api.SkillError{
			Code: api.ErrorCodeBadRequest, Message: invalid.Error(), Issues: &issues,
		}}
	}
	if errors.Is(err, skill.ErrPreconditionRequired) {
		return skillFailure{status: http.StatusPreconditionRequired, err: errSkillPreconditionRequired}
	}
	if errors.Is(err, skill.ErrNotFound) {
		return skillFailure{status: http.StatusNotFound, err: errSkillNotFound}
	}
	logger.Error(ctx, "Failed to write skill", tag.Name(name), tag.Error(err))
	return skillFailure{status: http.StatusInternalServerError, err: errFailedToSaveSkill}
}

// skillFileResponse streams one file of a skill as an attachment and closes it
// afterwards.
type skillFileResponse struct {
	ctx      context.Context
	body     io.ReadCloser
	size     int64
	filename string
	etag     string
}

func (r *skillFileResponse) VisitGetSkillFileResponse(w http.ResponseWriter) error {
	defer func() { _ = r.body.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(r.size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", r.filename))
	w.Header().Set("ETag", r.etag)
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, r.body); err != nil {
		logger.Warn(r.ctx, "Skill file stream ended early", tag.Error(err))
		// Headers are committed; abort so no error body joins the partial file.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// unsettledReadError gives the error of a read that saw no file of the skill
// because the folder kept changing or could not be read, and nil otherwise.
func unsettledReadError(ctx context.Context, sk *skill.Skill) error {
	for _, issue := range sk.Issues {
		switch issue.Rule {
		case skill.RuleChanging:
			return errSkillChanged
		case skill.RuleUnreadable:
			logger.Error(ctx, "Failed to read skill", tag.Name(sk.Name), tag.Dir(sk.Dir))
			return errFailedToReadSkill
		}
	}
	return nil
}

func (a *API) skillReadError(ctx context.Context, name string, err error) error {
	if errors.Is(err, skill.ErrNotFound) {
		return errSkillNotFound
	}
	logger.Error(ctx, "Failed to read skill", tag.Name(name), tag.Error(err))
	return errFailedToReadSkill
}

func skillInvalidPath(err error) error {
	return &Error{Code: api.ErrorCodeBadRequest, Message: err.Error(), HTTPStatus: http.StatusBadRequest}
}

func toSkillIssues(issues []skill.Issue) []api.SkillIssue {
	out := make([]api.SkillIssue, 0, len(issues))
	for _, i := range issues {
		item := api.SkillIssue{Severity: api.SkillIssueSeverity(i.Severity), Rule: i.Rule, Message: i.Message}
		if i.File != "" {
			file := i.File
			item.File = &file
		}
		out = append(out, item)
	}
	return out
}

func toSkillFiles(files []skill.File) []api.SkillFile {
	out := make([]api.SkillFile, 0, len(files))
	for _, f := range files {
		out = append(out, api.SkillFile{Path: f.Path, Size: f.Size, Sha256: f.SHA256, Text: f.Text})
	}
	return out
}

func toSkillSummary(sk *skill.Skill) api.SkillSummary {
	var total int64
	for _, f := range sk.Files {
		total += f.Size
	}
	stats := sk.Stats()
	return api.SkillSummary{
		Name:        sk.Name,
		Description: sk.Meta.Description,
		Hash:        sk.Hash,
		Valid:       sk.Valid(),
		Errors:      toSkillIssues(sk.Errors()),
		Warnings:    toSkillIssues(sk.Warnings()),
		FileCount:   len(sk.Files),
		TotalBytes:  total,
		BodyLines:   stats.BodyLines,
		BodyTokens:  stats.BodyTokens,
	}
}

func toSkillWriteResult(sk *skill.Skill) api.SkillWriteResult {
	return api.SkillWriteResult{
		Hash:     sk.Hash,
		Errors:   toSkillIssues(sk.Errors()),
		Warnings: toSkillIssues(sk.Warnings()),
		Files:    toSkillFiles(sk.Files),
	}
}

func ptrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
