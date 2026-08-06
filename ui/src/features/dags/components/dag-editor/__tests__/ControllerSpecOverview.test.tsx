// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  ControllerTaskStatus,
  DAGDetailsType,
} from '@/api/v1/schema';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ControllerSpecOverview } from '../ControllerSpecOverview';

describe('ControllerSpecOverview', () => {
  it('presents controller goals and selectable declared actions', async () => {
    const user = userEvent.setup();
    const onSelectStep = vi.fn();
    const reviewStep = {
      name: 'review',
      description: 'Review the package from one focused angle.',
      call: 'quality-review-pass',
      params: 'angle=complexity repo=${params.repo}',
    } satisfies components['schemas']['Step'];
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
          description: 'Build the deterministic package inventory.',
          script: 'go list ./...',
        },
        reviewStep,
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

    render(<ControllerSpecOverview dag={dag} onSelectStep={onSelectStep} />);

    expect(screen.getByText('confirmed-findings')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /inspect/i })
    ).toBeInTheDocument();
    expect(screen.getByText('Can ask user')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /review/i }));

    expect(onSelectStep).toHaveBeenCalledWith(reviewStep);
  });
});
