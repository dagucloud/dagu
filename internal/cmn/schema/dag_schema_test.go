// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDAGSchemaParams(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "StringPositionalParams",
			spec: `
params: first second
steps:
  - run: echo "$1 $2"
`,
		},
		{
			name: "LegacyNamedList",
			spec: `
params:
  - ENVIRONMENT: prod
  - COUNT: 3
steps:
  - run: echo "${ENVIRONMENT} ${COUNT}"
`,
		},
		{
			name: "InlineRichParams",
			spec: `
params:
  - name: region
    type: string
    default: us-east-1
    enum: [us-east-1, us-west-2]
    description: Deployment region
  - name: count
    type: integer
    default: 3
    minimum: 1
    maximum: 10
  - name: debug
    type: boolean
    default: false
steps:
  - run: echo "${region} ${count} ${debug}"
`,
		},
		{
			name: "MixedLegacyAndInline",
			spec: `
params:
  - name: environment
    type: string
    default: staging
    enum: [dev, staging, prod]
  - TAG: latest
steps:
  - run: echo "${environment} ${TAG}"
`,
		},
		{
			name: "ExternalSchemaMode",
			spec: `
params:
  schema: ./params.schema.json
  values:
    batch_size: 25
    environment: staging
steps:
  - run: echo done
`,
		},
		{
			name: "TopLevelInlineSchemaMode",
			spec: `
params:
  type: object
  properties:
    batch_size:
      type: integer
    debug:
      type: boolean
  additionalProperties: false
steps:
  - run: echo done
`,
		},
		{
			name: "RejectTopLevelInlineSchemaWithPropertiesArray",
			spec: `
params:
  type: object
  properties:
    - name: region
      type: string
  required: [region]
steps:
  - run: echo done
`,
			wantErr: "params",
		},
		{
			name: "ExternalInlineSchemaMode",
			spec: `
params:
  schema:
    type: object
    properties:
      batch_size:
        type: integer
  values:
    batch_size: 25
steps:
  - run: echo done
`,
		},
		{
			name: "ExternalBooleanSchemaModeWithValues",
			spec: `
params:
  schema: true
  values:
    batch_size: 25
steps:
  - run: echo done
`,
		},
		{
			name: "RejectCamelCaseInlineField",
			spec: `
params:
  - name: project_name
    type: string
    minLength: 3
steps:
  - run: echo "${project_name}"
`,
			wantErr: "params",
		},
		{
			name: "RejectLegacyNestedMapInlineEntry",
			spec: `
params:
  - project_name:
      type: string
      default: demo
steps:
  - run: echo hi
`,
			wantErr: "params",
		},
		{
			name: "RejectNameOnlyRichEntry",
			spec: `
params:
  - name: foo
steps:
  - run: echo "${foo}"
`,
			wantErr: "params",
		},
		{
			name: "LegacyMapAllowsSchemaKey",
			spec: `
params:
  schema: prod
  region: us
steps:
  - run: echo "${schema} ${region}"
`,
		},
		{
			name: "LegacyMapAllowsPropertiesObjectWithoutTypeObject",
			spec: `
params:
  properties:
    foo: bar
  region: us
steps:
  - run: echo "${region}"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaSecrets(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "ProviderAndKey",
			spec: `
secrets:
  - name: DB_PASSWORD
    provider: env
    key: DB_PASSWORD
steps:
  - run: echo done
`,
		},
		{
			name: "RegistryRef",
			spec: `
secrets:
  - name: DB_PASSWORD
    ref: prod/db-password
steps:
  - run: echo done
`,
		},
		{
			name: "RejectRefAndProviderKey",
			spec: `
secrets:
  - name: DB_PASSWORD
    ref: prod/db-password
    provider: env
    key: DB_PASSWORD
steps:
  - run: echo done
`,
			wantErr: "secrets",
		},
		{
			name: "RejectOptionsWithRegistryRef",
			spec: `
secrets:
  - name: DB_PASSWORD
    ref: prod/db-password
    options:
      region: us-east-1
steps:
  - run: echo done
`,
			wantErr: "secrets",
		},
		{
			name: "RejectDaguPrefixedName",
			spec: `
secrets:
  - name: DAGU_TOKEN
    provider: env
    key: TOKEN
steps:
  - run: echo done
`,
			wantErr: "secrets",
		},
		{
			name: "RejectInvalidRegistryRef",
			spec: `
secrets:
  - name: DB_PASSWORD
    ref: Prod/db_password
steps:
  - run: echo done
`,
			wantErr: "secrets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaStepOutputObject(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "LiteralObjectValue",
			spec: `
steps:
  - run: echo hi
    output:
      meta:
        version: v1.2.3
`,
		},
		{
			name: "StructuredSourceEntry",
			spec: `
steps:
  - run: echo hi
    output:
      version:
        from: stdout
        decode: json
        select: .version
`,
		},
		{
			name: "RejectInvalidStructuredSource",
			spec: `
steps:
  - run: echo hi
    output:
      version:
        from: network
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectFileSourceWithoutPath",
			spec: `
steps:
  - run: echo hi
    output:
      version:
        from: file
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectInvalidDecode",
			spec: `
steps:
  - run: echo hi
    output:
      version:
        from: stdout
        decode: xml
`,
			wantErr: "did not validate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaStepV2(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "Run",
			spec: `
steps:
  - run: echo ok
    with:
      shell: bash -e
      shell_args: [-c]
`,
		},
		{
			name: "ActionDagRunParallel",
			spec: `
steps:
  - action: dag.run
    parallel:
      items: [a, b]
    with:
      dag: child
      params:
        item: ${ITEM}
`,
		},
		{
			name: "CustomAction",
			spec: `
actions:
  slack.notify:
    input_schema:
      type: object
      properties:
        text:
          type: string
    output_schema:
      type: object
      properties:
        ok:
          type: boolean
    template:
      action: http.request
      with:
        method: POST
        url: ${SLACK_WEBHOOK_URL}
steps:
  - action: slack.notify
    with:
      text: hello
`,
		},
		{
			name: "RejectRunAndAction",
			spec: `
steps:
  - run: echo ok
    action: log.write
    with:
      message: ok
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectRunAndCommand",
			spec: `
steps:
  - run: echo ok
    command: echo legacy
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectRunAndExec",
			spec: `
steps:
  - run: echo ok
    exec:
      command: /bin/echo
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectRunAndShellArgs",
			spec: `
steps:
  - run: echo ok
    shell_args: [-c]
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionAndCommand",
			spec: `
steps:
  - action: log.write
    command: echo legacy
    with:
      message: ok
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionAndCall",
			spec: `
steps:
  - action: log.write
    call: child
    with:
      message: ok
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionAndScript",
			spec: `
steps:
  - action: log.write
    script: echo legacy
    with:
      message: ok
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionExecMissingCommand",
			spec: `
steps:
  - action: exec
    with:
      args: [hello]
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionJQFilterDataAndInput",
			spec: `
steps:
  - action: jq.filter
    with:
      filter: .name
      data:
        name: Alice
      input: input.json
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectActionNoopWithConfig",
			spec: `
steps:
  - action: noop
    with:
      message: ignored
`,
			wantErr: "did not validate",
		},
		{
			name: "RejectCustomActionTemplateLegacyExecutionField",
			spec: `
actions:
  bad.notify:
    input_schema:
      type: object
    template:
      action: log.write
      command: echo legacy
      with:
        message: ok
steps:
  - action: bad.notify
`,
			wantErr: "not: validated against",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaSchedule(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "TypedCronStart",
			spec: `
schedule:
  - kind: cron
    expression: "0 * * * *"
steps:
  - run: echo hi
`,
		},
		{
			name: "TypedOneOffStart",
			spec: `
schedule:
  start:
    kind: at
    at: "2026-03-29T02:10:00+01:00"
steps:
  - run: echo hi
`,
		},
		{
			name: "RejectTypedCronWithoutExpression",
			spec: `
schedule:
  - kind: cron
steps:
  - run: echo hi
`,
			wantErr: "schedule",
		},
		{
			name: "RejectTypedAtWithoutTimestamp",
			spec: `
schedule:
  - kind: at
steps:
  - run: echo hi
`,
			wantErr: "schedule",
		},
		{
			name: "RejectTypedStartWithBothFields",
			spec: `
schedule:
  start:
    kind: cron
    expression: "0 * * * *"
    at: "2026-03-29T02:10:00+01:00"
steps:
  - run: echo hi
`,
			wantErr: "schedule",
		},
		{
			name: "RejectTypedStopWithoutExpression",
			spec: `
schedule:
  stop:
    kind: cron
steps:
  - run: echo hi
`,
			wantErr: "schedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaRootRetryPolicy(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "NumericValues",
			spec: `
name: retryable-dag
retry_policy:
  limit: 3
  interval_sec: 10
  backoff: 2.0
  max_interval_sec: 60
steps:
  - run: echo hi
`,
		},
		{
			name: "NumericStringsAndBooleanBackoff",
			spec: `
name: retryable-dag
retry_policy:
  limit: "03"
  interval_sec: "10"
  backoff: false
  max_interval_sec: "60"
steps:
  - run: echo hi
`,
		},
		{
			name: "LimitZero",
			spec: `
name: retryable-dag
retry_policy:
  limit: 0
steps:
  - run: echo hi
`,
		},
		{
			name: "StringLimitZero",
			spec: `
name: retryable-dag
retry_policy:
  limit: "0"
steps:
  - run: echo hi
`,
		},
		{
			name: "RejectsMissingLimit",
			spec: `
name: retryable-dag
retry_policy:
  interval_sec: 10
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsNonNumericStringLimit",
			spec: `
name: retryable-dag
retry_policy:
  limit: three
  interval_sec: 10
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsNegativeLimit",
			spec: `
name: retryable-dag
retry_policy:
  limit: -1
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsNegativeStringLimit",
			spec: `
name: retryable-dag
retry_policy:
  limit: "-1"
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsNonNumericStringInterval",
			spec: `
name: retryable-dag
retry_policy:
  limit: 3
  interval_sec: later
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsZeroInterval",
			spec: `
name: retryable-dag
retry_policy:
  limit: 1
  interval_sec: 0
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsZeroMaxInterval",
			spec: `
name: retryable-dag
retry_policy:
  limit: 1
  max_interval_sec: 0
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsBackoffOnePointZero",
			spec: `
name: retryable-dag
retry_policy:
  limit: 3
  interval_sec: 10
  backoff: 1.0
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
		{
			name: "RejectsUnknownRetryField",
			spec: `
name: retryable-dag
retry_policy:
  limit: 3
  unknown_retry_field: 10
steps:
  - run: echo hi
`,
			wantErr: "retry_policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaRootRetryPolicyRejectsExitCode(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, `
name: retryable-dag
retry_policy:
  limit: 3
  interval_sec: 10
  exit_code: [1]
steps:
  - run: echo hi
`)

	err := resolved.Validate(doc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "retry_policy")
}

func TestDAGSchemaStepRetryPolicyRejectsUnknownField(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, `
steps:
  - run: echo hi
    retry_policy:
      limit: 1
      interval_sec: 5
      unknown_retry_field: 2
`)

	err := resolved.Validate(doc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "steps")
}

func TestDAGSchemaStepWithFieldAndConfigAlias(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "CanonicalWith",
			spec: `
steps:
  - action: http.request
    with:
      method: GET
      url: https://example.com
      timeout: 30
`,
		},
		{
			name: "LegacyConfigAlias",
			spec: `
steps:
  - type: http
    command: GET https://example.com
    config:
      timeout: 30
`,
		},
		{
			name: "RejectBothWithAndConfig",
			spec: `
steps:
  - type: http
    command: GET https://example.com
    with:
      timeout: 30
    config:
      timeout: 60
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaLogStepRequiresMessage(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "CanonicalWith",
			spec: `
steps:
  - action: log.write
    with:
      message: hello
`,
		},
		{
			name: "LegacyConfigAlias",
			spec: `
steps:
  - type: log
    config:
      message: hello
`,
		},
		{
			name: "RejectMissingWithOrConfig",
			spec: `
steps:
  - action: log.write
`,
			wantErr: "steps",
		},
		{
			name: "RejectMissingMessage",
			spec: `
steps:
  - action: log.write
    with: {}
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaLogExecutorObjectRequiresMessage(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchemaDefinition(t, "executorObject")

	tests := []struct {
		name    string
		value   map[string]any
		wantErr string
	}{
		{
			name: "Valid",
			value: map[string]any{
				"type": "log",
				"config": map[string]any{
					"message": "hello",
				},
			},
		},
		{
			name: "RejectMissingConfig",
			value: map[string]any{
				"type": "log",
			},
			wantErr: "config",
		},
		{
			name: "RejectMissingMessage",
			value: map[string]any{
				"type":   "log",
				"config": map[string]any{},
			},
			wantErr: "message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := resolved.Validate(tt.value)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaSSHExecutorPort(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, `
steps:
  - action: ssh.run
    with:
      command: hostname
      host: example.com
      user: deploy
      port: 22
`)

	require.NoError(t, resolved.Validate(doc))
}

func TestDAGSchemaSFTPExecutor(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	sftpConfigSchema := mustResolveDAGSchemaDefinition(t, "sftpExecutorConfig")

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "WithConfig",
			spec: `
steps:
  - action: sftp.upload
    with:
      host: example.com
      user: deploy
      port: "22"
      source: ./backup.tar.gz
      destination: /srv/backups/backup.tar.gz
`,
		},
		{
			name: "NumericPorts",
			spec: `
steps:
  - action: sftp.upload
    with:
      host: example.com
      user: deploy
      port: 22
      source: ./backup.tar.gz
      destination: /srv/backups/backup.tar.gz
      bastion:
        host: bastion.example.com
        user: deploy
        port: 2222
`,
		},
		{
			name: "LegacyConfigAlias",
			spec: `
steps:
  - type: sftp
    config:
      host: example.com
      source: /srv/backups/backup.tar.gz
      destination: ./backup.tar.gz
      direction: download
`,
		},
		{
			name: "RejectInvalidDirection",
			spec: `
steps:
  - action: sftp.upload
    with:
      host: example.com
      user: deploy
      port: "22"
      source: ./backup.tar.gz
      destination: /srv/backups/backup.tar.gz
      direction: sync
`,
			wantErr: "direction",
		},
		{
			name: "RejectEmptySource",
			spec: `
steps:
  - action: sftp.upload
    with:
      host: example.com
      user: deploy
      port: "22"
      source: ""
      destination: /srv/backups/backup.tar.gz
`,
			wantErr: "source",
		},
		{
			name: "RejectUnknownConfigField",
			spec: `
steps:
  - action: sftp.upload
    with:
      host: example.com
      user: deploy
      port: "22"
      source: ./backup.tar.gz
      destination: /srv/backups/backup.tar.gz
      unknown_field: true
`,
			wantErr: "unknown_field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)

			configErr := sftpConfigSchema.Validate(firstStepConfig(t, doc))
			require.Error(t, configErr)
			require.Contains(t, configErr.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaKubernetes(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "RootDefaultsAllowOmittedImage",
			spec: `
kubernetes:
  namespace: batch
  service_account: dagu-runner

steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      command: echo hello
`,
		},
		{
			name: "StepConfigAllowsImageOmittedWhenRootDefaultsProvideIt",
			spec: `
kubernetes:
  image: alpine:3.20
  namespace: batch

steps:
  - id: report
    action: k8s.run
    with:
      cleanup_policy: keep
      command: echo hello
`,
		},
		{
			name: "StepConfigSupportsKubernetesAlias",
			spec: `
steps:
  - id: report
    action: kubernetes.run
    with:
      image: alpine:3.20
      namespace: batch
      cleanup_policy: keep
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
      volumes:
        - name: scratch
          empty_dir:
            size_limit: 256Mi
      volume_mounts:
        - name: scratch
          mount_path: /tmp/work
      command: [sh, -c, "echo hello"]
`,
		},
		{
			name: "SupportsExtendedKubernetesConfig",
			spec: `
kubernetes:
  pod_security_context:
    run_as_non_root: true

steps:
  - id: report
    action: kubernetes.run
    with:
      image: alpine:3.20
      security_context:
        run_as_non_root: true
        capabilities:
          drop: [ALL]
        seccomp_profile:
          type: RuntimeDefault
      pod_security_context:
        fs_group: 2000
        fs_group_change_policy: OnRootMismatch
        sysctls:
          - name: net.ipv4.ip_unprivileged_port_start
            value: "0"
      affinity:
        node_affinity:
          required_during_scheduling_ignored_during_execution:
            node_selector_terms:
              - match_expressions:
                  - key: kubernetes.io/arch
                    operator: In
                    values: [amd64]
        pod_anti_affinity:
          required_during_scheduling_ignored_during_execution:
            - topology_key: kubernetes.io/hostname
              label_selector:
                match_labels:
                  app: dagu
      termination_grace_period_seconds: 30
      priority_class_name: batch-high
      pod_failure_policy:
        rules:
          - action: Count
            on_exit_codes:
              operator: In
              values: [42]
          - action: Ignore
            on_pod_conditions:
              - type: DisruptionTarget
      command: echo hello
`,
		},
		{
			name: "AllowsClearingInheritedExtendedConfig",
			spec: `
kubernetes:
  affinity:
    node_affinity:
      required_during_scheduling_ignored_during_execution:
        node_selector_terms:
          - match_expressions:
              - key: kubernetes.io/arch
                operator: In
                values: [amd64]
  pod_failure_policy:
    rules:
      - action: Count
        on_exit_codes:
          operator: In
          values: [42]

steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      affinity: {}
      pod_failure_policy: {}
      command: echo hello
`,
		},
		{
			name: "RejectUnknownRootField",
			spec: `
kubernetes:
  unknown_field: true

steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      command: echo hello
`,
			wantErr: "kubernetes",
		},
		{
			name: "RejectInvalidEnvEntry",
			spec: `
steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      env:
        - value: missing-name
      command: echo hello
`,
			wantErr: "steps",
		},
		{
			name: "RejectInvalidEnvFromEntry",
			spec: `
steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      env_from:
        - prefix: APP_
      command: echo hello
`,
			wantErr: "steps",
		},
		{
			name: "RejectInvalidSeccompLocalhostProfile",
			spec: `
steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      security_context:
        seccomp_profile:
          localhost_profile: profiles/custom.json
      command: echo hello
`,
			wantErr: "steps",
		},
		{
			name: "RejectUnsupportedPodFailureAction",
			spec: `
steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      pod_failure_policy:
        rules:
          - action: FailIndex
            on_exit_codes:
              operator: In
              values: [42]
      command: echo hello
`,
			wantErr: "steps",
		},
		{
			name: "RejectUnknownStepField",
			spec: `
steps:
  - id: report
    action: kubernetes.run
    with:
      image: alpine:3.20
      unknown_field: true
      command: echo hello
`,
			wantErr: "steps",
		},
		{
			name: "RejectMultipleVolumeSources",
			spec: `
steps:
  - id: report
    action: k8s.run
    with:
      image: alpine:3.20
      volumes:
        - name: data
          empty_dir: {}
          secret:
            secret_name: app-secret
      command: echo hello
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaHarness(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{
			name: "RootDefaultsAndFallback",
			spec: `
harness:
  provider: claude
  model: sonnet
  bare: true
  fallback:
    - provider: codex
      full-auto: true

steps:
  - run: Write tests

  - action: harness.run
    with:
      prompt: Fix bugs
      model: opus
      effort: high
`,
		},
		{
			name: "CustomNamedProvider",
			spec: `
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt

steps:
  - action: harness.run
    with:
      prompt: Summarize the repository state
      provider: gemini
      model: gemini-2.5-pro
      yolo: true
`,
		},
		{
			name: "RequirePromptFlagForFlagPromptMode",
			spec: `
harnesses:
  gemini:
    binary: gemini
    prompt_mode: flag

steps:
  - action: harness.run
    with:
      prompt: Summarize the repository state
      provider: gemini
`,
			wantErr: "harnesses",
		},
		{
			name: "RejectPromptFlagOutsideFlagPromptMode",
			spec: `
harnesses:
  gemini:
    binary: gemini
    prompt_mode: stdin
    prompt_flag: --prompt

steps:
  - action: harness.run
    with:
      prompt: Summarize the repository state
      provider: gemini
`,
			wantErr: "harnesses",
		},
		{
			name: "RejectInvalidFallbackShape",
			spec: `
harness:
  provider: claude
  fallback:
    provider: codex

steps:
  - run: Write tests
`,
			wantErr: "harness",
		},
		{
			name: "RejectNestedFallbackInFallbackProvider",
			spec: `
steps:
  - action: harness.run
    with:
      prompt: Write tests
      provider: claude
      fallback:
        - provider: codex
          fallback:
            - provider: copilot
`,
			wantErr: "steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDAGSchemaRepoCopyMatchesEmbeddedSchema(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	repoSchemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "schemas", "dag.schema.json")
	repoSchemaJSON, err := os.ReadFile(repoSchemaPath)
	require.NoError(t, err)
	require.Equal(t, string(DAGSchemaJSON), string(repoSchemaJSON))
}

func mustResolveDAGSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(DAGSchemaJSON, &schema))

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	return resolved
}

func mustResolveDAGSchemaDefinition(t *testing.T, name string) *jsonschema.Resolved {
	t.Helper()

	var root jsonschema.Schema
	require.NoError(t, json.Unmarshal(DAGSchemaJSON, &root))

	_, ok := root.Definitions[name]
	require.True(t, ok, "schema definition %q should exist", name)

	schema := jsonschema.Schema{
		Ref:         "#/definitions/" + name,
		Definitions: root.Definitions,
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	return resolved
}

func mustParseYAMLDocument(t *testing.T, spec string) map[string]any {
	t.Helper()

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(spec), &doc))
	return doc
}

func firstStepConfig(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()

	steps, ok := doc["steps"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, steps)

	step, ok := steps[0].(map[string]any)
	require.True(t, ok)

	if config, ok := step["with"].(map[string]any); ok {
		return config
	}
	config, ok := step["config"].(map[string]any)
	require.True(t, ok)
	return config
}
