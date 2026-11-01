// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import {
  createRef,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import {
  RightPanelProvider,
  useRightPanel,
  type RightPanelId,
} from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  CanvasViewHandle,
  EditResult,
  EditorNotice,
  StepCommand,
  VisualCanvasProps,
  VisualEditorHandle,
  VisualEditorState,
} from '../contracts';
import { useCanvasActions } from '../canvas/context';
import { useCanvasDirection } from '../canvas/useCanvasDirection';
import {
  copyStepsYaml,
  duplicateStepsOp,
  pasteStepsOp,
} from '../doc/ops/clipboard';
import { parseDagDoc } from '../doc';
import { useRunOverlay } from '../run/useRunOverlay';
import { realSchema } from '../inspector/__tests__/realSchema';
import {
  scopeHttpJq,
  yamlArraySteps,
  yamlUnreadable,
  yamlHandlers,
  yamlHttpJq,
  yamlShorthandSteps,
} from '../testing/fixtures';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));

const api = vi.hoisted(() => ({
  queries: [] as { path: string; init: unknown }[],
  post: (() => undefined) as (...args: unknown[]) => unknown,
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    api.queries.push({ path, init });
    return { data: undefined, isLoading: false, mutate: () => undefined };
  },
  useClient: () => ({ POST: (...args: unknown[]) => api.post(...args) }),
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

// The real hook, watched for its arguments.
vi.mock('../run/useRunOverlay', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../run/useRunOverlay')>();
  return { useRunOverlay: vi.fn(actual.useRunOverlay) };
});
vi.mock('../run/useFollowedRun', () => ({
  useFollowedRun: () => ({ details: null, active: true, error: null }),
}));
vi.mock('../run/StepRunPanel', () => ({
  StepRunPanel: () => <p data-testid="step-run-panel" />,
}));

// The real hook, watched for its calls.
vi.mock('../canvas/useCanvasDirection', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('../canvas/useCanvasDirection')>();
  return { ...actual, useCanvasDirection: vi.fn(actual.useCanvasDirection) };
});

