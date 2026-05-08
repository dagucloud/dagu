// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import Mermaid from '../mermaid';

type RenderResult = {
  svg: string;
  bindFunctions?: (element: Element) => void;
};

type PendingRender = {
  def: string;
  resolve: (value: RenderResult) => void;
  reject: (error: unknown) => void;
};

const mermaidRenderMock = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: mermaidRenderMock,
  },
}));

describe('Mermaid', () => {
  let pendingRenders: PendingRender[];

  function pendingRenderAt(index: number): PendingRender {
    const pendingRender = pendingRenders[index];
    if (!pendingRender) {
      throw new Error(`Missing pending render at index ${index}`);
    }
    return pendingRender;
  }

  beforeEach(() => {
    pendingRenders = [];
    mermaidRenderMock.mockReset();
    mermaidRenderMock.mockImplementation(
      (_id: string, def: string) =>
        new Promise<RenderResult>((resolve, reject) => {
          pendingRenders.push({ def, resolve, reject });
        })
    );
  });

  it('ignores stale render results after a newer definition renders', async () => {
    const { container, rerender } = render(
      <Mermaid def="graph TD; A-->B;" scale={1} />
    );

    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    rerender(<Mermaid def="graph TD; C-->D;" scale={1} />);
    await waitFor(() => expect(pendingRenders).toHaveLength(2));

    await act(async () => {
      pendingRenderAt(1).resolve({ svg: '<svg data-def="newer"></svg>' });
    });

    await waitFor(() => {
      expect(container.querySelector('svg[data-def="newer"]')).not.toBeNull();
    });

    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg data-def="stale"></svg>' });
    });

    expect(container.querySelector('svg[data-def="newer"]')).not.toBeNull();
    expect(container.querySelector('svg[data-def="stale"]')).toBeNull();
  });

  it('ignores stale render errors after a newer definition renders', async () => {
    const { rerender } = render(
      <Mermaid
        def="graph TD; A-->B;"
        fallback={<div>Fallback graph</div>}
        scale={1}
      />
    );

    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    rerender(
      <Mermaid
        def="graph TD; C-->D;"
        fallback={<div>Fallback graph</div>}
        scale={1}
      />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(2));

    await act(async () => {
      pendingRenderAt(1).resolve({ svg: '<svg data-def="newer"></svg>' });
    });
    await waitFor(() => {
      expect(screen.queryByText('Fallback graph')).not.toBeInTheDocument();
    });

    await act(async () => {
      pendingRenderAt(0).reject(new Error('stale failure'));
    });

    expect(screen.queryByText('Fallback graph')).not.toBeInTheDocument();
  });
});
