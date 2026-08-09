// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

let clientGet: (...args: unknown[]) => Promise<unknown> = () =>
  Promise.resolve({ data: null, error: { message: 'nope' } });

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: (...args: unknown[]) => clientGet(...args) }),
  useQuery: () => ({ data: undefined }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: () => true,
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: true, writeDags: true } }),
}));

import { WikiPageMarkdownPreview } from '../wiki-page-markdown-preview';

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

describe('WikiPageMarkdownPreview', () => {
  it('hides YAML frontmatter from the rendered preview', () => {
    const { container } = render(
      <WikiPageMarkdownPreview
        content={`---
title: Restart API
description: Restart the API service and verify health.
---

# Restart API

Follow the restart procedure.`}
      />
    );

    expect(container.textContent).not.toContain('title: Restart API');
    expect(container.textContent).not.toContain(
      'description: Restart the API service and verify health.'
    );
    expect(
      screen.getByRole('heading', { name: 'Restart API' })
    ).toBeInTheDocument();
    expect(
      screen.getByText('Follow the restart procedure.')
    ).toBeInTheDocument();
  });

  it('does not treat lines that only start with dashes as closing frontmatter delimiters', () => {
    const { container } = render(
      <WikiPageMarkdownPreview
        content={`---
title: Restart API
---not-a-delimiter

# Restart API`}
      />
    );

    expect(container.textContent).toContain('title: Restart API');
    expect(container.textContent).toContain('---not-a-delimiter');
  });
});

describe('WikiPageMarkdownPreview wikilinks', () => {
  const linkContext = { workspace: 'ops', wikiPagePath: 'runbooks/etl' };

  beforeEach(() => {
    clientGet = () =>
      Promise.resolve({ data: null, error: { message: 'nope' } });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders a Wiki page link scoped to the workspace', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="see [[guides/deploy]]"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'guides/deploy' });
    expect(link).toHaveAttribute('href', '/wiki/guides/deploy?workspace=ops');
    expect(link).toHaveAttribute('data-wikilink-target', 'guides/deploy');
  });

  it('uses the label and slugifies the anchor', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="see [[guides/deploy#Roll Back|rollback steps]]"
        linkContext={{ workspace: null, wikiPagePath: 'a' }}
      />
    );

    const link = screen.getByRole('link', { name: 'rollback steps' });
    expect(link).toHaveAttribute('href', '/wiki/guides/deploy#roll-back');
  });

  it('renders a dag wikilink as a link to the DAG page', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="status [[dag:daily-etl|ETL]]"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'ETL' });
    expect(link).toHaveAttribute('href', '/dags/daily-etl');
    expect(link).toHaveAttribute('data-wikilink-target', 'dag:daily-etl');
  });

  it('renders wikilinks as inert spans without a link context', () => {
    render(<WikiPageMarkdownPreview content="see [[guides/deploy]]" />);

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('guides/deploy')).toBeInTheDocument();
  });

  it('renders ![[name]] embeds as attachment images', async () => {
    class TestURL extends URL {
      static createObjectURL() {
        return 'blob:attachment-test';
      }

      static revokeObjectURL() {}
    }
    vi.stubGlobal('URL', TestURL);
    clientGet = () =>
      Promise.resolve({ data: new Blob(['png']), error: undefined });

    renderWithRouter(
      <WikiPageMarkdownPreview
        content="before ![[logo.png|the logo]] after"
        linkContext={linkContext}
      />
    );

    const img = await screen.findByRole('img', { name: 'the logo' });
    expect(img).toHaveAttribute('src', 'blob:attachment-test');
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('renders non-image attachment links as downloadable anchors', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="[report](attachment:monthly%20report.pdf)"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'report' });
    expect(link).toHaveAttribute('title', 'Download monthly report.pdf');
  });

  it('survives malformed percent-encoding in attachment references', () => {
    expect(() =>
      renderWithRouter(
        <WikiPageMarkdownPreview
          content={
            '![bad](attachment:bad%ZZ.png)\n\n[bad](attachment:bad%ZZ.pdf)'
          }
          linkContext={linkContext}
        />
      )
    ).not.toThrow();

    expect(screen.getByRole('link', { name: 'bad' })).toHaveAttribute(
      'title',
      'Download bad%ZZ.pdf'
    );
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });

  it('keeps percent sequences in wikilink targets literal', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="[[reports/50%25%20done]]"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'reports/50%25%20done' });
    expect(link).toHaveAttribute(
      'href',
      '/wiki/reports/50%2525%2520done?workspace=ops'
    );
    expect(link).toHaveAttribute(
      'data-wikilink-target',
      'reports/50%25%20done'
    );
  });

  it('degrades wiki-page-path embeds to plain wiki links', () => {
    renderWithRouter(
      <WikiPageMarkdownPreview
        content="![[guides/deploy]]"
        linkContext={linkContext}
      />
    );

    expect(
      screen.getByRole('link', { name: 'guides/deploy' })
    ).toBeInTheDocument();
  });

  it('dispatches dagu-run fences to the run block instead of plain code', () => {
    const { container } = renderWithRouter(
      <WikiPageMarkdownPreview
        content={'```dagu-run\ndag: daily-etl\nlabel: Retry\n```'}
        linkContext={linkContext}
      />
    );

    // Without a WikiLiveProvider the block renders an inert summary card.
    expect(container.querySelector('pre')).not.toBeInTheDocument();
    expect(screen.getByText('Retry')).toBeInTheDocument();
  });

  it('leaves wikilinks inside code untouched', () => {
    const { container } = renderWithRouter(
      <WikiPageMarkdownPreview
        content={'use `[[inline]]`\n\n```\n[[fenced]]\n```'}
        linkContext={linkContext}
      />
    );

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(container.textContent).toContain('[[inline]]');
    expect(container.textContent).toContain('[[fenced]]');
  });
});
