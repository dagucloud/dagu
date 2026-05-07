// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"dario.cat/mergo"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/go-viper/mapstructure/v2"

	"github.com/goccy/go-yaml"
)

// Errors for loading DAGs
var (
	ErrNameOrPathRequired = errors.New("name or path is required")
	ErrInvalidJSONFile    = errors.New("invalid JSON file")
)

// LoadOptions contains options for loading a DAG.
type LoadOptions struct {
	name                   string   // Name of the DAG.
	baseConfig             string   // Path to the base core.DAG configuration file.
	baseConfigContent      []byte   // Raw base config YAML content (used when file is unavailable, e.g., distributed mode).
	workspaceBaseConfigDir string   // Directory containing workspace base configs (<workspace>/base.yaml).
	params                 string   // Parameters to override default parameters in the DAG.
	paramsList             []string // List of parameters to override default parameters in the DAG.
	flags                  BuildFlag
	dagsDir                string            // Directory containing the core.DAG files.
	defaultWorkingDir      string            // Default working directory for DAGs without explicit workingDir.
	buildEnv               map[string]string // Pre-populated env vars for build (used for retry with dotenv).
}

// LoadOption is a function type for setting LoadOptions.
type LoadOption func(*LoadOptions)

// WithBaseConfig sets the base core.DAG configuration file.
func WithBaseConfig(baseDAG string) LoadOption {
	return func(o *LoadOptions) {
		o.baseConfig = baseDAG
	}
}

// WithBaseConfigContent sets the raw base config YAML content directly.
// This is used in distributed mode where workers may not have local base config files.
// When set, this takes precedence over the base config file path.
func WithBaseConfigContent(content []byte) LoadOption {
	return func(o *LoadOptions) {
		o.baseConfigContent = content
	}
}

// WithWorkspaceBaseConfigDir sets the directory containing workspace base configs.
// Named workspace DAGs inherit <dir>/<workspace>/base.yaml after the global base config.
func WithWorkspaceBaseConfigDir(dir string) LoadOption {
	return func(o *LoadOptions) {
		o.workspaceBaseConfigDir = dir
	}
}

// WithParams sets the parameters for the DAG.
func WithParams(params any) LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagValidateRuntimeParams
		switch params := params.(type) {
		case string:
			o.params = params
		case []string:
			o.paramsList = params
		default:
			panic(fmt.Sprintf("invalid type %T for params", params))
		}
	}
}

// WithoutEval disables the evaluation of dynamic fields.
func WithoutEval() LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagNoEval
	}
}

// OnlyMetadata sets the flag to load only metadata.
func OnlyMetadata() LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagOnlyMetadata
	}
}

// WithName sets the name of the DAG.
func WithName(name string) LoadOption {
	return func(o *LoadOptions) {
		o.name = name
	}
}

// WithDAGsDir sets the directory containing the core.DAG files.
// This directory is used as the base path for resolving relative core.DAG file paths.
// When a core.DAG is loaded by name rather than absolute path, the system will look
// for the core.DAG file in this directory. If not specified, the current working
// directory is used as the default.
func WithDAGsDir(dagsDir string) LoadOption {
	return func(o *LoadOptions) {
		o.dagsDir = dagsDir
	}
}

// WithAllowBuildErrors allows build errors to be ignored during core.DAG loading.
// This is required for loading DAGs that may have errors in their definitions,
// such as missing steps or invalid configurations. When this option is set,
// the loader will return a core.DAG with the errors included in the DAG's `BuildErrors` field,
// and will not fail the loading process.
func WithAllowBuildErrors() LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagAllowBuildErrors
	}
}

// SkipSchemaValidation disables schema resolution/validation during build.
func SkipSchemaValidation() LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagSkipSchemaValidation
	}
}

// WithSkipBaseHandlers skips merging handlerOn from base config.
// This is used for sub-DAG runs to prevent handler inheritance from base config.
// Sub-DAGs should have their own handlers defined explicitly if needed.
func WithSkipBaseHandlers() LoadOption {
	return func(o *LoadOptions) {
		o.flags |= BuildFlagSkipBaseHandlers
	}
}

// WithDefaultWorkingDir sets the default working directory for DAGs without explicit workingDir.
// This is used for sub-DAG execution to inherit the parent's working directory.
func WithDefaultWorkingDir(defaultWorkingDir string) LoadOption {
	return func(o *LoadOptions) {
		dir := strings.TrimSpace(defaultWorkingDir)
		if dir == "" {
			return
		}
		o.defaultWorkingDir = filepath.Clean(dir)
	}
}

