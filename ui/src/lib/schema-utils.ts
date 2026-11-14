/**
 * Utilities for navigating and resolving JSON Schema paths
 */

import { parse as parseYaml } from 'yaml';

/**
 * Context for schema resolution with YAML document values
 */
export interface SchemaContext {
  /** Parsed YAML document */
  document?: unknown;
  /** Current path being resolved */
  currentPath?: string[];
}

export interface JSONSchema {
  $id?: string;
  $ref?: string;
  type?: string | string[];
  description?: string;
  markdownDescription?: string;
  default?: unknown;
  enum?: unknown[];
  enumDescriptions?: string[];
  markdownEnumDescriptions?: string[];
  examples?: unknown[];
  deprecated?: boolean;
  properties?: Record<string, JSONSchema>;
  patternProperties?: Record<string, JSONSchema>;
  propertyNames?: JSONSchema;
  additionalProperties?: boolean | JSONSchema;
  items?: JSONSchema | JSONSchema[];
  oneOf?: JSONSchema[];
  anyOf?: JSONSchema[];
  allOf?: JSONSchema[];
  required?: string[];
  definitions?: Record<string, JSONSchema>;
  title?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  format?: string;
  // JSON Schema Draft 7 conditional keywords
  if?: JSONSchema;
  then?: JSONSchema;
  else?: JSONSchema;
  const?: unknown;
  [key: string]: unknown;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function unescapeJsonPointerSegment(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}

function resolveInternalRef(root: unknown, ref: string): unknown {
  if (ref === '#') {
    return root;
  }
  if (!ref.startsWith('#/')) {
    return {};
  }

  const segments = ref.slice(2).split('/').map(unescapeJsonPointerSegment);

  let current: unknown = root;
  for (const segment of segments) {
    if (current === null || current === undefined) {
      return {};
    }
    if (Array.isArray(current)) {
      const index = Number.parseInt(segment, 10);
      if (Number.isNaN(index)) {
        return {};
      }
      current = current[index];
      continue;
    }
    if (typeof current !== 'object') {
      return {};
    }
    current = (current as Record<string, unknown>)[segment];
  }

  return current ?? {};
}

/**
 * Resolves internal $ref pointers in a JSON Schema.
 * External refs are left untouched.
 */
export function dereferenceSchema(schema: JSONSchema): JSONSchema {
  const cache = new Map<string, JSONSchema>();
  const resolving = new Set<string>();

  function processNode(node: unknown): unknown {
    if (!node || typeof node !== 'object') {
      return node;
    }

    if (Array.isArray(node)) {
      return node.map(processNode);
    }

    const obj = node as Record<string, unknown>;
    if (typeof obj.$ref === 'string' && obj.$ref.startsWith('#')) {
      const ref = obj.$ref;
      if (resolving.has(ref)) {
        return { ...obj };
      }

      let resolved = cache.get(ref);
      if (!resolved) {
        resolving.add(ref);
        try {
          const dereferenced = processNode(resolveInternalRef(schema, ref));
          resolved = isRecord(dereferenced) ? (dereferenced as JSONSchema) : {};
          cache.set(ref, resolved);
        } finally {
          resolving.delete(ref);
        }
      }

      const { $ref: _ref, ...rest } = obj;
      // resolved is already dereferenced; walking it again would expand the
      // references it keeps to break cycles.
      return Object.keys(rest).length === 0
        ? resolved
        : { ...resolved, ...(processNode(rest) as Record<string, unknown>) };
    }

    const result: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(obj)) {
      result[key] = processNode(value);
    }
    return result;
  }

  return processNode(schema) as JSONSchema;
}

export interface SchemaPropertyInfo {
  name: string;
  path: string[];
  type: string | string[];
  description?: string;
  default?: unknown;
  enum?: unknown[];
  required: boolean;
  deprecated?: boolean;
  examples?: unknown[];
  properties?: Record<string, SchemaPropertyInfo>;
  items?: SchemaPropertyInfo;
  oneOf?: SchemaPropertyInfo[];
  title?: string;
  format?: string;
  pattern?: string;
}

