// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '@/lib/schema-utils';
import {
  CONTROLLER_DAG_NAME_PATTERN,
  CONTROLLER_ID_PATTERN,
  CONTROLLER_STATE_NAME_PATTERN,
  DEFAULT_CONTROLLER_MAX_TURNS,
  MAX_CONTROLLER_DAGS,
  MAX_CONTROLLER_DESCRIPTION_BYTES,
  MAX_CONTROLLER_MAX_TURNS,
  MAX_CONTROLLER_NAME_CODE_POINTS,
  MAX_CONTROLLER_STATES,
  MAX_CONTROLLER_SYSTEM_PROMPT_BYTES,
  MAX_CONTROLLER_TRANSITIONS,
  MIN_CONTROLLER_MAX_TURNS,
} from './constraints';

export const controllerSchema: JSONSchema = {
  $id: 'https://dagu.dev/schemas/controller.schema.json',
  title: 'Controller',
  type: 'object',
  additionalProperties: false,
  required: ['type', 'version', 'name', 'llm', 'dags', 'states'],
  properties: {
    type: { type: 'string', const: 'controller' },
    version: { type: 'integer', const: 1 },
    id: {
      type: 'string',
      pattern: CONTROLLER_ID_PATTERN.source,
      description: 'Immutable server-generated ID.',
    },
    name: {
      type: 'string',
      minLength: 1,
      maxLength: MAX_CONTROLLER_NAME_CODE_POINTS,
    },
    description: {
      type: 'string',
      maxLength: MAX_CONTROLLER_DESCRIPTION_BYTES,
    },
    maxTurns: {
      type: 'integer',
      minimum: MIN_CONTROLLER_MAX_TURNS,
      maximum: MAX_CONTROLLER_MAX_TURNS,
      default: DEFAULT_CONTROLLER_MAX_TURNS,
    },
    labels: { type: 'array', items: { type: 'string' } },
    llm: {
      type: 'object',
      additionalProperties: false,
      required: ['provider', 'model'],
      properties: {
        provider: { type: 'string', pattern: '\\S' },
        model: { type: 'string', pattern: '\\S' },
        system: {
          type: 'string',
          maxLength: MAX_CONTROLLER_SYSTEM_PROMPT_BYTES,
          description:
            'Starts with the reserved Router instruction placeholder.',
        },
      },
    },
    dags: {
      type: 'array',
      maxItems: MAX_CONTROLLER_DAGS,
      items: { type: 'string', pattern: CONTROLLER_DAG_NAME_PATTERN.source },
    },
    states: {
      type: 'object',
      maxProperties: MAX_CONTROLLER_STATES,
      propertyNames: { pattern: CONTROLLER_STATE_NAME_PATTERN.source },
      additionalProperties: {
        type: 'object',
        additionalProperties: false,
        properties: {
          description: {
            type: 'string',
            maxLength: MAX_CONTROLLER_DESCRIPTION_BYTES,
          },
          dags: {
            type: 'array',
            items: {
              type: 'string',
              pattern: CONTROLLER_DAG_NAME_PATTERN.source,
            },
          },
          transitions: {
            type: 'array',
            maxItems: MAX_CONTROLLER_TRANSITIONS,
            items: {
              type: 'object',
              additionalProperties: false,
              required: ['to', 'when'],
              properties: {
                to: { type: 'string' },
                when: {
                  type: 'string',
                  pattern: '\\S',
                  maxLength: MAX_CONTROLLER_DESCRIPTION_BYTES,
                },
              },
            },
          },
          terminal: { type: 'string', enum: ['succeeded', 'failed'] },
        },
      },
    },
  },
};
