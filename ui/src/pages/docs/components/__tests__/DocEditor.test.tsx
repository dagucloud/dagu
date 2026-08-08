// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DocTabProvider, useDocTabContext } from '@/contexts/DocTabContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import { attachmentUploadName } from '../../lib/doc-attachments';
import DocEditor from '../DocEditor';

const testState = vi.hoisted(() => ({
  doc: {
    content: 'server content',
    title: 'Runbook',
  },
  mutate: vi.fn(),
  put: vi.fn(),
}));

vi.mock('@/components/editors/MarkdownEditor', async () => {
  const React = await import('react');
  return {
    default: ({
      value,
      onChange,
      onEditorMount,
    }: {
      value: string;
      onChange: (value: string) => void;
      onEditorMount?: (editor: {
        getContainerDomNode: () => HTMLDivElement;
        getSelection: () => null;
        onDidDispose: (callback: () => void) => void;
      }) => void;
    }) => {
      const containerRef = React.useRef<HTMLDivElement>(null);
      const initialOnEditorMount = React.useRef(onEditorMount);
      React.useEffect(() => {
        const disposeCallbacks: Array<() => void> = [];
        initialOnEditorMount.current?.({
          getContainerDomNode: () => containerRef.current!,
          getSelection: () => null,
          onDidDispose: (callback) => disposeCallbacks.push(callback),
        });
        return () => disposeCallbacks.forEach((callback) => callback());
      }, []);
      return (
        <div ref={containerRef} data-testid="markdown-editor-container">
          <textarea
            aria-label="Markdown editor"
            value={value}
            onChange={(event) => onChange(event.target.value)}
          />
        </div>
      );
    },
  };
});

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
  useClient: () => ({ PUT: testState.put }),
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

function EditorHarness({ docPath = 'runbook' }: { docPath?: string }) {
  const { tabs, openDoc } = useDocTabContext();

  useEffect(() => {
    if (tabs.length === 0) openDoc('runbook', 'Runbook');
  }, [openDoc, tabs.length]);

  const tab = tabs[0];
  return tab ? (
    <DocEditor tabId={tab.id} docPath={docPath} workspace={null} />
  ) : null;
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

describe('DocEditor', () => {
  beforeEach(() => {
    localStorage.clear();
    testState.doc = {
      content: 'server content',
      title: 'Runbook',
    };
    testState.mutate.mockReset();
    testState.put.mockReset();
    testState.put.mockResolvedValue({
      data: { name: 'logo.png' },
      error: undefined,
    });
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

  it('uploads attachments to the current document after its path changes', async () => {
    const view = renderEditor();
    view.rerender(
      <UnsavedChangesProvider>
        <DocTabProvider storageKey={storageKey}>
          <EditorHarness docPath="renamed-runbook" />
        </DocTabProvider>
      </UnsavedChangesProvider>
    );

    await act(async () => {
      fireEvent.paste(screen.getByTestId('markdown-editor-container'), {
        clipboardData: {
          files: [new File(['png'], 'logo.png', { type: 'image/png' })],
        },
      });
      await Promise.resolve();
    });

    expect(testState.put).toHaveBeenCalledWith(
      '/docs/doc/attachment',
      expect.objectContaining({
        params: {
          query: expect.objectContaining({ path: 'renamed-runbook' }),
        },
      })
    );
  });
});

describe('attachmentUploadName', () => {
  it('preserves names accepted by the attachment API', () => {
    expect(
      attachmentUploadName({
        name: 'monthly report.pdf',
        type: 'application/pdf',
      })
    ).toBe('monthly report.pdf');
  });

  it.each(['notes.md', 'CON.png', 'trailing.', 'folder/logo.png'])(
    'generates a valid replacement for %s',
    (name) => {
      const generated = attachmentUploadName({ name, type: 'image/svg+xml' });
      expect(generated).toMatch(/^pasted-\d+-\d+\.svg$/);
      expect(generated).not.toBe(name);
    }
  );

  it('generates unique names for concurrent uploads', () => {
    const file = { name: 'folder/logo.png', type: 'image/png' };
    expect(attachmentUploadName(file)).not.toBe(attachmentUploadName(file));
  });
});