// WithBuildEnv provides additional environment variables for the build.
// These are added to the envScope before building, allowing YAML to
// reference them via ${VAR}. This is used for retry scenarios where
// dotenv values need to be available during rebuild from YamlData.
func WithBuildEnv(env map[string]string) LoadOption {
	return func(o *LoadOptions) {
		o.buildEnv = env
	}
}

// Load loads a Directed Acyclic Graph (core.DAG) from a file path or name with the given options.
//
// The function handles different input formats:
//
// 1. Absolute paths:
//   - YAML files (.yaml/.yml): Processed with dynamic evaluation, including base configs,
//     parameters, and environment variables
//
// 2. Relative paths or filenames:
//   - Resolved against the DAGsDir specified in options
//   - If DAGsDir is not provided, the current working directory is used
//   - For YAML files, the extension is optional
//
// This approach provides a flexible way to load core.DAG definitions from multiple sources
// while supporting customization through the LoadOptions.
func Load(ctx context.Context, nameOrPath string, opts ...LoadOption) (*core.DAG, error) {
	if nameOrPath == "" {
		return nil, ErrNameOrPathRequired
	}
	var options LoadOptions
	for _, opt := range opts {
		opt(&options)
	}
	buildContext := BuildContext{
		ctx: ctx,
		opts: BuildOpts{
			Base:                   options.baseConfig,
			BaseConfigContent:      options.baseConfigContent,
			WorkspaceBaseConfigDir: options.workspaceBaseConfigDir,
			Parameters:             options.params,
			ParametersList:         options.paramsList,
			Name:                   options.name,
			DAGsDir:                options.dagsDir,
			DefaultWorkingDir:      options.defaultWorkingDir,
			Flags:                  options.flags,
			BuildEnv:               options.buildEnv,
		},
	}
	return loadDAG(buildContext, nameOrPath)
}

// LoadYAML loads the core.DAG from the given YAML data with the specified options.
func LoadYAML(ctx context.Context, data []byte, opts ...LoadOption) (*core.DAG, error) {
	var options LoadOptions
	for _, opt := range opts {
		opt(&options)
	}
	return LoadYAMLWithOpts(ctx, data, BuildOpts{
		Base:                   options.baseConfig,
		BaseConfigContent:      options.baseConfigContent,
		WorkspaceBaseConfigDir: options.workspaceBaseConfigDir,
		Parameters:             options.params,
		ParametersList:         options.paramsList,
		Name:                   options.name,
		DAGsDir:                options.dagsDir,
		DefaultWorkingDir:      options.defaultWorkingDir,
		Flags:                  options.flags,
		BuildEnv:               options.buildEnv,
	})
}

// LoadYAMLWithOpts loads the core.DAG configuration from YAML data.
func LoadYAMLWithOpts(ctx context.Context, data []byte, opts BuildOpts) (*core.DAG, error) {
	baseDef, baseRaw, err := loadBaseDefinition(opts)
	if err != nil {
		return loadYAMLFailure(opts, err)
	}

	dags, err := loadDAGsFromData(BuildContext{ctx: ctx, opts: opts}, data, "", baseDef, baseRaw)
	if err != nil {
		return loadYAMLFailure(opts, err)
	}

	mainDAG, err := assembleLoadedDAGs(dags, fmt.Errorf("no DAGs found in YAML data"))
	if err != nil {
		return loadYAMLFailure(opts, err)
	}

	mainDAG.YamlData = data
	markConfiguredWorkingDirsExplicit(mainDAG)

	return mainDAG, nil
}

// loadYAMLFailure returns a placeholder DAG when YAML loading is allowed to fail.
func loadYAMLFailure(opts BuildOpts, err error) (*core.DAG, error) {
	if dag := buildLoadErrorDAG(opts, "", err); dag != nil {
		return dag, nil
	}
	return nil, core.ErrorList{err}
}

// buildLoadErrorDAG creates a placeholder DAG when build errors are allowed.
func buildLoadErrorDAG(opts BuildOpts, filePath string, err error) *core.DAG {
	if !opts.Has(BuildFlagAllowBuildErrors) {
		return nil
	}

	name := opts.Name
	if name == "" {
		name = defaultName(filePath)
	}

	return &core.DAG{
		Name:        name,
		Location:    filePath,
		SourceFile:  filePath,
		BuildErrors: []error{err},
	}
}

// LoadBaseConfig loads the global configuration from the given file.
// The global configuration can be overridden by the core.DAG configuration.
func LoadBaseConfig(ctx BuildContext, file string) (*core.DAG, error) {
	// The base config is optional.
	if !fileutil.FileExists(file) {
		return nil, nil
	}

	// Load the raw data from the file.
	raw, err := readYAMLFile(file)
	if err != nil {
		return nil, err
	}

	// Decode the raw data into a manifest.
	def, err := decode(raw)
	if err != nil {
		return nil, core.ErrorList{err}
	}

	ctx = ctx.WithOpts(BuildOpts{
		Flags: ctx.opts.Flags,
	}).WithFile(file)

	return def.build(ctx)
}

