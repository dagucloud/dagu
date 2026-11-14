// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  SettingsLeaveGuardProvider,
  useSettingsLeaveGuard,
  type SettingsLeaveGuard,
} from '../useSettingsLeaveGuard';

function Local({ onGuard }: { onGuard: (guard: SettingsLeaveGuard) => void }) {
  const guard = useSettingsLeaveGuard();
  onGuard(guard);
  return <>{guard.confirmDialog}</>;
}

function renderLocal() {
  let current!: SettingsLeaveGuard;
  render(<Local onGuard={(guard) => (current = guard)} />);
  return () => current;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useSettingsLeaveGuard', () => {
  it('runs next at once when nothing is at risk', () => {
    const { result } = renderHook(() => useSettingsLeaveGuard());
    const next = vi.fn();

    act(() => result.current.guard(next));

    expect(next).toHaveBeenCalledTimes(1);
    expect(result.current.atRisk).toBe(false);
  });

  it('asks before leaving a dirty section and runs next on confirm', async () => {
    const user = userEvent.setup();
    const guard = renderLocal();
    const next = vi.fn();

    act(() => guard().setDirty('notifications', true));
    expect(guard().atRisk).toBe(true);
    act(() => guard().guard(next));

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Leave without saving?');
    expect(dialog).toHaveTextContent(
      'The changes in this section are not saved.'
    );
    expect(next).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole('button', { name: 'Leave' }));

    expect(next).toHaveBeenCalledTimes(1);
    expect(guard().atRisk).toBe(false);
  });

  // The runtime profile choice of the Overview tab is a draft like the others.
  it('asks before leaving an unsaved runtime profile choice', async () => {
    const user = userEvent.setup();
    const guard = renderLocal();
    const next = vi.fn();

    act(() => guard().setDirty('profile', true));
    expect(guard().atRisk).toBe(true);
    act(() => guard().guard(next));
    const dialog = await screen.findByRole('dialog');
    expect(next).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Leave' }));
    expect(next).toHaveBeenCalledTimes(1);
    expect(guard().atRisk).toBe(false);

    act(() => guard().setDirty('profile', true));
    act(() => guard().setDirty('profile', false));
    expect(guard().atRisk).toBe(false);
  });

  it('keeps the state on cancel', async () => {
    const user = userEvent.setup();
    const guard = renderLocal();
    const next = vi.fn();

    act(() => guard().setDirty('incidents', true));
    act(() => guard().guard(next));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(next).not.toHaveBeenCalled();
    expect(guard().atRisk).toBe(true);
  });

  // The safe choice has the focus, and the focus goes back on close.
  it('focuses Cancel and gives the focus back to the opener', async () => {
    const user = userEvent.setup();
    const guard = renderLocal();
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();

    act(() => guard().setDirty('incidents', true));
    act(() => guard().guard(vi.fn()));
    const dialog = await screen.findByRole('dialog');
    await waitFor(() =>
      expect(
        within(dialog).getByRole('button', { name: 'Cancel' })
      ).toHaveFocus()
    );

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(opener).toHaveFocus());
    opener.remove();
  });

  it('asks before hiding a one-time secret', async () => {
    const guard = renderLocal();
    const next = vi.fn();

    act(() => guard().setSecretVisible(true));
    act(() => guard().guard(next));

    expect(await screen.findByRole('dialog')).toHaveTextContent(
      'This secret is shown only once.'
    );
    expect(next).not.toHaveBeenCalled();

    act(() => guard().setSecretVisible(false));
    expect(guard().atRisk).toBe(false);
  });

  // Browser Back can unmount the section while the question is open.
  it('closes the question when the risk goes', async () => {
    const guard = renderLocal();
    const next = vi.fn();

    act(() => guard().setDirty('notifications', true));
    act(() => guard().guard(next));
    expect(await screen.findByRole('dialog')).toBeVisible();

    act(() => guard().setDirty('notifications', false));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(next).not.toHaveBeenCalled();
  });

  it('sets a beforeunload listener only while at risk', () => {
    const add = vi.spyOn(window, 'addEventListener');
    const remove = vi.spyOn(window, 'removeEventListener');
    const guard = renderLocal();
    const beforeUnloadCalls = (spy: typeof add) =>
      spy.mock.calls.filter(([type]) => type === 'beforeunload');

    expect(beforeUnloadCalls(add)).toHaveLength(0);

    act(() => guard().setDirty('notifications', true));
    expect(beforeUnloadCalls(add)).toHaveLength(1);
    const listener = beforeUnloadCalls(add)[0]![1] as EventListener;
    const event = new Event('beforeunload', { cancelable: true });
    listener(event);
    expect(event.defaultPrevented).toBe(true);

    act(() => guard().setDirty('notifications', false));
    expect(beforeUnloadCalls(remove).map(([, fn]) => fn)).toContain(listener);
  });

  // The settings dialog and other leave actions read the same guard. Two
  // consumers stand in for them here.
  it('shares one guard through SettingsLeaveGuardProvider', async () => {
    const user = userEvent.setup();
    const next = vi.fn();
    function Section() {
      const { setDirty } = useSettingsLeaveGuard();
      return (
        <button type="button" onClick={() => setDirty('notifications', true)}>
          edit
        </button>
      );
    }
    function ModeTab() {
      const { guard, confirmDialog } = useSettingsLeaveGuard();
      return (
        <>
          <button type="button" onClick={() => guard(next)}>
            executions
          </button>
          {confirmDialog}
        </>
      );
    }
    function MenuItem() {
      const { atRisk } = useSettingsLeaveGuard();
      return <output data-testid="menu-risk">{String(atRisk)}</output>;
    }
    render(
      <SettingsLeaveGuardProvider>
        <Section />
        <ModeTab />
        <MenuItem />
      </SettingsLeaveGuardProvider>
    );

    await user.click(screen.getByRole('button', { name: 'executions' }));
    expect(next).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole('button', { name: 'edit' }));
    expect(screen.getByTestId('menu-risk')).toHaveTextContent('true');
    await user.click(screen.getByRole('button', { name: 'executions' }));

    // The provider renders one confirmation; a consumer's confirmDialog is null.
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(next).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: 'Leave' }));
    expect(next).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('menu-risk')).toHaveTextContent('false');
  });

  describe('editor draft', () => {
    it('is ignored by guard and asked by guardLeave', async () => {
      const user = userEvent.setup();
      const guard = renderLocal();
      const stay = vi.fn();
      const leave = vi.fn();

      act(() => guard().setEditorDirty(true));
      act(() => guard().guard(stay));
      expect(stay).toHaveBeenCalledTimes(1);
      expect(guard().atRisk).toBe(false);

      act(() => guard().guardLeave(leave));
      const dialog = await screen.findByRole('dialog');
      expect(dialog).toHaveTextContent(
        'The workflow has unsaved changes. Leave and discard them?'
      );
      expect(leave).not.toHaveBeenCalled();
      await user.click(within(dialog).getByRole('button', { name: 'Leave' }));
      expect(leave).toHaveBeenCalledTimes(1);
    });

    it('lets guardLeave run at once when the draft is clean', () => {
      const guard = renderLocal();
      const leave = vi.fn();
      act(() => guard().guardLeave(leave));
      expect(leave).toHaveBeenCalledTimes(1);
    });

    it('keeps the draft at risk after a confirmed guard that stays', async () => {
      const user = userEvent.setup();
      const guard = renderLocal();
      act(() => {
        guard().setEditorDirty(true);
        guard().setDirty('notifications', true);
      });
      act(() => guard().guard(vi.fn()));
      await user.click(await screen.findByRole('button', { name: 'Leave' }));
      const leave = vi.fn();
      act(() => guard().guardLeave(leave));
      expect(await screen.findByRole('dialog')).toBeInTheDocument();
      expect(leave).not.toHaveBeenCalled();
    });

    // The question of guard is about the sections: it closes when they are
    // clean again, though the editor draft stays.
    it('closes the question of guard when only the draft is left', async () => {
      const guard = renderLocal();
      const next = vi.fn();
      act(() => {
        guard().setEditorDirty(true);
        guard().setDirty('notifications', true);
      });
      act(() => guard().guard(next));
      expect(await screen.findByRole('dialog')).toBeVisible();

      act(() => guard().setDirty('notifications', false));

      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      expect(next).not.toHaveBeenCalled();
    });

    it('stops an unload while the draft is dirty', () => {
      const guard = renderLocal();
      const unload = () => {
        const event = new Event('beforeunload', { cancelable: true });
        window.dispatchEvent(event);
        return event.defaultPrevented;
      };
      expect(unload()).toBe(false);
      act(() => guard().setEditorDirty(true));
      expect(unload()).toBe(true);
      act(() => guard().setEditorDirty(false));
      expect(unload()).toBe(false);
    });
  });
});
