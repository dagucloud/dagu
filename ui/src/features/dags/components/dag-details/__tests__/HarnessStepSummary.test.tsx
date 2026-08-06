// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import HarnessStepSummary from '../HarnessStepSummary';

describe('HarnessStepSummary', () => {
  it('renders provider, fallback, prompt, and harness options', () => {
    const step = {
      name: 'review',
      commands: [
        {
          command: 'Review',
          args: ['this', 'repository'],
        },
      ],
      executorConfig: {
        type: 'harness',
        config: {
          provider: 'claude',
          model: 'sonnet',
          bare: true,
          disabled: false,
          fallback: [
            {
              provider: 'codex',
              'full-auto': true,
            },
          ],
        },
      },
    } as components['schemas']['Step'];

    render(<HarnessStepSummary step={step} />);

    expect(screen.getByText('Harness')).toBeInTheDocument();
    expect(screen.getByText('Primary')).toBeInTheDocument();
    expect(screen.getByText('claude')).toBeInTheDocument();
    expect(screen.getByText('model=sonnet')).toBeInTheDocument();
    expect(screen.getByText('bare')).toBeInTheDocument();
    expect(screen.getByText('Fallback 1')).toBeInTheDocument();
    expect(screen.getByText('codex')).toBeInTheDocument();
    expect(screen.getByText('full-auto')).toBeInTheDocument();
    expect(screen.getByText('Review this repository')).toBeInTheDocument();
    expect(screen.queryByText('disabled=false')).not.toBeInTheDocument();
  });

  it('returns nothing for non-harness steps', () => {
    const step = {
      name: 'plain-command',
      commands: [
        {
          command: 'echo',
          args: ['hello'],
        },
      ],
      executorConfig: {
        type: 'command',
        config: {},
      },
    } as components['schemas']['Step'];

    const { container } = render(<HarnessStepSummary step={step} />);

    expect(container).toBeEmptyDOMElement();
  });

  it('renders a compact summary for step tables', () => {
    const step = {
      name: 'review',
      commands: [
        {
          command: 'Review',
          args: ['this', 'repository'],
        },
      ],
      executorConfig: {
        type: 'harness',
        config: {
          provider: 'claude',
          model: 'sonnet',
          bare: true,
          fallback: [
            {
              provider: 'codex',
              'full-auto': true,
            },
          ],
        },
      },
    } as components['schemas']['Step'];

    render(<HarnessStepSummary step={step} compact />);

    expect(screen.getByText('Harness')).toBeInTheDocument();
    expect(screen.getByText('claude')).toBeInTheDocument();
    expect(screen.getByText('3 options')).toBeInTheDocument();
    expect(screen.getByText('1 fallback')).toBeInTheDocument();
    expect(screen.getByText('Review this repository')).toBeInTheDocument();
  });
});
