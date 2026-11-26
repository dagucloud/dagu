// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"

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
	// One skill at a time is read and dropped, so the request holds the text
	// of one skill at most.
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
	file, ok := sk.File(rel)
	if !ok {
		return nil, &Error{Code: api.ErrorCodeNotFound, Message: "Skill file not found", HTTPStatus: http.StatusNotFound}
	}
	body, err := sk.Open(rel)
	switch {
	case errors.Is(err, skill.ErrFileTooLarge):
		return nil, errSkillFileTooLarge
	case errors.Is(err, skill.ErrFileChanged):
		return nil, &Error{Code: api.ErrorCodeConflict, Message: "The skill changed while it was read; try again", HTTPStatus: http.StatusConflict}
	case err != nil:
		logger.Error(ctx, "Failed to open skill file", tag.Name(request.Name), tag.Error(err))
		return nil, errFailedToReadSkill
	}
	etag := `"` + sk.Hash + `"`
	return api.GetSkillFile200ApplicationoctetStreamResponse{
		Body: body,
		Headers: api.GetSkillFile200ResponseHeaders{
			ContentDisposition: ptrOf(fmt.Sprintf("attachment; filename=%q", sanitizeFilename(path.Base(rel)))),
			ETag:               &etag,
		},
		ContentLength: file.Size,
	}, nil
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
