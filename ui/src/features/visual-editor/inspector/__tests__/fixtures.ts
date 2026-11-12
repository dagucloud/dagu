// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { DagDocModel, JSONSchema, StepView } from '../../contracts';
import { yamlHttpJq } from '../../testing/fixtures';

/*
 * Shared inspector test data. `schemaSample` is a small dereferenced DAG
 * schema with the same rule shapes as `schemas/dag.schema.json`; tests that
 * need the full schema read the real file instead.
 */

const httpWith: JSONSchema = {
  allOf: [
    {
      type: 'object',
      additionalProperties: false,
      properties: {
        method: { type: 'string', description: 'HTTP method.' },
        url: { type: 'string', description: 'Request URL.' },
        timeout: { type: 'integer', minimum: 0 },
        headers: {
          type: 'object',
          additionalProperties: { type: 'string' },
          description: 'HTTP headers.',
        },
        body: { type: 'string', description: 'Request body content.' },
        silent: { type: 'boolean' },
        format: { type: 'string', enum: ['json'] },
      },
    },
    { type: 'object', required: ['method', 'url'] },
  ],
  description: 'Configuration for action: http.request.',
};

const jqWith: JSONSchema = {
  type: 'object',
  required: ['filter'],
  additionalProperties: true,
  properties: {
    filter: {
      type: 'string',
      minLength: 1,
      description: 'jq filter expression.',
    },
    data: { description: 'Inline JSON value to provide as jq input.' },
    input: { type: 'string' },
    raw: {
      oneOf: [{ type: 'boolean' }, { type: 'string' }],
      description: 'Raw output, or a reference.',
    },
  },
};

const routerWith: JSONSchema = {
  type: 'object',
  additionalProperties: false,
  required: ['value', 'routes'],
  properties: {
    value: { type: 'string', minLength: 1 },
    routes: {
      type: 'object',
      additionalProperties: {
        type: 'array',
        items: { type: 'string', minLength: 1 },
      },
    },
  },
};

const k8sWith: JSONSchema = {
  type: 'object',
  required: ['image'],
  properties: {
    image: { type: 'string' },
    namespace: { type: 'string' },
    replicas: { type: 'integer' },
    labels: { type: 'object', additionalProperties: { type: 'string' } },
    pod: {
      type: 'object',
      properties: { restart_policy: { type: 'string' } },
    },
  },
};

const redisWith: JSONSchema = {
  type: 'object',
  properties: { key: { type: 'string' }, value: { type: 'string' } },
};

function actionRule(action: JSONSchema, withSchema: JSONSchema): JSONSchema {
  return {
    if: { properties: { action }, required: ['action'] },
    then: { required: ['with'], properties: { with: withSchema } },
  };
}

export const schemaSample: JSONSchema = {
  type: 'object',
  definitions: {
    step: {
      type: 'object',
      additionalProperties: false,
      properties: {
        name: { type: 'string' },
        id: { type: 'string' },
        description: { type: 'string' },
        action: {
          anyOf: [
            {
              type: 'string',
              enum: [
                'http.request',
                'jq.filter',
                'k8s.run',
                'kubernetes.run',
                'noop',
                'router.route',
              ],
              description: 'Builtin action name.',
            },
            { type: 'string', pattern: '^redis\\.[A-Za-z][A-Za-z0-9_-]*$' },
          ],
        },
        with: { type: 'object' },
        run: {
          oneOf: [
            { type: 'string' },
            { type: 'array', items: { type: 'string' } },
          ],
        },
        shell: { type: 'string' },
        depends: {
          oneOf: [
            { type: 'string' },
            { type: 'array', items: { type: 'string' } },
          ],
        },
        timeout_sec: { type: 'integer', minimum: 0 },
        env: { oneOf: [{ type: 'array' }, { type: 'object' }] },
        outputs: { type: 'array', items: { type: 'object' } },
      },
      allOf: [
        { not: { required: ['run', 'action'] } },
        actionRule({ const: 'http.request' }, httpWith),
        actionRule({ const: 'jq.filter' }, jqWith),
        actionRule({ const: 'router.route' }, routerWith),
        actionRule({ enum: ['k8s.run', 'kubernetes.run'] }, k8sWith),
        actionRule({ pattern: '^redis\\.[A-Za-z][A-Za-z0-9_-]*$' }, redisWith),
        {
          if: {
            properties: { action: { const: 'noop' } },
            required: ['action'],
          },
          then: {
            anyOf: [
              { not: { required: ['with'] } },
              { properties: { with: { type: 'object', maxProperties: 0 } } },
            ],
          },
        },
      ],
    },
  },
};

