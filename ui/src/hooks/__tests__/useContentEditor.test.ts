// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useContentEditor } from '../useContentEditor';

interface HookProps {
  editorKey: string;
  serverContent: string;
}

function renderEditorHook(initialProps: HookProps) {
  return renderHook(
    ({ editorKey, serverContent }: HookProps) =>
      useContentEditor({ key: editorKey, serverContent }),
    { initialProps }
  );
}

describe('useContentEditor', () => {
  it('accepts the server echo of a pending save without a conflict', () => {
    const { result, rerender } = renderEditorHook({
      editorKey: 'doc',
      serverContent: '',
    });

    act(() => {
      result.current.setCurrentValue('# Saved');
      result.current.beginSave('# Saved');
    });

    rerender({ editorKey: 'doc', serverContent: '# Saved' });

    expect(result.current.conflict.hasConflict).toBe(false);
    expect(result.current.hasUnsavedChanges).toBe(false);

    act(() => {
      result.current.setCurrentValue('# Saved again');
    });
    expect(result.current.hasUnsavedChanges).toBe(true);

    act(() => {
      result.current.markAsSaved('# Saved again');
    });
    rerender({ editorKey: 'doc', serverContent: '# Saved' });

    expect(result.current.hasUnsavedChanges).toBe(false);
  });

  it('detects an external change after a pending save is cancelled', () => {
    const { result, rerender } = renderEditorHook({
      editorKey: 'doc',
      serverContent: '# Original',
    });

    act(() => {
      result.current.setCurrentValue('# Local edit');
      result.current.beginSave('# Local edit');
      result.current.cancelSave();
    });

    rerender({ editorKey: 'doc', serverContent: '# External edit' });

    expect(result.current.currentValue).toBe('# Local edit');
    expect(result.current.conflict).toEqual({
      hasConflict: true,
      externalContent: '# External edit',
    });
  });

  it.each([
    { label: 'empty', serverContent: '' },
    { label: 'non-empty', serverContent: '# Same' },
  ])(
    'initializes $label content when the editor key changes',
    ({ serverContent }) => {
      const { result, rerender } = renderEditorHook({
        editorKey: 'first',
        serverContent,
      });

      expect(result.current.currentValue).toBe(serverContent);

      act(() => {
        result.current.setCurrentValue('# First document edit');
      });
      expect(result.current.hasUnsavedChanges).toBe(true);

      rerender({ editorKey: 'second', serverContent });

      expect(result.current.currentValue).toBe(serverContent);
      expect(result.current.hasUnsavedChanges).toBe(false);
    }
  );

  it('preserves edits made while a save is pending', () => {
    const { result, rerender } = renderEditorHook({
      editorKey: 'doc',
      serverContent: '# Original',
    });

    act(() => {
      result.current.setCurrentValue('# Saved');
      result.current.beginSave('# Saved');
    });
    act(() => {
      result.current.setCurrentValue('# Newer edit');
    });
    act(() => {
      result.current.markAsSaved('# Saved');
    });

    rerender({ editorKey: 'doc', serverContent: '# Saved' });

    expect(result.current.currentValue).toBe('# Newer edit');
    expect(result.current.hasUnsavedChanges).toBe(true);
    expect(result.current.conflict.hasConflict).toBe(false);

    rerender({ editorKey: 'doc', serverContent: '# External edit' });

    expect(result.current.currentValue).toBe('# Newer edit');
    expect(result.current.conflict).toEqual({
      hasConflict: true,
      externalContent: '# External edit',
    });
  });
});
