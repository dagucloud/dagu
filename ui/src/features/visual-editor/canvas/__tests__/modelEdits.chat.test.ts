// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { AIEndpointChoice } from '@/features/ai-endpoints/useEndpointChoices';
import { readChatInfo, type ModelEditData } from '../chatModel';
import {
  effortEdits,
  endpointEdits,
  hasEffort,
  modelEdits,
  modelPickEdits,
  type EndpointLists,
} from '../modelEdits';
import { applyEdit } from '../../doc';

// The edits of the Model node and of the form for a chat step: the block
// `with.endpoint` of the new method, the keys of the old method, and the rows
// of a list.

const STEP = 'steps[0]';

function editOf(withBlock: Record<string, unknown>): ModelEditData {
  const edit = readChatInfo({
    action: 'chat.completion',
    with: withBlock,
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
    protocol: 'openai',
    ...(models ? { models } : {}),
    ...(modelInfo ? { modelInfo } : {}),
  } as AIEndpointChoice;
}

function applyOk(text: string, op: Parameters<typeof applyEdit>[1]): string {
  const result = applyEdit(text, op);
  if (!result.ok) throw new Error(result.message);
  return result.text;
}

const infos = [
  { id: 'big', efforts: ['low', 'medium', 'high'] },
  { id: 'small', efforts: ['low'] },
];
const lists: EndpointLists = {
  choices: [
    choice('other', ['big', 'small'], infos),
    choice('narrow', ['small'], infos),
    choice('unlisted', undefined),
  ],
  aliases: ['sonnet'],
};

describe('the sub-fields of the block with.endpoint of a chat step', () => {
  it('writes each sub-field under with.endpoint, and removes it with undefined', () => {
    const edit = editOf({
      endpoint: { name: 'a', model: 'big', effort: 'low' },
    });
    const row = edit.rows[0]!;
    expect(hasEffort(edit)).toBe(true);
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
    expect(endpointEdits(STEP, edit, row, 'other', lists)).toEqual([
      set(['with', 'endpoint', 'name'], 'other'),
    ]);
    expect(endpointEdits(STEP, edit, row, undefined, lists)).toEqual([
      set(['with', 'endpoint', 'name'], undefined),
    ]);
  });

  it('creates the block of an empty step, and keeps the other keys', () => {
    const edit = editOf({ prompt: 'hi' });
    const row = edit.rows[0]!;
    expect(edit.shape).toBe('none');
    expect(hasEffort(edit)).toBe(true);
    let doc = [
      'steps:',
      '  - name: a',
      '    action: chat.completion',
      '    with:',
      '      prompt: hi',
      '',
    ].join('\n');
    for (const op of endpointEdits(STEP, edit, row, 'cle1', lists)) {
      doc = applyOk(doc, op);
    }
    for (const op of modelEdits(STEP, edit, row, 'm')) doc = applyOk(doc, op);
    for (const op of effortEdits(STEP, edit, 'high')) doc = applyOk(doc, op);
    expect(doc).toContain(
      'endpoint:\n        name: cle1\n        model: m\n        effort: high'
    );
    expect(doc).toContain('prompt: hi');
    // Clearing the model and the effort leaves the name.
    for (const op of effortEdits(STEP, edit, undefined)) {
      doc = applyOk(doc, op);
    }
    for (const op of modelEdits(STEP, edit, row, undefined)) {
      doc = applyOk(doc, op);
    }
    expect(doc).toContain('name: cle1');
    expect(doc).not.toContain('effort');
    expect(doc).not.toContain('model: m');
  });

  it('removes a model that the new endpoint does not list, and an effort that it does not support', () => {
    const change = (
      block: Record<string, unknown>,
      value: string | undefined
    ) => {
      const edit = editOf({ endpoint: block });
      return endpointEdits(STEP, edit, edit.rows[0]!, value, lists);
    };
    expect(change({ name: 'a', model: 'big' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'model'], undefined),
    ]);
    expect(
      change({ name: 'a', model: 'small', effort: 'high' }, 'narrow')
    ).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
    // The effort belongs to the model: it goes with the model that is not listed.
    expect(
      change({ name: 'a', model: 'big', effort: 'high' }, 'narrow')
    ).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'model'], undefined),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
    // An alias of the CLI is no model of a chat step.
    expect(change({ name: 'a', model: 'sonnet' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'endpoint', 'model'], undefined),
    ]);
    // A reference, and an endpoint with no list, keep the model.
    expect(change({ name: 'a', model: '${MODEL}' }, 'narrow')).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
    ]);
    expect(change({ name: 'a', model: 'big' }, 'unlisted')).toEqual([
      set(['with', 'endpoint', 'name'], 'unlisted'),
    ]);
  });

  // Nothing tells that the effort of a model with no known levels is wrong:
  // the change of the endpoint keeps it.
  it('keeps the effort of a reference, and of a model of an endpoint with no list', () => {
    const change = (block: Record<string, unknown>, value: string) => {
      const edit = editOf({ endpoint: block });
      return endpointEdits(STEP, edit, edit.rows[0]!, value, lists);
    };
    expect(
      change({ name: 'a', model: '${MODEL}', effort: 'high' }, 'narrow')
    ).toEqual([set(['with', 'endpoint', 'name'], 'narrow')]);
    expect(
      change({ name: 'a', model: 'big', effort: 'high' }, 'unlisted')
    ).toEqual([set(['with', 'endpoint', 'name'], 'unlisted')]);
  });

  it('removes the effort that a new model does not support, in the same edit', () => {
    const edit = editOf({
      endpoint: { name: 'a', model: 'big', effort: 'high' },
    });
    expect(
      modelPickEdits(STEP, edit, edit.rows[0]!, 'small', infos[1])
    ).toEqual([
      set(['with', 'endpoint', 'model'], 'small'),
      set(['with', 'endpoint', 'effort'], undefined),
    ]);
    expect(modelPickEdits(STEP, edit, edit.rows[0]!, 'big', infos[0])).toEqual([
      set(['with', 'endpoint', 'model'], 'big'),
    ]);
  });
});

