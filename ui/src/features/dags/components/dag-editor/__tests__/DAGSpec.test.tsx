// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGContext } from '../../../contexts/DAGContext';
import DAGSpec from '../DAGSpec';

const mocks = vi.hoisted(() => {
  const get = vi.fn();
  const post = vi.fn();
  const put = vi.fn();
  return {
    get,
    post,
    put,
    // The client must be render-stable like the real useClient singleton.
    client: { GET: get, POST: post, PUT: put },
    showError: vi.fn(),
    showToast: vi.fn(),
    useQuery: vi.fn(),
    editorProps: { current: {} as { markers?: unknown[] } },
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
  useErrorModal: () => ({ showError: mocks.showError }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: mocks.showToast }),
}));

vi.mock('react-cookie', () => ({
  useCookies: () => [{}, vi.fn()],
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

vi.mock('../../../../../hooks/useDAGSSE', () => ({
  useDAGSSE: () => null,
}));

vi.mock('../../../../../hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: vi.fn(),
}));

vi.mock('@/features/dags/components/step-details', () => ({
  StepDetailsDrawer: () => null,
}));

vi.mock('../DAGAttributes', () => ({ default: () => null }));
vi.mock('../ControllerSpecOverview', () => ({
  ControllerSpecOverview: () => <div>Controller overview</div>,
}));
vi.mock('../ExternalChangeDialog', () => ({ default: () => null }));
vi.mock('../../dag-details', () => ({ DAGStepTable: () => null }));
vi.mock('../../value-reference-notices', () => ({
  ValueReferenceNoticesButton: () => null,
}));

vi.mock('../../visualization', () => ({
  FlowchartType: {},
  Graph: ({ steps }: { steps?: { name: string }[] }) => (
    <div data-testid="preview-graph">
      {steps?.map((step) => step.name).join(',')}
    </div>
  ),
}));

vi.mock('../DAGEditorWithDocs', () => ({
  default: (props: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
    headerActions?: React.ReactNode;
    markers?: unknown[];
  }) => {
    mocks.editorProps.current = props;
    return (
      <div>
        <div>{props.headerActions}</div>
        <textarea
          aria-label="DAG spec"
          readOnly={props.readOnly}
          value={props.value}
          onChange={(event) => props.onChange?.(event.target.value)}
        />
      </div>
    );
  },
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const savedSpec = 'steps:\n  - name: extract\n    run: echo hello\n';

function specData(overrides: Record<string, unknown> = {}) {
  return {
    data: {
      dag: { name: 'example', steps: [{ name: 'extract' }] },
      spec: savedSpec,
      errors: [],
      valueReferenceNotices: [],
      ...overrides,
    },
    isLoading: false,
    mutate: vi.fn(),
  };
}

function renderSpec() {
  return render(
    <AppBarContext.Provider value={appBarValue}>
      <DAGContext.Provider
        value={{
          refresh: vi.fn(),
          name: 'example',
          fileName: 'example.yaml',
        }}
      >
        <DAGSpec fileName="example.yaml" />
      </DAGContext.Provider>
    </AppBarContext.Provider>
  );
}

beforeEach(() => {
  mocks.useQuery.mockReturnValue(specData());
  mocks.post.mockResolvedValue({
    data: { valid: true, errors: [], dag: undefined },
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe('DAGSpec live validation', () => {
  it('validates the edited buffer once per idle window', async () => {
    vi.useFakeTimers();
    renderSpec();

    const editor = screen.getByLabelText('DAG spec');
    fireEvent.change(editor, { target: { value: 'steps: [' } });
    fireEvent.change(editor, { target: { value: 'steps: [broken' } });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(mocks.post).toHaveBeenCalledWith('/dags/validate', {
      params: { query: { remoteNode: 'local' } },
      body: { spec: 'steps: [broken', name: 'example.yaml' },
    });
  });

  it('renders markers, errors, and the live graph from the validate response', async () => {
    vi.useFakeTimers();
    mocks.post.mockResolvedValue({
      data: {
        valid: false,
        errors: [
          '[3:1] mapping values are not allowed in this context',
          "field 'steps': has invalid keys: nosuchfield",
        ],
        dag: { name: 'example', steps: [{ name: 'extract' }, { name: 'load' }] },
      },
    });

    renderSpec();
    fireEvent.change(screen.getByLabelText('DAG spec'), {
      target: { value: 'steps: [broken' },
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });

    expect(
      screen.getByText(/mapping values are not allowed/)
    ).toBeInTheDocument();
    expect(screen.getByText(/has invalid keys: nosuchfield/)).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent(
      'extract,load'
    );
    expect(screen.getByText('2 issues')).toBeInTheDocument();

    const markers = mocks.editorProps.current.markers as Array<{
      startLineNumber: number;
    }>;
    expect(markers).toHaveLength(1);
    expect(markers[0]?.startLineNumber).toBe(3);
  });

  it('renders the saved graph alongside saved errors', () => {
    mocks.useQuery.mockReturnValue(
      specData({ errors: ['something is misconfigured'] })
    );

    renderSpec();

    expect(screen.getByText('something is misconfigured')).toBeInTheDocument();
    expect(screen.getByTestId('preview-graph')).toHaveTextContent('extract');
  });
});
