// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDAGShell tests the DAGShell function for DAG-level shell evaluation
func TestDAGShell(t *testing.T) {
	t.Run("ReturnsDAGShellWhenSet", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Shell:     "/bin/bash",
			ShellArgs: []string{"-c"},
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		result := runtime.DAGShell(ctx)
		assert.Equal(t, []string{"/bin/bash", "-c"}, result)
	})

	t.Run("ExpandsEnvVarsInShell", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env:   []string{"TEST_SHELL=/bin/zsh"},
			Shell: "$TEST_SHELL",
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		result := runtime.DAGShell(ctx)
		assert.Equal(t, []string{"/bin/zsh"}, result)
	})

	t.Run("ExpandsEnvVarsInShellArgs", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env:       []string{"TEST_SHELL_ARG=-c"},
			Shell:     "/bin/bash",
			ShellArgs: []string{"$TEST_SHELL_ARG"},
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		result := runtime.DAGShell(ctx)
		assert.Equal(t, []string{"/bin/bash", "-c"}, result)
	})

	t.Run("UsesDAGEnvForExpansion", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env:   []string{"MY_SHELL=/usr/bin/fish"},
			Shell: "$MY_SHELL",
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		result := runtime.DAGShell(ctx)
		assert.Equal(t, []string{"/usr/bin/fish"}, result)
	})

	t.Run("ReturnsDefaultShellWhenDAGShellEmpty", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Shell: "", // Empty shell
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		result := runtime.DAGShell(ctx)
		assert.NotEmpty(t, result, "should return default shell when DAG shell is empty")
	})

	t.Run("ReturnsDefaultShellWhenNoDAG", func(t *testing.T) {
		t.Parallel()
		// Context without DAG - should return default shell
		ctx := context.Background()
		result := runtime.DAGShell(ctx)
		// May be empty or not depending on system
		_ = result
	})
}

// TestEnvShell tests the Env.Shell method
func TestEnvShell(t *testing.T) {
	t.Run("StepShellTakesPrecedence", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Shell:     "/bin/bash",
			ShellArgs: []string{"-c"},
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		step := core.Step{
			Name:      "test-step",
			Shell:     "/bin/zsh",
			ShellArgs: []string{"-e"},
		}
		env := runtime.NewEnv(ctx, step)
		result := env.Shell(ctx)
		assert.Equal(t, []string{"/bin/zsh", "-e"}, result)
	})

	t.Run("FallsBackToDAGShell", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Shell:     "/bin/bash",
			ShellArgs: []string{"-c"},
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		step := core.Step{
			Name: "test-step",
			// No step-level shell
		}
		env := runtime.NewEnv(ctx, step)
		result := env.Shell(ctx)
		assert.Equal(t, []string{"/bin/bash", "-c"}, result)
	})

	t.Run("ExpandsStepShellWithEnvVars", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env: []string{"MY_STEP_SHELL=/bin/fish"},
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		step := core.Step{
			Name:  "test-step",
			Shell: "$MY_STEP_SHELL",
		}
		env := runtime.NewEnv(ctx, step)
		result := env.Shell(ctx)
		assert.Equal(t, []string{"/bin/fish"}, result)
	})

	t.Run("ExpandsDAGShellWithEnvVars", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env:   []string{"MY_DAG_SHELL=/bin/ksh"},
			Shell: "$MY_DAG_SHELL",
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		step := core.Step{Name: "test-step"}
		env := runtime.NewEnv(ctx, step)
		result := env.Shell(ctx)
		assert.Equal(t, []string{"/bin/ksh"}, result)
	})

	t.Run("UsesDAGEnvVarsForExpansion", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env:   []string{"CUSTOM_SHELL=/bin/custom"},
			Shell: "$CUSTOM_SHELL",
		}
		ctx := runtime.NewContext(context.Background(), dag, "test-run", "test.log")
		step := core.Step{Name: "test-step"}
		env := runtime.NewEnv(ctx, step)
		result := env.Shell(ctx)
		assert.Equal(t, []string{"/bin/custom"}, result)
	})
}