describe('the first endpoint of a chat step of the old method', () => {
  const old = {
    provider: 'local',
    base_url: 'http://localhost:8080',
    api_key_name: 'KEY',
    model: 'big',
    thinking: { enabled: true, effort: 'high', budget_tokens: 1024 },
  };

  it('converts the step in one edit: the endpoint, the model and the effort move, the old keys go', () => {
    const edit = editOf(old);
    expect(hasEffort(edit)).toBe(false);
    expect(endpointEdits(STEP, edit, edit.rows[0]!, 'other', lists)).toEqual([
      set(['with', 'endpoint', 'name'], 'other'),
      set(['with', 'endpoint', 'model'], 'big'),
      set(['with', 'endpoint', 'effort'], 'high'),
      set(['with', 'provider'], undefined),
      set(['with', 'base_url'], undefined),
      set(['with', 'api_key_name'], undefined),
      set(['with', 'model'], undefined),
      set(['with', 'thinking'], undefined),
    ]);
  });

  // As in the block form, the effort goes with the model: the levels belong to it.
  it('drops the model that the endpoint does not list, and the effort with it', () => {
    const edit = editOf(old);
    expect(endpointEdits(STEP, edit, edit.rows[0]!, 'narrow', lists)).toEqual([
      set(['with', 'endpoint', 'name'], 'narrow'),
      set(['with', 'provider'], undefined),
      set(['with', 'base_url'], undefined),
      set(['with', 'api_key_name'], undefined),
      set(['with', 'model'], undefined),
      set(['with', 'thinking'], undefined),
    ]);
    const small = editOf({ ...old, model: 'small' });
    expect(endpointEdits(STEP, small, small.rows[0]!, 'narrow', lists)).toEqual(
      [
        set(['with', 'endpoint', 'name'], 'narrow'),
        set(['with', 'endpoint', 'model'], 'small'),
        set(['with', 'provider'], undefined),
        set(['with', 'base_url'], undefined),
        set(['with', 'api_key_name'], undefined),
        set(['with', 'model'], undefined),
        set(['with', 'thinking'], undefined),
      ]
    );
  });

  it('removes only the keys that the step holds', () => {
    const edit = editOf({ provider: 'openai', model: 'big' });
    expect(endpointEdits(STEP, edit, edit.rows[0]!, 'other', lists)).toEqual([
      set(['with', 'endpoint', 'name'], 'other'),
      set(['with', 'endpoint', 'model'], 'big'),
      set(['with', 'provider'], undefined),
      set(['with', 'model'], undefined),
    ]);
    const none = editOf({ base_url: 'http://x.test' });
    expect(endpointEdits(STEP, none, none.rows[0]!, 'other', lists)).toEqual([
      set(['with', 'endpoint', 'name'], 'other'),
      set(['with', 'base_url'], undefined),
    ]);
  });

  it('turns the keys of the file into the block, and keeps the others', () => {
    const edit = editOf(old);
    let doc = [
      'steps:',
      '  - name: a',
      '    action: chat.completion',
      '    with:',
      '      provider: local',
      '      base_url: http://localhost:8080',
      '      api_key_name: KEY',
      '      model: big',
      '      thinking:',
      '        enabled: true',
      '        effort: high',
      '        budget_tokens: 1024',
      '      prompt: hi',
      '',
    ].join('\n');
    for (const op of endpointEdits(STEP, edit, edit.rows[0]!, 'other', lists)) {
      doc = applyOk(doc, op);
    }
    expect(doc).toBe(
      [
        'steps:',
        '  - name: a',
        '    action: chat.completion',
        '    with:',
        '      prompt: hi',
        '      endpoint:',
        '        name: other',
        '        model: big',
        '        effort: high',
        '',
      ].join('\n')
    );
  });

  it('does nothing when the endpoint goes, and keeps the text of the model for the old method', () => {
    const edit = editOf(old);
    expect(endpointEdits(STEP, edit, edit.rows[0]!, undefined, lists)).toEqual(
      []
    );
    expect(modelEdits(STEP, edit, edit.rows[0]!, 'x')).toEqual([
      set(['with', 'model'], 'x'),
    ]);
    expect(
      modelPickEdits(STEP, edit, edit.rows[0]!, 'x', { id: 'x', efforts: [] })
    ).toEqual([set(['with', 'model'], 'x')]);
    // The old method has no effort control: no edit.
    expect(effortEdits(STEP, edit, 'low')).toEqual([]);
  });
});

