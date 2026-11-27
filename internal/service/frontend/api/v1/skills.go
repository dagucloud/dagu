// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strconv"

	"github.com/dagucloud/dagu/v2/api/v1"
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

func ptrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
