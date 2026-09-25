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
// intact. A retry reuses the succeeded step's stored output, which holds the
// mask, including for a secret that JSON escapes.
func TestStepOutputMaskedOnRetry(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"step_output_retry.yaml", "step_output_retry_escaped.yaml"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			const runID = "spec069-step-output"
			dagu := harness.NewRunner(t)
			env := []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}

			dagu.RunWithEnv(env, "start", "--run-id="+runID, fixture).ExpectNonZeroExitCode()
			dagu.ExpectTextFileContent("received.out", "real\n")

			dagu.WriteFile("ready", "")
			dagu.RunWithEnv(env, "retry", "--run-id="+runID, fixture).ExpectExitCode(0)
			dagu.ExpectTextFileContent("received.out", "real\n*******\n")
			dagu.ExpectTextFileContent("logins.out", "login\n")
		})
	}
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
