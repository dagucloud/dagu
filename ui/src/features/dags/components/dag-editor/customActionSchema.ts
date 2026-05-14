// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { dereferenceSchema, type JSONSchema } from '@/lib/schema-utils';
import { parse as parseYaml } from 'yaml';
import type { components } from '../../../../api/v1/schema';

export interface EditorLegacyDefinitionHint {
  name: string;
  targetType: string;
  description?: string;
  inputSchema: JSONSchema;
  outputSchema?: JSONSchema;
}

export interface EditorCustomActionHint {
  name: string;
  description?: string;
  inputSchema: JSONSchema;
  outputSchema?: JSONSchema;
}

export interface ExtractCustomDefinitionHintsResult {
  ok: boolean;
  legacyDefinitions: EditorLegacyDefinitionHint[];
  actions: EditorCustomActionHint[];
}

const legacyDefinitionNamePattern = /^[A-Za-z][A-Za-z0-9_-]*$/;
const customActionNamePattern =
  /^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z][A-Za-z0-9_-]*)*$/;
const localLegacyDefinitionSchemaDefinitionsKey =
  'legacyDefinitionInputSchemas';
const localCustomActionSchemaDefinitionsKey = 'customActionInputSchemas';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function cloneJson<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function escapeJsonPointerSegment(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function rewriteInternalRefs(value: unknown, basePointer: string): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => rewriteInternalRefs(item, basePointer));
  }
  if (!isRecord(value)) {
    return value;
  }

  const rewritten: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) {
    if (key === '$ref' && typeof item === 'string') {
      if (item === '#') {
        rewritten[key] = `#${basePointer}`;
      } else if (item.startsWith('#/')) {
        rewritten[key] = `#${basePointer}${item.slice(1)}`;
      } else {
        rewritten[key] = item;
      }
      continue;
    }
    rewritten[key] = rewriteInternalRefs(item, basePointer);
  }

  return rewritten;
}

function appendUniqueAllOf(
  existing: JSONSchema[] | undefined,
  additions: JSONSchema[]
): JSONSchema[] {
  const result = [...(existing ?? [])];
  const seen = new Set(result.map((item) => JSON.stringify(item)));

  for (const addition of additions) {
    const key = JSON.stringify(addition);
    if (seen.has(key)) {
      continue;
    }
    result.push(cloneJson(addition));
    seen.add(key);
  }

  return result;
}

function legacyDefinitionHintKey(hint: EditorLegacyDefinitionHint): string {
  return JSON.stringify({
    description: hint.description ?? '',
    inputSchema: hint.inputSchema,
    name: hint.name,
    outputSchema: hint.outputSchema ?? {},
    targetType: hint.targetType,
  });
}

function customActionHintKey(hint: EditorCustomActionHint): string {
  return JSON.stringify({
    description: hint.description ?? '',
    inputSchema: hint.inputSchema,
    name: hint.name,
    outputSchema: hint.outputSchema ?? {},
  });
}

function isEnumBranchForNames(schema: JSONSchema, names: string[]): boolean {
  if (!Array.isArray(schema.enum)) {
    return false;
  }

  if (schema.enum.length !== names.length) {
    return false;
  }

  return names.every((name, index) => schema.enum?.[index] === name);
}

function buildLegacyDefinitionEnumBranch(
  legacyTypeNames: string[],
  legacyTypeDescriptions: string[]
): JSONSchema {
  return {
    type: 'string',
    enum: legacyTypeNames,
    enumDescriptions: legacyTypeDescriptions,
    description:
      'Deprecated legacy execution definition declared in step_types or inherited from base config.',
  };
}

function buildCustomActionEnumBranch(
  customActionNames: string[],
  customActionDescriptions: string[]
): JSONSchema {
  return {
    type: 'string',
    enum: customActionNames,
    enumDescriptions: customActionDescriptions,
    description:
      'Custom action declared in actions or inherited from base config.',
  };
}

function augmentExecutorTypeSchema(
  schema: JSONSchema,
  legacyTypeNames: string[],
  legacyTypeDescriptions: string[]
) {
  if (!Array.isArray(schema.anyOf)) {
    return;
  }

  const legacyTypeBranch = buildLegacyDefinitionEnumBranch(
    legacyTypeNames,
    legacyTypeDescriptions
  );
  const anyOfWithoutLegacyBranch = schema.anyOf.filter(
    (entry) =>
      !isRecord(entry) ||
      !isEnumBranchForNames(entry as JSONSchema, legacyTypeNames)
  );

  schema.anyOf = [
    ...(anyOfWithoutLegacyBranch.slice(0, 1) as JSONSchema[]),
    legacyTypeBranch,
    ...(anyOfWithoutLegacyBranch.slice(1) as JSONSchema[]),
  ];
}

