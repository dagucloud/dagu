// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readAgentRunInfo, readChatInfo } from '../chatModel';
import {
  endpointEdits,
  endpointProtocols,
  modelEdits,
  type EndpointLists,
} from '../modelEdits';
import type { ModelEditData } from '../chatModel';

// The rows that the Model node edits, and the edits that it makes (Spec 093,
// "The UI contract"). The edits are the ones of the form of the step.

describe('the rows of the Model node of an agent step', () => {
  it('has one row with the endpoint, the model and the effort of the block, and the CLI', () => {
    expect(
      readAgentRunInfo({
        with: {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'claude-haiku-5', effort: 'high' },
        },
      }).edit
    ).toEqual({
      kind: 'agent',
      cli: 'claude',
      shape: 'block',
      effort: 'high',
      rows: [
        {
          index: null,
          name: 'claude-haiku-5',
          endpoint: 'cle1',
          provider: '',
        },
      ],
    });
  });

  it('has an empty row for a step with the provider only', () => {
    expect(readAgentRunInfo({ with: { provider: 'claude' } }).edit).toEqual({
      kind: 'agent',
      cli: 'claude',
      shape: 'none',
      rows: [{ index: null, name: '', endpoint: '', provider: '' }],
    });
  });

  it('reads a block with no name, and a model that is a reference', () => {
    const edit = readAgentRunInfo({
      with: { provider: 'claude', endpoint: { model: '${MODEL}' } },
    }).edit;
    expect(edit?.shape).toBe('block');
    expect(edit?.rows[0]).toMatchObject({ endpoint: '', name: '${MODEL}' });
  });

  // A text endpoint is a value of the wrong type: only the YAML edits it.
  // `model` and `effort` beside the block are unknown keys of the step.
  it('has no row for a text endpoint, and ignores model and effort beside the block', () => {
    expect(
      readAgentRunInfo({
        with: {
          provider: 'claude',
          endpoint: 'cle1',
          model: 'claude-haiku-5',
          effort: 'low',
        },
      }).edit
    ).toBeUndefined();
    expect(
      readAgentRunInfo({
        with: {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'm' },
          model: 'other',
          effort: 'low',
        },
      }).edit
    ).toEqual({
      kind: 'agent',
      cli: 'claude',
      shape: 'block',
      rows: [{ index: null, name: 'm', endpoint: 'cle1', provider: '' }],
    });
  });

  it('reads the model of the block for the label of the node', () => {
    expect(
      readAgentRunInfo({
        with: { provider: 'claude', endpoint: { name: 'cle1', model: 'm' } },
      }).models
    ).toEqual([{ name: 'm', provider: 'claude', endpoint: 'cle1' }]);
  });

  it('has no row for a value that only the YAML edits, and never throws', () => {
    expect(
      readAgentRunInfo({
        with: { provider: 'claude', endpoint: { name: 'a', model: { a: 1 } } },
      }).edit
    ).toBeUndefined();
    expect(
      readAgentRunInfo({ with: { provider: 'claude', endpoint: 3 } }).edit
    ).toBeUndefined();
    expect(
      readAgentRunInfo({ with: { provider: 'claude', endpoint: ['a'] } }).edit
    ).toBeUndefined();
    expect(
      readAgentRunInfo({
        with: { provider: 'claude', endpoint: { name: 3, model: [] } },
      }).models
    ).toEqual([{ name: 'claude' }]);
  });
});

