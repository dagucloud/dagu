// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { createRef, useState, type ReactNode, type RefObject } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type {
  DagDocModel,
  ReferenceCandidate,
  StepInspectorProps,
  StepView,
  ValidationIssue,
  VisualEditorHandle,
} from '../../contracts';
import { parseDagDoc } from '../../doc/parse';
import { DOC_MODEL_DELAY_MS } from '../../useDocModel';
import { catalogSample, yamlHttpJq } from '../../testing/fixtures';
import { hiddenAreasAround } from '../../useYamlEditorSync';
import { VisualEditor } from '../../VisualEditor';
import {
  InspectorViewProvider,
  useInspectorView,
  useInspectorViewState,
  type InspectorView,
} from '../InspectorViewContext';
import {
  fieldPathAncestors,
  sectionOfPath,
  segmentOfSection,
  StepInspector,
} from '../StepInspector';
import {
  httpStep,
  httpStepWithHeaders,
  jqStep,
  modelHttpJq,
  routerStep,
  runStep,
  schemaSample,
} from './fixtures';
import { realSchema } from './realSchema';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));
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
vi.mock('../../run/StepRunPanel', () => ({
  StepRunPanel: () => <p data-testid="step-run-panel" />,
}));

/** Every step kind of the fixtures in one model. */
const model: DagDocModel = {
  ...modelHttpJq,
  steps: [
    {
      ...httpStep,
      value: {
        ...httpStep.value,
        timeout_sec: 30,
        retry_policy: { limit: 2 },
        continue_on: { failure: true },
        preconditions: [{ condition: '${READY}', expected: 'yes' }],
        env: { MODE: 'fast' },
        outputs: [{ name: 'body' }],
      },
    },
    jqStep,
    runStep,
    routerStep,
  ],
};

const candidates: ReferenceCandidate[] = [
  {
    token: '${params.API_URL}',
    label: 'API_URL',
    group: 'params',
    known: 'static',
  },
];

const issue = (fieldPath: string): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message: `problem in ${fieldPath}`,
  stepPath: fieldPath.split('.')[0],
  fieldPath,
});

/** The view of the provider, for a test that drives it from outside. */
const latest: { view: InspectorView | null } = { view: null };

function ViewProbe() {
  latest.view = useInspectorView();
  return null;
}

function Split({ children }: { children: ReactNode }) {
  const view = useInspectorViewState();
  return (
    <InspectorViewProvider value={view}>
      {children}
      <ViewProbe />
    </InspectorViewProvider>
  );
}

function inspector(props: Partial<StepInspectorProps> = {}) {
  return (
    <StepInspector
      model={model}
      step="steps[0]"
      schema={realSchema ?? schemaSample}
      catalog={catalogSample}
      customActions={[]}
      candidates={candidates}
      issues={[]}
      readOnly={false}
      onEdit={vi.fn()}
      onClose={vi.fn()}
      embedded
      {...props}
    />
  );
}

function renderSplit(props: Partial<StepInspectorProps> = {}) {
  const result = render(<Split>{inspector(props)}</Split>);
  return {
    ...result,
    rerenderWith: (next: Partial<StepInspectorProps>) =>
      result.rerender(<Split>{inspector({ ...props, ...next })}</Split>),
  };
}

const segmentGroup = () =>
  screen.getByRole('group', { name: 'Part of the form to show' });
const segmentButton = (name: RegExp) =>
  within(segmentGroup()).getByRole('button', { name });
const parametersPart = () =>
  document.querySelector<HTMLElement>('[data-segment="parameters"]')!;
const settingsPart = () =>
  document.querySelector<HTMLElement>('[data-segment="settings"]')!;
const box = (path: string) =>
  document.querySelector<HTMLElement>(`[data-field-path="${path}"]`)!;

function focusPath(path: string) {
  act(() => latest.view?.setFocusPath(path));
}

beforeEach(() => {
  latest.view = null;
  localStorage.clear();
});
afterEach(() => vi.useRealTimers());