// loadDAG loads the core.DAG from the given file.
func loadDAG(ctx BuildContext, nameOrPath string) (*core.DAG, error) {
	filePath, err := resolveYamlFilePath(ctx, nameOrPath)
	if err != nil {
		return nil, err
	}

	ctx = ctx.WithFile(filePath)

	baseDef, baseRaw, err := loadBaseDefinition(ctx.opts)
	if err != nil {
		return loadDAGFailure(ctx, filePath, err)
	}

	dags, err := loadDAGsFromFile(ctx, filePath, baseDef, baseRaw)
	if err != nil {
		return loadDAGFailure(ctx, filePath, err)
	}

	mainDAG, err := assembleLoadedDAGs(dags, fmt.Errorf("no DAGs found in file %q", filePath))
	if err != nil {
		return loadDAGFailure(ctx, filePath, err)
	}

	core.InitializeDefaults(mainDAG)
	if err := applyWorkingDirFallback(mainDAG, filePath); err != nil {
		return nil, err
	}

	return mainDAG, nil
}

// loadDAGFailure returns a placeholder DAG when file loading is allowed to fail.
func loadDAGFailure(ctx BuildContext, filePath string, err error) (*core.DAG, error) {
	if dag := buildLoadErrorDAG(ctx.opts, filePath, err); dag != nil {
		return dag, nil
	}
	return nil, err
}

// assembleLoadedDAGs returns the first DAG and attaches later documents as locals.
func assembleLoadedDAGs(dags []*core.DAG, emptyErr error) (*core.DAG, error) {
	if len(dags) == 0 {
		return nil, emptyErr
	}

	mainDAG := dags[0]
	if err := attachLocalDAGs(mainDAG, dags[1:]); err != nil {
		return nil, err
	}

	return mainDAG, nil
}

// attachLocalDAGs registers secondary documents as named local DAGs.
func attachLocalDAGs(mainDAG *core.DAG, localDAGs []*core.DAG) error {
	if len(localDAGs) == 0 {
		return nil
	}

	mainDAG.LocalDAGs = make(map[string]*core.DAG, len(localDAGs))
	for i, dag := range localDAGs {
		index := i + 1
		if dag.Name == "" {
			return fmt.Errorf("child core.DAG at index %d must have a name", index)
		}
		mainDAG.LocalDAGs[dag.Name] = dag
	}
	return nil
}

// applyWorkingDirFallback marks configured working directories as explicit,
// then synthesizes a fallback from filePath or the process working directory
// when the manifest omits one, returning an error if the process working
// directory cannot be determined.
func applyWorkingDirFallback(dag *core.DAG, filePath string) error {
	markConfiguredWorkingDirsExplicit(dag)

	if dag.WorkingDir != "" {
		return nil
	}

	if filePath != "" {
		dag.WorkingDir = filepath.Dir(filePath)
		return nil
	}

	wd, err := getDefaultWorkingDir()
	if err != nil {
		return fmt.Errorf("failed to determine working directory: %w", err)
	}
	dag.WorkingDir = wd
	return nil
}

// markConfiguredWorkingDirsExplicit preserves configured working directories
// without synthesizing fallback values for YAML-only loads.
func markConfiguredWorkingDirsExplicit(dag *core.DAG) {
	if dag == nil {
		return
	}
	if dag.WorkingDir != "" {
		dag.WorkingDirExplicit = true
	}
	for _, localDAG := range dag.LocalDAGs {
		markConfiguredWorkingDirsExplicit(localDAG)
	}
}

// loadDAGsFromFile loads all DAGs from a multi-document YAML file.
func loadDAGsFromFile(ctx BuildContext, filePath string, baseDef *dag, baseRaw []byte) ([]*core.DAG, error) {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", filePath, err)
	}
	return loadDAGsFromData(ctx, data, filePath, baseDef, baseRaw)
}

type dagDocument struct {
	index int
	data  map[string]any
}

