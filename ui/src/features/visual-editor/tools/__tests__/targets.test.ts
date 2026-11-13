// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readStepTools } from '../parse';
import { dagLink, entryLink, skillLink, subRunLink } from '../targets';

function entry(category: 'dags' | 'mcp', raw: unknown) {
  return readStepTools({
    with: category === 'dags' ? { dags: [raw] } : { tools: { mcp: [raw] } },
  }).entries[0]!;
}

describe('page links by name', () => {
  it('builds the route of each kind of page', () => {
    expect(dagLink('refund')).toEqual({ name: 'refund', path: '/dags/refund' });
    expect(skillLink('refund-policy')).toEqual({
      name: 'refund-policy',
      path: '/ai/skills/refund-policy',
    });
  });

  it('encodes the name and has no page for an empty name or a reference', () => {
    expect(dagLink('a b/c')?.path).toBe('/dags/a%20b%2Fc');
    expect(dagLink('')).toBeNull();
    expect(dagLink(undefined)).toBeNull();
    expect(skillLink('${SKILL}')).toBeNull();
    expect(skillLink('  ')).toBeNull();
  });
});

describe('entryLink', () => {
  it('sends a DAG tool to the page of the DAG', () => {
    expect(entryLink(entry('dags', { name: 'refund' }))).toEqual({
      name: 'refund',
      path: '/dags/refund',
    });
  });

  it('has no page for an MCP server or an invalid entry', () => {
    expect(
      entryLink(
        entry('mcp', {
          name: 'docs',
          url: 'https://mcp.example.com',
          allow: [],
        })
      )
    ).toBeNull();
    expect(entryLink(entry('dags', 42))).toBeNull();
  });
});

describe('subRunLink', () => {
  it('opens the sub-run on the root run, with the file of the root when it is known', () => {
    const link = subRunLink(
      { name: 'main', id: 'run1', fileName: 'main-file' },
      'sub9',
      'local'
    );
    expect(link.name).toBe('sub9');
    expect(link.path).toContain('/dags/main-file');
    expect(link.path).toContain('run=run1');
    expect(link.path).toContain('subRun=sub9');
  });

  it('names the root DAG when the file is not known', () => {
    const link = subRunLink({ name: 'main', id: 'run1' }, 'sub9', 'local');
    expect(link.path).toContain('/dags/main');
  });
});