describe.skipIf(realSchema === null)('Step form in a split view', () => {
  // parity:test-ve-inspector
  // parity:inspector-container
  // parity:drawer-step-definition
  // parity:dup-5
  it('shows Parameters | Settings with the issue count of each part', () => {
    renderSplit({
      issues: [
        issue('steps[0].with.url'),
        issue('steps[0].name'),
        issue('steps[0].timeout_sec'),
      ],
    });

    const parameters = segmentButton(/^Parameters/);
    const settings = segmentButton(/^Settings/);
    expect(parameters).toHaveAttribute('aria-pressed', 'true');
    expect(settings).toHaveAttribute('aria-pressed', 'false');
    expect(within(parameters).getByLabelText('2 issues')).toHaveTextContent(
      '2'
    );
    expect(within(settings).getByLabelText('1 issue')).toHaveTextContent('1');

    // Parameters: General, What it does and the parameters of the action.
    expect(parametersPart()).toBeVisible();
    expect(settingsPart()).not.toBeVisible();
    for (const title of ['General', 'What it does', 'Parameters']) {
      expect(
        within(parametersPart()).getByRole('button', {
          name: new RegExp(`^${title}`),
        })
      ).toBeInTheDocument();
    }
    // The step YAML is the YAML segment of the frame, not a section here.
    expect(screen.queryByRole('button', { name: /^YAML/ })).toBeNull();

    // Settings: Flow.
    fireEvent.click(settings);
    expect(settings).toHaveAttribute('aria-pressed', 'true');
    expect(settingsPart()).toBeVisible();
    expect(parametersPart()).not.toBeVisible();
    expect(
      within(settingsPart()).getByRole('button', { name: /^Flow/ })
    ).toBeInTheDocument();
  });

  it('keeps one list of sections and the YAML section without a host', () => {
    render(inspector());
    expect(
      screen.queryByRole('group', { name: 'Part of the form to show' })
    ).toBeNull();
    expect(document.querySelector('[data-segment]')).toBeNull();
    expect(screen.getByRole('button', { name: 'YAML' })).toBeInTheDocument();
  });

  // parity:inspector-run-editor
  it('keeps the hidden part mounted through two switches', async () => {
    renderSplit({ step: 'steps[2]' });
    const editor = await screen.findByTestId('monaco');
    expect(editor).toHaveValue('make build\nmake test\n');
    const general = within(parametersPart()).getByRole('button', {
      name: /^General/,
    });
    fireEvent.click(general);
    expect(general).toHaveAttribute('aria-expanded', 'false');

    for (let round = 0; round < 2; round++) {
      fireEvent.click(segmentButton(/^Settings/));
      expect(parametersPart()).not.toBeVisible();
      fireEvent.click(segmentButton(/^Parameters/));
    }

    // The same editor instance and the same collapsed section.
    expect(await screen.findByTestId('monaco')).toBe(editor);
    expect(
      within(parametersPart()).getByRole('button', { name: /^General/ })
    ).toBe(general);
    expect(general).toHaveAttribute('aria-expanded', 'false');
  });

  it('maps a field path to its section and part', () => {
    expect(sectionOfPath('name')).toBe('general');
    expect(sectionOfPath('shell')).toBe('action');
    expect(sectionOfPath('with.headers.X')).toBe('parameters');
    expect(sectionOfPath('outputs[0].name')).toBe('flow');
    expect(sectionOfPath('call')).toBeNull();
    expect(segmentOfSection('flow')).toBe('settings');
    expect(segmentOfSection('parameters')).toBe('parameters');
    expect(fieldPathAncestors('with.items[0].name')).toEqual([
      'with.items[0].name',
      'with.items[0]',
      'with.items',
      'with',
    ]);
  });

  // parity:inspector-parameters-form
  it('shows and focuses a parameter from a focus path', () => {
    renderSplit();
    const parametersSection = within(parametersPart()).getByRole('button', {
      name: /^Parameters/,
    });
    fireEvent.click(parametersSection);
    expect(parametersSection).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(segmentButton(/^Settings/));

    focusPath('with.url');

    expect(segmentButton(/^Parameters/)).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(parametersSection).toHaveAttribute('aria-expanded', 'true');
    expect(box('with.url')).toContainElement(
      document.activeElement as HTMLElement
    );
    expect(document.activeElement).toHaveValue('${params.API_URL}');
    // The path is used once.
    expect(latest.view?.focusPath).toBeNull();
  });

  // parity:inspector-param-widgets
  it('focuses the nearest field of a path that has no field of its own', () => {
    renderSplit({ model: { ...model, steps: [httpStepWithHeaders] } });
    focusPath('with.headers.X.value');
    expect(box('with.headers')).toContainElement(
      document.activeElement as HTMLElement
    );
  });

  // parity:inspector-general
  // parity:inspector-action-picker
  it('focuses the fields of General and What it does', () => {
    renderSplit();
    focusPath('name');
    expect(document.activeElement).toHaveValue('fetch');
    expect(box('name')).toContainElement(document.activeElement as HTMLElement);
    focusPath('description');
    expect(box('description')).toContainElement(
      document.activeElement as HTMLElement
    );
    focusPath('action');
    expect(box('action')).toContainElement(
      document.activeElement as HTMLElement
    );
  });

  it('focuses the command and the shell of a command step', async () => {
    renderSplit({ step: 'steps[2]' });
    focusPath('run');
    expect(document.activeElement).toBe(await screen.findByTestId('monaco'));
    focusPath('shell');
    expect(document.activeElement).toBe(
      within(box('shell')).getByRole('combobox')
    );
  });

  // parity:inspector-flow-depends
  // parity:inspector-flow-timing
  // parity:inspector-flow-continue-on
  // parity:inspector-flow-preconditions
  // parity:inspector-flow-env
  // parity:inspector-flow-outputs
  it('opens Settings on the flow field of a focus path', () => {
    renderSplit();
    const inBox = (path: string) =>
      expect(box(path)).toContainElement(document.activeElement as HTMLElement);

    focusPath('timeout_sec');
    expect(segmentButton(/^Settings/)).toHaveAttribute('aria-pressed', 'true');
    expect(document.activeElement).toHaveValue('30');
    focusPath('retry_policy.limit');
    expect(document.activeElement).toHaveValue('2');
    focusPath('retry_policy.backoff');
    inBox('retry_policy');
    focusPath('continue_on.failure');
    inBox('continue_on');
    expect(document.activeElement).toHaveRole('switch');
    focusPath('preconditions[0].condition');
    inBox('preconditions[0]');
    expect(document.activeElement).toHaveValue('${READY}');
    focusPath('env');
    inBox('env');
    focusPath('outputs[0].name');
    expect(document.activeElement).toHaveValue('body');
    // A read-only list takes the focus itself.
    focusPath('depends');
    expect(document.activeElement).toBe(box('depends'));
  });

  // parity:inspector-route-table
  it('focuses the route table of a router', () => {
    renderSplit({ step: 'steps[3]' });
    focusPath('with.routes');
    expect(box('with.routes')).toContainElement(
      document.activeElement as HTMLElement
    );
    expect(box('with.value')).toBeInTheDocument();
  });

  // parity:reference-input
  // parity:reference-picker
  it('inserts a reference into a parameter through the picker', () => {
    renderSplit({
      model: {
        ...model,
        steps: [{ ...httpStep, value: { ...httpStep.value, with: {} } }],
      },
    });
    const url = within(box('with.url')).getByRole('textbox');
    act(() => url.focus());
    fireEvent.click(
      within(box('with.url')).getByRole('button', { name: 'Insert reference' })
    );
    const list = screen.getByRole('listbox', { name: 'References' });
    fireEvent.click(within(list).getByRole('option', { name: /API_URL/ }));
    expect(url).toHaveValue('${params.API_URL}');
  });

  // A required field of an optional object is not a field that the step
  // must have, so a new step does not start in it.
  it('starts a new step in a parameter that the step must have', () => {
    const notify: StepView = {
      ...httpStep,
      key: 'steps[1]',
      index: 1,
      explicitName: 'notify',
      action: 'notify',
      value: { name: 'notify', action: 'notify' },
    };
    const customActions = [
      {
        name: 'notify',
        inputSchema: {
          type: 'object',
          properties: {
            auth: {
              type: 'object',
              properties: { token: { type: 'string' } },
              required: ['token'],
            },
            channel: { type: 'string' },
          },
          required: ['channel'],
        },
      },
    ];
    const { rerenderWith } = renderSplit({
      model: { ...model, steps: [httpStep] },
      customActions,
    });
    rerenderWith({
      model: { ...model, steps: [httpStep, notify] },
      step: 'steps[1]',
    });

    expect(box('with.auth.token')).toBeInTheDocument();
    expect(box('with.channel')).toContainElement(
      document.activeElement as HTMLElement
    );
  });

  it('keeps the part when the step changes', async () => {
    const { rerenderWith } = renderSplit();
    fireEvent.click(segmentButton(/^Settings/));
    rerenderWith({ step: 'steps[1]' });
    expect(segmentButton(/^Settings/)).toHaveAttribute('aria-pressed', 'true');
    expect(settingsPart()).toBeVisible();
  });
});

