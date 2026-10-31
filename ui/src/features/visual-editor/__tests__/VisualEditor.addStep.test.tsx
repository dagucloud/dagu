// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import {
  createRef,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type {
  CanvasViewHandle,
  VisualCanvasProps,
  VisualEditorHandle,
  VisualEditorState,
} from '../contracts';
import { parseDagDoc } from '../doc/parse';
import { realSchema } from '../inspector/__tests__/realSchema';
import {
  catalogSample,
  yamlArraySteps,
  yamlUnreadable,
  yamlRouter,
} from '../testing/fixtures';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined, isLoading: false, mutate: () => {} }),
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
vi.mock('../run/StepRunPanel', () => ({
  StepRunPanel: () => <p data-testid="step-run-panel" />,
}));

// The real canvas, with its view handle watched: the pan after a pick is
// VisualEditor's, and this file only checks that it happens.
const view = vi.hoisted(() => ({ revealStep: vi.fn() }));
vi.mock('../canvas', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../canvas')>();
  function WatchedCanvas(props: VisualCanvasProps) {
    const inner = useRef<CanvasViewHandle>(null);
    useImperativeHandle(
      props.viewRef,
      () => ({
        fitView: () => inner.current?.fitView(),
        revealStep: (key) => {
          view.revealStep(key);
          inner.current?.revealStep(key);
        },
        zoomTo: (level) => inner.current?.zoomTo(level),
        getZoom: () => inner.current?.getZoom() ?? 1,
      }),
      []
    );
    return <actual.VisualCanvas {...props} viewRef={inner} />;
  }
  return { ...actual, VisualCanvas: WatchedCanvas };
});

type HarnessProps = Partial<VisualEditorProps> & { initial: string };

const state = { text: '' };

function Harness({ initial, ...overrides }: HarnessProps) {
  const [text, setText] = useState(initial);
  state.text = text;
  return (
    <VisualEditor
      fileName="example.yaml"
      dagName="example"
      text={text}
      onTextChange={setText}
      validation={null}
      editable
      isDirty={text !== initial}
      customActions={[]}
      {...overrides}
    />
  );
}

function wrap(node: ReactNode) {
  return <UserPreferencesProvider>{node}</UserPreferencesProvider>;
}

function renderEditor(props: Partial<HarnessProps> = {}) {
  const handleRef = createRef<VisualEditorHandle>();
  const base: HarnessProps = { initial: yamlArraySteps, handleRef, ...props };
  state.text = base.initial;
  const result = render(wrap(<Harness {...base} />));
  return {
    ...result,
    handle: () => handleRef.current!,
    update: (next: Partial<HarnessProps>) =>
      result.rerender(wrap(<Harness {...base} {...next} />)),
  };
}

function search() {
  return screen.getByRole('combobox', { name: 'Search actions' });
}

function addAction(query: string, shiftKey = false) {
  fireEvent.change(search(), { target: { value: query } });
  fireEvent.keyDown(search(), { key: 'Enter', shiftKey });
}

function targetLabel(): string | null {
  return (
    screen
      .getByTestId('ve-add-step')
      .querySelector('header p')
      ?.textContent?.trim() ?? null
  );
}

function stepNamed(name: string) {
  return parseDagDoc(state.text).steps.find(
    (step) => step.explicitName === name
  );
}

beforeEach(() => localStorage.clear());
afterEach(() => view.revealStep.mockClear());

