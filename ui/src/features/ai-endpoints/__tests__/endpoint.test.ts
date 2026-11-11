// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  allowSummary,
  authOptions,
  buildCreateBody,
  buildUpdateBody,
  describeApiError,
  formForPreset,
  formFromEndpoint,
  keyProblem,
  keyText,
  remainingNames,
  scopeQuery,
  urlProblem,
  validateForm,
  type AIEndpoint,
  type AIEndpointPreset,
  type EndpointForm,
} from '../endpoint';

const anthropic: AIEndpointPreset = {
  id: 'anthropic',
  label: 'Anthropic',
  needsKey: true,
  protocols: [
    {
      protocol: 'anthropic',
      baseUrl: 'https://api.anthropic.com',
      auth: 'x-api-key',
      chatType: 'anthropic',
      consumers: ['claude'],
    },
  ],
} as AIEndpointPreset;

const ollama = { ...anthropic, id: 'ollama', needsKey: false };
const custom: AIEndpointPreset = {
  id: 'custom',
  label: 'Custom',
  needsKey: true,
  protocols: [
    { protocol: 'openai', chatType: 'openai', consumers: ['chat.completion'] },
  ],
} as AIEndpointPreset;

const stored = {
  name: 'cle1',
  preset: 'anthropic',
  protocol: 'anthropic',
  baseUrl: 'https://api.anthropic.com',
  auth: 'x-api-key',
  scope: 'global',
  allow: { workspaces: ['*'], dags: ['fix-bug'] },
  createdAt: '',
  updatedAt: '',
} as AIEndpoint;

const ts = (text: string, params?: Record<string, string | number>) =>
  params
    ? text.replace(/\{(\w+)\}/g, (_, key: string) => String(params[key]))
    : text;

describe('name and url rules', () => {
  it('checks the URL as the server does', () => {
    expect(urlProblem('', false)).toBe('url-required');
    expect(urlProblem('api.example.com', false)).toBe('url-invalid');
    expect(urlProblem('http://api.example.com', false)).toBe('url-scheme');
    expect(urlProblem('http://localhost:11434', true)).toBeNull();
    expect(urlProblem('https://u:p@api.example.com', false)).toBe('url-parts');
    expect(urlProblem('https://api.example.com/v1?x=1', false)).toBe(
      'url-parts'
    );
    expect(urlProblem('https://api.example.com/v1', false)).toBeNull();
  });

  it('checks the name, the URL and the allow lists of a new endpoint', () => {
    const form = {
      ...formForPreset(anthropic, anthropic.protocols[0]!.protocol),
      name: 'Bad',
    };
    const ctx = { creating: true, preset: anthropic, taken: ['cle1'] };
    expect(validateForm(form, ctx)).toEqual({
      name: 'name-pattern',
      allowDags: 'allow-empty',
      allowWorkspaces: 'allow-empty',
    });
    expect(
      validateForm(
        { ...form, name: 'cle1', allowDags: ['*'], allowWorkspaces: ['*'] },
        ctx
      )
    ).toEqual({ name: 'name-exists' });
    expect(
      validateForm(
        { ...form, name: 'cle2', allowDags: ['*'], allowWorkspaces: ['*'] },
        ctx
      )
    ).toEqual({});
  });

  it('accepts http for a local model and for custom only', () => {
    const base = { name: 'a', allowDags: ['*'], allowWorkspaces: ['*'] };
    const local = {
      ...formForPreset(ollama, ollama.protocols[0]!.protocol, base),
      baseUrl: 'http://localhost:11434',
    };
    expect(
      validateForm(local, { creating: true, preset: ollama, taken: [] })
    ).toEqual({});
    const plain = { ...local, preset: 'anthropic' };
    expect(
      validateForm(plain, { creating: true, preset: anthropic, taken: [] })
    ).toEqual({ baseUrl: 'url-scheme' });
  });

  it('does not need allow.workspaces in a workspace scope', () => {
    const form = {
      ...formForPreset(anthropic, anthropic.protocols[0]!.protocol),
      name: 'cle2',
      workspace: 'team',
      allowDags: ['*'],
    };
    expect(
      validateForm(form, { creating: true, preset: anthropic, taken: [] })
    ).toEqual({});
  });

  it('checks the key without keeping it', () => {
    expect(keyProblem('', true)).toBe('key-required');
    expect(keyProblem('', false)).toBeNull();
    expect(keyProblem('sk-a\nb', true)).toBe('key-control');
    expect(keyProblem('sk-test', true)).toBeNull();
  });
});

