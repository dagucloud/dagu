// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGSpecReadOnly component displays a DAG-run specification snapshot.
 * Root DAG-run snapshots can be edited locally and retried as a new run.
 *
 * @module features/dags/components/dag-editor
 */
import React from 'react';
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useCanWrite } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useUserPreferences } from '@/contexts/UserPreference';
import { cn } from '@/lib/utils';
import { RefreshCw, Save, X } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
} from '../../../../api/v1/schema';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient, useQuery } from '../../../../hooks/api';
import ConfirmModal from '@/components/ui/confirm-dialog';
import Graph, { type FlowchartType } from '../visualization/Graph';
import DAGEditorWithDocs from './DAGEditorWithDocs';

/**
 * Props for the DAGSpecReadOnly component
 */
type DAGSpecReadOnlyProps = {
  /** DAG name to fetch the spec for */
  dagName: string;
  /** DAG run ID */
  dagRunId: string;
  /** Optional sub-DAG run ID for viewing subdag specs */
  subDAGRunId?: string;
  /** Source DAG file name when the DAG-run was created from a DAGs-dir file */
  sourceFileName?: string;
  /** Additional class name for the container */
  className?: string;
};

type EditRetryPreview = {
  dagName: string;
  skippedSteps: string[];
  runnableSteps: string[];
  steps: components['schemas']['Step'][];
  ineligibleSteps: { stepName: string; reason: string }[];
  errors: string[];
  warnings: string[];
};

const normalizeEditRetryPreview = (
  preview: EditRetryPreview | null | undefined
): EditRetryPreview | null => {
  if (!preview) {
    return null;
  }
  return {
    dagName: preview.dagName ?? '',
    skippedSteps: preview.skippedSteps ?? [],
    runnableSteps: preview.runnableSteps ?? [],
    steps: preview.steps ?? [],
    ineligibleSteps: preview.ineligibleSteps ?? [],
    errors: preview.errors ?? [],
    warnings: preview.warnings ?? [],
  };
};

const orderedSelectedSkipSteps = (
  steps: components['schemas']['Step'][],
  selectedSkipSteps: string[]
) => {
  const selected = new Set(selectedSkipSteps);
  return steps.map((step) => step.name).filter((name) => selected.has(name));
};

const buildPreviewNodes = (
  steps: components['schemas']['Step'][],
  selectedSkipSteps: string[]
): components['schemas']['Node'][] => {
  const selected = new Set(selectedSkipSteps);
  return steps.map((step) => {
    const willReuseOutput = selected.has(step.name);
    return {
      step,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 0,
      status: willReuseOutput ? NodeStatus.Success : NodeStatus.NotStarted,
      statusLabel: willReuseOutput
        ? NodeStatusLabel.succeeded
        : NodeStatusLabel.not_started,
    };
  });
};

/**
 * Skeleton placeholder for the editor while loading
 */
function EditorSkeleton({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        'flex flex-col bg-surface border border-border rounded-lg overflow-hidden min-h-[300px] h-[70vh]',
        className
      )}
    >
      <div className="flex-shrink-0 flex justify-between items-center p-2 border-b border-border">
        <div className="h-6 w-16 bg-muted animate-pulse rounded" />
      </div>
      <div className="flex-1 p-4 space-y-2">
        <div className="h-4 w-3/4 bg-muted animate-pulse rounded" />
        <div className="h-4 w-1/2 bg-muted animate-pulse rounded" />
        <div className="h-4 w-2/3 bg-muted animate-pulse rounded" />
        <div className="h-4 w-1/3 bg-muted animate-pulse rounded" />
        <div className="h-4 w-3/4 bg-muted animate-pulse rounded" />
        <div className="h-4 w-1/2 bg-muted animate-pulse rounded" />
      </div>
    </div>
  );
}

/**
 * DAGSpecReadOnly fetches and displays a DAG specification in readonly mode
 * with the Schema Documentation sidebar available for reference.
 */
