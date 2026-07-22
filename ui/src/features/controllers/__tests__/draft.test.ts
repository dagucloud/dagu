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
          path: 'states.default.transitions',
          message: expect.stringContaining('does not exist'),
        }),
      ])
    );
    expect(parsed.builderRepresentable).toBe(false);
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

  it('requires an immutable ID only for persisted definitions', () => {
    const draft = createControllerDraft();
    draft.name = 'Router';
    draft.description = 'Route work.';
    draft.llm.model = 'gpt-5';
    const defaultState = draft.states.default;
    if (!defaultState) throw new Error('Expected default state');
    defaultState.description = 'Complete the work.';
    defaultState.terminal = 'succeeded';

    expect(validateControllerDefinition(draft)).toEqual([]);
    expect(validateControllerDefinition(draft, { requireId: true })).toEqual(
      expect.arrayContaining([expect.objectContaining({ path: 'id' })])
    );
  });
});
