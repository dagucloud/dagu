// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { createPortal } from 'react-dom';
import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { realSchema } from '@/features/visual-editor/inspector/__tests__/realSchema';
import { DAGContext } from '../../../contexts/DAGContext';
import DAGSpec, { type DAGSpecHostProps } from '../DAGSpec';
import type {
  VisualEditorHandle,
  VisualEditorState,
  WorkspaceEditorHandle,
} from '../hostTypes';
import type { NdvState } from '../NodeDetailsFrame';

const mocks = vi.hoisted(() => {
  const post = vi.fn();
  const put = vi.fn();
  return {
    post,
    put,
    client: { GET: vi.fn(), POST: post, PUT: put },
    useQuery: vi.fn(),
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));
vi.mock('../../../../../contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../../../../../contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: null }),
}));
vi.mock('../../../../../contexts/UnsavedChangesContext', () => ({
  useUnsavedChanges: () => ({ setHasUnsavedChanges: vi.fn() }),
}));
vi.mock('../../../../../hooks/useDAGSSE', () => ({ useDAGSSE: () => null }));
vi.mock('../../../../../hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));
vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
}));
vi.mock('../ExternalChangeDialog', () => ({ default: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));
vi.mock('../WorkflowPanel', () => ({ WorkflowPanel: () => null }));
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: () => null,
}));
vi.mock('../DAGEditorWithDocs', () => ({
  default: (props: {
    value: string;
    quickFixSource?: { getPlan(): unknown };
  }) => {
    visual.editorProps = props;
    return <textarea aria-label="DAG spec" value={props.value} readOnly />;
  },
}));
vi.mock(
  '@monaco-editor/react',
  async () =>
    (await import('@/features/visual-editor/inspector/__tests__/monacoMock'))
      .monacoModule
);
vi.mock('@/features/visual-editor/inspector/monacoSetup', () => ({
  monacoTheme: () => 'light',
}));
vi.mock('@/features/visual-editor/run/SaveDiffDialog', () => ({
  SaveDiffDialog: () => null,
}));

type VisualProps = {
  text: string;
  toolbarTrail?: React.ReactNode;
  yamlVisible?: boolean;
  onYamlVisibleChange?(visible: boolean): void;
  drawerBodyRoot?: HTMLElement | null;
  formVisible?: boolean;
  onFormVisibleChange?(visible: boolean): void;
  onStateChange?(state: VisualEditorState): void;
  handleRef?: React.Ref<VisualEditorHandle>;
};
const visual = vi.hoisted(() => ({
  editorProps: null as null | {
    quickFixSource?: { getPlan(): unknown };
  },
  formVisible: undefined as boolean | undefined,
  openForm: vi.fn<(name: string) => void>(),
  focusPaths: [] as (string | null)[],
}));

// Stands in for the editor around the real step form: it selects steps by
// their names and shows the form of the selection in the drawer body.
vi.mock('@/features/visual-editor', async () => {
  const { parseDagDoc } = await import('@/features/visual-editor/doc/parse');
  const { StepInspector } = await import(
    '@/features/visual-editor/inspector/StepInspector'
  );
  const { buildActionCatalog } = await import(
    '@/features/visual-editor/inspector/catalog'
  );
  const { useInspectorView } = await import(
    '@/features/visual-editor/inspector/InspectorViewContext'
  );
  const { realSchema: schema } = await import(
    '@/features/visual-editor/inspector/__tests__/realSchema'
  );
  function FocusProbe() {
    const { focusPath } = useInspectorView();
    if (focusPath !== null) visual.focusPaths.push(focusPath);
    return null;
  }
  return {
    default: function MockVisualEditor(props: VisualProps) {
      visual.formVisible = props.formVisible;
      const [selected, setSelected] = React.useState<string | null>(null);
      const model = React.useMemo(() => parseDagDoc(props.text), [props.text]);
      const keyOf = (name: string) =>
        model.steps.find((step) => step.explicitName === name)?.key ?? null;
      const live = React.useRef({ props, keyOf });
      live.current = { props, keyOf };
      React.useImperativeHandle(
        props.handleRef,
        () =>
          ({
            revealStep: (name: string) => {
              if (live.current.keyOf(name) === null) return false;
              setSelected(name);
              return true;
            },
            openYaml: (name: string) => {
              if (live.current.keyOf(name) === null) return false;
              setSelected(name);
              live.current.props.onFormVisibleChange?.(false);
              live.current.props.onYamlVisibleChange?.(true);
              return true;
            },
            openForm: (name: string) => {
              visual.openForm(name);
              if (live.current.keyOf(name) === null) return false;
              setSelected(name);
              live.current.props.onFormVisibleChange?.(true);
              live.current.props.onYamlVisibleChange?.(true);
              return true;
            },
          }) as unknown as VisualEditorHandle,
        []
      );
      const report = React.useRef(props.onStateChange);
      React.useEffect(() => {
        report.current?.({
          frozen: false,
          canUndo: false,
          canRedo: false,
          selectedStepName: selected,
          drawer: 'form',
          direction: 'TB',
        });
      }, [selected]);
      const key = selected === null ? null : keyOf(selected);
      return (
        <div data-testid="visual-editor-mock">
          {props.toolbarTrail}
          <FocusProbe />
          {props.drawerBodyRoot &&
            props.formVisible &&
            key !== null &&
            createPortal(
              <StepInspector
                model={model}
                step={key}
                schema={schema ?? {}}
                catalog={buildActionCatalog(schema ?? {}, [])}
                customActions={[]}
                candidates={[]}
                issues={[]}
                readOnly={false}
                onEdit={() => {}}
                onClose={() => {}}
                embedded
              />,
              props.drawerBodyRoot
            )}
        </div>
      );
    },
  };
});

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const savedSpec = `steps:
  - name: fetch
    action: http.request
    with:
      method: GET
      url: https://example.com
  - name: transform
    depends: [fetch]
    run: sort
  - name: publish
    depends: [transform]
    run: cat
`;

