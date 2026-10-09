// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGSpec component displays and allows editing of a DAG specification.
 *
 * @module features/dags/components/dag-editor
 */
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import {
  type SpecViewMode,
  useUserPreferences,
} from '@/contexts/UserPreference';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { StepDetailsDrawer } from '@/features/dags/components/step-details';
import { cn, toMermaidNodeId } from '@/lib/utils';
import { workspaceNameFromLabels } from '@/lib/workspace';
import BorderedBox from '@/components/ui/bordered-box';
import { AlertTriangle, Check, Copy, Save, Undo2 } from 'lucide-react';
import React, { useEffect } from 'react';
import { useCookies } from 'react-cookie';
import { components } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { Tab, Tabs } from '@/components/ui/tabs';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useSchema } from '../../../../contexts/SchemaContext';
import { useUnsavedChanges } from '../../../../contexts/UnsavedChangesContext';
import { useClient, useQuery } from '../../../../hooks/api';
import { useContentEditor } from '../../../../hooks/useContentEditor';
import { useDAGSSE } from '../../../../hooks/useDAGSSE';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../../../hooks/useSSECacheSync';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGContext } from '../../contexts/DAGContext';
import { DAGStepTable } from '../dag-details';
import { ValueReferenceNoticesButton } from '../value-reference-notices';
import { FlowchartType, Graph } from '../visualization';
import { GraphInteractionsHint } from '../visualization/GraphInteractionsHint';
import {
  buildAugmentedDAGSchema,
  customActionHintsEqual,
  type EditorCustomActionHint,
  type EditorLegacyDefinitionHint,
  extractLocalCustomDefinitionHints,
  legacyDefinitionHintsEqual,
  mergeCustomActionHints,
  mergeLegacyDefinitionHints,
  toInheritedCustomActionHints,
  toInheritedLegacyDefinitionHints,
} from './customActionSchema';
import DAGAttributes from './DAGAttributes';
import DAGEditorWithDocs from './DAGEditorWithDocs';
import { parseValidationMarkers } from './validationMarkers';
import { AgentSpecOverview } from './AgentSpecOverview';
import ExternalChangeDialog from './ExternalChangeDialog';
import SpecViewSwitcher from './SpecViewSwitcher';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

/** Narrowest spec tab, in pixels, that shows the graph beside the editor. */
const SPLIT_VIEW_MIN_WIDTH = 1024;
/** View shown instead of Split where it does not fit. */
const SPLIT_FALLBACK_VIEW: SpecViewMode = 'graph';

/**
 * Props for the DAGSpec component
 */
type Props = {
  /** DAG file name */
  fileName: string;
  /** Local DAGs from parent (optional, avoids redundant fetch) */
  localDags?: components['schemas']['LocalDag'][];
  /** Editor-only metadata used for dynamic schema synthesis */
  editorHints?: components['schemas']['DAGEditorHints'];
};

/**
 * DAGSpec displays and allows editing of a DAG specification
 * including visualization, attributes, steps, and YAML definition
 */
