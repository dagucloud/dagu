// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import type { ToolSelection } from '../contracts';
import { parseDagDoc } from '../doc';
import { ToolPanel } from '../ToolPanel';
import { entryTooltip } from '../tools/kinds';
import { readStepTools } from '../tools/parse';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

// A MCP entry with no url names a server entry of the AI page (Spec 095).
const NAMED = `steps:
  - id: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      tools:
        mcp:
          - name: docs
            allow: [search]
`;

function serve(servers: string[] | null) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (path: string) =>
      path === '/ai/mcp-server-choices' && servers !== null
        ? {
            data: {
              choices: servers.map((name) => ({ name, scope: 'global' })),
            },
          }
        : {}
  );
}

afterEach(() => vi.clearAllMocks());

const select: ToolSelection = { step: 'steps[0]', category: 'mcp', index: 0 };

function setup(text: string, servers: string[] | null) {
  serve(servers);
  const onEdit = vi.fn();
  const model = parseDagDoc(text);
  render(
    <ToolPanel
      model={model}
      step={model.steps[0]!}
      stepLabel="code"
      tool={select}
      issues={[]}
      candidates={[]}
      readOnly={false}
      dag={{ name: 'fix-bug', workspace: 'team' }}
      onEdit={onEdit}
      onBack={vi.fn()}
      onClose={vi.fn()}
    />
  );
  return { onEdit, model };
}

function sectionTitles(): string[] {
  return screen
    .getAllByRole('button')
    .filter((button) => button.hasAttribute('aria-controls'))
    .map((button) => button.querySelector('span')?.textContent ?? '');
}

describe('a MCP entry with no url', () => {
  it('is read as a named entry, and an entry with a url is not', () => {
    const named = readStepTools(
      (parseDagDoc(NAMED).steps[0]?.value ?? {}) as Record<string, unknown>
    );
    expect(named.entries[0]?.mcp?.named).toBe(true);
    expect(named.entries[0]?.mcp?.url).toBe('');
    const inline = readStepTools(
      (parseDagDoc(
        NAMED.replace(
          '- name: docs',
          '- name: docs\n            url: https://x.example.com'
        )
      ).steps[0]?.value ?? {}) as Record<string, unknown>
    );
    expect(inline.entries[0]?.mcp?.named).toBe(false);
    // A new inline entry has an empty url: it is not named.
    const fresh = readStepTools({
      with: { tools: { mcp: [{ name: '', url: '', allow: [] }] } },
    });
    expect(fresh.entries[0]?.mcp?.named).toBe(false);
  });

  it('has no Connection section: the URL and the credentials stay in the coordinator', () => {
    setup(NAMED, ['docs', 'wiki']);
    expect(sectionTitles()).toEqual(['General', 'Allowed tools', 'YAML']);
    expect(screen.queryByLabelText('Server URL')).toBeNull();
    expect(
      screen.getByText(
        /The coordinator calls this server with the credentials of the entry/
      )
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: /Open the MCP servers/ })
    ).toHaveAttribute('href', expect.stringContaining('/ai/mcp'));
  });

  it('chooses the server entry from the entries that the DAG may use', () => {
    const { onEdit } = setup(NAMED, ['docs', 'wiki']);
    const field = screen.getByLabelText('MCP server');
    expect(field).toHaveValue('docs');
    fireEvent.change(field, { target: { value: 'wiki' } });
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepTools',
      step: 'steps[0]',
      change: {
        kind: 'update',
        category: 'mcp',
        index: 0,
        path: ['name'],
        value: 'wiki',
      },
    });
  });

  it('warns about a name that the DAG may not use, and keeps it as a value', () => {
    setup(NAMED, ['wiki']);
    expect(screen.getByLabelText('MCP server')).toHaveValue('docs');
    expect(
      screen.getByText(
        'The server does not know the MCP server "docs", or this DAG may not use it. The step fails when it starts.'
      )
    ).toBeInTheDocument();
  });

  it('shows the name as text while the list has not arrived', () => {
    setup(NAMED, null);
    expect(screen.queryByLabelText('MCP server')).toBeNull();
    expect(screen.getAllByLabelText('Name')[0]).toHaveValue('docs');
    expect(
      screen.queryByText(/The server does not know the MCP server/)
    ).toBeNull();
  });

  it('shows the server entry in the tooltip, and the host for an inline server', () => {
    const ts = (text: string, params?: Record<string, string | number>) =>
      Object.entries(params ?? {}).reduce(
        (out, [key, value]) => out.replace(`{${key}}`, String(value)),
        text
      );
    const [named] = readStepTools(
      (parseDagDoc(NAMED).steps[0]?.value ?? {}) as Record<string, unknown>
    ).entries;
    expect(entryTooltip(ts, named!)).toEqual([
      'MCP server',
      'MCP server entry: docs',
    ]);
    const [inline] = readStepTools({
      with: {
        tools: {
          mcp: [
            { name: 'docs', url: 'https://mcp.example.com/x', allow: ['a'] },
          ],
        },
      },
    }).entries;
    expect(entryTooltip(ts, inline!)).toEqual([
      'MCP server',
      'Server: mcp.example.com',
    ]);
  });
});
