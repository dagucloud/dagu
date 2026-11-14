// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NoticeLine, type NoticeLineItem } from '../NoticeLine';

function frozen(): NoticeLineItem {
  return {
    id: 'frozen',
    tone: 'warning',
    message: 'YAML has errors: the canvas shows the last valid version',
    priority: 1000,
  };
}

function draft(restore = vi.fn(), discard = vi.fn()): NoticeLineItem {
  return {
    id: 'draft',
    tone: 'warning',
    label: 'Unsaved draft',
    message: 'Restore unsaved draft from today?',
    action: { label: 'Restore', run: restore },
    secondaryAction: { label: 'Discard', run: discard },
    priority: 800,
  };
}

function refused(convert = vi.fn(), dismiss = vi.fn()): NoticeLineItem {
  return {
    id: 'edit-refused',
    tone: 'warning',
    role: 'alert',
    message: 'This step cannot be edited here',
    action: { label: 'Convert steps', run: convert },
    dismissible: true,
    onDismiss: dismiss,
    priority: 900,
  };
}

describe('NoticeLine', () => {
  it('renders nothing without notices', () => {
    const { container } = render(<NoticeLine notices={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('colours a warning with the warning token only', () => {
    render(<NoticeLine notices={[frozen()]} />);
    const notice = screen.getByRole('status');
    expect(notice).toHaveClass(
      'text-warning',
      'bg-warning/10',
      'border-warning/40'
    );
    expect(notice.className).not.toMatch(/amber-/);
  });

  // parity:canvas-frozen-state
  it('shows the notice with the highest priority and counts the rest', () => {
    const host: NoticeLineItem = {
      id: 'run-not-found',
      tone: 'info',
      message: 'The run was not found',
      priority: 1,
    };
    render(<NoticeLine notices={[host, draft(), frozen(), refused()]} />);

    const line = screen.getByTestId('notice-line');
    expect(line).toHaveTextContent('the canvas shows the last valid version');
    expect(line).not.toHaveTextContent('The run was not found');
    const more = screen.getByRole('button', { name: '3 more notices' });
    expect(more).toHaveTextContent('+3');
    expect(more).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(more);
    const panel = screen.getByRole('dialog', { name: 'More notices' });
    expect(
      within(panel)
        .getAllByRole('listitem')
        .map((item) => item.firstElementChild?.getAttribute('data-notice-id'))
    ).toEqual(['edit-refused', 'draft', 'run-not-found']);
  });

  it('keeps the given order for equal priorities', () => {
    render(
      <NoticeLine
        notices={[
          { id: 'a', tone: 'info', message: 'First', priority: 5 },
          { id: 'b', tone: 'info', message: 'Second', priority: 5 },
        ]}
      />
    );
    expect(screen.getByRole('status')).toHaveTextContent('First');
  });

  // parity:spec-draft-recovery
  it('offers Restore and Discard for a draft', () => {
    const restore = vi.fn();
    const discard = vi.fn();
    render(<NoticeLine notices={[draft(restore, discard)]} />);

    const notice = screen.getByRole('status', { name: 'Unsaved draft' });
    fireEvent.click(within(notice).getByRole('button', { name: 'Restore' }));
    expect(restore).toHaveBeenCalledOnce();
    fireEvent.click(within(notice).getByRole('button', { name: 'Discard' }));
    expect(discard).toHaveBeenCalledOnce();
    expect(
      within(notice).queryByRole('button', { name: 'Dismiss' })
    ).not.toBeInTheDocument();
  });

  // parity:edit-notice-bar
  it('shows a refused edit as an alert with its actions', () => {
    const convert = vi.fn();
    const dismiss = vi.fn();
    render(<NoticeLine notices={[refused(convert, dismiss)]} />);

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('This step cannot be edited here');
    fireEvent.click(
      within(alert).getByRole('button', { name: 'Convert steps' })
    );
    expect(convert).toHaveBeenCalledOnce();
    fireEvent.click(within(alert).getByRole('button', { name: 'Dismiss' }));
    expect(dismiss).toHaveBeenCalledOnce();
  });

  it('runs the actions of a notice in the popover', () => {
    const convert = vi.fn();
    render(<NoticeLine notices={[frozen(), refused(convert)]} />);

    fireEvent.click(screen.getByRole('button', { name: '1 more notice' }));
    const panel = screen.getByRole('dialog', { name: 'More notices' });
    fireEvent.click(
      within(panel).getByRole('button', { name: 'Convert steps' })
    );
    expect(convert).toHaveBeenCalledOnce();
  });

  it('hides a dismissed host notice until it leaves and comes back', () => {
    const host: NoticeLineItem = {
      id: 'newer-run-active',
      tone: 'info',
      message: 'A newer run is active',
      dismissible: true,
      priority: 10,
    };
    const view = render(<NoticeLine notices={[host]} />);

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('A newer run is active')).toBeNull();

    view.rerender(<NoticeLine notices={[host]} />);
    expect(screen.queryByText('A newer run is active')).toBeNull();

    view.rerender(<NoticeLine notices={[]} />);
    view.rerender(<NoticeLine notices={[host]} />);
    expect(screen.getByText('A newer run is active')).toBeInTheDocument();
  });

  // The host persists the choice, as the monitoring offer does, or clears the
  // cause, as run-not-found does.
  it('calls the dismiss of the top host notice', () => {
    const offer = vi.fn();
    const notFound = vi.fn();
    render(
      <NoticeLine
        notices={[
          {
            id: 'monitoring-offer',
            tone: 'info',
            message: 'Get an email if this server stops',
            dismissible: true,
            dismiss: offer,
            priority: 10,
          },
          {
            id: 'run-not-found',
            tone: 'warning',
            message: 'The run in the link was not found.',
            dismissible: true,
            dismiss: notFound,
            priority: 60,
          },
        ]}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(notFound).toHaveBeenCalledOnce();
    expect(screen.queryByText('The run in the link was not found.')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(offer).toHaveBeenCalledOnce();
  });

  it('calls the dismiss of a host notice in the popover', () => {
    const offer = vi.fn();
    render(
      <NoticeLine
        notices={[
          frozen(),
          {
            id: 'monitoring-offer',
            tone: 'info',
            message: 'Get an email if this server stops',
            dismissible: true,
            dismiss: offer,
            priority: -100,
          },
        ]}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: '1 more notice' }));
    const panel = screen.getByRole('dialog', { name: 'More notices' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss' }));
    expect(offer).toHaveBeenCalledOnce();
  });

  it('keeps the focus in the line when the last notice in the popover goes', () => {
    render(
      <NoticeLine
        notices={[
          frozen(),
          {
            id: 'monitoring-offer',
            tone: 'info',
            message: 'Get an email if this server stops',
            dismissible: true,
            priority: -100,
          },
        ]}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: '1 more notice' }));
    const panel = screen.getByRole('dialog', { name: 'More notices' });
    const dismiss = within(panel).getByRole('button', { name: 'Dismiss' });
    act(() => dismiss.focus());
    fireEvent.click(dismiss);
    expect(screen.queryByRole('dialog', { name: 'More notices' })).toBeNull();
    expect(screen.getByTestId('notice-line')).toHaveFocus();
  });

  it('closes the popover when no other notice is left', () => {
    const view = render(<NoticeLine notices={[frozen(), draft()]} />);
    fireEvent.click(screen.getByRole('button', { name: '1 more notice' }));
    expect(screen.getByRole('dialog', { name: 'More notices' })).toBeVisible();

    view.rerender(<NoticeLine notices={[frozen()]} />);
    expect(screen.queryByRole('dialog', { name: 'More notices' })).toBeNull();
    expect(
      screen.queryByRole('button', { name: /more notices/ })
    ).not.toBeInTheDocument();
  });
});
