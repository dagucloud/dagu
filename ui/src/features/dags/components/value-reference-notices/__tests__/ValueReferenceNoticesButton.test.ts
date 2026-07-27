// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValueReferenceNoticeClass,
  ValueReferenceNoticeReason,
} from '../../../../../api/v1/schema';
import {
  DEFECT_REASONS,
  isDefect,
  REASON_LABELS,
} from '../ValueReferenceNoticesButton';

// Every reason needs a readable label. DEFECT_REASONS covers reasons that are
// always defects when a response arrives without the class field.
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

  it('classifies old-server step-output namespace notices as defects', () => {
    expect(
      isDefect({
        message: 'Step outputs are unavailable',
        reason: ValueReferenceNoticeReason.namespace_unavailable,
        token: '${steps.build.outputs.image}',
      })
    ).toBe(true);
    expect(
      isDefect({
        message: 'Run context is unavailable',
        reason: ValueReferenceNoticeReason.namespace_unavailable,
        token: '${context.run.id}',
      })
    ).toBe(false);
  });

  it('prefers the server class over fallback classification', () => {
    expect(
      isDefect({
        message: 'Runtime-only value',
        class: ValueReferenceNoticeClass.runtime_only,
        reason: ValueReferenceNoticeReason.namespace_unavailable,
        token: '${steps.build.outputs.image}',
      })
    ).toBe(false);
  });
});
