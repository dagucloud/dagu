// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  openStepInLastMode,
  readStepViewMode,
  useStepViewForm,
} from '../stepViewMode';

const KEY = 'dagu.stepView.mode';

beforeEach(() => sessionStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe('readStepViewMode', () => {
  it('is the YAML until the form is used', () => {
    expect(readStepViewMode()).toBe('yaml');
    sessionStorage.setItem(KEY, 'form');
    expect(readStepViewMode()).toBe('form');
    sessionStorage.setItem(KEY, 'other');
    expect(readStepViewMode()).toBe('yaml');
  });

  it('is the YAML when the storage cannot be read', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(readStepViewMode()).toBe('yaml');
  });
});

describe('useStepViewForm', () => {
  it('writes each change while the step view is open, and only then', () => {
    const { result, rerender } = renderHook(
      ({ open }) => useStepViewForm(open),
      { initialProps: { open: false } }
    );
    act(() => result.current[1](true));
    expect(sessionStorage.getItem(KEY)).toBeNull();

    rerender({ open: true });
    expect(sessionStorage.getItem(KEY)).toBe('form');
    act(() => result.current[1](false));
    expect(sessionStorage.getItem(KEY)).toBe('yaml');
  });

  it('starts in the last mode for an open view, on the text for the file', () => {
    sessionStorage.setItem(KEY, 'form');
    expect(renderHook(() => useStepViewForm(true)).result.current[0]).toBe(
      true
    );
    expect(renderHook(() => useStepViewForm(false)).result.current[0]).toBe(
      false
    );
  });

  it('works without a storage', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const { result } = renderHook(() => useStepViewForm(true));
    expect(() => act(() => result.current[1](true))).not.toThrow();
    expect(result.current[0]).toBe(true);
  });
});

describe('openStepInLastMode', () => {
  const handle = () => ({
    openForm: vi.fn(() => true),
    openYaml: vi.fn(() => true),
  });

  it('opens the YAML at first and the form after the form', () => {
    const h = handle();
    expect(openStepInLastMode(h, 'a')).toBe(true);
    expect(h.openYaml).toHaveBeenCalledWith('a');
    expect(h.openForm).not.toHaveBeenCalled();

    sessionStorage.setItem(KEY, 'form');
    expect(openStepInLastMode(h, 'b')).toBe(true);
    expect(h.openForm).toHaveBeenCalledWith('b');
  });

  it('falls back to the YAML for a step without a form', () => {
    sessionStorage.setItem(KEY, 'form');
    const h = { ...handle(), openForm: vi.fn(() => false) };
    expect(openStepInLastMode(h, 'a')).toBe(true);
    expect(h.openYaml).toHaveBeenCalledWith('a');
  });
});