/** Document model of `yamlHttpJq` (ranges follow the rule in testing/fixtures.ts). */
export const modelHttpJq: DagDocModel = {
  text: yamlHttpJq,
  ok: true,
  parseErrors: [],
  documentCount: 1,
  dagType: 'graph',
  stepsForm: 'array',
  steps: [
    {
      key: 'steps[0]',
      index: 0,
      form: 'block',
      explicitName: 'fetch',
      id: 'fetch',
      kind: 'action',
      action: 'http.request',
      depends: [],
      value: {
        id: 'fetch',
        name: 'fetch',
        action: 'http.request',
        with: { method: 'GET', url: '${params.API_URL}' },
      },
      range: {
        from: 80,
        to: 187,
        startLine: 5,
        startColumn: 5,
        endLine: 10,
        endColumn: 29,
      },
    },
    {
      key: 'steps[1]',
      index: 1,
      form: 'block',
      explicitName: 'extract',
      id: 'names',
      kind: 'action',
      action: 'jq.filter',
      depends: ['fetch'],
      value: {
        id: 'names',
        name: 'extract',
        depends: ['fetch'],
        action: 'jq.filter',
        with: { filter: '[.[].name]', data: '${steps.fetch.outputs.body}' },
      },
      range: {
        from: 193,
        to: 340,
        startLine: 12,
        startColumn: 5,
        endLine: 18,
        endColumn: 40,
      },
    },
  ],
  handlers: [],
};

export const httpStep: StepView = modelHttpJq.steps[0]!;
export const jqStep: StepView = modelHttpJq.steps[1]!;

/** An http.request step with headers and a key that the schema does not know. */
export const httpStepWithHeaders: StepView = {
  ...httpStep,
  value: {
    ...httpStep.value,
    with: {
      method: 'GET',
      url: 'https://example.com',
      headers: { X: 'a', Y: 'b' },
      legacy_key: 'kept',
    },
  },
};

/** A run step with a multi-line script and flow settings. */
export const runStep: StepView = {
  key: 'steps[2]',
  index: 2,
  form: 'block',
  explicitName: 'build',
  kind: 'run',
  runPreview: 'make build',
  depends: ['fetch', 'extract'],
  value: {
    name: 'build',
    depends: ['fetch', 'extract'],
    run: 'make build\nmake test\n',
    timeout_sec: 60,
    env: [{ GOFLAGS: '-mod=mod' }],
    outputs: [{ name: 'artifact' }],
  },
  range: {
    from: 0,
    to: 0,
    startLine: 1,
    startColumn: 1,
    endLine: 1,
    endColumn: 1,
  },
};

export const routerStep: StepView = {
  key: 'steps[3]',
  index: 3,
  form: 'block',
  explicitName: 'route',
  kind: 'router',
  action: 'router.route',
  depends: [],
  value: {
    name: 'route',
    action: 'router.route',
    with: {
      value: '${CODE}',
      routes: { '200': ['ok'], 're:^5': ['retry', 'alert'] },
    },
  },
  range: {
    from: 0,
    to: 0,
    startLine: 1,
    startColumn: 1,
    endLine: 1,
    endColumn: 1,
  },
};
