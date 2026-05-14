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
  }: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
    headerActions?: React.ReactNode;
  }) => (
    <div>
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

vi.mock('../../visualization/Graph', () => ({
  default: ({
    steps,
  }: {
    steps?: { step: { name: string }; status: number }[];
  }) => (
    <div data-testid="preview-graph">
      {steps?.map((node) => `${node.step.name}:${node.status}`).join(',')}
    </div>
  ),
}));

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
});

describe('DAGSpecReadOnly', () => {
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
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract:4');
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('load:0');
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
    expect(mocks.navigate).toHaveBeenCalledWith('/dag-runs/example/run-2');
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
    const saveButton = await screen.findByRole('button', {
      name: /save source dag/i,
    });
    expect(saveButton).toBeDisabled();

    fireEvent.change(editor, { target: { value: editedSpec } });
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