function collectEnumValues(schema: JSONSchema): unknown[] | undefined {
  const values: unknown[] = [];
  const seen = new Set<string>();

  const addValues = (items?: unknown[]) => {
    for (const item of items ?? []) {
      const key = JSON.stringify(item);
      if (seen.has(key)) {
        continue;
      }
      values.push(item);
      seen.add(key);
    }
  };

  addValues(schema.enum);

  for (const variant of schema.oneOf ?? []) {
    addValues(collectEnumValues(variant));
  }

  for (const variant of schema.anyOf ?? []) {
    addValues(collectEnumValues(variant));
  }

  return values.length > 0 ? values : undefined;
}

/**
 * Gets a value from a parsed YAML document at the specified path
 */
function getValueAtPath(document: unknown, path: string[]): unknown {
  let current: unknown = document;
  for (const segment of path) {
    if (current === null || current === undefined) {
      return undefined;
    }
    if (typeof current !== 'object') {
      return undefined;
    }
    if (Array.isArray(current)) {
      const index = parseInt(segment, 10);
      if (isNaN(index)) {
        return undefined;
      }
      current = current[index];
    } else {
      current = (current as Record<string, unknown>)[segment];
    }
  }
  return current;
}

/**
 * Evaluates if a JSON Schema condition matches against a value
 */
function evaluateCondition(condition: JSONSchema, value: unknown): boolean {
  // Check properties conditions (e.g., { properties: { type: { const: "docker" } } })
  if (condition.properties && typeof value === 'object' && value !== null) {
    const obj = value as Record<string, unknown>;
    for (const [propName, propCondition] of Object.entries(
      condition.properties
    )) {
      const propValue = obj[propName];
      if (!evaluateCondition(propCondition, propValue)) {
        return false;
      }
    }
    return true;
  }

  // Check const condition
  if (condition.const !== undefined) {
    return value === condition.const;
  }

  // Check enum condition
  if (condition.enum !== undefined) {
    return condition.enum.includes(value);
  }

  // Check type condition
  if (condition.type !== undefined) {
    const types = Array.isArray(condition.type)
      ? condition.type
      : [condition.type];
    const actualType = typeof value;
    if (value === null) {
      return types.includes('null');
    }
    if (Array.isArray(value)) {
      return types.includes('array');
    }
    if (actualType === 'number') {
      return types.includes('number') || types.includes('integer');
    }
    return types.includes(actualType);
  }

  // Default: condition passes
  return true;
}

/**
 * Resolves a schema at a given YAML path
 * @param schema The root JSON schema
 * @param path The path to resolve
 * @param yamlContent Optional YAML content for context-aware resolution of if-then conditionals
 */