const host: {
  view: InspectorView | null;
  setText: ((text: string) => void) | null;
  hiddenAreas: unknown[][];
} = { view: null, setText: null, hiddenAreas: [] };

/** The YAML editor of the host, as far as the step scope needs it. */
const yamlEditor = {
  getModel: () => ({
    getValue: () => yamlHttpJq,
    getPositionAt: () => ({ lineNumber: 1, column: 1 }),
    getLineCount: () => yamlHttpJq.split('\n').length,
  }),
  executeEdits: () => true,
  pushUndoStop: () => true,
  trigger: () => undefined,
  setHiddenAreas: (ranges: unknown[]) => {
    host.hiddenAreas.push(ranges);
  },
};

/** A host frame as DAGSpec builds it: one bar, the YAML and the form. */
function NdvHost({
  handleRef,
}: {
  handleRef: RefObject<VisualEditorHandle | null>;
}) {
  const [text, setText] = useState(yamlHttpJq);
  const [yaml, setYaml] = useState(false);
  const [form, setForm] = useState(false);
  const [bar, setBar] = useState<HTMLDivElement | null>(null);
  const [body, setBody] = useState<HTMLDivElement | null>(null);
  const view = useInspectorViewState();
  host.view = view;
  host.setText = setText;
  return (
    <InspectorViewProvider value={view}>
      <VisualEditor
        fileName="example.yaml"
        dagName="example"
        text={text}
        onTextChange={setText}
        validation={null}
        editable
        isDirty={false}
        customActions={[]}
        yamlVisible={yaml}
        onYamlVisibleChange={setYaml}
        yamlToolbarRoot={bar}
        drawerBodyRoot={body}
        formVisible={form}
        onFormVisibleChange={setForm}
        handleRef={handleRef}
        yamlEditorRef={{ current: yamlEditor as never }}
      />
      <section hidden={!yaml} aria-label="Frame">
        <div ref={setBar} />
        <div ref={setBody} hidden={!form} data-testid="frame-body" />
      </section>
      <button type="button">Outside</button>
    </InspectorViewProvider>
  );
}

