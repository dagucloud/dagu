import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { dereferenceSchema, type JSONSchema } from '@/lib/schema-utils';

describe('dereferenceSchema', () => {
  it('handles recursive internal references without overflowing the stack', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        root: {
          $ref: '#/definitions/node',
        },
      },
      definitions: {
        node: {
          type: 'object',
          properties: {
            name: { type: 'string' },
            next: {
              $ref: '#/definitions/node',
            },
          },
        },
      },
    };

    expect(() => dereferenceSchema(schema)).not.toThrow();
    const dereferenced = dereferenceSchema(schema);
    expect(dereferenced.properties?.root?.properties?.next).toBeDefined();
  });

  it('handles a self reference that carries sibling keys', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        selector: {
          $ref: '#/definitions/selector',
          description: 'Where to act',
        },
      },
      definitions: {
        selector: {
          type: 'object',
          properties: {
            name: { type: 'string' },
            in: {
              $ref: '#/definitions/selector',
              description: 'Parent element',
            },
          },
        },
      },
    };

    expect(() => dereferenceSchema(schema)).not.toThrow();
    const selector = dereferenceSchema(schema).properties?.selector;
    expect(selector?.description).toBe('Where to act');
    expect(selector?.properties?.name?.type).toBe('string');
  });

  it('dereferences a sibling key that points at the same definition', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        step: {
          $ref: '#/definitions/base',
          allOf: [{ $ref: '#/definitions/base' }],
        },
      },
      definitions: {
        base: {
          type: 'object',
          properties: { id: { type: 'string' } },
        },
      },
    };

    const step = dereferenceSchema(schema).properties?.step;
    expect(step?.properties?.id?.type).toBe('string');
    expect(step?.allOf?.[0]?.properties?.id?.type).toBe('string');
  });

  it('dereferences the bundled DAG schema used by the editor', () => {
    const schemaPath = path.resolve(
      path.dirname(fileURLToPath(import.meta.url)),
      '../../../../schemas/dag.schema.json'
    );
    const rawSchema = JSON.parse(
      fs.readFileSync(schemaPath, 'utf8')
    ) as JSONSchema;

    expect(() => dereferenceSchema(rawSchema)).not.toThrow();
    const dereferenced = dereferenceSchema(rawSchema);
    expect(Object.keys(dereferenced.properties ?? {})).toContain('name');
  });
});