export function getSchemaAtPath(
  schema: JSONSchema,
  path: string[],
  yamlContent?: string
): JSONSchema | null {
  if (!schema || path.length === 0) {
    return schema;
  }

  // Parse YAML content once if provided
  let document: unknown = undefined;
  if (yamlContent) {
    try {
      document = parseYaml(yamlContent);
    } catch {
      // Ignore parse errors, proceed without context
    }
  }

  let current: JSONSchema | null = resolveSchemaNode(schema, schema);

  for (let i = 0; i < path.length; i++) {
    const segment = path[i];
    current = resolveSchemaNode(schema, current);

    if (!current || !segment) {
      return null;
    }

    // Handle array index access
    if (/^\d+$/.test(segment)) {
      current = resolveArrayItems(current);
      if (!current) {
        return null;
      }
      continue;
    }

    // Try to resolve from properties
    const prop: JSONSchema | undefined = current.properties?.[segment];
    if (prop) {
      // Check if current schema has allOf with if-then that overrides this property
      if (current.allOf) {
        const parentPath = path.slice(0, i + 1);
        const parentValue = document
          ? getValueAtPath(document, parentPath.slice(0, -1))
          : undefined;

        const allOfOverride = findInAllOfWithContext(
          current.allOf,
          segment,
          parentValue
        );
        if (allOfOverride) {
          const overrideProp = allOfOverride.properties?.[segment];
          if (overrideProp) {
            current = overrideProp;
            continue;
          }
        }
      }

      current = prop;
      continue;
    }

    // Try to find in oneOf/anyOf schemas
    const unionMatch = findInUnionTypes(current, segment);
    if (unionMatch) {
      let unionProp = unionMatch.properties?.[segment];

      // Check if the matched union variant has allOf with if-then that overrides this property
      if (unionMatch.allOf && unionProp) {
        // Get the parent object value for context-aware condition evaluation
        const parentPath = path.slice(0, i);
        const parentValue = document
          ? getValueAtPath(document, parentPath)
          : undefined;

        const allOfOverride = findInAllOfWithContext(
          unionMatch.allOf,
          segment,
          parentValue
        );
        if (allOfOverride) {
          const overrideProp = allOfOverride.properties?.[segment];
          if (overrideProp) {
            unionProp = overrideProp;
          }
        }
      }

      current = unionProp || unionMatch;
      continue;
    }

    // Try allOf - merge all schemas and look for property
    if (current.allOf) {
      const parentPath = path.slice(0, i);
      const parentValue = document
        ? getValueAtPath(document, parentPath)
        : undefined;

      const allOfMatch = findInAllOfWithContext(
        current.allOf,
        segment,
        parentValue
      );
      if (allOfMatch) {
        const allOfProp = allOfMatch.properties?.[segment];
        current = allOfProp || allOfMatch;
        continue;
      }
    }

    // Check additionalProperties
    if (
      current.additionalProperties &&
      typeof current.additionalProperties === 'object'
    ) {
      current = current.additionalProperties;
      continue;
    }

    // Not found
    return null;
  }

  return resolveSchemaNode(schema, current);
}

function resolveSchemaNode(
  root: JSONSchema,
  node: JSONSchema | null
): JSONSchema | null {
  if (!node) {
    return null;
  }

  let current = node;
  const visited = new Set<string>();

  while (typeof current.$ref === 'string' && current.$ref.startsWith('#')) {
    if (visited.has(current.$ref)) {
      return current;
    }
    visited.add(current.$ref);

    const resolved = resolveInternalRef(root, current.$ref);
    if (!isRecord(resolved)) {
      return current;
    }

    const { $ref: _ref, ...rest } = current;
    current =
      Object.keys(rest).length === 0
        ? (resolved as JSONSchema)
        : ({ ...(resolved as JSONSchema), ...rest } as JSONSchema);
  }

  return current;
}

/**
 * Resolves array items schema, handling oneOf arrays
 */
function resolveArrayItems(schema: JSONSchema): JSONSchema | null {
  // Direct items property
  if (schema.items) {
    if (Array.isArray(schema.items)) {
      return schema.items[0] || null;
    }
    return schema.items;
  }

  // Check oneOf for array type
  if (schema.oneOf) {
    for (const variant of schema.oneOf) {
      if (variant.type === 'array' && variant.items) {
        if (Array.isArray(variant.items)) {
          return variant.items[0] || null;
        }
        return variant.items;
      }
      // Also check if variant itself has items (implicit array)
      if (variant.items) {
        if (Array.isArray(variant.items)) {
          return variant.items[0] || null;
        }
        return variant.items;
      }
    }
  }

  // Check anyOf for array type
  if (schema.anyOf) {
    for (const variant of schema.anyOf) {
      if (variant.type === 'array' && variant.items) {
        if (Array.isArray(variant.items)) {
          return variant.items[0] || null;
        }
        return variant.items;
      }
      if (variant.items) {
        if (Array.isArray(variant.items)) {
          return variant.items[0] || null;
        }
        return variant.items;
      }
    }
  }

  return null;
}

/**
 * Searches oneOf/anyOf schemas for a property
 */
