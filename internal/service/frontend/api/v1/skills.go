// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// Spec 096: the skills of the DAGs directory. The folder skills/<name>/ is the
// only source of truth. Every call reads the disk, and every write carries the
// hash that the client read.

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
	skills, err := a.skillStore().List()
	if err != nil {
		logger.Error(ctx, "Failed to list skills", tag.Dir(a.skillsDir()), tag.Error(err))
		return nil, errFailedToReadSkill
	}
	out := make([]api.SkillSummary, 0, len(skills))
	for _, sk := range skills {
		out = append(out, toSkillSummary(sk))
	}
	return api.ListSkills200JSONResponse{Skills: out}, nil
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
	detail := api.SkillDetail{
		Name:     request.Name,
		Content:  string(sk.Raw()),
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
	pre, err := skillPrecondition(request.Params.IfMatch, request.Params.IfNoneMatch)
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
	a.logAudit(ctx, audit.CategorySystem, "skill_update", map[string]string{"name": request.Name, "hash": sk.Hash})
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
	pre, err := skillPrecondition(request.Params.IfMatch, nil)
	if err != nil {
		return nil, err
	}
	if err := a.skillStore().DeleteSkill(ctx, request.Name, pre); err != nil {
		failure := a.classifySkillWrite(ctx, request.Name, err)
		if failure.status == http.StatusConflict {
			return api.DeleteSkill409JSONResponse(failure.body), nil
		}
		return nil, failure.err
	}
	a.logAudit(ctx, audit.CategorySystem, "skill_delete", map[string]string{"name": request.Name})
	return api.DeleteSkill204Response{}, nil
}

// GetSkillFile streams one file of a skill, exactly as stored.
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
	if _, ok := sk.File(rel); !ok {
		return nil, &Error{Code: api.ErrorCodeNotFound, Message: "Skill file not found", HTTPStatus: http.StatusNotFound}
	}
	data, err := sk.ReadVerified(rel)
	if err != nil {
		logger.Error(ctx, "Failed to read skill file", tag.Name(request.Name), tag.Error(err))
		return nil, &Error{Code: api.ErrorCodeConflict, Message: "The skill changed while it was read; try again", HTTPStatus: http.StatusConflict}
	}
	etag := `"` + sk.Hash + `"`
	return api.GetSkillFile200ApplicationoctetStreamResponse{
		Body: strings.NewReader(string(data)),
		Headers: api.GetSkillFile200ResponseHeaders{
			ContentDisposition: ptrOf(fmt.Sprintf("attachment; filename=%q", sanitizeFilename(path.Base(rel)))),
			ETag:               &etag,
		},
		ContentLength: int64(len(data)),
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
	pre, err := skillPrecondition(request.Params.IfMatch, nil)
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
	pre, err := skillPrecondition(request.Params.IfMatch, nil)
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

// DownloadSkill streams one skill as a ZIP.
func (a *API) DownloadSkill(ctx context.Context, request api.DownloadSkillRequestObject) (api.DownloadSkillResponseObject, error) {
	if !skill.IsSafeFolderName(request.Name) {
		return nil, errInvalidSkillName
	}
	body, name, err := a.skillZip(ctx, []string{request.Name})
	if err != nil {
		return nil, err
	}
	return api.DownloadSkill200ApplicationzipResponse{
		Body:    body,
		Headers: api.DownloadSkill200ResponseHeaders{ContentDisposition: zipDisposition(name, []string{request.Name})},
	}, nil
}

// DownloadSkills streams several skills as one ZIP.
func (a *API) DownloadSkills(ctx context.Context, request api.DownloadSkillsRequestObject) (api.DownloadSkillsResponseObject, error) {
	if request.Body == nil || len(request.Body.Names) == 0 {
		return nil, ErrInvalidRequestBody
	}
	for _, n := range request.Body.Names {
		if !skill.IsSafeFolderName(n) {
			return nil, errInvalidSkillName
		}
	}
	body, name, err := a.skillZip(ctx, request.Body.Names)
	if err != nil {
		return nil, err
	}
	return api.DownloadSkills200ApplicationzipResponse{
		Body:    body,
		Headers: api.DownloadSkills200ResponseHeaders{ContentDisposition: zipDisposition(name, request.Body.Names)},
	}, nil
}

// skillZip checks every name first, so that an unknown name gives an error and
// no partial ZIP, then returns the reader of the stream.
func (a *API) skillZip(ctx context.Context, names []string) (io.Reader, string, error) {
	skills, err := a.skillStore().DownloadSkills(names)
	if err != nil {
		switch {
		case errors.Is(err, skill.ErrNotFound):
			return nil, "", &Error{Code: api.ErrorCodeNotFound, Message: "Skill not found: " + strings.TrimPrefix(err.Error(), skill.ErrNotFound.Error()+": "), HTTPStatus: http.StatusNotFound}
		case errors.Is(err, skill.ErrDownloadTooLarge):
			return nil, "", &Error{Code: api.ErrorCodeBadRequest, Message: err.Error(), HTTPStatus: http.StatusBadRequest}
		}
		logger.Error(ctx, "Failed to read skills for a download", tag.Error(err))
		return nil, "", errFailedToReadSkill
	}
	unique := make([]string, len(skills))
	for i, sk := range skills {
		unique[i] = sk.Name
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(skill.WriteZip(pw, skills))
	}()
	a.logAudit(ctx, audit.CategorySystem, "skill_download", map[string]string{"names": strings.Join(unique, ",")})
	return pr, skill.ZipFileName(unique, time.Now()), nil
}

// zipDisposition builds the header Content-Disposition. A name that is not
// ASCII gets the extended form of RFC 6266.
func zipDisposition(fileName string, names []string) *string {
	value := fmt.Sprintf("attachment; filename=%q", fileName)
	if len(names) == 1 && fileName == "skill.zip" {
		value += "; filename*=UTF-8''" + percentEncode(names[0]+".zip")
	}
	return &value
}

func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// skillPrecondition reads the headers If-Match and If-None-Match.
func skillPrecondition(ifMatch, ifNoneMatch *string) (skill.Precondition, error) {
	if ifNoneMatch != nil && strings.TrimSpace(*ifNoneMatch) != "" {
		if strings.TrimSpace(*ifNoneMatch) != "*" {
			return skill.Precondition{}, &Error{Code: api.ErrorCodeBadRequest, Message: "If-None-Match must be *", HTTPStatus: http.StatusBadRequest}
		}
		return skill.Precondition{MustNotExist: true}, nil
	}
	if ifMatch == nil || strings.TrimSpace(*ifMatch) == "" {
		return skill.Precondition{}, errSkillPreconditionRequired
	}
	hash := strings.Trim(strings.TrimPrefix(strings.TrimSpace(*ifMatch), "W/"), `"`)
	if len(hash) != 64 {
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
		hash := conflict.Current
		return skillFailure{status: http.StatusConflict, body: api.SkillError{
			Code: api.ErrorCodeConflict, Message: conflict.Message, Hash: &hash,
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
