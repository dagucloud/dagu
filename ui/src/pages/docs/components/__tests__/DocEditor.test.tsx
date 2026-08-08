// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DocTabProvider, useDocTabContext } from '@/contexts/DocTabContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import DocEditor from '../DocEditor';

const testState = vi.hoisted(() => ({
  doc: {
    content: 'server content',
    title: 'Runbook',
  },
  mutate: vi.fn(),
}));

vi.mock('@/components/editors/MarkdownEditor', () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (value: string) => void;
  }) => (
    <textarea
      aria-label="Markdown editor"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
}));

vi.mock('@/components/ui/doc-markdown-preview', () => ({
  DocMarkdownPreview: () => null,
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({}),
  useQuery: () => ({ data: testState.doc, mutate: testState.mutate }),
}));

vi.mock('@/hooks/useDocSSE', () => ({
  useDocSSE: () => ({}),
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => undefined,
}));

vi.mock('../DocHistoryModal', () => ({
  DocHistoryModal: () => null,
}));

vi.mock('../DocExternalChangeDialog', () => ({
  default: ({
    visible,
    onDiscard,
  }: {
    visible: boolean;
    onDiscard: () => void;
  }) =>
    visible ? (
      <button type="button" onClick={onDiscard}>
        Discard conflict
      </button>
    ) : null,
}));

const storageKey = 'dagu_doc_tabs:doc-editor-test';

function EditorHarness() {
  const { tabs, openDoc } = useDocTabContext();

  useEffect(() => {
    if (tabs.length === 0) openDoc('runbook', 'Runbook');
  }, [openDoc, tabs.length]);

  const tab = tabs[0];
  return tab ? <DocEditor tabId={tab.id} docPath={tab.docPath} /> : null;
}

function renderEditor() {
  return render(
    <UnsavedChangesProvider>
      <DocTabProvider storageKey={storageKey}>
        <EditorHarness />
      </DocTabProvider>
    </UnsavedChangesProvider>
  );
}

describe('DocEditor draft persistence', () => {
  beforeEach(() => {
    localStorage.clear();
    testState.doc = {
      content: 'server content',
      title: 'Runbook',
    };
    testState.mutate.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not restore a persisted draft after discarding a conflict', () => {
    const view = renderEditor();
    const editor = screen.getByLabelText('Markdown editor');

    fireEvent.change(editor, { target: { value: 'local draft' } });
    act(() => vi.advanceTimersByTime(300));

    let stored = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as {
      drafts?: [string, string][];
    };
    expect(stored.drafts?.map(([, draft]) => draft)).toContain('local draft');

    testState.doc = { ...testState.doc, content: 'external content' };
    view.rerender(
      <UnsavedChangesProvider>
        <DocTabProvider storageKey={storageKey}>
          <EditorHarness />
        </DocTabProvider>
      </UnsavedChangesProvider>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Discard conflict' }));

    stored = JSON.parse(localStorage.getItem(storageKey) ?? '{}') as {
      drafts?: [string, string][];
    };
    expect(stored.drafts).toEqual([]);

    view.unmount();
    renderEditor();
    expect(screen.getByLabelText('Markdown editor')).toHaveValue(
      'external content'
    );
  });
});
