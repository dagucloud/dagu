// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/diagnostic"
)

// DiagnosticKindValueResolution identifies diagnostics produced by value resolution.
const DiagnosticKindValueResolution diagnostic.Kind = "value_resolution"

// CodeValueReferenceUnresolved identifies a supported reference left unresolved.
const CodeValueReferenceUnresolved diagnostic.Code = "value_reference_unresolved"

func addUnresolvedReferenceDiagnostic(ctx context.Context, field, token string, err error) {
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
	diagnostic.Report(ctx, diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     DiagnosticKindValueResolution,
		Code:     CodeValueReferenceUnresolved,
		Message:  message,
		Location: diagnostic.Location{
			FieldPath: field,
		},
		Attributes: map[string]string{
			"token": token,
		},
	})
}