function augmentActionNameSchema(
  schema: JSONSchema,
  customActionNames: string[],
  customActionDescriptions: string[]
) {
  if (!Array.isArray(schema.anyOf)) {
    return;
  }

  const customActionBranch = buildCustomActionEnumBranch(
    customActionNames,
    customActionDescriptions
  );
  const anyOfWithoutCustomBranch = schema.anyOf.filter(
    (entry) =>
      !isRecord(entry) ||
      !isEnumBranchForNames(entry as JSONSchema, customActionNames)
  );

  schema.anyOf = [
    ...(anyOfWithoutCustomBranch.slice(0, 1) as JSONSchema[]),
    customActionBranch,
    ...(anyOfWithoutCustomBranch.slice(1) as JSONSchema[]),
  ];
}

function isStepLikeSchema(schema: JSONSchema): boolean {
  const properties = schema.properties;
  return (
    schema.type === 'object' &&
    !!properties &&
    (isRecord(properties.type) || isRecord(properties.action)) &&
    (isRecord(properties.with) || isRecord(properties.config))
  );
}

function hasStepSpecificProperties(schema: JSONSchema): boolean {
  const properties = schema.properties;
  if (!properties) {
    return false;
  }

  return (
    'name' in properties ||
    'command' in properties ||
    'run' in properties ||
    'script' in properties ||
    'depends' in properties ||
    'working_dir' in properties ||
    'parallel' in properties ||
    'call' in properties
  );
}

function isStepSchemaCandidate(schema: JSONSchema): boolean {
  if (!isStepLikeSchema(schema)) {
    return false;
  }
  if (!hasStepSpecificProperties(schema)) {
    return false;
  }

  const typeSchema = schema.properties?.type;
  const actionSchema = schema.properties?.action;
  if (!isRecord(typeSchema) && !isRecord(actionSchema)) {
    return false;
  }

  const hasCustomizableTypeSchema =
    isRecord(typeSchema) &&
    (typeSchema.$ref === '#/definitions/executorType' ||
      Array.isArray(typeSchema.anyOf) ||
      Array.isArray(typeSchema.oneOf));
  const hasCustomizableActionSchema =
    isRecord(actionSchema) &&
    (actionSchema.$ref === '#/definitions/actionName' ||
      Array.isArray(actionSchema.anyOf) ||
      Array.isArray(actionSchema.oneOf));

  return hasCustomizableTypeSchema || hasCustomizableActionSchema;
}

function parseLocalLegacyDefinitionHints(
  document: Record<string, unknown>
): EditorLegacyDefinitionHint[] {
  const legacyDefinitionsValue = document.step_types;
  if (!isRecord(legacyDefinitionsValue)) {
    return [];
  }

  const legacyDefinitions: EditorLegacyDefinitionHint[] = [];
  for (const [rawName, rawDef] of Object.entries(legacyDefinitionsValue)) {
    if (!isRecord(rawDef)) {
      continue;
    }

    const name = rawName.trim();
    const targetType =
      typeof rawDef.type === 'string' ? rawDef.type.trim() : '';
    const description =
      typeof rawDef.description === 'string'
        ? rawDef.description.trim() || undefined
        : undefined;

    if (!legacyDefinitionNamePattern.test(name) || !targetType) {
      continue;
    }
    if (!isRecord(rawDef.input_schema)) {
      continue;
    }

    legacyDefinitions.push({
      name,
      targetType,
      description,
      inputSchema: cloneJson(rawDef.input_schema as JSONSchema),
      outputSchema: isRecord(rawDef.output_schema)
        ? cloneJson(rawDef.output_schema as JSONSchema)
        : undefined,
    });
  }

  return legacyDefinitions;
}