func TestEnv_AllEnvsMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setupEnv func(env runtime.Env) runtime.Env
		expected map[string]string
	}{
		{
			name: "CombinesVariables",
			setupEnv: func(env runtime.Env) runtime.Env {
				env.Scope = env.Scope.WithEntries(map[string]string{
					"VAR1": "value1",
					"VAR2": "value2",
					"ENV1": "env1",
					"ENV2": "env2",
				}, eval.EnvSourceStepEnv)
				return env
			},
			expected: map[string]string{
				"VAR1":                    "value1",
				"VAR2":                    "value2",
				"ENV1":                    "env1",
				"ENV2":                    "env2",
				exec.EnvKeyDAGRunStepName: "test-step",
			},
		},
		{
			name: "EmptyScope",
			setupEnv: func(env runtime.Env) runtime.Env {
				return env
			},
			expected: map[string]string{
				exec.EnvKeyDAGRunStepName: "test-step",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a temporary directory to use as DAG working directory
			tempDir := t.TempDir()

			// Set up DAG context with WorkingDir and BaseEnv
			dag := &core.DAG{
				Name:       "test-dag",
				WorkingDir: tempDir,
			}
			ctx := exec.NewContext(context.Background(), dag, "", "")

			env := runtime.NewEnv(ctx, core.Step{Name: "test-step"})
			env = tt.setupEnv(env)

			// Use WithEnv to set the env in context, then call AllEnvsMap
			ctx = runtime.WithEnv(ctx, env)
			result := runtime.AllEnvsMap(ctx)

			// Check that all expected keys exist with correct values
			for key, expectedValue := range tt.expected {
				require.Equal(t, expectedValue, result[key], "key %s should have value %s", key, expectedValue)
			}
		})
	}
}

