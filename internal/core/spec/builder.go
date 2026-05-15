// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
)

// BuildContext is the context for building a DAG.
type BuildContext struct {
	ctx   context.Context
	file  string
	opts  BuildOpts
	index int

	customStepTypes *customStepTypeRegistry
	// baseDAG contains the built base-config DAG for the current document.
	// It is used while building child handlers and steps so DAG-level defaults
	// inherited from base config are visible during executor inference.
	baseDAG *core.DAG
	// baseDefaults contains decoded step defaults inherited from base config.
	// They are merged with DAG-local defaults before building steps and handlers.
	baseDefaults *defaults

	// buildEnv is a temporary map used during core.DAG building to pass env vars to params
	// This is not serialized and is cleared after build completes
	buildEnv map[string]string

	// envScope is a shared state pointer for thread-safe environment variable handling.
	// It holds accumulated env vars (OS + DAG env) and is used by transformers
	// to expand variables without mutating global os.Env.
	// This is initialized by build() and populated by buildEnvs transformer.
	envScope *envScopeState

	// paramsState caches DAG-level parameter parsing/resolution during a single build.
	// This avoids reparsing params for Params, DefaultParams, ParamsJSON, and ParamDefs.
	paramsState *paramsState
}

// envScopeState holds mutable state that needs to be shared across transformers.
// Using a pointer allows value-passed BuildContext to share state.
type envScopeState struct {
	scope    *eval.EnvScope
	buildEnv map[string]string // Also store as map for WithVariables
}

type paramsState struct {
	cached bool
	result *paramsResult
	err    error
}

// StepBuildContext is the context for building a step.
type StepBuildContext struct {
	BuildContext
	dag *core.DAG
}

func (c BuildContext) WithOpts(opts BuildOpts) BuildContext {
	copy := c
	copy.opts = opts
	copy.paramsState = nil
	return copy
}

func (c BuildContext) WithFile(file string) BuildContext {
	copy := c
	copy.file = file
	return copy
}

func (c BuildContext) WithCustomStepTypes(registry *customStepTypeRegistry) BuildContext {
	copy := c
	copy.customStepTypes = registry
	return copy
}

// BuildFlag represents a bitmask option that influences DAG building behaviour.
type BuildFlag uint32

const (
	BuildFlagNone BuildFlag = 0

	BuildFlagNoEval BuildFlag = 1 << iota
	BuildFlagOnlyMetadata
	BuildFlagAllowBuildErrors
	BuildFlagSkipSchemaValidation
	BuildFlagSkipBaseHandlers // Skip merging handlerOn from base config (for sub-DAG runs)
	BuildFlagValidateRuntimeParams
)

// BuildOpts is used to control the behavior of the builder.
type BuildOpts struct {
	// Base specifies the Base configuration file for the DAG.
	Base string
	// BaseConfigContent is the raw base config YAML content.
	// When set, this takes precedence over Base file path.
	BaseConfigContent []byte
	// WorkspaceBaseConfigDir contains per-workspace base configs at <workspace>/base.yaml.
	WorkspaceBaseConfigDir string
	// Parameters specifies the Parameters to the DAG.
	// Parameters are used to override the default Parameters in the DAG.
	Parameters string
	// ParametersList specifies the parameters to the DAG.
	ParametersList []string
	// Name of the core.DAG if it's not defined in the spec
	Name string
	// DAGsDir is the directory containing the core.DAG files.
	DAGsDir string
	// DefaultWorkingDir is the default working directory for DAGs without explicit workingDir.
	// This is used for sub-DAG execution to inherit the parent's working directory.
	DefaultWorkingDir string
	// Flags stores all boolean options controlling build behaviour.
	Flags BuildFlag
	// BuildEnv provides pre-populated environment variables for the build.
	// These are added to envScope before building, allowing YAML to reference
	// them via ${VAR}. Used for retry/restart where dotenv values need to be
	// available during rebuild from YamlData.
	BuildEnv map[string]string
}

// Has reports whether the flag is enabled on the current BuildOpts.
func (o BuildOpts) Has(flag BuildFlag) bool {
	return o.Flags&flag != 0
}