function parseLocalCustomActionHints(
  document: Record<string, unknown>
): EditorCustomActionHint[] {
  const actionsValue = document.actions;
  if (!isRecord(actionsValue)) {
    return [];
  }

  const actions: EditorCustomActionHint[] = [];
  for (const [rawName, rawDef] of Object.entries(actionsValue)) {
    if (!isRecord(rawDef)) {
      continue;
    }

    const name = rawName.trim();
    const description =
      typeof rawDef.description === 'string'
        ? rawDef.description.trim() || undefined
        : undefined;

    if (!customActionNamePattern.test(name)) {
      continue;
    }
    if (!isRecord(rawDef.input_schema)) {
      continue;
    }

    actions.push({
      name,
      description,
      inputSchema: cloneJson(rawDef.input_schema as JSONSchema),
      outputSchema: isRecord(rawDef.output_schema)
        ? cloneJson(rawDef.output_schema as JSONSchema)
        : undefined,
    });
  }

  return actions;
}

function sortCustomHintsByName<T extends { name: string }>(hints: T[]): T[] {
  return Array.from(hints).sort((left, right) =>
    left.name.localeCompare(right.name)
  );
}

function augmentStepSchema(
  stepSchema: JSONSchema,
  legacyDefinitionRules: JSONSchema[],
  legacyTypeNames: string[],
  legacyTypeDescriptions: string[],
  customActionRules: JSONSchema[],
  customActionNames: string[],
  customActionDescriptions: string[]
) {
  stepSchema.allOf = appendUniqueAllOf(stepSchema.allOf, [
    ...legacyDefinitionRules,
    ...customActionRules,
  ]);
  suppressConditionalPropertySuggestions(stepSchema);

  const typeSchema = stepSchema.properties?.type;
  if (legacyTypeNames.length > 0 && isRecord(typeSchema)) {
    const clonedTypeSchema = cloneJson(typeSchema as JSONSchema);
    stepSchema.properties = {
      ...stepSchema.properties,
      type: clonedTypeSchema,
    };
    augmentExecutorTypeSchema(
      clonedTypeSchema,
      legacyTypeNames,
      legacyTypeDescriptions
    );
  }

  const actionSchema = stepSchema.properties?.action;
  if (customActionNames.length > 0 && isRecord(actionSchema)) {
    const clonedActionSchema = cloneJson(actionSchema as JSONSchema);
    stepSchema.properties = {
      ...stepSchema.properties,
      action: clonedActionSchema,
    };
    augmentActionNameSchema(
      clonedActionSchema,
      customActionNames,
      customActionDescriptions
    );
  }
}

function markPropertiesAsDoNotSuggest(schema: JSONSchema | undefined) {
  if (!isRecord(schema?.properties)) {
    return;
  }

  for (const [propertyName, propertySchema] of Object.entries(
    schema.properties
  )) {
    if (!isRecord(propertySchema)) {
      continue;
    }

    schema.properties[propertyName] = {
      ...(propertySchema as JSONSchema),
      doNotSuggest: true,
    };
  }
}

function suppressConditionalPropertySuggestions(stepSchema: JSONSchema) {
  if (!Array.isArray(stepSchema.allOf)) {
    return;
  }

  for (const rule of stepSchema.allOf) {
    if (!isRecord(rule)) {
      continue;
    }

    markPropertiesAsDoNotSuggest(rule.if as JSONSchema | undefined);
    markPropertiesAsDoNotSuggest(rule.then as JSONSchema | undefined);
  }
}

function visitSchemas(
  node: unknown,
  visitor: (schema: JSONSchema, path: string[]) => void,
  path: string[] = []
) {
  if (Array.isArray(node)) {
    for (const [index, item] of node.entries()) {
      visitSchemas(item, visitor, [...path, String(index)]);
    }
    return;
  }

  if (!isRecord(node)) {
    return;
  }

  visitor(node as JSONSchema, path);

  for (const [key, value] of Object.entries(node)) {
    visitSchemas(value, visitor, [...path, key]);
  }
}

function collectStepSchemaPaths(schema: JSONSchema): string[][] {
  const pathMap = new Map<string, string[]>();

  visitSchemas(schema, (candidate, path) => {
    if (
      candidate.$ref === '#/definitions/step' ||
      isStepSchemaCandidate(candidate)
    ) {
      pathMap.set(path.join('/'), path);
    }
  });

  return Array.from(pathMap.values());
}

function getNodeAtPath(root: unknown, path: string[]): JSONSchema | null {
  let current = root;
  for (const segment of path) {
    if (Array.isArray(current)) {
      const index = Number.parseInt(segment, 10);
      if (Number.isNaN(index)) {
        return null;
      }
      current = current[index];
      continue;
    }
    if (!isRecord(current)) {
      return null;
    }
    current = current[segment];
  }

  return isRecord(current) ? (current as JSONSchema) : null;
}

