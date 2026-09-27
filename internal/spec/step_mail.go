// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type mailFieldKind uint8

const (
	mailString mailFieldKind = iota
	mailBool
	mailLimit
	mailDuration
	mailMark
	mailMove
	mailEmails
)

var (
	mailSearchFields = map[string]mailFieldKind{
		"mailbox":          mailString,
		"folder":           mailString,
		"unread":           mailBool,
		"from":             mailString,
		"subject":          mailString,
		"within":           mailDuration,
		"has_attachments":  mailBool,
		"save_attachments": mailBool,
		"limit":            mailLimit,
	}
	mailOrganizeFields = map[string]mailFieldKind{
		"mailbox": mailString,
		"emails":  mailEmails,
		"mark":    mailMark,
		"move":    mailMove,
		"folder":  mailString,
		"dry_run": mailBool,
	}
)

func normalizeMailSearchAction(normalized map[string]any, with map[string]any) error {
	if err := validateFixedOutputs(normalized, "mail.search"); err != nil {
		return err
	}
	if err := validateMailFields("mail.search", with, mailSearchFields, "mailbox"); err != nil {
		return err
	}
	return normalizeOperationAction(normalized, "mail", with, "search")
}

func normalizeMailOrganizeAction(normalized map[string]any, with map[string]any) error {
	if err := validateFixedOutputs(normalized, "mail.organize"); err != nil {
		return err
	}
	if err := validateMailFields("mail.organize", with, mailOrganizeFields, "mailbox", "emails"); err != nil {
		return err
	}
	_, hasMark := with["mark"]
	_, hasMove := with["move"]
	if !hasMark && !hasMove {
		return ir.NewValidationError("with", nil, fmt.Errorf("mail.organize requires with.mark or with.move"))
	}
	return normalizeOperationAction(normalized, "mail", with, "organize")
}

// validateMailFields checks field names and literal values. A string holding a
// value reference passes, since it resolves only at run time.
func validateMailFields(action string, with map[string]any, allowed map[string]mailFieldKind, required ...string) error {
	for _, name := range required {
		if _, ok := with[name]; !ok {
			return ir.NewValidationError("with", nil, fmt.Errorf("%s requires with.%s", action, name))
		}
	}
	for name, value := range with {
		kind, ok := allowed[name]
		if !ok {
			return ir.NewValidationError("with", nil, fmt.Errorf("unsupported %s field %q", action, name))
		}
		if err := validateMailField(name, kind, value); err != nil {
			return ir.NewValidationError("with."+name, nil, err)
		}
	}
	return nil
}

func validateMailField(name string, kind mailFieldKind, value any) error {
	text, isString := value.(string)
	if isString && isValueReference(text) {
		return nil
	}
	switch kind {
	case mailString:
		if !isString || strings.TrimSpace(text) == "" {
			return fmt.Errorf("with.%s must be a non-empty string", name)
		}
	case mailBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("with.%s must be a boolean", name)
		}
	case mailLimit:
		limit, ok := mailInt(value)
		if !ok || limit < ir.MailSearchMinLimit || limit > ir.MailSearchMaxLimit {
			return fmt.Errorf("with.%s must be an integer from %d to %d", name, ir.MailSearchMinLimit, ir.MailSearchMaxLimit)
		}
	case mailDuration:
		if !isString {
			return fmt.Errorf("with.%s must be a duration such as 24h or 7d", name)
		}
		if _, err := ParseDuration(text); err != nil {
			return fmt.Errorf("with.%s must be a duration such as 24h or 7d", name)
		}
	case mailMark:
		if marks := ir.MailMarks(); !isString || !slices.Contains(marks, text) {
			return fmt.Errorf("with.%s must be one of %s", name, strings.Join(marks, ", "))
		}
	case mailMove:
		if moves := ir.MailMoves(); !isString || !slices.Contains(moves, text) {
			return fmt.Errorf("with.%s must be one of %s", name, strings.Join(moves, ", "))
		}
	case mailEmails:
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return nil
			}
		case map[string]any:
			return nil
		case []any:
			if len(v) > 0 {
				return nil
			}
		}
		return fmt.Errorf("with.%s must be an email ID, an email object, or a non-empty list of them", name)
	}
	return nil
}

// isValueReference reports whether text holds a value reference, including
// the bare ${NAME} environment form, so it resolves only at run time.
func isValueReference(text string) bool {
	return cmnvalue.IsWholeReference(strings.TrimSpace(text)) || cmnvalue.HasValueReference(text)
}

// mailInt accepts the integer types YAML decoding produces.
func mailInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case uint64:
		return int(min(v, uint64(ir.MailSearchMaxLimit+1))), true
	case float64:
		if v == float64(int(v)) {
			return int(v), true
		}
	}
	return 0, false
}
