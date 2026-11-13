// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { filterSuggestions, MAX_SUGGESTIONS } from '../suggestions';

const models = [
  'claude-opus-5-5',
  'claude-sonnet-5-5',
  'claude-haiku-5',
  'gpt-5',
];

describe('filterSuggestions', () => {
  it('gives every name for an empty text, in the order of the list', () => {
    expect(filterSuggestions(models, '')).toEqual(models);
  });

  it('filters by the text, with no regard to case', () => {
    expect(filterSuggestions(models, 'SONNET')).toEqual(['claude-sonnet-5-5']);
    expect(filterSuggestions(models, 'claude')).toHaveLength(3);
  });

  it('gives nothing for a text that no name holds: the text stays free', () => {
    expect(filterSuggestions(models, 'my-own-model')).toEqual([]);
  });

  it('gives nothing for a reference, which the reference picker owns', () => {
    expect(filterSuggestions(models, '${params.MODEL}')).toEqual([]);
    expect(filterSuggestions(models, 'claude-${params.V}')).toEqual([]);
  });

  it('gives nothing when the only match is the text itself', () => {
    expect(filterSuggestions(models, 'gpt-5')).toEqual([]);
    expect(filterSuggestions(models, 'claude-haiku-5')).toEqual([]);
    expect(filterSuggestions([...models, 'gpt-5-mini'], 'gpt-5')).toEqual([
      'gpt-5',
      'gpt-5-mini',
    ]);
  });

  it('gives nothing without a list', () => {
    expect(filterSuggestions(undefined, 'x')).toEqual([]);
    expect(filterSuggestions([], '')).toEqual([]);
  });

  it('shows the first names of a long list only', () => {
    const long = Array.from({ length: 400 }, (_, i) => `model-${i}`);
    expect(filterSuggestions(long, '')).toHaveLength(MAX_SUGGESTIONS);
  });
});
