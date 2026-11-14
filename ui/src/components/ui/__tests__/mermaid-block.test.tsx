// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { findForbiddenStaticImports } from '@/testing/importGraph';
import { MermaidBlock } from '../mermaid-block';

type RenderResult = { svg: string };

type PendingRender = {
  id: string;
  code: string;
  resolve: (value: RenderResult) => void;
  reject: (error: unknown) => void;
};

const mermaidMock = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(),
}));

vi.mock('mermaid', () => ({ default: mermaidMock }));

// parity:graph-mermaid-render - Markdown diagrams still render through Mermaid,
// which now loads with a dynamic import.
describe('MermaidBlock', () => {
  let pending: PendingRender[];

  async function nextRender(index: number): Promise<PendingRender> {
    await waitFor(() => expect(pending[index]).toBeDefined());
    return pending[index]!;
  }

  beforeEach(() => {
    pending = [];
    mermaidMock.initialize.mockReset();
    mermaidMock.render.mockReset();
    mermaidMock.render.mockImplementation(
      (id: string, code: string) =>
        new Promise<RenderResult>((resolve, reject) => {
          pending.push({ id, code, resolve, reject });
        })
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('has no static import of mermaid', () => {
    expect(
      findForbiddenStaticImports(
        ['components/ui/mermaid-block.tsx', 'components/ui/markdown.tsx'],
        [/^pkg:mermaid$/]
      )
    ).toEqual([]);
  });

  it('renders the SVG from the dynamically loaded mermaid', async () => {
    const { container } = render(
      <MermaidBlock code={'  graph TD; A-->B;  '} />
    );

    const call = await nextRender(0);
    expect(call.code).toBe('graph TD; A-->B;');
    expect(mermaidMock.initialize).toHaveBeenCalledWith(
      expect.objectContaining({ startOnLoad: false, securityLevel: 'loose' })
    );

    call.resolve({ svg: '<svg data-testid="diagram"></svg>' });

    expect(await screen.findByTestId('diagram')).toBeInTheDocument();
    expect(container.querySelector('pre')).not.toBeInTheDocument();
  });

  it('keeps the source in a <pre> fallback when the render fails', async () => {
    const { container } = render(<MermaidBlock code="graph TD; A-->" />);

    const call = await nextRender(0);
    const injected = document.createElement('div');
    injected.id = call.id;
    document.body.appendChild(injected);
    call.reject(new Error('Parse error'));

    await waitFor(() =>
      expect(container.querySelector('pre code')).toHaveTextContent(
        'graph TD; A-->'
      )
    );
    expect(document.getElementById(call.id)).toBeNull();
  });

  it('ignores a result that arrives after unmount', async () => {
    const consoleError = vi.spyOn(console, 'error');
    const { container, unmount } = render(
      <MermaidBlock code="graph TD; A-->B;" />
    );
    const host = container.firstElementChild as HTMLElement;

    const call = await nextRender(0);
    unmount();
    call.resolve({ svg: '<svg data-testid="late"></svg>' });
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(host).toBeEmptyDOMElement();
    expect(screen.queryByTestId('late')).not.toBeInTheDocument();
    expect(consoleError).not.toHaveBeenCalled();
  });

  it('ignores a stale result when the code changes', async () => {
    const { container, rerender } = render(
      <MermaidBlock code="graph TD; A-->B;" />
    );
    const first = await nextRender(0);

    rerender(<MermaidBlock code="graph TD; C-->D;" />);
    const second = await nextRender(1);

    second.resolve({ svg: '<svg data-testid="new"></svg>' });
    expect(await screen.findByTestId('new')).toBeInTheDocument();

    first.resolve({ svg: '<svg data-testid="old"></svg>' });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByTestId('old')).not.toBeInTheDocument();
    expect(container.querySelectorAll('svg')).toHaveLength(1);
  });

  // A fresh module copy has no cached mermaid, so the load itself can be
  // held until after unmount. Mermaid must then stay untouched.
  it('does not call mermaid when unmounted before it loads', async () => {
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    vi.resetModules();
    vi.doMock('mermaid', async () => {
      await gate;
      return { default: mermaidMock };
    });
    const fresh = await import('../mermaid-block');

    const { unmount } = render(<fresh.MermaidBlock code="graph TD; A-->B;" />);
    unmount();
    release();
    await import('mermaid');
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mermaidMock.initialize).not.toHaveBeenCalled();
    expect(mermaidMock.render).not.toHaveBeenCalled();
  });

  it('loads mermaid again when the code changes after a failed load', async () => {
    let attempts = 0;
    vi.resetModules();
    vi.doMock('mermaid', () => {
      attempts++;
      if (attempts === 1) throw new Error('chunk load failed');
      return { default: mermaidMock };
    });
    const fresh = await import('../mermaid-block');

    const { container, rerender } = render(
      <fresh.MermaidBlock code="graph TD; A-->B;" />
    );
    await waitFor(() =>
      expect(container.querySelector('pre code')).toHaveTextContent(
        'graph TD; A-->B;'
      )
    );

    rerender(<fresh.MermaidBlock code="graph TD; C-->D;" />);
    const call = await nextRender(0);
    call.resolve({ svg: '<svg data-testid="diagram"></svg>' });

    expect(await screen.findByTestId('diagram')).toBeInTheDocument();
    expect(container.querySelector('pre')).not.toBeInTheDocument();
  });
});
