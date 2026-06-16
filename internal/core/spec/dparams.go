// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"maps"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
)

type dagParamKind uint8

const (
	dagParamKindLegacy dagParamKind = iota
	dagParamKindExternalSchema
	dagParamKindInlineSchema
)

type dagParamPlan struct {
	kind             dagParamKind
	entries          []dagParamEntry
	paramDefs        []core.ParamDef
	schema           *jsonschema.Resolved
	schemaOrder      []string
	schemaProperties map[string]*jsonschema.Schema
	schemaClosed     bool
}

type dagParamEntry struct {
	Name     string
	Value    string
	HasValue bool
	Eval     string
}

func buildDAGParamsResult(ctx BuildContext, d *dag) (*paramsResult, error) {
	plan, err := buildDAGParamPlan(ctx, d)
	if err != nil {
		return nil, err
	}

	resolveRuntimeParams := shouldResolveRuntimeParams(ctx)
	defaultEntries := cloneParamEntries(plan.entries)
	defaultPairs := runtimePairsFromEntries(defaultEntries)
	finalPairs := defaultPairs

	switch plan.kind {
	case dagParamKindLegacy:
		if plan.schema == nil {
			finalPairs = defaultPairs
			if resolveRuntimeParams {
				finalPairs, err = resolveLegacyRuntimePairs(plan.entries, ctx.opts.Parameters, ctx.opts.ParametersList)
				if err != nil {
					return nil, err
				}
			}
			break
		}

		if resolveRuntimeParams {
			finalEntries, err := resolveLegacyEntries(ctx, plan, ctx.opts.Parameters, ctx.opts.ParametersList, false)
			if err != nil {
				return nil, err
			}
			finalPairs = runtimePairsFromEntries(finalEntries)

			// Eval-backed inline params may depend on mutable state or perform command
			// execution, so reuse the execution-time resolution instead of running
			// the same expressions again just to populate DefaultParams.
			if paramPlanHasEval(plan) && !ctx.opts.Has(BuildFlagNoEval) {
				defaultPairs = finalPairs
				break
			}
		}

		defaultEntries, err = resolveLegacyEntries(ctx, plan, "", nil, true)
		if err != nil {
			return nil, err
		}
		defaultPairs = runtimePairsFromEntries(defaultEntries)
		if !resolveRuntimeParams {
			finalPairs = defaultPairs
		}

	case dagParamKindExternalSchema:
		if resolveRuntimeParams {
			finalEntries, err := resolveExternalSchemaEntries(plan, ctx.opts.Parameters, ctx.opts.ParametersList)
			if err != nil {
				return nil, err
			}
			finalPairs = runtimePairsFromEntries(finalEntries)
		}

	case dagParamKindInlineSchema:
		if resolveRuntimeParams {
			finalEntries, err := resolveInlineSchemaEntries(plan, ctx.opts.Parameters, ctx.opts.ParametersList)
			if err != nil {
				return nil, err
			}
			finalPairs = runtimePairsFromEntries(finalEntries)
		}
	}

	defaultParts := make([]string, 0, len(defaultPairs))
	for _, pair := range defaultPairs {
		defaultParts = append(defaultParts, pair.Escaped())
	}
	defaultParams := strings.Join(defaultParts, " ")

	params := make([]string, 0, len(finalPairs))
	for _, pair := range finalPairs {
		params = append(params, pair.String())
	}

	rawOverride := ctx.opts.Parameters
	if rawOverride == "" && len(ctx.opts.ParametersList) == 1 {
		rawOverride = ctx.opts.ParametersList[0]
	}
	paramsJSON, err := buildResolvedParamsJSON(finalPairs, rawOverride)
	if err != nil {
		return nil, err
	}

	paramSchema, err := buildRenderableParamSchema(plan.schema)
	if err != nil {
		return nil, err
	}

	return &paramsResult{
		Params:        params,
		DefaultParams: defaultParams,
		ParamDefs:     cloneParamDefs(plan.paramDefs),
		ParamSchema:   paramSchema,
		ParamsJSON:    paramsJSON,
	}, nil
}

