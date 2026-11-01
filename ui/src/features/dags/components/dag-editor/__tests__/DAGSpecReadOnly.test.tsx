// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import DAGSpecReadOnly from '../DAGSpecReadOnly';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  navigate: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  showError: vi.fn(),
  showToast: vi.fn(),
  useQuery: vi.fn(),
  runDags: true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: mocks.get, POST: mocks.post, PUT: mocks.put }),
  useQuery: mocks.useQuery,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
  useResolvedTheme: () => 'light',
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: mocks.runDags,
    },
  }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: mocks.showToast }),
}));

vi.mock('react-router-dom', async () => {
  const actual =
    await vi.importActual<typeof import('react-router-dom')>(
      'react-router-dom'
    );
  return {
    ...actual,
    useNavigate: () => mocks.navigate,
  };
});

vi.mock('../DAGEditorWithDocs', () => ({
  default: ({
    value,
    onChange,
    readOnly,
    headerActions,
    defaultDocsOpen,
  }: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
    headerActions?: React.ReactNode;
    defaultDocsOpen?: boolean;
  }) => (
    <div data-default-docs-open={String(defaultDocsOpen)}>
      <div>{headerActions}</div>
      <textarea
        aria-label="DAG spec"
        readOnly={readOnly}
        value={value}
        onChange={(event) => onChange?.(event.target.value)}
      />
    </div>
  ),
}));

// The lazy Graph module stays pending until a test releases this gate, so
// the Suspense placeholder can be checked. The module loads once per file;
// imports counts the requests for it.
const graphGate = vi.hoisted(() => {
  let release = () => {};
  const loaded = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { loaded, release: () => release(), imports: 0 };
});

vi.mock('../../visualization/Graph', async () => {
  graphGate.imports += 1;
  await graphGate.loaded;
  return {
    default: ({
      steps,
    }: {
      steps?: { step: { name: string }; status: number }[];
    }) => (
      <div data-testid="preview-graph">
        {steps?.map((node) => `${node.step.name}:${node.status}`).join(',')}
      </div>
    ),
  };
});

