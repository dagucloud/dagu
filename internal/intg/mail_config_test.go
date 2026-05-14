// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestMailConfigEnvExpansion(t *testing.T) {
	t.Parallel()

	t.Run("SMTPConfigWithSecrets", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Create a secret file for SMTP password
		secretFile := th.TempFile(t, "smtp-password.txt", []byte("super-secret-smtp-password"))

		// Test that SMTP config with secrets is properly evaluated at runtime.
		// The DAG should run successfully even though we're not actually sending mail.
		// Note: With env isolation, the DAG struct is NOT mutated - evaluation happens
		// at runtime in the agent. We verify correctness by successful execution.
		dag := th.DAG(t, `
secrets:
  - name: SMTP_PASSWORD
    provider: file
    key: `+secretFile+`

env:
  - SMTP_HOST: smtp.example.com

smtp:
  host: ${SMTP_HOST}
  port: "587"
  username: user@example.com
  password: ${SMTP_PASSWORD}

steps:
  - name: test-step
    run: echo "SMTP config evaluated successfully"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertOutputs(t, map[string]any{
			"RESULT": "SMTP config evaluated successfully",
		})

		// Note: DAG struct is NOT mutated with env isolation.
		// Evaluation happens at runtime in the agent.
		// Success of the run verifies configs were processed correctly.
		require.NotNil(t, dag.DAG.SMTP)
		// Templates are preserved in DAG struct
		require.Equal(t, "${SMTP_HOST}", dag.DAG.SMTP.Host)
		require.Equal(t, "${SMTP_PASSWORD}", dag.DAG.SMTP.Password)
	})

	t.Run("WaitMailConfigWithEnvVars", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		dag := th.DAG(t, `
env:
  - WAIT_PREFIX: "[Waiting]"
  - OPS_EMAIL: ops@example.com

wait_mail:
  from: alerts@example.com
  to: ${OPS_EMAIL}
  prefix: ${WAIT_PREFIX}

steps:
  - name: test-step
    run: echo "WaitMail config evaluated successfully"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertOutputs(t, map[string]any{
			"RESULT": "WaitMail config evaluated successfully",
		})

		// Note: DAG struct preserves templates with env isolation.
		// Evaluation happens at runtime in the agent.
		require.NotNil(t, dag.DAG.WaitMail)
		require.Equal(t, "alerts@example.com", dag.DAG.WaitMail.From)
		require.Equal(t, []string{"${OPS_EMAIL}"}, dag.DAG.WaitMail.To)
		require.Equal(t, "${WAIT_PREFIX}", dag.DAG.WaitMail.Prefix)
	})

	t.Run("MultipleRecipientsWithEnvVars", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		dag := th.DAG(t, `
env:
  - ADMIN1: admin1@example.com
  - ADMIN2: admin2@example.com

error_mail:
  from: alerts@example.com
  to:
    - ${ADMIN1}
    - ${ADMIN2}
  prefix: "[Alert]"

steps:
  - name: test-step
    run: echo "Multiple recipients evaluated successfully"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Multiple recipients evaluated successfully",
		})

		// Note: DAG struct preserves templates with env isolation.
		// Evaluation happens at runtime in the agent.
		require.NotNil(t, dag.DAG.ErrorMail)
		require.Equal(t, []string{"${ADMIN1}", "${ADMIN2}"}, dag.DAG.ErrorMail.To)
	})

	t.Run("AllMailConfigsWithMixedSources", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Create secrets
		smtpPassFile := th.TempFile(t, "smtp-pass.txt", []byte("smtp-pass-123"))
		adminEmailFile := th.TempFile(t, "admin-email.txt", []byte("secret-admin@corp.com"))

		dag := th.DAG(t, `
env:
  - SMTP_HOST: mail.corp.com
  - SMTP_USER: mailbot@corp.com
  - OPS_EMAIL: ops@corp.com

secrets:
  - name: SMTP_PASS
    provider: file
    key: `+smtpPassFile+`
  - name: ADMIN_EMAIL
    provider: file
    key: `+adminEmailFile+`

smtp:
  host: ${SMTP_HOST}
  port: "465"
  username: ${SMTP_USER}
  password: ${SMTP_PASS}

error_mail:
  from: ${SMTP_USER}
  to:
    - ${ADMIN_EMAIL}
    - ${OPS_EMAIL}
  prefix: "[ERROR]"

info_mail:
  from: ${SMTP_USER}
  to: ${OPS_EMAIL}
  prefix: "[INFO]"

steps:
  - name: test-step
    run: echo "All mail configs evaluated successfully"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertOutputs(t, map[string]any{
			"RESULT": "All mail configs evaluated successfully",
		})

		// Note: DAG struct preserves templates with env isolation.
		// Evaluation happens at runtime in the agent. Success of the run
		// verifies configs were processed correctly.

		// SMTP config preserves templates
		require.NotNil(t, dag.DAG.SMTP)
		require.Equal(t, "${SMTP_HOST}", dag.DAG.SMTP.Host)
		require.Equal(t, "${SMTP_USER}", dag.DAG.SMTP.Username)
		require.Equal(t, "${SMTP_PASS}", dag.DAG.SMTP.Password)

		// error_mail config preserves templates
		require.NotNil(t, dag.DAG.ErrorMail)
		require.Equal(t, "${SMTP_USER}", dag.DAG.ErrorMail.From)
		require.Equal(t, []string{"${ADMIN_EMAIL}", "${OPS_EMAIL}"}, dag.DAG.ErrorMail.To)

		// info_mail config preserves templates
		require.NotNil(t, dag.DAG.InfoMail)
		require.Equal(t, "${SMTP_USER}", dag.DAG.InfoMail.From)
		require.Equal(t, []string{"${OPS_EMAIL}"}, dag.DAG.InfoMail.To)
	})
}