describe('requests', () => {
  it('builds a create body with no key', () => {
    const form = {
      ...formForPreset(anthropic, anthropic.protocols[0]!.protocol),
      name: 'cle2',
      description: ' Team ',
      allowDags: ['*'],
      allowWorkspaces: ['*'],
    };
    const body = buildCreateBody(form);
    expect(body).toEqual({
      name: 'cle2',
      description: 'Team',
      preset: 'anthropic',
      protocol: 'anthropic',
      baseUrl: 'https://api.anthropic.com',
      auth: 'x-api-key',
      scope: 'global',
      allow: { workspaces: ['*'], dags: ['*'] },
    });
    expect(body).not.toHaveProperty('key');
  });

  it('sends the models URL of custom only', () => {
    const form = {
      ...formForPreset(custom, custom.protocols[0]!.protocol),
      name: 'x',
      baseUrl: 'https://x.test/v1',
      modelsUrl: 'https://x.test/v1/models',
      workspace: 'team',
      allowDags: ['*'],
    };
    const body = buildCreateBody(form);
    expect(body.modelsUrl).toBe('https://x.test/v1/models');
    expect(body.scope).toBe('workspace:team');
    expect(body.allow).toEqual({ dags: ['*'] });
    expect(buildCreateBody({ ...form, preset: 'mistral' })).not.toHaveProperty(
      'modelsUrl'
    );
  });

  it('builds a change body with the changed fields only', () => {
    const form = formFromEndpoint(stored);
    expect(buildUpdateBody(stored, form)).toEqual({});
    expect(
      buildUpdateBody(stored, {
        ...form,
        description: 'New',
        auth: 'bearer' as EndpointForm['auth'],
      })
    ).toEqual({ description: 'New', auth: 'bearer' });
    expect(
      buildUpdateBody(stored, { ...form, allowDags: ['fix-bug', 'review'] })
    ).toEqual({ allow: { workspaces: ['*'], dags: ['fix-bug', 'review'] } });
  });

  it('writes the scope of a request', () => {
    expect(scopeQuery('global')).toBe('global');
    expect(scopeQuery('workspace:team')).toBe('team');
  });

  it('offers anthropic-oauth with the Anthropic protocol only', () => {
    expect(authOptions('anthropic' as never)).toContain('anthropic-oauth');
    expect(authOptions('openai' as never)).not.toContain('anthropic-oauth');
  });
});

describe('lists', () => {
  const text = {
    all: 'All workspaces',
    allDags: 'All DAGs',
    dags: (count: number) => `${count} DAGs`,
    workspaces: (count: number) => `${count} workspaces`,
    join: (parts: string[]) => parts.join(' · '),
  };

  it('summarizes who may use the endpoint', () => {
    expect(allowSummary(stored.allow, 'global', text)).toBe(
      'All workspaces · 1 DAGs'
    );
    expect(
      allowSummary({ workspaces: ['a', 'b'], dags: ['*'] }, 'global', text)
    ).toBe('2 workspaces · All DAGs');
    expect(allowSummary({ dags: ['*'] }, 'workspace:team', text)).toBe(
      'All DAGs'
    );
  });

  // A key is write-only: the list shows at most 6 characters of the prefix
  // and the 12 digits of the fingerprint, whatever the server sends.
  it('shows the prefix and the fingerprint of a key, never more', () => {
    expect(keyText('sk-tes', '0a1b2c3d4e5f')).toBe('sk-tes… · 0a1b2c3d4e5f');
    expect(keyText(undefined, '0a1b2c3d4e5f')).toBe('0a1b2c3d4e5f');
    expect(keyText('', '0a1b2c3d4e5f')).toBe('0a1b2c3d4e5f');
    expect(
      keyText('sk-test-0123456789abcdef', '0a1b2c3d4e5f0a1b2c3d4e5f')
    ).toBe('sk-tes… · 0a1b2c3d4e5f');
  });

  it('suggests the names that are not chosen yet', () => {
    expect(remainingNames(['a', 'ab', 'b'], ['a'], '')).toEqual(['ab', 'b']);
    expect(remainingNames(['a', 'ab', 'b'], [], 'B')).toEqual(['ab', 'b']);
  });
});

describe('describeApiError', () => {
  const fallback = 'The endpoint was not saved.';

  it('maps the status first', () => {
    expect(describeApiError(ts, 403, { message: 'x' }, fallback)).toBe(
      'Only an administrator can manage the AI endpoints.'
    );
    expect(describeApiError(ts, 409, {}, fallback)).toBe(
      'An endpoint with this name exists in this scope.'
    );
    expect(
      describeApiError(
        ts,
        404,
        { message: 'Workspace of the scope not found' },
        fallback
      )
    ).toBe('The workspace of the scope does not exist.');
    expect(
      describeApiError(ts, 404, { message: 'AI endpoint not found' }, fallback)
    ).toBe('The endpoint does not exist. Reload the page.');
  });

  it('maps the messages of the server to sentences', () => {
    const cases: [string, string][] = [
      [
        'invalid AI endpoint: name "X": expected a match of ^[a-z]',
        'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.',
      ],
      [
        'invalid AI endpoint: key is required for the preset "anthropic"',
        'This preset needs a key.',
      ],
      [
        'invalid AI endpoint: key has 9000 bytes, above the limit of 8192',
        'The key is too long. The limit is 8192 bytes.',
      ],
      [
        'invalid AI endpoint: base_url "x": expected the scheme https',
        'The URL must start with https.',
      ],
      [
        'invalid AI endpoint: allow.dags is required',
        'Choose at least one DAG, or all DAGs.',
      ],
    ];
    for (const [message, sentence] of cases) {
      expect(describeApiError(ts, 400, { message }, fallback)).toBe(sentence);
    }
  });

  it('shows an unknown message as the server wrote it, without its prefix', () => {
    expect(
      describeApiError(
        ts,
        400,
        { message: 'invalid AI endpoint: something new' },
        fallback
      )
    ).toBe('something new');
    expect(describeApiError(ts, 500, undefined, fallback)).toBe(fallback);
  });
});
