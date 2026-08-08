// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: vi.fn().mockResolvedValue({ data: null, error: { message: 'nope' } }) }),
  useQuery: () => ({ data: undefined }),
}));

import { DocMarkdownPreview } from '../doc-markdown-preview';

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

describe('DocMarkdownPreview', () => {
  it('hides YAML frontmatter from the rendered preview', () => {
    const { container } = render(
      <DocMarkdownPreview
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
    expect(screen.getByText('Follow the restart procedure.')).toBeInTheDocument();
  });

  it('does not treat lines that only start with dashes as closing frontmatter delimiters', () => {
    const { container } = render(
      <DocMarkdownPreview
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

describe('DocMarkdownPreview wikilinks', () => {
  const linkContext = { workspace: 'ops', docPath: 'runbooks/etl' };

  it('renders a doc wikilink as an internal link scoped to the workspace', () => {
    renderWithRouter(
      <DocMarkdownPreview
        content="see [[guides/deploy]]"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'guides/deploy' });
    expect(link).toHaveAttribute('href', '/docs/guides/deploy?workspace=ops');
    expect(link).toHaveAttribute('data-wikilink-target', 'guides/deploy');
  });

  it('uses the label and slugifies the anchor', () => {
    renderWithRouter(
      <DocMarkdownPreview
        content="see [[guides/deploy#Roll Back|rollback steps]]"
        linkContext={{ workspace: null, docPath: 'a' }}
      />
    );

    const link = screen.getByRole('link', { name: 'rollback steps' });
    expect(link).toHaveAttribute('href', '/docs/guides/deploy#roll-back');
  });

  it('renders a dag wikilink as a link to the DAG page', () => {
    renderWithRouter(
      <DocMarkdownPreview
        content="status [[dag:daily-etl|ETL]]"
        linkContext={linkContext}
      />
    );

    const link = screen.getByRole('link', { name: 'ETL' });
    expect(link).toHaveAttribute('href', '/dags/daily-etl');
    expect(link).toHaveAttribute('data-wikilink-target', 'dag:daily-etl');
  });

  it('renders wikilinks as inert spans without a link context', () => {
    render(<DocMarkdownPreview content="see [[guides/deploy]]" />);

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('guides/deploy')).toBeInTheDocument();
  });

  it('leaves wikilinks inside code untouched', () => {
    const { container } = renderWithRouter(
      <DocMarkdownPreview
        content={'use `[[inline]]`\n\n```\n[[fenced]]\n```'}
        linkContext={linkContext}
      />
    );

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(container.textContent).toContain('[[inline]]');
    expect(container.textContent).toContain('[[fenced]]');
  });
});
