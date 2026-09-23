// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// minSecretLength skips secret values shorter than this many characters,
// which would match ordinary words, numbers, and element IDs in instructions
// and page text.
const minSecretLength = 4

func longEnoughToCheck(value string) bool {
	return utf8.RuneCountInString(value) >= minSecretLength
}

// checkSecrets rejects operations whose model-bound text contains a secret
// value. Such values must travel as variables, which the model never sees.
func checkSecrets(cfg config, secrets map[string]string) error {
	names := make([]string, 0, len(secrets))
	for name, value := range secrets {
		if longEnoughToCheck(value) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for i, op := range cfg.Do {
		for _, text := range op.promptTexts() {
			for _, name := range names {
				if strings.Contains(text, secrets[name]) {
					return fmt.Errorf(
						"browser: do[%d].%s contains the value of secret %s, which would be sent to the model; pass it in with.variables and reference it as %%name%%",
						i, op.kind(), name,
					)
				}
			}
		}
	}
	return nil
}

// newMasker hides declared secrets and ask answers in logs, timeline events,
// and text sent to the model. Plain variables are not secret; masking them
// would also corrupt page text and element IDs that happen to contain them.
func newMasker(secrets, answers map[string]string) *masking.Masker {
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
