// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { AIEndpointProtocol } from '@/api/v1/schema';
import { isCliModelAlias, modelNotListed, modelProblem } from '../modelCheck';

// Spec 093, "Use in a step": these functions mirror the model check that the
// server makes when a step starts.

const ALIASES = ['default', 'best', 'sonnet', 'opus', 'haiku', 'opusplan'];
const MODELS = ['claude-opus-5-5', 'claude-haiku-5'];

const choice = (
  name: string,
  protocol: AIEndpointProtocol,
  models?: string[]
) => ({
  name,
  protocol,
  preset: 'custom',
  scope: 'global',
  ...(models ? { models } : {}),
});

describe('isCliModelAlias', () => {
  it('accepts an alias, in any case, with or without the suffix [1m]', () => {
    for (const name of [
      'sonnet',
      'Opus',
      'opusplan',
      'default',
      'sonnet[1m]',
    ]) {
      expect(isCliModelAlias(name, ALIASES)).toBe(true);
    }
  });

  it('refuses a name that is not an alias', () => {
    for (const name of ['', '[1m]', 'claude-opus-5-5', 'sonnet-4', 'gpt-5']) {
      expect(isCliModelAlias(name, ALIASES)).toBe(false);
    }
  });
});

describe('modelNotListed', () => {
  const base = { models: MODELS, aliases: ALIASES };

  it('is true for a literal that a non-empty list lacks', () => {
    expect(modelNotListed({ ...base, model: 'ghost' })).toBe(true);
  });

  it('is false for a listed model, a reference, an empty model and an empty list', () => {
    expect(modelNotListed({ ...base, model: 'claude-haiku-5' })).toBe(false);
    expect(modelNotListed({ ...base, model: '${params.MODEL}' })).toBe(false);
    expect(modelNotListed({ ...base, model: '' })).toBe(false);
    expect(
      modelNotListed({ models: [], aliases: ALIASES, model: 'ghost' })
    ).toBe(false);
  });

  it('lets the CLI claude use its aliases and a listed name with the suffix [1m]', () => {
    for (const model of [
      'sonnet',
      'opusplan',
      'opus[1m]',
      'claude-haiku-5[1m]',
    ]) {
      expect(modelNotListed({ ...base, model, cliProvider: 'claude' })).toBe(
        false
      );
    }
    // The suffix of a name that the list lacks does not help.
    expect(
      modelNotListed({ ...base, model: 'ghost[1m]', cliProvider: 'claude' })
    ).toBe(true);
  });

  it('knows no alias for a chat step', () => {
    expect(modelNotListed({ ...base, model: 'sonnet' })).toBe(true);
    expect(modelNotListed({ ...base, model: 'claude-haiku-5[1m]' })).toBe(true);
  });

  it('claims nothing before the aliases of the server arrive', () => {
    expect(
      modelNotListed({ models: MODELS, aliases: undefined, model: 'ghost' })
    ).toBe(false);
  });
});

describe('modelProblem', () => {
  const choices = [
    choice('cle1', AIEndpointProtocol.anthropic, MODELS),
    choice('gpt', AIEndpointProtocol.openai, ['gpt-5']),
  ];
  const common = { choices, loaded: true, aliases: ALIASES };

  it('names an endpoint that the server does not know', () => {
    expect(modelProblem({ ...common, endpoint: 'nope', model: 'x' })).toEqual({
      kind: 'endpoint',
      name: 'nope',
    });
  });

  it('names an endpoint of a protocol that the step does not accept', () => {
    expect(
      modelProblem({
        ...common,
        endpoint: 'gpt',
        model: 'gpt-5',
        protocols: ['anthropic'],
      })
    ).toEqual({ kind: 'protocol', name: 'gpt' });
    expect(
      modelProblem({
        ...common,
        endpoint: 'gpt',
        model: 'gpt-5',
        protocols: ['anthropic', 'openai'],
      })
    ).toBeNull();
  });

  it('names a model that the list of a known endpoint lacks', () => {
    expect(
      modelProblem({ ...common, endpoint: 'cle1', model: 'ghost' })
    ).toEqual({
      kind: 'model',
      endpoint: 'cle1',
      model: 'ghost',
    });
    expect(
      modelProblem({ ...common, endpoint: 'cle1', model: 'claude-opus-5-5' })
    ).toBeNull();
  });

  it('claims nothing with no endpoint, or before the choices have arrived', () => {
    expect(
      modelProblem({ ...common, endpoint: '', model: 'ghost' })
    ).toBeNull();
    expect(
      modelProblem({
        ...common,
        loaded: false,
        endpoint: 'nope',
        model: 'ghost',
      })
    ).toBeNull();
  });

  it('claims nothing for an endpoint with no list', () => {
    expect(
      modelProblem({
        ...common,
        choices: [choice('plain', AIEndpointProtocol.anthropic)],
        endpoint: 'plain',
        model: 'ghost',
      })
    ).toBeNull();
  });
});
