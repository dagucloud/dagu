// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Code, Eye, Settings } from 'lucide-react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { TabStrip, type TabStripTab } from '../tab-strip';

const TABS: TabStripTab[] = [
  { id: 'a', label: 'Overview', icon: Eye, controls: 'panel-a' },
  { id: 'b', label: 'Spec', icon: Code, badge: 2 },
  { id: 'c', label: 'Settings', icon: Settings, disabled: true },
  { id: 'd', label: 'Plain' },
];

function Harness({
  compact,
  initial = 'a',
  onChange,
}: {
  compact?: boolean;
  initial?: string;
  onChange?: (id: string) => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <TabStrip
      label="Views"
      tabs={TABS}
      value={value}
      compact={compact}
      onChange={(id) => {
        setValue(id);
        onChange?.(id);
      }}
    />
  );
}

describe('TabStrip', () => {
  it('has a tablist with tabs and the selected state', () => {
    render(<Harness />);
    expect(screen.getByRole('tablist', { name: 'Views' })).toBeInTheDocument();
    const tabs = screen.getAllByRole('tab');
    expect(tabs).toHaveLength(4);
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute(
      'aria-controls',
      'panel-a'
    );
    expect(screen.getByRole('tab', { name: /Spec/ })).toHaveAttribute(
      'aria-selected',
      'false'
    );
    expect(screen.getByRole('tab', { name: 'Settings' })).toBeDisabled();
  });

  it('uses a 32 px row, 24 px tabs and the selected classes', () => {
    render(<Harness />);
    expect(screen.getByRole('tablist')).toHaveClass('h-8');
    const selected = screen.getByRole('tab', { name: 'Overview' });
    expect(selected).toHaveClass(
      'h-6',
      'bg-muted',
      'font-medium',
      'text-foreground'
    );
    const other = screen.getByRole('tab', { name: /Spec/ });
    expect(other).toHaveClass('h-6', 'hover:bg-muted');
    expect(other).not.toHaveClass('bg-muted');
    expect(other.className).toContain('focus-visible:ring-2');
  });

  it('keeps one tab stop that follows the selection', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const stops = () =>
      screen.getAllByRole('tab').filter((tab) => tab.tabIndex === 0);
    expect(stops().map((tab) => tab.textContent)).toEqual(['Overview']);
    await user.click(screen.getByRole('tab', { name: /Spec/ }));
    expect(stops().map((tab) => tab.getAttribute('data-tab-id'))).toEqual([
      'b',
    ]);
  });

  it('falls back to the first enabled tab when the value is unknown', () => {
    render(<Harness initial="zzz" />);
    const stops = screen
      .getAllByRole('tab')
      .filter((tab) => tab.tabIndex === 0);
    expect(stops).toHaveLength(1);
    expect(stops[0]).toHaveAttribute('data-tab-id', 'a');
  });

  it('selects and focuses with the arrows, Home and End, skipping disabled tabs', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    screen.getByRole('tab', { name: 'Overview' }).focus();

    await user.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenLastCalledWith('b');
    expect(screen.getByRole('tab', { name: /Spec/ })).toHaveFocus();

    // Settings is disabled: the next tab is Plain.
    await user.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenLastCalledWith('d');
    expect(screen.getByRole('tab', { name: 'Plain' })).toHaveFocus();

    await user.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenLastCalledWith('a');

    await user.keyboard('{ArrowLeft}');
    expect(onChange).toHaveBeenLastCalledWith('d');

    await user.keyboard('{Home}');
    expect(onChange).toHaveBeenLastCalledWith('a');
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus();

    await user.keyboard('{End}');
    expect(onChange).toHaveBeenLastCalledWith('d');
    expect(screen.getByRole('tab', { name: 'Plain' })).toHaveFocus();
  });

  it('shows names and a badge when it is not compact', () => {
    render(<Harness />);
    const spec = screen.getByRole('tab', { name: /Spec/ });
    expect(spec).toHaveTextContent('Spec');
    expect(spec).toHaveTextContent('2');
    expect(spec).not.toHaveAttribute('aria-label');
  });

  it('shows only icons in compact mode, with an aria-label and a tooltip', async () => {
    const user = userEvent.setup();
    render(<Harness compact />);
    const overview = screen.getByRole('tab', { name: 'Overview' });
    expect(overview).toHaveAttribute('aria-label', 'Overview');
    expect(overview).toHaveTextContent('');
    expect(overview.querySelector('svg')).not.toBeNull();
    expect(screen.getByRole('tab', { name: 'Spec (2)' })).toBeInTheDocument();
    await user.hover(overview);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Overview');
    // A tab without an icon keeps its label.
    expect(screen.getByRole('tab', { name: 'Plain' })).toHaveTextContent(
      'Plain'
    );
  });
});
