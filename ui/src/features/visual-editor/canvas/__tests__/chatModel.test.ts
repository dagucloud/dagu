// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readAgentRunInfo, readChatInfo } from '../chatModel';

describe('readChatInfo', () => {
  it('reads one model and its provider', () => {
    expect(
      readChatInfo({ with: { provider: 'openai', model: 'gpt-4o' } }).models
    ).toEqual([{ name: 'gpt-4o', provider: 'openai' }]);
  });

  it('has no provider when the step names none', () => {
    expect(readChatInfo({ with: { model: 'local' } }).models).toEqual([
      { name: 'local' },
    ]);
  });

  it('reads a list of names and of maps, in order', () => {
    const models = readChatInfo({
      with: {
        provider: 'openai',
        model: ['a', { provider: 'anthropic', name: 'b' }, { name: 'c' }],
      },
    }).models;
    expect(models).toEqual([
      { name: 'a', provider: 'openai' },
      { name: 'b', provider: 'anthropic' },
      { name: 'c', provider: 'openai' },
    ]);
  });

  it('gives no model for a missing, empty or wrong value', () => {
    expect(readChatInfo({}).models).toEqual([]);
    expect(readChatInfo({ with: 'x' }).models).toEqual([]);
    expect(readChatInfo({ with: { provider: 'openai' } }).models).toEqual([]);
    expect(readChatInfo({ with: { model: ' ' } }).models).toEqual([]);
    expect(readChatInfo({ with: { model: [1, null, {}] } }).models).toEqual([]);
  });
});

// Spec 094: a chat step names its endpoint in the block `with.endpoint`, as an
// agent step does. A model of a list can name its own endpoint.
describe('readChatInfo with an endpoint', () => {
  it('reads the model of the block with its endpoint', () => {
    expect(
      readChatInfo({
        with: {
          endpoint: { name: 'cle1', model: 'claude-haiku-4-5', effort: 'low' },
        },
      }).models
    ).toEqual([{ name: 'claude-haiku-4-5', endpoint: 'cle1' }]);
    expect(
      readChatInfo({ with: { endpoint: { name: 'cle1' } } }).models
    ).toEqual([]);
  });

  it('reads the endpoint of each model of a list', () => {
    expect(
      readChatInfo({
        with: {
          model: [
            'a',
            { name: 'c', endpoint: 'cle2' },
            { name: 'd', provider: 'openai' },
          ],
        },
      }).models
    ).toEqual([
      { name: 'a' },
      { name: 'c', endpoint: 'cle2' },
      { name: 'd', provider: 'openai' },
    ]);
  });

  it('gives no endpoint for a value that is not a block', () => {
    expect(readChatInfo({ with: { endpoint: 3, model: 'm' } }).models).toEqual([
      { name: 'm' },
    ]);
  });
});

describe('readAgentRunInfo', () => {
  it('reads the model of the block with its provider', () => {
    expect(
      readAgentRunInfo({
        with: { provider: 'claude', endpoint: { model: 'claude-haiku-4-5' } },
      }).models
    ).toEqual([{ name: 'claude-haiku-4-5', provider: 'claude' }]);
  });

  it('reads the endpoint of the block with the model', () => {
    expect(
      readAgentRunInfo({
        with: {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'sonnet' },
        },
      }).models
    ).toEqual([{ name: 'sonnet', provider: 'claude', endpoint: 'cle1' }]);
    // A step with no model names the provider, and the endpoint stays.
    expect(
      readAgentRunInfo({
        with: { provider: 'claude', endpoint: { name: 'cle1' } },
      }).models
    ).toEqual([{ name: 'claude', endpoint: 'cle1' }]);
    // An endpoint that is not a block gives no endpoint.
    for (const endpoint of [3, 'cle1']) {
      expect(
        readAgentRunInfo({ with: { provider: 'claude', endpoint } }).models
      ).toEqual([{ name: 'claude' }]);
    }
  });

  // `model` and `effort` are not keys of the `with` of an agent step: the
  // canvas reads neither of them.
  it('reads no model and no effort beside the block', () => {
    const info = readAgentRunInfo({
      with: { provider: 'claude', model: 'sonnet', effort: 'low' },
    });
    expect(info.models).toEqual([{ name: 'claude' }]);
    expect(info.edit).toMatchObject({ shape: 'none', rows: [{ name: '' }] });
    expect(info.edit?.effort).toBeUndefined();
  });

  it('names the provider when the step names no model', () => {
    expect(readAgentRunInfo({ with: { provider: 'claude' } }).models).toEqual([
      { name: 'claude' },
    ]);
  });

  it('names the model alone when the step names no provider', () => {
    expect(
      readAgentRunInfo({ with: { endpoint: { model: 'm' } } }).models
    ).toEqual([{ name: 'm' }]);
  });

  it('keeps a reference as the name and gives no model for a step that names nothing', () => {
    expect(
      readAgentRunInfo({
        with: { endpoint: { model: '${MODEL}' }, provider: 'claude' },
      }).models[0]?.name
    ).toBe('${MODEL}');
    expect(readAgentRunInfo({}).models).toEqual([]);
    expect(readAgentRunInfo({ with: 'x' }).models).toEqual([]);
    expect(readAgentRunInfo({ with: { prompt: 'x' } }).models).toEqual([]);
  });

  it('reads the names of with.skills with their place in the list', () => {
    expect(readAgentRunInfo({ with: { skills: ['a', 'b'] } }).skills).toEqual([
      { name: 'a', index: 0 },
      { name: 'b', index: 1 },
    ]);
  });

  it('reads the limits, and the default of the calls when the step sets none', () => {
    expect(
      readAgentRunInfo({
        with: { max_tool_iterations: 20, max_tool_calls: 7 },
      }).limits
    ).toEqual({ turns: 20, calls: 7 });
    expect(readAgentRunInfo({ with: {} }).limits).toEqual({
      calls: 100,
      callsDefault: true,
    });
    expect(
      readAgentRunInfo({ with: { max_tool_iterations: '${TURNS}' } }).limits
    ).toEqual({ turns: '${TURNS}', calls: 100, callsDefault: true });
    expect(
      readAgentRunInfo({ with: { max_tool_iterations: 'x' } }).limits?.turns
    ).toBeUndefined();
  });
});

describe('readChatInfo limits', () => {
  it('reads the turns of a chat step and has no default of calls', () => {
    expect(
      readChatInfo({ with: { model: 'm', max_tool_iterations: 5 } }).limits
    ).toEqual({ turns: 5 });
    expect(readChatInfo({ with: { model: 'm' } }).limits).toBeUndefined();
  });
});