describe('the rows of the Model node of a chat step', () => {
  it('has one row for the single form, with the provider of the old method', () => {
    expect(
      readChatInfo({ with: { provider: 'openai', model: 'gpt-5' } }).edit
    ).toEqual({
      kind: 'chat',
      cli: '',
      shape: 'legacy',
      oldKeys: ['provider', 'model'],
      rows: [{ index: null, name: 'gpt-5', endpoint: '', provider: 'openai' }],
    });
  });

  it('reads the block with.endpoint as an agent step does', () => {
    const block = readChatInfo({
      with: {
        endpoint: { name: 'cle1', model: 'claude-haiku-5', effort: 'high' },
      },
    }).edit;
    expect(block).toEqual({
      kind: 'chat',
      cli: '',
      shape: 'block',
      effort: 'high',
      rows: [
        {
          index: null,
          name: 'claude-haiku-5',
          endpoint: 'cle1',
          provider: '',
        },
      ],
    });
    // The block of a chat step and of an agent step give the same rows.
    expect(block?.rows).toEqual(
      readAgentRunInfo({
        with: {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'claude-haiku-5' },
        },
      }).edit?.rows
    );
    expect(
      readChatInfo({ with: { endpoint: { model: '${MODEL}' } } }).edit
    ).toMatchObject({
      shape: 'block',
      rows: [{ endpoint: '', name: '${MODEL}' }],
    });
  });

  it('has the shape none for an empty step, and legacy for any key of the old method', () => {
    expect(readChatInfo({ with: { prompt: 'hi' } }).edit).toMatchObject({
      shape: 'none',
      oldKeys: [],
      rows: [{ index: null, name: '', endpoint: '' }],
    });
    expect(
      readChatInfo({
        with: {
          provider: 'local',
          base_url: 'http://localhost:8080',
          api_key_name: 'KEY',
          model: 'm',
          thinking: { enabled: true, effort: 'high' },
        },
      }).edit
    ).toMatchObject({
      shape: 'legacy',
      oldKeys: ['provider', 'base_url', 'api_key_name', 'model', 'thinking'],
      effort: 'high',
    });
    // A thinking that is off has no effort to move.
    expect(
      readChatInfo({
        with: { model: 'm', thinking: { enabled: false, effort: 'high' } },
      }).edit?.effort
    ).toBeUndefined();
  });

  it('has no edit data for an endpoint that is not a map, or a value that is not text', () => {
    expect(
      readChatInfo({ with: { endpoint: 'cle1', model: 'm' } }).edit
    ).toBeUndefined();
    expect(readChatInfo({ with: { endpoint: ['a'] } }).edit).toBeUndefined();
    expect(
      readChatInfo({ with: { endpoint: { name: 'a', model: 3 } } }).edit
    ).toBeUndefined();
    // The block hides a flat model: the server refuses it.
    expect(
      readChatInfo({ with: { endpoint: { name: 'a' }, model: 'x' } }).edit
        ?.rows[0]?.name
    ).toBe('');
  });

  it('has a row for each entry of a list, with its place', () => {
    expect(
      readChatInfo({
        with: {
          model: [
            { name: 'claude-haiku-5', endpoint: 'cle1' },
            { name: 'old', provider: 'openai' },
            { name: 'mine' },
          ],
        },
      }).edit?.rows
    ).toEqual([
      { index: 0, name: 'claude-haiku-5', endpoint: 'cle1', provider: '' },
      { index: 1, name: 'old', endpoint: '', provider: 'openai' },
      { index: 2, name: 'mine', endpoint: '', provider: '' },
    ]);
  });

  // An entry of a list is a map: the server refuses a bare name.
  it('has no row for a list that holds a value that is no map', () => {
    expect(
      readChatInfo({ with: { model: ['a', { name: 'b' }] } }).edit
    ).toBeUndefined();
    expect(
      readChatInfo({ with: { model: [{ name: 'a' }, 3] } }).edit
    ).toBeUndefined();
    expect(
      readChatInfo({ with: { model: { name: 'a' } } }).edit
    ).toBeUndefined();
  });
});

describe('the edits of the Model node', () => {
  const row = (over: Partial<ReturnType<typeof rowOf>> = {}) => ({
    ...rowOf(),
    ...over,
  });
  function rowOf() {
    return {
      index: null as number | null,
      name: '',
      endpoint: '',
      provider: '',
    };
  }
  const set = (path: (string | number)[], value: unknown) => ({
    kind: 'setStepField',
    step: 'steps[2]',
    path,
    value,
  });

  const chat: ModelEditData = { kind: 'chat', cli: '', rows: [] };
  const lists: EndpointLists = { choices: [], aliases: undefined };
  const endpoint = (r: ReturnType<typeof rowOf>, value: string | undefined) =>
    endpointEdits('steps[2]', chat, r, value, lists);

  it('writes the keys of the entry for a list', () => {
    const mapRow = row({ index: 1, name: 'a' });
    expect(endpoint(mapRow, 'cle1')).toEqual([
      set(['with', 'model', 1, 'endpoint'], 'cle1'),
    ]);
    expect(endpoint(mapRow, undefined)).toEqual([
      set(['with', 'model', 1, 'endpoint'], undefined),
    ]);
    expect(modelEdits('steps[2]', chat, mapRow, undefined)).toEqual([
      set(['with', 'model', 1, 'name'], ''),
    ]);
  });

  it('offers the protocol anthropic to an agent step, and both to a chat step', () => {
    expect(
      endpointProtocols({ kind: 'agent', cli: 'claude', rows: [] })
    ).toEqual(['anthropic']);
    expect(endpointProtocols({ kind: 'chat', cli: '', rows: [] })).toEqual([
      'anthropic',
      'openai',
    ]);
  });
});