func TestNewEnvForStep_WorkingDirectory(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create subdirectory for relative path tests
	subDir := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0755))

	// Create testdir in home for tilde tests
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	homeTempDir := filepath.Join(homeDir, "dagu_test_workdir")
	require.NoError(t, os.MkdirAll(homeTempDir, 0755))
	t.Cleanup(func() { _ = os.RemoveAll(homeTempDir) })

	tests := []struct {
		name        string
		step        core.Step
		dagWorkDir  string // DAG's WorkingDir for context
		expectedDir string
	}{
		{
			name: "StepWithAbsoluteDirectory",
			step: core.Step{
				Name: "test-step",
				Dir:  tempDir,
			},
			dagWorkDir:  "/some/dag/workdir",
			expectedDir: tempDir,
		},
		{
			name: "StepWithRelativeDirectory_ResolvesAgainstDAGWorkDir",
			step: core.Step{
				Name: "test-step",
				Dir:  "./subdir",
			},
			dagWorkDir:  tempDir,
			expectedDir: subDir,
		},
		{
			name: "StepWithRelativeDirectory_NoLeadingDot",
			step: core.Step{
				Name: "test-step",
				Dir:  "subdir",
			},
			dagWorkDir:  tempDir,
			expectedDir: subDir,
		},
		{
			name: "StepWithHomeDirectoryNotation",
			step: core.Step{
				Name: "test-step",
				Dir:  "~/dagu_test_workdir",
			},
			dagWorkDir:  tempDir,
			expectedDir: homeTempDir,
		},
		{
			name: "StepWithNonExistentAbsoluteDirectory",
			step: core.Step{
				Name: "test-step",
				Dir: func() string {
					if goruntime.GOOS == "windows" {
						return "C:\\non\\existent\\directory"
					}
					return "/non/existent/directory"
				}(),
			},
			dagWorkDir: tempDir,
			expectedDir: func() string {
				if goruntime.GOOS == "windows" {
					return "C:\\non\\existent\\directory"
				}
				return "/non/existent/directory"
			}(),
		},
		{
			name: "StepWithEnvironmentVariableInPath_Absolute",
			step: core.Step{
				Name: "test-step",
				Dir: func() string {
					if goruntime.GOOS == "windows" {
						return "$USERPROFILE\\dagu_test_workdir"
					}
					return "$HOME/dagu_test_workdir"
				}(),
			},
			dagWorkDir:  tempDir,
			expectedDir: homeTempDir,
		},
		{
			name: "StepWithNoDir_InheritsDAGWorkDir",
			step: core.Step{
				Name: "test-step",
				Dir:  "",
			},
			dagWorkDir:  tempDir,
			expectedDir: tempDir,
		},
		{
			name: "StepWithParentRelativeDirectory",
			step: core.Step{
				Name: "test-step",
				Dir:  "../",
			},
			dagWorkDir:  subDir,
			expectedDir: tempDir,
		},
		{
			name: "DAGWorkDirWithTildePrefix",
			step: core.Step{
				Name: "test-step",
				Dir:  "", // Empty - should inherit DAG WorkingDir
			},
			dagWorkDir:  "~/dagu_test_workdir",
			expectedDir: homeTempDir,
		},
		{
			name: "DAGWorkDirWithEnvVarExpandingToHome",
			step: core.Step{
				Name: "test-step",
				Dir:  "", // Empty - should inherit DAG WorkingDir
			},
			dagWorkDir: func() string {
				if goruntime.GOOS == "windows" {
					return "$USERPROFILE\\dagu_test_workdir"
				}
				return "$HOME/dagu_test_workdir"
			}(),
			expectedDir: homeTempDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Set up DAG context with WorkingDir
			dag := &core.DAG{
				Name:       "test-dag",
				WorkingDir: tt.dagWorkDir,
			}
			dagCtx := exec.Context{
				DAG: dag,
			}
			ctx := runtime.WithDAGContext(context.Background(), dagCtx)

			env := runtime.NewEnv(ctx, tt.step)

			// Check that DAG_RUN_STEP_NAME is set via Scope
			val, ok := env.Scope.Get(exec.EnvKeyDAGRunStepName)
			assert.True(t, ok, "DAG_RUN_STEP_NAME should be set")
			assert.Equal(t, tt.step.Name, val)

			// Resolve symlinks for comparison (macOS /var vs /private/var)
			expectedResolved, _ := filepath.EvalSymlinks(tt.expectedDir)
			actualResolved, _ := filepath.EvalSymlinks(env.WorkingDir)
			assert.Equal(t, expectedResolved, actualResolved)

			// PWD should match WorkingDir via Scope
			pwd, _ := env.Scope.Get("PWD")
			assert.Equal(t, env.WorkingDir, pwd)
		})
	}
}

func TestNewEnvForStep_ImplicitWorkingDirUsesDAGRunWorkDir(t *testing.T) {
	t.Parallel()

	staleSerializedDir := t.TempDir()
	runWorkDir := t.TempDir()

	dag := &core.DAG{
		Name:       "test-dag",
		WorkingDir: staleSerializedDir,
	}
	ctx := exec.NewContext(context.Background(), dag, "run-id", "", exec.WithWorkDir(runWorkDir))

	env := runtime.NewEnv(ctx, core.Step{Name: "test-step"})
	assert.Equal(t, runWorkDir, env.WorkingDir)
	pwd, _ := env.Scope.Get("PWD")
	assert.Equal(t, runWorkDir, pwd)

	env = runtime.NewEnv(ctx, core.Step{Name: "relative-step", Dir: "child"})
	assert.Equal(t, filepath.Join(runWorkDir, "child"), env.WorkingDir)
}

func TestNewEnvForStep_ExplicitWorkingDirIgnoresDAGRunWorkDir(t *testing.T) {
	t.Parallel()

	explicitDir := t.TempDir()
	runWorkDir := t.TempDir()

	dag := &core.DAG{
		Name:               "test-dag",
		WorkingDir:         explicitDir,
		WorkingDirExplicit: true,
	}
	ctx := exec.NewContext(context.Background(), dag, "run-id", "", exec.WithWorkDir(runWorkDir))

	env := runtime.NewEnv(ctx, core.Step{Name: "test-step"})
	assert.Equal(t, explicitDir, env.WorkingDir)

	env = runtime.NewEnv(ctx, core.Step{Name: "relative-step", Dir: "child"})
	assert.Equal(t, filepath.Join(explicitDir, "child"), env.WorkingDir)
}

