// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDAGJSONSecuritySensitiveFieldsExcluded verifies that sensitive fields
// with json:"-" tags are NOT serialized to JSON, preventing secret leakage.
// Note: Shell, ShellArgs, and WorkingDir are now serialized because they store
// unexpanded templates (e.g., "${MY_SHELL}"), not resolved secrets.
func TestDAGJSONSecuritySensitiveFieldsExcluded(t *testing.T) {
	dag := &DAG{
		Name:       "test-dag",
		Env:        []string{"SECRET_KEY=super_secret_value", "API_TOKEN=my_token"},
		Params:     []string{"password=mypassword"},
		ParamsJSON: `{"password":"mypassword"}`,
		Shell:      "${MY_SHELL}", // Now stores template, not expanded value
		ShellArgs:  []string{"${ARG}"},
		WorkingDir: "${WORK_DIR}", // Now stores template, not expanded value
		RegistryAuths: map[string]*AuthConfig{
			"docker.io": {
				Username: "user",
				Password: "docker_secret_password",
			},
		},
		SMTP:     &SMTPConfig{Password: "smtp_secret"},
		SSH:      &SSHConfig{Password: "ssh_secret"},
		YamlData: []byte("name: test-dag"),
	}

	data, err := json.MarshalIndent(dag, "", "  ")
	require.NoError(t, err)

	jsonStr := string(data)

	// Verify sensitive fields with secrets are NOT in JSON
	sensitiveFields := map[string]string{
		"Env":           "super_secret_value",
		"Params":        "mypassword",
		"ParamsJSON":    `"password"`,
		"RegistryAuths": "docker_secret_password",
		"SMTP":          "smtp_secret",
		"SSH":           "ssh_secret",
	}

	for field, secret := range sensitiveFields {
		assert.NotContains(t, jsonStr, secret,
			"SECURITY FAILURE: %s contains secret '%s' in JSON output", field, secret)
	}

	// Shell, ShellArgs, WorkingDir now contain templates (safe to serialize)
	assert.Contains(t, jsonStr, "${MY_SHELL}", "Shell template should be preserved in JSON")
	assert.Contains(t, jsonStr, "${WORK_DIR}", "WorkingDir template should be preserved in JSON")

	// Verify safe fields ARE in JSON
	assert.Contains(t, jsonStr, "test-dag", "name should be preserved in JSON")
	assert.Contains(t, jsonStr, "yamlData", "yamlData should be preserved in JSON")
}

// TestContainerJSONEnvSerialized verifies that Container.Env is serialized to JSON
// so it can be correctly restored when ReadDAG deserializes from the stored DAGDefinition file.
func TestContainerJSONEnvSerialized(t *testing.T) {
	container := &Container{
		Image: "nginx:latest",
		Env:   []string{"DB_PASSWORD=super_secret", "API_KEY=my_api_key"},
	}

	data, err := json.MarshalIndent(container, "", "  ")
	require.NoError(t, err)

	jsonStr := string(data)

	// Container.Env MUST be in JSON so ReadDAG can restore it from DAGDefinition
	assert.Contains(t, jsonStr, "DB_PASSWORD=super_secret", "Container.Env must be serialized for DAGDefinition persistence")
	assert.Contains(t, jsonStr, "API_KEY=my_api_key", "Container.Env must be serialized for DAGDefinition persistence")

	// Image should be preserved
	assert.Contains(t, jsonStr, "nginx:latest", "Container.Image should be preserved in JSON")
}

// TestDAGRoundTripMissingFields verifies that when loading a DAG from JSON,
// the excluded fields are empty (which triggers re-evaluation from YamlData).
// Note: Shell, ShellArgs, and WorkingDir are now preserved in JSON since they
// store templates, not resolved secrets.
func TestDAGRoundTripMissingFields(t *testing.T) {
	original := &DAG{
		Name:       "test-dag",
		Env:        []string{"SECRET_KEY=value"},
		Params:     []string{"param1=value1"},
		ParamsJSON: `{"param1":"value1"}`,
		Shell:      "${MY_SHELL}",
		ShellArgs:  []string{"${ARG}"},
		WorkingDir: "${WORK_DIR}",
		RegistryAuths: map[string]*AuthConfig{
			"docker.io": {Password: "secret"},
		},
		SMTP:     &SMTPConfig{Password: "smtp_secret"},
		SSH:      &SSHConfig{Password: "ssh_secret"},
		YamlData: []byte("name: test-dag\nenv:\n  SECRET_KEY: value"),
	}

	// Serialize to JSON
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Deserialize from JSON
	var loaded DAG
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	// Sensitive fields should be empty after round-trip
	assert.Empty(t, loaded.Env, "Env should be empty after JSON round-trip")
	assert.Empty(t, loaded.Params, "Params should be empty after JSON round-trip")
	assert.Empty(t, loaded.ParamsJSON, "ParamsJSON should be empty after JSON round-trip")
	assert.Nil(t, loaded.RegistryAuths, "RegistryAuths should be nil after JSON round-trip")
	assert.Nil(t, loaded.SMTP, "SMTP should be nil after JSON round-trip")
	assert.Nil(t, loaded.SSH, "SSH should be nil after JSON round-trip")

	// Template fields are now preserved (they contain templates, not secrets)
	assert.Equal(t, "${MY_SHELL}", loaded.Shell, "Shell template should be preserved")
	assert.Equal(t, []string{"${ARG}"}, loaded.ShellArgs, "ShellArgs template should be preserved")
	assert.Equal(t, "${WORK_DIR}", loaded.WorkingDir, "WorkingDir template should be preserved")

	// Safe fields should be preserved
	assert.Equal(t, "test-dag", loaded.Name, "Name should be preserved")
	assert.NotEmpty(t, loaded.YamlData, "YamlData should be preserved")
}

// TestJSONFieldTagsPresent verifies all sensitive fields have json:"-" tag.
// Note: Shell, ShellArgs, and WorkingDir are now serialized (they contain templates).
func TestJSONFieldTagsPresent(t *testing.T) {
	dag := &DAG{
		Env:           []string{"test"},
		Params:        []string{"test"},
		ParamsJSON:    "test",
		Shell:         "test",
		ShellArgs:     []string{"test"},
		WorkingDir:    "test",
		RegistryAuths: map[string]*AuthConfig{"test": {}},
		SMTP:          &SMTPConfig{Password: "test"},
		SSH:           &SSHConfig{Password: "test"},
	}

	data, err := json.Marshal(dag)
	require.NoError(t, err)

	jsonStr := string(data)

	// These fields should NOT appear in JSON output (contain secrets)
	sensitiveFieldNames := []string{
		`"env"`,
		`"params"`,
		`"paramsJSON"`,
		`"registryAuths"`,
		`"smtp"`,
		`"ssh"`,
	}

	for _, fieldName := range sensitiveFieldNames {
		if strings.Contains(jsonStr, fieldName) {
			t.Errorf("SECURITY: JSON output contains sensitive field %s: %s", fieldName, jsonStr)
		}
	}

	// These fields SHOULD appear in JSON output (contain templates, not secrets)
	templateFieldNames := []string{
		`"shell"`,
		`"shellArgs"`,
		`"workingDir"`,
	}

	for _, fieldName := range templateFieldNames {
		assert.Contains(t, jsonStr, fieldName, "Template field %s should be in JSON output", fieldName)
	}
}
