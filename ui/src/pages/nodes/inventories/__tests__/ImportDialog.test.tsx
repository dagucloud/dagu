// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ImportDialog } from '../ImportDialog';

const client = vi.hoisted(() => ({ POST: vi.fn() }));

vi.mock('@/hooks/api', () => ({ useClient: () => client }));

const OSAKA = [{ name: 'Osaka', nodes: [{ name: 'osaka-web01', host: 'h' }] }];

function renderDialog() {
  const onClose = vi.fn();
  const onImported = vi.fn();
  render(<ImportDialog onClose={onClose} onImported={onImported} />);
  return { onClose, onImported };
}

function type(text: string) {
  fireEvent.change(screen.getByLabelText('Inventories JSON'), {
    target: { value: text },
  });
}

async function click(name: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

describe('ImportDialog', () => {
  beforeEach(() => {
    client.POST.mockReset();
  });

  it('checks the import, then stores it', async () => {
    client.POST.mockResolvedValueOnce({
      data: { created: ['Osaka'], replaced: [], dryRun: true },
    }).mockResolvedValueOnce({
      data: { created: ['Osaka'], replaced: [], dryRun: false },
    });
    const { onClose, onImported } = renderDialog();

    type(JSON.stringify(OSAKA));
    const importButton = screen.getByRole('button', { name: 'Import' });
    expect(importButton).toBeDisabled();
    await click('Check');
    expect(screen.getByText('Would create: Osaka')).toBeInTheDocument();
    await click('Import');

    expect(client.POST.mock.calls.map(([path, init]) => [path, init])).toEqual([
      [
        '/inventory-imports',
        { params: { query: { replace: false, dryRun: true } }, body: OSAKA },
      ],
      [
        '/inventory-imports',
        { params: { query: { replace: false, dryRun: false } }, body: OSAKA },
      ],
    ]);
    expect(onImported).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('needs a new check after the text or the replace option changes', async () => {
    client.POST.mockResolvedValue({
      data: { created: [], replaced: ['Osaka'], dryRun: true },
    });
    renderDialog();

    type(JSON.stringify(OSAKA));
    fireEvent.click(
      screen.getByRole('checkbox', {
        name: 'Replace inventories with the same name',
      })
    );
    await click('Check');
    expect(client.POST.mock.calls[0]![1].params.query).toEqual({
      replace: true,
      dryRun: true,
    });
    const importButton = screen.getByRole('button', { name: 'Import' });
    expect(importButton).toBeEnabled();

    type(`${JSON.stringify(OSAKA)} `);
    expect(importButton).toBeDisabled();
  });

  it('needs a new check after the replace option changes, and passes it for the new option', async () => {
    client.POST.mockResolvedValue({
      data: { created: ['Osaka'], replaced: [], dryRun: true },
    });
    renderDialog();

    type(JSON.stringify(OSAKA));
    await click('Check');
    const importButton = screen.getByRole('button', { name: 'Import' });
    expect(importButton).toBeEnabled();

    const replace = screen.getByRole('checkbox', {
      name: 'Replace inventories with the same name',
    });
    fireEvent.click(replace);
    expect(importButton).toBeDisabled();
    await click('Check');
    expect(importButton).toBeEnabled();
    expect(client.POST.mock.calls[1]![1].params.query).toEqual({
      replace: true,
      dryRun: true,
    });
  });

  it('lists each rule the import breaks', async () => {
    client.POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message: 'nothing imported',
        errors: [
          {
            inventory: 'Osaka',
            node: 'osaka-web01',
            field: 'host',
            message: 'is required',
          },
        ],
      },
    });
    renderDialog();

    type(JSON.stringify(OSAKA));
    await click('Check');

    expect(screen.getByRole('alert')).toHaveTextContent(
      'node "osaka-web01": host: is required'
    );
    expect(screen.getByRole('button', { name: 'Import' })).toBeDisabled();
  });

  it('refuses text that is not a JSON array, before any request', async () => {
    renderDialog();

    type('{"name": "Osaka"}');
    await click('Check');

    expect(screen.getByRole('alert')).toHaveTextContent(
      'An import is a JSON array of inventories'
    );
    expect(client.POST).not.toHaveBeenCalled();
  });
});
