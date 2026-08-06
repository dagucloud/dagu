// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  ControllerTaskStatus,
  DAGDetailsType,
} from '@/api/v1/schema';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ControllerSpecOverview } from '../ControllerSpecOverview';

describe('ControllerSpecOverview', () => {
  it('shows compact actions and the selected action configuration', async () => {
    const user = userEvent.setup();
    const onEditYAML = vi.fn();
    const dag = {
      name: 'code-quality-audit',
      type: DAGDetailsType.controller,
      tasks: [
        {
          name: 'confirmed-findings',
          description:
            'Every reported finding has been independently verified.',
          status: ControllerTaskStatus.open,
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
          name: '__controller__',
          description: 'LLM controller',
          executorConfig: { type: 'controller' },
        },
        {
          name: 'ask_user',
          id: 'ask_user',
          description: 'Question asked by the controller',
        },
      ],
    } satisfies components['schemas']['DAGDetails'];

    render(<ControllerSpecOverview dag={dag} onEditYAML={onEditYAML} />);

    expect(screen.getByText('confirmed-findings')).toBeInTheDocument();
    expect(screen.getByText('Can ask user')).toBeInTheDocument();
    expect(screen.queryByText('LLM controller')).not.toBeInTheDocument();

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

    await user.click(screen.getByRole('button', { name: 'Edit YAML' }));
    expect(onEditYAML).toHaveBeenCalledOnce();
  });
});