const dag = {
  name: 'example',
  steps: [
    { name: 'fetch', executorConfig: { type: 'http' } },
    { name: 'transform', depends: ['fetch'] },
    { name: 'publish', depends: ['transform'] },
  ],
};

function specData(errors: string[] = []) {
  return {
    data: { dag, spec: savedSpec, errors, valueReferenceNotices: [] },
    isLoading: false,
    mutate: vi.fn(),
  };
}

type HostProps = Partial<DAGSpecHostProps> & {
  hosted?: boolean;
  initialNdv?: NdvState;
};

const editor = { handle: null as WorkspaceEditorHandle | null };

/** A host page that owns the ndv state, or the classic page. */
function Host({ hosted = true, initialNdv = null, ...props }: HostProps) {
  const [state, setState] = React.useState<NdvState>(initialNdv);
  return (
    <DAGSpec
      fileName="example.yaml"
      editorHandleRef={(handle) => {
        editor.handle = handle;
      }}
      {...props}
      {...(hosted
        ? {
            ndv: { state, onStateChange: setState },
            chrome: { compactNotices: true },
          }
        : {})}
    />
  );
}

async function settle() {
  await act(async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
  });
}

async function renderSpec(props: HostProps = {}) {
  render(
    <UserPreferencesProvider>
      <AppBarContext.Provider value={appBarValue}>
        <DAGContext.Provider
          value={{
            refresh: vi.fn(),
            name: 'example',
            fileName: 'example.yaml',
          }}
        >
          <Host {...props} />
        </DAGContext.Provider>
      </AppBarContext.Provider>
    </UserPreferencesProvider>
  );
  // The mocked editor module loads its own imports first.
  await screen.findByTestId('visual-editor-mock', {}, { timeout: 4000 });
  await settle();
}

const frame = () => screen.getByTestId('ndv');
const header = () => within(frame().querySelector('header')!);
const segment = (name: RegExp) =>
  within(
    screen.getByRole('group', { name: 'Part of the form to show' })
  ).getByRole('button', { name });
const formTitle = () =>
  (
    document.querySelector(
      '[data-field-path="name"] input'
    ) as HTMLInputElement | null
  )?.value;

