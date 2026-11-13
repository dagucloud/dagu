// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { allowSummary, keyText } from '../endpoint';
import {
  buildMcpCreateBody,
  buildMcpUpdateBody,
  credentialsOf,
  credentialsUpdatedAt,
  describeMcpApiError,
  emptyMcpForm,
  formFromServer,
  headerNameProblems,
  mcpProblemText,
  mcpUrlProblem,
  urlHost,
  validateMcpForm,
  valueProblem,
  valueTooLong,
  type AIMCPServer,
  type McpServerForm,
} from '../mcpServer';

const server: AIMCPServer = {
  name: 'docs',
  url: 'https://docs.example.com/mcp',
  scope: 'global',
  allow: { workspaces: ['*'], dags: ['fix-bug'] },
  token: {
    keyPrefix: 'tok-01',
    keyFingerprint: '0a1b2c3d4e5f',
    keyUpdatedAt: '2026-11-04T10:00:00Z',
  },
  headers: [
    {
      name: 'x-team',
      keyFingerprint: 'bbbbbbbbbbbb',
      keyUpdatedAt: '2026-11-04T12:00:00Z',
    },
  ],
  createdAt: '2026-11-04T10:00:00Z',
  updatedAt: '2026-11-04T10:00:00Z',
};

const ts = (text: string) => text;

describe('mcpUrlProblem', () => {
  it('accepts https and http, with a path and a query', () => {
    expect(mcpUrlProblem('https://docs.example.com/mcp')).toBeNull();
    expect(mcpUrlProblem('http://localhost:9000/mcp?workspace=a')).toBeNull();
  });

  it('refuses the cases that the server refuses', () => {
    expect(mcpUrlProblem('')).toBe('url-required');
    expect(mcpUrlProblem('  ')).toBe('url-required');
    expect(mcpUrlProblem('docs.example.com')).toBe('url-invalid');
    expect(mcpUrlProblem('ftp://docs.example.com')).toBe('url-scheme');
    expect(mcpUrlProblem('https://user:pw@docs.example.com')).toBe('url-parts');
    expect(mcpUrlProblem('https://docs.example.com/mcp#frag')).toBe(
      'url-parts'
    );
    expect(mcpUrlProblem('https://docs.example.com/mcp#')).toBe('url-parts');
  });
});

describe('validateMcpForm', () => {
  const valid: McpServerForm = {
    ...emptyMcpForm(),
    name: 'fresh',
    url: 'https://x.example.com/mcp',
    allowWorkspaces: ['*'],
    allowDags: ['*'],
  };

  it('accepts a valid form', () => {
    expect(validateMcpForm(valid, { creating: true, taken: [] })).toEqual({});
  });

  it('checks the name on a create only', () => {
    expect(
      validateMcpForm({ ...valid, name: 'Bad' }, { creating: true, taken: [] })
    ).toEqual({ name: 'name-pattern' });
    expect(
      validateMcpForm({ ...valid, name: 'dagu' }, { creating: true, taken: [] })
    ).toEqual({ name: 'name-reserved' });
    expect(
      validateMcpForm(valid, { creating: true, taken: ['fresh'] })
    ).toEqual({ name: 'name-exists' });
    expect(
      validateMcpForm({ ...valid, name: '' }, { creating: false, taken: [] })
    ).toEqual({});
    // The limit of 64 characters.
    expect(
      validateMcpForm(
        { ...valid, name: `a${'b'.repeat(64)}` },
        { creating: true, taken: [] }
      )
    ).toEqual({ name: 'name-pattern' });
  });

  it('needs the lists of the use rule: workspaces for the global scope only', () => {
    const empty = { ...valid, allowWorkspaces: [], allowDags: [] };
    expect(validateMcpForm(empty, { creating: true, taken: [] })).toEqual({
      allowWorkspaces: 'allow-empty',
      allowDags: 'allow-empty',
    });
    expect(
      validateMcpForm(
        { ...empty, workspace: 'team' },
        { creating: true, taken: [] }
      )
    ).toEqual({ allowDags: 'allow-empty' });
  });
});

describe('headerNameProblems', () => {
  it('checks the names of the headers as the server does', () => {
    const rows = [
      { id: 1, name: 'X-Api-Key' },
      { id: 2, name: 'x-api-key' },
      { id: 3, name: 'Content-Type' },
      { id: 4, name: 'X_Bad' },
      { id: 5, name: '' },
      { id: 6, name: 'Authorization' },
    ];
    const problems = headerNameProblems(rows, 'headers');
    expect(problems.get(1)).toBeUndefined();
    expect(problems.get(2)).toBe('header-twice');
    expect(problems.get(3)).toBe('header-reserved');
    expect(problems.get(4)).toBe('header-name');
    expect(problems.get(5)).toBe('header-name');
    expect(problems.get(6)).toBeUndefined();
    expect(headerNameProblems(rows, 'token').size).toBe(0);
  });
});

describe('values', () => {
  it('flags a control character and a value above 8192 bytes', () => {
    expect(valueProblem('ok-value')).toBeNull();
    expect(valueProblem('bad\nvalue')).toBe('value-control');
    expect(valueTooLong('a'.repeat(8192))).toBe(false);
    expect(valueTooLong('a'.repeat(8193))).toBe(true);
    // The server counts bytes, not characters.
    expect(valueTooLong('é'.repeat(4097))).toBe(true);
  });
});