function DAGSpec({ fileName, localDags, editorHints }: Props) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const client = useClient();
  const { schema: baseSchema } = useSchema();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const { setHasUnsavedChanges } = useUnsavedChanges();
  const { preferences, updatePreference } = useUserPreferences();
  const [specRoot, setSpecRoot] = React.useState<HTMLDivElement | null>(null);
  const specWidth = useElementWidth(specRoot);

  const [activeTab, setActiveTab] = React.useState('parent');
  const [selectedSpecStepName, setSelectedSpecStepName] = React.useState<
    string | null
  >(null);
  const [isSpecStepDetailsOpen, setIsSpecStepDetailsOpen] =
    React.useState(false);

  const closeSpecStepDetails = React.useCallback(() => {
    setIsSpecStepDetailsOpen(false);
  }, []);

  const handleActiveTabChange = React.useCallback(
    (tab: string) => {
      setActiveTab(tab);
      setSelectedSpecStepName(null);
      closeSpecStepDetails();
    },
    [closeSpecStepDetails]
  );

  // Flowchart direction preference stored in cookies
  const [cookie, setCookie] = useCookies(['flowchart']);
  const [flowchart, setFlowchart] = React.useState(cookie['flowchart']);

  // Reference to save function and refresh callback for keyboard shortcut
  const saveHandlerRef = React.useRef<(() => Promise<void>) | null>(null);
  const refreshCallbackRef = React.useRef<(() => void) | null>(null);

  /**
   * Handle flowchart direction change and save preference to cookie
   */
  const onChangeFlowchart = React.useCallback(
    (value: FlowchartType) => {
      if (!value) {
        return;
      }
      setCookie('flowchart', value, { path: '/' });
      setFlowchart(value);
    },
    [setCookie, setFlowchart]
  );

  const dagSSE = useDAGSSE(fileName, !!fileName, remoteNode);

  // Fetch spec — SWR is the single source of truth, refreshed by live invalidations
  const {
    data,
    isLoading,
    mutate: mutateSpec,
  } = useQuery(
    '/dags/{fileName}/spec',
    {
      params: {
        query: {
          remoteNode,
        },
        path: {
          fileName: fileName,
        },
      },
    },
    sseFallbackOptions(dagSSE)
  );
  useSSECacheSync(dagSSE, mutateSpec, (next) =>
    next.spec === undefined
      ? undefined
      : {
          dag: next.dag,
          errors: next.errors ?? [],
          warnings: next.warnings ?? [],
          valueReferenceNotices: data?.valueReferenceNotices ?? [],
          spec: next.spec,
        }
  );

  const dagWorkspaceName = React.useMemo(
    () =>
      workspaceNameFromLabels([
        ...(data?.dag?.labels ?? []),
        ...(data?.dag?.tags ?? []),
      ]),
    [data?.dag?.labels, data?.dag?.tags]
  );
  const editable = useCanWriteForWorkspace(dagWorkspaceName);

  // Server spec — SWR cache stays current via live invalidations or polling fallback
  const serverSpec = data?.spec ?? null;
  const valueReferenceNotices = data?.valueReferenceNotices ?? [];

  // Change tracking (source-agnostic)
  const {
    currentValue,
    setCurrentValue,
    hasUnsavedChanges: localHasUnsavedChanges,
    conflict,
    resolveConflict,
    beginSave,
    cancelSave,
    markAsSaved,
    discardChanges,
  } = useContentEditor({
    key: `${fileName}:${remoteNode}`,
    serverContent: serverSpec,
  });

  const { copied: specCopied, copy: copySpec } = useCopyFeedback();

  // Live server-side validation of the edited buffer. Cleared whenever the
  // buffer stops being dirty (save or discard), which also clears the markers.
  // Kept while the next check is pending so the preview does not flip back to
  // the saved spec on every keystroke.
  const [liveValidation, setLiveValidation] = React.useState<{
    errors: string[];
    warnings: string[];
    dag?: components['schemas']['DAGDetails'];
  } | null>(null);
  const [isValidating, setIsValidating] = React.useState(false);
  const [isSaving, setIsSaving] = React.useState(false);
  const validateSeqRef = React.useRef(0);

  React.useEffect(() => {
    if (!editable || !localHasUnsavedChanges || currentValue == null) {
      validateSeqRef.current += 1;
      setLiveValidation(null);
      setIsValidating(false);
      return;
    }

    const seq = ++validateSeqRef.current;
    setIsValidating(true);
    const timer = window.setTimeout(() => {
      void client
        .POST('/dags/validate', {
          params: { query: { remoteNode } },
          body: { spec: currentValue, name: fileName },
        })
        .then(({ data: result, error: requestError }) => {
          if (validateSeqRef.current !== seq) {
            return;
          }
          setIsValidating(false);
          if (!requestError && result) {
            setLiveValidation({
              errors: result.errors ?? [],
              warnings: result.warnings ?? [],
              dag: result.dag,
            });
          } else {
            // A failed request leaves the buffer's validity unknown; stale
            // results from an older buffer would misreport it.
            setLiveValidation(null);
          }
        })
        .catch(() => {
          if (validateSeqRef.current === seq) {
            setIsValidating(false);
            setLiveValidation(null);
          }
        });
    }, 600);

    return () => window.clearTimeout(timer);
  }, [
    client,
    currentValue,
    editable,
    fileName,
    localHasUnsavedChanges,
    remoteNode,
  ]);

  const liveMarkers = React.useMemo(
    () => parseValidationMarkers(liveValidation?.errors ?? []).markers,
    [liveValidation]
  );

  const [lastGoodLegacyDefinitions, setLastGoodLegacyDefinitions] =
    React.useState(
      () =>
        extractLocalCustomDefinitionHints(serverSpec ?? '').legacyDefinitions
    );
  const [lastGoodLocalActions, setLastGoodLocalActions] = React.useState(
    () => extractLocalCustomDefinitionHints(serverSpec ?? '').actions
  );

  const parsedInheritedLegacyDefinitions = React.useMemo(
    () => toInheritedLegacyDefinitionHints(editorHints),
    [editorHints]
  );
  const inheritedLegacyDefinitions = useStableLegacyDefinitionHints(
    parsedInheritedLegacyDefinitions
  );
  const parsedInheritedCustomActions = React.useMemo(
    () => toInheritedCustomActionHints(editorHints),
    [editorHints]
  );
  const inheritedCustomActions = useStableCustomActionHints(
    parsedInheritedCustomActions
  );

  const parsedLocalDefinitions = React.useMemo(
    () => extractLocalCustomDefinitionHints(currentValue ?? serverSpec ?? ''),
    [currentValue, serverSpec]
  );

  useEffect(() => {
    if (!parsedLocalDefinitions.ok) {
      return;
    }
    setLastGoodLegacyDefinitions((previous) =>
      legacyDefinitionHintsEqual(
        previous,
        parsedLocalDefinitions.legacyDefinitions
      )
        ? previous
        : parsedLocalDefinitions.legacyDefinitions
    );
    setLastGoodLocalActions((previous) =>
      customActionHintsEqual(previous, parsedLocalDefinitions.actions)
        ? previous
        : parsedLocalDefinitions.actions
    );
  }, [parsedLocalDefinitions]);

  const effectiveLegacyDefinitions = React.useMemo(() => {
    if (!parsedLocalDefinitions.ok) {
      return lastGoodLegacyDefinitions;
    }
    return legacyDefinitionHintsEqual(
      lastGoodLegacyDefinitions,
      parsedLocalDefinitions.legacyDefinitions
    )
      ? lastGoodLegacyDefinitions
      : parsedLocalDefinitions.legacyDefinitions;
  }, [lastGoodLegacyDefinitions, parsedLocalDefinitions]);
  const effectiveLocalActions = React.useMemo(() => {
    if (!parsedLocalDefinitions.ok) {
      return lastGoodLocalActions;
    }
    return customActionHintsEqual(
      lastGoodLocalActions,
      parsedLocalDefinitions.actions
    )
      ? lastGoodLocalActions
      : parsedLocalDefinitions.actions;
  }, [lastGoodLocalActions, parsedLocalDefinitions]);

  const editorSchema = React.useMemo(() => {
    if (!baseSchema) {
      return null;
    }
    return buildAugmentedDAGSchema(
      baseSchema,
      mergeLegacyDefinitionHints(
        inheritedLegacyDefinitions,
        effectiveLegacyDefinitions
      ),
      mergeCustomActionHints(inheritedCustomActions, effectiveLocalActions)
    );
  }, [
    baseSchema,
    effectiveLocalActions,
    effectiveLegacyDefinitions,
    inheritedCustomActions,
    inheritedLegacyDefinitions,
  ]);

  const editorModelUri = React.useMemo(
    () =>
      `inmemory://dagu/${encodeURIComponent(remoteNode)}/dags/${encodeURIComponent(fileName)}.yaml`,
    [fileName, remoteNode]
  );

  // Sync unsaved changes context
  useEffect(() => {
    setHasUnsavedChanges(localHasUnsavedChanges);
  }, [localHasUnsavedChanges, setHasUnsavedChanges]);

  // Clean up unsaved changes state on unmount
  useEffect(() => {
    return () => {
      setHasUnsavedChanges(false);
    };
  }, [setHasUnsavedChanges]);

  // Save handler function
  const handleSave = React.useCallback(async () => {
    if (isSaving) {
      return;
    }
    if (currentValue == null) {
      showError('No changes to save', 'Make some edits before saving.');
      return;
    }

    beginSave(currentValue);

    setIsSaving(true);
    try {
      const { data: responseData, error } = await client.PUT(
        '/dags/{fileName}/spec',
        {
          params: {
            path: {
              fileName: fileName,
            },
            query: {
              remoteNode,
            },
          },
          body: {
            spec: currentValue,
          },
        }
      );

      if (error) {
        cancelSave();
        showError(
          error.message || 'Failed to save spec',
          'Please check the YAML syntax and try again.'
        );
        return;
      }

      if (responseData?.errors?.length) {
        cancelSave();
        // Feed the rejected save into the same markers/panel as live validation.
        setLiveValidation((prev) => ({
          errors: responseData.errors,
          warnings: prev?.warnings ?? [],
          dag: prev?.dag,
        }));
        showError(
          'The spec was not saved',
          undefined,
          'Validation errors',
          responseData.errors
        );
        return;
      }

      // Mark as saved to prevent false conflict detection on our own save
      markAsSaved(currentValue);

      // Revalidate SWR cache from server as safety net
      mutateSpec();

      // Show success toast notification
      showToast('Changes saved successfully');
    } catch {
      cancelSave();
      showError('Failed to save spec', 'Please try again.');
    } finally {
      setIsSaving(false);
    }
  }, [
    isSaving,
    currentValue,
    fileName,
    remoteNode,
    client,
    showError,
    showToast,
    beginSave,
    cancelSave,
    markAsSaved,
    mutateSpec,
  ]);

  // Update save handler ref when handleSave changes
  useEffect(() => {
    saveHandlerRef.current = handleSave;
  }, [handleSave]);

  // Add keyboard shortcut for saving (Ctrl+S / Cmd+S)
  useEffect(() => {
    if (!editable) {
      return;
    }

    const handleKeyDown = async (event: KeyboardEvent) => {
      // Check for Ctrl+S (Windows/Linux) or Cmd+S (macOS)
      if ((event.ctrlKey || event.metaKey) && event.key === 's') {
        event.preventDefault(); // Prevent browser's default save dialog

        // Call the save handler if available
        if (saveHandlerRef.current) {
          await saveHandlerRef.current();

          // Refresh after saving
          if (refreshCallbackRef.current) {
            refreshCallbackRef.current();
          }
        }
      }
    };

    // Add event listener to document
    document.addEventListener('keydown', handleKeyDown);

    // Cleanup on unmount
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [editable]);

  // Show loading indicator while fetching data
  if (isLoading) {
    return <LoadingIndicator />;
  }

  const warnings = localHasUnsavedChanges
    ? (liveValidation?.warnings ?? [])
    : (data?.warnings ?? []);

  // Check if we have local DAGs
  const hasLocalDags = localDags && localDags.length > 0;

  // The saved type decides the layout, so typing does not reshape the tab.
  const isAgentDag = data?.dag?.type === 'agent';
  const canSplit =
    !isAgentDag &&
    (specWidth === undefined || specWidth >= SPLIT_VIEW_MIN_WIDTH);
  // A stored Split choice survives narrow windows and returns on wide ones.
  const view: SpecViewMode =
    preferences.specViewMode === 'split' && !canSplit
      ? SPLIT_FALLBACK_VIEW
      : preferences.specViewMode;

  const handleViewChange = (next: SpecViewMode) => {
    if (next === view) {
      return;
    }
    // The preview remounts in the next view and would reopen a stale drawer.
    closeSpecStepDetails();
    updatePreference('specViewMode', next);
  };

  // While the buffer is dirty, preview the live validation result instead of
  // the saved spec. Local DAGs always preview their saved definition.
  const parentErrors = liveValidation ? liveValidation.errors : data?.errors;
  const selectedLocalDag =
    activeTab === 'parent'
      ? undefined
      : localDags?.find(
          (ld: components['schemas']['LocalDag']) => ld.name === activeTab
        );
  const preview =
    activeTab === 'parent'
      ? { dag: liveValidation?.dag ?? data?.dag, errors: parentErrors }
      : { dag: selectedLocalDag?.dag, errors: selectedLocalDag?.errors };

  const renderErrors = (errors?: string[]) =>
    errors?.length ? (
      <div className="space-y-3">
        {errors.map((e, i) => (
          <div
            key={i}
            className="p-3 bg-destructive/10 rounded-md text-destructive font-mono text-sm break-words flex items-start gap-2"
          >
            <AlertTriangle className="h-4 w-4 mt-0.5 flex-shrink-0" />
            {e}
          </div>
        ))}
      </div>
    ) : null;

  const warningsBanner =
    warnings.length > 0 ? (
      <div
        role="status"
        className="rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-800 dark:text-amber-200"
      >
        <div className="mb-2 flex items-center gap-2 font-medium">
          <AlertTriangle className="h-4 w-4" aria-hidden="true" />
          <I18nText text={'Warnings'} />
        </div>
        <ul className="list-disc space-y-1 pl-5">
          {warnings.map((warning) => (
            <li key={warning} className="whitespace-normal break-words">
              {warning}
            </li>
          ))}
        </ul>
      </div>
    ) : null;

  const localDagTabs = hasLocalDags ? (
    <div className="flex-shrink-0">
      <div className="overflow-x-auto -mx-2 px-2 scrollbar-thin scrollbar-thumb-gray-300">
        <Tabs className="w-max min-w-full">
          <Tab
            isActive={activeTab === 'parent'}
            onClick={() => handleActiveTabChange('parent')}
            className="cursor-pointer whitespace-nowrap"
          >
            {data?.dag?.name} <I18nText text={'(Parent)'} />
          </Tab>
          {localDags?.map((localDag: components['schemas']['LocalDag']) => (
            <Tab
              key={localDag.name}
              isActive={activeTab === localDag.name}
              onClick={() => handleActiveTabChange(localDag.name)}
              className="cursor-pointer whitespace-nowrap"
            >
              {localDag.name}
            </Tab>
          ))}
        </Tabs>
      </div>
    </div>
  ) : null;

  // Renders the preview: the graph alone filling its pane when `fillGraph` is
  // set, otherwise the graph followed by attributes and step tables.
  const renderDAGContent = (
    dag: components['schemas']['DAGDetails'],
    errors: string[] | undefined,
    fillGraph: boolean
  ) => {
    const selectedStep = selectedSpecStepName
      ? dag.steps?.find((step) => step.name === selectedSpecStepName)
      : undefined;

    const handleStepSelect = (step: components['schemas']['Step']) => {
      setSelectedSpecStepName(step.name);
      setIsSpecStepDetailsOpen(true);
    };

    const handleGraphNodeSelect = (nodeId: string) => {
      const step = dag.steps?.find(
        (item) => toMermaidNodeId(item.name) === nodeId
      );
      if (!step) {
        return;
      }
      handleStepSelect(step);
    };

    return (
      <div
        className={
          fillGraph ? 'flex min-h-0 flex-1 flex-col gap-4' : 'space-y-6'
        }
      >
        {renderErrors(errors)}

        {dag.type === 'agent' ? (
          <AgentSpecOverview dag={dag} />
        ) : (
          <>
            {!dag.steps || dag.steps.length === 0 ? (
              <div className="py-8 px-4 text-center">
                <AlertTriangle className="h-12 w-12 text-warning mx-auto mb-4" />
                <p className="text-muted-foreground mb-2">
                  <I18nText text={'No steps to render'} />
                </p>
                <p className="text-sm text-muted-foreground">
                  <I18nText
                    text={'Define at least one step to view the graph'}
                  />
                </p>
              </div>
            ) : (
              <div className={fillGraph ? 'flex min-h-64 flex-1 flex-col' : ''}>
                <BorderedBox
                  className={
                    fillGraph
                      ? 'flex min-h-0 flex-1 flex-col p-4'
                      : 'py-4 px-4 flex flex-col overflow-x-auto'
                  }
                >
                  <Graph
                    steps={dag.steps}
                    name={dag.name}
                    type="config"
                    flowchart={flowchart}
                    onChangeFlowchart={onChangeFlowchart}
                    onClickNode={handleGraphNodeSelect}
                    selectOnClick
                    height={fillGraph ? '100%' : undefined}
                  />
                </BorderedBox>
                <GraphInteractionsHint className="mt-2" inspect />
              </div>
            )}

            {!fillGraph && (
              <>
                {/* The page header describes only the parent DAG. */}
                {activeTab !== 'parent' && dag.description && (
                  <p className="whitespace-pre-line break-words text-muted-foreground">
                    {dag.description}
                  </p>
                )}
                <DAGAttributes dag={dag} />

                {dag.steps ? (
                  <div className="overflow-hidden">
                    <DAGStepTable steps={dag.steps} />
                  </div>
                ) : null}
              </>
            )}
          </>
        )}

        {!fillGraph && getHandlers(dag)?.length ? (
          <div className="overflow-hidden">
            <DAGStepTable steps={getHandlers(dag)} />
          </div>
        ) : null}

        <StepDetailsDrawer
          dagName={dag.name}
          isOpen={isSpecStepDetailsOpen}
          step={selectedStep}
          onClose={closeSpecStepDetails}
        />
      </div>
    );
  };

  const renderValidationStatus = () => {
    if (isValidating) {
      return <I18nText text={'Validating...'} />;
    }
    if (!liveValidation) {
      return null;
    }
    const count = liveValidation.errors.length;
    if (count > 0) {
      return ts(count === 1 ? '{count} issue' : '{count} issues', { count });
    }
    if (liveValidation.warnings.length > 0) {
      return <I18nText text={'Valid with warnings'} />;
    }
    return <I18nText text={'Valid'} />;
  };

  return (
    <DAGContext.Consumer>
      {(props) => {
        // Update refresh callback ref directly (safe in render)
        refreshCallbackRef.current = props.refresh;
        const specActions = (
          <div className="flex items-center gap-2">
            {editable && localHasUnsavedChanges && (
              <span
                className={
                  !isValidating && liveValidation?.errors.length
                    ? 'text-xs text-destructive'
                    : 'text-xs text-muted-foreground'
                }
              >
                {renderValidationStatus()}
              </span>
            )}
            {valueReferenceNotices.length > 0 && (
              <I18nProps>
                <ValueReferenceNoticesButton
                  notices={valueReferenceNotices}
                  description="Value-reference notices produced while loading this spec."
                />
              </I18nProps>
            )}
            <I18nProps>
              <Button
                variant="ghost"
                title="Copy YAML"
                aria-label={specCopied ? 'YAML copied' : 'Copy YAML'}
                onClick={() => copySpec(currentValue ?? serverSpec ?? '')}
              >
                {specCopied ? (
                  <Check className="h-4 w-4 text-green-500" />
                ) : (
                  <Copy className="h-4 w-4" />
                )}
                <I18nText text={'Copy'} />
              </Button>
            </I18nProps>
            {editable && (
              <>
                {localHasUnsavedChanges && (
                  <I18nProps>
                    <Button
                      variant="ghost"
                      title="Discard changes"
                      onClick={discardChanges}
                    >
                      <Undo2 className="h-4 w-4" />
                      <I18nText text={'Discard'} />
                    </Button>
                  </I18nProps>
                )}
                <I18nProps>
                  <Button
                    id="save-config"
                    title="Save changes (Ctrl+S / Cmd+S)"
                    disabled={isSaving || !localHasUnsavedChanges}
                    onClick={async () => {
                      await handleSave();
                      props.refresh();
                    }}
                  >
                    <Save className="h-4 w-4" />
                    <I18nText text={'Save'} />
                  </Button>
                </I18nProps>
              </>
            )}
          </div>
        );

        return (
          data?.dag && (
            <React.Fragment>
              {/* External changes conflict dialog */}
              <ExternalChangeDialog
                visible={conflict.hasConflict}
                onDiscard={() => resolveConflict('discard')}
                onIgnore={() => resolveConflict('ignore')}
              />

              <div
                ref={setSpecRoot}
                className="flex min-h-0 flex-1 flex-col gap-3"
              >
                <div className="flex flex-shrink-0 flex-wrap items-center justify-between gap-2">
                  <SpecViewSwitcher
                    value={view}
                    onChange={handleViewChange}
                    canSplit={canSplit}
                    isAgent={isAgentDag}
                  />
                  {specActions}
                </div>

                <div
                  className={cn(
                    'flex min-h-80 flex-1 gap-4',
                    view === 'split' ? 'flex-row' : 'flex-col'
                  )}
                >
                  {view !== 'yaml' && (
                    <div
                      className={
                        view === 'split'
                          ? 'flex min-h-0 min-w-0 shrink-0 basis-2/5 flex-col gap-4 overflow-y-auto'
                          : 'min-h-0 min-w-0 flex-1 space-y-6 overflow-y-auto'
                      }
                    >
                      {warningsBanner}
                      {localDagTabs}
                      {preview.dag &&
                        renderDAGContent(
                          preview.dag,
                          preview.errors,
                          view === 'split'
                        )}
                    </div>
                  )}

                  {/* Stays mounted in every view so the editor keeps its
                      undo history, cursor and scroll position. */}
                  <section
                    hidden={view === 'graph'}
                    aria-label={ts('YAML')}
                    className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
                  >
                    <DAGEditorWithDocs
                      value={
                        editable
                          ? (currentValue ?? serverSpec ?? '')
                          : (serverSpec ?? '')
                      }
                      readOnly={!editable}
                      onChange={
                        editable
                          ? (newValue) => {
                              setCurrentValue(newValue ?? '');
                            }
                          : undefined
                      }
                      className="h-auto min-h-0 flex-1"
                      modelUri={editorModelUri}
                      schema={editorSchema}
                      markers={liveMarkers}
                    />
                    {/* Below the editor so validation messages never push it
                        down while typing. */}
                    {view === 'yaml' &&
                      (warningsBanner || parentErrors?.length) && (
                        <div className="max-h-40 flex-shrink-0 space-y-3 overflow-y-auto">
                          {warningsBanner}
                          {renderErrors(parentErrors)}
                        </div>
                      )}
                  </section>
                </div>
              </div>
            </React.Fragment>
          )
        );
      }}
    </DAGContext.Consumer>
  );
}

