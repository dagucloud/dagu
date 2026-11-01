// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import {
  PanelPlacementProvider,
  type PanelPlacement,
} from '@/features/dags/components/step-details/PanelShell';
import { realSchema } from '../inspector/__tests__/realSchema';
import { yamlArraySteps } from '../testing/fixtures';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: undefined,
    isLoading: false,
    mutate: () => undefined,
  }),
  useClient: () => ({ POST: () => undefined }),
}));
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({ useCanExecute: () => true }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: realSchema }),
}));

const CHROME: VisualEditorProps['chrome'] = { hideRunBar: true };
const initialWidth = window.innerWidth;

function setWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    writable: true,
    value: width,
  });
}

// The bar of the YAML drawer holds the Form button.
let yamlBar: HTMLElement;
beforeEach(() => {
  yamlBar = document.createElement('div');
  document.body.appendChild(yamlBar);
});
afterEach(() => yamlBar.remove());

function Harness(overrides: Partial<VisualEditorProps>) {
  const [text, setText] = useState(yamlArraySteps);
  const [yamlVisible, setYamlVisible] = useState(false);
  return (
    <VisualEditor
      fileName="example.yaml"
      dagName="example"
      text={text}
      onTextChange={setText}
      validation={null}
      editable
      isDirty={false}
      customActions={[]}
      yamlVisible={yamlVisible}
      onYamlVisibleChange={setYamlVisible}
      workflowPanel={<p>About the workflow</p>}
      yamlToolbarRoot={yamlBar}
      {...overrides}
    />
  );
}

function renderAt(
  width: number,
  overrides: Partial<VisualEditorProps> = {},
  placement: PanelPlacement = 'window'
) {
  setWidth(width);
  return render(
    <UserPreferencesProvider>
      <PanelPlacementProvider value={placement}>
        <Harness {...overrides} />
      </PanelPlacementProvider>
    </UserPreferencesProvider>
  );
}

// A double click opens the YAML of the step; the Form button opens the form.
function openInspector() {
  fireEvent.doubleClick(screen.getByTestId('ve-canvas-node-steps[1]'));
  expect(screen.queryByTestId('ve-inspector')).not.toBeInTheDocument();
  fireEvent.click(within(yamlBar).getByRole('button', { name: 'Form' }));
  expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
}

function editorPadding() {
  return screen.getByTestId('visual-editor').style.paddingRight;
}

afterEach(() => setWidth(initialWidth));

describe.skipIf(realSchema === null)('VisualEditor drawer width', () => {
  // With chrome, the drawer shrinks the canvas from 1024px and covers it
  // below. Without chrome, the editor always leaves the drawer width free.
  it.each<[string, VisualEditorProps['chrome'], number, boolean]>([
    ['chrome', CHROME, 1280, true],
    ['chrome', CHROME, 900, false],
    ['no chrome', undefined, 1280, true],
    ['no chrome', undefined, 900, true],
  ])(
    '%s at %ipx leaves the drawer width free: %s',
    (_, chrome, width, free) => {
      renderAt(width, { chrome });
      expect(editorPadding()).toBe('');
      openInspector();
      if (free) {
        expect(editorPadding()).toMatch(/^\d+px$/);
      } else {
        expect(editorPadding()).toBe('');
      }
    }
  );

  it('follows a resize of the window with chrome', () => {
    renderAt(1280, { chrome: CHROME });
    openInspector();
    expect(editorPadding()).toMatch(/^\d+px$/);

    act(() => {
      setWidth(900);
      window.dispatchEvent(new Event('resize'));
    });
    expect(editorPadding()).toBe('');

    act(() => {
      setWidth(1024);
      window.dispatchEvent(new Event('resize'));
    });
    expect(editorPadding()).toMatch(/^\d+px$/);
  });
});

// A zone of the measured width: jsdom has no layout, so the box is stubbed.
function stubZone(width: number) {
  return vi
    .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    .mockImplementation(function (this: HTMLElement) {
      const w = this.dataset.testid === 'visual-editor' ? width : 0;
      return {
        width: w,
        height: 0,
        top: 0,
        left: 0,
        right: w,
        bottom: 0,
      } as DOMRect;
    });
}

describe.skipIf(realSchema === null)('VisualEditor zone width', () => {
  afterEach(() => vi.restoreAllMocks());

  // At 1280px with a 560px panel, a zone under 1040px would leave the canvas
  // less than 480px: the drawer covers it, with no empty strip.
  it.each<[number, boolean]>([
    [784, false],
    [1039, false],
    [1040, true],
    [1200, true],
  ])('a zone of %ipx shrinks the canvas beside a drawer: %s', (zone, free) => {
    stubZone(zone);
    renderAt(1280, { chrome: CHROME }, 'zone');
    openInspector();
    if (free) {
      expect(editorPadding()).toBe('560px');
    } else {
      expect(editorPadding()).toBe('');
    }
  });

  it('caps the padding at the width of the capped drawer below 1100px', () => {
    // Zone 1000px in a window of 1050px: the drawer takes 45%.
    stubZone(1000);
    renderAt(1050, { chrome: CHROME }, 'zone');
    openInspector();
    expect(editorPadding()).toBe('450px');
  });

  // A fixed panel (the classic page) takes its share of the window, not of
  // the editor: the padding follows the window.
  it('pads by the share of the window for a panel over the page', () => {
    stubZone(700);
    renderAt(1000, {});
    openInspector();
    expect(editorPadding()).toBe('450px');
  });
});

