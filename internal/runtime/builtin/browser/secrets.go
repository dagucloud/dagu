// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// minGuardedSecretLength skips very short secret values, which would match
// ordinary words in instructions.
const minGuardedSecretLength = 4

// checkSecrets rejects operations whose model-bound text contains a secret
// value. Such values must travel as variables, which the model never sees.
func checkSecrets(cfg config, secrets map[string]string) error {
	names := make([]string, 0, len(secrets))
	for name, value := range secrets {
		if len(value) >= minGuardedSecretLength {
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

// newMasker hides secrets and variable values in logs, timeline events, and
// text sent to the model.
func newMasker(secrets, variables map[string]string) *masking.Masker {
	pairs := make([]string, 0, len(secrets)+len(variables))
	for name, value := range secrets {
		pairs = append(pairs, name+"="+value)
	}
	for name, value := range variables {
		pairs = append(pairs, name+"="+value)
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: pairs})
}
