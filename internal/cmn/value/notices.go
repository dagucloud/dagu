// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"fmt"
	"strings"
)

// ValueReferenceNotice describes a supported value reference left unresolved.
type ValueReferenceNotice struct {
	Message   string
	FieldPath string
	Token     string
}

// ValueReferenceNoticeSink receives passive value-reference notices.
type ValueReferenceNoticeSink interface {
	Report(ValueReferenceNotice)
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
	sink.Report(ValueReferenceNotice{
		Message:   message,
		FieldPath: field,
		Token:     token,
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