describe.skipIf(realSchema === null)('VisualEditor canvas frame', () => {
  it('fills the zone of the host with chrome', () => {
    renderAt(1280, { chrome: CHROME });
    const frame = screen.getByTestId('ve-canvas-frame');
    expect(frame).toHaveClass('min-h-0', 'flex-1');
    expect(frame).not.toHaveClass('min-h-[420px]');
  });

  it('keeps its floor without chrome', () => {
    renderAt(1280);
    const frame = screen.getByTestId('ve-canvas-frame');
    expect(frame).toHaveClass('min-h-[420px]');
    expect(frame).not.toHaveClass('min-h-0');
  });
});

describe.skipIf(realSchema === null)('VisualEditor narrow tools', () => {
  it('moves the tools into a sheet below 768px', () => {
    renderAt(390, { chrome: CHROME });
    const frame = screen.getByTestId('ve-canvas-frame');
    expect(frame).toHaveClass('[&_[data-flow-overlay].left-2.top-2]:hidden');
    // The rule matches the panel that holds the full toolbar of the canvas.
    const hidden = frame.querySelectorAll('[data-flow-overlay].left-2.top-2');
    expect(hidden).toHaveLength(1);
    expect(
      within(hidden[0] as HTMLElement).getByRole('button', {
        name: 'Top to bottom',
      })
    ).toBeInTheDocument();
    // The top-right panel keeps only the export menu.
    expect(
      within(screen.getByTestId('ve-toolbar')).queryByRole('button', {
        name: 'Undo',
      })
    ).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    const sheet = screen.getByTestId('ve-tools-sheet-actions');
    for (const name of ['Undo', 'Redo', 'Workflow', 'YAML']) {
      expect(within(sheet).getByRole('button', { name })).toBeInTheDocument();
    }
    fireEvent.click(within(sheet).getByRole('button', { name: 'Workflow' }));
    expect(screen.getByText('About the workflow')).toBeInTheDocument();
    expect(screen.queryByTestId('ve-tools-sheet')).toBeNull();
  });

  it('shows the Tools button to a reader', () => {
    renderAt(390, { chrome: CHROME, editable: false });
    expect(screen.getByRole('button', { name: 'Tools' })).toBeInTheDocument();
  });

  it.each<[string, VisualEditorProps['chrome'], number]>([
    ['chrome', CHROME, 1280],
    ['chrome', CHROME, 800],
    ['no chrome', undefined, 390],
  ])('keeps the full toolbar with %s at %ipx', (_, chrome, width) => {
    renderAt(width, { chrome });
    expect(screen.queryByRole('button', { name: 'Tools' })).toBeNull();
    expect(
      within(screen.getByTestId('ve-toolbar')).getByRole('button', {
        name: 'Undo',
      })
    ).toBeInTheDocument();
    expect(screen.getByTestId('ve-canvas-frame')).not.toHaveClass(
      '[&_[data-flow-overlay].left-2.top-2]:hidden'
    );
  });
});

describe.skipIf(realSchema === null)(
  'VisualEditor panel over the canvas',
  () => {
    // One rule for every panel: below 1000px a panel covers the canvas, and the
    // toggles stay reachable in the free strip through the Tools sheet.
    it('keeps Workflow and YAML in reach beside a covering panel', () => {
      renderAt(800, { chrome: CHROME });
      expect(screen.queryByRole('button', { name: 'Tools' })).toBeNull();
      fireEvent.click(
        within(screen.getByTestId('ve-toolbar')).getByRole('button', {
          name: 'Workflow',
        })
      );
      expect(editorPadding()).toBe('');
      expect(screen.getByText('About the workflow')).toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
      const sheet = screen.getByTestId('ve-tools-sheet-actions');
      for (const name of ['Workflow', 'YAML']) {
        expect(within(sheet).getByRole('button', { name })).toBeInTheDocument();
      }
      // Toggling Workflow again closes the panel and brings the toolbar back.
      fireEvent.click(within(sheet).getByRole('button', { name: 'Workflow' }));
      expect(screen.queryByRole('button', { name: 'Tools' })).toBeNull();
    });

    it('shrinks the canvas beside a panel from 1000px', () => {
      renderAt(1000, { chrome: CHROME });
      openInspector();
      expect(editorPadding()).toMatch(/^\d+px$/);
      expect(screen.queryByRole('button', { name: 'Tools' })).toBeNull();
    });
  }
);

describe.skipIf(realSchema === null)('VisualEditor step scope path', () => {
  it('reports the YAML path of the step that the drawer shows alone', () => {
    const onYamlStepPathChange = vi.fn();
    renderAt(1280, {
      chrome: CHROME,
      yamlVisible: true,
      onYamlStepPathChange,
    });
    expect(onYamlStepPathChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
    expect(onYamlStepPathChange).toHaveBeenLastCalledWith(['steps', '1']);
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
    expect(onYamlStepPathChange).toHaveBeenLastCalledWith(['steps', '0']);
  });

  it('reports no path when the drawer closes', () => {
    const onYamlStepPathChange = vi.fn();
    renderAt(1280, { chrome: CHROME, onYamlStepPathChange });
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
    expect(onYamlStepPathChange).toHaveBeenLastCalledWith(['steps', '1']);
    fireEvent.click(
      within(screen.getByTestId('ve-toolbar')).getByRole('button', {
        name: 'YAML',
      })
    );
    expect(onYamlStepPathChange).toHaveBeenLastCalledWith(null);
  });
});