// parsePrecondition parses the precondition field.
func parsePrecondition(ctx BuildContext, precondition any) ([]*core.Condition, error) {
	switch v := precondition.(type) {
	case nil:
		return nil, nil

	case string:
		return []*core.Condition{{Condition: v}}, nil

	case map[string]any:
		var ret core.Condition
		for key, vv := range v {
			switch strings.ToLower(key) {
			case "condition":
				val, ok := vv.(string)
				if !ok {
					return nil, core.NewValidationError("preconditions", vv, ErrPreconditionValueMustBeString)
				}
				ret.Condition = val

			case "expected":
				val, ok := vv.(string)
				if !ok {
					return nil, core.NewValidationError("preconditions", vv, ErrPreconditionValueMustBeString)
				}
				ret.Expected = val

			case "command":
				val, ok := vv.(string)
				if !ok {
					return nil, core.NewValidationError("preconditions", vv, ErrPreconditionValueMustBeString)
				}
				ret.Condition = val

			case "negate":
				val, ok := vv.(bool)
				if !ok {
					return nil, core.NewValidationError("preconditions", vv, ErrPreconditionNegateMustBeBool)
				}
				ret.Negate = val

			default:
				return nil, core.NewValidationError("preconditions", key, fmt.Errorf("%w: %s", ErrPreconditionHasInvalidKey, key))

			}
		}

		if err := ret.Validate(); err != nil {
			return nil, core.NewValidationError("preconditions", v, err)
		}

		return []*core.Condition{&ret}, nil

	case []any:
		var ret []*core.Condition
		for _, vv := range v {
			parsed, err := parsePrecondition(ctx, vv)
			if err != nil {
				return nil, err
			}
			ret = append(ret, parsed...)
		}
		return ret, nil

	default:
		return nil, core.NewValidationError("preconditions", v, ErrPreconditionMustBeArrayOrString)

	}
}

var (
	secretEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretRefPathPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)
)

// Keep in sync with internal/core/exec runtime env keys. This package cannot
// import core/exec because core/exec imports spec for DAG loading.
var reservedSecretEnvNames = []string{
	"DAG_NAME",
	"DAG_RUN_ID",
	"DAG_RUN_LOG_FILE",
	"DAG_RUN_STEP_NAME",
	"DAG_RUN_STEP_STDOUT_FILE",
	"DAG_RUN_STEP_STDERR_FILE",
	"DAG_RUN_STATUS",
	"DAGU_PARAMS_JSON",
	"DAG_DOCS_DIR",
	"DAG_PARAMS_JSON",
	"DAG_RUN_WORK_DIR",
	"DAG_RUN_ARTIFACTS_DIR",
	"DAG_PUSHBACK",
	"DAG_PUSHBACK_ITERATION",
	"DAG_PUSHBACK_PREVIOUS_STDOUT_FILE",
	"DAGU_EXTERNAL_STEP_RETRY",
	"DAGU_QUEUE_DISPATCH_RETRY",
}

// parseSecretRefs parses secret references from the YAML definition.
func parseSecretRefs(ctx BuildContext, d *dag) ([]core.SecretRef, error) {
	secretRefs := d.Secrets

	// Convert secretRef to core.SecretRef and validate
	secrets := make([]core.SecretRef, 0, len(secretRefs))
	names := make(map[string]bool)
	conflicts, err := secretNameConflicts(ctx, d)
	if err != nil {
		return nil, err
	}

	for i, def := range secretRefs {
		// Validate required fields
		if def.Name == "" {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret at index %d: 'name' field is required", i))
		}
		if !secretEnvNamePattern.MatchString(def.Name) {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q must be a valid environment variable name", def.Name))
		}
		if strings.HasPrefix(def.Name, "DAGU_") {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q must not start with DAGU_", def.Name))
		}
		if source, ok := conflicts[def.Name]; ok {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q collides with %s", def.Name, source))
		}

		// Check for duplicate names
		if names[def.Name] {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("duplicate secret name %q", def.Name))
		}
		names[def.Name] = true

		hasRef := strings.TrimSpace(def.Ref) != ""
		hasProvider := strings.TrimSpace(def.Provider) != ""
		hasKey := strings.TrimSpace(def.Key) != ""
		hasDirectRef := hasProvider && hasKey
		if hasRef == hasDirectRef || (!hasRef && hasProvider != hasKey) {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q: exactly one of 'ref' or 'provider' plus 'key' is required", def.Name))
		}
		if hasRef && len(def.Options) > 0 {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q: 'options' cannot be used with registry ref", def.Name))
		}
		if hasRef && !secretRefPathPattern.MatchString(def.Ref) {
			return nil, core.NewValidationError("secrets", def, fmt.Errorf("secret %q: registry ref must be a slash-separated lowercase slug path", def.Name))
		}

		secrets = append(secrets, core.SecretRef{
			Name:     def.Name,
			Ref:      def.Ref,
			Provider: def.Provider,
			Key:      def.Key,
			Options:  def.Options,
		})
	}

	return secrets, nil
}

