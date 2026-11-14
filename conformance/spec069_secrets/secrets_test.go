// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec069_secrets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The env provider resolves from the real OS environment. The tree preview
// is read back from the on-disk step log file, so the resolved value being
// masked there proves the masking writer redacts the file itself, not just
// the terminal display.
func TestEnvProviderMasksValue(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"SOURCE_SECRET_VALUE=supersecret123"}, "start", "env_success.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "*******")
	require.NotContains(t, result.Stdout(), "supersecret123")
}

// Masking keeps a final line that has no trailing newline, in the step log
// and in a stdout redirect file.
func TestMaskedLogKeepsFinalLine(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"SOURCE_SECRET_VALUE=supersecret123"}, "start", "log_unterminated_line.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "hello world")
	require.Contains(t, result.Stdout(), "tail *******")
	require.NotContains(t, result.Stdout(), "supersecret123")
	dagu.ExpectTextFileContent("tail.out", "tail *******")
}

func TestEnvProviderMissingVariable(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "env_missing.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`environment variable "DAGU_CONFORMANCE_UNSET_SECRET_VAR" is not set`)
}

// The file provider resolves relative paths against the DAG's own directory.
func TestFileProviderMasksValue(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "file_success.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "*******")
	require.NotContains(t, result.Stdout(), "filesecretvalue123")
}

func TestFileProviderMissingFile(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "file_missing.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("secret file not found")
}

// A later step of the same run reads a published output with the secret
// intact. A retry restores the succeeded step with that output stored masked,
// so the step reading it fails instead of running on the mask, including for a
// secret that JSON escapes, the ${<id>.outputs.<name>} form, and an output
// variable read as ${env.<name>}.
func TestStepOutputMaskedOnRetry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ fixture, output string }{
		{fixture: "step_output_retry.yaml", output: "token"},
		{fixture: "step_output_retry_escaped.yaml", output: "token"},
		{fixture: "step_output_retry_property.yaml", output: "token"},
		{fixture: "step_output_retry_env.yaml", output: "TOKEN"},
		// foreach resolves its items while it runs.
		{fixture: "step_output_retry_items.yaml", output: "TOKEN"},
	} {
		fixture := tc.fixture
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			const runID = "spec069-step-output"
			dagu := harness.NewRunner(t)
			env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

			dagu.RunWithEnv(env, "start", "--run-id="+runID, fixture).ExpectNonZeroExitCode()
			dagu.ExpectTextFileContent("received.out", "real\n")

			dagu.WriteFile("ready", "")
			retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, fixture)
			retry.ExpectNonZeroExitCode()
			retry.ExpectStderrContains(`reads output "`+tc.output+`" of step "login"`, `step "login" has to run again`)
			dagu.ExpectTextFileContent("received.out", "real\n")
			dagu.ExpectTextFileContent("logins.out", "login\n")
		})
	}
}

// A retried step that reads only outputs masking did not change runs on their
// stored values. An escaped reference to the masked output is literal text.
func TestStepOutputUnmaskedOnRetry(t *testing.T) {
	t.Parallel()

	const runID = "spec069-step-output-unread"
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

	dagu.RunWithEnv(env, "start", "--run-id="+runID, "step_output_retry_unread.yaml").ExpectNonZeroExitCode()
	first := "us-east\n${steps.login.outputs.token}\n"
	dagu.ExpectTextFileContent("received.out", first)

	dagu.WriteFile("ready", "")
	dagu.RunWithEnv(env, "retry", "--run-id="+runID, "step_output_retry_unread.yaml").ExpectExitCode(0)
	dagu.ExpectTextFileContent("received.out", first+first)
	dagu.ExpectTextFileContent("logins.out", "login\n")
}

// A parallel step retries a step of its child run by restoring the child run
// from its stored status, so the retry fails on the masked output variable
// instead of running on the mask.
func TestStepOutputMaskedOnParallelRetry(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

	run := dagu.RunWithEnv(env, "start", "step_output_parallel_retry.yaml")
	run.ExpectNonZeroExitCode()
	run.ExpectStderrContains(`reads output "TOKEN" of step "login"`)
	dagu.ExpectTextFileContent("received.out", "filesecretvalue123\n")
}

// A child run that a step starts in process checks its own reads, so its
// handler reads the masked output variable as it is and the retry succeeds.
func TestStepOutputMaskedInChildHandler(t *testing.T) {
	t.Parallel()

	const runID = "spec069-child-handler"
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

	dagu.RunWithEnv(env, "start", "--run-id="+runID, "step_output_retry_child_handler.yaml").ExpectNonZeroExitCode()
	first := "use ran\nhandler [filesecretvalue123]\n"
	dagu.ExpectTextFileContent("received.out", first)

	dagu.WriteFile("ready", "")
	dagu.RunWithEnv(env, "retry", "--run-id="+runID, "step_output_retry_child_handler.yaml").ExpectExitCode(0)
	dagu.ExpectTextFileContent("received.out", first+"use ran\nhandler [*******]\n")
}

