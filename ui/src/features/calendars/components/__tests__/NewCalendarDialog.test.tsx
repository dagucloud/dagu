// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NewCalendarDialog } from '../NewCalendarDialog';

const post = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: post }),
}));

function renderDialog(onCreated = vi.fn()) {
  render(
    <NewCalendarDialog
      open
      remoteNode="local"
      existingNames={['ops']}
      onClose={vi.fn()}
      onCreated={onCreated}
    />
  );
  return onCreated;
}

function submit(name: string) {
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: name } });
  fireEvent.click(screen.getByRole('button', { name: 'Create' }));
}

describe('NewCalendarDialog', () => {
  beforeEach(() => {
    post.mockReset();
  });

  it.each([
    ['bad name', 'Use letters, digits, - and _ only.'],
    ['weekdays', 'weekdays is reserved.'],
    ['OPS', 'A calendar named OPS already exists.'],
  ])('refuses %s before asking the server', (name, problem) => {
    renderDialog();
    submit(name);
    expect(screen.getByText(problem)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it('lets the user try again after the request fails', async () => {
    post.mockRejectedValueOnce(new Error('timeout'));
    renderDialog();

    submit('jp-banking');

    expect(
      await screen.findByText('Failed to create the calendar.')
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'jp-banking' },
    });
    expect(screen.getByRole('button', { name: 'Create' })).toBeEnabled();
  });

  it('creates the calendar and hands over its name', async () => {
    post.mockResolvedValue({ data: { name: 'jp-banking' } });
    const onCreated = renderDialog();

    submit('jp-banking');

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('jp-banking'));
    expect(post).toHaveBeenCalledWith('/calendars', {
      params: { query: { remoteNode: 'local' } },
      body: { name: 'jp-banking' },
    });
  });
});
