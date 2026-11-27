// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
)

// jsonTestReport is the report that --format json writes (Spec 101, JSON
// Report).
type jsonTestReport struct {
	Cases        []jsonTestCase    `json:"cases"`
	InvalidFiles []jsonInvalidFile `json:"invalid_files,omitempty"`
}

type jsonTestCase struct {
	File   string `json:"file"`
	Name   string `json:"name"`
	Result string `json:"result"`
	// DurationMS is set for a case that ran.
	DurationMS    *int64             `json:"duration_ms,omitempty"`
	Failures      []jsonFailure      `json:"failures,omitempty"`
	Errors        []jsonTestError    `json:"errors,omitempty"`
	Fix           string             `json:"fix,omitempty"`
	FileFix       string             `json:"file_fix,omitempty"`
	DefaultMocked []string           `json:"default_mocked,omitempty"`
	AutoApproved  []string           `json:"auto_approved,omitempty"`
	Notifications []testNotification `json:"notifications,omitempty"`
	CaseDir       string             `json:"case_dir,omitempty"`
	// Update is what --update did with the case's expect.
	Update string `json:"update,omitempty"`
	// UpdateKept lists the assertions --update kept because the run's value
	// holds a host value.
	UpdateKept []jsonKept `json:"update_kept,omitempty"`
}

// jsonKept is an assertion that --update kept as written because the
// run's value holds a host value.
type jsonKept struct {
	Step      string   `json:"step"`
	Field     string   `json:"field"`
	Variables []string `json:"variables"`
	Env       []string `json:"env,omitempty"`
}

type jsonFailure struct {
	Step     string `json:"step,omitempty"`
	Field    string `json:"field"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
}

type jsonTestError struct {
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

type jsonInvalidFile struct {
	File   string          `json:"file"`
	Errors []jsonTestError `json:"errors"`
}

func newJSONTestReport() *jsonTestReport {
	return &jsonTestReport{Cases: []jsonTestCase{}}
}

func jsonCaseOf(c caseResult) jsonTestCase {
	out := jsonTestCase{
		File:          c.file,
		Name:          c.name,
		Result:        c.result(),
		Errors:        jsonErrors(c.errors),
		Fix:           c.fix,
		FileFix:       c.fileFix,
		DefaultMocked: c.defaultMocked,
		AutoApproved:  c.autoApproved,
		Notifications: c.notifications,
		CaseDir:       c.caseDir,
	}
	if out.Result != resultInvalid {
		ms := c.duration.Milliseconds()
		out.DurationMS = &ms
	}
	if c.update != nil {
		out.Update = c.update.outcome
		for _, k := range c.update.kept {
			out.UpdateKept = append(out.UpdateKept, jsonKept{Step: k.step, Field: k.field, Variables: k.vars, Env: k.env})
		}
	}
	for _, f := range c.failures {
		failure := jsonFailure{Step: f.step, Field: f.field, Expected: f.expected, Actual: f.actual}
		if f.field == failureTimeout {
			// The JSON report gives the --timeout limit in milliseconds.
			failure.Expected = f.expected.(time.Duration).Milliseconds()
		}
		out.Failures = append(out.Failures, failure)
	}
	return out
}

func jsonErrors(errs []dagtest.Finding) []jsonTestError {
	var out []jsonTestError
	for _, err := range errs {
		out = append(out, jsonTestError{Source: err.Source, Message: err.Message})
	}
	return out
}