vi.mock('react-diff-viewer-continued', () => ({
  default: ({
    oldValue,
    newValue,
    leftTitle,
    rightTitle,
  }: {
    oldValue: string;
    newValue: string;
    leftTitle: string;
    rightTitle: string;
  }) => (
    <div data-testid="source-diff">
      <div>{leftTitle}</div>
      <div>{rightTitle}</div>
      <pre>{oldValue}</pre>
      <pre>{newValue}</pre>
    </div>
  ),
  DiffMethod: { LINES: 'lines' },
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const originalSpec = `name: example
steps:
  - name: extract
    run: echo old
`;

const editedSpec = `name: example
steps:
  - name: extract
    run: echo new
`;

const previewResponse = {
  dagName: 'example',
  skippedSteps: ['extract'],
  runnableSteps: ['load'],
  steps: [{ name: 'extract' }, { name: 'load', depends: ['extract'] }],
  ineligibleSteps: [],
  errors: [],
  warnings: ['output variables will be copied'],
};

function renderSpec(
  props: Partial<React.ComponentProps<typeof DAGSpecReadOnly>> = {}
) {
  return render(
    <AppBarContext.Provider value={appBarValue}>
      <DAGSpecReadOnly dagName="example" dagRunId="run-1" {...props} />
    </AppBarContext.Provider>
  );
}

afterEach(() => {
  cleanup();
  mocks.get.mockReset();
  mocks.navigate.mockReset();
  mocks.post.mockReset();
  mocks.put.mockReset();
  mocks.showError.mockReset();
  mocks.showToast.mockReset();
  mocks.useQuery.mockReset();
  mocks.runDags = true;
});

async function openCanvasPreview(
  props: Partial<React.ComponentProps<typeof DAGSpecReadOnly>> = {},
  mount: () => void = () => renderSpec({ previewEngine: 'canvas', ...props })
) {
  mocks.useQuery.mockReturnValue({
    data: { spec: originalSpec },
    isLoading: false,
    error: undefined,
  });
  mocks.post
    .mockResolvedValueOnce({ data: previewResponse })
    .mockResolvedValueOnce({ data: { dagRunId: 'run-2' } });

  mount();

  const editor = screen.getByLabelText('DAG spec');
  await waitFor(() => expect(editor).toHaveValue(originalSpec));
  fireEvent.change(editor, { target: { value: editedSpec } });
  await userEvent.click(
    await screen.findByRole('button', { name: /retry as a new run/i })
  );
  await screen.findByText('Step review');
  await screen.findByTestId('ve-canvas-node-load', {}, { timeout: 4000 });
}

const runState = (step: string) =>
  screen
    .getByTestId(`ve-canvas-node-${step}`)
    .querySelector('[data-testid="ve-canvas-node-status"]');

const reviewCard = (step: string) =>
  screen
    .getByLabelText(`Reuse previous output for ${step}`)
    .closest('[role="button"]');

// These tests run before the Mermaid tests below: the Graph module loads
// once per file, so imports stays 0 only while no test has rendered it.
// parity:run-tab-spec-edit-retry parity:run-snapshot-readonly-spec
// parity:test-dag-spec-readonly parity:api-edit-retry-reschedule
describe('DAGSpecReadOnly with the canvas preview', () => {
  it('draws the retry preview on the canvas and never requests Graph', async () => {
    await openCanvasPreview();

    expect(screen.getByTestId('ve-canvas-node-extract')).toBeInTheDocument();
    expect(runState('extract')).toHaveAttribute(
      'data-status',
      String(NodeStatus.Skipped)
    );
    expect(
      runState('extract')?.querySelector('[data-testid="status-dot"]')
    ).toHaveAttribute('title', 'Reuse previous output');
    expect(runState('load')).toHaveAttribute(
      'data-status',
      String(NodeStatus.NotStarted)
    );
    expect(
      runState('load')?.querySelector('[data-testid="status-dot"]')
    ).toHaveAttribute('title', 'Run again');
    expect(screen.queryByTestId('preview-graph')).not.toBeInTheDocument();
    expect(graphGate.imports).toBe(0);
  });

  it('runs a step again when its reuse box is cleared', async () => {
    await openCanvasPreview();

    await userEvent.click(
      screen.getByLabelText('Reuse previous output for extract')
    );
    expect(runState('extract')).toHaveAttribute(
      'data-status',
      String(NodeStatus.NotStarted)
    );

    await userEvent.click(
      screen.getByRole('button', { name: /create new run/i })
    );
    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/edit-retry',
      expect.objectContaining({
        body: { spec: editedSpec, dagName: 'example', skipSteps: [] },
      })
    );
    expect(graphGate.imports).toBe(0);
  });

  it('selects the reviewed step from the canvas and from the review', async () => {
    await openCanvasPreview();

    const ring = (step: string) =>
      screen.getByTestId(`ve-canvas-node-${step}`).classList;
    expect(reviewCard('extract')).toHaveClass('border-primary');
    expect(ring('extract')).toContain('ring-primary');

    fireEvent.click(screen.getByTestId('ve-canvas-node-load'));
    expect(reviewCard('load')).toHaveClass('border-primary');
    expect(reviewCard('extract')).not.toHaveClass('border-primary');
    expect(ring('load')).toContain('ring-primary');
    expect(ring('extract')).not.toContain('ring-primary');

    // A click on the empty canvas keeps the reviewed step.
    fireEvent.click(screen.getByTestId('flow'));
    expect(reviewCard('load')).toHaveClass('border-primary');

    fireEvent.click(reviewCard('extract')!);
    expect(ring('extract')).toContain('ring-primary');
    expect(ring('load')).not.toContain('ring-primary');
  });

  it('creates the new run from the canvas preview', async () => {
    await openCanvasPreview();

    await userEvent.click(
      screen.getByRole('button', { name: /create new run/i })
    );

    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/edit-retry',
      expect.objectContaining({
        body: { spec: editedSpec, dagName: 'example', skipSteps: ['extract'] },
      })
    );
    expect(mocks.navigate).toHaveBeenCalledWith(
      '/dags/example?remoteNode=local&run=run-2'
    );
  });

  // The DAG name of the run can differ from the name of its file.
  it('opens the new run on the page of the source file', async () => {
    await openCanvasPreview({ sourceFileName: 'example-v2' });

    await userEvent.click(
      screen.getByRole('button', { name: /create new run/i })
    );

    await waitFor(() =>
      expect(mocks.navigate).toHaveBeenCalledWith(
        '/dags/example-v2?remoteNode=local&run=run-2'
      )
    );
  });

  it('hides the retry when DAG runs are disabled', async () => {
    mocks.runDags = false;
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });

    renderSpec({ previewEngine: 'canvas' });

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    fireEvent.change(editor, { target: { value: editedSpec } });
    expect(
      screen.queryByRole('button', { name: /retry as a new run/i })
    ).not.toBeInTheDocument();
  });

  it('offers no retry for a sub-DAG run', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });

    renderSpec({
      previewEngine: 'canvas',
      subDAGRunId: 'child-1',
      sourceFileName: 'example',
    });

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    expect(editor).toHaveAttribute('readonly');
    expect(
      screen.queryByRole('button', { name: /retry as a new run/i })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /save source dag/i })
    ).not.toBeInTheDocument();
  });

  it('is the canvas preview of a run spec', async () => {
    await openCanvasPreview({ sourceFileName: 'example' });

    expect(mocks.useQuery).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/spec',
      expect.objectContaining({
        params: expect.objectContaining({
          path: { name: 'example', dagRunId: 'run-1' },
        }),
      })
    );
    expect(runState('extract')).toHaveAttribute(
      'data-status',
      String(NodeStatus.Skipped)
    );
    // The preview dialog hides the editor header from the accessibility tree.
    expect(
      screen.getByRole('button', { name: /save source dag/i, hidden: true })
    ).toBeInTheDocument();
    expect(graphGate.imports).toBe(0);
  });

  it('saves the edited spec to the source DAG after a diff', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    mocks.get.mockResolvedValueOnce({
      data: { spec: originalSpec, errors: [] },
    });
    mocks.put.mockResolvedValueOnce({ data: { errors: [] } });

    renderSpec({ previewEngine: 'canvas', sourceFileName: 'example' });

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    fireEvent.change(editor, { target: { value: editedSpec } });
    await userEvent.click(
      await screen.findByRole('button', { name: /save source dag/i })
    );
    const dialog = await screen.findByRole('dialog', {
      name: /save source dag/i,
    });
    expect(within(dialog).getByTestId('source-diff')).toHaveTextContent(
      'Edited DAG spec'
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: /^save source dag$/i })
    );

    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(1));
    expect(mocks.put).toHaveBeenCalledWith(
      '/dags/{fileName}/spec',
      expect.objectContaining({ body: { spec: editedSpec } })
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      'Source DAG saved successfully'
    );
    expect(graphGate.imports).toBe(0);
  });
});

