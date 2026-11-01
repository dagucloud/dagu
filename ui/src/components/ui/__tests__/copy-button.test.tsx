// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CopyButton } from '@/components/ui/copy-button';

const copyText = vi.hoisted(() => vi.fn());
vi.mock('@/lib/clipboard', () => ({ copyText }));

async function click(name: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

describe('CopyButton', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    copyText.mockReset();
    copyText.mockResolvedValue(true);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('copies, shows Check for 2 s, then returns', async () => {
    render(<CopyButton text="abc" label="Copy id" />);
    const idle = screen.getByRole('button', { name: 'Copy id' });
    const idleIcon = idle.innerHTML;
    const idleClass = idle.className;

    await click('Copy id');
    expect(copyText).toHaveBeenCalledWith('abc');

    const copied = screen.getByRole('button', { name: 'Copied' });
    expect(copied.innerHTML).not.toBe(idleIcon);
    expect(copied.querySelector('svg')).toHaveClass('text-success');
    expect(copied.className).toBe(idleClass);

    await act(async () => {
      vi.advanceTimersByTime(1999);
    });
    expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(1);
    });
    const back = screen.getByRole('button', { name: 'Copy id' });
    expect(back.innerHTML).toBe(idleIcon);
  });

  it('uses the copied label that the caller gives', async () => {
    render(<CopyButton text="abc" label="Copy" copiedLabel="Done" />);
    await click('Copy');
    expect(screen.getByRole('button', { name: 'Done' })).toBeInTheDocument();
  });

  it('reads an async text function when clicked', async () => {
    const text = vi.fn(async () => 'later');
    render(<CopyButton text={text} label="Copy" />);
    expect(text).not.toHaveBeenCalled();
    await click('Copy');
    expect(copyText).toHaveBeenCalledWith('later');
  });

  it('stays idle when the copy fails', async () => {
    copyText.mockResolvedValue(false);
    render(<CopyButton text="abc" label="Copy" />);
    await click('Copy');
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument();
  });

  it('stays idle when the text function rejects', async () => {
    const text = vi.fn(async (): Promise<string> => {
      throw new Error('no link');
    });
    render(<CopyButton text={text} label="Copy" />);
    await click('Copy');
    expect(copyText).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument();
  });
});
