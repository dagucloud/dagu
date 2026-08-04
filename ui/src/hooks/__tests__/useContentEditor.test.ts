// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useContentEditor } from '../useContentEditor';

describe('useContentEditor', () => {
  it('accepts the server echo of a pending save without a conflict', () => {
    const { result, rerender } = renderHook(
      ({ serverContent }) => useContentEditor({ key: 'doc', serverContent }),
      { initialProps: { serverContent: '' } }
    );

    act(() => {
      result.current.setCurrentValue('# Saved');
      result.current.beginSave('# Saved');
    });

    rerender({ serverContent: '# Saved' });

    expect(result.current.conflict.hasConflict).toBe(false);

    act(() => {
      result.current.markAsSaved('# Saved');
    });
    rerender({ serverContent: '# Saved' });

    expect(result.current.hasUnsavedChanges).toBe(false);
  });
});
