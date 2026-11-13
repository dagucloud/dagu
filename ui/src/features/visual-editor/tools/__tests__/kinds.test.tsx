// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { translateStatic } from '@/i18n/translateStatic';
import { KindBadge, KindIcon } from '../KindBadge';
import {
  SCOPE_KINDS,
  TOOL_KINDS,
  VALID_KINDS,
  builtinTooltip,
  entryIcon,
  entryTooltip,
  kindBadge,
  kindClasses,
  kindGroupTitle,
  kindIcon,
  kindLabel,
  kindAvailability,
  kindOfCategory,
  scopeOfKind,
  stepAccepts,
} from '../kinds';
import { BUILTIN_TOOLS } from '../model';
import { readStepTools } from '../parse';

const ts = (text: string, values?: Record<string, string | number>) =>
  translateStatic('en', text, values);

function entry(category: 'dags' | 'mcp', raw: unknown) {
  return readStepTools({
    with: category === 'dags' ? { dags: [raw] } : { tools: { mcp: [raw] } },
  }).entries[0]!;
}

describe('kinds of tools', () => {
  it('have three kinds, each with its own icon, label, badge, group title and colour', () => {
    expect(TOOL_KINDS).toEqual(['dag', 'mcp', 'builtin']);
    const distinct = (values: unknown[]) =>
      expect(new Set(values).size).toBe(TOOL_KINDS.length);
    distinct(TOOL_KINDS.map(kindIcon));
    distinct(TOOL_KINDS.map((kind) => kindLabel(ts, kind)));
    distinct(TOOL_KINDS.map((kind) => kindBadge(ts, kind)));
    distinct(TOOL_KINDS.map((kind) => kindGroupTitle(ts, kind)));
    distinct(TOOL_KINDS.map((kind) => kindClasses(kind).text));
  });

  it('say the kind in a short badge and a longer label', () => {
    expect(TOOL_KINDS.map((kind) => kindBadge(ts, kind))).toEqual([
      'DAG',
      'MCP',
      'Built-in',
    ]);
    expect(TOOL_KINDS.map((kind) => kindLabel(ts, kind))).toEqual([
      'DAG tool',
      'MCP server',
      'Built-in tool',
    ]);
  });

  // The DAGs dock lists the DAG tools, the Tools dock the others: no kind is in both.
  it('share between the two docks, each kind in one dock', () => {
    expect(SCOPE_KINDS).toEqual({ dags: ['dag'], tools: ['mcp', 'builtin'] });
    expect([...SCOPE_KINDS.dags, ...SCOPE_KINDS.tools].sort()).toEqual(
      [...TOOL_KINDS].sort()
    );
    for (const kind of TOOL_KINDS) {
      expect(SCOPE_KINDS[scopeOfKind(kind)]).toContain(kind);
    }
  });

  it('map each category of the tool map to a kind', () => {
    expect(kindOfCategory('dags')).toBe('dag');
    expect(kindOfCategory('mcp')).toBe('mcp');
    expect(kindOfCategory('builtin')).toBe('builtin');
  });

  it('use the alert icon for an invalid entry only', () => {
    expect(entryIcon('dag')).toBe(kindIcon('dag'));
    expect(entryIcon('mcp')).toBe(kindIcon('mcp'));
    expect(TOOL_KINDS.map(kindIcon)).not.toContain(entryIcon('invalid'));
  });
});

describe('tooltips', () => {
  it('name the kind and the DAG or the host of the server', () => {
    expect(entryTooltip(ts, entry('dags', { name: 'refund' }))).toEqual([
      'DAG tool',
      'DAG: refund',
    ]);
    expect(
      entryTooltip(
        ts,
        entry('mcp', { name: 'docs', url: 'https://mcp.example.com:8443/x' })
      )
    ).toEqual(['MCP server', 'Server: mcp.example.com:8443']);
  });

  it('list the permissions of the shell tool', () => {
    expect(
      builtinTooltip(ts, {
        name: 'shell',
        allow: ['git status'],
        deny: ['git push *'],
      })
    ).toEqual([
      'Built-in tool',
      'Shell commands',
      'Allow: git status',
      'Deny: git push *',
    ]);
    expect(builtinTooltip(ts, { name: 'read' })).toEqual([
      'Built-in tool',
      'Read files',
    ]);
  });
});

describe('the kinds that each step accepts', () => {
  it('is one table: a chat step takes DAG and MCP tools, an agent step takes the three kinds', () => {
    expect(VALID_KINDS.chat).toEqual(['dag', 'mcp']);
    expect(VALID_KINDS.agent).toEqual(['dag', 'mcp', 'builtin']);
    expect(stepAccepts('agent', 'dag')).toBe(true);
    expect(stepAccepts('chat', 'builtin')).toBe(false);
  });

  it('turns on every kind that the step accepts, and gives the other a one-line reason', () => {
    const none = new Set<never>();
    const chat = kindAvailability(ts, 'chat', none);
    expect(TOOL_KINDS.map((kind) => chat[kind].enabled)).toEqual([
      true,
      true,
      false,
    ]);
    expect(chat.builtin.note).toBe('Valid on agent.run steps only.');
    expect(chat.dag.note).toBeNull();
    const agent = kindAvailability(ts, 'agent', none);
    expect(TOOL_KINDS.map((kind) => agent[kind].enabled)).toEqual([
      true,
      true,
      true,
    ]);
  });

  it('turns the built-in kind off, with its reason, when every built-in tool is on', () => {
    const all = kindAvailability(ts, 'agent', new Set(BUILTIN_TOOLS));
    expect(all.builtin).toEqual({
      enabled: false,
      note: 'All built-in tools are enabled.',
    });
    expect(all.dag.enabled).toBe(true);
    const some = kindAvailability(ts, 'agent', new Set(['read', 'shell']));
    expect(some.builtin.enabled).toBe(true);
  });
});

describe('KindBadge and KindIcon', () => {
  it('draw the short text of the kind in the colour of the kind', () => {
    render(
      <>
        {TOOL_KINDS.map((kind) => (
          <KindBadge key={kind} kind={kind} />
        ))}
      </>
    );
    const badges = screen.getAllByTestId('ve-tool-kind');
    expect(badges.map((badge) => badge.textContent)).toEqual([
      'DAG',
      'MCP',
      'Built-in',
    ]);
    expect(badges[0]).toHaveClass('text-tool-dag', 'bg-tool-dag/10');
    expect(badges[2]).toHaveClass('text-tool-builtin');
  });

  it('colour the icon of a kind', () => {
    const { container } = render(<KindIcon kind="mcp" />);
    const icon = container.querySelector('[data-kind-icon="mcp"]');
    expect(icon).toHaveClass('text-tool-mcp');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
  });
});
