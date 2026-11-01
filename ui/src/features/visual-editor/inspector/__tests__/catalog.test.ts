// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditorCustomActionHint } from '../../contracts';
import { builtinActionNames } from '../actionRules';
import { buildActionCatalog } from '../catalog';
import { getActionWithSchema } from '../withSchema';
import { schemaSample } from './fixtures';
import { realSchema } from './realSchema';

const custom: EditorCustomActionHint[] = [
  {
    name: 'send-slack',
    description: 'Send a Slack message.',
    inputSchema: {
      type: 'object',
      required: ['channel'],
      properties: { channel: { type: 'string' } },
    },
  },
  { name: 'archive', inputSchema: { type: 'object' } },
  { name: 'http.request', inputSchema: { type: 'object' } },
];

describe('buildActionCatalog', () => {
  const catalog = buildActionCatalog(schemaSample, custom);
  const byName = new Map(catalog.map((entry) => [entry.name, entry]));

  it('lists core, builtin and custom entries in order', () => {
    expect(catalog.map((entry) => entry.name)).toEqual([
      'run',
      'http.request',
      'jq.filter',
      'k8s.run',
      'kubernetes.run',
      'noop',
      'redis.get',
      'redis.set',
      'router.route',
      'archive',
      'send-slack',
    ]);
  });

  it('gives the core run entry', () => {
    expect(byName.get('run')).toEqual({
      name: 'run',
      namespace: 'core',
      title: 'Run command',
      description: 'Run a shell command or script.',
      source: 'core',
      template: { run: 'echo "TODO"' },
    });
  });

  it('describes a builtin action', () => {
    expect(byName.get('http.request')).toEqual({
      name: 'http.request',
      namespace: 'http',
      title: 'HTTP request',
      description: undefined,
      source: 'builtin',
      template: {
        action: 'http.request',
        with: { method: 'GET', url: 'https://example.com' },
      },
    });
    expect(byName.get('noop')?.template).toEqual({ action: 'noop' });
    expect(byName.get('router.route')?.template).toEqual({
      action: 'router.route',
      with: { value: 'ok', routes: {} },
    });
  });

  it('describes a custom action', () => {
    expect(byName.get('send-slack')).toEqual({
      name: 'send-slack',
      namespace: 'custom',
      title: 'Send slack',
      description: 'Send a Slack message.',
      source: 'custom',
      template: { action: 'send-slack', with: { channel: '' } },
    });
    expect(byName.get('archive')?.template).toEqual({
      action: 'archive',
      with: {},
    });
  });

  it('keeps the builtin entry when a custom action takes its name', () => {
    expect(
      catalog.filter((entry) => entry.name === 'http.request')
    ).toHaveLength(1);
    expect(byName.get('http.request')?.source).toBe('builtin');
  });

  describe.skipIf(!realSchema)('with the DAG schema', () => {
    const real = realSchema ? buildActionCatalog(realSchema, []) : [];
    const names = new Set(real.map((entry) => entry.name));

    it('has an entry for every builtin action name', () => {
      for (const name of builtinActionNames(realSchema!)) {
        expect(names.has(name), name).toBe(true);
      }
      expect(names.has('run')).toBe(true);
      expect(names.has('dag.run')).toBe(true);
      expect(names.has('redis.get')).toBe(true);
    });

    it('has a with schema for 54 or more entries', () => {
      const withSchema = real.filter(
        (entry) => getActionWithSchema(realSchema!, entry.name, []) !== null
      );
      expect(withSchema.length).toBeGreaterThanOrEqual(54);
    });

    it('gives each entry a namespace and a title', () => {
      for (const entry of real) {
        expect(entry.namespace, entry.name).not.toBe('');
        expect(entry.title, entry.name).not.toBe('');
      }
    });
  });
});