// loadDAGsFromData builds DAGs from every non-empty YAML document in the input.
func loadDAGsFromData(ctx BuildContext, data []byte, filePath string, baseDef *dag, baseRaw []byte) ([]*core.DAG, error) {
	docs, err := decodeDocuments(data)
	if err != nil {
		return nil, err
	}

	fileBaseDef, fileBaseRaw := baseDef, baseRaw
	if len(docs) > 0 {
		fileBaseDef, fileBaseRaw, err = loadEffectiveBaseDefinition(ctx.opts, docs[0].data, baseDef, baseRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to process document %d: %w", docs[0].index, err)
		}
	}

	dags := make([]*core.DAG, 0, len(docs))
	for _, doc := range docs {
		docBaseDef, docBaseRaw := fileBaseDef, fileBaseRaw
		if doc.index == 0 || workspaceNameFromDocument(doc.data) != "" {
			docBaseDef, docBaseRaw, err = loadEffectiveBaseDefinition(ctx.opts, doc.data, baseDef, baseRaw)
			if err != nil {
				return nil, fmt.Errorf("failed to process document %d: %w", doc.index, err)
			}
		}

		dag, err := processDAGDocument(buildDocumentContext(ctx, doc.index), doc.data, docBaseDef, docBaseRaw, filePath, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process document %d: %w", doc.index, err)
		}
		dags = append(dags, dag)
	}

	if err := validateUniqueNames(dags); err != nil {
		return nil, err
	}
	return dags, nil
}

// decodeDocuments splits a YAML stream into non-empty manifest documents.
func decodeDocuments(data []byte) ([]dagDocument, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	docs := make([]dagDocument, 0, 1)

	for index := 0; ; index++ {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return nil, fmt.Errorf("failed to decode document %d: %w", index, err)
		}
		if len(doc) == 0 {
			continue
		}
		docs = append(docs, dagDocument{index: len(docs), data: doc})
	}
}

// loadBaseDefinition loads and decodes the optional base manifest.
func loadBaseDefinition(opts BuildOpts) (*dag, []byte, error) {
	if opts.Has(BuildFlagOnlyMetadata) {
		return nil, nil, nil
	}

	baseRaw, description, err := readBaseDefinitionData(opts)
	if err != nil || len(baseRaw) == 0 {
		return nil, nil, err
	}

	baseDef, err := decodeDefinitionData(baseRaw, description)
	if err != nil {
		return nil, nil, err
	}
	return baseDef, baseRaw, nil
}

// readBaseDefinitionData returns the raw bytes and label for the base manifest.
func readBaseDefinitionData(opts BuildOpts) ([]byte, string, error) {
	if len(opts.BaseConfigContent) > 0 {
		return opts.BaseConfigContent, "embedded base config", nil
	}
	if opts.Base == "" {
		return nil, "", nil
	}

	baseRaw, err := os.ReadFile(opts.Base) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed to read base config: %w", err)
	}
	return baseRaw, "base config", nil
}

// decodeDefinitionData parses manifest data into the internal dag definition.
func decodeDefinitionData(data []byte, description string) (*dag, error) {
	raw, err := unmarshalData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", description, err)
	}
	def, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", description, err)
	}
	return def, nil
}

// processDAGDocument processes a single DAG document from the YAML file.
func processDAGDocument(
	ctx BuildContext,
	doc map[string]any,
	baseDef *dag,
	baseRaw []byte,
	filePath string,
	fullData []byte,
) (*core.DAG, error) {
	spec, err := decode(doc)
	if err != nil {
		return nil, err
	}

	docCtx, dest, err := prepareDocumentContext(ctx, baseDef, spec)
	if err != nil {
		return nil, err
	}

	if shouldInheritType(doc, baseDef, spec) {
		spec.Type = baseDef.Type
	}

	dag, err := spec.build(docCtx)
	if err != nil {
		return nil, err
	}
	if err := merge(dest, dag); err != nil {
		return nil, err
	}
	if len(baseRaw) > 0 {
		dest.BaseConfigData = baseRaw
	}
	applyHistoryRetentionOverride(dest, spec.HistRetentionDays != nil, spec.HistRetentionRuns != nil)

	dest.Location = filePath
	dest.SourceFile = filePath
	dest.YamlData, err = documentYAML(ctx.index, doc, fullData)
	if err != nil {
		return nil, err
	}
	return dest, nil
}

// loadEffectiveBaseDefinition returns the base definition that applies to a document.
// Embedded base configs are already effective for distributed workers, so local
// workspace config files are only considered when loading from filesystem state.
func loadEffectiveBaseDefinition(opts BuildOpts, doc map[string]any, baseDef *dag, baseRaw []byte) (*dag, []byte, error) {
	workspaceRaw, err := readWorkspaceBaseDefinitionData(opts, doc)
	if err != nil {
		return nil, nil, err
	}
	if len(workspaceRaw) == 0 {
		return baseDef, baseRaw, nil
	}
	return mergeBaseDefinitionData(baseRaw, workspaceRaw)
}