function DAGSpecReadOnly({
  dagName,
  dagRunId,
  subDAGRunId,
  sourceFileName,
  className,
}: DAGSpecReadOnlyProps) {
  const remoteNode = useRemoteNode();
  const client = useClient();
  const navigate = useNavigate();
  const canWrite = useCanWrite();
  const config = useConfig();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const [sourceSpec, setSourceSpec] = React.useState('');
  const [editedSpec, setEditedSpec] = React.useState('');
  const [previewLoading, setPreviewLoading] = React.useState(false);
  const [retrySubmitting, setRetrySubmitting] = React.useState(false);
  const [previewVisible, setPreviewVisible] = React.useState(false);
  const [retryPreview, setRetryPreview] =
    React.useState<EditRetryPreview | null>(null);
  const [sourceDAGSpec, setSourceDAGSpec] = React.useState<string | null>(null);
  const [sourceDiffVisible, setSourceDiffVisible] = React.useState(false);
  const [sourceDiffLoading, setSourceDiffLoading] = React.useState(false);
  const [sourceSaving, setSourceSaving] = React.useState(false);
  const [selectedSkipSteps, setSelectedSkipSteps] = React.useState<string[]>(
    []
  );
  const [selectedPreviewStep, setSelectedPreviewStep] = React.useState('');
  const [previewFlowchart, setPreviewFlowchart] =
    React.useState<FlowchartType>('TD');

  // Select endpoint based on whether this is a subdag
  const endpoint = subDAGRunId
    ? ('/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/spec' as const)
    : ('/dag-runs/{name}/{dagRunId}/spec' as const);

  // Build path params conditionally
  const pathParams = subDAGRunId
    ? { name: dagName, dagRunId: dagRunId, subDAGRunId: subDAGRunId }
    : { name: dagName, dagRunId: dagRunId };

  // Fetch DAG specification data using the appropriate endpoint
  const { data, isLoading, error } = useQuery(endpoint, {
    params: {
      query: {
        remoteNode,
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      path: pathParams as any,
    },
  });

  React.useEffect(() => {
    if (!data?.spec) {
      setSourceSpec('');
      setEditedSpec('');
      setRetryPreview(null);
      setSelectedSkipSteps([]);
      setSelectedPreviewStep('');
      setPreviewVisible(false);
      return;
    }
    setSourceSpec(data.spec);
    setEditedSpec(data.spec);
    setRetryPreview(null);
    setSourceDAGSpec(null);
    setSourceDiffVisible(false);
    setSelectedSkipSteps([]);
    setSelectedPreviewStep('');
    setPreviewVisible(false);
  }, [data?.spec]);

  const isEditableRetry = !subDAGRunId;
  const hasLoadedSpec = sourceSpec !== '';
  const editorSpec = !hasLoadedSpec && data?.spec ? data.spec : editedSpec;
  const hasEdits =
    isEditableRetry && hasLoadedSpec && editorSpec !== sourceSpec;
  const canRetryEditedSpec = config.permissions.runDags && isEditableRetry;
  const canOpenSourceDAGDiff = hasEdits && !!sourceFileName;

  const previewEditedSpec = React.useCallback(async () => {
    if (
      !canRetryEditedSpec ||
      !hasEdits ||
      !editorSpec.trim() ||
      previewLoading ||
      retrySubmitting ||
      sourceSaving
    ) {
      return;
    }
    setPreviewLoading(true);
    setRetryPreview(null);
    try {
      const { data: previewData, error: previewError } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/edit-retry/preview',
        {
          params: {
            path: {
              name: dagName,
              dagRunId,
            },
            query: {
              remoteNode,
            },
          },
          body: {
            spec: editorSpec,
            dagName,
          },
        }
      );
      if (previewError) {
        showError(
          previewError.message || 'Failed to preview edited retry',
          'Check the edited YAML and try again.'
        );
        return;
      }
      const preview = normalizeEditRetryPreview(previewData);
      if (!preview) {
        showError(
          'Failed to preview edited retry',
          'The server did not return a retry preview.'
        );
        return;
      }
      setRetryPreview(preview);
      setSelectedSkipSteps(preview.skippedSteps);
      setSelectedPreviewStep(preview.steps[0]?.name ?? '');
      setPreviewVisible(true);
    } catch (err) {
      showError(
        err instanceof Error && err.message
          ? err.message
          : 'Failed to preview edited retry',
        'Check your connection and try again.'
      );
    } finally {
      setPreviewLoading(false);
    }
  }, [
    client,
    canRetryEditedSpec,
    dagName,
    dagRunId,
    editorSpec,
    hasEdits,
    previewLoading,
    remoteNode,
    retrySubmitting,
    showError,
    sourceSaving,
  ]);

  const submitEditedRetry = React.useCallback(async () => {
    if (
      !canRetryEditedSpec ||
      !retryPreview ||
      retryPreview.errors.length > 0 ||
      !editorSpec.trim() ||
      retrySubmitting
    ) {
      return;
    }

    setRetrySubmitting(true);
    try {
      const { data: retryData, error: retryError } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/edit-retry',
        {
          params: {
            path: {
              name: dagName,
              dagRunId,
            },
            query: {
              remoteNode,
            },
          },
          body: {
            spec: editorSpec,
            dagName,
            skipSteps: orderedSelectedSkipSteps(
              retryPreview.steps,
              selectedSkipSteps
            ),
          },
        }
      );
      if (retryError) {
        showError(
          retryError.message || 'Failed to retry edited DAG',
          'Check the edited YAML and preview, then try again.'
        );
        return;
      }
      if (!retryData?.dagRunId) {
        showError(
          'Failed to retry edited DAG',
          'The server did not return a new DAG-run ID.'
        );
        return;
      }
      setPreviewVisible(false);
      showToast(`New DAG run created: ${retryData.dagRunId}`);
      const searchParams = new URLSearchParams();
      searchParams.set('remoteNode', remoteNode);
      navigate(
        `/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(
          retryData.dagRunId
        )}?${searchParams.toString()}`
      );
    } catch (err) {
      showError(
        err instanceof Error && err.message
          ? err.message
          : 'Failed to retry edited DAG',
        'Check your connection and try again.'
      );
    } finally {
      setRetrySubmitting(false);
    }
  }, [
    client,
    canRetryEditedSpec,
    dagName,
    dagRunId,
    editorSpec,
    navigate,
    remoteNode,
    retryPreview,
    retrySubmitting,
    selectedSkipSteps,
    showError,
    showToast,
  ]);

  const openSourceDAGDiff = React.useCallback(async () => {
    if (
      !canWrite ||
      !sourceFileName ||
      !hasEdits ||
      !editorSpec.trim() ||
      sourceDiffLoading ||
      sourceSaving
    ) {
      return;
    }

    setSourceDiffLoading(true);
    try {
      const { data: sourceData, error: sourceError } = await client.GET(
        '/dags/{fileName}/spec',
        {
          params: {
            path: {
              fileName: sourceFileName,
            },
            query: {
              remoteNode,
            },
          },
        }
      );
      if (sourceError) {
        showError(
          sourceError.message || 'Failed to load source DAG',
          'The source DAG file may no longer be editable from the DAGs directory.'
        );
        return;
      }

      const currentSourceSpec = sourceData?.spec ?? '';
      setSourceDAGSpec(currentSourceSpec);
      if (currentSourceSpec === editorSpec) {
        showToast('Source DAG already matches the edited spec');
        return;
      }
      setSourceDiffVisible(true);
    } catch (err) {
      showError(
        err instanceof Error && err.message
          ? err.message
          : 'Failed to load source DAG',
        'Check your connection and try again.'
      );
    } finally {
      setSourceDiffLoading(false);
    }
  }, [
    canWrite,
    client,
    editorSpec,
    hasEdits,
    remoteNode,
    showError,
    showToast,
    sourceDiffLoading,
    sourceFileName,
    sourceSaving,
  ]);

  const saveSourceDAG = React.useCallback(async () => {
    if (
      !canWrite ||
      !sourceFileName ||
      sourceDAGSpec == null ||
      !editorSpec.trim() ||
      sourceSaving
    ) {
      return;
    }

    setSourceSaving(true);
    try {
      const { data: responseData, error: saveError } = await client.PUT(
        '/dags/{fileName}/spec',
        {
          params: {
            path: {
              fileName: sourceFileName,
            },
            query: {
              remoteNode,
            },
          },
          body: {
            spec: editorSpec,
          },
        }
      );

      if (saveError) {
        showError(
          saveError.message || 'Failed to save source DAG',
          'Please check the YAML syntax and try again.'
        );
        return;
      }

      if (responseData?.errors?.length) {
        showError('Validation errors', responseData.errors.join('\n'));
        return;
      }

      setSourceDAGSpec(editorSpec);
      setSourceDiffVisible(false);
      showToast('Source DAG saved successfully');
    } catch (err) {
      showError(
        err instanceof Error && err.message
          ? err.message
          : 'Failed to save source DAG',
        'Check your connection and try again.'
      );
    } finally {
      setSourceSaving(false);
    }
  }, [
    canWrite,
    client,
    editorSpec,
    remoteNode,
    showError,
    showToast,
    sourceDAGSpec,
    sourceFileName,
    sourceSaving,
  ]);

  const handleSpecChange = React.useCallback((value?: string) => {
    setEditedSpec(value ?? '');
    setRetryPreview(null);
    setSelectedSkipSteps([]);
    setSelectedPreviewStep('');
  }, []);

  const previewNodes = React.useMemo(
    () =>
      retryPreview
        ? buildPreviewNodes(retryPreview.steps, selectedSkipSteps)
        : [],
    [retryPreview, selectedSkipSteps]
  );

  const selectedPreviewNode = React.useMemo(
    () =>
      previewNodes.find((node) => node.step.name === selectedPreviewStep) ??
      previewNodes[0],
    [previewNodes, selectedPreviewStep]
  );

  const retryStepCount = React.useMemo(() => {
    if (!retryPreview) {
      return 0;
    }
    const selected = new Set(selectedSkipSteps);
    return retryPreview.steps.filter((step) => !selected.has(step.name)).length;
  }, [retryPreview, selectedSkipSteps]);

  const eligibleReuseSteps = React.useMemo(
    () => new Set(retryPreview?.skippedSteps ?? []),
    [retryPreview]
  );

  const ineligibleReasonByStep = React.useMemo(() => {
    const reasons = new Map<string, string>();
    retryPreview?.ineligibleSteps.forEach((step) => {
      reasons.set(step.stepName, step.reason);
    });
    return reasons;
  }, [retryPreview]);

  const toggleReuseStep = React.useCallback(
    (stepName: string) => {
      if (!eligibleReuseSteps.has(stepName)) {
        return;
      }
      setSelectedSkipSteps((current) =>
        current.includes(stepName)
          ? current.filter((name) => name !== stepName)
          : [...current, stepName]
      );
    },
    [eligibleReuseSteps]
  );

  if (isLoading) {
    return <EditorSkeleton className={className} />;
  }

  if (error) {
    return (
      <div className="text-sm text-danger p-4">
        Failed to load DAG spec: {error.message ?? 'Unknown error'}
      </div>
    );
  }

  if (!data?.spec) {
    return (
      <div className="text-sm text-muted-foreground p-4">
        No DAG spec available for this DAG.
      </div>
    );
  }

  return (
    <>
      <DAGEditorWithDocs
        value={editorSpec}
        onChange={handleSpecChange}
        readOnly={!isEditableRetry}
        className={className}
        headerActions={
          isEditableRetry && hasLoadedSpec ? (
            <div className="flex flex-wrap items-center justify-end gap-2">
              {canWrite && sourceFileName && (
                <Button
                  type="button"
                  size="xs"
                  variant="secondary"
                  disabled={
                    !canOpenSourceDAGDiff ||
                    sourceDiffLoading ||
                    sourceSaving ||
                    !editorSpec.trim()
                  }
                  onClick={() => void openSourceDAGDiff()}
                >
                  {sourceDiffLoading ? (
                    <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Save className="h-3.5 w-3.5" />
                  )}
                  {sourceDiffLoading ? 'Loading diff...' : 'Save source DAG'}
                </Button>
              )}
              {canRetryEditedSpec && (
                <Button
                  type="button"
                  size="xs"
                  variant="primary"
                  disabled={
                    !hasEdits ||
                    previewLoading ||
                    retrySubmitting ||
                    sourceSaving ||
                    !editorSpec.trim()
                  }
                  onClick={() => void previewEditedSpec()}
                >
                  <RefreshCw className="h-3.5 w-3.5" />
                  {previewLoading ? 'Previewing...' : 'Retry as a new run'}
                </Button>
              )}
            </div>
          ) : undefined
        }
        modelUri={`inmemory://dagu/dag-runs/${encodeURIComponent(
          dagName
        )}/${encodeURIComponent(dagRunId)}/${encodeURIComponent(
          subDAGRunId ?? 'root'
        )}.yaml`}
      />

      <SourceDAGDiffDialog
        open={sourceDiffVisible}
        onOpenChange={(open) => {
          if (!sourceSaving) {
            setSourceDiffVisible(open);
          }
        }}
        sourceFileName={sourceFileName ?? ''}
        sourceSpec={sourceDAGSpec ?? ''}
        editedSpec={editorSpec}
        saving={sourceSaving}
        onSave={() => void saveSourceDAG()}
      />

      <ConfirmModal
        title="Retry Edited DAG Run"
        buttonText={retrySubmitting ? 'Creating...' : 'Create new run'}
        visible={previewVisible}
        dismissModal={() => {
          if (!retrySubmitting) {
            setPreviewVisible(false);
          }
        }}
        onSubmit={() => void submitEditedRetry()}
        submitDisabled={
          retrySubmitting || !retryPreview || retryPreview.errors.length > 0
        }
        contentClassName="grid !h-[calc(100dvh-1rem)] !max-h-[calc(100dvh-1rem)] !w-[calc(100vw-1rem)] !max-w-[1920px] grid-rows-[auto_minmax(0,1fr)_auto] !gap-0 overflow-hidden !p-0 sm:!h-[90vh] sm:!max-h-[1080px] sm:!w-[94vw]"
        headerClassName="border-b px-4 py-3 pr-12 text-left sm:px-6 sm:pr-12"
        bodyClassName="min-h-0 overflow-hidden p-0"
        footerClassName="gap-2 border-t px-4 py-3 sm:gap-0 sm:px-6 [&>button]:w-full sm:[&>button]:w-auto"
      >
        {retryPreview && (
          <div className="grid h-full min-h-0 grid-cols-1 grid-rows-[minmax(280px,45fr)_minmax(240px,55fr)] gap-3 p-3 sm:p-4 xl:grid-cols-[minmax(0,1fr)_440px] xl:grid-rows-1">
            <div className="flex min-h-0 flex-col rounded-md border bg-surface">
              <div className="grid grid-cols-2 gap-3 border-b px-3 py-2 text-xs sm:grid-cols-4">
                <div className="min-w-0">
                  <div className="text-xs uppercase text-muted-foreground">
                    Target DAG
                  </div>
                  <div className="truncate font-mono text-xs">
                    {retryPreview.dagName}
                  </div>
                </div>
                <div className="min-w-0">
                  <div className="text-xs uppercase text-muted-foreground">
                    Reuse previous output
                  </div>
                  <div>{selectedSkipSteps.length}</div>
                </div>
                <div className="min-w-0">
                  <div className="text-xs uppercase text-muted-foreground">
                    Run again
                  </div>
                  <div>{retryStepCount}</div>
                </div>
                <div className="min-w-0">
                  <div className="text-xs uppercase text-muted-foreground">
                    Source DAG-run ID
                  </div>
                  <div className="truncate font-mono text-xs">{dagRunId}</div>
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-hidden p-2">
                <Graph
                  steps={previewNodes}
                  type="status"
                  flowchart={previewFlowchart}
                  onChangeFlowchart={setPreviewFlowchart}
                  onClickNode={setSelectedPreviewStep}
                  onRightClickNode={setSelectedPreviewStep}
                  isExpandedView={true}
                  height="100%"
                />
              </div>
            </div>

            <div className="flex min-h-0 flex-col rounded-md border bg-surface">
              <div className="border-b px-3 py-2">
                <div className="text-sm font-medium">Step review</div>
                {selectedPreviewNode && (
                  <div className="mt-1 font-mono text-xs text-muted-foreground">
                    {selectedPreviewNode.step.name}
                  </div>
                )}
              </div>

              <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
                {retryPreview.errors.length > 0 && (
                  <div className="space-y-1 rounded-md border border-destructive/40 bg-destructive/5 p-2 text-sm text-destructive">
                    {retryPreview.errors.map((error) => (
                      <div key={error}>{error}</div>
                    ))}
                  </div>
                )}

                {retryPreview.warnings.length > 0 && (
                  <div className="space-y-1 rounded-md border p-2 text-sm text-muted-foreground">
                    {retryPreview.warnings.map((warning) => (
                      <div key={warning}>{warning}</div>
                    ))}
                  </div>
                )}

                <div className="space-y-2">
                  {retryPreview.steps.map((step) => {
                    const canReuse = eligibleReuseSteps.has(step.name);
                    const willReuse = selectedSkipSteps.includes(step.name);
                    const ineligibleReason = ineligibleReasonByStep.get(
                      step.name
                    );
                    return (
                      <div
                        key={step.name}
                        role="button"
                        tabIndex={0}
                        className={cn(
                          'w-full rounded-md border p-3 text-left transition-colors hover:bg-muted/40',
                          selectedPreviewStep === step.name &&
                            'border-primary bg-primary/5'
                        )}
                        onClick={() => setSelectedPreviewStep(step.name)}
                        onKeyDown={(event) => {
                          if (event.key !== 'Enter' && event.key !== ' ') {
                            return;
                          }
                          event.preventDefault();
                          setSelectedPreviewStep(step.name);
                        }}
                      >
                        <div className="flex items-start gap-3">
                          <Checkbox
                            checked={willReuse}
                            disabled={!canReuse}
                            onCheckedChange={() => toggleReuseStep(step.name)}
                            onClick={(event) => event.stopPropagation()}
                            className="mt-0.5 border-border"
                            aria-label={`Reuse previous output for ${step.name}`}
                          />
                          <div className="min-w-0 flex-1 space-y-1">
                            <div className="break-all font-mono text-xs">
                              {step.name}
                            </div>
                            <div
                              className={cn(
                                'text-xs',
                                willReuse
                                  ? 'text-success'
                                  : 'text-muted-foreground'
                              )}
                            >
                              {willReuse
                                ? 'Reuse previous output'
                                : 'Run again'}
                            </div>
                            {step.depends && step.depends.length > 0 && (
                              <div className="text-xs text-muted-foreground">
                                Depends on {step.depends.join(', ')}
                              </div>
                            )}
                            {!canReuse && ineligibleReason && (
                              <div className="text-xs text-muted-foreground">
                                {ineligibleReason}
                              </div>
                            )}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          </div>
        )}
      </ConfirmModal>
    </>
  );
}

function SourceDAGDiffDialog({
  open,
  onOpenChange,
  sourceFileName,
  sourceSpec,
  editedSpec,
  saving,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sourceFileName: string;
  sourceSpec: string;
  editedSpec: string;
  saving: boolean;
  onSave: () => void;
}) {
  const preferencesContext = useUserPreferences();
  const isDarkMode = preferencesContext?.preferences.theme === 'dark';

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        hideCloseButton
        className="grid max-h-[92vh] w-[calc(100vw-1rem)] max-w-6xl grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden p-0 duration-100 sm:w-[calc(100vw-2rem)]"
      >
        <DialogHeader className="flex flex-row items-center justify-between space-y-0 border-b border-border/40 px-4 py-3">
          <DialogTitle className="min-w-0 truncate text-sm font-mono">
            Save Source DAG: {sourceFileName}
          </DialogTitle>
          <DialogDescription className="sr-only">
            Review the current source DAG and edited DAG specification before
            saving changes.
          </DialogDescription>
          <DialogClose className="rounded-md p-1.5 opacity-70 transition-opacity hover:bg-muted hover:opacity-100">
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
          </DialogClose>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-auto">
          <ReactDiffViewer
            oldValue={sourceSpec}
            newValue={editedSpec}
            splitView={true}
            leftTitle="Current source DAG"
            rightTitle="Edited DAG spec"
            useDarkTheme={isDarkMode}
            compareMethod={DiffMethod.LINES}
            showDiffOnly={false}
            styles={{
              variables: {
                dark: {
                  diffViewerBackground: '#1e1e1e',
                  gutterBackground: '#252526',
                  addedBackground: '#1e3a29',
                  addedGutterBackground: '#1e3a29',
                  removedBackground: '#3a1e1e',
                  removedGutterBackground: '#3a1e1e',
                  wordAddedBackground: '#2ea043',
                  wordRemovedBackground: '#f85149',
                  emptyLineBackground: '#1e1e1e',
                  gutterColor: '#6e7681',
                },
                light: {
                  diffViewerBackground: '#ffffff',
                  gutterBackground: '#f6f8fa',
                  addedBackground: '#e6ffec',
                  addedGutterBackground: '#ccffd8',
                  removedBackground: '#ffebe9',
                  removedGutterBackground: '#ffd7d5',
                  wordAddedBackground: '#abf2bc',
                  wordRemovedBackground: '#ff818266',
                  emptyLineBackground: '#ffffff',
                  gutterColor: '#57606a',
                },
              },
              contentText: {
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
                fontSize: '12px',
                lineHeight: '1.5',
              },
              titleBlock: {
                padding: '8px 12px',
                fontSize: '12px',
                fontWeight: 500,
              },
              line: {
                padding: '0 8px',
              },
            }}
          />
        </div>

        <DialogFooter className="gap-2 border-t border-border/40 px-4 py-3 sm:gap-0">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={saving}
            onClick={() => onOpenChange(false)}
          >
            <X className="h-3.5 w-3.5" />
            Cancel
          </Button>
          <Button type="button" size="sm" disabled={saving} onClick={onSave}>
            {saving ? (
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Save className="h-3.5 w-3.5" />
            )}
            {saving ? 'Saving...' : 'Save source DAG'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default DAGSpecReadOnly;
