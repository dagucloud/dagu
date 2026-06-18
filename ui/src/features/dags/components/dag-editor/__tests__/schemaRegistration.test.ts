// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import { describe, expect, it } from 'vitest';
import { buildAugmentedDAGSchema } from '../customActionSchema';
import {
  buildSchemaRegistration,
  removeSchemaRegistration,
  toMonacoYamlSchemas,
  upsertSchemaRegistration,
  type StoredSchemaRegistration,
} from '../schemaRegistration';

const modelUri = 'inmemory://dagu/local/dags/example.yaml';
const defaultSchemaUrl = '/assets/dag.schema.json';

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
      ],
    },
    actionName: {
      anyOf: [
        {
          type: 'string',
          enum: ['log.write'],
        },
      ],
    },
    step: {
      type: 'object',
      properties: {
        action: {
          $ref: '#/definitions/actionName',
        },
        with: {
          type: 'object',
        },
      },
      allOf: [],
    },
  },
};

function registrationFor(schema: JSONSchema | null | undefined) {
  return buildSchemaRegistration(
    'editor-1',
    modelUri,
    schema,
    defaultSchemaUrl
  );
}

function registrationForOwner(
  ownerId: string,
  schema: JSONSchema | null | undefined
) {
  return buildSchemaRegistration(ownerId, modelUri, schema, defaultSchemaUrl);
}

describe('schemaRegistration', () => {
  it('keeps semantically identical schema content on the same fingerprint', () => {
    const first = registrationFor({
      type: 'object',
      properties: {
        image: { type: 'string' },
        tag: { type: 'string' },
      },
    });
    const second = registrationFor({
      properties: {
        tag: { type: 'string' },
        image: { type: 'string' },
      },
      type: 'object',
    });

    expect(second.fingerprint).toBe(first.fingerprint);
  });

  it('does not update Monaco registrations for equal-content schemas', () => {
    const registrations = new Map<string, StoredSchemaRegistration>();
    const first = registrationFor({
      type: 'object',
      properties: {
        image: { type: 'string' },
      },
    });
    const second = registrationFor({
      properties: {
        image: { type: 'string' },
      },
      type: 'object',
    });

    expect(upsertSchemaRegistration(registrations, first)).toBe(true);
    expect(upsertSchemaRegistration(registrations, second)).toBe(false);
    expect(toMonacoYamlSchemas(registrations)).toHaveLength(1);
  });

  it('updates Monaco registrations when dynamic custom action schema changes', () => {
    const registrations = new Map<string, StoredSchemaRegistration>();
    const imageActionSchema = buildAugmentedDAGSchema(
      baseSchema,
      [],
      [
        {
          name: 'deploy.image',
          inputSchema: {
            type: 'object',
            properties: {
              image: { type: 'string' },
            },
          },
        },
      ]
    );
    const taggedImageActionSchema = buildAugmentedDAGSchema(
      baseSchema,
      [],
      [
        {
          name: 'deploy.image',
          inputSchema: {
            type: 'object',
            properties: {
              image: { type: 'string' },
              tag: { type: 'string' },
            },
          },
        },
      ]
    );

    const first = registrationFor(imageActionSchema);
    const second = registrationFor(taggedImageActionSchema);

    expect(first.fingerprint).not.toBe(second.fingerprint);
    expect(upsertSchemaRegistration(registrations, first)).toBe(true);
    expect(upsertSchemaRegistration(registrations, second)).toBe(true);
    expect(toMonacoYamlSchemas(registrations)[0]?.schema).toEqual(
      second.registration.schema
    );
  });

  it('ignores stale cleanup after a newer registration is active', () => {
    const registrations = new Map<string, StoredSchemaRegistration>();
    const first = registrationFor({
      type: 'object',
      properties: {
        image: { type: 'string' },
      },
    });
    const second = registrationFor({
      type: 'object',
      properties: {
        image: { type: 'string' },
        tag: { type: 'string' },
      },
    });

    expect(upsertSchemaRegistration(registrations, first)).toBe(true);
    expect(upsertSchemaRegistration(registrations, second)).toBe(true);
    expect(
      removeSchemaRegistration(
        registrations,
        modelUri,
        first.ownerId,
        first.fingerprint
      )
    ).toBe(false);
    expect(toMonacoYamlSchemas(registrations)[0]?.schema).toEqual(
      second.registration.schema
    );
  });

  it('keeps a shared schema registered while another editor still owns it', () => {
    const registrations = new Map<string, StoredSchemaRegistration>();
    const first = registrationForOwner('editor-1', {
      type: 'object',
      properties: {
        image: { type: 'string' },
      },
    });
    const second = registrationForOwner('editor-2', {
      properties: {
        image: { type: 'string' },
      },
      type: 'object',
    });

    expect(upsertSchemaRegistration(registrations, first)).toBe(true);
    expect(upsertSchemaRegistration(registrations, second)).toBe(false);
    expect(
      removeSchemaRegistration(
        registrations,
        modelUri,
        first.ownerId,
        first.fingerprint
      )
    ).toBe(false);

    expect(toMonacoYamlSchemas(registrations)).toHaveLength(1);
    expect(toMonacoYamlSchemas(registrations)[0]?.schema).toEqual(
      second.registration.schema
    );
  });
});
