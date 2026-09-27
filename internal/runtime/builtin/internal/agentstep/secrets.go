// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// minSecretLength skips secret values shorter than this many characters,
// which would match ordinary words and numbers in instructions and pages.
const minSecretLength = 4

func longEnoughToCheck(value string) bool {
	return utf8.RuneCountInString(value) >= minSecretLength
}

// OperationTexts is the text of one with.do operation that reaches a model.
type OperationTexts struct {
	Kind  string
	Texts []string
}

// CheckSecrets rejects operations whose model-bound text contains a secret
// value. Such values must travel as variables, which the model sees only as
// %name%. step names the step type in the error.
func CheckSecrets(step string, operations []OperationTexts, secrets map[string]string) error {
	names := make([]string, 0, len(secrets))
	for name, value := range secrets {
		if longEnoughToCheck(value) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for i, op := range operations {
		for _, text := range op.Texts {
			for _, name := range names {
				if strings.Contains(text, secrets[name]) {
					return fmt.Errorf(
						"%s: do[%d].%s contains the value of secret %s, which would be sent to the model; pass it in with.variables and reference it as %%name%%",
						step, i, op.Kind, name,
					)
				}
			}
		}
	}
	return nil
}

// NewMasker hides declared secrets and ask answers in logs, timeline events,
// and text sent to the model. Plain variables are not secret; masking them
// would also corrupt text that happens to contain them.
func NewMasker(secrets, answers map[string]string) *masking.Masker {
	pairs := make([]string, 0, len(secrets)+len(answers))
	for _, values := range []map[string]string{secrets, answers} {
		for name, value := range values {
			if longEnoughToCheck(value) {
				pairs = append(pairs, name+"="+value)
			}
		}
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: pairs})
}
