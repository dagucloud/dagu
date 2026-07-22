// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';

export const controllerSchema: JSONSchema = {
  $id: 'https://dagu.dev/schemas/controller.schema.json',
  title: 'Controller',
  type: 'object',
  additionalProperties: false,
  required: ['type', 'version', 'name', 'description', 'llm', 'dags', 'states'],
  properties: {
    type: { type: 'string', const: 'controller' },
    version: { type: 'integer', const: 1 },
    id: {
      type: 'string',
      pattern: '^ctrl_[a-z2-7]{16}$',
      description: 'Immutable server-generated ID.',
    },
    name: { type: 'string', minLength: 1, maxLength: 100 },
    description: { type: 'string', maxLength: 4096 },
    maxTurns: { type: 'integer', minimum: 2, maximum: 1000, default: 100 },
    labels: { type: 'array', items: { type: 'string' } },
    llm: {
      type: 'object',
      additionalProperties: false,
      required: ['provider', 'model'],
      properties: {
        provider: { type: 'string', enum: ['openai', 'anthropic', 'gemini'] },
        model: { type: 'string', minLength: 1 },
        system: {
          type: 'string',
          maxLength: 16384,
          description:
            'Starts with the reserved Router instruction placeholder.',
        },
      },
    },
    dags: { type: 'array', maxItems: 64, items: { type: 'string' } },
    states: {
      type: 'object',
      maxProperties: 64,
      propertyNames: { pattern: '^[A-Za-z][A-Za-z0-9_-]{0,63}$' },
      additionalProperties: {
        type: 'object',
        additionalProperties: false,
        required: ['description', 'dags', 'transitions'],
        properties: {
          description: { type: 'string', maxLength: 4096 },
          dags: { type: 'array', items: { type: 'string' } },
          transitions: {
            type: 'array',
            items: {
              type: 'object',
              additionalProperties: false,
              required: ['to', 'when'],
              properties: {
                to: { type: 'string' },
                when: { type: 'string', maxLength: 4096 },
              },
            },
          },
          terminal: { type: 'string', enum: ['succeeded', 'failed'] },
        },
      },
    },
  },
};