// The mocked editor module loads the real step form once, before the tests.
beforeAll(async () => {
  await import('@/features/visual-editor');
}, 30_000);

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  visual.editorProps = null;
  visual.formVisible = undefined;
  editor.handle = null;
  visual.openForm.mockReset();
  visual.focusPaths = [];
  mocks.useQuery.mockReturnValue(specData());
  mocks.post.mockResolvedValue({
    data: { valid: true, errors: [], dag: undefined },
  });
  mocks.put.mockResolvedValue({ data: { errors: [] } });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe.skipIf(realSchema === null)('DAGSpec ndv segments', () => {
  it('keeps the part across a step selection and the moves of the ndv', async () => {
    await renderSpec();
    act(() => void editor.handle?.openForm('transform'));
    expect(frame()).toBeVisible();
    expect(formTitle()).toBe('transform');
    expect(segment(/^Parameters/)).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(segment(/^Settings/));
    expect(segment(/^Settings/)).toHaveAttribute('aria-pressed', 'true');

    // Next and previous step in the header of the frame.
    fireEvent.click(header().getByRole('button', { name: 'Next step' }));
    expect(formTitle()).toBe('publish');
    expect(segment(/^Settings/)).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(header().getByRole('button', { name: 'Previous step' }));
    fireEvent.click(header().getByRole('button', { name: 'Previous step' }));
    expect(formTitle()).toBe('fetch');
    expect(segment(/^Settings/)).toHaveAttribute('aria-pressed', 'true');

    // A selection from the host.
    act(() => void editor.handle?.openForm('publish'));
    expect(formTitle()).toBe('publish');
    expect(segment(/^Settings/)).toHaveAttribute('aria-pressed', 'true');
  });

  it('opens the form on the field of an issue row', async () => {
    mocks.useQuery.mockReturnValue(specData(['Bad url']));
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['Bad url'],
        issues: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'Bad url',
            stepName: 'fetch',
            stepPath: 'steps[0]',
            fieldPath: 'steps[0].with.url',
          },
        ],
      },
    });
    await renderSpec();
    await settle();
    act(() => void editor.handle?.openForm('transform'));
    fireEvent.click(segment(/^Settings/));
    visual.openForm.mockClear();

    fireEvent.click(screen.getByRole('button', { name: /^Issues \(\d+\)$/ }));
    fireEvent.click(
      within(screen.getByRole('dialog', { name: 'Issues' })).getByRole(
        'button',
        { name: /Bad url/ }
      )
    );

    expect(visual.openForm).toHaveBeenCalledWith('fetch');
    expect(visual.focusPaths).toContain('with.url');
    expect(formTitle()).toBe('fetch');
    expect(segment(/^Parameters/)).toHaveAttribute('aria-pressed', 'true');
    const url = document.querySelector('[data-field-path="with.url"]');
    expect(url).toContainElement(document.activeElement as HTMLElement);
    expect(document.activeElement).toHaveValue('https://example.com');
  });

  // A workflow-level error must not repeat in the footer of every step.
  it('shows an error in the footer of its own step only', async () => {
    mocks.useQuery.mockReturnValue(specData(['Boom', 'Bad url']));
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: ['Boom', 'Bad url'],
        issues: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'Boom',
          },
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'Bad url',
            stepName: 'fetch',
            stepPath: 'steps[0]',
            fieldPath: 'steps[0].url',
          },
        ],
      },
    });
    await renderSpec();
    await settle();

    act(() => void editor.handle?.openForm('transform'));
    expect(screen.queryByTestId('ndv-problems')).toBeNull();

    act(() => void editor.handle?.openForm('fetch'));
    const footer = screen.getByTestId('ndv-problems');
    expect(footer).toHaveTextContent('Bad url');
    expect(footer).not.toHaveTextContent('Boom');
  });

  // The editor of the node view and the editor of the file panel are one
  // component: both get the quick fixes of the issues, with the plan of the
  // text that they show.
  it('gives the editor of the step view the quick fixes of the issues', async () => {
    await renderSpec();
    act(() => void editor.handle?.openYaml('transform'));
    expect(frame()).toBeVisible();
    await waitFor(() =>
      expect(visual.editorProps?.quickFixSource).toBeDefined()
    );
    expect(visual.editorProps?.quickFixSource?.getPlan()).toMatchObject({
      model: { text: savedSpec },
    });
  });

  // A step view that stays open after a reload shows the mode of the last
  // one: the form when the form was used.
  it('starts a mounted view in the last mode of the session', async () => {
    sessionStorage.setItem('dagu.stepView.mode', 'form');
    await renderSpec({ initialNdv: 'expanded', selectedStepName: 'transform' });
    expect(visual.formVisible).toBe(true);
  });

  it('gives the classic page the form of today', async () => {
    await renderSpec({ hosted: false });
    act(() => void editor.handle?.openForm('transform'));
    expect(formTitle()).toBe('transform');
    expect(
      screen.queryByRole('group', {
        name: 'Part of the form to show',
        hidden: true,
      })
    ).toBeNull();
    expect(
      screen.getByRole('button', { name: 'YAML', hidden: true })
    ).toHaveAttribute('aria-expanded', 'false');
  });
});
