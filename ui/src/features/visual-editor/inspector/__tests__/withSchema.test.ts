// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditorCustomActionHint } from '../../contracts';
import { builtinActionNames } from '../actionRules';
import {
  getActionWithSchema,
  isLargeActionSchema,
  reduceToEssentialFields,
} from '../withSchema';
import { schemaSample } from './fixtures';
import { realSchema } from './realSchema';

const custom: EditorCustomActionHint[] = [
  {
    name: 'send-slack',
    inputSchema: {
      type: 'object',
      required: ['channel'],
      properties: { channel: { type: 'string' }, text: { type: 'string' } },
    },
  },
  {
    name: 'http.request',
    inputSchema: { type: 'object', properties: { x: {} } },
  },
];

describe('getActionWithSchema', () => {
  it('flattens the http.request rule', () => {
    const schema = getActionWithSchema(schemaSample, 'http.request', []);
    expect(schema?.type).toBe('object');
    expect(Object.keys(schema?.properties ?? {})).toEqual([
      'method',
      'url',
      'timeout',
      'headers',
      'body',
      'silent',
      'format',
    ]);
    expect(schema?.required).toEqual(['method', 'url']);
    expect(schema?.additionalProperties).toBe(false);
    expect(schema?.allOf).toBeUndefined();
  });

  it.each([
    ['jq.filter', ['filter']],
    ['router.route', ['value', 'routes']],
    ['kubernetes.run', ['image']],
    ['redis.get', undefined],
  ])('resolves %s', (action, required) => {
    const schema = getActionWithSchema(schemaSample, action, []);
    expect(schema).not.toBeNull();
    expect(schema?.required).toEqual(required);
  });

  it.each(['run', '', 'noop', 'unknown.action', 'redis'])(
    'gives null for %j',
    (action) => {
      expect(getActionWithSchema(schemaSample, action, [])).toBeNull();
    }
  );

  it('gives the input schema of a custom action', () => {
    expect(getActionWithSchema(schemaSample, 'send-slack', custom)).toEqual({
      type: 'object',
      required: ['channel'],
      properties: { channel: { type: 'string' }, text: { type: 'string' } },
    });
  });

  // The server reads a ref of a custom input schema inside that schema.
  it('resolves the refs of a custom action in its input schema', () => {
    const page: EditorCustomActionHint = {
      name: 'fetch-page',
      inputSchema: {
        type: 'object',
        allOf: [{ $ref: '#/$defs/fields' }],
        $defs: {
          fields: {
            required: ['url'],
            properties: { url: { type: 'string' } },
          },
        },
      },
    };
    const schema = getActionWithSchema(schemaSample, 'fetch-page', [page]);
    expect(schema?.required).toEqual(['url']);
    expect(schema?.properties).toEqual({ url: { type: 'string' } });
  });

  it('prefers the builtin rule over a custom action of the same name', () => {
    expect(
      getActionWithSchema(schemaSample, 'http.request', custom)?.properties?.url
    ).toBeDefined();
  });

  describe.skipIf(!realSchema)('with the DAG schema', () => {
    const names = realSchema ? builtinActionNames(realSchema) : [];

    it('gives a with schema for 54 or more builtin actions', () => {
      const withSchema = names.filter(
        (n) => getActionWithSchema(realSchema!, n, []) !== null
      );
      expect(withSchema.length).toBeGreaterThanOrEqual(54);
    });

    it('requires method and url for http.request', () => {
      const schema = getActionWithSchema(realSchema!, 'http.request', []);
      expect(schema?.properties?.url).toBeDefined();
      expect(schema?.properties?.method).toBeDefined();
      expect(schema?.required).toEqual(
        expect.arrayContaining(['method', 'url'])
      );
    });

    it('does not require the keys of a conditional rule', () => {
      const schema = getActionWithSchema(realSchema!, 'xlsx.sheet', []);
      expect(schema?.required ?? []).not.toContain('to');
    });

    it('resolves any redis operation', () => {
      expect(
        getActionWithSchema(realSchema!, 'redis.hset', [])?.properties?.key
      ).toBeDefined();
    });
  });
});

describe('isLargeActionSchema', () => {
  it.each([
    ['k8s.run', true],
    ['kubernetes.run', true],
    ['browser.run', true],
    ['browser.extract', true],
    ['computer.run', true],
    ['http.request', false],
    ['browser', false],
    ['k8s.runner', false],
  ])('%s gives %s', (action, expected) => {
    expect(isLargeActionSchema(action)).toBe(expected);
  });
});

describe('reduceToEssentialFields', () => {
  it('keeps required and scalar properties only', () => {
    const k8s = getActionWithSchema(schemaSample, 'k8s.run', [])!;
    const reduced = reduceToEssentialFields(k8s);
    expect(Object.keys(reduced.properties ?? {})).toEqual([
      'image',
      'namespace',
      'replicas',
    ]);
    expect(reduced.required).toEqual(['image']);
  });

  it('keeps enums, scalar unions and required objects', () => {
    const reduced = reduceToEssentialFields({
      type: 'object',
      required: ['spec'],
      properties: {
        mode: { enum: ['a', 'b'] },
        limit: { oneOf: [{ type: 'integer' }, { type: 'string' }] },
        spec: { type: 'object' },
        list: { type: 'array' },
        any: {},
      },
    });
    expect(Object.keys(reduced.properties ?? {})).toEqual([
      'mode',
      'limit',
      'spec',
    ]);
  });

  it.skipIf(!realSchema)('reduces the k8s.run schema of the DAG schema', () => {
    const full = getActionWithSchema(realSchema!, 'k8s.run', [])!;
    const reduced = reduceToEssentialFields(full);
    const count = (s: typeof full) => Object.keys(s.properties ?? {}).length;
    expect(count(reduced)).toBeGreaterThan(0);
    expect(count(reduced)).toBeLessThan(count(full));
  });
});
