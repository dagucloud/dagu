// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  within,
} from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { RightPanelProvider } from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { PanelPlacementProvider } from '@/features/dags/components/step-details/PanelShell';
import { useYamlSection, type YamlSectionHost } from '../useYamlSection';
import { YamlFilePanel, type YamlFilePanelProps } from '../YamlFilePanel';

const copyText = vi.hoisted(() => vi.fn());
vi.mock('@/lib/clipboard', () => ({ copyText }));

const editor = vi.hoisted(() => ({
  props: null as null | Record<string, unknown>,
}));
vi.mock('../DAGEditorWithDocs', () => ({
  default: (props: {
    value: string;
    readOnly?: boolean;
    onChange?: (value?: string) => void;
  }) => {
    editor.props = props;
    return (
      <textarea
        data-testid="yaml-editor"
        readOnly={props.readOnly}
        value={props.value}
        onChange={(event) => props.onChange?.(event.target.value)}
      />
    );
  },
}));

const TEXT = 'name: demo\nsteps:\n  - name: a\n    command: echo\n';

function renderPanel(extra: Partial<YamlFilePanelProps> = {}) {
  const props: YamlFilePanelProps = {
    text: TEXT,
    readOnly: false,
    state: 'docked',
    scope: 'file',
    onClose: vi.fn(),
    stepName: null,
    onSelectStep: vi.fn(),
    formMode: false,
    canForm: false,
    onFormModeChange: vi.fn(),
    toolbarRef: vi.fn(),
    inputRef: vi.fn(),
    outputRef: vi.fn(),
    ...extra,
  };
  const wrap = (children: ReactNode) => (
    <UserPreferencesProvider>
      <RightPanelProvider>
        <PanelPlacementProvider value="zone">{children}</PanelPlacementProvider>
      </RightPanelProvider>
    </UserPreferencesProvider>
  );
  return { props, ...render(wrap(<YamlFilePanel {...props} />)) };
}

// The structure that a reader sees: the same in the editor and in Executions.
function shape(root: HTMLElement) {
  const header = root.querySelector('[data-panel-header]')!;
  return {
    state: root.getAttribute('data-state'),
    shell: root.getAttribute('data-panel-shell'),
    placement: root.getAttribute('data-placement'),
    headings: Array.from(header.querySelectorAll('h2')).map(
      (h) => h.textContent
    ),
    // The slots after the title that the header has, in order.
    slots: Array.from(header.querySelectorAll('[data-testid]'))
      .map((slot) => slot.getAttribute('data-testid'))
      .filter((id) => id !== 'ndv-step-icon'),
    close: within(header as HTMLElement)
      .getAllByRole('button')
      .pop()
      ?.getAttribute('aria-label'),
    copy: within(root).getByRole('button', { name: 'Copy YAML' }).className,
    body: root.querySelector('[data-testid="yaml-file-body"]') !== null,
  };
}

