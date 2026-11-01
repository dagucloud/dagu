// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  readFavouriteActions,
  readRecentActions,
  toggleFavouriteAction,
  withRecentAction,
  writeFavouriteActions,
  writeRecentActions,
} from '../recentActions';

describe('recent actions', () => {
  beforeEach(() => localStorage.clear());

  it('puts the newest action first, once, and keeps six', () => {
    expect(withRecentAction(['a', 'b'], 'c')).toEqual(['c', 'a', 'b']);
    expect(withRecentAction(['a', 'b'], 'b')).toEqual(['b', 'a']);
    expect(withRecentAction(['1', '2', '3', '4', '5', '6'], '7')).toEqual([
      '7',
      '1',
      '2',
      '3',
      '4',
      '5',
    ]);
  });

  it('keeps the list for the next visit', () => {
    writeRecentActions(['run', 'http.request']);
    expect(readRecentActions()).toEqual(['run', 'http.request']);
  });

  it('reads nothing from a missing, broken or wrong value', () => {
    expect(readRecentActions()).toEqual([]);
    localStorage.setItem('dagu.visualEditor.recentActions', '{oops');
    expect(readRecentActions()).toEqual([]);
    localStorage.setItem('dagu.visualEditor.recentActions', '{"a":1}');
    expect(readRecentActions()).toEqual([]);
    localStorage.setItem('dagu.visualEditor.recentActions', '["a",3,null]');
    expect(readRecentActions()).toEqual(['a']);
  });

  it('works when the browser refuses its storage', () => {
    const get = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const set = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    expect(readRecentActions()).toEqual([]);
    expect(() => writeRecentActions(['run'])).not.toThrow();
    get.mockRestore();
    set.mockRestore();
  });
});

// parity:add-step-panel
describe('favourite actions', () => {
  const KEY = 'dagu.visualEditor.favouriteActions';
  beforeEach(() => localStorage.clear());

  it('adds an action at the end and removes it on a second toggle', () => {
    expect(toggleFavouriteAction(['a'], 'b')).toEqual(['a', 'b']);
    expect(toggleFavouriteAction(['a', 'b'], 'a')).toEqual(['b']);
  });

  it('keeps the favourites under their own key', () => {
    writeFavouriteActions(['http.request', 'run']);
    expect(localStorage.getItem(KEY)).toBe('["http.request","run"]');
    expect(readFavouriteActions()).toEqual(['http.request', 'run']);
    expect(readRecentActions()).toEqual([]);
  });

  it('reads nothing from a missing, broken or wrong value', () => {
    expect(readFavouriteActions()).toEqual([]);
    localStorage.setItem(KEY, '{oops');
    expect(readFavouriteActions()).toEqual([]);
    localStorage.setItem(KEY, '{"a":1}');
    expect(readFavouriteActions()).toEqual([]);
    localStorage.setItem(KEY, '["a",3,"a",null]');
    expect(readFavouriteActions()).toEqual(['a']);
  });

  it('works when the browser refuses its storage', () => {
    const get = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const set = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    expect(readFavouriteActions()).toEqual([]);
    expect(() => writeFavouriteActions(['run'])).not.toThrow();
    get.mockRestore();
    set.mockRestore();
  });
});
