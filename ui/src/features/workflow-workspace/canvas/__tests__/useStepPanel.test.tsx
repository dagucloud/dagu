// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useStepPanel } from '../useStepPanel';

type Props = { step: string | null; covered: boolean };

function render(initial: Props) {
  const zoneRef = { current: null };
  return renderHook(
    ({ step, covered }: Props) =>
      useStepPanel({ zoneRef, step, covered, linked: false, active: true }),
    { initialProps: initial }
  );
}

describe('useStepPanel', () => {
  // The click on a step can also close a trigger panel: the zone is free
  // before the step is in the URL. The panel still opens on that step.
  it('opens on a click whose step comes after the zone is free', () => {
    const view = render({ step: null, covered: true });
    act(() => view.result.current.show({ x: 1, y: 1 }));
    view.rerender({ step: null, covered: false });
    view.rerender({ step: 'extract', covered: false });
    expect(view.result.current.shown).toBe(true);
  });

  it('closes when the step is cleared', () => {
    const view = render({ step: 'extract', covered: false });
    act(() => view.result.current.show({ x: 1, y: 1 }));
    expect(view.result.current.shown).toBe(true);
    view.rerender({ step: null, covered: false });
    view.rerender({ step: 'extract', covered: false });
    expect(view.result.current.shown).toBe(false);
  });
});