export function toInheritedLegacyDefinitionHints(
  editorHints?: components['schemas']['DAGEditorHints']
): EditorLegacyDefinitionHint[] {
  const legacyDefinitions: EditorLegacyDefinitionHint[] = [];

  for (const hint of editorHints?.inheritedLegacyDefinitions ?? []) {
    if (!hint?.name || !hint?.targetType || !isRecord(hint.inputSchema)) {
      continue;
    }

    const name = hint.name.trim();
    if (!legacyDefinitionNamePattern.test(name)) {
      continue;
    }

    legacyDefinitions.push({
      name,
      targetType: hint.targetType.trim(),
      description: hint.description?.trim() || undefined,
      inputSchema: cloneJson(hint.inputSchema as JSONSchema),
      outputSchema: isRecord(hint.outputSchema)
        ? cloneJson(hint.outputSchema as JSONSchema)
        : undefined,
    });
  }

  return legacyDefinitions;
}

export function toInheritedCustomActionHints(
  editorHints?: components['schemas']['DAGEditorHints']
): EditorCustomActionHint[] {
  const actions: EditorCustomActionHint[] = [];

  for (const hint of editorHints?.inheritedCustomActions ?? []) {
    if (!hint?.name || !isRecord(hint.inputSchema)) {
      continue;
    }

    const name = hint.name.trim();
    if (!customActionNamePattern.test(name)) {
      continue;
    }

    actions.push({
      name,
      description: hint.description?.trim() || undefined,
      inputSchema: cloneJson(hint.inputSchema as JSONSchema),
      outputSchema: isRecord(hint.outputSchema)
        ? cloneJson(hint.outputSchema as JSONSchema)
        : undefined,
    });
  }

  return actions;
}

export function extractLocalCustomDefinitionHints(
  yamlContent: string
): ExtractCustomDefinitionHintsResult {
  if (!yamlContent.trim()) {
    return { ok: true, legacyDefinitions: [], actions: [] };
  }

  let document: unknown;
  try {
    document = parseYaml(yamlContent);
  } catch {
    return { ok: false, legacyDefinitions: [], actions: [] };
  }

  if (!isRecord(document)) {
    return { ok: true, legacyDefinitions: [], actions: [] };
  }

  return {
    ok: true,
    legacyDefinitions: parseLocalLegacyDefinitionHints(document),
    actions: parseLocalCustomActionHints(document),
  };
}

export function mergeLegacyDefinitionHints(
  inherited: EditorLegacyDefinitionHint[],
  local: EditorLegacyDefinitionHint[]
): EditorLegacyDefinitionHint[] {
  const merged = new Map<string, EditorLegacyDefinitionHint>();

  for (const hint of inherited) {
    merged.set(hint.name.trim(), hint);
  }
  for (const hint of local) {
    merged.set(hint.name.trim(), hint);
  }

  return sortCustomHintsByName(Array.from(merged.values()));
}

export function mergeCustomActionHints(
  inherited: EditorCustomActionHint[],
  local: EditorCustomActionHint[]
): EditorCustomActionHint[] {
  const merged = new Map<string, EditorCustomActionHint>();

  for (const hint of inherited) {
    merged.set(hint.name.trim(), hint);
  }
  for (const hint of local) {
    merged.set(hint.name.trim(), hint);
  }

  return sortCustomHintsByName(Array.from(merged.values()));
}

export function legacyDefinitionHintsEqual(
  left: EditorLegacyDefinitionHint[],
  right: EditorLegacyDefinitionHint[]
): boolean {
  if (left.length !== right.length) {
    return false;
  }

  for (let index = 0; index < left.length; index += 1) {
    const leftHint = left[index];
    const rightHint = right[index];
    if (!leftHint || !rightHint) {
      return false;
    }
    if (
      legacyDefinitionHintKey(leftHint) !== legacyDefinitionHintKey(rightHint)
    ) {
      return false;
    }
  }

  return true;
}

export function customActionHintsEqual(
  left: EditorCustomActionHint[],
  right: EditorCustomActionHint[]
): boolean {
  if (left.length !== right.length) {
    return false;
  }

  for (let index = 0; index < left.length; index += 1) {
    const leftHint = left[index];
    const rightHint = right[index];
    if (!leftHint || !rightHint) {
      return false;
    }
    if (customActionHintKey(leftHint) !== customActionHintKey(rightHint)) {
      return false;
    }
  }

  return true;
}