func TestNewEnvForStep_BasicFields(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Set up DAG context with WorkingDir
	dag := &core.DAG{
		Name:       "test-dag",
		WorkingDir: tempDir,
	}
	dagCtx := exec.Context{
		DAG: dag,
	}
	ctx := runtime.WithDAGContext(context.Background(), dagCtx)

	step := core.Step{
		Name: "test-step",
		Commands: []core.CommandEntry{{
			Command:     "echo",
			Args:        []string{"hello", "arg1", "arg2"},
			CmdWithArgs: "echo hello arg1 arg2",
		}},
	}

	env := runtime.NewEnv(ctx, step)

	// Check basic fields
	assert.Equal(t, step, env.Step)
	assert.NotNil(t, env.Scope)
	assert.NotNil(t, env.StepMap)

	// Check that DAG_RUN_STEP_NAME is set via Scope
	stepName, _ := env.Scope.Get(exec.EnvKeyDAGRunStepName)
	assert.Equal(t, "test-step", stepName)

	// Check that PWD is set to DAG's WorkingDir
	pwd, _ := env.Scope.Get("PWD")
	assert.Equal(t, tempDir, pwd)

	// Check that WorkingDir is set to DAG's WorkingDir
	assert.Equal(t, tempDir, env.WorkingDir)
}

func TestNewEnvForStep_WorkingDirectory_DAGEnvExpansion(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0755))

	// Set up DAG context with WorkingDir and env vars
	dag := &core.DAG{
		Name:       "test-dag",
		WorkingDir: tempDir,
		Env:        []string{"MY_SUBDIR=subdir"},
	}
	dagCtx := exec.Context{
		DAG: dag,
	}
	ctx := runtime.WithDAGContext(context.Background(), dagCtx)

	step := core.Step{
		Name: "test-step",
		Dir:  "./$MY_SUBDIR", // Uses DAG env var in relative path
	}

	env := runtime.NewEnv(ctx, step)

	// Resolve symlinks for comparison
	expectedResolved, _ := filepath.EvalSymlinks(subDir)
	actualResolved, _ := filepath.EvalSymlinks(env.WorkingDir)
	assert.Equal(t, expectedResolved, actualResolved)
}

func TestEnv_UserEnvsMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) (context.Context, runtime.Env)
		expected map[string]string
	}{
		{
			name: "IncludesOutputsFromPreviousSteps",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				dag := &core.DAG{Env: []string{"DAG_VAR=dag_value"}}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("OUTPUT_VAR", "output_value", eval.EnvSourceOutput)
				return ctx, env
			},
			expected: map[string]string{
				"DAG_VAR":    "dag_value",
				"OUTPUT_VAR": "output_value",
			},
		},
		{
			name: "StepEnvOverridesAll",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				dag := &core.DAG{Env: []string{"KEY=dag"}}
				secrets := []string{"KEY=secret"}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log",
					runtime.WithSecrets(secrets),
				)

				step := core.Step{Name: "test"}
				env := runtime.NewEnv(ctx, step)
				// Step env has highest precedence
				env.Scope = env.Scope.WithEntry("KEY", "step", eval.EnvSourceStepEnv)

				envCtx := runtime.WithEnv(ctx, env)
				return envCtx, env
			},
			expected: map[string]string{
				"KEY": "step",
			},
		},
		{
			name: "ExcludesOSEnvironment",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				dag := &core.DAG{Env: []string{"USER_VAR=user"}}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				return ctx, env
			},
			expected: map[string]string{
				"USER_VAR": "user",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			_, env := tt.setup(ctx)

			result := env.UserEnvsMap()

			for key, expectedValue := range tt.expected {
				assert.Equal(t, expectedValue, result[key], "key %s should have value %s", key, expectedValue)
			}
			// Ensure OS env is not included (PATH should not be in result)
			_, hasPath := result["PATH"]
			assert.False(t, hasPath, "UserEnvsMap should not include OS environment variables like PATH")
		})
	}
}