func secretNameConflicts(ctx BuildContext, d *dag) (map[string]string, error) {
	conflicts := make(map[string]string)

	envVars, err := loadVariablesFromEnvValue(ctx, d.Env)
	if err != nil {
		return nil, err
	}
	for name := range envVars {
		conflicts[name] = "DAG env"
	}

	params, err := parseParamsInternal(ctx, d)
	if err != nil {
		return nil, err
	}
	for _, param := range params.Params {
		name, _, ok := strings.Cut(param, "=")
		if ok && name != "" {
			conflicts[name] = "DAG parameter"
		}
	}

	for _, name := range reservedSecretEnvNames {
		conflicts[name] = "Dagu-managed runtime environment variable"
	}

	return conflicts, nil
}

// generateTypedStepName generates a type-based name for a step after it's been built
func generateTypedStepName(existingNames map[string]struct{}, step *core.Step, index int) string {
	var prefix string

	// Determine prefix based on the built step's properties
	if customType, _ := step.ExecutorConfig.Metadata["custom_type"].(string); customType != "" {
		prefix = customType
	} else if step.ExecutorConfig.Type != "" {
		prefix = step.ExecutorConfig.Type
	} else if step.Container != nil {
		prefix = "docker"
	} else if step.Parallel != nil {
		prefix = "parallel"
	} else if step.SubDAG != nil {
		prefix = "dag"
	} else if step.Script != "" {
		prefix = "script"
	} else if len(step.Commands) > 0 {
		prefix = "cmd"
	} else {
		prefix = "step"
	}

	// Generate unique name with the prefix
	counter := index + 1
	name := fmt.Sprintf("%s_%d", prefix, counter)

	for {
		if _, exists := existingNames[name]; !exists {
			existingNames[name] = struct{}{}
			return name
		}
		counter++
		name = fmt.Sprintf("%s_%d", prefix, counter)
	}
}

// normalizedStepData converts string to map[string]any for subsequent process
func normalizeStepData(ctx BuildContext, data []any) []any {
	// Convert string steps to map format for shorthand syntax support
	normalized := make([]any, len(data))
	for i, item := range data {
		switch step := item.(type) {
		case string:
			// Shorthand: convert string to map with command field
			normalized[i] = map[string]any{"command": step}
		default:
			// Keep as-is (already a map or other structure)
			normalized[i] = item
		}
	}
	return normalized
}

func decodeStep(raw map[string]any) (*step, error) {
	if err := validateStepConfigAliasRaw(raw); err != nil {
		return nil, err
	}

	var st step
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		ErrorUnused: true,
		Result:      &st,
		TagName:     "yaml",
		DecodeHook:  TypedUnionDecodeHook(),
	})
	if err := md.Decode(raw); err != nil {
		return nil, core.NewValidationError("steps", raw, withSnakeCaseKeyHint(err))
	}
	return &st, nil
}

