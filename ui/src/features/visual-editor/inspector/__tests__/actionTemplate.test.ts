// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import { actionTemplate, initialFieldValue } from '../actionTemplate';
import { applyEdit, parseDagDoc } from '../../doc';
import { buildActionCatalog } from '../catalog';
import { checkStepSchema } from '../stepSchemaCheck';
import { realSchema } from './realSchema';

describe('initialFieldValue', () => {
  it.each<[JSONSchema | undefined, unknown]>([
    [{ type: 'string' }, ''],
    [{ type: 'string', default: 'GET' }, 'GET'],
    [{ type: 'string', enum: ['json', 'text'] }, 'json'],
    [{ type: 'boolean' }, false],
    [{ type: 'object' }, {}],
    [{ type: 'array' }, []],
    [{ type: 'integer' }, undefined],
    [{ type: 'number', default: 3 }, 3],
    [{ type: ['string', 'null'] }, ''],
    [{ oneOf: [{ type: 'object' }, { type: 'string' }] }, {}],
    [{}, ''],
    [undefined, ''],
  ])('%j gives %j', (schema, expected) => {
    expect(initialFieldValue(schema)).toEqual(expected);
  });
});

describe('actionTemplate', () => {
  const withSchema: JSONSchema = {
    type: 'object',
    required: ['method', 'url', 'timeout', 'headers'],
    properties: {
      method: { type: 'string' },
      url: { type: 'string' },
      timeout: { type: 'integer' },
      headers: { type: 'object' },
      body: { type: 'string' },
    },
  };

  it('fills the required fields only and leaves numbers out', () => {
    expect(actionTemplate('http.call', withSchema)).toEqual({
      action: 'http.call',
      with: { method: '', url: '', headers: {} },
    });
  });

  // An empty method or url does not validate, and the schema has no default.
  it('starts an http.request step with a valid request', () => {
    expect(actionTemplate('http.request', withSchema)).toEqual({
      action: 'http.request',
      with: { method: 'GET', url: 'https://example.com' },
    });
  });

  // The provider retires claude-sonnet-4-5, so a new step must not name it.
  it.each(['browser.extract', 'computer.extract', 'xlsx.extract'])(
    'starts a %s step with a model that is still served',
    (action) => {
      expect(actionTemplate(action, null).with?.llm).toEqual({
        provider: 'anthropic',
        model: 'claude-sonnet-5-5',
      });
    }
  );

  it('leaves the starter values out for a custom action', () => {
    expect(actionTemplate('http.request', withSchema, true).with).toEqual({
      method: '',
      url: '',
      headers: {},
    });
  });

  it('leaves with out when no field is required', () => {
    expect(actionTemplate('noop', null)).toEqual({ action: 'noop' });
    expect(actionTemplate('state.list', { type: 'object' })).toEqual({
      action: 'state.list',
    });
  });

  it('keeps an empty with block on request', () => {
    expect(actionTemplate('send-slack', { type: 'object' }, true)).toEqual({
      action: 'send-slack',
      with: {},
    });
  });
});

describe.skipIf(realSchema === null)('the a2a starters', () => {
  const entries = realSchema ? buildActionCatalog(realSchema, []) : [];

  it.each(['a2a.send', 'a2a.get_task'])(
    '%s adds a step that parses and passes the real schema',
    (name) => {
      const entry = entries.find((e) => e.name === name);
      expect(entry).toBeDefined();
      const base = 'steps:\n  - name: first\n    run: echo hi\n';
      const result = applyEdit(base, {
        kind: 'addStep',
        step: structuredClone(entry!.template),
      });
      expect(result.ok).toBe(true);
      if (!result.ok) return;
      const model = parseDagDoc(result.text);
      expect(model.ok).toBe(true);
      const added = model.steps[model.steps.length - 1]!;
      expect(added.value).toMatchObject({ action: name });
      expect(checkStepSchema(realSchema!, added.value)).toEqual([]);
    }
  );
});
