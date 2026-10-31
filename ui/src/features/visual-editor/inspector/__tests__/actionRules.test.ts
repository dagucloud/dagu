// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import {
  builtinActionNames,
  readActionRules,
  resolveSchemaRef,
  ruleMatches,
} from '../actionRules';
import { schemaSample } from './fixtures';
import { realSchema } from './realSchema';

describe('readActionRules', () => {
  const rules = readActionRules(schemaSample);

  it('reads const, enum and pattern rules and skips the others', () => {
    expect(rules.map((r) => r.names)).toEqual([
      ['http.request'],
      ['jq.filter'],
      ['router.route'],
      ['k8s.run', 'kubernetes.run'],
      [],
      ['noop'],
    ]);
    expect(rules[4]?.pattern?.source).toBe('^redis\\.[A-Za-z][A-Za-z0-9_-]*$');
  });

  it('keeps the with schema of a rule when it has one', () => {
    expect(rules[0]?.withSchema?.description).toBe(
      'Configuration for action: http.request.'
    );
    expect(rules[5]?.withSchema).toBeUndefined();
  });

  it.each([
    ['http.request', 0, true],
    ['http.request', 1, false],
    ['kubernetes.run', 3, true],
    ['redis.get', 4, true],
    ['redis', 4, false],
  ])('matches %s against rule %i: %s', (action, index, expected) => {
    expect(ruleMatches(rules[index]!, action)).toBe(expected);
  });

  it('marks a rule whose if tests other fields as conditional', () => {
    expect(rules.every((rule) => !rule.conditional)).toBe(true);
    const [rule] = readActionRules({
      definitions: {
        step: {
          allOf: [
            {
              if: {
                properties: {
                  action: { const: 'x.sheet' },
                  with: { required: ['op'] },
                },
                required: ['action', 'with'],
              },
              then: { properties: { with: { required: ['to'] } } },
            },
          ],
        },
      },
    });
    expect(rule?.conditional).toBe(true);
  });

  it('returns no rules for a schema without a step definition', () => {
    expect(readActionRules({ type: 'object' })).toEqual([]);
  });
});

describe('resolveSchemaRef', () => {
  const root: JSONSchema = {
    definitions: {
      a: { type: 'string', description: 'A' },
      loop: { $ref: '#/definitions/loop' },
    },
  };

  it('resolves a local reference and keeps sibling keys', () => {
    expect(
      resolveSchemaRef(root, { $ref: '#/definitions/a', description: 'B' })
    ).toEqual({
      type: 'string',
      description: 'B',
    });
  });

  it('stops on a cycle or a missing target', () => {
    expect(resolveSchemaRef(root, { $ref: '#/definitions/loop' })).toEqual({
      $ref: '#/definitions/loop',
    });
    expect(resolveSchemaRef(root, { $ref: '#/definitions/none' })).toEqual({
      $ref: '#/definitions/none',
    });
  });
});

describe('builtinActionNames', () => {
  it('reads the enum members of the action property', () => {
    expect(builtinActionNames(schemaSample)).toEqual([
      'http.request',
      'jq.filter',
      'k8s.run',
      'kubernetes.run',
      'noop',
      'router.route',
    ]);
  });

  it.skipIf(!realSchema)('reads the builtin names of the DAG schema', () => {
    const names = builtinActionNames(realSchema!);
    expect(names.length).toBeGreaterThanOrEqual(77);
    expect(names).toContain('http.request');
    expect(names).toContain('git.worktree.add');
  });

  it.skipIf(!realSchema)('reads the action rules of the DAG schema', () => {
    const rules = readActionRules(realSchema!);
    expect(rules.length).toBeGreaterThanOrEqual(53);
    expect(rules.some((r) => r.pattern && ruleMatches(r, 'redis.get'))).toBe(
      true
    );
  });
});
