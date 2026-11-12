// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { isSameYaml } from '@/features/visual-editor/doc/format';
import { useContentEditor } from '../useContentEditor';

const saved = 'name: demo\nsteps:\n  - name: a\n    run: echo a\n';

function setup() {
  return renderHook(
    (props: { serverContent: string }) =>
      useContentEditor({
        key: 'demo',
        serverContent: props.serverContent,
        isSame: isSameYaml,
      }),
    { initialProps: { serverContent: saved } }
  );
}

describe('useContentEditor with a semantic comparison', () => {
  it('is not dirty after a blank line or a trailing space', () => {
    const { result } = setup();
    expect(result.current.currentValue).toBe(saved);

    act(() => result.current.setCurrentValue(saved + '\n\n'));
    expect(result.current.hasUnsavedChanges).toBe(false);
    act(() => result.current.setCurrentValue(saved.replace('demo', 'demo  ')));
    expect(result.current.hasUnsavedChanges).toBe(false);
    act(() => result.current.setCurrentValue(saved.replace(/\n/g, '\r\n')));
    expect(result.current.hasUnsavedChanges).toBe(false);
  });

  it('is dirty after a real change, and clean again when it is undone', () => {
    const { result } = setup();

    act(() => result.current.setCurrentValue(saved + '# note\n'));
    expect(result.current.hasUnsavedChanges).toBe(true);
    act(() => result.current.setCurrentValue(saved + '\n'));
    expect(result.current.hasUnsavedChanges).toBe(false);
  });

  it('updates the buffer without a conflict when only blank lines differ', () => {
    const { result, rerender } = setup();
    act(() => result.current.setCurrentValue(saved + '\n\n'));

    const external = saved.replace('echo a', 'echo b');
    rerender({ serverContent: external });

    expect(result.current.conflict.hasConflict).toBe(false);
    expect(result.current.currentValue).toBe(external);
  });

  it('keeps the strict comparison without isSame', () => {
    const { result } = renderHook(() =>
      useContentEditor({ key: 'k', serverContent: saved })
    );
    act(() => result.current.setCurrentValue(saved + '\n'));
    expect(result.current.hasUnsavedChanges).toBe(true);
  });
});
