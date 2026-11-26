// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
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
	errFailedToReadSkill = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to read skill",
		HTTPStatus: http.StatusInternalServerError,
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
