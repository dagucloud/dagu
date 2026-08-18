// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { combineQuestionAnswer } from '../agentSessionAnswers';

describe('combineQuestionAnswer', () => {
  it('keeps selected options and appends a custom multiple-choice answer', () => {
    expect(combineQuestionAnswer(['One', 'Two'], ' Three ', true)).toEqual([
      'One',
      'Two',
      'Three',
    ]);
  });

  it('uses a custom answer as the single-choice response', () => {
    expect(combineQuestionAnswer(['One'], 'Other', false)).toEqual(['Other']);
  });

  it('keeps selected options when the custom answer is blank', () => {
    expect(combineQuestionAnswer(['One'], '  ', true)).toEqual(['One']);
  });
});
