// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import type { DockTab } from '../../state/workspaceUrl';
import { DockTabStrip, dockTabLabel } from '../DockTabStrip';

const TABS: DockTab[] = ['log', 'timeline', 'outputs', 'actions'];

function Harness({ start = 'log' }: { start?: DockTab }) {
  const [active, setActive] = useState<DockTab>(start);
  return (
    <DockTabStrip
      tabs={TABS}
      active={active}
      badges={{}}
      onSelect={setActive}
    />
  );
}

const tab = (name: string) => screen.getByRole('tab', { name });

describe('DockTabStrip', () => {
  it('has no Steps tab and no Spec tab', () => {
    render(<Harness />);
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual([
      'Log',
      'Timeline',
      'Outputs',
      'Actions',
    ]);
  });

  it('keeps one tab stop, on the selected tab', () => {
    render(<Harness />);
    expect(tab('Log')).toHaveAttribute('tabindex', '0');
    for (const name of ['Timeline', 'Outputs', 'Actions']) {
      expect(tab(name)).toHaveAttribute('tabindex', '-1');
    }
  });

  it('keeps a tab stop when the active tab is not in the strip', () => {
    render(
      <DockTabStrip tabs={TABS} active="chat" badges={{}} onSelect={() => {}} />
    );
    expect(tab('Log')).toHaveAttribute('tabindex', '0');
    expect(tab('Timeline')).toHaveAttribute('tabindex', '-1');
  });

  it('moves the selection and the focus with the arrow keys', () => {
    render(<Harness />);
    tab('Log').focus();
    fireEvent.keyDown(tab('Log'), { key: 'ArrowRight' });
    expect(tab('Timeline')).toHaveAttribute('aria-selected', 'true');
    expect(tab('Timeline')).toHaveFocus();
    expect(tab('Timeline')).toHaveAttribute('tabindex', '0');
    expect(tab('Log')).toHaveAttribute('tabindex', '-1');
    fireEvent.keyDown(tab('Timeline'), { key: 'ArrowLeft' });
    expect(tab('Log')).toHaveFocus();
  });

  it('wraps around and jumps with Home and End', () => {
    render(<Harness />);
    tab('Log').focus();
    fireEvent.keyDown(tab('Log'), { key: 'ArrowLeft' });
    expect(tab('Actions')).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(tab('Actions'), { key: 'ArrowRight' });
    expect(tab('Log')).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(tab('Log'), { key: 'End' });
    expect(tab('Actions')).toHaveFocus();
    fireEvent.keyDown(tab('Actions'), { key: 'Home' });
    expect(tab('Log')).toHaveFocus();
  });

  it('leaves other keys alone', () => {
    render(<Harness />);
    tab('Log').focus();
    fireEvent.keyDown(tab('Log'), { key: 'a' });
    expect(tab('Log')).toHaveAttribute('aria-selected', 'true');
  });

  it('prefixes the ids and names the list for a second strip', () => {
    render(
      <DockTabStrip
        tabs={TABS}
        active="log"
        badges={{}}
        onSelect={() => {}}
        idPrefix="ww-step-output"
        label="Run"
      />
    );
    expect(tab('Log')).toHaveAttribute('id', 'ww-step-output-tab-log');
    expect(tab('Log')).toHaveAttribute('aria-controls', 'ww-step-output-panel');
    expect(screen.getByRole('tablist', { name: 'Run' })).toBeInTheDocument();
  });

  it('is a 32 px row with no More button and no select', () => {
    render(<Harness />);
    expect(screen.getByRole('tablist')).toHaveClass('h-8');
    expect(screen.queryByRole('button', { name: /More/ })).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('names the waiting count in the tab and shows it in a badge', () => {
    render(
      <DockTabStrip
        tabs={TABS}
        active="log"
        badges={{ actions: 2 }}
        onSelect={() => {}}
      />
    );
    expect(tab('Actions, 2 waiting')).toHaveTextContent('Actions2');
  });
});

describe('dockTabLabel', () => {
  it('names a host tab without a label by its id', () => {
    expect(dockTabLabel('details', {})).toBe('details');
    expect(dockTabLabel('details', { details: 'Details' })).toBe('Details');
  });
});