// A foreach body step that defines the name of a masked output variable in its
// own env reads its own value, so the retry runs.
func TestStepOutputShadowedOnRetry(t *testing.T) {
	t.Parallel()

	const runID = "spec069-step-output-shadowed"
	dagu := harness.NewRunner(t)
	env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

	dagu.RunWithEnv(env, "start", "--run-id="+runID, "step_output_retry_foreach.yaml").ExpectNonZeroExitCode()
	dagu.ExpectTextFileContent("received.out", "local\n")

	dagu.WriteFile("ready", "")
	dagu.RunWithEnv(env, "retry", "--run-id="+runID, "step_output_retry_foreach.yaml").ExpectExitCode(0)
	dagu.ExpectTextFileContent("received.out", "local\nlocal\n")
	dagu.ExpectTextFileContent("logins.out", "login\n")
}

func TestUnknownProvider(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "unknown_provider.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("unknown secret provider: nope")
}

// Build-time validation rejects malformed secret declarations before any
// provider is contacted.
func TestSecretValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		errText string
	}{
		{"validate_missing_name.yaml", "'name' field is required"},
		{"validate_invalid_name.yaml", "must be a valid environment variable name"},
		{"validate_dagu_prefix.yaml", "must not start with DAGU_"},
		{"validate_reserved_name.yaml", "collides with Dagu-managed runtime environment variable"},
		{"validate_duplicate_name.yaml", `duplicate secret name "MY_SECRET"`},
		{"validate_ref_and_provider.yaml", "exactly one of 'ref' or 'provider' plus 'key' is required"},
		{"validate_neither.yaml", "exactly one of 'ref' or 'provider' plus 'key' is required"},
		{"validate_options_with_ref.yaml", "'options' cannot be used with registry ref"},
		{"validate_bad_ref_pattern.yaml", "registry ref must be a slash-separated lowercase slug path"},
		{"validate_empty_ref_segment.yaml", "registry ref must be a slash-separated lowercase slug path"},
		{"validate_valid_ref.yaml", ""},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			if tc.errText == "" {
				result.ExpectExitCode(0)
				return
			}
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errText)
		})
	}
}

// Providers whose parsing fails before any network client is created report
// a provider-specific diagnostic. No provider here ever dials out.
func TestProviderValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		errText string
	}{
		{"gcp_missing_project.yaml", `project ID is required for GCP Secret Manager secret "bare-id-no-options"`},
		{"azure_bad_option.yaml", `unsupported option "bogus"`},
		{"alibaba_bad_name.yaml", "secret name for Alibaba Cloud KMS contains unsupported characters"},
		{"kubernetes_bad_key.yaml", "secret name and data key are required"},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errText)
		})
	}
}

// A local endpoint checks AWS provider dispatch and secret masking.
func TestAWSProviderLiveResolve(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkAWSRequest(t, r)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte(`{"Name":"mysecret","SecretString":"awssecretvalue456"}`))
	}))
	t.Cleanup(server.Close)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(awsMockEnv(server.URL), "start", "aws.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "*******")
	require.NotContains(t, result.Stdout(), "awssecretvalue456")
}

func TestAWSProviderNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkAWSRequest(t, r)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Header().Set("X-Amzn-Errortype", "ResourceNotFoundException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Message":"Secrets Manager can't find the specified secret."}`))
	}))
	t.Cleanup(server.Close)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(awsMockEnv(server.URL), "start", "aws.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`AWS Secrets Manager secret "mysecret" was not found`)
}

func checkAWSRequest(t *testing.T, r *http.Request) {
	t.Helper()
	assert.Equal(t, "secretsmanager.GetSecretValue", r.Header.Get("X-Amz-Target"))
	var request struct {
		SecretID string `json:"SecretId"`
	}
	if assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
		assert.Equal(t, "mysecret", request.SecretID)
	}
}

func awsMockEnv(endpoint string) []string {
	return []string{
		"AWS_ENDPOINT_URL=" + endpoint,
		"AWS_ENDPOINT_URL_SECRETS_MANAGER=" + endpoint,
		"AWS_IGNORE_CONFIGURED_ENDPOINT_URLS=false",
		"AWS_PROFILE=",
		"AWS_DEFAULT_PROFILE=",
		"AWS_CONFIG_FILE=" + os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE=" + os.DevNull,
		"AWS_SESSION_TOKEN=",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_ACCESS_KEY_ID=conformance-test",
		"AWS_SECRET_ACCESS_KEY=conformance-test",
		"AWS_REGION=us-east-1",
	}
}
