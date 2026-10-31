// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useLegacyRedirect } from '../useLegacyRedirect';

const navigate = vi.fn();

vi.mock('react-router-dom', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router-dom')>()),
  useNavigate: () => navigate,
}));

type Props = { tab?: string; dagName: string | null };

function renderRedirect(
  entry: string,
  initial: Props,
  appBarRemoteNode = 'local'
) {
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <MemoryRouter initialEntries={[entry]}>
      <AppBarContext.Provider
        value={{ selectedRemoteNode: appBarRemoteNode } as never}
      >
        {children}
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return renderHook(
    ({ tab, dagName }: Props) => useLegacyRedirect('flow.yaml', tab, dagName),
    { wrapper, initialProps: initial }
  );
}

// parity:shell-run-redirect
// parity:shell-docs-redirect
// parity:actions-retry
describe('useLegacyRedirect', () => {
  beforeEach(() => {
    navigate.mockClear();
  });

  it.each([
    [
      'a redirect',
      '/dags/flow.yaml/docs',
      'docs',
      '/dags/flow.yaml?panel=workflow&wtab=wiki',
    ],
    [
      'a run page',
      '/dags/flow.yaml?dagRunId=X',
      undefined,
      '/dags/flow.yaml?remoteNode=local&run=X',
    ],
    [
      'a history run link (R7)',
      '/dags/flow.yaml/history?dagRunId=X&remoteNode=edge',
      'history',
      '/dags/flow.yaml?run=X&remoteNode=edge',
    ],
    [
      'a history link (R9)',
      '/dags/flow.yaml/history',
      'history',
      '/dags/flow.yaml?dock=history',
    ],
  ])('stays pending on %s and navigates once', (_, entry, tab, to) => {
    const { result, rerender } = renderRedirect(entry, {
      tab,
      dagName: 'flow',
    });

    expect(result.current).toBe('pending');
    rerender({ tab, dagName: 'flow' });
    expect(result.current).toBe('pending');
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith(to, { replace: true });
  });

  it('waits for the workflow name before the run page (R1)', () => {
    const { result, rerender } = renderRedirect('/dags/flow.yaml?dagRunId=X', {
      dagName: null,
    });

    expect(result.current).toBe('pending');
    expect(navigate).not.toHaveBeenCalled();

    rerender({ dagName: 'flow' });

    expect(result.current).toBe('pending');
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith(
      '/dags/flow.yaml?remoteNode=local&run=X',
      { replace: true }
    );
  });

  it('does not wait when the URL names the run root', () => {
    renderRedirect('/dags/flow.yaml?dagRunId=X&dagRunName=root', {
      dagName: null,
    });

    expect(navigate).toHaveBeenCalledWith('/dags/root?remoteNode=local&run=X', {
      replace: true,
    });
  });

  it('gives a history index to the workspace (R8)', () => {
    const { result } = renderRedirect('/dags/flow.yaml/history?idx=3', {
      tab: 'history',
      dagName: 'flow',
    });

    expect(result.current).toEqual({ needsHistory: 3 });
    expect(navigate).not.toHaveBeenCalled();
  });

  it('settles on the current link', () => {
    const { result } = renderRedirect('/dags/flow.yaml?run=X', {
      tab: undefined,
      dagName: 'flow',
    });

    expect(result.current).toBe('settled');
    expect(navigate).not.toHaveBeenCalled();
  });

  it('redirects /executions to the workflow page, with replace', () => {
    const { result } = renderRedirect(
      '/dags/flow.yaml/executions?run=X&step=y',
      { tab: 'executions', dagName: 'flow' }
    );

    expect(result.current).toBe('pending');
    expect(navigate).toHaveBeenCalledWith('/dags/flow.yaml?run=X&step=y', {
      replace: true,
    });
  });

  it('gives an /executions index to the workspace', () => {
    const { result } = renderRedirect('/dags/flow.yaml/executions?idx=3', {
      tab: 'executions',
      dagName: 'flow',
    });

    expect(result.current).toEqual({ needsHistory: 3 });
    expect(navigate).not.toHaveBeenCalled();
  });

  it('keeps the app bar node when the URL names none', () => {
    renderRedirect(
      '/dags/flow.yaml/docs',
      { tab: 'docs', dagName: 'flow' },
      'edge'
    );

    expect(navigate).toHaveBeenCalledWith(
      '/dags/flow.yaml?remoteNode=edge&panel=workflow&wtab=wiki',
      { replace: true }
    );
  });
});
