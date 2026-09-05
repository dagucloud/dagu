// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec056_s3 holds black-box conformance tests for
// Spec 056: S3 Actions.
package spec056_s3_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

type uploadResult struct {
	Success      bool   `json:"success"`
	Bucket       string `json:"bucket"`
	Key          string `json:"key"`
	ETag         string `json:"etag"`
	Size         int64  `json:"size"`
	ContentType  string `json:"contentType"`
	StorageClass string `json:"storageClass"`
}

type downloadResult struct {
	Success     bool   `json:"success"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
}

type listObject struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

type listResult struct {
	Success    bool         `json:"success"`
	Objects    []listObject `json:"objects"`
	TotalCount int          `json:"totalCount"`
}

type deleteResult struct {
	Success      bool     `json:"success"`
	DeletedCount int      `json:"deletedCount"`
	DeletedKeys  []string `json:"deletedKeys"`
}

func TestS3Live(t *testing.T) {
	dockerClient := requireDockerDaemon(t)
	port := startMinIOContainer(t, dockerClient)
	endpointEnv := "S3_ENDPOINT=" + s3Endpoint(port)

	t.Run("uploads a local file and reports its bucket, key, and etag", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "hello s3")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "upload_basic.yaml")
		result.ExpectExitCode(0)

		var upload uploadResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &upload))
		require.True(t, upload.Success)
		require.Equal(t, bucket, upload.Bucket)
		require.Equal(t, "hello.txt", upload.Key)
		require.EqualValues(t, len("hello s3"), upload.Size)
		require.NotEmpty(t, upload.ETag)
	})

	t.Run("a downloaded object matches what was uploaded", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "round trip content")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "download_roundtrip.yaml")
		result.ExpectExitCode(0)

		var download downloadResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &download))
		require.True(t, download.Success)
		require.EqualValues(t, len("round trip content"), download.Size)

		data, err := os.ReadFile(dagu.ProjectPath("downloaded/hello.txt")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.Equal(t, "round trip content", string(data))
	})

	t.Run("with.content_type is stored and returned by a later download", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "content_type_roundtrip.yaml")
		result.ExpectExitCode(0)

		var download downloadResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &download))
		require.Equal(t, "application/x-custom", download.ContentType)
	})

	t.Run("without with.recursive, list groups keys under a common prefix instead of descending into it", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "list_prefix_nonrecursive.yaml")
		result.ExpectExitCode(0)

		var list listResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &list))
		require.Equal(t, 3, list.TotalCount)
		keys := make([]string, len(list.Objects))
		for i, o := range list.Objects {
			keys[i] = o.Key
		}
		require.ElementsMatch(t, []string{"a/1.txt", "a/2.txt", "a/nested/"}, keys)
	})

	t.Run("with.recursive descends into every common prefix", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "list_prefix_recursive.yaml")
		result.ExpectExitCode(0)

		var list listResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &list))
		require.Equal(t, 3, list.TotalCount)
		keys := make([]string, len(list.Objects))
		for i, o := range list.Objects {
			keys[i] = o.Key
		}
		require.ElementsMatch(t, []string{"a/1.txt", "a/2.txt", "a/nested/3.txt"}, keys)
	})

	t.Run("with.output_format jsonl writes a bare object instead of an objects/totalCount envelope", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "list_jsonl.yaml")
		result.ExpectExitCode(0)

		var object listObject
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &object))
		require.Equal(t, "only.txt", object.Key)
	})

	t.Run("deletes a single key by with.key", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "delete_single.yaml")
		result.ExpectExitCode(0)

		var del deleteResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &del))
		require.True(t, del.Success)
		require.Equal(t, 1, del.DeletedCount)
		require.Equal(t, []string{"hello.txt"}, del.DeletedKeys)
	})

	t.Run("with.prefix deletes every matching key regardless of nesting", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "delete_by_prefix.yaml")
		result.ExpectExitCode(0)

		var list listResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &list))
		require.Equal(t, 1, list.TotalCount)
		require.Equal(t, "b/3.txt", list.Objects[0].Key)
	})

	t.Run("a step-level with.bucket overrides the DAG-level default", func(t *testing.T) {
		t.Parallel()

		defaultBucket := createTestBucket(t, port)
		overrideBucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + defaultBucket, "S3_BUCKET_OVERRIDE=" + overrideBucket}
		result := dagu.RunWithEnv(env, "start", "dag_level_defaults.yaml")
		result.ExpectExitCode(0)

		var upload uploadResult
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &upload))
		require.Equal(t, overrideBucket, upload.Bucket)
	})

	t.Run("downloading a key that does not exist fails", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "download_missing_key.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("s3: download failed")
		result.ExpectStderrContains("NoSuchKey")
	})

	// download_missing_bucket, download_bad_credentials, and the case above
	// all wrap the underlying AWS error under the same generic "s3:
	// download failed" prefix and the same exit code -- the executor does
	// not distinguish a missing key from a missing bucket or bad
	// credentials at that level, only in the wrapped message text.
	t.Run("downloading from a bucket that does not exist fails the same way as a missing key", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		keyMissing := dagu.RunWithEnv([]string{endpointEnv, "S3_BUCKET=" + createTestBucket(t, port)}, "start", "download_missing_key.yaml")
		keyMissing.ExpectNonZeroExitCode()

		bucketMissing := dagu.RunWithEnv([]string{endpointEnv}, "start", "download_missing_bucket.yaml")
		bucketMissing.ExpectNonZeroExitCode()
		bucketMissing.ExpectStderrContains("s3: download failed")
		bucketMissing.ExpectStderrContains("NoSuchBucket")
		require.Equal(t, keyMissing.ExitCode(), bucketMissing.ExitCode())
	})

	t.Run("bad credentials fail the same way as a missing key or bucket", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)

		keyMissing := dagu.RunWithEnv([]string{endpointEnv, "S3_BUCKET=" + bucket}, "start", "download_missing_key.yaml")
		keyMissing.ExpectNonZeroExitCode()

		badCreds := dagu.RunWithEnv([]string{endpointEnv, "S3_BUCKET=" + bucket}, "start", "download_bad_credentials.yaml")
		badCreds.ExpectNonZeroExitCode()
		badCreds.ExpectStderrContains("s3: download failed")
		badCreds.ExpectStderrContains("InvalidAccessKeyId")
		require.Equal(t, keyMissing.ExitCode(), badCreds.ExitCode())
	})

	t.Run("uploading a source file that does not exist locally fails", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "upload_missing_local_source.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("s3: source not found")
	})

	t.Run("with.sse aws:kms without with.sse_kms_key_id fails", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "sse_kms_missing_key.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("sse_kms_key_id is required when sse is 'aws:kms'")
	})

	t.Run("an unrecognized with.storage_class fails", func(t *testing.T) {
		t.Parallel()

		bucket := createTestBucket(t, port)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "x")

		env := []string{endpointEnv, "S3_BUCKET=" + bucket}
		result := dagu.RunWithEnv(env, "start", "invalid_storage_class.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`invalid storage class "NOT_A_REAL_CLASS"`)
	})
}

// TestS3Validation proves that s3's registered step validator only checks
// that an operation is named (upload/download/list/delete): the full set
// of per-operation requirements (bucket, source/key/destination,
// key-or-prefix) is enforced only when a step runs, not at DAG-build time
// -- unlike a negative part_size/concurrency or an invalid sse/output_format
// value, which the registered JSON Schema rejects at build time regardless.
func TestS3Validation(t *testing.T) {
	t.Parallel()

	t.Run("with.bucket missing everywhere fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "missing_bucket.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "missing_bucket.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("bucket is required")
	})

	t.Run("with.source and with.key missing for s3.upload fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "upload_missing_source_and_key.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "upload_missing_source_and_key.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("source is required for upload")
	})

	t.Run("with.destination missing for s3.download fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "download_missing_destination.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "download_missing_destination.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("destination is required for download")
	})

	t.Run("with.key and with.prefix both missing for s3.delete fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "delete_missing_key_and_prefix.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "delete_missing_key_and_prefix.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("key or prefix is required for delete")
	})

	// The Go config code treats part_size: 0 and concurrency: 0 as "use the
	// default", but the registered JSON Schema declares a minimum of 5 and 1
	// respectively -- so writing either explicitly, rather than omitting the
	// field, is rejected at DAG-build time before that Go code ever runs.
	t.Run("an explicit with.part_size of 0 fails validate despite the Go default meaning the same thing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "part_size_zero.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("minimum")
	})

	t.Run("an explicit with.concurrency of 0 fails validate despite the Go default meaning the same thing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "concurrency_zero.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("minimum")
	})

	t.Run("an unrecognized with.sse value fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "sse_invalid_enum.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("enum")
	})

	t.Run("an unrecognized with.output_format value fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "output_format_invalid_enum.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("enum")
	})
}