/**
 * Extract lifecycle handlers from DAG definition
 */
function getHandlers(
  dag?: components['schemas']['DAGDetails']
): components['schemas']['Step'][] {
  const steps: components['schemas']['Step'][] = [];
  if (!dag) {
    return steps;
  }
  const h = dag.handlerOn;
  if (h?.success) {
    steps.push(h.success);
  }
  if (h?.failure) {
    steps.push(h?.failure);
  }
  if (h?.abort) {
    steps.push(h?.abort);
  }
  if (h?.exit) {
    steps.push(h?.exit);
  }
  return steps;
}

function useStableLegacyDefinitionHints(
  hints: EditorLegacyDefinitionHint[]
): EditorLegacyDefinitionHint[] {
  const stableRef = React.useRef(hints);
  if (!legacyDefinitionHintsEqual(stableRef.current, hints)) {
    stableRef.current = hints;
  }
  return stableRef.current;
}

/** Tracks the rendered width of `element`, or undefined before it mounts. */
function useElementWidth(element: HTMLElement | null): number | undefined {
  const [width, setWidth] = React.useState<number>();

  // Layout effect so the first paint already uses the measured width.
  React.useLayoutEffect(() => {
    if (!element) {
      return;
    }
    setWidth(element.getBoundingClientRect().width);
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(([entry]) => {
      if (entry) {
        setWidth(entry.contentRect.width);
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);

  return width;
}

function useStableCustomActionHints(
  hints: EditorCustomActionHint[]
): EditorCustomActionHint[] {
  const stableRef = React.useRef(hints);
  if (!customActionHintsEqual(stableRef.current, hints)) {
    stableRef.current = hints;
  }
  return stableRef.current;
}

export default DAGSpec;
