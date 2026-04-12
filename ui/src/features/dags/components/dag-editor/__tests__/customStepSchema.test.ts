// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  dereferenceSchema,
  getSchemaAtPath,
  toPropertyInfo,
  type JSONSchema,
} from '@/lib/schema-utils';
import { describe, expect, it } from 'vitest';
import {
  buildAugmentedDAGSchema,
  extractLocalCustomStepTypeHints,
  mergeCustomStepTypeHints,
} from '../customStepSchema';

const baseSchema: JSONSchema = {
  type: 'object',
  properties: {
    steps: {
      type: 'array',
      items: {
        $ref: '#/definitions/step',
      },
    },
  },
  definitions: {
    executorType: {
      anyOf: [
        {
          type: 'string',
          enum: ['command'],
        },
        {
          type: 'string',
          pattern: '^[A-Za-z][A-Za-z0-9_-]*$',
        },
      ],
    },
    step: {
      type: 'object',
      properties: {
        name: {
          type: 'string',
        },
        type: {
          $ref: '#/definitions/executorType',
        },
        config: {
          type: 'object',
        },
      },
      allOf: [],
    },
  },
};

const baseSchemaWithExecutorObject: JSONSchema = {
  type: 'object',
  properties: {
    steps: {
      type: 'array',
      items: {
        $ref: '#/definitions/step',
      },
    },
  },
  definitions: {
    executorType: {
      anyOf: [
        {
          type: 'string',
          enum: ['command'],
        },
        {
          type: 'string',
          pattern: '^[A-Za-z][A-Za-z0-9_-]*$',
        },
      ],
    },
    executorObject: {
      type: 'object',
      properties: {
        type: {
          $ref: '#/definitions/executorType',
        },
        config: {
          type: 'object',
        },
      },
      allOf: [],
    },
    step: {
      type: 'object',
      properties: {
        name: {
          type: 'string',
        },
        type: {
          $ref: '#/definitions/executorType',
        },
        config: {
          type: 'object',
        },
        executor: {
          $ref: '#/definitions/executorObject',
        },
      },
      allOf: [],
    },
  },
};

const dereferencedBaseSchema = dereferenceSchema(baseSchema);