describe('YamlFilePanel', () => {
  it('renders the same panel for the editor and for a read-only run', () => {
    const editable = renderPanel({
      tools: <button data-testid="yaml-format">Format</button>,
    });
    const { slots: editorSlots, ...editorShape } = shape(
      screen.getByTestId('ndv')
    );
    editable.unmount();

    renderPanel({ readOnly: true, runRef: { id: 'run-0123456789' } });
    const { slots: runSlots, ...runShape } = shape(screen.getByTestId('ndv'));

    // The frame is the same; the run adds its identity and the lock after the
    // title, where the editor has its tools.
    expect(runShape).toEqual(editorShape);
    expect(runShape).toMatchObject({
      state: 'docked',
      shell: 'right',
      placement: 'zone',
      headings: ['YAML'],
      close: 'Close',
      body: true,
    });
    expect(editorSlots).toEqual(['yaml-format']);
    expect(runSlots).toEqual(['yaml-run-ref', 'readonly-marker']);
  });

  it('shows tools and the lock in the header only when the host needs them', () => {
    renderPanel();
    expect(screen.queryByTestId('yaml-format')).toBeNull();
    expect(screen.queryByTestId('readonly-marker')).toBeNull();
    expect(screen.queryByTestId('yaml-run-ref')).toBeNull();
  });

  it('marks a read-only panel with a lock, not a chip', () => {
    renderPanel({ readOnly: true });
    expect(screen.getByTestId('readonly-marker')).toHaveAccessibleName(
      'Read-only'
    );
    expect(screen.queryByTestId('yaml-readonly-chip')).toBeNull();
  });

  it('names the run in the lock and shows its identity after the title', () => {
    renderPanel({ readOnly: true, runRef: { id: 'run-0123456789' } });
    const header = screen
      .getByTestId('ndv')
      .querySelector('[data-panel-header]')!;
    const heading = header.querySelector('h2')!;
    const identity = within(header as HTMLElement).getByTestId('yaml-run-ref');
    const lock = within(header as HTMLElement).getByTestId('readonly-marker');
    expect(identity).toHaveTextContent('run-0123');
    expect(identity).toHaveAttribute('title', 'run-0123456789');
    expect(lock).toHaveAccessibleName(
      'Read-only: definition of run run-0123456789'
    );
    expect(
      heading.compareDocumentPosition(identity) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(
      identity.compareDocumentPosition(lock) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('puts the note after the title, before the close button', () => {
    renderPanel({
      note: <span data-testid="yaml-note">Note</span>,
    });
    const header = screen
      .getByTestId('ndv')
      .querySelector('[data-panel-header]')!;
    const heading = header.querySelector('h2')!;
    const note = within(header as HTMLElement).getByTestId('yaml-note');
    expect(
      heading.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('takes input only when it is editable', () => {
    const onChange = vi.fn();
    const { unmount } = renderPanel({ onChange });
    fireEvent.change(screen.getByTestId('yaml-editor'), {
      target: { value: 'name: x\n' },
    });
    expect(onChange).toHaveBeenCalledWith('name: x\n');
    unmount();

    const readOnlyChange = vi.fn();
    renderPanel({ readOnly: true, onChange: readOnlyChange });
    expect(screen.getByTestId('yaml-editor')).toHaveAttribute('readonly');
    expect(editor.props?.onChange).toBeUndefined();
  });

  it('copies the text from the overlay and shows when it is done', async () => {
    copyText.mockReset().mockResolvedValue(true);
    renderPanel({ readOnly: true });
    const copy = screen.getByRole('button', { name: 'Copy YAML' });
    expect(copy).toHaveTextContent('');
    await act(async () => {
      fireEvent.click(copy);
    });
    expect(copyText).toHaveBeenCalledWith(TEXT);
    const done = screen.getByRole('button', { name: 'YAML copied' });
    expect(done.querySelector('.lucide-check')).toHaveClass('text-success');
  });

  it('closes with the close button', () => {
    const { props } = renderPanel({ readOnly: true });
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(props.onClose).toHaveBeenCalled();
  });

  // The step view and the panel of the file are this component: every
  // setting of the editor must reach it, or the editor there lacks a
  // feature (the code actions of the quick fixes were lost this way).
  it('gives every editor setting to the editor, the quick fixes too', () => {
    const quickFixSource = { getPlan: () => null };
    const markers = [{ message: 'm' }] as never;
    const referenceState = () => ({});
    renderPanel({
      quickFixSource,
      markers,
      referenceState,
      modelUri: 'inmemory://dagu/a.yaml',
      scopePath: ['steps', '0'],
      changeBaseline: TEXT,
    });
    expect(editor.props).toMatchObject({
      quickFixSource,
      markers,
      referenceState,
      modelUri: 'inmemory://dagu/a.yaml',
      scopePath: ['steps', '0'],
      changeBaseline: TEXT,
    });
  });

  it('keeps the editor mounted while the form shows', () => {
    const { props, rerender } = renderPanel();
    const textarea = screen.getByTestId('yaml-editor');
    rerender(
      <UserPreferencesProvider>
        <RightPanelProvider>
          <PanelPlacementProvider value="zone">
            <YamlFilePanel {...props} formMode />
          </PanelPlacementProvider>
        </RightPanelProvider>
      </UserPreferencesProvider>
    );
    expect(screen.getByTestId('yaml-editor')).toBe(textarea);
    expect(screen.getByTestId('yaml-file-body')).toHaveAttribute('hidden');
  });
});

describe('useYamlSection', () => {
  function host(state: YamlSectionHost['state'], active = true) {
    return { state, onStateChange: vi.fn(), active } satisfies YamlSectionHost;
  }

  it('opens the whole file docked and a step expanded', () => {
    const h = host(null);
    const { result } = renderHook(() => useYamlSection(h));
    act(() => result.current.setSectionOpen(true, 'panel'));
    expect(h.onStateChange).toHaveBeenLastCalledWith('docked');
    expect(result.current.yamlKind).toBe('file');
    act(() => result.current.setSectionOpen(true));
    expect(h.onStateChange).toHaveBeenLastCalledWith('expanded');
    expect(result.current.yamlKind).toBe('step');
  });

  it('closes with null, and not when it is closed already', () => {
    const h = host(null);
    const { result } = renderHook(() => useYamlSection(h));
    act(() => result.current.setSectionOpen(false));
    expect(h.onStateChange).not.toHaveBeenCalled();
    const open = host('docked');
    const second = renderHook(() => useYamlSection(open));
    act(() => second.result.current.setSectionOpen(false));
    expect(open.onStateChange).toHaveBeenCalledWith(null);
  });

  it('reads the kind from the link and sets a link that disagrees right', () => {
    const h = host('docked');
    const { result } = renderHook(() => useYamlSection(h));
    expect(result.current.yamlKind).toBe('file');
    expect(result.current.sectionOpen).toBe(true);
    act(() => result.current.setYamlKind('step'));
    expect(h.onStateChange).toHaveBeenCalledWith('expanded');
  });

  // A link that opens a step (Open step, a run tab) turns the host
  // expanded: the step shows, and the file is not written back.
  it.each([['docked'], [null]] as const)(
    'shows the step when the host turns from %s to expanded',
    (from) => {
      const onStateChange = vi.fn();
      const { result, rerender } = renderHook(
        (state: YamlSectionHost['state']) =>
          useYamlSection({ state, onStateChange }),
        { initialProps: from as YamlSectionHost['state'] }
      );
      rerender('expanded');
      expect(result.current.yamlKind).toBe('step');
      expect(onStateChange).not.toHaveBeenCalled();
    }
  );

  it('follows the host, and never writes, when its pane is hidden', () => {
    const h = host('expanded', false);
    const { result } = renderHook(() => useYamlSection(h));
    expect(result.current.yamlKind).toBe('step');
    const hidden = host('docked', false);
    const second = renderHook(() =>
      useYamlSection({ ...hidden, state: 'docked' })
    );
    act(() => second.result.current.setYamlKind('step'));
    expect(hidden.onStateChange).not.toHaveBeenCalled();
  });

  it('keeps its own state without a host', () => {
    const { result } = renderHook(() => useYamlSection());
    expect(result.current.sectionOpen).toBe(false);
    act(() => result.current.setSectionOpen(true, 'panel'));
    expect(result.current.sectionOpen).toBe(true);
  });
});