// The real canvas, with its view handle watched.
const view = vi.hoisted(() => ({
  revealStep: vi.fn(),
  highlightStep: vi.fn(),
  fitView: vi.fn(),
  // The props of the last canvas render.
  canvas: null as VisualCanvasProps | null,
}));
vi.mock('../canvas', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../canvas')>();
  function WatchedCanvas(props: VisualCanvasProps) {
    const inner = useRef<CanvasViewHandle>(null);
    view.canvas = props;
    useImperativeHandle(
      props.viewRef,
      () => ({
        fitView: () => {
          view.fitView();
          inner.current?.fitView();
        },
        revealStep: (key) => {
          view.revealStep(key);
          inner.current?.revealStep(key);
        },
        highlightStep: (key) => {
          view.highlightStep(key);
          inner.current?.highlightStep(key);
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

// The real operations, watched; a test can give a result of its own.
vi.mock('../doc/ops/clipboard', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../doc/ops/clipboard')>();
  return {
    copyStepsYaml: vi.fn(actual.copyStepsYaml),
    pasteStepsOp: vi.fn(actual.pasteStepsOp),
    duplicateStepsOp: vi.fn(actual.duplicateStepsOp),
  };
});

/** `bar` gives the editor a bar of the YAML drawer, where the Form button is. */
type HarnessProps = Partial<VisualEditorProps> & {
  initial: string;
  bar?: boolean;
};

const state = { text: '' };

let yamlBar: HTMLElement;
beforeEach(() => {
  yamlBar = document.createElement('div');
  document.body.appendChild(yamlBar);
});

function Harness({ initial, bar, ...overrides }: HarnessProps) {
  const [text, setText] = useState(initial);
  const [yamlVisible, setYamlVisible] = useState(false);
  state.text = text;
  const barProps: Partial<VisualEditorProps> = bar
    ? {
        yamlVisible,
        onYamlVisibleChange: (visible) => setYamlVisible(visible),
        yamlToolbarRoot: yamlBar,
      }
    : {};
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
      {...barProps}
      {...overrides}
    />
  );
}

/** Opens the YAML of a step with a double click, then its form with the Form button. */
function openForm(key: string) {
  fireEvent.doubleClick(nodeCard(key));
  clickForm();
}

function clickForm() {
  fireEvent.click(within(yamlBar).getByRole('button', { name: 'Form' }));
}

function wrap(node: ReactNode) {
  return <UserPreferencesProvider>{node}</UserPreferencesProvider>;
}

function renderEditor(props: HarnessProps) {
  state.text = props.initial;
  const result = render(wrap(<Harness {...props} />));
  return {
    ...result,
    update: (next: HarnessProps) =>
      result.rerender(wrap(<Harness {...next} />)),
  };
}

function last<T>(items: T[]): T | undefined {
  return items[items.length - 1];
}

function nodeCard(key: string): HTMLElement {
  return screen.getByTestId(`ve-canvas-node-${key}`);
}

function nodeWrapper(key: string): HTMLElement {
  return nodeCard(key).closest<HTMLElement>('[data-flow-node]')!;
}

function isSelected(key: string): boolean {
  return within(nodeWrapper(key))
    .getByTestId('ve-canvas-node-toolbar')
    .classList.contains('opacity-100');
}

function runOf(status: NodeStatus, name = 'fetch'): ApiDAGRunDetails {
  return {
    dagRunId: 'run-7',
    nodes: [
      {
        step: { name },
        status,
        statusLabel: 'failed',
        retryCount: 0,
        doneCount: 1,
      },
    ],
  } as unknown as ApiDAGRunDetails;
}

const handlerDetails = {
  name: 'with-handlers',
  steps: [{ name: 'main' }],
  handlerOn: {
    success: { name: 'onSuccess' },
    failure: { name: 'onFailure' },
  },
} as unknown as ApiDAGDetails;

afterEach(() => {
  yamlBar.remove();
  localStorage.clear();
  api.queries = [];
  api.post = () => undefined;
  view.revealStep.mockClear();
  view.highlightStep.mockClear();
  view.fitView.mockClear();
  vi.mocked(useRunOverlay).mockClear();
  vi.mocked(copyStepsYaml).mockClear();
  vi.mocked(pasteStepsOp).mockClear();
  vi.mocked(duplicateStepsOp).mockClear();
});

describe.skipIf(realSchema === null)('VisualEditor in a host', () => {
  // parity:test-visual-editor
  describe('run source', () => {
    it('reads no run and colours the steps from the given run', () => {
      renderEditor({
        initial: yamlArraySteps,
        runSource: { runId: 'run-7', details: runOf(NodeStatus.Failed) },
      });

      expect(
        api.queries.filter(
          (q) => q.path === '/dag-runs/{name}/{dagRunId}' && q.init !== null
        )
      ).toEqual([]);
      expect(vi.mocked(useRunOverlay).mock.calls.length).toBeGreaterThan(0);
      for (const [args] of vi.mocked(useRunOverlay).mock.calls) {
        expect(args.runId).toBeNull();
      }
      expect(
        within(nodeCard('steps[0]')).getByTestId('ve-canvas-node-status')
      ).toHaveAttribute('data-status', String(NodeStatus.Failed));
    });

    it('reads the latest run itself without a run source', () => {
      renderEditor({ initial: yamlArraySteps });

      expect(vi.mocked(useRunOverlay).mock.calls[0]![0].runId).toBe('latest');
      expect(
        api.queries.some(
          (q) => q.path === '/dag-runs/{name}/{dagRunId}' && q.init !== null
        )
      ).toBe(true);
    });
  });

  // parity:canvas-selection
  describe('controlled selection', () => {
    it('selects the node of the server step name', () => {
      renderEditor({ initial: yamlArraySteps, selectedStepName: 'transform' });

      expect(isSelected('steps[1]')).toBe(true);
      expect(isSelected('steps[0]')).toBe(false);
    });

    it('selects a handler by its server name', () => {
      renderEditor({
        initial: yamlHandlers,
        validation: { issues: [], dag: handlerDetails },
        selectedStepName: 'onFailure',
      });

      expect(isSelected('handler_on.failure')).toBe(true);
    });

    it('tells the host the server name of a clicked step', () => {
      const onSelectedStepNameChange = vi.fn();
      renderEditor({
        initial: yamlArraySteps,
        selectedStepName: null,
        onSelectedStepNameChange,
      });

      fireEvent.click(nodeCard('steps[2]'));

      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('publish');
    });

    // A pointer click has a click count; a key that activates an element
    // dispatches a click without one.
    it('tells the host of a click on a step, not of a key', () => {
      const onStepClick = vi.fn();
      renderEditor({ initial: yamlArraySteps, openNdvOn: 'open', onStepClick });

      fireEvent.click(nodeCard('steps[2]'), {
        detail: 1,
        clientX: 40,
        clientY: 20,
      });
      expect(onStepClick).toHaveBeenLastCalledWith('publish', {
        clientX: 40,
        clientY: 20,
      });
      // A click on the selected step is a click again.
      fireEvent.click(nodeCard('steps[2]'), { detail: 1 });
      expect(onStepClick).toHaveBeenCalledTimes(2);

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'ArrowUp' });
      expect(isSelected('steps[1]')).toBe(true);
      fireEvent.click(nodeCard('steps[1]'), { detail: 0 });
      expect(onStepClick).toHaveBeenCalledTimes(2);
    });

    it('tells the host of no click on a control of the card', () => {
      const onStepClick = vi.fn();
      renderEditor({ initial: yamlArraySteps, openNdvOn: 'open', onStepClick });
      const card = nodeCard('steps[2]');
      const control = document.createElement('button');
      card.appendChild(control);

      fireEvent.click(control, { detail: 1 });
      expect(isSelected('steps[2]')).toBe(true);
      expect(onStepClick).not.toHaveBeenCalled();
    });

    it('pans to a step that the host selects', () => {
      const props = { initial: yamlArraySteps, selectedStepName: 'fetch' };
      const { update } = renderEditor(props);
      view.revealStep.mockClear();

      update({ ...props, selectedStepName: 'publish' });

      expect(isSelected('steps[2]')).toBe(true);
      expect(view.revealStep).toHaveBeenCalledWith('steps[2]');
    });

    it('tells the host the new name of the selected step after a rename', () => {
      const onSelectedStepNameChange = vi.fn();
      const details = (second: string) =>
        ({
          name: 'nightly-import',
          steps: [{ name: 'fetch' }, { name: second }, { name: 'publish' }],
        }) as unknown as ApiDAGDetails;
      const props = {
        initial: yamlArraySteps,
        selectedStepName: 'transform',
        onSelectedStepNameChange,
        validation: { issues: [], dag: details('transform') },
      };
      const { update } = renderEditor(props);
      expect(onSelectedStepNameChange).not.toHaveBeenCalled();

      vi.useFakeTimers();
      try {
        update({
          ...props,
          text: yamlArraySteps.replace('name: transform', 'name: shape'),
          validation: { issues: [], dag: details('shape') },
        });
        act(() => {
          vi.advanceTimersByTime(1000);
        });
      } finally {
        vi.useRealTimers();
      }

      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('shape');
      expect(isSelected('steps[1]')).toBe(true);
    });

    // A host that keeps the selection as a name and a validation that still
    // describes the text before the edit: the copy gets the selection.
    describe('after a clipboard edit', () => {
      const stale = {
        issues: [],
        dag: {
          name: 'nightly-import',
          steps: [
            { name: 'fetch' },
            { name: 'transform' },
            { name: 'publish' },
          ],
        } as unknown as ApiDAGDetails,
      };

      function ControlledHost({
        handleRef,
      }: {
        handleRef: VisualEditorProps['handleRef'];
      }) {
        const [name, setName] = useState<string | null>(null);
        return (
          <Harness
            initial={yamlArraySteps}
            handleRef={handleRef}
            validation={stale}
            selectedStepName={name}
            onSelectedStepNameChange={setName}
          />
        );
      }

      function setup() {
        const handleRef = createRef<VisualEditorHandle>();
        render(wrap(<ControlledHost handleRef={handleRef} />));
        return () => handleRef.current!;
      }

      it('selects the copy after a duplicate in the middle', () => {
        const handle = setup();
        fireEvent.click(nodeCard('steps[1]'));
        act(() => {
          handle().duplicateSelection();
        });

        expect(
          parseDagDoc(state.text).steps.map((x) => x.explicitName)
        ).toEqual(['fetch', 'transform', 'transform_2', 'publish']);
        expect(isSelected('steps[2]')).toBe(true);
        expect(isSelected('steps[3]')).toBe(false);
      });

      // A host that writes each name to a URL can show a stale one late: the
      // editor must not report the step that moved into the old position.
      it('never reports the name of the step that follows the copy', () => {
        const names: (string | null)[] = [];
        const handleRef = createRef<VisualEditorHandle>();
        render(
          wrap(
            <Harness
              initial={yamlArraySteps}
              handleRef={handleRef}
              validation={stale}
              selectedStepName="transform"
              onSelectedStepNameChange={(name) => names.push(name)}
            />
          )
        );
        act(() => {
          handleRef.current!.duplicateSelection();
        });

        expect(names).not.toContain('publish');
        expect(names[names.length - 1]).toBe('transform_2');
      });

      it('selects the copy after a duplicate of the last step', () => {
        const handle = setup();
        fireEvent.click(nodeCard('steps[2]'));
        act(() => {
          handle().duplicateSelection();
        });

        expect(isSelected('steps[3]')).toBe(true);
        expect(isSelected('steps[2]')).toBe(false);
      });

      it('selects the first pasted step', () => {
        const handle = setup();
        fireEvent.click(nodeCard('steps[0]'));
        act(() => {
          handle().paste(
            '- name: x\n  run: echo x\n- name: y\n  run: echo y\n'
          );
        });

        expect(isSelected('steps[1]')).toBe(true);
        expect(isSelected('steps[2]')).toBe(false);
      });
    });

    it('tells the host when the form closes, and selects the step again', () => {
      const onSelectedStepNameChange = vi.fn();
      const props = {
        initial: yamlArraySteps,
        selectedStepName: null,
        onSelectedStepNameChange,
        bar: true,
      };
      const { update } = renderEditor(props);
      openForm('steps[1]');
      update({ ...props, selectedStepName: 'transform' });

      // Escape in the drawer closes it through the drawer, not the form.
      const field = within(screen.getByTestId('ve-inspector')).getAllByRole(
        'textbox'
      )[0]!;
      field.focus();
      fireEvent.keyDown(field, { key: 'Escape' });

      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith(null);
      expect(isSelected('steps[1]')).toBe(false);
      update({ ...props, selectedStepName: null });
      update({ ...props, selectedStepName: 'transform' });
      expect(isSelected('steps[1]')).toBe(true);
    });

    // A host that clears the selection from outside (Escape on the page)
    // must not get the old step back from the rename check. A host callback
    // that changes with the URL runs that check in the same render.
    it('keeps a selection that the host clears', () => {
      const onSelectedStepNameChange = vi.fn();
      const props = {
        initial: yamlArraySteps,
        selectedStepName: 'transform',
        onSelectedStepNameChange,
      };
      const { update } = renderEditor(props);
      expect(isSelected('steps[1]')).toBe(true);

      update({
        ...props,
        selectedStepName: null,
        onSelectedStepNameChange: (name) => onSelectedStepNameChange(name),
      });

      expect(isSelected('steps[1]')).toBe(false);
      expect(onSelectedStepNameChange).not.toHaveBeenCalled();
    });

    it('keeps a selection that the host clears after a click', () => {
      const onSelectedStepNameChange = vi.fn();
      const props = {
        initial: yamlArraySteps,
        selectedStepName: null,
        onSelectedStepNameChange,
      };
      const { update } = renderEditor(props);
      fireEvent.click(nodeCard('steps[1]'));
      update({ ...props, selectedStepName: 'transform' });
      onSelectedStepNameChange.mockClear();

      update({
        ...props,
        selectedStepName: null,
        onSelectedStepNameChange: (name) => onSelectedStepNameChange(name),
      });

      expect(isSelected('steps[1]')).toBe(false);
      expect(onSelectedStepNameChange).not.toHaveBeenCalled();
    });

    it('does not pan again when the host gives back the clicked step', () => {
      const onSelectedStepNameChange = vi.fn();
      const props = {
        initial: yamlArraySteps,
        selectedStepName: null,
        onSelectedStepNameChange,
      };
      const { update } = renderEditor(props);
      fireEvent.click(nodeCard('steps[1]'));
      view.revealStep.mockClear();

      update({ ...props, selectedStepName: 'transform' });

      expect(view.revealStep).not.toHaveBeenCalled();
      expect(isSelected('steps[1]')).toBe(true);
    });
  });

  describe('openNdvOn', () => {
    it("only selects on a click with 'open'", () => {
      const onYamlVisibleChange = vi.fn();
      const onSelectedStepNameChange = vi.fn();
      renderEditor({
        initial: yamlArraySteps,
        openNdvOn: 'open',
        onYamlVisibleChange,
        onSelectedStepNameChange,
      });

      fireEvent.click(nodeCard('steps[1]'));

      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('transform');
      expect(isSelected('steps[1]')).toBe(true);
      expect(onYamlVisibleChange).not.toHaveBeenCalled();
      expect(screen.queryByTestId('ve-inspector')).toBeNull();
    });

    // Opening a step shows its YAML first; the Form button opens the form.
    it("opens the YAML on Enter with 'open'", () => {
      renderEditor({ initial: yamlArraySteps, openNdvOn: 'open', bar: true });
      fireEvent.click(nodeCard('steps[1]'));
      expect(
        within(yamlBar).queryByRole('button', { name: 'Form' })
      ).toBeNull();

      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'Enter' });

      // The bar of the YAML shows, and the form does not.
      expect(
        within(yamlBar).getByRole('button', { name: 'Form' })
      ).toBeEnabled();
      expect(screen.queryByTestId('ve-inspector')).toBeNull();
    });

    it("opens the YAML on a double click with 'open', then the form", () => {
      renderEditor({ initial: yamlArraySteps, openNdvOn: 'open', bar: true });
      fireEvent.click(nodeCard('steps[1]'));
      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      expect(
        within(yamlBar).queryByRole('button', { name: 'Form' })
      ).toBeNull();

      fireEvent.doubleClick(nodeCard('steps[1]'));

      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      clickForm();
      expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
    });

    it("opens the YAML with the Open command with 'open'", () => {
      renderEditor({ initial: yamlArraySteps, openNdvOn: 'open', bar: true });

      fireEvent.pointerEnter(nodeWrapper('steps[0]'));
      fireEvent.click(
        within(nodeWrapper('steps[0]')).getByLabelText('Open step')
      );

      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      clickForm();
      expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
    });

    // A host that opens what the step calls, such as its child run, takes
    // the open: the YAML of the step stays shut.
    it('lets the host open what a step calls', () => {
      const onOpenCall = vi.fn(() => true);
      renderEditor({
        initial: yamlArraySteps,
        openNdvOn: 'open',
        bar: true,
        onOpenCall,
      });

      fireEvent.doubleClick(nodeCard('steps[1]'));

      expect(onOpenCall).toHaveBeenCalledWith('transform');
      expect(
        within(yamlBar).queryByRole('button', { name: 'Form' })
      ).toBeNull();
    });

    it("shows the YAML on a click with the default 'select'", () => {
      const onYamlVisibleChange = vi.fn();
      renderEditor({ initial: yamlArraySteps, onYamlVisibleChange });

      fireEvent.click(nodeCard('steps[1]'));

      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true);
    });
  });

  describe('step run and upstream data', () => {
    it('shows the run of the step from the host in the form', () => {
      const renderStepRun = vi.fn((name: string) => (
        <p data-testid="host-facts">{name}</p>
      ));
      renderEditor({ initial: yamlArraySteps, renderStepRun, bar: true });

      openForm('steps[1]');

      const inspector = within(screen.getByTestId('ve-inspector'));
      expect(inspector.getByTestId('host-facts')).toHaveTextContent(
        'transform'
      );
      expect(inspector.queryByTestId('step-run-panel')).toBeNull();
      expect(renderStepRun).toHaveBeenCalledWith('transform', 'facts');
    });

    it('keeps the run panel of the editor without a host', () => {
      renderEditor({ initial: yamlArraySteps, bar: true });
      openForm('steps[1]');

      expect(
        within(screen.getByTestId('ve-inspector')).getByTestId('step-run-panel')
      ).toBeInTheDocument();
    });

    it('puts the upstream data and the run in the columns of the host', () => {
      const input = document.createElement('div');
      const output = document.createElement('div');
      document.body.append(input, output);
      try {
        renderEditor({
          initial: yamlArraySteps,
          renderStepRun: (name) => <p data-testid="host-facts">{name}</p>,
          inputColumnRoot: input,
          outputColumnRoot: output,
          bar: true,
        });
        openForm('steps[1]');

        expect(within(input).getByText('Upstream data')).toBeInTheDocument();
        expect(within(output).getByTestId('host-facts')).toHaveTextContent(
          'transform'
        );
        const inspector = screen.getByTestId('ve-inspector');
        expect(inspector).not.toContainElement(
          input.firstElementChild as never
        );
        expect(within(inspector).queryByText('Upstream data')).toBeNull();
        expect(within(inspector).queryByTestId('host-facts')).toBeNull();
      } finally {
        input.remove();
        output.remove();
      }
    });
  });

  describe('step commands', () => {
    // A probe in the toolbar sends commands as the node menus do.
    function CommandProbe({ cmd }: { cmd: StepCommand }) {
      const { onCommand } = useCanvasActions();
      return (
        <button type="button" onClick={() => onCommand({ kind: 'step', cmd })}>
          Probe
        </button>
      );
    }

    it('forwards a step command to the host', () => {
      const onStepCommand = vi.fn();
      const cmd: StepCommand = { kind: 'viewLogs', step: 'fetch' };
      renderEditor({
        initial: yamlArraySteps,
        onStepCommand,
        toolbarStart: <CommandProbe cmd={cmd} />,
      });

      fireEvent.click(screen.getByRole('button', { name: 'Probe' }));

      expect(onStepCommand).toHaveBeenCalledWith(cmd);
    });

    it('lets the host confirm the run of a step', () => {
      const onStepCommand = vi.fn();
      const calls: unknown[] = [];
      api.post = (...args: unknown[]) => calls.push(args);
      renderEditor({ initial: yamlArraySteps, onStepCommand });

      fireEvent.pointerEnter(nodeWrapper('steps[1]'));
      fireEvent.click(
        within(nodeWrapper('steps[1]')).getByLabelText('Run step')
      );

      expect(onStepCommand).toHaveBeenCalledWith({
        kind: 'runStep',
        step: 'transform',
      });
      expect(screen.queryByRole('dialog')).toBeNull();
      expect(calls).toEqual([]);
    });

    // A host that cannot run the steps, such as for a child workflow of the
    // file, gives no Run step to the nodes.
    it('shows no Run step when the host cannot run steps', () => {
      renderEditor({
        initial: yamlArraySteps,
        onStepCommand: vi.fn(),
        stepCommandGates: {
          canRunStep: false,
          canRetry: () => false,
          canSetStatus: () => false,
          hasSubRuns: () => false,
        },
      });

      fireEvent.pointerEnter(nodeWrapper('steps[1]'));

      expect(
        within(nodeWrapper('steps[1]')).queryByLabelText('Run step')
      ).toBeNull();
    });

    it('confirms the run itself without a host', () => {
      renderEditor({ initial: yamlArraySteps });

      fireEvent.pointerEnter(nodeWrapper('steps[1]'));
      fireEvent.click(
        within(nodeWrapper('steps[1]')).getByLabelText('Run step')
      );

      expect(screen.getByRole('dialog')).toHaveTextContent(
        'Run only the step transform now?'
      );
    });

    it('gives the run gates to the canvas only with a command handler', () => {
      const gates = {
        canRunStep: true,
        canRetry: () => true,
        canSetStatus: () => true,
        hasSubRuns: () => false,
      };
      const seen: { gates?: unknown } = {};
      function GateProbe() {
        seen.gates = useCanvasActions().stepCommandGates;
        return null;
      }
      const { update } = renderEditor({
        initial: yamlArraySteps,
        stepCommandGates: gates,
        toolbarStart: <GateProbe />,
      });
      expect(seen.gates).toBeUndefined();

      update({
        initial: yamlArraySteps,
        stepCommandGates: gates,
        onStepCommand: () => undefined,
        toolbarStart: <GateProbe />,
      });
      expect(seen.gates).toBe(gates);
    });
  });

  describe('chrome and notices', () => {
    it('hides the run bar', () => {
      renderEditor({ initial: yamlArraySteps, chrome: { hideRunBar: true } });

      const toolbar = within(screen.getByTestId('ve-toolbar'));
      expect(toolbar.queryByRole('button', { name: 'Run' })).toBeNull();
      expect(toolbar.getByRole('button', { name: 'Undo' })).toBeInTheDocument();
    });

    it('stripes the canvas with chrome.striped only', () => {
      const view = renderEditor({
        initial: yamlArraySteps,
        chrome: { striped: true },
      });
      expect(screen.getByTestId('ve-canvas-frame')).toHaveAttribute(
        'data-striped',
        'true'
      );
      // The dark theme has its own, lighter stripes (the border at 40%).
      const classes = screen.getByTestId('ve-canvas-frame').className;
      expect(classes).toContain('dark:bg-[repeating-linear-gradient(');
      expect(classes).toContain('var(--border)_40%');
      view.unmount();

      renderEditor({ initial: yamlArraySteps });
      expect(screen.getByTestId('ve-canvas-frame')).not.toHaveAttribute(
        'data-striped'
      );
    });

    it('keeps the run bar by default', () => {
      renderEditor({ initial: yamlArraySteps });

      expect(
        within(screen.getByTestId('ve-toolbar')).getByRole('button', {
          name: 'Run',
        })
      ).toBeInTheDocument();
    });

    // Adding after a shorthand step needs the explicit form of the steps.
    function refuseAnEdit() {
      fireEvent.click(nodeCard('steps[0]'));
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = screen.getByRole('combobox', { name: 'Search actions' });
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });
    }

    it('keeps the alert bar without a host', () => {
      renderEditor({ initial: yamlShorthandSteps });

      refuseAnEdit();

      expect(screen.getByRole('alert')).toHaveTextContent(
        'This edit needs the steps in the explicit form'
      );
    });

    it('gives an edit refusal to the host, which can convert and dismiss', () => {
      const notices: (EditorNotice | null)[] = [];
      const onNotice = vi.fn((n: EditorNotice | null) => notices.push(n));
      renderEditor({ initial: yamlShorthandSteps, onNotice });

      refuseAnEdit();

      expect(screen.queryByRole('alert')).toBeNull();
      const notice = last(notices)!;
      expect(notice).toMatchObject({
        kind: 'edit-refused',
        convertible: true,
      });
      expect(notice.message).toContain(
        'This edit needs the steps in the explicit form'
      );

      act(() => notice.convert());
      expect(state.text).not.toBe(yamlShorthandSteps);
      expect(last(notices)).toBeNull();

      refuseAnEdit();
      const again = last(notices);
      // The steps are explicit now: the add works and there is no notice.
      expect(again ?? null).toBeNull();
    });

    it('tells the host that a connection would make a cycle', () => {
      const notices: (EditorNotice | null)[] = [];
      renderEditor({
        initial: yamlArraySteps,
        onNotice: (n) => notices.push(n),
      });

      act(() => view.canvas!.onRefused?.('cycle'));

      expect(last(notices)).toMatchObject({
        kind: 'edit-refused',
        message: 'This dependency makes a cycle.',
        convertible: false,
      });
    });

    it('dismisses a notice of the host', () => {
      const notices: (EditorNotice | null)[] = [];
      renderEditor({
        initial: yamlShorthandSteps,
        onNotice: (n) => notices.push(n),
      });
      refuseAnEdit();
      const notice = last(notices)!;

      act(() => notice.dismiss());

      expect(last(notices)).toBeNull();
    });

    it('hides the bars with compact notices and reports the frozen state', () => {
      const states: VisualEditorState[] = [];
      const { update } = renderEditor({
        initial: yamlArraySteps,
        chrome: { compactNotices: true },
        onStateChange: (s) => states.push(s),
      });
      expect(last(states)?.frozen).toBe(false);

      vi.useFakeTimers();
      try {
        update({
          initial: yamlArraySteps,
          text: yamlUnreadable,
          chrome: { compactNotices: true },
          onStateChange: (s) => states.push(s),
        });
        act(() => {
          vi.advanceTimersByTime(1000);
        });
      } finally {
        vi.useRealTimers();
      }

      expect(last(states)?.frozen).toBe(true);
      expect(screen.queryByRole('status')).toBeNull();
      expect(
        screen.queryByText(
          'YAML has errors: the canvas shows the last valid version'
        )
      ).toBeNull();
      expect(document.querySelector('[data-frozen]')).not.toBeNull();
    });
  });

  // parity:canvas-undo-redo
  describe('state and handle', () => {
    it('reports the state only when a field changes', () => {
      const onStateChange = vi.fn();
      const props = { initial: yamlArraySteps, onStateChange, bar: true };
      const { update } = renderEditor(props);
      expect(onStateChange).toHaveBeenCalledTimes(1);
      expect(onStateChange).toHaveBeenLastCalledWith({
        frozen: false,
        canUndo: false,
        canRedo: false,
        selectedStepName: null,
        drawer: 'none',
        direction: 'TB',
      });

      update({ ...props, toolbarTrail: <span>trail</span> });
      expect(onStateChange).toHaveBeenCalledTimes(1);

      fireEvent.doubleClick(nodeCard('steps[1]'));
      expect(onStateChange).toHaveBeenLastCalledWith(
        expect.objectContaining({
          selectedStepName: 'transform',
          drawer: 'yaml',
        })
      );
      clickForm();
      expect(onStateChange).toHaveBeenLastCalledWith(
        expect.objectContaining({
          selectedStepName: 'transform',
          drawer: 'form',
        })
      );
    });

    function renderWithHandle(props: Partial<HarnessProps> = {}) {
      const handleRef = createRef<VisualEditorHandle>();
      const base = { initial: yamlArraySteps, handleRef, ...props };
      const view = renderEditor(base);
      return {
        ...view,
        update: (next: Partial<HarnessProps>) =>
          view.update({ ...base, ...next }),
        handle: () => handleRef.current!,
      };
    }

    it('fits the view', () => {
      const { handle } = renderWithHandle();
      act(() => handle().fitView());
      expect(view.fitView).toHaveBeenCalledTimes(1);
    });

    it('reveals a known step and refuses an unknown one', () => {
      const onSelectedStepNameChange = vi.fn();
      const { handle } = renderWithHandle({ onSelectedStepNameChange });

      let found = true;
      act(() => {
        found = handle().revealStep('nope');
      });
      expect(found).toBe(false);

      act(() => {
        found = handle().revealStep('publish');
      });
      expect(found).toBe(true);
      expect(isSelected('steps[2]')).toBe(true);
      expect(view.revealStep).toHaveBeenCalledWith('steps[2]');
      expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('publish');
    });

    // The History tab of the Run panel names the server step; the canvas
    // lights its node and the selection stays.
    it('spotlights the node of a server step name without selecting it', () => {
      const onSelectedStepNameChange = vi.fn();
      const { handle } = renderWithHandle({ onSelectedStepNameChange });

      act(() => handle().highlightStep('publish'));
      expect(view.highlightStep).toHaveBeenLastCalledWith('steps[2]');
      act(() => handle().highlightStep('nope'));
      expect(view.highlightStep).toHaveBeenLastCalledWith(null);
      act(() => handle().highlightStep(null));
      expect(view.highlightStep).toHaveBeenLastCalledWith(null);
      expect(isSelected('steps[2]')).toBe(false);
      expect(onSelectedStepNameChange).not.toHaveBeenCalled();
      expect(view.revealStep).not.toHaveBeenCalled();
    });

    it('opens the list of steps only when the user may edit', () => {
      const { handle } = renderWithHandle({ editable: false });
      act(() => handle().addStep());
      expect(screen.queryByTestId('ve-add-step')).toBeNull();
    });

    it('opens the list of steps after the selection', () => {
      const { handle } = renderWithHandle();
      fireEvent.click(nodeCard('steps[0]'));
      act(() => handle().addStep());
      expect(screen.getByTestId('ve-add-step')).toBeInTheDocument();
      expect(screen.getByText('After fetch')).toBeInTheDocument();
    });

    it('names the place of a first or a route target', () => {
      const { handle } = renderWithHandle();
      fireEvent.click(nodeCard('steps[1]'));
      act(() => handle().addStep({ mode: 'first' }));
      expect(screen.getByText('First step')).toBeInTheDocument();
      act(() =>
        handle().addStep({ mode: 'route', router: 'steps[0]', port: '2xx' })
      );
      expect(screen.getByText('Route 2xx of fetch')).toBeInTheDocument();
    });

    it('does not open the list of steps on a frozen canvas', () => {
      const { handle, update } = renderWithHandle();
      vi.useFakeTimers();
      try {
        update({ text: yamlUnreadable });
        act(() => {
          vi.advanceTimersByTime(1000);
        });
      } finally {
        vi.useRealTimers();
      }
      act(() => handle().addStep({ mode: 'first' }));
      expect(screen.queryByTestId('ve-add-step')).toBeNull();
    });

    it('toggles the workflow panel', () => {
      const { handle } = renderWithHandle({
        workflowPanel: <p>About the workflow</p>,
      });
      const button = screen.getByRole('button', { name: 'Workflow' });
      act(() => handle().openWorkflowPanel());
      expect(button).toHaveAttribute('aria-pressed', 'true');
      act(() => handle().openWorkflowPanel());
      expect(button).toHaveAttribute('aria-pressed', 'false');
      act(() => handle().openWorkflowPanel(true));
      act(() => handle().openWorkflowPanel(true));
      expect(button).toHaveAttribute('aria-pressed', 'true');
    });

    it('toggles the YAML through the host', () => {
      const onYamlVisibleChange = vi.fn();
      const { handle } = renderWithHandle({ onYamlVisibleChange });
      act(() => handle().toggleYaml());
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(true, 'panel');
      act(() => handle().toggleYaml(false));
      expect(onYamlVisibleChange).toHaveBeenLastCalledWith(false);
    });

    it('opens the form of a step by its server name', () => {
      const { handle } = renderWithHandle();
      let opened = false;
      act(() => {
        opened = handle().openForm('transform');
      });
      expect(opened).toBe(true);
      expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
      act(() => {
        opened = handle().openForm('nope');
      });
      expect(opened).toBe(false);
    });

    it('copies, pastes and duplicates through the clipboard operations', () => {
      const { handle } = renderWithHandle();
      expect(handle().copySelection()).toBeNull();

      fireEvent.click(nodeCard('steps[1]'));
      expect(handle().copySelection()).toContain('name: transform');
      expect(copyStepsYaml).toHaveBeenCalledWith(
        expect.objectContaining({ text: yamlArraySteps }),
        ['steps[1]']
      );

      const yaml = '- name: x\n  run: echo x\n';
      let result: EditResult | undefined;
      act(() => {
        result = handle().paste(yaml);
      });
      expect(result?.ok).toBe(true);
      expect(pasteStepsOp).toHaveBeenCalledWith(
        expect.objectContaining({ text: yamlArraySteps }),
        yaml,
        'steps[1]'
      );
      expect(parseDagDoc(state.text).steps).toHaveLength(4);
      expect(state.text).toContain('name: x');

      // The pasted step is now the selection.
      const pasted = state.text;
      act(() => {
        result = handle().duplicateSelection();
      });
      expect(result?.ok).toBe(true);
      expect(duplicateStepsOp).toHaveBeenCalledWith(
        expect.objectContaining({ text: pasted }),
        ['steps[2]']
      );
      expect(parseDagDoc(state.text).steps).toHaveLength(5);
    });

    // Each paste and each duplicate is one undo step, also back to back.
    it('undoes two clipboard edits in a row one at a time', async () => {
      const { handle } = renderWithHandle();
      fireEvent.click(nodeCard('steps[1]'));

      await act(async () => {
        handle().paste('- name: x\n  run: echo x\n');
        await Promise.resolve();
      });
      const pasted = state.text;
      await act(async () => {
        handle().duplicateSelection();
        await Promise.resolve();
      });
      expect(parseDagDoc(state.text).steps).toHaveLength(5);

      act(() => handle().undo());
      expect(state.text).toBe(pasted);
      act(() => handle().undo());
      expect(state.text).toBe(yamlArraySteps);
    });

    it('copies and duplicates the node of a menu, and pastes the copy', () => {
      renderWithHandle();
      const canvas = () => view.canvas!;
      expect(canvas().canPaste).toBe(false);

      act(() => canvas().onCommand({ kind: 'copy', step: 'steps[2]' }));
      expect(copyStepsYaml).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: yamlArraySteps }),
        ['steps[2]']
      );
      expect(canvas().canPaste).toBe(true);

      // No clipboard text: the steps copied last are pasted.
      act(() => canvas().onCommand({ kind: 'paste', text: '' }));
      expect(pasteStepsOp).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: yamlArraySteps }),
        expect.stringContaining('name: publish'),
        null
      );
      expect(parseDagDoc(state.text).steps).toHaveLength(4);

      act(() => canvas().onCommand({ kind: 'duplicate', step: 'steps[0]' }));
      expect(duplicateStepsOp).toHaveBeenLastCalledWith(expect.anything(), [
        'steps[0]',
      ]);
      expect(parseDagDoc(state.text).steps).toHaveLength(5);
    });

    it('gives Escape to the host through chrome.escapeToHost', () => {
      const { update } = renderWithHandle({ chrome: { escapeToHost: true } });
      expect(view.canvas?.escapeToHost).toBe(true);
      update({ chrome: {} });
      expect(view.canvas?.escapeToHost).toBe(false);
    });

    it('pastes nothing for a reader', () => {
      const { handle } = renderWithHandle({ editable: false });
      fireEvent.click(nodeCard('steps[1]'));
      expect(handle().paste('- name: x\n')).toMatchObject({ ok: false });
      expect(handle().duplicateSelection()).toMatchObject({ ok: false });
      expect(pasteStepsOp).not.toHaveBeenCalled();
      expect(duplicateStepsOp).not.toHaveBeenCalled();
    });

    it('applies a pasted text as one undo step and pans to its step', () => {
      const pasted = `${yamlArraySteps}\n  - name: pasted\n    run: echo pasted\n`;
      vi.mocked(pasteStepsOp).mockReturnValueOnce({
        ok: true,
        text: pasted,
        edits: [],
        select: 'steps[3]',
      });
      const { handle } = renderWithHandle();
      act(() => {
        handle().paste('- name: pasted\n');
      });
      expect(state.text).toBe(pasted);
      expect(screen.getByTestId('ve-canvas-node-steps[3]')).toBeInTheDocument();
      expect(view.revealStep).toHaveBeenCalledWith('steps[3]');

      act(() => handle().undo());
      expect(state.text).toBe(yamlArraySteps);
      act(() => handle().redo());
      expect(state.text).toBe(pasted);
    });

    it('undoes and redoes through the edit session', () => {
      const { handle } = renderWithHandle();
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      fireEvent.keyDown(
        screen.getByRole('combobox', { name: 'Search actions' }),
        { key: 'Enter' }
      );
      const edited = state.text;
      expect(edited).not.toBe(yamlArraySteps);

      act(() => handle().undo());
      expect(state.text).toBe(yamlArraySteps);
      act(() => handle().redo());
      expect(state.text).toBe(edited);
    });

    it('does not undo for a reader', () => {
      const { handle, update } = renderWithHandle();
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      fireEvent.keyDown(
        screen.getByRole('combobox', { name: 'Search actions' }),
        { key: 'Enter' }
      );
      const edited = state.text;
      update({ editable: false });
      act(() => handle().undo());
      expect(state.text).toBe(edited);
    });

    it('pans to the step that an edit selects', () => {
      renderEditor({ initial: yamlArraySteps });
      fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
      const search = screen.getByRole('combobox', { name: 'Search actions' });
      fireEvent.change(search, { target: { value: 'http.request' } });
      fireEvent.keyDown(search, { key: 'Enter' });

      expect(view.revealStep).toHaveBeenCalledWith('steps[3]');
    });
  });
});