func TestEnv_EvalString_Precedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) (context.Context, runtime.Env)
		input    string
		expected string
	}{
		{
			name: "StepEnvOverridesOutputVariablesAndDAGEnv",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				// Create DAG with env variable
				dag := &core.DAG{
					Env: []string{"FOO=from_dag"},
				}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

				// Create executor env
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})

				// Set output variable
				env.Scope = env.Scope.WithEntry("FOO", "from_output", eval.EnvSourceOutput)

				// Set step env (highest precedence)
				env.Scope = env.Scope.WithEntry("FOO", "from_step", eval.EnvSourceStepEnv)

				return ctx, env
			},
			input:    "${FOO}",
			expected: "from_step",
		},
		{
			name: "OutputVariablesOverrideDAGEnv",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				// Create DAG with env variable
				dag := &core.DAG{
					Env: []string{"BAR=from_dag"},
				}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

				// Create executor env
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})

				// Set output variable (higher precedence than DAG)
				env.Scope = env.Scope.WithEntry("BAR", "from_output", eval.EnvSourceOutput)

				return ctx, env
			},
			input:    "${BAR}",
			expected: "from_output",
		},
		{
			name: "DAGEnvUsedWhenNoOverrideExists",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				// Create DAG with env variable
				dag := &core.DAG{
					Env: []string{"BAZ=from_dag"},
				}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

				// Create executor env
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})

				return ctx, env
			},
			input:    "${BAZ}",
			expected: "from_dag",
		},
		{
			name: "MultipleVariablesWithDifferentPrecedence",
			setup: func(ctx context.Context) (context.Context, runtime.Env) {
				// Create DAG with multiple env variables
				dag := &core.DAG{
					Env: []string{"VAR1=dag1", "VAR2=dag2", "VAR3=dag3"},
				}
				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

				// Create executor env
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})

				// Set output variables (VAR1, VAR2)
				env.Scope = env.Scope.WithEntries(map[string]string{
					"VAR1": "output1",
					"VAR2": "output2",
				}, eval.EnvSourceOutput)

				// Set step env (only for VAR1, highest precedence)
				env.Scope = env.Scope.WithEntry("VAR1", "step1", eval.EnvSourceStepEnv)

				return ctx, env
			},
			input:    "VAR1=${VAR1}, VAR2=${VAR2}, VAR3=${VAR3}",
			expected: "VAR1=step1, VAR2=output2, VAR3=dag3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			ctx, env := tt.setup(ctx)

			result, err := env.EvalString(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnv_SpecialEnvVars_DAGDocsDirAndParamsJSON(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Paths.DocsDir = "/docs"
	ctx := config.WithConfig(context.Background(), cfg)

	dag := &core.DAG{
		Name:       "test-dag",
		WorkingDir: t.TempDir(),
		ParamsJSON: `{"a":"b"}`,
	}
	ctx = runtime.NewContext(ctx, dag, "run-1", "test.log")

	env := runtime.NewEnv(ctx, core.Step{Name: "step1"})
	ctx = runtime.WithEnv(ctx, env)
	result := runtime.AllEnvsMap(ctx)

	assert.Equal(t, filepath.Join(cfg.Paths.DocsDir, dag.Name), result[exec.EnvKeyDAGDocsDir])
	assert.Equal(t, `{"a":"b"}`, result[exec.EnvKeyDAGParamsJSONCompat])
	assert.Equal(t, `{"a":"b"}`, result[exec.EnvKeyDAGParamsJSON])
}

func TestEnv_SpecialEnvVars_DAGRunWorkDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag := &core.DAG{Name: "test-dag", WorkingDir: t.TempDir()}
	workDir := filepath.Join(t.TempDir(), "work")
	ctx = runtime.NewContext(ctx, dag, "run-1", "test.log",
		runtime.WithWorkDir(workDir),
	)

	env := runtime.NewEnv(ctx, core.Step{Name: "step1"})
	ctx = runtime.WithEnv(ctx, env)
	result := runtime.AllEnvsMap(ctx)
	assert.Equal(t, workDir, result[exec.EnvKeyDAGRunWorkDir])
}