function findInUnionTypes(
  schema: JSONSchema,
  propertyName: string
): JSONSchema | null {
  const unions = [...(schema.oneOf || []), ...(schema.anyOf || [])];

  for (const variant of unions) {
    if (variant.properties?.[propertyName]) {
      return variant;
    }
    // Recursively check nested unions
    if (variant.oneOf || variant.anyOf) {
      const nested = findInUnionTypes(variant, propertyName);
      if (nested) return nested;
    }
  }

  return null;
}

/**
 * Searches allOf schemas for a property with context-aware if-then evaluation
 * @param allOf Array of schemas in allOf
 * @param propertyName Property to search for
 * @param contextValue The actual value from YAML document for condition evaluation
 */
function findInAllOfWithContext(
  allOf: JSONSchema[],
  propertyName: string,
  contextValue: unknown
): JSONSchema | null {
  // First pass: try to find a matching if-then conditional
  if (contextValue !== undefined) {
    for (const schema of allOf) {
      // Check if-then-else conditionals with actual value evaluation
      if (schema.if !== undefined) {
        const conditionMatches = evaluateCondition(schema.if, contextValue);

        if (conditionMatches && schema.then) {
          // Condition matches, check 'then' branch
          if (schema.then.properties?.[propertyName]) {
            return schema.then;
          }
          // Check nested structures in 'then'
          if (schema.then.oneOf || schema.then.anyOf) {
            const nested = findInUnionTypes(schema.then, propertyName);
            if (nested) return nested;
          }
          if (schema.then.allOf) {
            const nested = findInAllOfWithContext(
              schema.then.allOf,
              propertyName,
              contextValue
            );
            if (nested) return nested;
          }
        } else if (!conditionMatches && schema.else) {
          // Condition doesn't match, check 'else' branch
          if (schema.else.properties?.[propertyName]) {
            return schema.else;
          }
          if (schema.else.oneOf || schema.else.anyOf) {
            const nested = findInUnionTypes(schema.else, propertyName);
            if (nested) return nested;
          }
          if (schema.else.allOf) {
            const nested = findInAllOfWithContext(
              schema.else.allOf,
              propertyName,
              contextValue
            );
            if (nested) return nested;
          }
        }
      }
    }
  }

  // Second pass: fall back to non-contextual search
  return findInAllOf(allOf, propertyName);
}

/**
 * Searches allOf schemas for a property (fallback without context)
 */
function findInAllOf(
  allOf: JSONSchema[],
  propertyName: string
): JSONSchema | null {
  for (const schema of allOf) {
    if (schema.properties?.[propertyName]) {
      return schema;
    }

    // Check if-then-else conditionals (JSON Schema Draft 7) - returns first match
    if (schema.if !== undefined) {
      // Check 'then' branch
      if (schema.then?.properties?.[propertyName]) {
        return schema.then;
      }
      if (schema.then) {
        if (schema.then.oneOf || schema.then.anyOf) {
          const nested = findInUnionTypes(schema.then, propertyName);
          if (nested) return nested;
        }
        if (schema.then.allOf) {
          const nested = findInAllOf(schema.then.allOf, propertyName);
          if (nested) return nested;
        }
      }

      // Check 'else' branch
      if (schema.else?.properties?.[propertyName]) {
        return schema.else;
      }
      if (schema.else) {
        if (schema.else.oneOf || schema.else.anyOf) {
          const nested = findInUnionTypes(schema.else, propertyName);
          if (nested) return nested;
        }
        if (schema.else.allOf) {
          const nested = findInAllOf(schema.else.allOf, propertyName);
          if (nested) return nested;
        }
      }
    }

    // Check nested union types (oneOf/anyOf)
    if (schema.oneOf || schema.anyOf) {
      const nested = findInUnionTypes(schema, propertyName);
      if (nested) return nested;
    }
    if (schema.allOf) {
      const nested = findInAllOf(schema.allOf, propertyName);
      if (nested) return nested;
    }
  }
  return null;
}