// readWorkspaceBaseDefinitionData returns raw per-workspace base config data for a named workspace DAG.
func readWorkspaceBaseDefinitionData(opts BuildOpts, doc map[string]any) ([]byte, error) {
	if opts.Has(BuildFlagOnlyMetadata) || opts.WorkspaceBaseConfigDir == "" || len(opts.BaseConfigContent) > 0 {
		return nil, nil
	}

	workspaceName := workspaceNameFromDocument(doc)
	if workspaceName == "" {
		return nil, nil
	}

	data, err := os.ReadFile(filepath.Join(opts.WorkspaceBaseConfigDir, workspaceName, workspace.BaseConfigFileName)) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read workspace base config %q: %w", workspaceName, err)
	}
	return data, nil
}

func workspaceNameFromDocument(doc map[string]any) string {
	for _, key := range []string{"labels", "tags"} {
		labels, ok := labelsValueFromRaw(doc[key])
		if !ok {
			continue
		}

		var workspaceName string
		for _, entry := range labels.Entries() {
			labelKey := strings.ToLower(strings.TrimSpace(entry.Key()))
			if labelKey != "workspace" {
				continue
			}

			value := strings.TrimSpace(entry.Value())
			if err := workspace.ValidateName(value); err != nil {
				return ""
			}
			if workspaceName != "" && !strings.EqualFold(workspaceName, value) {
				return ""
			}
			workspaceName = value
		}
		if workspaceName != "" {
			return workspaceName
		}
	}
	return ""
}

func labelsValueFromRaw(raw any) (types.LabelsValue, bool) {
	if raw == nil {
		return types.LabelsValue{}, false
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return types.LabelsValue{}, false
	}

	var labels types.LabelsValue
	if err := yaml.Unmarshal(data, &labels); err != nil {
		return types.LabelsValue{}, false
	}
	if labels.IsZero() {
		return types.LabelsValue{}, false
	}
	return labels, true
}

func mergeBaseDefinitionData(baseRaw, overrideRaw []byte) (*dag, []byte, error) {
	if len(baseRaw) == 0 {
		def, err := decodeDefinitionData(overrideRaw, "workspace base config")
		return def, overrideRaw, err
	}
	if len(overrideRaw) == 0 {
		def, err := decodeDefinitionData(baseRaw, "base config")
		return def, baseRaw, err
	}

	baseMap, err := unmarshalData(baseRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal base config: %w", err)
	}
	overrideMap, err := unmarshalData(overrideRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal workspace base config: %w", err)
	}

	mergedMap, err := mergeDefinitionMaps(baseMap, overrideMap)
	if err != nil {
		return nil, nil, err
	}
	def, err := decode(mergedMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode merged base config: %w", err)
	}

	mergedRaw, err := yaml.Marshal(mergedMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal merged base config: %w", err)
	}
	return def, mergedRaw, nil
}

func mergeDefinitionMaps(base, override map[string]any) (map[string]any, error) {
	merged := cloneMap(base)
	if merged == nil {
		merged = make(map[string]any, len(override))
	}
	for key, overrideValue := range override {
		baseValue, ok := merged[key]
		if key == "env" {
			mergedEnv, err := mergeBaseEnvRaw(baseValue, overrideValue)
			if err != nil {
				return nil, err
			}
			merged[key] = mergedEnv
			continue
		}

		baseMap, baseIsMap := baseValue.(map[string]any)
		overrideMap, overrideIsMap := overrideValue.(map[string]any)
		if ok && baseIsMap && overrideIsMap {
			mergedNested, err := mergeDefinitionMaps(baseMap, overrideMap)
			if err != nil {
				return nil, err
			}
			merged[key] = mergedNested
			continue
		}
		merged[key] = cloneAny(overrideValue)
	}
	return merged, nil
}

func mergeBaseEnvRaw(base, override any) (any, error) {
	switch {
	case base == nil:
		return cloneAny(override), nil
	case override == nil:
		return cloneAny(base), nil
	}

	baseEnv, err := decodeViaYAML[types.EnvValue](base)
	if err != nil {
		return nil, fmt.Errorf("invalid base config env: %w", err)
	}
	overrideEnv, err := decodeViaYAML[types.EnvValue](override)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace base config env: %w", err)
	}

	combined := overrideEnv.Prepend(baseEnv)
	return envValueToRaw(combined), nil
}

// buildDocumentContext applies per-document overrides for multi-DAG files.
func buildDocumentContext(ctx BuildContext, index int) BuildContext {
	ctx.index = index
	if index == 0 {
		return ctx
	}

	opts := ctx.opts
	opts.Parameters = ""
	opts.ParametersList = nil
	opts.Flags &^= BuildFlagValidateRuntimeParams
	return ctx.WithOpts(opts)
}

