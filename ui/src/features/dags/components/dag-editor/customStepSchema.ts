// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { dereferenceSchema, type JSONSchema } from '@/lib/schema-utils';
import { parse as parseYaml } from 'yaml';
import type { components } from '../../../../api/v1/schema';

export interface EditorCustomStepTypeHint {
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

export interface ExtractCustomStepTypesResult {
  ok: boolean;
  stepTypes: EditorCustomStepTypeHint[];
  actions: EditorCustomActionHint[];
}

const customStepTypeNamePattern = /^[A-Za-z][A-Za-z0-9_-]*$/;
const customActionNamePattern = /^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z][A-Za-z0-9_-]*)*$/;
const localCustomSchemaDefinitionsKey = 'customStepTypeInputSchemas';
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

function customStepTypeHintKey(hint: EditorCustomStepTypeHint): string {
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

function isCustomTypeEnumBranch(
  schema: JSONSchema,
  customTypeNames: string[]
): boolean {
  if (!Array.isArray(schema.enum)) {
    return false;
  }

  if (schema.enum.length !== customTypeNames.length) {
    return false;
  }

  return customTypeNames.every((name, index) => schema.enum?.[index] === name);
}

function buildCustomTypeEnumBranch(
  customTypeNames: string[],
  customTypeDescriptions: string[]
): JSONSchema {
  return {
    type: 'string',
    enum: customTypeNames,
    enumDescriptions: customTypeDescriptions,
    description:
      'Custom step type declared in step_types or inherited from base config.',
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
    description: 'Custom action declared in actions or inherited from base config.',
  };
}

function augmentExecutorTypeSchema(
  schema: JSONSchema,
  customTypeNames: string[],
  customTypeDescriptions: string[]
) {
  if (!Array.isArray(schema.anyOf)) {
    return;
  }

  const customTypeBranch = buildCustomTypeEnumBranch(
    customTypeNames,
    customTypeDescriptions
  );
  const anyOfWithoutCustomBranch = schema.anyOf.filter(
    (entry) =>
      !isRecord(entry) ||
      !isCustomTypeEnumBranch(entry as JSONSchema, customTypeNames)
  );

  schema.anyOf = [
    ...(anyOfWithoutCustomBranch.slice(0, 1) as JSONSchema[]),
    customTypeBranch,
    ...(anyOfWithoutCustomBranch.slice(1) as JSONSchema[]),
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
      !isCustomTypeEnumBranch(entry as JSONSchema, customActionNames)
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

function parseLocalCustomStepTypeHints(
  document: Record<string, unknown>
): EditorCustomStepTypeHint[] {
  const stepTypesValue = document.step_types;
  if (!isRecord(stepTypesValue)) {
    return [];
  }

  const stepTypes: EditorCustomStepTypeHint[] = [];
  for (const [rawName, rawDef] of Object.entries(stepTypesValue)) {
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

    if (!customStepTypeNamePattern.test(name) || !targetType) {
      continue;
    }
    if (!isRecord(rawDef.input_schema)) {
      continue;
    }

    stepTypes.push({
      name,
      targetType,
      description,
      inputSchema: cloneJson(rawDef.input_schema as JSONSchema),
      outputSchema: isRecord(rawDef.output_schema)
        ? cloneJson(rawDef.output_schema as JSONSchema)
        : undefined,
    });
  }

  return stepTypes;
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
  customStepRules: JSONSchema[],
  customTypeNames: string[],
  customTypeDescriptions: string[],
  customActionRules: JSONSchema[],
  customActionNames: string[],
  customActionDescriptions: string[]
) {
  stepSchema.allOf = appendUniqueAllOf(stepSchema.allOf, [
    ...customStepRules,
    ...customActionRules,
  ]);
  suppressConditionalPropertySuggestions(stepSchema);

  const typeSchema = stepSchema.properties?.type;
  if (customTypeNames.length > 0 && isRecord(typeSchema)) {
    const clonedTypeSchema = cloneJson(typeSchema as JSONSchema);
    stepSchema.properties = {
      ...stepSchema.properties,
      type: clonedTypeSchema,
    };
    augmentExecutorTypeSchema(
      clonedTypeSchema,
      customTypeNames,
      customTypeDescriptions
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

  for (const [propertyName, propertySchema] of Object.entries(schema.properties)) {
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

export function toInheritedCustomStepTypeHints(
  editorHints?: components['schemas']['DAGEditorHints']
): EditorCustomStepTypeHint[] {
  const stepTypes: EditorCustomStepTypeHint[] = [];

  for (const hint of editorHints?.inheritedCustomStepTypes ?? []) {
    if (!hint?.name || !hint?.targetType || !isRecord(hint.inputSchema)) {
      continue;
    }

    const name = hint.name.trim();
    if (!customStepTypeNamePattern.test(name)) {
      continue;
    }

    stepTypes.push({
      name,
      targetType: hint.targetType.trim(),
      description: hint.description?.trim() || undefined,
      inputSchema: cloneJson(hint.inputSchema as JSONSchema),
      outputSchema: isRecord(hint.outputSchema)
        ? cloneJson(hint.outputSchema as JSONSchema)
        : undefined,
    });
  }

  return stepTypes;
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

export function extractLocalCustomStepTypeHints(
  yamlContent: string
): ExtractCustomStepTypesResult {
  if (!yamlContent.trim()) {
    return { ok: true, stepTypes: [], actions: [] };
  }

  let document: unknown;
  try {
    document = parseYaml(yamlContent);
  } catch {
    return { ok: false, stepTypes: [], actions: [] };
  }

  if (!isRecord(document)) {
    return { ok: true, stepTypes: [], actions: [] };
  }

  return {
    ok: true,
    stepTypes: parseLocalCustomStepTypeHints(document),
    actions: parseLocalCustomActionHints(document),
  };
}

export function mergeCustomStepTypeHints(
  inherited: EditorCustomStepTypeHint[],
  local: EditorCustomStepTypeHint[]
): EditorCustomStepTypeHint[] {
  const merged = new Map<string, EditorCustomStepTypeHint>();

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

export function customStepTypeHintsEqual(
  left: EditorCustomStepTypeHint[],
  right: EditorCustomStepTypeHint[]
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
    if (customStepTypeHintKey(leftHint) !== customStepTypeHintKey(rightHint)) {
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
  stepTypes: EditorCustomStepTypeHint[],
  actions: EditorCustomActionHint[] = []
): JSONSchema {
  const augmented = cloneJson(baseSchema);
  const definitions = augmented.definitions;

  if (stepTypes.length === 0 && actions.length === 0) {
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

  const customDefinitions: Record<string, JSONSchema> = {};
  const customStepRules: JSONSchema[] = [];
  const customTypeNames: string[] = [];
  const customTypeDescriptions: string[] = [];
  const customActionDefinitions: Record<string, JSONSchema> = {};
  const customActionRules: JSONSchema[] = [];
  const customActionNames: string[] = [];
  const customActionDescriptions: string[] = [];

  for (const stepType of stepTypes) {
    const escapedName = escapeJsonPointerSegment(stepType.name);
    const definitionPointer = `/definitions/${localCustomSchemaDefinitionsKey}/definitions/${escapedName}`;
    customDefinitions[stepType.name] = rewriteInternalRefs(
      cloneJson(stepType.inputSchema),
      definitionPointer
    ) as JSONSchema;

    customStepRules.push({
      if: {
        properties: { type: { const: stepType.name } },
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

    customTypeNames.push(stepType.name);
    customTypeDescriptions.push(
      stepType.description ||
        `Custom step type expanding to ${stepType.targetType}.`
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

  if (stepTypes.length > 0) {
    definitions[localCustomSchemaDefinitionsKey] = {
      definitions: customDefinitions,
    };
  }
  if (actions.length > 0) {
    definitions[localCustomActionSchemaDefinitionsKey] = {
      definitions: customActionDefinitions,
    };
  }

  const resolved = dereferenceSchema(augmented);
  const resolvedCustomStepRules =
    dereferenceSchema({
      definitions: {
        [localCustomSchemaDefinitionsKey]: {
          definitions: customDefinitions,
        },
      },
      allOf: cloneJson(customStepRules),
    }).allOf ?? customStepRules;
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
      resolvedCustomStepRules,
      customTypeNames,
      customTypeDescriptions,
      resolvedCustomActionRules,
      customActionNames,
      customActionDescriptions
    );
  }

  return resolved;
}