describe.skipIf(realSchema === null)('VisualEditor add-step glue', () => {
  // parity:add-step-entry-points
  describe('the place of the new step', () => {
    it('says "After {name}" for a child target', () => {
      const { handle } = renderEditor();
      act(() => handle().addStep({ mode: 'child', source: 'steps[0]' }));
      expect(targetLabel()).toBe('After fetch');
    });

    it('says "Between {from} and {to}" for an edge', () => {
      const { handle } = renderEditor();
      act(() =>
        handle().addStep({
          mode: 'insert',
          edgeId: 'depends:steps[0]->steps[1]',
        })
      );
      expect(targetLabel()).toBe('Between fetch and transform');
    });

    it('says "Route {port} of {router}" for a route target', () => {
      const { handle } = renderEditor({ initial: yamlRouter });
      act(() =>
        handle().addStep({ mode: 'route', router: 'steps[0]', port: 're:.*' })
      );
      expect(targetLabel()).toBe('Route re:.* of pick');
    });

    it('says "At the end of the workflow" with no selection', () => {
      const { handle } = renderEditor();
      act(() => handle().addStep());
      expect(targetLabel()).toBe('At the end of the workflow');
    });

    it('says "After {name}" for the selected step', () => {
      const { handle } = renderEditor();
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[2]'));
      act(() => handle().addStep({ mode: 'after' }));
      expect(targetLabel()).toBe('After publish');
    });

    it('says "First step" for a first target', () => {
      const { handle } = renderEditor();
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
      act(() => handle().addStep({ mode: 'first' }));
      expect(targetLabel()).toBe('First step');
    });
  });

  describe('a pick', () => {
    it('adds with Enter, closes the panel, selects the new step and pans to it', () => {
      const onSelectedStepNameChange = vi.fn();
      const states: VisualEditorState[] = [];
      renderEditor({
        onSelectedStepNameChange,
        onStateChange: (s) => states.push(s),
      });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(states[states.length - 1]?.drawer).toBe('add-step');

      addAction('http.request');

      expect(stepNamed('http_request')?.key).toBe('steps[3]');
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('http_request');
      expect(states[states.length - 1]?.selectedStepName).toBe('http_request');
      expect(view.revealStep).toHaveBeenCalledWith('steps[3]');
    });

    it('adds with Shift+Enter, stays open and aims after the new step', () => {
      const onSelectedStepNameChange = vi.fn();
      renderEditor({ onSelectedStepNameChange });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });

      addAction('http.request', true);
      expect(screen.getByTestId('ve-add-step')).toBeInTheDocument();
      expect(stepNamed('http_request')).toBeDefined();
      expect(targetLabel()).toBe('After http_request');
      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('http_request');
      expect(view.revealStep).toHaveBeenCalledWith('steps[3]');

      // The next pick is a child of the step added before.
      addAction('jq.filter', true);
      expect(stepNamed('jq_filter')?.depends).toEqual(['http_request']);
      expect(targetLabel()).toBe('After jq_filter');

      fireEvent.keyDown(search(), { key: 'Escape' });
      fireEvent.keyDown(search(), { key: 'Escape' });
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
    });

    // The new step is selected after each add: the next add is still wired.
    it('connects each step of a sequence of adds to the selected step', () => {
      const { handle } = renderEditor();
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

      act(() => handle().addStep());
      addAction('http.request');
      expect(stepNamed('http_request')?.depends).toEqual(['transform']);
      expect(stepNamed('http_request')?.key).toBe('steps[2]');

      act(() => handle().addStep());
      expect(targetLabel()).toBe('After http_request');
      addAction('jq.filter');
      expect(stepNamed('jq_filter')?.depends).toEqual(['http_request']);

      act(() => handle().addStep());
      addAction('log.write');
      expect(stepNamed('log_write')?.depends).toEqual(['jq_filter']);
    });

    // The panel says 'First step': the step goes before the others.
    it('adds a first target before the first step of a workflow', () => {
      const { handle } = renderEditor();
      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
      act(() => handle().addStep({ mode: 'first' }));
      expect(targetLabel()).toBe('First step');

      addAction('http.request');
      expect(stepNamed('http_request')?.key).toBe('steps[0]');
      expect(stepNamed('fetch')?.key).toBe('steps[1]');
    });

    it('writes the route of a route target, as one undo step', () => {
      const { handle } = renderEditor({ initial: yamlRouter });
      act(() =>
        handle().addStep({ mode: 'route', router: 'steps[0]', port: 're:.*' })
      );
      addAction('run');

      const pick = parseDagDoc(state.text).steps[0]!;
      const routes = (pick.value.with as { routes: Record<string, string[]> })
        .routes;
      expect(routes['re:.*']).toEqual(['other', 'cmd_4']);
      expect(stepNamed('cmd_4')?.depends).toEqual(['pick']);

      act(() => handle().undo());
      expect(state.text).toBe(yamlRouter);
    });
  });

  describe('a read-only or frozen editor', () => {
    it('never opens the panel for a reader', () => {
      const { handle } = renderEditor({ editable: false });
      act(() => handle().addStep());
      act(() => handle().addStep({ mode: 'first' }));
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
    });

    it('closes the panel when the YAML stops parsing, and does not open it', () => {
      const { handle, update } = renderEditor();
      act(() => handle().addStep());
      expect(screen.getByTestId('ve-add-step')).toBeInTheDocument();

      vi.useFakeTimers();
      try {
        update({ text: yamlUnreadable });
        act(() => {
          vi.advanceTimersByTime(1000);
        });
      } finally {
        vi.useRealTimers();
      }
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
      act(() => handle().addStep());
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
    });

    it('closes the panel when the user loses the right to write', () => {
      const { handle, update } = renderEditor();
      act(() => handle().addStep());
      update({ editable: false });
      expect(screen.queryByTestId('ve-add-step')).not.toBeInTheDocument();
    });
  });

  // parity:step-palette-fallback
  describe('the step list of the canvas', () => {
    it('is not used by the editor, which lists the steps in its panel', () => {
      renderEditor();
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(screen.getAllByRole('combobox')).toHaveLength(1);
      expect(screen.getByTestId('ve-canvas')).not.toContainElement(search());
    });

    it('opens nothing in a canvas without a host', async () => {
      const { VisualCanvas, buildCanvasGraph } = await import('../canvas');
      const model = parseDagDoc(yamlArraySteps);
      const graph = buildCanvasGraph({
        model,
        names: { byKey: {}, byName: {} },
        details: undefined,
        issues: { byStep: {}, dag: [] },
      });
      const onEdit = vi.fn();
      render(
        wrap(
          <VisualCanvas
            graph={graph}
            direction="TB"
            selected={null}
            frozen={false}
            readOnly={false}
            catalog={catalogSample}
            onSelect={() => {}}
            onEdit={onEdit}
            onCommand={() => {}}
          />
        )
      );
      // Without a host, the key opens nothing: the canvas has no palette.
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
      expect(onEdit).not.toHaveBeenCalled();
    });
  });
});