function renderHost() {
  const handleRef = createRef<VisualEditorHandle>();
  render(
    <UserPreferencesProvider>
      <NdvHost handleRef={handleRef} />
    </UserPreferencesProvider>
  );
  return () => handleRef.current!;
}

function addAction(query: string) {
  const search = screen.getByRole('combobox', { name: 'Search actions' });
  fireEvent.change(search, { target: { value: query } });
  fireEvent.keyDown(search, { key: 'Enter' });
}

const frameBody = () => screen.getByTestId('frame-body');

describe.skipIf(realSchema === null)(
  'Step form in the node details view',
  () => {
    beforeEach(() => {
      host.view = null;
      host.setText = null;
      host.hiddenAreas = [];
    });

    // parity:test-ve-inspector
    // The template fills the required parameters with valid values, so the
    // step starts in its name.
    it('starts a new step with valid parameters in its name', () => {
      const handle = renderHost();
      act(() => void handle().openForm('fetch'));
      expect(
        within(frameBody()).getByRole('group', {
          name: 'Part of the form to show',
        })
      ).toBeInTheDocument();
      act(() => host.view?.setSegment('settings'));

      act(() => handle().addStep());
      addAction('http.request');

      const url = frameBody().querySelector('[data-field-path="with.url"]');
      expect(url?.querySelector('input')).toHaveValue('https://example.com');
      expect(
        frameBody().querySelector('[data-field-path="name"]')
      ).toContainElement(document.activeElement as HTMLElement);
    });

    it('starts a new step without required parameters in its name', () => {
      const handle = renderHost();
      act(() => void handle().openForm('fetch'));
      act(() => handle().addStep());
      addAction('run');

      expect(document.activeElement).toHaveValue('cmd_3');
      expect(
        frameBody().querySelector('[data-field-path="name"]')
      ).toContainElement(document.activeElement as HTMLElement);
    });

    it('leaves the focus alone on a plain selection of a step', () => {
      const handle = renderHost();
      act(() => void handle().openForm('fetch'));
      const outside = screen.getByRole('button', { name: 'Outside' });
      act(() => outside.focus());

      fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
      expect(frameBody()).toHaveTextContent('jq.filter');
      expect(document.activeElement).toBe(outside);

      act(() => void handle().openForm('fetch'));
      expect(document.activeElement).toBe(outside);
    });

    // parity:yaml-form-mode-switch
    // parity:inspector-step-yaml
    // parity:chg-step-yaml-live
    it('switches between the step YAML and the form in one bar', async () => {
      const handle = renderHost();
      act(() => void handle().openForm('fetch'));
      const editorGroup = screen.getByRole('group', { name: 'Editor' });
      const yamlButton = within(editorGroup).getByRole('button', {
        name: 'YAML',
      });
      const formButton = within(editorGroup).getByRole('button', {
        name: 'Form',
      });
      expect(formButton).toHaveAttribute('aria-pressed', 'true');
      // The form has no YAML section of its own, and nothing to apply.
      expect(within(frameBody()).queryByRole('button', { name: /^YAML/ })).toBe(
        null
      );
      expect(within(frameBody()).queryByRole('button', { name: 'Apply' })).toBe(
        null
      );

      fireEvent.click(yamlButton);
      expect(yamlButton).toHaveAttribute('aria-pressed', 'true');
      expect(frameBody()).not.toBeVisible();
      // The YAML segment shows the step alone: the editor hides the lines
      // of the other steps, and the bar has no choice of the part to show.
      expect(
        screen.queryByRole('group', { name: 'Part of the YAML to show' })
      ).toBeNull();
      expect(screen.queryByRole('button', { name: 'File' })).toBeNull();
      const fetch = parseDagDoc(yamlHttpJq).steps[0]!;
      const lines = yamlHttpJq.split('\n').length;
      const hidden = host.hiddenAreas[host.hiddenAreas.length - 1];
      expect(hidden).toEqual(hiddenAreasAround(fetch, lines));
      expect(hidden).toEqual([
        expect.objectContaining({
          startLineNumber: 1,
          endLineNumber: fetch.range.startLine - 1,
        }),
        expect.objectContaining({
          startLineNumber: fetch.range.endLine + 1,
          endLineNumber: lines,
        }),
      ]);

      // An edit in the YAML editor changes the text at once: the form shows
      // it after the parse pause, with no Apply step.
      act(() =>
        host.setText?.(
          yamlHttpJq.replace('url: ${params.API_URL}', 'url: https://x.test')
        )
      );
      await act(
        () =>
          new Promise((resolve) => setTimeout(resolve, DOC_MODEL_DELAY_MS + 10))
      );
      fireEvent.click(formButton);
      expect(frameBody()).toBeVisible();
      expect(
        within(
          frameBody().querySelector<HTMLElement>(
            '[data-field-path="with.url"]'
          )!
        ).getByRole('textbox')
      ).toHaveValue('https://x.test');
    });
  }
);