describe('customStepSchema', () => {
  it('extracts local custom step types from YAML', () => {
    const result = extractLocalCustomStepTypeHints(`
step_types:
  greet:
    type: command
    description: Send a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: echo
        args:
          - {$input: message}
`);

    expect(result.ok).toBe(true);
    expect(result.stepTypes).toHaveLength(1);
    expect(result.stepTypes[0]).toMatchObject({
      name: 'greet',
      targetType: 'command',
      description: 'Send a greeting',
    });
  });

  it('preserves the local definition when it overrides an inherited name', () => {
    const merged = mergeCustomStepTypeHints(
      [
        {
          name: 'greet',
          targetType: 'command',
          inputSchema: {
            type: 'object',
            properties: {
              message: { type: 'string' },
            },
          },
        },
      ],
      [
        {
          name: 'greet',
          targetType: 'command',
          inputSchema: {
            type: 'object',
            properties: {
              count: { type: 'integer' },
            },
          },
        },
      ]
    );

    const schema = buildAugmentedDAGSchema(baseSchema, merged);
    const propertySchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'config', 'count'],
      `
steps:
  - type: greet
    config:
      count: 1
`
    );

    expect(propertySchema).toMatchObject({ type: 'integer' });
  });

  it('augments dereferenced step schemas with custom config inference', () => {
    const schema = buildAugmentedDAGSchema(dereferencedBaseSchema, [
      {
        name: 'greet',
        targetType: 'command',
        inputSchema: {
          type: 'object',
          properties: {
            count: { type: 'integer' },
          },
        },
      },
    ]);

    const propertySchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'config', 'count'],
      `
steps:
  - type: greet
    config:
      count: 1
`
    );

    expect(propertySchema).toMatchObject({ type: 'integer' });
  });

  it('shows builtin and custom step names in type docs for dereferenced schemas', () => {
    const schema = buildAugmentedDAGSchema(dereferencedBaseSchema, [
      {
        name: 'greet',
        targetType: 'command',
        description: 'Send a greeting',
        inputSchema: {
          type: 'object',
          properties: {
            message: { type: 'string' },
          },
        },
      },
    ]);

    const typeSchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'type'],
      `
steps:
  - type: greet
`
    );
    const propertyInfo = toPropertyInfo(typeSchema, 'type', [
      'steps',
      '0',
      'type',
    ]);

    expect(propertyInfo?.enum).toEqual(
      expect.arrayContaining(['command', 'greet'])
    );
  });

  it('does not augment executor objects that only reuse type/config fields', () => {
    const schema = buildAugmentedDAGSchema(baseSchemaWithExecutorObject, [
      {
        name: 'greet',
        targetType: 'command',
        inputSchema: {
          type: 'object',
          properties: {
            message: { type: 'string' },
          },
        },
      },
    ]);

    const typeSchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'executor', 'type'],
      `
steps:
  - name: example
    executor:
      type: command
`
    );
    const propertyInfo = toPropertyInfo(typeSchema, 'type', [
      'steps',
      '0',
      'executor',
      'type',
    ]);

    expect(propertyInfo?.enum).toEqual(['command']);
  });

  it('resolves internal refs inside local custom input schemas', () => {
    const schema = buildAugmentedDAGSchema(baseSchema, [
      {
        name: 'greet',
        targetType: 'command',
        inputSchema: {
          type: 'object',
          properties: {
            profile: {
              $ref: '#/definitions/profile',
            },
          },
          definitions: {
            profile: {
              type: 'object',
              properties: {
                message: {
                  type: 'string',
                },
              },
            },
          },
        },
      },
    ]);

    const propertySchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'config', 'profile', 'message'],
      `
steps:
  - type: greet
    config:
      profile:
        message: hello
`
    );

    expect(propertySchema).toMatchObject({ type: 'string' });
  });

  it('does not augment nested custom input schemas that only resemble steps', () => {
    const schema = buildAugmentedDAGSchema(dereferencedBaseSchema, [
      {
        name: 'greet',
        targetType: 'command',
        inputSchema: {
          type: 'object',
          properties: {
            nested: {
              type: 'object',
              properties: {
                type: {
                  type: 'string',
                },
                config: {
                  type: 'string',
                },
              },
            },
          },
        },
      },
    ]);

    const nestedTypeSchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'config', 'nested', 'type'],
      `
steps:
  - type: greet
    config:
      nested:
        type: internal
`
    );
    const nestedConfigSchema = getSchemaAtPath(
      schema,
      ['steps', '0', 'config', 'nested', 'config'],
      `
steps:
  - type: greet
    config:
      nested:
        config: value
`
    );

    expect(nestedTypeSchema).toEqual({ type: 'string' });
    expect(nestedConfigSchema).toEqual({ type: 'string' });
  });

  it('handles recursive internal refs without infinite recursion', () => {
    const recursiveSchema = dereferenceSchema({
      type: 'object',
      properties: {
        node: {
          $ref: '#/definitions/node',
        },
      },
      definitions: {
        node: {
          type: 'object',
          properties: {
            value: {
              type: 'string',
            },
            next: {
              $ref: '#/definitions/node',
            },
          },
        },
      },
    });

    const valueSchema = getSchemaAtPath(recursiveSchema, [
      'node',
      'next',
      'value',
    ]);
    const propertyInfo = toPropertyInfo(
      recursiveSchema.properties?.node as JSONSchema,
      'node',
      ['node']
    );

    expect(valueSchema).toMatchObject({ type: 'string' });
    expect(propertyInfo?.properties?.next).toBeDefined();
  });

  it('marks invalid YAML extraction as unsuccessful', () => {
    const result = extractLocalCustomStepTypeHints(`
step_types:
  greet:
    input_schema:
      - invalid
    type: command
steps:
  - type: greet
    config:
      message: [unterminated
`);

    expect(result.ok).toBe(false);
    expect(result.stepTypes).toEqual([]);
  });
});
