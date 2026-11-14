// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { catalogSample } from '../../testing/fixtures';
import { searchCatalog } from '../catalogSearch';

const names = (query: string) =>
  searchCatalog(catalogSample, query).map((e) => e.name);

describe('searchCatalog', () => {
  it.each([
    ['', ['run', 'http.request', 'jq.filter', 'router.route', 'send-slack']],
    ['   ', ['run', 'http.request', 'jq.filter', 'router.route', 'send-slack']],
    ['http', ['http.request']],
    ['HTTP REQ', ['http.request']],
    ['json', ['jq.filter']],
    ['custom', ['send-slack']],
    ['core', ['run']],
    ['send request', ['http.request']],
    ['nothing-matches', []],
  ])('%j gives %j', (query, expected) => {
    expect(names(query)).toEqual(expected);
  });

  it('puts name-prefix matches first', () => {
    // "r" occurs in four entries; "run" and "router.route" start with it.
    expect(names('r')).toEqual([
      'run',
      'router.route',
      'http.request',
      'jq.filter',
    ]);
  });
});
