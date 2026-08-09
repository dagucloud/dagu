// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  WikiPageTabProvider,
  useWikiPageTabContext,
} from '@/contexts/WikiPageTabContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import { attachmentUploadName } from '../../lib/wiki-page-attachments';
import WikiPageEditor from '../WikiPageEditor';

const testState = vi.hoisted(() => ({
  page: {
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

vi.mock('@/components/ui/wiki-page-markdown-preview', () => ({
  WikiPageMarkdownPreview: () => null,
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
  useQuery: () => ({ data: testState.page, mutate: testState.mutate }),
}));

vi.mock('@/hooks/useWikiPageSSE', () => ({
  useWikiPageSSE: () => ({}),
}));

vi.mock('@/components/wiki-live/WikiLiveProvider', () => ({
  WikiLiveProvider: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => undefined,
}));

vi.mock('../WikiPageHistoryModal', () => ({
  WikiPageHistoryModal: () => null,
}));

vi.mock('../WikiPageExternalChangeDialog', () => ({
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

const storageKey = 'dagu_wiki_tabs:page-editor-test';

function EditorHarness({
  wikiPagePath = 'runbook',
}: {
  wikiPagePath?: string;
}) {
  const { tabs, openWikiPage } = useWikiPageTabContext();

  useEffect(() => {
    if (tabs.length === 0) openWikiPage('runbook', 'Runbook');
  }, [openWikiPage, tabs.length]);

  const tab = tabs[0];
  return tab ? (
    <WikiPageEditor
      tabId={tab.id}
      wikiPagePath={wikiPagePath}
      workspace={null}
    />
  ) : null;
}

function renderEditor() {
  return render(
    <UnsavedChangesProvider>
      <WikiPageTabProvider storageKey={storageKey}>
        <EditorHarness />
      </WikiPageTabProvider>
    </UnsavedChangesProvider>
  );
}

describe('WikiPageEditor', () => {
  beforeEach(() => {
    localStorage.clear();
    testState.page = {
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

  it('copies the legacy editor mode without deleting it', () => {
    localStorage.setItem('doc-editor-mode', 'preview');

    renderEditor();

    expect(screen.getByRole('button', { name: 'Preview' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(localStorage.getItem('wiki-page-editor-mode')).toBe('preview');
    expect(localStorage.getItem('doc-editor-mode')).toBe('preview');
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

    testState.page = { ...testState.page, content: 'external content' };
    view.rerender(
      <UnsavedChangesProvider>
        <WikiPageTabProvider storageKey={storageKey}>
          <EditorHarness />
        </WikiPageTabProvider>
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

  it('uploads attachments to the current Wiki page after its path changes', async () => {
    const view = renderEditor();
    view.rerender(
      <UnsavedChangesProvider>
        <WikiPageTabProvider storageKey={storageKey}>
          <EditorHarness wikiPagePath="renamed-runbook" />
        </WikiPageTabProvider>
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
      '/wiki/page/attachment',
      expect.objectContaining({
        params: {
          query: expect.objectContaining({ path: 'renamed-runbook' }),
        },
      })
    );
  });

  it('uploads pasted files in order', async () => {
    let finishFirst: (value: {
      data: { name: string };
      error: undefined;
    }) => void = () => {};
    const firstUpload = new Promise<{
      data: { name: string };
      error: undefined;
    }>((resolve) => {
      finishFirst = resolve;
    });
    testState.put
      .mockImplementationOnce(() => firstUpload)
      .mockResolvedValueOnce({
        data: { name: 'second.png' },
        error: undefined,
      });
    renderEditor();

    fireEvent.paste(screen.getByTestId('markdown-editor-container'), {
      clipboardData: {
        files: [
          new File(['first'], 'first.png', { type: 'image/png' }),
          new File(['second'], 'second.png', { type: 'image/png' }),
        ],
      },
    });
    expect(testState.put).toHaveBeenCalledTimes(1);

    await act(async () => {
      finishFirst({ data: { name: 'first.png' }, error: undefined });
      await firstUpload;
    });
    expect(testState.put).toHaveBeenCalledTimes(2);
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
