// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ValueReferenceNoticeReason } from '../../../../../api/v1/schema';
import { DEFECT_REASONS, REASON_LABELS } from '../ValueReferenceNoticesButton';

// A reason added to the API without a label here would render as a raw enum
// string, and one missing from DEFECT_REASONS would be misclassified whenever a
// response arrives without the class field.
describe('value reference notice reasons', () => {
  const reasons = Object.values(ValueReferenceNoticeReason);

  it('covers every reason with a readable label', () => {
    const unlabelled = reasons.filter((reason) => !REASON_LABELS[reason]);
    expect(unlabelled).toEqual([]);
  });

  it('classifies every reason as a defect or as runtime-only', () => {
    // Membership is the classification, so this only asserts the set stays a
    // subset of the reasons the API actually defines.
    const unknown = [...DEFECT_REASONS].filter(
      (reason) => !reasons.includes(reason as ValueReferenceNoticeReason)
    );
    expect(unknown).toEqual([]);
  });
});