/** A right-side panel of another component, with a button that claims it. */
function PanelClaimer({
  id,
  onEvicted,
}: {
  id: RightPanelId;
  onEvicted(): void;
}) {
  const { claim } = useRightPanel(id, onEvicted);
  return (
    <button type="button" onClick={claim}>
      Claim {id}
    </button>
  );
}

/** Roots in the document for the drawer body and the host columns. */
function hostRoots() {
  const roots = {
    body: document.createElement('div'),
    input: document.createElement('div'),
    output: document.createElement('div'),
  };
  document.body.append(roots.body, roots.input, roots.output);
  return {
    ...roots,
    remove: () => Object.values(roots).forEach((root) => root.remove()),
  };
}

describe.skipIf(realSchema === null)('VisualEditor seams of the NDV', () => {
  it('reads the direction from the shared hook and still toggles it', () => {
    const onStateChange = vi.fn();
    renderEditor({ initial: yamlArraySteps, onStateChange });
    expect(vi.mocked(useCanvasDirection)).toHaveBeenCalled();

    const leftToRight = screen.getByRole('button', { name: 'Left to right' });
    expect(leftToRight).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(leftToRight);

    expect(leftToRight).toHaveAttribute('aria-pressed', 'true');
    expect(onStateChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ direction: 'LR' })
    );
  });

  // parity:drawer-shell
  it('closes the ndv panel when the list of steps opens, and back', () => {
    const ndvEvicted = vi.fn();
    render(
      wrap(
        <RightPanelProvider>
          <Harness initial={yamlArraySteps} />
          <PanelClaimer id="ndv" onEvicted={ndvEvicted} />
        </RightPanelProvider>
      )
    );

    fireEvent.click(screen.getByText('Claim ndv'));
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'n' });
    expect(ndvEvicted).toHaveBeenCalledTimes(1);
    expect(
      screen.getByRole('combobox', { name: 'Search actions' })
    ).toBeInTheDocument();

    fireEvent.click(screen.getByText('Claim ndv'));
    expect(
      screen.queryByRole('combobox', { name: 'Search actions' })
    ).toBeNull();
  });

  it('claims ndv for the step inspector drawer', () => {
    const workflowEvicted = vi.fn();
    render(
      wrap(
        <RightPanelProvider>
          <Harness initial={yamlArraySteps} bar />
          <PanelClaimer id="workflow" onEvicted={workflowEvicted} />
        </RightPanelProvider>
      )
    );

    fireEvent.click(screen.getByText('Claim workflow'));
    openForm('steps[1]');
    expect(screen.getByTestId('ve-inspector')).toBeInTheDocument();
    expect(workflowEvicted).toHaveBeenCalledTimes(1);
  });

  // parity:upstream-data-panel
  it('fills the host columns while the YAML of a step shows', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
      });

      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      const upstream = within(roots.input).getByText('Upstream data');
      expect(upstream.closest('[data-layout]')).toHaveAttribute(
        'data-layout',
        'column'
      );
      expect(within(roots.input).getByText('fetch')).toBeInTheDocument();
      // Without renderStepRun and without a run, OUTPUT lists the outputs
      // that the definition declares, and shows no run text.
      const declared = within(roots.output).getByTestId('declared-outputs');
      expect(within(declared).getByText('result')).toBeInTheDocument();
      expect(
        within(declared).getByText('${steps.names.outputs.result}')
      ).toBeInTheDocument();
      expect(within(roots.output).queryByTestId('step-run-panel')).toBeNull();
      expect(within(roots.output).queryByText('No run selected')).toBeNull();
    } finally {
      roots.remove();
    }
  });

  // The button sits in the INPUT column, on the row of Upstream data.
  it('runs the upstream steps from the INPUT column when the host can run', () => {
    const roots = hostRoots();
    const gates = {
      canRunStep: true,
      canRetry: () => false,
      canSetStatus: () => false,
      hasSubRuns: () => false,
    };
    const props = {
      initial: yamlHttpJq,
      validation: { issues: [], referenceScope: scopeHttpJq },
      onSelectedStepNameChange: vi.fn(),
      yamlVisible: true,
      onYamlVisibleChange: vi.fn(),
      drawerBodyRoot: roots.body,
      formVisible: false,
      inputColumnRoot: roots.input,
      outputColumnRoot: roots.output,
    };
    try {
      const onStepCommand = vi.fn();
      const view = renderEditor({
        ...props,
        selectedStepName: 'extract',
        onStepCommand,
        stepCommandGates: gates,
      });
      const button = within(roots.input).getByRole('button', {
        name: 'Run upstream steps',
      });
      expect(button).toBeEnabled();
      expect(button.parentElement).toContainElement(
        within(roots.input).getByText('Upstream data')
      );
      fireEvent.click(button);
      expect(onStepCommand).toHaveBeenCalledWith({
        kind: 'runUpstream',
        step: 'extract',
      });

      // The host cannot run: no button.
      view.update({
        ...props,
        selectedStepName: 'extract',
        onStepCommand,
        stepCommandGates: { ...gates, canRunStep: false },
      });
      expect(
        within(roots.input).queryByRole('button', {
          name: 'Run upstream steps',
        })
      ).toBeNull();

      // The first step has no upstream step: no button.
      view.update({
        ...props,
        selectedStepName: 'fetch',
        onStepCommand,
        stepCommandGates: gates,
      });
      expect(
        within(roots.input).queryByRole('button', {
          name: 'Run upstream steps',
        })
      ).toBeNull();
    } finally {
      roots.remove();
    }
  });

  // The run of Execute step fills the OUTPUT column of its own step only.
  it('shows the followed Execute step run in the OUTPUT column', () => {
    const roots = hostRoots();
    const props = {
      initial: yamlHttpJq,
      validation: { issues: [], referenceScope: scopeHttpJq },
      onSelectedStepNameChange: vi.fn(),
      yamlVisible: true,
      onYamlVisibleChange: vi.fn(),
      drawerBodyRoot: roots.body,
      formVisible: false,
      inputColumnRoot: roots.input,
      outputColumnRoot: roots.output,
    };
    const followedRun = {
      kind: 'execute',
      step: 'extract',
      runId: 'r9',
      steps: ['extract'],
      startedAt: 1,
    } as const;
    try {
      const view = renderEditor({
        ...props,
        selectedStepName: 'extract',
        followedRuns: [followedRun],
      });
      expect(within(roots.output).getByTestId('step-run-panel')).toBeVisible();
      expect(within(roots.output).queryByTestId('declared-outputs')).toBeNull();

      // Another step keeps its declared outputs.
      view.update({
        ...props,
        selectedStepName: 'fetch',
        followedRuns: [followedRun],
      });
      expect(within(roots.output).queryByTestId('step-run-panel')).toBeNull();
    } finally {
      roots.remove();
    }
  });

  // A host with renderStepOutput owns the whole OUTPUT column: it gets the
  // followed Execute run, or null, and the declared outputs to show itself.
  it('hands the OUTPUT column to renderStepOutput', () => {
    const roots = hostRoots();
    const renderStepOutput = vi.fn(
      (step: string, ctx: { runId: string | null; declared: ReactNode }) => (
        <div data-testid="host-output" data-run={ctx.runId ?? ''}>
          {step}
          {ctx.declared}
        </div>
      )
    );
    const props = {
      initial: yamlHttpJq,
      validation: { issues: [], referenceScope: scopeHttpJq },
      selectedStepName: 'extract',
      onSelectedStepNameChange: vi.fn(),
      yamlVisible: true,
      onYamlVisibleChange: vi.fn(),
      drawerBodyRoot: roots.body,
      formVisible: false,
      inputColumnRoot: roots.input,
      outputColumnRoot: roots.output,
      renderStepOutput,
    };
    try {
      const view = renderEditor(props);
      const out = within(roots.output).getByTestId('host-output');
      expect(out).toHaveAttribute('data-run', '');
      expect(out).toHaveTextContent('extract');
      expect(within(out).getByTestId('declared-outputs')).toBeVisible();
      expect(within(roots.output).queryByTestId('step-run-panel')).toBeNull();

      view.update({
        ...props,
        followedRuns: [
          {
            kind: 'execute',
            step: 'extract',
            runId: 'r9',
            steps: ['extract'],
            startedAt: 1,
          },
        ],
      });
      expect(within(roots.output).getByTestId('host-output')).toHaveAttribute(
        'data-run',
        'r9'
      );
    } finally {
      roots.remove();
    }
  });

  it('gives the followed upstream run to the INPUT column of its step', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
        followedRuns: [
          {
            kind: 'upstream',
            step: 'extract',
            runId: 'r9',
            steps: ['fetch'],
            startedAt: Date.now(),
          },
        ],
      });
      expect(
        within(roots.input).getByTestId('upstream-state-fetch')
      ).toHaveAttribute('data-state', 'waiting');
      expect(
        within(roots.input).getByTestId('upstream-last-run')
      ).toBeVisible();
    } finally {
      roots.remove();
    }
  });

  // The INPUT run: the followed upstream run, else the run that the host says
  // feeds the step, else the selected run (no host field: nothing changes).
  describe('INPUT run', () => {
    const base = (roots: ReturnType<typeof hostRoots>) => ({
      initial: yamlHttpJq,
      validation: { issues: [], referenceScope: scopeHttpJq },
      selectedStepName: 'extract',
      onSelectedStepNameChange: vi.fn(),
      yamlVisible: true,
      onYamlVisibleChange: vi.fn(),
      drawerBodyRoot: roots.body,
      formVisible: false,
      inputColumnRoot: roots.input,
      outputColumnRoot: roots.output,
      runSource: { runId: 'sel-1', details: null },
    });
    const inputRun = {
      runId: 'src-1',
      stale: new Set(['fetch']),
      missing: new Set<string>(),
      waitingFor: null,
    };

    it('is the selected run without the host field', () => {
      const roots = hostRoots();
      try {
        renderEditor(base(roots));
        expect(within(roots.input).getByText('Run sel-1')).toBeVisible();
        expect(within(roots.input).queryByTestId('upstream-input-run')).toBe(
          null
        );
      } finally {
        roots.remove();
      }
    });

    it('is the run that the host gives, with its marks', () => {
      const roots = hostRoots();
      try {
        renderEditor({ ...base(roots), inputRun });
        expect(
          within(roots.input).getByTestId('upstream-input-run')
        ).toHaveAttribute('data-run-id', 'src-1');
        expect(within(roots.input).queryByText('Run sel-1')).toBeNull();
        expect(
          within(roots.input).getByTestId('upstream-mark-fetch')
        ).toHaveTextContent('changed since run src-1');
      } finally {
        roots.remove();
      }
    });

    it('is the followed upstream run when there is one', () => {
      const roots = hostRoots();
      try {
        renderEditor({
          ...base(roots),
          inputRun,
          followedRuns: [
            {
              kind: 'upstream',
              step: 'extract',
              runId: 'r9',
              steps: ['fetch'],
              startedAt: Date.now(),
            },
          ],
        });
        expect(
          within(roots.input).getByTestId('upstream-input-run')
        ).toHaveAttribute('data-run-id', 'r9');
      } finally {
        roots.remove();
      }
    });
  });

  // The validation of a draft can give a DAG without the step: the columns
  // keep the step that they showed.
  it('keeps the host columns when a draft gives a DAG without the step', () => {
    const roots = hostRoots();
    try {
      const initial = yamlHttpJq
        .replace('    name: fetch\n', '')
        .replace('    name: extract\n', '');
      const props = {
        initial,
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
      };
      const named = {
        issues: [],
        referenceScope: scopeHttpJq,
        dag: {
          name: 'fetch-items',
          steps: [{ name: 'fetch' }, { name: 'extract' }],
        } as unknown as ApiDAGDetails,
      };
      const { update } = renderEditor({ ...props, validation: named });
      expect(
        within(roots.output).getByTestId('declared-outputs')
      ).toBeInTheDocument();

      update({
        ...props,
        validation: {
          ...named,
          dag: { name: 'fetch-items', steps: [] } as unknown as ApiDAGDetails,
        },
      });

      expect(
        within(roots.output).getByTestId('declared-outputs')
      ).toBeInTheDocument();
      expect(within(roots.input).getByText('fetch')).toBeInTheDocument();
    } finally {
      roots.remove();
    }
  });

  it('names the action of a step as the action picker does', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
      });
      const declared = within(roots.output).getByTestId('declared-outputs');
      expect(within(declared).getByText('jq.filter')).toBeInTheDocument();
    } finally {
      roots.remove();
    }
  });

  // The columns belong to the node view, not to the form: the facts of the
  // host show in OUTPUT while the YAML of the step shows.
  it('puts the facts of the host in OUTPUT while the YAML shows', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
        renderStepRun: (name) => <p data-testid="host-facts">{name}</p>,
      });
      expect(screen.queryByTestId('ve-inspector')).toBeNull();
      expect(within(roots.output).getByTestId('host-facts')).toHaveTextContent(
        'extract'
      );
      expect(
        within(roots.input).getByText('Upstream data')
      ).toBeInTheDocument();
    } finally {
      roots.remove();
    }
  });

  it('keeps the run panel in OUTPUT when the host gives a run id', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: false,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
        runSource: { runId: 'run-7', details: null },
      });
      expect(
        within(roots.output).getByTestId('step-run-panel')
      ).toBeInTheDocument();
      expect(within(roots.output).queryByTestId('declared-outputs')).toBeNull();
    } finally {
      roots.remove();
    }
  });

  it('leaves the host columns empty without a selected step', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
      });
      expect(roots.input).toBeEmptyDOMElement();
      expect(roots.output).toBeEmptyDOMElement();
    } finally {
      roots.remove();
    }
  });

  // parity:upstream-insert-into-field
  it('inserts a value from the INPUT column into the focused field', () => {
    vi.useFakeTimers();
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: true,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
      });
      const filter = within(roots.body).getByLabelText(
        'filter'
      ) as HTMLInputElement;
      fireEvent.focusIn(filter);
      filter.setSelectionRange(filter.value.length, filter.value.length);

      const insert = within(roots.input)
        .getAllByRole('button')
        .find((button) => button.title === '${steps.fetch.outputs.body}');
      fireEvent.click(insert!);
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      expect(filter.value).toBe('[.[].name]${steps.fetch.outputs.body}');
      expect(state.text).toContain('[.[].name]${steps.fetch.outputs.body}');
    } finally {
      roots.remove();
      vi.useRealTimers();
    }
  });

  // parity:drawer-shell
  it('locks the form for a reader, and keeps OUTPUT', () => {
    const roots = hostRoots();
    try {
      renderEditor({
        initial: yamlHttpJq,
        editable: false,
        validation: { issues: [], referenceScope: scopeHttpJq },
        selectedStepName: 'extract',
        onSelectedStepNameChange: vi.fn(),
        yamlVisible: true,
        onYamlVisibleChange: vi.fn(),
        drawerBodyRoot: roots.body,
        formVisible: true,
        inputColumnRoot: roots.input,
        outputColumnRoot: roots.output,
        renderStepRun: (name) => <p data-testid="host-facts">{name}</p>,
      });

      const filter = within(roots.body).getByLabelText(
        'filter'
      ) as HTMLInputElement;
      expect(filter.disabled || filter.readOnly).toBe(true);
      expect(
        within(roots.input).queryAllByRole('button', { name: 'Insert' })
      ).toEqual([]);
      expect(
        within(roots.input).queryAllByRole('button', { name: 'Copy reference' })
      ).toEqual([]);
      expect(within(roots.output).getByTestId('host-facts')).toHaveTextContent(
        'extract'
      );
    } finally {
      roots.remove();
    }
  });
});
