// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, NodeStatusLabel } from '@/api/v1/schema';
import { toMermaidNodeId } from '@/lib/utils';
import Graph from '../Graph';

const mermaidRenderMock = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: mermaidRenderMock,
  },
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
}));

function node(
  name: string,
  status: NodeStatus,
  depends?: string[]
): components['schemas']['Node'] {
  return {
    step: { name, depends },
    stdout: '',
    stderr: '',
    startedAt: '',
    finishedAt: '',
    status,
    statusLabel: NodeStatusLabel.not_started,
    retryCount: 0,
    doneCount: 0,
  };
}

describe('Graph', () => {
  it('renders an interactive fallback when Mermaid rendering fails', async () => {
    mermaidRenderMock.mockRejectedValueOnce(new TypeError('render exploded'));
    const onClickNode = vi.fn();

    render(
      <Graph
        type="status"
        steps={[
          node('extract (source)', NodeStatus.Success),
          node('load:warehouse', NodeStatus.NotStarted, ['extract (source)']),
        ]}
        onClickNode={onClickNode}
        selectOnClick
      />
    );

    const fallback = await screen.findByTestId('graph-fallback');
    expect(fallback).toHaveTextContent('extract (source)');
    expect(fallback).toHaveTextContent('load:warehouse');
    expect(
      screen.queryByText(/Error rendering diagram/i)
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: /inspect extract \(source\)/i })
    );

    await waitFor(() => {
      expect(onClickNode).toHaveBeenCalledWith(
        toMermaidNodeId('extract (source)')
      );
    });
  });
});
