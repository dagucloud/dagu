// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import DAGPinButton from '../DAGPinButton';

const mocks = vi.hoisted(() => ({
  put: vi.fn(),
  del: vi.fn(),
  showError: vi.fn(),
  canWrite: true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: mocks.put, DELETE: mocks.del }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => mocks.canWrite,
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

const request = {
  params: { path: { fileName: 'etl' }, query: { remoteNode: 'remote-a' } },
};

function renderButton(pinned: boolean, onChanged = vi.fn()) {
  render(
    <RemoteNodeProvider remoteNode="remote-a">
      <DAGPinButton
        fileName="etl"
        name="etl"
        pinned={pinned}
        workspace=""
        onChanged={onChanged}
      />
    </RemoteNodeProvider>
  );
  return { onChanged };
}

function pinButton() {
  return screen.getByRole('button', { name: 'Pin workflow etl' });
}

describe('DAGPinButton', () => {
  beforeEach(() => {
    mocks.put.mockReset().mockResolvedValue({});
    mocks.del.mockReset().mockResolvedValue({});
    mocks.showError.mockReset();
    mocks.canWrite = true;
  });

  it('pins the workflow', async () => {
    const { onChanged } = renderButton(false);

    await act(async () => {
      fireEvent.click(pinButton());
    });

    expect(mocks.put).toHaveBeenCalledWith('/dags/{fileName}/pin', request);
    expect(pinButton()).toHaveAttribute('aria-pressed', 'true');
    expect(onChanged).toHaveBeenCalled();
  });

  it('unpins the workflow', async () => {
    renderButton(true);

    await act(async () => {
      fireEvent.click(pinButton());
    });

    expect(mocks.del).toHaveBeenCalledWith('/dags/{fileName}/pin', request);
    expect(pinButton()).toHaveAttribute('aria-pressed', 'false');
  });

  it('restores the pin state when the request fails', async () => {
    mocks.put.mockResolvedValue({ error: { message: 'denied' } });
    const { onChanged } = renderButton(false);

    await act(async () => {
      fireEvent.click(pinButton());
    });

    expect(pinButton()).toHaveAttribute('aria-pressed', 'false');
    expect(mocks.showError).toHaveBeenCalledWith('denied', expect.any(String));
    expect(onChanged).not.toHaveBeenCalled();
  });

  it('shows a marker without a button for read-only users', () => {
    mocks.canWrite = false;
    renderButton(true);

    expect(screen.getByRole('img', { name: 'Pinned' })).toBeVisible();
    expect(
      screen.queryByRole('button', { name: 'Pin workflow etl' })
    ).not.toBeInTheDocument();
  });
});
