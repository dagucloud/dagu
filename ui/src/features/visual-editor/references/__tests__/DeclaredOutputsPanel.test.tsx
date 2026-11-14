// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ReferenceScopeStepOutputsKnown } from '@/api/v1/schema';
import { scopeHttpJq } from '../../testing/fixtures';
import { DeclaredOutputsPanel } from '../DeclaredOutputsPanel';

describe('DeclaredOutputsPanel', () => {
  it('lists the outputs that the step declares, with their references', () => {
    render(
      <DeclaredOutputsPanel
        scope={scopeHttpJq}
        stepName="fetch"
        action="http.request"
      />
    );
    expect(screen.getByText('http.request')).toBeInTheDocument();
    expect(screen.getByText('body')).toBeInTheDocument();
    expect(screen.getByText('${steps.fetch.outputs.body}')).toBeInTheDocument();
    expect(screen.getByText('status')).toBeInTheDocument();
    expect(screen.queryByText('No run selected')).toBeNull();
  });

  // The reference truncates, so that every card has the same two rows.
  it('truncates the reference and has no Copy or Insert button', () => {
    render(<DeclaredOutputsPanel scope={scopeHttpJq} stepName="fetch" />);
    const card = screen.getAllByTestId('declared-output')[0]!;
    const reference = within(card).getByText(/^\$\{steps\.fetch\.outputs\./);
    expect(reference).toHaveClass('truncate');
    expect(reference).toHaveAttribute('title', reference.textContent ?? '');
    expect(reference.parentElement).toBe(card);
    expect(
      within(card).queryByRole('button', { name: 'Copy reference' })
    ).toBeNull();
    expect(within(card).queryByRole('button', { name: 'Insert' })).toBeNull();
    // The name is the only button of the card.
    expect(within(card).getAllByRole('button')).toHaveLength(1);
  });

  it('shows a neutral hint for a step that declares no outputs', () => {
    render(
      <DeclaredOutputsPanel
        scope={{
          ...scopeHttpJq,
          steps: [
            {
              name: 'load',
              stepPath: 'steps[2]',
              upstream: [],
              outputs: [],
              outputsKnown: ReferenceScopeStepOutputsKnown.static,
            },
          ],
        }}
        stepName="load"
      />
    );
    expect(screen.getByText('This step declares no outputs')).toBeVisible();
    expect(screen.queryByText('No run selected')).toBeNull();
  });

  it('shows the hint while the scope is not known yet', () => {
    render(<DeclaredOutputsPanel stepName="fetch" />);
    expect(screen.getByText('This step declares no outputs')).toBeVisible();
  });

  it('copies the reference when the name is clicked, and swaps to a Check', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    render(<DeclaredOutputsPanel scope={scopeHttpJq} stepName="fetch" />);
    fireEvent.click(screen.getByRole('button', { name: 'body' }));
    expect(writeText).toHaveBeenCalledWith('${steps.fetch.outputs.body}');
    expect(
      await screen.findByRole('button', { name: 'Copied' })
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'status' })).toBeInTheDocument();
  });

  it('inserts at the cursor when a field takes the token', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    const onInsert = vi.fn(() => true);
    render(
      <DeclaredOutputsPanel
        scope={scopeHttpJq}
        stepName="fetch"
        onInsert={onInsert}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'status' }));
    expect(onInsert).toHaveBeenCalledWith('${steps.fetch.outputs.status}');
    expect(writeText).not.toHaveBeenCalled();
  });
});