export function buildAugmentedDAGSchema(
  baseSchema: JSONSchema,
  legacyDefinitions: EditorLegacyDefinitionHint[],
  actions: EditorCustomActionHint[] = []
): JSONSchema {
  const augmented = cloneJson(baseSchema);
  const definitions = augmented.definitions;

  if (legacyDefinitions.length === 0 && actions.length === 0) {
    for (const path of collectStepSchemaPaths(augmented)) {
      const schema = getNodeAtPath(augmented, path);
      if (!schema || !isStepLikeSchema(schema)) {
        continue;
      }
      suppressConditionalPropertySuggestions(schema);
    }

    return augmented;
  }

  if (!definitions) {
    return augmented;
  }

  const legacyDefinitionDefinitions: Record<string, JSONSchema> = {};
  const legacyDefinitionRules: JSONSchema[] = [];
  const legacyTypeNames: string[] = [];
  const legacyTypeDescriptions: string[] = [];
  const customActionDefinitions: Record<string, JSONSchema> = {};
  const customActionRules: JSONSchema[] = [];
  const customActionNames: string[] = [];
  const customActionDescriptions: string[] = [];

  for (const legacyDefinition of legacyDefinitions) {
    const escapedName = escapeJsonPointerSegment(legacyDefinition.name);
    const definitionPointer = `/definitions/${localLegacyDefinitionSchemaDefinitionsKey}/definitions/${escapedName}`;
    legacyDefinitionDefinitions[legacyDefinition.name] = rewriteInternalRefs(
      cloneJson(legacyDefinition.inputSchema),
      definitionPointer
    ) as JSONSchema;

    legacyDefinitionRules.push({
      if: {
        properties: { type: { const: legacyDefinition.name } },
        required: ['type'],
      },
      then: {
        properties: {
          with: {
            $ref: `#${definitionPointer}`,
          },
          config: {
            $ref: `#${definitionPointer}`,
            deprecated: true,
            doNotSuggest: true,
          },
        },
      },
    });

    legacyTypeNames.push(legacyDefinition.name);
    legacyTypeDescriptions.push(
      legacyDefinition.description ||
        `Deprecated legacy execution definition expanding to ${legacyDefinition.targetType}.`
    );
  }

  for (const action of actions) {
    const escapedName = escapeJsonPointerSegment(action.name);
    const definitionPointer = `/definitions/${localCustomActionSchemaDefinitionsKey}/definitions/${escapedName}`;
    customActionDefinitions[action.name] = rewriteInternalRefs(
      cloneJson(action.inputSchema),
      definitionPointer
    ) as JSONSchema;

    customActionRules.push({
      if: {
        properties: { action: { const: action.name } },
        required: ['action'],
      },
      then: {
        properties: {
          with: {
            $ref: `#${definitionPointer}`,
          },
        },
      },
    });

    customActionNames.push(action.name);
    customActionDescriptions.push(action.description || 'Custom action.');
  }

  if (legacyDefinitions.length > 0) {
    definitions[localLegacyDefinitionSchemaDefinitionsKey] = {
      definitions: legacyDefinitionDefinitions,
    };
  }
  if (actions.length > 0) {
    definitions[localCustomActionSchemaDefinitionsKey] = {
      definitions: customActionDefinitions,
    };
  }

  const resolved = dereferenceSchema(augmented);
  const resolvedLegacyDefinitionRules =
    dereferenceSchema({
      definitions: {
        [localLegacyDefinitionSchemaDefinitionsKey]: {
          definitions: legacyDefinitionDefinitions,
        },
      },
      allOf: cloneJson(legacyDefinitionRules),
    }).allOf ?? legacyDefinitionRules;
  const resolvedCustomActionRules =
    dereferenceSchema({
      definitions: {
        [localCustomActionSchemaDefinitionsKey]: {
          definitions: customActionDefinitions,
        },
      },
      allOf: cloneJson(customActionRules),
    }).allOf ?? customActionRules;

  for (const path of collectStepSchemaPaths(resolved)) {
    const schema = getNodeAtPath(resolved, path);
    if (!schema || !isStepLikeSchema(schema)) {
      continue;
    }
    augmentStepSchema(
      schema,
      resolvedLegacyDefinitionRules,
      legacyTypeNames,
      legacyTypeDescriptions,
      resolvedCustomActionRules,
      customActionNames,
      customActionDescriptions
    );
  }

  return resolved;
}
