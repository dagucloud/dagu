// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useUnsavedChangesWarning } from '../useUnsavedChangesWarning';

function Guard({
  enabled = true,
  navigationBlocked = false,
}: {
  enabled?: boolean;
  navigationBlocked?: boolean;
}) {
  useUnsavedChangesWarning(enabled, 'Discard changes?', navigationBlocked);
  return null;
}

describe('useUnsavedChangesWarning', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('restores canceled browser history navigation before the router sees it', () => {
    window.history.replaceState({ idx: 5 }, '');
    const routerListener = vi.fn();
    window.addEventListener('popstate', routerListener);
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const view = render(<Guard />);

    window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 4 } }));

    expect(go).toHaveBeenCalledWith(1);
    expect(routerListener).not.toHaveBeenCalled();

    window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 5 } }));
    expect(routerListener).not.toHaveBeenCalled();

    view.unmount();
    window.removeEventListener('popstate', routerListener);
  });

  it('allows confirmed browser history navigation', () => {
    window.history.replaceState({ idx: 5 }, '');
    const routerListener = vi.fn();
    window.addEventListener('popstate', routerListener);
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const view = render(<Guard />);

    window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 4 } }));

    expect(go).not.toHaveBeenCalled();
    expect(routerListener).toHaveBeenCalledOnce();

    view.unmount();
    window.removeEventListener('popstate', routerListener);
  });

  it('restores blocked browser history navigation without prompting', () => {
    window.history.replaceState({ idx: 5 }, '');
    const routerListener = vi.fn();
    window.addEventListener('popstate', routerListener);
    const go = vi.spyOn(window.history, 'go').mockImplementation(() => {});
    const confirm = vi.spyOn(window, 'confirm');
    const view = render(<Guard navigationBlocked />);

    window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 4 } }));

    expect(go).toHaveBeenCalledWith(1);
    expect(confirm).not.toHaveBeenCalled();
    expect(routerListener).not.toHaveBeenCalled();

    view.unmount();
    window.removeEventListener('popstate', routerListener);
  });
});
