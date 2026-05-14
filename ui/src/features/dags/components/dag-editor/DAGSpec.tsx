// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGSpec component displays and allows editing of a DAG specification.
 *
 * @module features/dags/components/dag-editor
 */
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { StepDetailsDrawer } from '@/features/dags/components/step-details';
import { toMermaidNodeId } from '@/lib/utils';
import { workspaceNameFromLabels } from '@/lib/workspace';
import BorderedBox from '@/components/ui/bordered-box';
import { AlertTriangle, MousePointerClick, Save, Undo2 } from 'lucide-react';
import React, { useEffect } from 'react';
import { useCookies } from 'react-cookie';
import { components } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { Tab, Tabs } from '@/components/ui/tabs';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { AppBarContext } from '../../../../contexts/AppBarContext';
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
import { FlowchartType, Graph } from '../visualization';
import {
  buildAugmentedDAGSchema,
  customActionHintsEqual,
  extractLocalCustomDefinitionHints,
  legacyDefinitionHintsEqual,
  mergeCustomActionHints,
  mergeLegacyDefinitionHints,
  toInheritedCustomActionHints,
  toInheritedLegacyDefinitionHints,
} from './customActionSchema';
import DAGAttributes from './DAGAttributes';
import DAGEditorWithDocs from './DAGEditorWithDocs';
import ExternalChangeDialog from './ExternalChangeDialog';

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
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const client = useClient();
  const { schema: baseSchema } = useSchema();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const { setHasUnsavedChanges } = useUnsavedChanges();

  const [scrollPosition, setScrollPosition] = React.useState(0);
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

  // Reference to the main container div
  const containerRef = React.useRef<HTMLDivElement>(null);

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

  const dagSSE = useDAGSSE(fileName, !!fileName);

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

  // Change tracking (source-agnostic)
  const {
    currentValue,
    setCurrentValue,
    hasUnsavedChanges: localHasUnsavedChanges,
    conflict,
    resolveConflict,
    markAsSaved,
    discardChanges,
  } = useContentEditor({
    key: `${fileName}:${remoteNode}`,
    serverContent: serverSpec,
  });

  const [lastGoodLegacyDefinitions, setLastGoodLegacyDefinitions] =
    React.useState(
      () =>
        extractLocalCustomDefinitionHints(serverSpec ?? '').legacyDefinitions
    );
  const [lastGoodLocalActions, setLastGoodLocalActions] = React.useState(
    () => extractLocalCustomDefinitionHints(serverSpec ?? '').actions
  );

  const inheritedLegacyDefinitions = React.useMemo(
    () => toInheritedLegacyDefinitionHints(editorHints),
    [editorHints]
  );
  const inheritedCustomActions = React.useMemo(
    () => toInheritedCustomActionHints(editorHints),
    [editorHints]
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

  // Save scroll position before saving
  const saveScrollPosition = React.useCallback(() => {
    if (containerRef.current) {
      setScrollPosition(window.scrollY);
    }
  }, []);

  // Save handler function
  const handleSave = React.useCallback(async () => {
    if (currentValue == null) {
      showError('No changes to save', 'Make some edits before saving.');
      return;
    }

    // Save current scroll position before any operations that might cause re-render
    saveScrollPosition();

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
      showError(
        error.message || 'Failed to save spec',
        'Please check the YAML syntax and try again.'
      );
      return;
    }

    if (responseData?.errors) {
      showError('Validation errors', responseData.errors.join('\n'));
      return;
    }

    // Mark as saved to prevent false conflict detection on our own save
    markAsSaved(currentValue);

    // Revalidate SWR cache from server as safety net
    mutateSpec();

    // Show success toast notification
    showToast('Changes saved successfully');
  }, [
    currentValue,
    fileName,
    remoteNode,
    client,
    saveScrollPosition,
    showError,
    showToast,
    markAsSaved,
    mutateSpec,
  ]);

  // Restore scroll position after render
  useEffect(() => {
    if (scrollPosition > 0) {
      // Use a small timeout to ensure the DOM has updated before scrolling
      const timer = setTimeout(() => {
        window.scrollTo({
          top: scrollPosition,
          behavior: 'auto', // Use 'auto' instead of 'smooth' to avoid animation
        });
      }, 100);

      return () => clearTimeout(timer);
    }
  }, [scrollPosition]);

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

  // Check if we have local DAGs
  const hasLocalDags = localDags && localDags.length > 0;

  // Helper function to render DAG content (Graph, Attributes, Steps, Errors)
  const renderDAGContent = (
    dag: components['schemas']['DAGDetails'],
    errors?: string[]
  ) => {
    const selectedStep = selectedSpecStepName
      ? dag.steps?.find((step) => step.name === selectedSpecStepName)
      : undefined;

    const handleGraphNodeSelect = (nodeId: string) => {
      const step = dag.steps?.find(
        (item) => toMermaidNodeId(item.name) === nodeId
      );
      if (!step) {
        return;
      }
      setSelectedSpecStepName(step.name);
      setIsSpecStepDetailsOpen(true);
    };

    return (
      <div className="space-y-6">
        {errors?.length ? (
          <div className="space-y-3">
            {errors.map((e, i) => (
              <div
                key={i}
                className="p-3 bg-danger-highlight rounded-md text-danger font-mono text-sm break-words flex items-start gap-2"
              >
                <AlertTriangle className="h-4 w-4 mt-0.5 flex-shrink-0" />
                {e}
              </div>
            ))}
          </div>
        ) : null}

        {errors?.length || !dag.steps || dag.steps.length === 0 ? (
          <div className="py-8 px-4 text-center">
            <AlertTriangle className="h-12 w-12 text-warning mx-auto mb-4" />
            <p className="text-muted-foreground mb-2">
              Cannot render graph due to configuration errors
            </p>
            <p className="text-sm text-muted-foreground">
              Please fix the errors above and save the configuration to view the
              graph
            </p>
          </div>
        ) : (
          <div>
            <BorderedBox className="py-4 px-4 flex flex-col overflow-x-auto">
              <Graph
                steps={dag.steps}
                type="config"
                flowchart={flowchart}
                onChangeFlowchart={onChangeFlowchart}
                onClickNode={handleGraphNodeSelect}
                selectOnClick
                showIcons={false}
              />
            </BorderedBox>
            <div className="mt-2 flex justify-end">
              <Tooltip>
                <TooltipTrigger asChild>
                  <div
                    className="flex h-7 w-7 items-center justify-center rounded bg-muted text-muted-foreground cursor-help"
                    aria-label="Graph interactions"
                  >
                    <MousePointerClick className="h-3.5 w-3.5" />
                  </div>
                </TooltipTrigger>
                <TooltipContent>
                  <p>Click: Inspect step details</p>
                </TooltipContent>
              </Tooltip>
            </div>
          </div>
        )}

        <DAGAttributes dag={dag} />

        {dag.steps ? (
          <div className="overflow-hidden">
            <DAGStepTable steps={dag.steps} />
          </div>
        ) : null}

        {getHandlers(dag)?.length ? (
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

  return (
    <DAGContext.Consumer>
      {(props) => {
        // Update refresh callback ref directly (safe in render)
        refreshCallbackRef.current = props.refresh;

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
                className="flex flex-col flex-1 min-h-0 space-y-6 mb-6"
                ref={containerRef}
              >
                {hasLocalDags && (
                  <div className="flex-shrink-0">
                    <div className="overflow-x-auto -mx-2 px-2 scrollbar-thin scrollbar-thumb-gray-300">
                      <Tabs className="w-max min-w-full">
                        <Tab
                          isActive={activeTab === 'parent'}
                          onClick={() => handleActiveTabChange('parent')}
                          className="cursor-pointer whitespace-nowrap"
                        >
                          {data?.dag?.name} (Parent)
                        </Tab>
                        {localDags?.map(
                          (localDag: components['schemas']['LocalDag']) => (
                            <Tab
                              key={localDag.name}
                              isActive={activeTab === localDag.name}
                              onClick={() =>
                                handleActiveTabChange(localDag.name)
                              }
                              className="cursor-pointer whitespace-nowrap"
                            >
                              {localDag.name}
                            </Tab>
                          )
                        )}
                      </Tabs>
                    </div>
                  </div>
                )}

                {(() => {
                  if (activeTab === 'parent') {
                    return (
                      data?.dag && (
                        <div className="flex-shrink-0">
                          {renderDAGContent(data.dag, data?.errors)}
                        </div>
                      )
                    );
                  }
                  const selectedLocalDag = localDags?.find(
                    (ld: components['schemas']['LocalDag']) =>
                      ld.name === activeTab
                  );
                  return (
                    selectedLocalDag?.dag && (
                      <div className="flex-shrink-0">
                        {renderDAGContent(
                          selectedLocalDag.dag,
                          selectedLocalDag.errors
                        )}
                      </div>
                    )
                  );
                })()}

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
                  className="min-h-[400px]"
                  modelUri={editorModelUri}
                  schema={editorSchema}
                  headerActions={
                    editable ? (
                      <>
                        {localHasUnsavedChanges && (
                          <Button
                            variant="ghost"
                            title="Discard changes"
                            onClick={discardChanges}
                          >
                            <Undo2 className="h-4 w-4" />
                            Discard
                          </Button>
                        )}
                        <Button
                          id="save-config"
                          title="Save changes (Ctrl+S / Cmd+S)"
                          disabled={!localHasUnsavedChanges}
                          onClick={async () => {
                            await handleSave();
                            props.refresh();
                          }}
                        >
                          <Save className="h-4 w-4" />
                          Save
                        </Button>
                      </>
                    ) : undefined
                  }
                />
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

export default DAGSpec;
