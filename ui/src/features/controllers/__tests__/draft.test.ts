// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import {
  ROUTER_INSTRUCTION,
  createControllerDraft,
  parseControllerYAML,
  serializeControllerDefinition,
  systemSuffix,
  validateControllerDefinition,
  validateSystemPrompt,
  withSystemSuffix,
} from '../draft';
import { validateControllerPrompt } from '../constraints';

describe('Controller draft model', () => {
  it('round-trips a valid ID-less definition', () => {
    const draft = createControllerDraft('ops');
    draft.name = 'Incident router';
    draft.description = 'Route incidents.';
    draft.llm.model = 'gpt-5';
    draft.dags = ['triage'];
    const defaultState = draft.states.default;
    if (!defaultState) throw new Error('Expected default state');
    defaultState.description = 'Inspect the incident.';
    defaultState.dags = ['triage'];

    const source = serializeControllerDefinition(draft);
    const parsed = parseControllerYAML(source);

    expect(parsed.issues).toEqual([]);
    expect(parsed.builderRepresentable).toBe(true);
    expect(parsed.definition).toMatchObject({
      name: 'Incident router',
      labels: ['workspace=ops'],
      dags: ['triage'],
    });
    expect(parsed.definition?.id).toBeUndefined();
  });

  it('accepts omitted descriptions and chat.completion providers', () => {
    const parsed = parseControllerYAML(`
type: controller
version: 1
name: Router
llm:
  provider: openrouter
  model: openai/gpt-5
dags: []
states:
  default:
    terminal: succeeded
`);

    expect(parsed.issues).toEqual([]);
    expect(parsed.builderRepresentable).toBe(true);
    expect(parsed.definition?.description).toBe('');
    expect(parsed.definition?.states.default?.description).toBe('');
  });

  it('rejects unknown fields and missing transition destinations', () => {
    const parsed = parseControllerYAML(`
type: controller
version: 1
name: Router
unknown: true
llm:
  provider: openai
  model: gpt-5
dags: []
states:
  default:
    dags: []
    transitions:
      - to: missing
        when: Always
`);

    expect(parsed.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: 'unknown_field', path: '$.unknown' }),
        expect.objectContaining({
          path: 'states.default.transitions[0].to',
          message: expect.stringContaining('does not exist'),
        }),
      ])
    );
    expect(parsed.builderRepresentable).toBe(false);
  });

  it('checks State names as own keys', () => {
    const draft = createControllerDraft();
    draft.name = 'Router';
    draft.description = 'Route work.';
    draft.llm.model = 'gpt-5';
    draft.states.default = {
      description: 'Choose the next State.',
      dags: [],
      transitions: [{ to: 'constructor', when: 'Work is complete.' }],
    };

    expect(validateControllerDefinition(draft)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: 'states.default.transitions[0].to',
          message: expect.stringContaining('does not exist'),
        }),
      ])
    );

    Object.defineProperty(draft.states, 'constructor', {
      configurable: true,
      enumerable: true,
      value: {
        description: 'Finish the work.',
        dags: [],
        transitions: [],
        terminal: 'succeeded',
      },
      writable: true,
    });
    expect(validateControllerDefinition(draft)).toEqual([]);
  });

  it('preserves special YAML State keys for validation', () => {
    const parsed = parseControllerYAML(`
type: controller
version: 1
name: Router
description: Route work.
llm:
  provider: openai
  model: gpt-5
dags: []
states:
  default:
    description: Finish the work.
    terminal: succeeded
  __proto__:
    description: Invalid State name.
    terminal: failed
`);

    expect(parsed.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: 'states.__proto__',
          message: 'State name is invalid',
        }),
      ])
    );
  });

  it('disables Builder for values that would be rewritten lossily', () => {
    const source = `
type: controller
version: 1
name: Router
description: Route work.
maxTurns: "100"
llm:
  provider: openai
  model: gpt-5
dags: []
states:
  default:
    description: Initial state.
    transitions: []
`;

    const parsed = parseControllerYAML(source);

    expect(parsed.definition).not.toBeNull();
    expect(parsed.builderRepresentable).toBe(false);
    expect(source).toContain('maxTurns: "100"');
  });

  it('enforces the exact reserved system prompt prefix', () => {
    expect(validateSystemPrompt(ROUTER_INSTRUCTION)).toBeNull();
    expect(withSystemSuffix('Use short answers.')).toBe(
      `${ROUTER_INSTRUCTION}\n\nUse short answers.`
    );
    expect(systemSuffix(withSystemSuffix('Use short answers.'))).toBe(
      'Use short answers.'
    );
    expect(validateSystemPrompt(`${ROUTER_INSTRUCTION}\n `)).not.toBeNull();
    expect(validateSystemPrompt(`${ROUTER_INSTRUCTION}\n\n`)).toBe(
      'Custom system instructions cannot contain only whitespace'
    );
    expect(
      validateSystemPrompt(`${ROUTER_INSTRUCTION}\n\n${'${{.Other}}'}`)
    ).not.toBeNull();
  });

  it('enforces the system prompt limit in UTF-8 bytes', () => {
    expect(
      validateSystemPrompt(`${ROUTER_INSTRUCTION}\n\n${'界'.repeat(5_000)}`)
    ).toBeNull();
    expect(
      validateSystemPrompt(`${ROUTER_INSTRUCTION}\n\n${'界'.repeat(6_000)}`)
    ).toBe('System prompt must be 16 KiB or less');
  });

  it('enforces user prompt limits in UTF-8 bytes', () => {
    expect(validateControllerPrompt('   ')).toBe('Enter a prompt.');
    expect(validateControllerPrompt('\u0085')).toBe('Enter a prompt.');
    expect(validateControllerPrompt('界'.repeat(5_000))).toBeNull();
    expect(validateControllerPrompt('界'.repeat(6_000))).toBe(
      'The prompt must be 16 KiB or less.'
    );
  });

  it('enforces create and update ID contracts', () => {
    const draft = createControllerDraft();
    draft.name = 'Router';
    draft.description = 'Route work.';
    draft.llm.model = 'gpt-5';
    const defaultState = draft.states.default;
    if (!defaultState) throw new Error('Expected default state');
    defaultState.description = 'Complete the work.';
    defaultState.terminal = 'succeeded';

    expect(validateControllerDefinition(draft)).toEqual([]);
    expect(validateControllerDefinition(draft, 'update')).toEqual(
      expect.arrayContaining([expect.objectContaining({ path: 'id' })])
    );
    expect(
      parseControllerYAML(serializeControllerDefinition(draft), 'update').issues
    ).toEqual(
      expect.arrayContaining([expect.objectContaining({ path: 'id' })])
    );

    draft.id = 'ctrl_aaaaaaaaaaaaaaaa';
    expect(validateControllerDefinition(draft, 'update')).toEqual([]);
    expect(validateControllerDefinition(draft, 'create')).toEqual(
      expect.arrayContaining([expect.objectContaining({ path: 'id' })])
    );
  });

  it('validates the required Router provider and model locally', () => {
    const draft = createControllerDraft();
    draft.name = 'Router';
    draft.description = 'Route work.';
    draft.llm.provider = '';
    draft.llm.model = '   ';
    const defaultState = draft.states.default;
    if (!defaultState) throw new Error('Expected default state');
    defaultState.description = 'Complete the work.';
    defaultState.terminal = 'succeeded';

    expect(validateControllerDefinition(draft)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: 'llm.provider' }),
        expect.objectContaining({ path: 'llm.model' }),
      ])
    );
  });

  it('validates labels and workspace labels locally', () => {
    const draft = createControllerDraft();
    draft.name = 'Router';
    draft.description = 'Route work.';
    draft.llm.model = 'gpt-5';
    draft.states.default = {
      description: 'Complete the work.',
      dags: [],
      transitions: [],
      terminal: 'succeeded',
    };

    for (const labels of [
      [''],
      ['bad key=value'],
      ['key=bad value'],
      ['workspace'],
      ['workspace=default'],
      ['workspace=ops', 'WORKSPACE=security'],
    ]) {
      draft.labels = labels;
      expect(validateControllerDefinition(draft)).toEqual(
        expect.arrayContaining([expect.objectContaining({ path: 'labels' })])
      );
    }

    draft.labels = [' owner = Platform/Runtime ', 'workspace=ops'];
    expect(validateControllerDefinition(draft)).toEqual([]);
  });

  it('matches server limits and duplicate graph checks', () => {
    const draft = createControllerDraft();
    draft.name = '界'.repeat(101);
    draft.description = 'd'.repeat(4_097);
    draft.llm.model = 'gpt-5';
    draft.dags = ['triage', 'triage', 'invalid.name'];
    draft.states.default = {
      description: 's'.repeat(4_097),
      dags: ['triage', 'triage'],
      transitions: [
        { to: 'done', when: 'Complete.' },
        { to: 'done', when: 'w'.repeat(4_097) },
      ],
    };
    draft.states.done = {
      description: 'Finished.',
      dags: [],
      transitions: [],
      terminal: 'succeeded',
    };

    expect(validateControllerDefinition(draft)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: 'name' }),
        expect.objectContaining({ path: 'description' }),
        expect.objectContaining({ path: 'dags[1]' }),
        expect.objectContaining({ path: 'dags[2]' }),
        expect.objectContaining({ path: 'states.default.description' }),
        expect.objectContaining({ path: 'states.default.dags[1]' }),
        expect.objectContaining({
          path: 'states.default.transitions[1].to',
        }),
        expect.objectContaining({
          path: 'states.default.transitions[1].when',
        }),
      ])
    );
  });

  it.each([
    ['line feed', '\n'],
    ['vertical tab', '\v'],
    ['form feed', '\f'],
    ['carriage return', '\r'],
    ['next line', '\u0085'],
    ['line separator', '\u2028'],
    ['paragraph separator', '\u2029'],
  ])('requires Controller names to be one line (%s)', (_, lineBreak) => {
    const draft = createControllerDraft();
    draft.name = `Incident${lineBreak}router`;
    draft.description = 'Route work.';
    draft.llm.model = 'gpt-5';
    const defaultState = draft.states.default;
    if (!defaultState) throw new Error('Expected default state');
    defaultState.description = 'Complete the work.';
    defaultState.terminal = 'succeeded';

    expect(validateControllerDefinition(draft)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          path: 'name',
          message: 'Name must be one line',
        }),
      ])
    );
  });

  it('reports source fields that cannot be preserved by Builder', () => {
    const parsed = parseControllerYAML(`
type: controller
version: 1
name: Router
description: Route work.
maxTurns: "100"
llm:
  provider: openai
  model: gpt-5
  system: 42
states:
  default:
    description: Complete the work.
    terminal: succeeded
`);

    expect(parsed.issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: 'maxTurns' }),
        expect.objectContaining({ path: 'llm.system' }),
        expect.objectContaining({ path: 'dags' }),
      ])
    );
    expect(parsed.builderRepresentable).toBe(false);
  });
});
