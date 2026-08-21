// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpload_PathPrefixedEndpoint(t *testing.T) {
	t.Parallel()

	type observedRequest struct {
		method        string
		path          string
		authorization string
	}

	requests := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observedRequest{
			method:        r.Method,
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
		}
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	source := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(source, []byte("daily report"), 0o600))

	impl := newS3TestExecutor(t, server.URL+"/storage/v1/s3", opUpload, map[string]any{
		"source": source,
		"key":    "daily/report.txt",
	})

	var stdout bytes.Buffer
	impl.SetStdout(&stdout)
	require.NoError(t, impl.Run(context.Background()))

	request := <-requests
	assert.Equal(t, http.MethodPut, request.method)
	assert.Equal(t, "/storage/v1/s3/reports/daily/report.txt", request.path)
	assert.NotEmpty(t, request.authorization)

	var result UploadResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "test-etag", result.ETag)
	assert.Equal(t, int64(len("daily report")), result.Size)
}

func TestUpload_AppliesConfiguredHeaders(t *testing.T) {
	t.Parallel()

	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	source := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(source, []byte("daily report"), 0o600))
	impl := newS3TestExecutor(t, server.URL, opUpload, map[string]any{
		"source":         source,
		"key":            "daily/report.txt",
		"content_type":   "text/plain",
		"storage_class":  "STANDARD_IA",
		"metadata":       map[string]string{"owner": "finance"},
		"tags":           map[string]string{"environment": "test", "owner": "finance"},
		"acl":            "bucket-owner-full-control",
		"sse":            "aws:kms",
		"sse_kms_key_id": "test-key-id",
	})
	impl.SetStdout(&bytes.Buffer{})
	require.NoError(t, impl.Run(context.Background()))

	requestHeaders := <-headers
	assert.Equal(t, "text/plain", requestHeaders.Get("Content-Type"))
	assert.Equal(t, "STANDARD_IA", requestHeaders.Get("X-Amz-Storage-Class"))
	assert.Equal(t, "finance", requestHeaders.Get("X-Amz-Meta-Owner"))
	assert.Equal(t, "environment=test&owner=finance", requestHeaders.Get("X-Amz-Tagging"))
	assert.Equal(t, "bucket-owner-full-control", requestHeaders.Get("X-Amz-Acl"))
	assert.Equal(t, "aws:kms", requestHeaders.Get("X-Amz-Server-Side-Encryption"))
	assert.Equal(t, "test-key-id", requestHeaders.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id"))
}

func TestDownload_PathPrefixedEndpoint(t *testing.T) {
	t.Parallel()

	type observedRequest struct {
		method string
		path   string
	}

	requests := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observedRequest{method: r.Method, path: r.URL.Path}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"download-etag"`)
		_, _ = w.Write([]byte("daily report"))
	}))
	t.Cleanup(server.Close)

	destination := filepath.Join(t.TempDir(), "downloads", "report.txt")
	impl := newS3TestExecutor(t, server.URL+"/storage/v1/s3", opDownload, map[string]any{
		"key":         "daily/report.txt",
		"destination": destination,
	})

	var stdout bytes.Buffer
	impl.SetStdout(&stdout)
	require.NoError(t, impl.Run(context.Background()))

	request := <-requests
	assert.Equal(t, http.MethodGet, request.method)
	assert.Equal(t, "/storage/v1/s3/reports/daily/report.txt", request.path)
	contents, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, "daily report", string(contents))

	var result DownloadResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, "download-etag", result.ETag)
	assert.Equal(t, int64(len("daily report")), result.Size)
}

func TestList_PathPrefixedEndpoint(t *testing.T) {
	t.Parallel()

	type observedRequest struct {
		path      string
		listType  string
		prefix    string
		delimiter string
	}

	requests := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observedRequest{
			path:      r.URL.Path,
			listType:  r.URL.Query().Get("list-type"),
			prefix:    r.URL.Query().Get("prefix"),
			delimiter: r.URL.Query().Get("delimiter"),
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>reports</Name>
  <Prefix>daily/</Prefix>
  <KeyCount>1</KeyCount>
  <MaxKeys>1000</MaxKeys>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>daily/report.txt</Key>
    <LastModified>2026-08-21T00:00:00Z</LastModified>
    <ETag>&quot;list-etag&quot;</ETag>
    <Size>12</Size>
    <StorageClass>STANDARD</StorageClass>
  </Contents>
</ListBucketResult>`))
	}))
	t.Cleanup(server.Close)

	impl := newS3TestExecutor(t, server.URL+"/storage/v1/s3", opList, map[string]any{
		"prefix": "daily/",
	})

	var stdout bytes.Buffer
	impl.SetStdout(&stdout)
	require.NoError(t, impl.Run(context.Background()))

	request := <-requests
	assert.Equal(t, "/storage/v1/s3/reports", request.path)
	assert.Equal(t, "2", request.listType)
	assert.Equal(t, "daily/", request.prefix)
	assert.Equal(t, "/", request.delimiter)

	var result ListResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	require.Len(t, result.Objects, 1)
	assert.Equal(t, "daily/report.txt", result.Objects[0].Key)
	assert.Equal(t, "list-etag", result.Objects[0].ETag)
}

