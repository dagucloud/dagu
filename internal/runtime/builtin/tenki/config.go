// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tenki

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	executorType = "tenki"

	// defaultCreateTimeout bounds how long we wait for a fresh sandbox to
	// become exec-ready before failing the step.
	defaultCreateTimeout = 3 * time.Minute

	// defaultMaxDuration caps a created sandbox's lifetime as a backstop against
	// leaks when cleanup never runs, e.g. after a host crash; 0 defers to server.
	defaultMaxDuration = time.Hour

	// defaultShell runs the step command/script inside the sandbox. The base
	// image ships /bin/sh, so it is a safe POSIX default.
	defaultShell = "/bin/sh"
)

// config is the parsed `with:` configuration for the Tenki sandbox executor.
type config struct {
	Name          string
	Image         string
	CPUCores      int32
	MemoryMB      int32
	SessionID     string
	KeepSession   bool
	CreateTimeout time.Duration
	MaxDuration   time.Duration
	Env           []string
	WorkingDir    string
	Shell         string
	ShellArgs     []string
	APIKey        string
	APIURL        string
	ProjectID     string
	WorkspaceID   string
}

// mapConfig mirrors config for mapstructure decoding of the `with:` map.
type mapConfig struct {
	Name          string   `mapstructure:"name"`
	Image         string   `mapstructure:"image"`
	CPUCores      int32    `mapstructure:"cpu_cores"`
	MemoryMB      int32    `mapstructure:"memory_mb"`
	SessionID     string   `mapstructure:"session_id"`
	KeepSession   bool     `mapstructure:"keep_session"`
	CreateTimeout string   `mapstructure:"create_timeout"`
	MaxDuration   string   `mapstructure:"max_duration"`
	Env           []string `mapstructure:"env"`
	WorkingDir    string   `mapstructure:"working_dir"`
	Shell         string   `mapstructure:"shell"`
	ShellArgs     []string `mapstructure:"shell_args"`
	APIKey        string   `mapstructure:"api_key"`
	APIURL        string   `mapstructure:"api_url"`
	ProjectID     string   `mapstructure:"project_id"`
	WorkspaceID   string   `mapstructure:"workspace_id"`
}

func loadConfig(mapCfg map[string]any) (config, error) {
	var def mapConfig
	md, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &def,
		WeaklyTypedInput: true,
	})
	if err != nil {
		return config{}, fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := md.Decode(mapCfg); err != nil {
		return config{}, fmt.Errorf("failed to decode tenki config: %w", err)
	}

	createTimeout, err := parseDuration("create_timeout", def.CreateTimeout, defaultCreateTimeout)
	if err != nil {
		return config{}, err
	}
	maxDuration, err := parseDuration("max_duration", def.MaxDuration, defaultMaxDuration)
	if err != nil {
		return config{}, err
	}
	shell, shellArgs, err := parseShellConfig(def.Shell, def.ShellArgs)
	if err != nil {
		return config{}, fmt.Errorf("failed to parse shell config: %w", err)
	}

	return config{
		Name:          def.Name,
		Image:         def.Image,
		CPUCores:      def.CPUCores,
		MemoryMB:      def.MemoryMB,
		SessionID:     def.SessionID,
		KeepSession:   def.KeepSession,
		CreateTimeout: createTimeout,
		MaxDuration:   maxDuration,
		Env:           def.Env,
		WorkingDir:    def.WorkingDir,
		Shell:         shell,
		ShellArgs:     shellArgs,
		APIKey:        def.APIKey,
		APIURL:        def.APIURL,
		ProjectID:     def.ProjectID,
		WorkspaceID:   def.WorkspaceID,
	}, nil
}

// parseDuration parses a duration string, returning def when empty.
func parseDuration(field, s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration %q: %w", field, s, err)
	}
	return d, nil
}

// parseShellConfig splits a shell string that may embed arguments like
// "/bin/bash -o pipefail" and appends explicit shell_args, mirroring ssh.
func parseShellConfig(shell string, args []string) (string, []string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return "", slices.Clone(args), nil
	}
	parsedShell, parsedArgs, err := cmdutil.SplitCommand(shell)
	if err != nil {
		return "", nil, err
	}
	allArgs := append(parsedArgs, args...)
	return strings.TrimSpace(parsedShell), allArgs, nil
}

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"name":           {Type: "string", Description: "Human-readable sandbox session name"},
		"image":          {Type: "string", Description: "Tenki registry image ref to launch the sandbox from. If unset, uses the base image"},
		"cpu_cores":      {Type: "integer", Description: "vCPU cores for a new sandbox (1-16)"},
		"memory_mb":      {Type: "integer", Description: "Memory in MB for a new sandbox"},
		"session_id":     {Type: "string", Description: "Reuse an existing sandbox session instead of creating one; the session is not terminated"},
		"keep_session":   {Type: "boolean", Description: "Keep a created sandbox running after the step instead of terminating it"},
		"create_timeout": {Type: "string", Description: "How long to wait for a new sandbox to become ready (e.g. '3m', '90s')"},
		"max_duration":   {Type: "string", Description: "Lifetime cap for a created sandbox as a leak backstop (e.g. '1h'); '0s' defers to the server default"},
		"env":            {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Session environment variables in KEY=VALUE form"},
		"working_dir":    {Type: "string", Description: "Working directory inside the sandbox"},
		"shell":          {Type: "string", Description: "Shell used to run the command/script; defaults to /bin/sh. May embed args, e.g. '/bin/bash -o pipefail'"},
		"shell_args":     {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Additional shell arguments (e.g. ['-o', 'pipefail'])"},
		"api_key":        {Type: "string", Description: "Tenki API key; falls back to the TENKI_API_KEY environment variable"},
		"api_url":        {Type: "string", Description: "Tenki API base URL; falls back to TENKI_API_URL or the default endpoint"},
		"project_id":     {Type: "string", Description: "Tenki project ID to create the sandbox in; falls back to the TENKI_PROJECT_ID environment variable"},
		"workspace_id":   {Type: "string", Description: "Tenki workspace ID to create the sandbox in; falls back to the TENKI_WORKSPACE_ID environment variable"},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
