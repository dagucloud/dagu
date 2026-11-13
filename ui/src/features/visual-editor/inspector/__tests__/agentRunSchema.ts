// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema } from '../../contracts';

/*
 * The `with` of `agent.run` as Spec 091 ("The action") lists it. The tests
 * use this fixture, not `dag.schema.json`, so that they do not depend on the
 * schema file of the repository: the form shows what the schema says.
 */

/** The keys of the `with` of `agent.run`, in the order of the spec. */
export const AGENT_RUN_KEYS = [
  'prompt',
  'system',
  'provider',
  'endpoint',
  'dags',
  'tools',
  'skills',
  'max_tool_iterations',
  'max_tool_calls',
] as const;

const agentRunWith: JSONSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    prompt: { type: 'string', description: 'The prompt template.' },
    system: { type: 'string', description: 'Text for the instructions.' },
    provider: { type: 'string', description: 'A CLI provider.' },
    endpoint: {
      type: 'object',
      additionalProperties: false,
      description: 'The AI endpoint, the model and the effort.',
      properties: {
        name: {
          type: 'string',
          pattern: '^[a-z][a-z0-9-]{0,63}$',
          description: 'Name of an AI endpoint.',
        },
        model: { type: 'string', description: 'A model of the endpoint.' },
        effort: {
          type: 'string',
          enum: ['low', 'medium', 'high', 'xhigh', 'max'],
        },
      },
    },
    // The DAG tools have a key of their own, beside the tool map.
    dags: { type: 'array' },
    tools: {
      type: 'object',
      additionalProperties: false,
      properties: {
        mcp: { type: 'array' },
        builtin: { type: 'object' },
      },
    },
    skills: { type: 'array', items: { type: 'string' } },
    max_tool_iterations: { type: 'integer', minimum: 1 },
    max_tool_calls: { type: 'integer', minimum: 1, default: 100 },
  },
};

/** A dereferenced DAG schema with the rule of `agent.run` and nothing else. */
export const agentRunSchema: JSONSchema = {
  type: 'object',
  definitions: {
    step: {
      type: 'object',
      additionalProperties: false,
      properties: {
        name: { type: 'string' },
        action: { type: 'string' },
        with: { type: 'object' },
        run: { type: 'string' },
        timeout_sec: { type: 'integer', minimum: 0 },
        secrets: { type: 'array', items: { type: 'string' } },
        untrusted: { type: 'boolean' },
      },
      allOf: [
        {
          if: {
            properties: { action: { const: 'agent.run' } },
            required: ['action'],
          },
          then: { required: ['with'], properties: { with: agentRunWith } },
        },
      ],
    },
  },
};