// prepareDocumentContext builds the inherited context and destination DAG.
func prepareDocumentContext(ctx BuildContext, baseDef, spec *dag) (BuildContext, *core.DAG, error) {
	customStepTypes, err := buildCustomStepActionRegistry(
		stepTypesOf(baseDef),
		stepTypesOf(spec),
		actionsOf(baseDef),
		actionsOf(spec),
	)
	if err != nil {
		return ctx, nil, err
	}
	ctx = ctx.WithCustomStepTypes(customStepTypes)

	if baseDef == nil {
		return ctx, new(core.DAG), nil
	}

	baseDAG, baseDefaults, err := buildDocumentBase(ctx, baseDef)
	if err != nil {
		return ctx, nil, err
	}
	ctx.baseDAG = baseDAG
	ctx.baseDefaults = baseDefaults
	return ctx, baseDAG, nil
}

// buildDocumentBase builds the reusable base DAG and decoded defaults.
func buildDocumentBase(ctx BuildContext, baseDef *dag) (*core.DAG, *defaults, error) {
	baseDAG, err := buildBaseDAG(ctx, baseDef)
	if err != nil {
		return nil, nil, err
	}

	baseDefaults, err := decodeDefaults(baseDef.Defaults)
	if err != nil {
		return nil, nil, err
	}

	return baseDAG, baseDefaults, nil
}

// documentYAML returns the YAML payload stored for a specific document.
func documentYAML(index int, doc map[string]any, fullData []byte) ([]byte, error) {
	if index == 0 {
		return fullData, nil
	}
	return yaml.Marshal(doc)
}

// buildBaseDAG builds a new base DAG from the base definition.
func buildBaseDAG(ctx BuildContext, baseDef *dag) (*core.DAG, error) {
	buildOpts := ctx.opts
	buildOpts.Parameters = ""
	buildOpts.ParametersList = nil

	customStepTypes, err := buildCustomStepActionRegistry(stepTypesOf(baseDef), nil, actionsOf(baseDef), nil)
	if err != nil {
		return nil, err
	}

	baseDAG, err := baseDef.build(ctx.WithOpts(buildOpts).WithCustomStepTypes(customStepTypes))
	if err != nil {
		return nil, fmt.Errorf("failed to build base core.DAG: %w", err)
	}

	// Skip handlers from base config for sub-DAG runs to prevent inheritance.
	// Sub-DAGs should define their own handlers explicitly if needed.
	if ctx.opts.Has(BuildFlagSkipBaseHandlers) {
		baseDAG.HandlerOn = core.HandlerOn{}
	}

	return baseDAG, nil
}

// stepTypesOf returns the custom step type declarations for a manifest.
func stepTypesOf(d *dag) map[string]customStepTypeSpec {
	if d == nil {
		return nil
	}
	return d.StepTypes
}

// actionsOf returns the custom action declarations for a manifest.
func actionsOf(d *dag) map[string]customStepTypeSpec {
	if d == nil {
		return nil
	}
	return d.Actions
}

// shouldInheritType reports whether a document should reuse the base DAG type.
func shouldInheritType(doc map[string]any, baseDef, spec *dag) bool {
	if baseDef == nil || spec == nil {
		return false
	}
	if _, exists := doc["type"]; exists {
		return false
	}
	return strings.TrimSpace(baseDef.Type) != ""
}

// validateUniqueNames ensures all DAGs in a multi-DAG file have unique names.
func validateUniqueNames(dags []*core.DAG) error {
	if len(dags) < 2 {
		return nil
	}

	names := make(map[string]struct{}, len(dags))
	if dags[0].Name != "" {
		names[dags[0].Name] = struct{}{}
	}
	for i, dag := range dags[1:] {
		index := i + 1
		if dag.Name == "" {
			return fmt.Errorf("DAG at index %d must have a name in multi-DAG file", index)
		}
		if _, exists := names[dag.Name]; exists {
			return fmt.Errorf("duplicate DAG name %q found", dag.Name)
		}
		names[dag.Name] = struct{}{}
	}
	return nil
}

// defaultName returns the default name for the given file.
// The default name is the filename without the extension.
func defaultName(file string) string {
	if file == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
}

// resolveYamlFilePath resolves the YAML file path.
// If the file name does not have an extension, it appends ".yaml".
func resolveYamlFilePath(ctx BuildContext, file string) (string, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return "", errors.New("file path is required")
	}

	file = expandHomeDir(file)

	if filepath.IsAbs(file) {
		return file, nil
	}

	if absFile, err := filepath.Abs(file); err == nil && fileutil.FileExists(absFile) {
		return absFile, nil
	}

	if ctx.opts.DAGsDir != "" {
		file = filepath.Join(ctx.opts.DAGsDir, file)
	}

	if !strings.HasSuffix(file, ".yaml") && !strings.HasSuffix(file, ".yml") {
		file += ".yaml"
	}

	return filepath.Abs(file)
}