// parity:run-tab-spec - the run spec tab renders without a static Graph import.
describe('DAGSpecReadOnly', () => {
  // The Spec tab of a run starts with the schema docs closed.
  it('starts with the schema docs closed', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    const { container } = renderSpec();
    await waitFor(() =>
      expect(screen.getByLabelText('DAG spec')).toHaveValue(originalSpec)
    );
    expect(container.querySelector('[data-default-docs-open]')).toHaveAttribute(
      'data-default-docs-open',
      'false'
    );
  });

  // Save source DAG is a real action only once the spec differs from the run.
  it('offers Save source DAG only while the spec has edits', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    renderSpec({ sourceFileName: 'example' });
    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    expect(
      screen.queryByRole('button', { name: /save source dag/i })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /retry as a new run/i })
    ).toBeDisabled();

    fireEvent.change(editor, { target: { value: editedSpec } });
    expect(
      await screen.findByRole('button', { name: /save source dag/i })
    ).toBeEnabled();
    expect(
      screen.getByRole('button', { name: /retry as a new run/i })
    ).toBeEnabled();

    fireEvent.change(editor, { target: { value: originalSpec } });
    expect(
      screen.queryByRole('button', { name: /save source dag/i })
    ).not.toBeInTheDocument();
  });

  it('hides edited retry controls when DAG runs are disabled', async () => {
    mocks.runDags = false;
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });

    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    fireEvent.change(editor, { target: { value: editedSpec } });

    expect(
      screen.queryByRole('button', { name: /retry as a new run/i })
    ).not.toBeInTheDocument();
  });

  it('previews and confirms before creating an edited retry run', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    mocks.post
      .mockResolvedValueOnce({ data: previewResponse })
      .mockResolvedValueOnce({ data: { dagRunId: 'run-2' } });

    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    const retryButton = await screen.findByRole('button', {
      name: /retry as a new run/i,
    });
    expect(retryButton).toBeDisabled();

    fireEvent.change(editor, { target: { value: editedSpec } });
    await waitFor(() => expect(retryButton).toBeEnabled());

    await userEvent.click(retryButton);

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/edit-retry/preview',
      expect.objectContaining({
        body: {
          spec: editedSpec,
          dagName: 'example',
        },
      })
    );
    expect(await screen.findByText('Step review')).toBeInTheDocument();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveClass('!max-w-[1920px]');
    expect(dialog).toHaveClass('sm:!h-[90vh]');
    expect(dialog).toHaveClass('sm:!max-h-[1080px]');
    expect(dialog).toHaveClass('sm:!w-[94vw]');
    expect(dialog).toHaveClass('left-[50%]');
    expect(dialog).not.toHaveClass('inset-0');
    expect(dialog).not.toHaveClass('w-screen');
    expect(dialog).not.toHaveClass('max-w-none');
    expect(dialog).not.toHaveClass('sm:max-w-[500px]');
    // parity:spec-edit-retry - the graph loads lazily; the placeholder keeps
    // the graph box height and is not a spinner.
    const placeholder = screen.getByTestId('preview-graph-placeholder');
    expect(placeholder).toHaveClass('h-full', 'w-full');
    expect(within(dialog).queryByRole('status')).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('progressbar')).not.toBeInTheDocument();
    graphGate.release();
    const graph = await screen.findByTestId('preview-graph');
    expect(graph).toHaveTextContent('extract:4');
    expect(graph).toHaveTextContent('load:0');
    expect(
      screen.queryByTestId('preview-graph-placeholder')
    ).not.toBeInTheDocument();
    expect(screen.getAllByText('Reuse previous output').length).toBeGreaterThan(
      0
    );
    expect(screen.getAllByText('extract').length).toBeGreaterThan(0);
    expect(screen.getByText('load')).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole('button', { name: /create new run/i })
    );

    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/edit-retry',
      expect.objectContaining({
        body: {
          spec: editedSpec,
          dagName: 'example',
          skipSteps: ['extract'],
        },
      })
    );
    expect(mocks.navigate).toHaveBeenCalledWith(
      '/dags/example?remoteNode=local&run=run-2'
    );
  });

  it('allows eligible reusable steps to run again instead', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    mocks.post
      .mockResolvedValueOnce({ data: previewResponse })
      .mockResolvedValueOnce({ data: { dagRunId: 'run-2' } });

    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    fireEvent.change(editor, { target: { value: editedSpec } });

    await userEvent.click(
      await screen.findByRole('button', { name: /retry as a new run/i })
    );
    await userEvent.click(
      await screen.findByLabelText('Reuse previous output for extract')
    );
    await userEvent.click(
      screen.getByRole('button', { name: /create new run/i })
    );

    await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(2));
    expect(mocks.post).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/edit-retry',
      expect.objectContaining({
        body: {
          spec: editedSpec,
          dagName: 'example',
          skipSteps: [],
        },
      })
    );
  });

  it('shows a diff before saving the edited spec to the source DAG', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    mocks.get.mockResolvedValueOnce({
      data: { spec: originalSpec, errors: [] },
    });
    mocks.put.mockResolvedValueOnce({ data: { errors: [] } });

    renderSpec({ sourceFileName: 'example' });

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    expect(
      screen.queryByRole('button', { name: /save source dag/i })
    ).not.toBeInTheDocument();

    fireEvent.change(editor, { target: { value: editedSpec } });
    const saveButton = await screen.findByRole('button', {
      name: /save source dag/i,
    });
    await waitFor(() => expect(saveButton).toBeEnabled());

    await userEvent.click(saveButton);

    expect(mocks.get).toHaveBeenCalledWith(
      '/dags/{fileName}/spec',
      expect.objectContaining({
        params: {
          path: { fileName: 'example' },
          query: { remoteNode: 'local' },
        },
      })
    );
    expect(await screen.findByTestId('source-diff')).toHaveTextContent(
      'Current source DAG'
    );
    expect(screen.getByTestId('source-diff')).toHaveTextContent(
      'Edited DAG spec'
    );

    const dialog = await screen.findByRole('dialog', {
      name: /save source dag/i,
    });
    await userEvent.click(
      within(dialog).getByRole('button', { name: /^save source dag$/i })
    );

    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(1));
    expect(mocks.put).toHaveBeenCalledWith(
      '/dags/{fileName}/spec',
      expect.objectContaining({
        body: {
          spec: editedSpec,
        },
        params: {
          path: { fileName: 'example' },
          query: { remoteNode: 'local' },
        },
      })
    );
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.showToast).toHaveBeenCalledWith(
      'Source DAG saved successfully'
    );
  });

  it('does not submit when the preview contains errors', async () => {
    mocks.useQuery.mockReturnValue({
      data: { spec: originalSpec },
      isLoading: false,
      error: undefined,
    });
    mocks.post.mockResolvedValueOnce({
      data: {
        ...previewResponse,
        errors: ['invalid edited DAG'],
      },
    });

    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    await waitFor(() => expect(editor).toHaveValue(originalSpec));
    fireEvent.change(editor, { target: { value: editedSpec } });

    await userEvent.click(
      await screen.findByRole('button', { name: /retry as a new run/i })
    );

    const submitButton = await screen.findByRole('button', {
      name: /create new run/i,
    });
    expect(submitButton).toBeDisabled();

    await userEvent.click(submitButton);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});
