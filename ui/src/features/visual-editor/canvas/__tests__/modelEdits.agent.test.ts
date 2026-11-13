// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { AIEndpointChoice } from '@/features/ai-endpoints/useEndpointChoices';
import { readAgentRunInfo, type ModelEditData } from '../chatModel';
import {
  effortEdits,
  endpointEdits,
  modelEdits,
  modelPickEdits,
  type EndpointLists,
} from '../modelEdits';
import { applyEdit } from '../../doc';

// The edits of the Model node and of the form for an agent step: the block
// `with.endpoint` with `name`, `model` and `effort`.

const STEP = 'steps[0]';

function editOf(withBlock: Record<string, unknown>): ModelEditData {
  const edit = readAgentRunInfo({
    with: { provider: 'claude', ...withBlock },
  }).edit;
  if (!edit) throw new Error('the step has no edit data');
  return edit;
}

const set = (path: (string | number)[], value: unknown) => ({
  kind: 'setStepField',
  step: STEP,
  path,
  value,
});

function choice(
  name: string,
  models: string[] | undefined,
  modelInfo?: { id: string; efforts?: string[] }[]
): AIEndpointChoice {
  return {
    name,
    protocol: 'anthropic',
    ...(models ? { models } : {}),
    ...(modelInfo ? { modelInfo } : {}),
  } as AIEndpointChoice;
}