describe('the rows of a list of models', () => {
  const row = (name: string, extra: Record<string, unknown> = {}) => ({
    name,
    ...extra,
  });

  it('writes the keys of the entry', () => {
    const edit = editOf({
      model: [
        row('a', { endpoint: 'x' }),
        row('b'),
        row('c', { provider: 'openai' }),
      ],
    });
    expect(hasEffort(edit)).toBe(false);
    const [first, plain, old] = edit.rows as [
      (typeof edit.rows)[number],
      (typeof edit.rows)[number],
      (typeof edit.rows)[number],
    ];
    expect(endpointEdits(STEP, edit, first, 'other', lists)).toEqual([
      set(['with', 'model', 0, 'endpoint'], 'other'),
    ]);
    expect(endpointEdits(STEP, edit, plain, 'other', lists)).toEqual([
      set(['with', 'model', 1, 'endpoint'], 'other'),
    ]);
    expect(endpointEdits(STEP, edit, old, 'other', lists)).toEqual([
      set(['with', 'model', 2, 'endpoint'], 'other'),
      set(['with', 'model', 2, 'provider'], undefined),
    ]);
    expect(modelEdits(STEP, edit, first, 'z')).toEqual([
      set(['with', 'model', 0, 'name'], 'z'),
    ]);
    expect(effortEdits(STEP, edit, 'low')).toEqual([]);
  });
});