func TestDelete_PathPrefixedEndpoint(t *testing.T) {
	t.Parallel()

	type observedRequest struct {
		method string
		path   string
	}

	requests := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observedRequest{method: r.Method, path: r.URL.Path}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	impl := newS3TestExecutor(t, server.URL+"/storage/v1/s3", opDelete, map[string]any{
		"key": "daily/report.txt",
	})

	var stdout bytes.Buffer
	impl.SetStdout(&stdout)
	require.NoError(t, impl.Run(context.Background()))

	request := <-requests
	assert.Equal(t, http.MethodDelete, request.method)
	assert.Equal(t, "/storage/v1/s3/reports/daily/report.txt", request.path)

	var result DeleteResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, 1, result.DeletedCount)
	assert.Equal(t, []string{"daily/report.txt"}, result.DeletedKeys)
}

func newS3TestExecutor(t *testing.T, endpoint, operation string, config map[string]any) *executorImpl {
	t.Helper()

	mergedConfig := map[string]any{
		"endpoint":          endpoint,
		"region":            "local",
		"bucket":            "reports",
		"access_key_id":     "test-key",
		"secret_access_key": "test-secret",
		"force_path_style":  true,
	}
	maps.Copy(mergedConfig, config)

	exec, err := newExecutor(context.Background(), ir.Step{
		Name:     operation,
		Commands: []ir.CommandEntry{{Command: operation}},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "s3",
			Config: mergedConfig,
		},
	})
	require.NoError(t, err)
	return exec.(*executorImpl)
}

func TestContextInjection(t *testing.T) {
	t.Parallel()

	t.Run("WithS3Config_and_get", func(t *testing.T) {
		t.Parallel()

		cfg := &ir.S3Config{
			Region:          "us-west-2",
			Bucket:          "test-bucket",
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "test-key",
			SecretAccessKey: "test-secret",
			ForcePathStyle:  true,
		}

		ctx := context.Background()
		ctx = WithS3Config(ctx, cfg)

		retrieved := getS3ConfigFromContext(ctx)
		require.NotNil(t, retrieved)
		assert.Equal(t, "us-west-2", retrieved.Region)
		assert.Equal(t, "test-bucket", retrieved.Bucket)
		assert.Equal(t, "http://localhost:9000", retrieved.Endpoint)
		assert.Equal(t, "test-key", retrieved.AccessKeyID)
		assert.Equal(t, "test-secret", retrieved.SecretAccessKey)
		assert.True(t, retrieved.ForcePathStyle)
	})

	t.Run("getS3ConfigFromContext_nil_when_not_set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		retrieved := getS3ConfigFromContext(ctx)
		assert.Nil(t, retrieved)
	})

	t.Run("WithS3Config_nil_value", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		ctx = WithS3Config(ctx, nil)

		retrieved := getS3ConfigFromContext(ctx)
		assert.Nil(t, retrieved)
	})
}

