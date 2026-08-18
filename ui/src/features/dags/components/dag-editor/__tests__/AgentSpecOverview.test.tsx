// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  AgentTaskStatus,
  DAGDetailsType,
} from '@/api/v1/schema';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it } from 'vitest';
import { AgentSpecOverview } from '../AgentSpecOverview';

describe('AgentSpecOverview', () => {
  it('shows compact actions and the selected action configuration', async () => {
    const user = userEvent.setup();
    const dag = {
      name: 'code-quality-audit',
      type: DAGDetailsType.agent,
      tasks: [
        {
          name: 'confirmed-findings',
          description:
            'Every reported finding has been independently verified.',
          status: AgentTaskStatus.open,
        },
      ],
      steps: [
        {
          name: 'inspect',
          id: 'inspect',
          description: 'Build the deterministic package inventory.',
          script: 'go list ./...',
          dir: '${params.repo}',
        },
        {
          name: 'review',
          id: 'review',
          description: 'Review the package from one focused angle.',
          call: 'quality-review-pass',
          params: 'angle=complexity repo=${params.repo}',
        },
        {
          name: '__agent__',
          description: 'LLM agent',
          executorConfig: { type: 'agent' },
        },
        {
          name: 'ask_user',
          id: 'ask_user',
          description: 'Question asked by the agent',
        },
      ],
    } satisfies components['schemas']['DAGDetails'];

    render(<AgentSpecOverview dag={dag} />);

    expect(screen.getByRole('heading', { name: 'Tasks' })).toBeInTheDocument();
    expect(screen.getByText('confirmed-findings')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Every reported finding has been independently verified.'
      )
    ).toBeInTheDocument();

    const inspectButton = screen.getByRole('button', { name: /inspect/i });
    const reviewButton = screen.getByRole('button', { name: /review/i });
    expect(inspectButton).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('go list ./...')).toBeInTheDocument();

    await user.click(reviewButton);

    expect(reviewButton).toHaveAttribute('aria-pressed', 'true');
    expect(inspectButton).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByText('quality-review-pass')).toBeInTheDocument();

    const parameters = screen
      .getByRole('heading', {
        name: 'Parameters',
      })
      .closest('section');
    expect(parameters).not.toBeNull();
    expect(within(parameters!).getByText('angle')).toBeInTheDocument();
    expect(within(parameters!).getByText('complexity')).toBeInTheDocument();
    expect(within(parameters!).getByText('repo')).toBeInTheDocument();
    expect(within(parameters!).getByText('${params.repo}')).toBeInTheDocument();
  });
});