describe('the sub-fields of the block with.endpoint', () => {
  it('writes each sub-field under with.endpoint, and removes it with undefined', () => {
    const edit = editOf({ endpoint: { name: 'a', model: 'm', effort: 'low' } });
    const row = edit.rows[0]!;
    expect(modelEdits(STEP, edit, row, 'm2')).toEqual([
      set(['with', 'endpoint', 'model'], 'm2'),
    ]);
    expect(modelEdits(STEP, edit, row, undefined)).toEqual([
      set(['with', 'endpoint', 'model'], undefined),
    ]);
    expect(effortEdits(STEP, edit, 'high')).toEqual([
      set(['with', 'endpoint', 'effort'], 'high'),
    ]);
    expect(effortEdits(STEP, edit, undefined)).toEqual([
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('creates the block when the step has none, and keeps the other keys', () => {
    const edit = editOf({});
    const row = edit.rows[0]!;
    const text = [
      'steps:',
      '  - name: a',
      '    action: agent.run',
      '    with:',
      '      provider: claude',
      '      prompt: hi',
      '',
    ].join('\n');
    let doc = text;
    for (const op of endpointEdits(STEP, edit, row, 'cle1', {
      choices: [],
      aliases: undefined,
    })) {
      doc = applyOk(doc, op);
    }
    for (const op of modelEdits(STEP, edit, row, 'm')) doc = applyOk(doc, op);
    for (const op of effortEdits(STEP, edit, 'high')) doc = applyOk(doc, op);
    expect(doc).toContain(
      'endpoint:\n        name: cle1\n        model: m\n        effort: high'
    );
    expect(doc).toContain('prompt: hi');
    // Clearing the model and the effort leaves the name.
    const edit2 = editOf({ endpoint: { name: 'cle1', model: 'm' } });
    for (const op of modelEdits(STEP, edit2, edit2.rows[0]!, undefined)) {
      doc = applyOk(doc, op);
    }
    expect(doc).not.toContain('model: m');
    expect(doc).toContain('name: cle1');
  });
});

function applyOk(text: string, op: Parameters<typeof applyEdit>[1]): string {
  const result = applyEdit(text, op);
  if (!result.ok) throw new Error(result.message);
  return result.text;
}

// `model` and `effort` beside the block are unknown keys of an agent step:
// the edits write the block only, and leave those keys to the YAML.
describe('a step with model and effort beside the block', () => {
  it('writes the keys of the block only', () => {
    const edit = editOf({
      endpoint: { name: 'cle1', model: 'm' },
      model: 'other',
      effort: 'low',
    });
    const row = edit.rows[0]!;
    expect(edit.effort).toBeUndefined();
    expect(modelEdits(STEP, edit, row, 'm2')).toEqual([
      set(['with', 'endpoint', 'model'], 'm2'),
    ]);
    expect(effortEdits(STEP, edit, 'high')).toEqual([
      set(['with', 'endpoint', 'effort'], 'high'),
    ]);
  });
});

describe('the change of the endpoint of an agent step', () => {
  const infos = [
    { id: 'big', efforts: ['low', 'medium', 'high', 'xhigh'] },
    { id: 'small', efforts: ['low'] },
    { id: 'plain' },
  ];
  const lists: EndpointLists = {
    choices: [
      choice('other', ['big', 'small', 'plain'], infos),
      choice('narrow', ['small'], infos),
      choice('unlisted', undefined),
      choice('empty', []),
    ],
    aliases: ['sonnet', 'opus'],
  };
  const change = (
    block: Record<string, unknown>,
    value: string | undefined,
    over: Partial<EndpointLists> = {}
  ) => {
    const edit = editOf({ endpoint: block });
    return endpointEdits(STEP, edit, edit.rows[0]!, value, {
      ...lists,
      ...over,
    });
  };

  it('removes a model that the new endpoint does not list, in the same edit', () => {
    expect(change({ name: 'a', model: 'big' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'model'], undefined),
    ]);
  });

  // The levels of effort belong to the model: no model, no effort.
  it('removes the effort when the model goes', () => {
    expect(
      change({ name: 'a', model: 'big', effort: 'xhigh' }, 'narrow')
    ).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'model'], undefined),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('keeps a model that the new endpoint lists, and removes an effort that it does not support', () => {
    expect(change({ name: 'a', model: 'small' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
    ]);
    expect(
      change({ name: 'a', model: 'small', effort: 'high' }, 'narrow')
    ).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
    expect(
      change({ name: 'a', model: 'big', effort: 'high' }, 'other')
    ).toEqual([set(['with', 'endpoint', 'name'], 'other')]);
  });

  it('keeps a reference, an alias of the CLI and a model of a list with no info', () => {
    expect(change({ name: 'a', model: '${MODEL}' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
    ]);
    expect(change({ name: 'a', model: 'sonnet' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
    ]);
    expect(change({ name: 'a', model: 'opus[1m]' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
    ]);
    // A model with no info takes every level: the effort stays.
    expect(
      change({ name: 'a', model: 'plain', effort: 'max' }, 'other')
    ).toEqual([set(['with', 'endpoint', 'name'], 'other')]);
  });

  it('keeps the model when the new list is unknown', () => {
    for (const next of ['unlisted', 'empty', 'not-in-the-choices']) {
      expect(change({ name: 'a', model: 'big', effort: 'max' }, next)).toEqual([
        set(['with', 'endpoint', 'name'], next),
      ]);
    }
    // A server that makes no model check sends no aliases.
    expect(
      change({ name: 'a', model: 'big' }, 'narrow', { aliases: undefined })
    ).toEqual([set(['with', 'endpoint', 'name'], 'narrow')]);
  });

  it('removes only the name when the endpoint goes', () => {
    expect(change({ name: 'a', model: 'big' }, undefined)).toEqual([
      set(['with', 'endpoint', 'name'], undefined),
    ]);
  });
});

describe('a pick in the Model picker of an agent step', () => {
  it('removes the effort that the new model does not support, in the same edit', () => {
    const edit = editOf({
      endpoint: { name: 'a', model: 'big', effort: 'max' },
    });
    const pick = (name: string, efforts?: string[]) =>
      modelPickEdits(STEP, edit, edit.rows[0]!, name, {
        id: name,
        ...(efforts ? { efforts } : {}),
      });
    expect(pick('small', ['low'])).toEqual([
      set(['with', 'endpoint', 'model'], 'small'),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
    expect(pick('big', ['low', 'max'])).toEqual([
      set(['with', 'endpoint', 'model'], 'big'),
    ]);
    // A model with no known levels, such as an alias, keeps the effort.
    expect(pick('opus')).toEqual([set(['with', 'endpoint', 'model'], 'opus')]);
  });
});
