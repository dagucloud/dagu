// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import DAGStepTableRow from '../DAGStepTableRow';

describe('DAGStepTableRow', () => {
  it('shows log step messages in the execution column', () => {
    const step = {
      name: 'announce',
      executorConfig: {
        type: 'log',
        config: {
          message: 'Deploying ${ENVIRONMENT}',
        },
      },
    } as components['schemas']['Step'];

    render(
      <table>
        <tbody>
          <DAGStepTableRow step={step} index={0} />
        </tbody>
      </table>
    );

    expect(
      screen.getByLabelText('Log message: Deploying ${ENVIRONMENT}')
    ).toBeInTheDocument();
  });

  it('shows the script badge for harness steps', () => {
    const step = {
      name: 'review',
      script: 'summarize the current branch',
      commands: [
        {
          command: 'Review the repository',
          args: [],
        },
      ],
      executorConfig: {
        type: 'harness',
        config: {
          provider: 'claude',
        },
      },
    } as components['schemas']['Step'];

    render(
      <table>
        <tbody>
          <DAGStepTableRow step={step} index={0} />
        </tbody>
      </table>
    );

    expect(screen.getByText('Script defined')).toBeInTheDocument();
    expect(screen.getByText('Review the repository')).toBeInTheDocument();
  });
});
