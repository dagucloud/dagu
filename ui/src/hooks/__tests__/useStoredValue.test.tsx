// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStoredValue } from '../useStoredValue';

function Reader({ storageKey }: { storageKey: string }) {
  const [value, setValue] = useStoredValue(storageKey);
  return (
    <button type="button" onClick={() => setValue('dismissed')}>
      {value ?? 'unset'}
    </button>
  );
}

function values(): (string | null)[] {
  return screen.getAllByRole('button').map((button) => button.textContent);
}

describe('useStoredValue', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('updates every reader of the key at once', () => {
    render(
      <>
        <Reader storageKey="offer:local" />
        <Reader storageKey="offer:local" />
        <Reader storageKey="offer:edge" />
      </>
    );

    fireEvent.click(screen.getAllByRole('button')[0]!);

    expect(values()).toEqual(['dismissed', 'dismissed', 'unset']);
    expect(localStorage.getItem('offer:local')).toBe('dismissed');
  });

  // Privacy-restricted browsers reject writes; a dismissal must still apply
  // for the rest of the page's life.
  it('keeps a value the browser refuses to store', () => {
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new DOMException('blocked', 'SecurityError');
      });
    try {
      render(
        <>
          <Reader storageKey="offer:blocked" />
          <Reader storageKey="offer:blocked" />
        </>
      );

      fireEvent.click(screen.getAllByRole('button')[0]!);

      expect(values()).toEqual(['dismissed', 'dismissed']);
    } finally {
      setItem.mockRestore();
    }
  });

  it('follows a value stored in another tab', () => {
    render(<Reader storageKey="offer:local" />);

    act(() => {
      localStorage.setItem('offer:local', 'dismissed');
      window.dispatchEvent(new StorageEvent('storage', { key: 'offer:local' }));
    });

    expect(values()).toEqual(['dismissed']);
  });
});