/**
 * Converts a JSON Schema to SchemaPropertyInfo for display
 */
export function toPropertyInfo(
  schema: JSONSchema | null,
  name: string,
  path: string[],
  parentRequired: string[] = [],
  seen = new WeakSet<object>()
): SchemaPropertyInfo | null {
  if (!schema) {
    return null;
  }

  if (seen.has(schema)) {
    return {
      name,
      path,
      type: resolveType(schema),
      description: schema.description,
      default: schema.default,
      enum: collectEnumValues(schema),
      required: parentRequired.includes(name),
      deprecated: schema.deprecated,
      examples: schema.examples,
      title: schema.title,
      format: schema.format,
      pattern: schema.pattern,
    };
  }
  seen.add(schema);

  const typeValue = resolveType(schema);
  const isRequired = parentRequired.includes(name);

  const info: SchemaPropertyInfo = {
    name,
    path,
    type: typeValue,
    description: schema.description,
    default: schema.default,
    enum: collectEnumValues(schema),
    required: isRequired,
    deprecated: schema.deprecated,
    examples: schema.examples,
    title: schema.title,
    format: schema.format,
    pattern: schema.pattern,
  };

  // Add nested properties info
  if (schema.properties) {
    info.properties = {};
    const childRequired = schema.required || [];
    for (const [key, propSchema] of Object.entries(schema.properties)) {
      const childInfo = toPropertyInfo(
        propSchema,
        key,
        [...path, key],
        childRequired,
        seen
      );
      if (childInfo) {
        info.properties[key] = childInfo;
      }
    }
  }

  // Add items info for arrays
  if (schema.items && !Array.isArray(schema.items)) {
    info.items =
      toPropertyInfo(schema.items, 'items', [...path, '[]'], [], seen) ||
      undefined;
  }

  // Add oneOf info
  if (schema.oneOf) {
    info.oneOf = schema.oneOf
      .map((variant, i) =>
        toPropertyInfo(variant, `option${i + 1}`, path, [], seen)
      )
      .filter((v): v is SchemaPropertyInfo => v !== null);
  }

  return info;
}

/**
 * Resolves the type string(s) from a schema
 */
function resolveType(schema: JSONSchema): string | string[] {
  if (schema.type) {
    return schema.type;
  }

  if (schema.oneOf) {
    const types = schema.oneOf
      .map((s) => resolveType(s))
      .flat()
      .filter((t): t is string => typeof t === 'string')
      .filter((t, i, arr) => arr.indexOf(t) === i);
    return types.length === 1 ? (types[0] ?? 'unknown') : types;
  }

  if (schema.anyOf) {
    const types = schema.anyOf
      .map((s) => resolveType(s))
      .flat()
      .filter((t): t is string => typeof t === 'string')
      .filter((t, i, arr) => arr.indexOf(t) === i);
    return types.length === 1 ? (types[0] ?? 'unknown') : types;
  }

  if (schema.enum) {
    return 'enum';
  }

  if (schema.properties) {
    return 'object';
  }

  if (schema.items) {
    return 'array';
  }

  return 'unknown';
}

/**
 * Gets sibling properties at the same level
 */
export function getSiblingProperties(
  schema: JSONSchema,
  path: string[],
  yamlContent?: string
): string[] {
  if (path.length === 0) {
    return Object.keys(schema.properties || {});
  }

  const parentPath = path.slice(0, -1);
  const parentSchema = getSchemaAtPath(schema, parentPath, yamlContent);

  if (!parentSchema?.properties) {
    return [];
  }

  return Object.keys(parentSchema.properties);
}

/**
 * Gets the parent schema's required fields to determine if current property is required
 */
export function getParentRequired(
  schema: JSONSchema,
  path: string[],
  yamlContent?: string
): string[] {
  if (path.length === 0) {
    return schema.required || [];
  }

  const parentPath = path.slice(0, -1);
  const parentSchema = getSchemaAtPath(schema, parentPath, yamlContent);

  return parentSchema?.required || [];
}
