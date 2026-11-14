// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { DraftRecoveryCard } from '../DraftRecoveryCard';
import { YamlTools } from '../YamlTools';

describe('YamlTools', () => {
  it('applies the quick fixes when some exist', () => {
    const onFixAll = vi.fn();
    render(
      <YamlTools
        fixCount={2}
        canFormat
        onFixAll={onFixAll}
        onFormat={vi.fn()}
      />
    );
    const wand = screen.getByRole('button', { name: 'Apply all quick fixes' });
    expect(wand).toBeEnabled();
    expect(wand).toHaveAttribute('title', 'Apply all quick fixes');
    fireEvent.click(wand);
    expect(onFixAll).toHaveBeenCalledOnce();
  });

  it('turns the wand off, with a title, when no fix exists', () => {
    render(
      <YamlTools fixCount={0} canFormat onFixAll={vi.fn()} onFormat={vi.fn()} />
    );
    const wand = screen.getByRole('button', { name: 'Apply all quick fixes' });
    expect(wand).toBeDisabled();
    expect(wand).toHaveAttribute('title', 'No quick fix to apply');
  });

  it('formats on request, and not a text that does not parse', () => {
    const onFormat = vi.fn();
    const { rerender } = render(
      <YamlTools
        fixCount={0}
        canFormat
        onFixAll={vi.fn()}
        onFormat={onFormat}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Format YAML' }));
    expect(onFormat).toHaveBeenCalledOnce();

    rerender(
      <YamlTools
        fixCount={0}
        canFormat={false}
        onFixAll={vi.fn()}
        onFormat={onFormat}
      />
    );
    const format = screen.getByRole('button', { name: 'Format YAML' });
    expect(format).toBeDisabled();
    expect(format).toHaveAttribute(
      'title',
      expect.stringContaining('Fix the YAML syntax error')
    );
  });
});

describe('YamlTools of a step', () => {
  it('has the two buttons of the file, with the labels of the step', () => {
    const onFixAll = vi.fn();
    const onFormat = vi.fn();
    render(
      <YamlTools
        scope="step"
        fixCount={1}
        canFormat
        onFixAll={onFixAll}
        onFormat={onFormat}
      />
    );
    const wand = screen.getByRole('button', {
      name: 'Apply quick fixes to this step',
    });
    const format = screen.getByRole('button', { name: 'Format this step' });
    expect(wand).toBeEnabled();
    // Same size as the buttons of the whole file.
    expect(wand.className).toContain('h-7 w-7');
    expect(format.className).toContain('h-7 w-7');
    fireEvent.click(wand);
    fireEvent.click(format);
    expect(onFixAll).toHaveBeenCalledOnce();
    expect(onFormat).toHaveBeenCalledOnce();
  });

  it('keeps both buttons, off, with the reason, when there is nothing to do', () => {
    render(
      <YamlTools
        scope="step"
        fixCount={0}
        canFormat={false}
        formatOffReason="Select a step first"
        onFixAll={vi.fn()}
        onFormat={vi.fn()}
      />
    );
    const wand = screen.getByRole('button', {
      name: 'Apply quick fixes to this step',
    });
    expect(wand).toBeDisabled();
    expect(wand).toHaveAttribute('title', 'No quick fix');
    const format = screen.getByRole('button', { name: 'Format this step' });
    expect(format).toBeDisabled();
    expect(format).toHaveAttribute('title', 'Select a step first');
  });
});

describe('DraftRecoveryCard', () => {
  it('offers the draft with Restore and Discard', () => {
    const onRestore = vi.fn();
    const onDiscard = vi.fn();
    render(
      <DraftRecoveryCard
        savedAt={Date.UTC(2026, 0, 2)}
        onRestore={onRestore}
        onDiscard={onDiscard}
      />
    );
    const card = screen.getByRole('status', { name: 'Unsaved draft' });
    expect(card).toHaveTextContent('Restore unsaved draft from');
    expect(card).toHaveTextContent(
      `from ${formatAbsoluteTime(Date.UTC(2026, 0, 2))}?`
    );
    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }));
    expect(onRestore).toHaveBeenCalledOnce();
    expect(onDiscard).toHaveBeenCalledOnce();
  });
});