func shouldResolveRuntimeParams(ctx BuildContext) bool {
	return ctx.opts.Has(BuildFlagValidateRuntimeParams) || ctx.opts.Parameters != "" || len(ctx.opts.ParametersList) > 0
}

func paramPlanHasEval(plan *dagParamPlan) bool {
	for _, entry := range plan.entries {
		if strings.TrimSpace(entry.Eval) != "" {
			return true
		}
	}
	return false
}

func buildDAGParamPlan(ctx BuildContext, d *dag) (*dagParamPlan, error) {
	if _, ok := extractParamsSchemaDeclaration(d.Params); ok {
		if ctx.opts.Has(BuildFlagSkipSchemaValidation) {
			return buildLegacyParamPlan(extractSchemaValues(d.Params))
		}
		return buildExternalSchemaParamPlan(d.Params, d.WorkingDir, ctx.file)
	}
	if err := malformedInlineJSONSchemaShapeError(d.Params); err != nil {
		return nil, err
	}
	if isInlineJSONSchema(d.Params) {
		return buildInlineSchemaParamPlan(d.Params, ctx.opts.Has(BuildFlagSkipSchemaValidation))
	}
	return buildLegacyParamPlan(d.Params)
}

func buildLegacyParamPlan(input any) (*dagParamPlan, error) {
	noEvalCtx := BuildContext{opts: BuildOpts{Flags: BuildFlagNoEval}}
	plan := &dagParamPlan{kind: dagParamKindLegacy}
	seenNames := map[string]struct{}{}

	switch v := input.(type) {
	case nil:
		return plan, nil

	case string:
		pairs, err := parseStringParams(noEvalCtx, v)
		if err != nil {
			return nil, core.NewValidationError("params", v, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		if err := appendLegacyPairs(plan, pairs, seenNames); err != nil {
			return nil, err
		}
		return plan, nil

	case []string:
		pairs, err := parseListParams(noEvalCtx, v)
		if err != nil {
			return nil, core.NewValidationError("params", v, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		if err := appendLegacyPairs(plan, pairs, seenNames); err != nil {
			return nil, err
		}
		return plan, nil

	case map[string]any:
		pairs, err := parseMapParams(noEvalCtx, []any{v})
		if err != nil {
			return nil, core.NewValidationError("params", v, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
		}
		if err := appendLegacyPairs(plan, pairs, seenNames); err != nil {
			return nil, err
		}
		return plan, nil

	case []any:
		var hasInlineDefinitions bool
		for _, item := range v {
			switch value := item.(type) {
			case string:
				pairs, err := parseStringParams(noEvalCtx, value)
				if err != nil {
					return nil, core.NewValidationError("params", item, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
				}
				if err := appendLegacyPairs(plan, pairs, seenNames); err != nil {
					return nil, err
				}

			case map[string]any:
				inlineDef, err := detectInlineParamDefinition(value)
				if err != nil {
					return nil, core.NewValidationError("params", item, err)
				}
				if inlineDef != nil {
					hasInlineDefinitions = true
					paramDef, entry, err := parseInlineParamDefinition(inlineDef.name, inlineDef.fields)
					if err != nil {
						return nil, core.NewValidationError("params", item, err)
					}
					if err := rememberParamName(seenNames, paramDef.Name); err != nil {
						return nil, err
					}
					plan.paramDefs = append(plan.paramDefs, paramDef)
					plan.entries = append(plan.entries, entry)
					continue
				}

				pairs, err := parseMapParams(noEvalCtx, []any{value})
				if err != nil {
					return nil, core.NewValidationError("params", item, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
				}
				if err := appendLegacyPairs(plan, pairs, seenNames); err != nil {
					return nil, err
				}

			default:
				return nil, core.NewValidationError("params", item, fmt.Errorf("%w: %T", ErrInvalidParamValue, item))
			}
		}

		if hasInlineDefinitions {
			compiledSchema, err := compileInlineParamSchema(plan.paramDefs)
			if err != nil {
				return nil, err
			}
			plan.schema = compiledSchema.resolved
			plan.schemaProperties = compiledSchema.properties
			plan.schemaOrder = compiledSchema.order
			plan.entries, err = validateSchemaBackedEntries(plan.entries, plan.schema, plan.schemaProperties, plan.schemaOrder, true, false)
			if err != nil {
				return nil, err
			}
		}

		return plan, nil

	default:
		return nil, core.NewValidationError("params", v, fmt.Errorf("%w: %T", ErrInvalidParamValue, v))
	}
}

func buildExternalSchemaParamPlan(input any, workingDir, dagLocation string) (*dagParamPlan, error) {
	resolvedSchema, err := resolveSchemaFromParams(input, workingDir, dagLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON schema: %w", err)
	}
	if resolvedSchema == nil {
		return &dagParamPlan{kind: dagParamKindExternalSchema}, nil
	}

	values := extractSchemaValues(input)
	noEvalCtx := BuildContext{opts: BuildOpts{Flags: BuildFlagNoEval}}
	basePairs, err := parseParamValue(noEvalCtx, values)
	if err != nil {
		return nil, core.NewValidationError("params", values, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
	}

	root := resolvedSchema.Schema()
	schemaOrder := topLevelSchemaOrder(root)
	schemaProperties := map[string]*jsonschema.Schema{}
	if root != nil {
		maps.Copy(schemaProperties, root.Properties)
	}

	typedDefaults, err := schemaPairsToMap(basePairs, schemaProperties, true)
	if err != nil {
		return nil, err
	}
	typedDefaults, err = validateSchemaMap(typedDefaults, resolvedSchema, true)
	if err != nil {
		return nil, err
	}

	plan := &dagParamPlan{
		kind:             dagParamKindExternalSchema,
		schema:           resolvedSchema,
		schemaOrder:      schemaOrder,
		schemaProperties: schemaProperties,
		entries:          entriesFromTypedMap(typedDefaults, schemaOrder),
	}

	paramDefs, ok := deriveExternalSchemaParamDefs(root, typedDefaults)
	if ok {
		plan.paramDefs = paramDefs
	}

	return plan, nil
}

func appendLegacyPairs(plan *dagParamPlan, pairs []paramPair, seenNames map[string]struct{}) error {
	for _, pair := range pairs {
		if err := rememberParamName(seenNames, pair.Name); err != nil {
			return err
		}
		plan.entries = append(plan.entries, dagParamEntry{
			Name:     pair.Name,
			Value:    pair.Value,
			HasValue: true,
		})
		plan.paramDefs = append(plan.paramDefs, core.ParamDef{
			Name:    pair.Name,
			Type:    core.ParamDefTypeString,
			Default: pair.Value,
		})
	}
	return nil
}

func rememberParamName(seenNames map[string]struct{}, name string) error {
	if name == "" {
		return nil
	}
	if _, exists := seenNames[name]; exists {
		return core.NewValidationError(
			"params",
			name,
			fmt.Errorf("%w: duplicate parameter name %q", ErrInvalidParamValue, name),
		)
	}
	seenNames[name] = struct{}{}
	return nil
}

func validateSchemaBackedEntries(entries []dagParamEntry, schema *jsonschema.Resolved, schemaProperties map[string]*jsonschema.Schema, schemaOrder []string, metadataMode bool, allowSchemaFallbackJSON bool) ([]dagParamEntry, error) {
	namedPairs := make([]paramPair, 0, len(entries))
	for _, entry := range entries {
		if !entry.HasValue || entry.Name == "" {
			continue
		}
		namedPairs = append(namedPairs, paramPair{Name: entry.Name, Value: entry.Value})
	}

	typedMap, err := schemaPairsToMap(namedPairs, schemaProperties, allowSchemaFallbackJSON)
	if err != nil {
		return nil, err
	}

	typedMap, err = validateSchemaMap(typedMap, schema, metadataMode)
	if err != nil {
		return nil, err
	}

	return mergeTypedMapIntoEntries(entries, typedMap, schemaOrder), nil
}
