// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { StatusDot } from '../status-dot';

describe('StatusDot', () => {
  it('is named by the status text of a run', () => {
    render(<StatusDot kind="run" status={Status.Queued} />);
    expect(screen.getByRole('img', { name: 'Queued' })).toBeInTheDocument();
  });

  it('is named by the status text of a step', () => {
    render(<StatusDot kind="step" status={NodeStatus.Skipped} />);
    expect(screen.getByRole('img', { name: 'Skipped' })).toBeInTheDocument();
  });

  it('takes the label when given', () => {
    render(<StatusDot kind="run" status={Status.Failed} label="Last run" />);
    expect(screen.getByRole('img', { name: 'Last run' })).toBeInTheDocument();
  });

  it('is hidden from assistive technology when decorative', () => {
    render(<StatusDot kind="run" status={Status.Success} decorative />);
    expect(screen.queryByRole('img')).toBeNull();
    expect(screen.getByTestId('status-dot')).toHaveAttribute(
      'aria-hidden',
      'true'
    );
  });

  it('uses the status token of its tone', () => {
    render(<StatusDot kind="step" status={NodeStatus.Failed} />);
    expect(screen.getByTestId('status-dot')).toHaveClass(
      'bg-[var(--status-error)]'
    );
  });

  it('pulses on Running, Retrying and Waiting only', () => {
    const cases: Array<[ReactElement, boolean]> = [
      [<StatusDot key="a" kind="run" status={Status.Running} />, true],
      [<StatusDot key="b" kind="run" status={Status.Waiting} />, true],
      [<StatusDot key="c" kind="run" status={Status.Queued} />, false],
      [<StatusDot key="d" kind="step" status={NodeStatus.Running} />, true],
      [<StatusDot key="e" kind="step" status={NodeStatus.Retrying} />, true],
      [<StatusDot key="f" kind="step" status={NodeStatus.Waiting} />, true],
      [<StatusDot key="g" kind="step" status={NodeStatus.Success} />, false],
    ];
    for (const [element, pulse] of cases) {
      const { unmount } = render(element);
      const dot = screen.getByTestId('status-dot');
      if (pulse) {
        expect(dot).toHaveClass('motion-safe:animate-pulse');
      } else {
        expect(dot).not.toHaveClass('motion-safe:animate-pulse');
      }
      unmount();
    }
  });

  // The API status stays in the data attribute: logic and tests read it.
  it('reads Healthy in the success token and pulses, for a healthy run', () => {
    render(<StatusDot kind="run" status={Status.Running} healthy />);
    const dot = screen.getByRole('img', { name: 'Healthy' });
    expect(dot).toHaveClass(
      'bg-[var(--status-success)]',
      'motion-safe:animate-pulse'
    );
    expect(dot).toHaveAttribute('data-status', String(Status.Running));
  });

  it('ignores healthy for a step', () => {
    render(<StatusDot kind="step" status={NodeStatus.Running} healthy />);
    expect(screen.getByRole('img', { name: 'Running' })).toBeInTheDocument();
  });

  // A reused step has the hatch in place of the solid fill, at the same size.
  it('hatches the dot of a reused step, green or red, with its own name', () => {
    const { rerender } = render(
      <StatusDot
        kind="step"
        status={NodeStatus.Skipped}
        reused="success"
        reusedFrom="034mroF1"
      />
    );
    const dot = screen.getByRole('img', { name: 'Reused, succeeded' });
    expect(dot).toHaveClass('bg-[var(--status-success)]', 'h-2', 'w-2');
    expect(dot.style.backgroundImage).toContain('repeating-linear-gradient');
    expect(dot).toHaveAttribute('title', 'Reused from run 034mroF1: succeeded');

    rerender(
      <StatusDot
        kind="step"
        status={NodeStatus.Skipped}
        reused="failed"
        reusedFrom="034mroF1"
      />
    );
    const failed = screen.getByRole('img', { name: 'Reused, failed' });
    expect(failed).toHaveClass('bg-[var(--status-error)]', 'h-2', 'w-2');
    expect(failed).toHaveAttribute('title', 'Reused from run 034mroF1: failed');
  });

  it('keeps the solid fill of a step that ran', () => {
    render(<StatusDot kind="step" status={NodeStatus.Success} />);
    expect(screen.getByTestId('status-dot').style.backgroundImage).toBe('');
    expect(screen.getByTestId('status-dot')).not.toHaveAttribute('data-reused');
  });
});