// expandHomeDir expands a leading tilde when the caller used a home-relative path.
func expandHomeDir(file string) string {
	if file != "~" && !strings.HasPrefix(file, "~/") && !strings.HasPrefix(file, `~\`) {
		return file
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return file
	}
	return strings.Replace(file, "~", homeDir, 1)
}

type mergeTransformer struct{}

var _ mergo.Transformers = (*mergeTransformer)(nil)

// Transformer customizes merge behavior for fields that need non-default semantics.
func (*mergeTransformer) Transformer(
	typ reflect.Type,
) func(dst, src reflect.Value) error {
	// mergo does not override a value with zero value for a pointer.
	if typ == reflect.TypeFor[core.MailOn]() {
		// We need to explicitly override the value for a pointer with a zero
		// value.
		return func(dst, src reflect.Value) error {
			if dst.CanSet() {
				dst.Set(src)
			}

			return nil
		}
	}

	if typ == reflect.TypeFor[core.DAGRetryPolicy]() {
		// DAG retry policies are configured as a single root object. Replace the
		// inherited policy wholesale so limit: 0 can intentionally disable retries.
		return func(dst, src reflect.Value) error {
			if dst.CanSet() {
				dst.Set(src)
			}

			return nil
		}
	}

	if typ == reflect.TypeFor[core.WebhookConfig]() {
		// Webhook forwarding config is a single DAG-level object. Replace the
		// inherited object wholesale so child DAGs can override or clear the
		// header allowlist deterministically.
		return func(dst, src reflect.Value) error {
			if dst.CanSet() {
				dst.Set(src)
			}

			return nil
		}
	}

	if typ == reflect.TypeFor[core.KubernetesConfig]() {
		return func(dst, src reflect.Value) error {
			if !dst.CanSet() || !src.IsValid() || src.IsNil() {
				return nil
			}

			srcCfg := src.Interface().(core.KubernetesConfig)
			if len(srcCfg) == 0 {
				dst.Set(reflect.ValueOf(core.KubernetesConfig{}))
				return nil
			}

			var dstCfg map[string]any
			if !dst.IsNil() {
				dstCfg = map[string]any(dst.Interface().(core.KubernetesConfig))
			}

			merged := mergeKubernetesConfigMaps(dstCfg, map[string]any(srcCfg))
			dst.Set(reflect.ValueOf(core.KubernetesConfig(merged)))
			return nil
		}
	}

	if typ == reflect.TypeFor[core.HarnessDefinitions]() {
		return func(dst, src reflect.Value) error {
			if !dst.CanSet() || !src.IsValid() || src.IsNil() {
				return nil
			}

			srcDefs := src.Interface().(core.HarnessDefinitions)
			if len(srcDefs) == 0 {
				return nil
			}

			cloneDef := func(def *core.HarnessDefinition) *core.HarnessDefinition {
				if def == nil {
					return nil
				}
				return &core.HarnessDefinition{
					Binary:         def.Binary,
					PrefixArgs:     append([]string(nil), def.PrefixArgs...),
					PromptMode:     def.PromptMode,
					PromptFlag:     def.PromptFlag,
					PromptPosition: def.PromptPosition,
					FlagStyle:      def.FlagStyle,
					OptionFlags:    maps.Clone(def.OptionFlags),
				}
			}

			cloneDefs := func(defs core.HarnessDefinitions) core.HarnessDefinitions {
				if defs == nil {
					return nil
				}
				cloned := make(core.HarnessDefinitions, len(defs))
				for name, def := range defs {
					cloned[name] = cloneDef(def)
				}
				return cloned
			}

			var merged core.HarnessDefinitions
			if !dst.IsNil() {
				merged = cloneDefs(dst.Interface().(core.HarnessDefinitions))
			} else {
				merged = make(core.HarnessDefinitions)
			}

			for name, def := range srcDefs {
				if def == nil {
					delete(merged, name)
					continue
				}
				merged[name] = cloneDef(def)
			}

			if len(merged) == 0 {
				dst.Set(reflect.Zero(typ))
				return nil
			}

			dst.Set(reflect.ValueOf(merged))
			return nil
		}
	}

	// Handle []string fields (like Env) by appending instead of replacing
	if typ == reflect.TypeFor[[]string]() {
		return func(dst, src reflect.Value) error {
			if !dst.CanSet() || src.Len() == 0 {
				return nil
			}

			// Append src values to dst
			result := reflect.AppendSlice(dst, src)
			dst.Set(result)

			return nil
		}
	}

	return nil
}

// readYAMLFile reads the contents of the file into a map.
func readYAMLFile(file string) (cfg map[string]any, err error) {
	data, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %v", file, err)
	}

	return unmarshalData(data)
}

// unmarshalData unmarshals the data into a map.
func unmarshalData(data []byte) (map[string]any, error) {
	return newManifestDecoder().Unmarshal(data)
}

// decode decodes the configuration map into a manifest.
func decode(cm map[string]any) (*dag, error) {
	return newManifestDecoder().Decode(cm)
}

// extractRawHandlerOn copies raw handler definitions for later processing.
func extractRawHandlerOn(cm map[string]any) map[string]map[string]any {
	rawHandlers, ok := cm["handler_on"].(map[string]any)
	if !ok || len(rawHandlers) == 0 {
		return nil
	}

	cloned := make(map[string]map[string]any, len(rawHandlers))
	for key, value := range rawHandlers {
		rawStep, ok := value.(map[string]any)
		if !ok {
			continue
		}
		cloned[key] = cloneMap(rawStep)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

// extractRawDefaults copies raw defaults from the decoded manifest map.
func extractRawDefaults(cm map[string]any) map[string]any {
	rawDefaults, ok := cm["defaults"].(map[string]any)
	if !ok || len(rawDefaults) == 0 {
		return nil
	}
	return cloneMap(rawDefaults)
}

// TypedUnionDecodeHook returns a decode hook that handles our typed union types.
// It converts raw map[string]any values to the appropriate typed values.
func TypedUnionDecodeHook() mapstructure.DecodeHookFunc {
	return func(_ reflect.Type, to reflect.Type, data any) (any, error) {
		// Handle types.ShellValue
		if to == reflect.TypeFor[types.ShellValue]() {
			return decodeViaYAML[types.ShellValue](data)
		}
		// Handle types.StringOrArray
		if to == reflect.TypeFor[types.StringOrArray]() {
			return decodeViaYAML[types.StringOrArray](data)
		}
		// Handle types.ScheduleValue
		if to == reflect.TypeFor[types.ScheduleValue]() {
			return decodeViaYAML[types.ScheduleValue](data)
		}
		// Handle types.EnvValue
		if to == reflect.TypeFor[types.EnvValue]() {
			return decodeViaYAML[types.EnvValue](data)
		}
		// Handle types.ContinueOnValue
		if to == reflect.TypeFor[types.ContinueOnValue]() {
			return decodeViaYAML[types.ContinueOnValue](data)
		}
		// Handle types.PortValue
		if to == reflect.TypeFor[types.PortValue]() {
			return decodeViaYAML[types.PortValue](data)
		}
		// Handle types.LogOutputValue
		if to == reflect.TypeFor[types.LogOutputValue]() {
			return decodeViaYAML[types.LogOutputValue](data)
		}
		// Handle types.ModelValue
		if to == reflect.TypeFor[types.ModelValue]() {
			return decodeViaYAML[types.ModelValue](data)
		}
		// Handle types.LabelsValue
		if to == reflect.TypeFor[types.LabelsValue]() {
			return decodeViaYAML[types.LabelsValue](data)
		}
		// Handle types.RepeatMode
		if to == reflect.TypeFor[types.RepeatMode]() {
			return decodeViaYAML[types.RepeatMode](data)
		}
		// Handle types.IntOrDynamic
		if to == reflect.TypeFor[types.IntOrDynamic]() {
			return decodeViaYAML[types.IntOrDynamic](data)
		}
		// Handle types.BackoffValue
		if to == reflect.TypeFor[types.BackoffValue]() {
			return decodeViaYAML[types.BackoffValue](data)
		}
		return data, nil
	}
}

// decodeViaYAML converts data to YAML and unmarshals it to the target type.
// This allows the custom UnmarshalYAML methods to be used.
func decodeViaYAML[T any](data any) (T, error) {
	var result T
	if data == nil {
		return result, nil
	}
	// Convert the data to YAML bytes
	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return result, fmt.Errorf("failed to marshal to YAML: %w", err)
	}
	// Unmarshal using the custom UnmarshalYAML method
	if err := yaml.Unmarshal(yamlBytes, &result); err != nil {
		return result, fmt.Errorf("failed to unmarshal from YAML: %w", err)
	}
	return result, nil
}

// merge merges the source core.DAG into the destination DAG.
func merge(dst, src *core.DAG) error {
	return mergo.Merge(dst, src, mergo.WithOverride,
		mergo.WithTransformers(&mergeTransformer{}))
}