func finalizeBuiltStepName(names map[string]struct{}, builtStep *core.Step, idx int) {
	if builtStep.Name == "" {
		if builtStep.ID != "" {
			builtStep.Name = builtStep.ID
		} else {
			builtStep.Name = generateTypedStepName(names, builtStep, idx)
		}
	}
	// Register every resolved name (explicit, promoted, or generated) so that
	// subsequent auto-generated names skip it. generateTypedStepName already
	// registers internally, but map[string]struct{} insertion is idempotent.
	names[builtStep.Name] = struct{}{}
}

func buildConcreteStep(ctx StepBuildContext, s *step) (*core.Step, error) {
	return s.build(ctx)
}

// buildStepFromRaw build core.Step from give raw data (map[string]any)
func buildStepFromRaw(ctx StepBuildContext, idx int, raw map[string]any, names map[string]struct{}, defs *defaults) (*core.Step, error) {
	normalizedRaw, err := normalizeStepExecutionRaw(raw, ctx.customStepTypes)
	if err != nil {
		return nil, err
	}
	st, err := decodeStep(normalizedRaw)
	if err != nil {
		return nil, err
	}
	builtStep, err := buildStepFromSpec(ctx, idx, st, normalizedRaw, names, defs, "")
	if err != nil {
		return nil, err
	}
	return builtStep, nil
}

func buildStepFromSpec(
	ctx StepBuildContext,
	idx int,
	st *step,
	raw map[string]any,
	names map[string]struct{},
	defs *defaults,
	forcedName string,
) (*core.Step, error) {
	if raw != nil {
		_, hasRun := raw["run"]
		_, hasAction := raw["action"]
		if hasRun || hasAction {
			normalizedRaw, err := normalizeStepExecutionRaw(raw, ctx.customStepTypes)
			if err != nil {
				return nil, err
			}
			normalizedSpec, err := decodeStep(normalizedRaw)
			if err != nil {
				return nil, err
			}
			st = normalizedSpec
			raw = normalizedRaw
		}
	}

	stCopy := *st
	if forcedName != "" {
		stCopy.Name = forcedName
	}

	var builtStep *core.Step
	var err error
	if registry := ctx.customStepTypes; registry != nil {
		if customType, ok := registry.Lookup(stCopy.Type); ok {
			builtStep, err = buildCustomStepFromSpec(ctx, &stCopy, raw, defs, customType, forcedName != "")
			if err != nil {
				return nil, err
			}
		}
	}
	if builtStep == nil {
		applyDefaults(&stCopy, defs, raw)
		builtStep, err = buildConcreteStep(ctx, &stCopy)
		if err != nil {
			return nil, err
		}
	}
	finalizeBuiltStepName(names, builtStep, idx)
	return builtStep, nil
}

// injectChainDependencies adds implicit dependencies for chain type execution.
// In chain execution, each step depends on the immediately previous step(s).
func injectChainDependencies(dag *core.DAG, prevSteps []*core.Step, step *core.Step) {
	// Early returns for cases where we shouldn't inject dependencies
	if dag.Type != core.TypeChain || len(prevSteps) == 0 {
		return
	}

	// Build a set of existing dependencies for efficient lookup
	existingDeps := make(map[string]struct{}, len(step.Depends))
	for _, dep := range step.Depends {
		existingDeps[dep] = struct{}{}
	}

	// Add each previous step as a dependency if not already present
	for _, prevStep := range prevSteps {
		var depKey = prevStep.ID
		if depKey == "" {
			depKey = prevStep.Name
		}

		// Skip if this dependency already exists
		if _, exists := existingDeps[depKey]; exists {
			continue
		}

		// Also check alternate key (ID vs Name) to avoid duplicates
		altKey := getStepAlternateKey(prevStep, depKey)
		if altKey != "" {
			if _, exists := existingDeps[altKey]; exists {
				continue
			}
		}

		step.Depends = append(step.Depends, depKey)
		existingDeps[depKey] = struct{}{}
	}
}

// getStepAlternateKey returns the alternate identifier for a step, or empty string if none
func getStepAlternateKey(step *core.Step, primaryKey string) string {
	if step.ID != "" && primaryKey == step.ID {
		return step.Name
	}
	if step.ID != "" && primaryKey == step.Name {
		return step.ID
	}
	return ""
}