func TestNewExecutor_DAGLevelConfigMerging(t *testing.T) {
	t.Parallel()

	t.Run("DAGLevelConfig_applied_as_defaults", func(t *testing.T) {
		t.Parallel()

		// Create DAG-level S3 config
		dagS3 := &ir.S3Config{
			Region:          "us-east-1",
			Bucket:          "dag-bucket",
			Endpoint:        "http://minio:9000",
			AccessKeyID:     "dag-key",
			SecretAccessKey: "dag-secret",
			ForcePathStyle:  true,
		}

		ctx := context.Background()
		ctx = WithS3Config(ctx, dagS3)

		// Step with minimal config (just source and key for upload)
		step := ir.Step{
			Name:     "upload-step",
			Commands: []ir.CommandEntry{{Command: "upload"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "s3",
				Config: map[string]any{
					"source": "/tmp/test.txt",
					"key":    "uploads/test.txt",
				},
			},
		}

		exec, err := newExecutor(ctx, step)
		require.NoError(t, err)

		impl, ok := exec.(*executorImpl)
		require.True(t, ok)

		// Verify DAG-level config was applied
		assert.Equal(t, "us-east-1", impl.cfg.Region)
		assert.Equal(t, "dag-bucket", impl.cfg.Bucket)
		assert.Equal(t, "http://minio:9000", impl.cfg.Endpoint)
		assert.Equal(t, "dag-key", impl.cfg.AccessKeyID)
		assert.Equal(t, "dag-secret", impl.cfg.SecretAccessKey)
		assert.True(t, impl.cfg.ForcePathStyle)

		// Verify step-level executor config was also applied.
		assert.Equal(t, "/tmp/test.txt", impl.cfg.Source)
		assert.Equal(t, "uploads/test.txt", impl.cfg.Key)
	})

	t.Run("StepLevelConfig_overrides_DAGLevel", func(t *testing.T) {
		t.Parallel()

		// Create DAG-level S3 config
		dagS3 := &ir.S3Config{
			Region:          "us-east-1",
			Bucket:          "dag-bucket",
			Endpoint:        "http://minio:9000",
			AccessKeyID:     "dag-key",
			SecretAccessKey: "dag-secret",
		}

		ctx := context.Background()
		ctx = WithS3Config(ctx, dagS3)

		// Step with config that overrides DAG-level bucket and region
		step := ir.Step{
			Name:     "upload-step",
			Commands: []ir.CommandEntry{{Command: "upload"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "s3",
				Config: map[string]any{
					"source": "/tmp/test.txt",
					"key":    "uploads/test.txt",
					"bucket": "step-bucket", // Override
					"region": "eu-west-1",   // Override
				},
			},
		}

		exec, err := newExecutor(ctx, step)
		require.NoError(t, err)

		impl, ok := exec.(*executorImpl)
		require.True(t, ok)

		// Step-level overrides
		assert.Equal(t, "step-bucket", impl.cfg.Bucket)
		assert.Equal(t, "eu-west-1", impl.cfg.Region)

		// DAG-level values for non-overridden fields
		assert.Equal(t, "http://minio:9000", impl.cfg.Endpoint)
		assert.Equal(t, "dag-key", impl.cfg.AccessKeyID)
		assert.Equal(t, "dag-secret", impl.cfg.SecretAccessKey)
	})

	t.Run("NoDAGLevelConfig_uses_step_only", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		// No DAG-level config set

		step := ir.Step{
			Name:     "upload-step",
			Commands: []ir.CommandEntry{{Command: "upload"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "s3",
				Config: map[string]any{
					"source":            "/tmp/test.txt",
					"key":               "uploads/test.txt",
					"bucket":            "step-bucket",
					"region":            "ap-northeast-1",
					"access_key_id":     "step-key",
					"secret_access_key": "step-secret",
				},
			},
		}

		exec, err := newExecutor(ctx, step)
		require.NoError(t, err)

		impl, ok := exec.(*executorImpl)
		require.True(t, ok)

		assert.Equal(t, "step-bucket", impl.cfg.Bucket)
		assert.Equal(t, "ap-northeast-1", impl.cfg.Region)
		assert.Equal(t, "step-key", impl.cfg.AccessKeyID)
		assert.Equal(t, "step-secret", impl.cfg.SecretAccessKey)
	})

	t.Run("DAGLevelConfig_partial_override", func(t *testing.T) {
		t.Parallel()

		// Create DAG-level S3 config with all fields
		dagS3 := &ir.S3Config{
			Region:          "us-west-2",
			Bucket:          "dag-bucket",
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "dag-key",
			SecretAccessKey: "dag-secret",
			SessionToken:    "dag-token",
			ForcePathStyle:  true,
			DisableSSL:      true,
		}

		ctx := context.Background()
		ctx = WithS3Config(ctx, dagS3)

		// Step only overrides endpoint and force_path_style
		step := ir.Step{
			Name:     "list-step",
			Commands: []ir.CommandEntry{{Command: "list"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "s3",
				Config: map[string]any{
					"endpoint":         "http://production-s3:9000",
					"force_path_style": false,
				},
			},
		}

		exec, err := newExecutor(ctx, step)
		require.NoError(t, err)

		impl, ok := exec.(*executorImpl)
		require.True(t, ok)

		// Step-level overrides
		assert.Equal(t, "http://production-s3:9000", impl.cfg.Endpoint)
		assert.False(t, impl.cfg.ForcePathStyle)

		// DAG-level values preserved
		assert.Equal(t, "us-west-2", impl.cfg.Region)
		assert.Equal(t, "dag-bucket", impl.cfg.Bucket)
		assert.Equal(t, "dag-key", impl.cfg.AccessKeyID)
		assert.Equal(t, "dag-secret", impl.cfg.SecretAccessKey)
		assert.Equal(t, "dag-token", impl.cfg.SessionToken)
		assert.True(t, impl.cfg.DisableSSL)
	})
}

func TestNewExecutor_ValidationWithDAGConfig(t *testing.T) {
	t.Parallel()

	t.Run("DAGLevelBucket_satisfies_validation", func(t *testing.T) {
		t.Parallel()

		// DAG-level config provides bucket
		dagS3 := &ir.S3Config{
			Bucket: "dag-bucket",
		}

		ctx := context.Background()
		ctx = WithS3Config(ctx, dagS3)

		// Step doesn't specify bucket (uses DAG-level)
		step := ir.Step{
			Name:     "list-step",
			Commands: []ir.CommandEntry{{Command: "list"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type:   "s3",
				Config: map[string]any{},
			},
		}

		exec, err := newExecutor(ctx, step)
		require.NoError(t, err)

		impl, ok := exec.(*executorImpl)
		require.True(t, ok)
		assert.Equal(t, "dag-bucket", impl.cfg.Bucket)
	})

	t.Run("MissingBucket_fails_validation", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		// No DAG-level config

		step := ir.Step{
			Name:     "list-step",
			Commands: []ir.CommandEntry{{Command: "list"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type:   "s3",
				Config: map[string]any{},
			},
		}

		_, err := newExecutor(ctx, step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bucket")
	})
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	t.Run("valid_command", func(t *testing.T) {
		t.Parallel()

		step := ir.Step{
			ExecutorConfig: ir.ExecutorConfig{Type: "s3"},
			Commands:       []ir.CommandEntry{{Command: "upload"}},
		}
		err := validateStep(step)
		require.NoError(t, err)
	})

	t.Run("empty_command", func(t *testing.T) {
		t.Parallel()

		step := ir.Step{
			ExecutorConfig: ir.ExecutorConfig{Type: "s3"},
			Commands:       []ir.CommandEntry{{Command: ""}},
		}
		err := validateStep(step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "command is required")
	})

	t.Run("no_commands", func(t *testing.T) {
		t.Parallel()

		step := ir.Step{
			ExecutorConfig: ir.ExecutorConfig{Type: "s3"},
			Commands:       []ir.CommandEntry{},
		}
		err := validateStep(step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "command is required")
	})

	t.Run("different_executor_type_skipped", func(t *testing.T) {
		t.Parallel()

		step := ir.Step{
			ExecutorConfig: ir.ExecutorConfig{Type: "http"},
			Commands:       []ir.CommandEntry{},
		}
		err := validateStep(step)
		require.NoError(t, err)
	})
}

func TestNewExecutor_Operations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		command   string
		config    map[string]any
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "upload_valid",
			command: "upload",
			config: map[string]any{
				"bucket": "test-bucket",
				"source": "/tmp/file.txt",
				"key":    "uploads/file.txt",
			},
		},
		{
			name:    "download_valid",
			command: "download",
			config: map[string]any{
				"bucket":      "test-bucket",
				"key":         "downloads/file.txt",
				"destination": "/tmp/file.txt",
			},
		},
		{
			name:    "list_valid",
			command: "list",
			config: map[string]any{
				"bucket": "test-bucket",
			},
		},
		{
			name:    "delete_valid_with_key",
			command: "delete",
			config: map[string]any{
				"bucket": "test-bucket",
				"key":    "delete/file.txt",
			},
		},
		{
			name:    "delete_valid_with_prefix",
			command: "delete",
			config: map[string]any{
				"bucket": "test-bucket",
				"prefix": "delete/",
			},
		},
		{
			name:      "invalid_operation",
			command:   "copy",
			config:    map[string]any{"bucket": "test-bucket"},
			wantErr:   true,
			errSubstr: "unsupported s3 operation",
		},
		{
			name:      "upload_missing_source",
			command:   "upload",
			config:    map[string]any{"bucket": "test-bucket", "key": "test.txt"},
			wantErr:   true,
			errSubstr: "source is required",
		},
		{
			name:      "upload_missing_key",
			command:   "upload",
			config:    map[string]any{"bucket": "test-bucket", "source": "/tmp/file.txt"},
			wantErr:   true,
			errSubstr: "key is required",
		},
		{
			name:      "download_missing_destination",
			command:   "download",
			config:    map[string]any{"bucket": "test-bucket", "key": "file.txt"},
			wantErr:   true,
			errSubstr: "destination is required",
		},
		{
			name:      "delete_missing_key_and_prefix",
			command:   "delete",
			config:    map[string]any{"bucket": "test-bucket"},
			wantErr:   true,
			errSubstr: "key or prefix is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := ir.Step{
				Name:     tt.name,
				Commands: []ir.CommandEntry{{Command: tt.command}},
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "s3",
					Config: tt.config,
				},
			}

			_, err := newExecutor(context.Background(), step)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	assert.Equal(t, int64(10), cfg.PartSize)
	assert.Equal(t, 5, cfg.Concurrency)
	assert.Equal(t, 1000, cfg.MaxKeys)
	assert.Equal(t, "json", cfg.OutputFormat)
}

func TestS3ConfigVariableEvaluation(t *testing.T) {
	t.Parallel()

	t.Run("EvalObject_expands_variables_in_S3Config", func(t *testing.T) {
		t.Parallel()

		// Create S3Config with variable references
		cfg := ir.S3Config{
			Region:          "${AWS_REGION}",
			Bucket:          "${S3_BUCKET}",
			Endpoint:        "${S3_ENDPOINT}",
			AccessKeyID:     "${AWS_ACCESS_KEY_ID}",
			SecretAccessKey: "${AWS_SECRET_ACCESS_KEY}",
			SessionToken:    "${AWS_SESSION_TOKEN}",
			Profile:         "${AWS_PROFILE}",
		}

		// Variables to expand
		vars := map[string]string{
			"AWS_REGION":            "us-west-2",
			"S3_BUCKET":             "my-test-bucket",
			"S3_ENDPOINT":           "http://localhost:9000",
			"AWS_ACCESS_KEY_ID":     "test-access-key",
			"AWS_SECRET_ACCESS_KEY": "test-secret-key",
			"AWS_SESSION_TOKEN":     "test-session-token",
			"AWS_PROFILE":           "test-profile",
		}

		ctx := context.Background()
		evaluated, err := evalS3TestConfig(ctx, cfg, vars)
		require.NoError(t, err)

		// Verify all variables were expanded
		assert.Equal(t, "us-west-2", evaluated.Region)
		assert.Equal(t, "my-test-bucket", evaluated.Bucket)
		assert.Equal(t, "http://localhost:9000", evaluated.Endpoint)
		assert.Equal(t, "test-access-key", evaluated.AccessKeyID)
		assert.Equal(t, "test-secret-key", evaluated.SecretAccessKey)
		assert.Equal(t, "test-session-token", evaluated.SessionToken)
		assert.Equal(t, "test-profile", evaluated.Profile)
	})

	t.Run("EvalObject_partial_variable_expansion", func(t *testing.T) {
		t.Parallel()

		cfg := ir.S3Config{
			Region:   "${AWS_REGION}",
			Bucket:   "prefix-${BUCKET_NAME}-suffix",
			Endpoint: "http://${HOST}:${PORT}",
		}

		vars := map[string]string{
			"AWS_REGION":  "eu-west-1",
			"BUCKET_NAME": "data",
			"HOST":        "minio.local",
			"PORT":        "9000",
		}

		ctx := context.Background()
		evaluated, err := evalS3TestConfig(ctx, cfg, vars)
		require.NoError(t, err)

		assert.Equal(t, "eu-west-1", evaluated.Region)
		assert.Equal(t, "prefix-data-suffix", evaluated.Bucket)
		assert.Equal(t, "http://minio.local:9000", evaluated.Endpoint)
	})

	t.Run("EvalObject_missing_variable_preserved", func(t *testing.T) {
		t.Parallel()

		cfg := ir.S3Config{
			Region: "${UNDEFINED_VAR}",
			Bucket: "static-bucket",
		}

		vars := map[string]string{} // Empty vars

		ctx := context.Background()
		evaluated, err := evalS3TestConfig(ctx, cfg, vars)
		require.NoError(t, err)

		// Undefined variables are preserved as-is
		assert.Equal(t, "${UNDEFINED_VAR}", evaluated.Region)
		assert.Equal(t, "static-bucket", evaluated.Bucket)
	})

	t.Run("EvalObject_preserves_boolean_fields", func(t *testing.T) {
		t.Parallel()

		cfg := ir.S3Config{
			Region:         "${AWS_REGION}",
			ForcePathStyle: true,
			DisableSSL:     true,
		}

		vars := map[string]string{
			"AWS_REGION": "us-east-1",
		}

		ctx := context.Background()
		evaluated, err := evalS3TestConfig(ctx, cfg, vars)
		require.NoError(t, err)

		assert.Equal(t, "us-east-1", evaluated.Region)
		assert.True(t, evaluated.ForcePathStyle)
		assert.True(t, evaluated.DisableSSL)
	})
}

func evalS3TestConfig(ctx context.Context, cfg ir.S3Config, vars map[string]string) (ir.S3Config, error) {
	scope := cmnvalue.NewEnvScope(nil, false).WithEntries(vars, cmnvalue.EnvSourceStepEnv)
	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{Env: scope})
	got, err := resolver.Object(ctx, cfg, cmnvalue.HostConfigObjectField("s3"))
	if err != nil {
		return ir.S3Config{}, err
	}
	value, ok := got.(ir.S3Config)
	if !ok {
		return ir.S3Config{}, fmt.Errorf("type assertion failed: expected ir.S3Config, got %T", got)
	}
	return value, nil
}