describe('request bodies', () => {
  it('builds the body of a create, with no credential', () => {
    const body = buildMcpCreateBody({
      ...emptyMcpForm(),
      name: 'fresh',
      url: ' https://x.example.com/mcp ',
      allowWorkspaces: ['*'],
      allowDags: ['a'],
    });
    expect(body).toEqual({
      name: 'fresh',
      url: 'https://x.example.com/mcp',
      scope: 'global',
      allow: { workspaces: ['*'], dags: ['a'] },
    });
    expect(body).not.toHaveProperty('token');
    expect(body).not.toHaveProperty('headers');
    const scoped = buildMcpCreateBody({
      ...emptyMcpForm(),
      name: 'fresh',
      url: 'https://x.example.com/mcp',
      workspace: 'team',
      allowDags: ['a'],
    });
    expect(scoped.scope).toBe('workspace:team');
    expect(scoped.allow).toEqual({ dags: ['a'] });
  });

  it('sends the fields that differ in a change', () => {
    const form = formFromServer(server);
    expect(buildMcpUpdateBody(server, form)).toEqual({});
    expect(
      buildMcpUpdateBody(server, { ...form, url: 'https://other.example.com' })
    ).toEqual({ url: 'https://other.example.com' });
    expect(
      buildMcpUpdateBody(server, { ...form, allowDags: ['fix-bug', 'review'] })
    ).toEqual({ allow: { workspaces: ['*'], dags: ['fix-bug', 'review'] } });
  });
});

describe('what the list shows', () => {
  it('shows the host only', () => {
    expect(urlHost('https://docs.example.com/mcp?token=x')).toBe(
      'docs.example.com'
    );
    expect(urlHost('not a url')).toBe('not a url');
  });

  it('gives the token and the headers with their display values', () => {
    expect(credentialsOf(server)).toEqual([
      { label: 'token', prefix: 'tok-01', fingerprint: '0a1b2c3d4e5f' },
      { label: 'x-team', prefix: undefined, fingerprint: 'bbbbbbbbbbbb' },
    ]);
    expect(
      credentialsOf(server).map((item) =>
        keyText(item.prefix, item.fingerprint)
      )
    ).toEqual(['tok-01… · 0a1b2c3d4e5f', 'bbbbbbbbbbbb']);
    expect(credentialsUpdatedAt(server)).toBe('2026-11-04T12:00:00Z');
    expect(
      credentialsUpdatedAt({ ...server, token: undefined, headers: [] })
    ).toBe(undefined);
  });

  // A MCP server entry has the allow block of an endpoint: one summary serves both.
  it('says who may use the entry', () => {
    const text = {
      all: 'All workspaces',
      allDags: 'All DAGs',
      dags: (count: number) => `${count} DAGs`,
      workspaces: (count: number) => `${count} workspaces`,
      join: (parts: string[]) => parts.join(' · '),
    };
    expect(allowSummary(server.allow, 'global', text)).toBe(
      'All workspaces · 1 DAGs'
    );
    expect(allowSummary({ dags: ['*'] }, 'workspace:team', text)).toBe(
      'All DAGs'
    );
  });
});

describe('describeMcpApiError', () => {
  it('maps the status and the messages of the server to sentences', () => {
    const describe = (status: number, message: string, code?: string) =>
      describeMcpApiError(
        ts,
        status,
        { message, ...(code ? { code } : {}) },
        'fallback'
      );
    expect(describe(403, 'x')).toBe(
      'Only an administrator can manage the MCP servers.'
    );
    expect(describe(409, 'x', 'already_exists')).toBe(
      'A MCP server with this name exists in this scope.'
    );
    expect(describe(404, 'Workspace of the scope not found')).toBe(
      'The workspace of the scope does not exist.'
    );
    expect(describe(404, 'MCP server entry not found')).toBe(
      'The MCP server does not exist. Reload the page.'
    );
    expect(describe(503, 'x')).toBe('The server does not store MCP servers.');
    expect(
      describe(
        400,
        'invalid AI endpoint: a token and a header authorization contradict each other; the token is the header authorization'
      )
    ).toBe('A token is the header authorization. Remove one of them.');
    expect(
      describe(
        400,
        'invalid AI endpoint: headers has 17 items, above the limit of 16'
      )
    ).toBe('An entry has at most 16 headers.');
    expect(describe(400, 'invalid AI endpoint: something new')).toBe(
      'something new'
    );
    expect(describeMcpApiError(ts, 500, undefined, 'fallback')).toBe(
      'fallback'
    );
  });

  it('has a sentence for each problem of the form', () => {
    for (const problem of [
      'name-pattern',
      'name-reserved',
      'name-exists',
      'url-required',
      'url-invalid',
      'url-scheme',
      'url-parts',
      'allow-empty',
      'token-required',
      'value-control',
      'value-long',
      'headers-empty',
      'headers-many',
      'header-name',
      'header-reserved',
      'header-twice',
      'header-value-required',
    ] as const) {
      expect(mcpProblemText(ts, problem).length).toBeGreaterThan(5);
    }
  });
});
