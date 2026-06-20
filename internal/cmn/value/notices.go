// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"errors"
	"fmt"
	"strings"
)

// ValueReferenceNotice describes a supported value reference left unresolved.
type ValueReferenceNotice struct {
	Message   string
	FieldPath string
	Token     string
	Reason    ValueReferenceNoticeReason
}

// ValueReferenceNoticeReason explains why a supported reference was preserved.
type ValueReferenceNoticeReason string

const (
	ValueReferenceReasonUnknownStepID        ValueReferenceNoticeReason = "unknown_step_id"
	ValueReferenceReasonUnknownOutputName    ValueReferenceNoticeReason = "unknown_output_name"
	ValueReferenceReasonMissingDependency    ValueReferenceNoticeReason = "missing_dependency"
	ValueReferenceReasonSelfReference        ValueReferenceNoticeReason = "self_reference"
	ValueReferenceReasonNamespaceUnavailable ValueReferenceNoticeReason = "namespace_unavailable"
	ValueReferenceReasonUnknownContextField  ValueReferenceNoticeReason = "unknown_context_field"
)

type noticeReasonError struct {
	reason ValueReferenceNoticeReason
	msg    string
}

func (e noticeReasonError) Error() string {
	return e.msg
}

func newNoticeReasonError(reason ValueReferenceNoticeReason, msg string) error {
	return noticeReasonError{reason: reason, msg: msg}
}

// ValueReferenceNoticeSink receives passive value-reference notices.
type ValueReferenceNoticeSink interface {
	Report(ValueReferenceNotice)
}

// SuppressStepOutputReferenceNotices drops generic notices for exact Spec 007 tokens.
func SuppressStepOutputReferenceNotices(sink ValueReferenceNoticeSink) ValueReferenceNoticeSink {
	if sink == nil {
		return nil
	}
	return stepOutputReferenceSuppressingSink{sink: sink}
}

type stepOutputReferenceSuppressingSink struct {
	sink ValueReferenceNoticeSink
}

func (s stepOutputReferenceSuppressingSink) Report(notice ValueReferenceNotice) {
	if IsStepOutputReferenceToken(notice.Token) {
		return
	}
	s.sink.Report(notice)
}

// ValueReferenceNoticeCollector stores unique notices in insertion order.
type ValueReferenceNoticeCollector struct {
	notices []ValueReferenceNotice
	seen    map[valueReferenceNoticeKey]struct{}
}

type valueReferenceNoticeKey struct {
	fieldPath string
	token     string
}

// Report records notice unless the same field has already reported the same token.
func (c *ValueReferenceNoticeCollector) Report(notice ValueReferenceNotice) {
	if c.seen == nil {
		c.seen = make(map[valueReferenceNoticeKey]struct{})
	}
	key := valueReferenceNoticeKey{fieldPath: notice.FieldPath, token: notice.Token}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.notices = append(c.notices, notice)
}

// Notices returns recorded notices in insertion order.
func (c *ValueReferenceNoticeCollector) Notices() []ValueReferenceNotice {
	out := make([]ValueReferenceNotice, len(c.notices))
	copy(out, c.notices)
	return out
}

func addUnresolvedReferenceNotice(sink ValueReferenceNoticeSink, field, token string, err error) {
	if sink == nil {
		return
	}
	refName := token
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		refName = token[2 : len(token)-1]
	}
	message := fmt.Sprintf("%s was left unchanged because %s had no value when %s was evaluated.", token, refName, field)
	if field == "" {
		message = fmt.Sprintf("%s was left unchanged because %s had no value when the field was evaluated.", token, refName)
	}
	if err != nil {
		message += " " + err.Error() + "."
	}
	var reasonErr noticeReasonError
	var reason ValueReferenceNoticeReason
	if errors.As(err, &reasonErr) {
		reason = reasonErr.reason
	}
	sink.Report(ValueReferenceNotice{
		Message:   message,
		FieldPath: field,
		Token:     token,
		Reason:    reason,
	})
}

// ReportStepOutputReferenceNotice reports a passive Spec 007 step-output notice.
func ReportStepOutputReferenceNotice(sink ValueReferenceNoticeSink, field, token string, reason ValueReferenceNoticeReason) {
	if sink == nil {
		return
	}
	evaluatedField := field
	if evaluatedField == "" {
		evaluatedField = "the field"
	}
	message := fmt.Sprintf("%s was left unchanged because step output references are unavailable when %s was evaluated.", token, field)
	if field == "" {
		message = fmt.Sprintf("%s was left unchanged because step output references are unavailable when the field was evaluated.", token)
	}
	switch reason {
	case ValueReferenceReasonUnknownStepID:
		message = fmt.Sprintf("%s was left unchanged because the referenced step id does not exist when %s was evaluated.", token, evaluatedField)
	case ValueReferenceReasonUnknownOutputName:
		message = fmt.Sprintf("%s was left unchanged because the referenced output name is not declared when %s was evaluated.", token, evaluatedField)
	case ValueReferenceReasonMissingDependency:
		message = fmt.Sprintf("%s was left unchanged because the owning step does not depend on the producing step when %s was evaluated.", token, evaluatedField)
	case ValueReferenceReasonSelfReference:
		message = fmt.Sprintf("%s was left unchanged because a step cannot reference its own output when %s was evaluated.", token, evaluatedField)
	case ValueReferenceReasonNamespaceUnavailable:
		message = fmt.Sprintf("%s was left unchanged because %s has no step-output lookup scope.", token, evaluatedField)
	case ValueReferenceReasonUnknownContextField:
		message = fmt.Sprintf("%s was left unchanged because the context field is not defined when %s was evaluated.", token, evaluatedField)
	}
	sink.Report(ValueReferenceNotice{
		Message:   message,
		FieldPath: field,
		Token:     token,
		Reason:    reason,
	})
}

// ReportUnresolvedEnvExpansionNotices reports missing simple shell-style env references in input.
func ReportUnresolvedEnvExpansionNotices(input, field string, scope *EnvScope, sink ValueReferenceNoticeSink) {
	if sink == nil {
		return
	}
	matches := reVarSubstitution.FindAllStringSubmatchIndex(input, -1)
	for _, loc := range matches {
		match := input[loc[0]:loc[1]]
		if isSingleQuotedVar(input, loc[0], loc[1]) || isEscapedDollar(input, loc[0]) {
			continue
		}

		key, ok := simpleEnvExpansionKey(input, loc)
		if !ok {
			continue
		}
		if _, found := scope.Get(key); found {
			continue
		}
		addUnresolvedReferenceNotice(sink, field, match, fmt.Errorf("unknown env.%s binding", key))
	}
}

func simpleEnvExpansionKey(input string, loc []int) (string, bool) {
	var key string
	switch {
	case loc[2] >= 0:
		key = input[loc[2]:loc[3]]
		if !ValidEnvName(key) {
			return "", false
		}
	case loc[4] >= 0:
		key = input[loc[4]:loc[5]]
		if !ValidEnvName(key) {
			return "", false
		}
	case loc[6] >= 0:
		return "", false
	default:
		return "", false
	}
	return key, true
}
