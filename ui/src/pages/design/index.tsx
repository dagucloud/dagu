// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { Tab, Tabs } from '@/components/ui/tabs';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWrite } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { usePageContext } from '@/contexts/PageContext';
import { useSchema } from '@/contexts/SchemaContext';
import { useUnsavedChanges } from '@/contexts/UnsavedChangesContext';
import { AgentChatPanelView, useAgentChat } from '@/features/agent';
import type {
  AgentChatController,
  DAGContext as AgentDAGContext,
} from '@/features/agent';
import { DAGStepTable } from '@/features/dags/components/dag-details';
import DAGAttributes from '@/features/dags/components/dag-editor/DAGAttributes';
import DAGEditorWithDocs from '@/features/dags/components/dag-editor/DAGEditorWithDocs';
import ExternalChangeDialog from '@/features/dags/components/dag-editor/ExternalChangeDialog';
import {
  buildAugmentedDAGSchema,
  customActionHintsEqual,
  extractLocalCustomDefinitionHints,
  legacyDefinitionHintsEqual,
  mergeCustomActionHints,
  mergeLegacyDefinitionHints,
  toInheritedCustomActionHints,
  toInheritedLegacyDefinitionHints,
} from '@/features/dags/components/dag-editor/customActionSchema';
import { StepDetails } from '@/features/dags/components/step-details';
import { FlowchartType, Graph } from '@/features/dags/components/visualization';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useContentEditor } from '@/hooks/useContentEditor';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { validateDAGName } from '@/lib/dag-validation';
import { ensureWorkspaceLabelInDAGSpec } from '@/lib/dagSpec';
import {
  workspaceSelectionKey,
  workspaceSelectionQuery,
  workspaceSelectionLabel,
  WorkspaceKind,
  workspaceNameForSelection,
} from '@/lib/workspace';
import { cn, toMermaidNodeId } from '@/lib/utils';
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  FileCode,
  GitBranch,
  Network,
  Plus,
  RefreshCw,
  Save,
  Search,
  Send,
  MessageSquare,
  Sparkles,
  XCircle,
} from 'lucide-react';
import React from 'react';
import { useCookies } from 'react-cookie';
import { Link, useSearchParams } from 'react-router-dom';
import { buildWorkflowDesignPrompt } from './buildWorkflowDesignPrompt';

type DAGDetails = components['schemas']['DAGDetails'];
type Step = components['schemas']['Step'];

type ValidationState = {
  valid: boolean;
  dag?: DAGDetails;
  errors: string[];
};

type LeftPanelTab = 'workflows' | 'agent';
type DesignResizeSide = 'left' | 'right';

const NEW_DAG_VALUE = '__new__';
const DEFAULT_DRAFT_SPEC = `steps:
  - name: hello
    run: echo hello
`;
const DESIGN_LEFT_PANEL_STORAGE_KEY = 'workflowDesignLeftPanelWidth';
const DESIGN_RIGHT_PANEL_STORAGE_KEY = 'workflowDesignRightPanelWidth';
const DESIGN_RESIZE_HANDLE_WIDTH = 4;
const DESIGN_PANEL_LIMITS = {
  left: {
    defaultWidth: 360,
    minWidth: 280,
    maxWidth: 560,
  },
  right: {
    defaultWidth: 360,
    minWidth: 280,
    maxWidth: 720,
  },
  minMainWidth: 480,
  minWorkspaceWidth: 360,
};

function WorkflowDesignPage() {
  const canWriteInSelectedScope = useCanWrite();
  const config = useConfig();
  const client = useClient();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspace = workspaceNameForSelection(workspaceSelection);
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(workspaceSelection),
    [workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(workspaceSelection);
  const workspaceDescription = workspaceSelectionLabel(workspaceSelection);
  const { setContext } = usePageContext();
  const { schema: baseSchema } = useSchema();
  const { setHasUnsavedChanges } = useUnsavedChanges();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedDagFile = searchParams.get('dag') || '';
  const selectedStepName = searchParams.get('step') || '';
  const {
    contentRef,
    draggingSide,
    getSeparatorValue,
    handleSeparatorKeyDown,
    layoutStyle,
    shellRef,
    startResize,
  } = useDesignPanelLayout();

  const agent = useAgentChat({ active: true });
  const [requestText, setRequestText] = React.useState('');
  const [isSendingDesignRequest, setIsSendingDesignRequest] =
    React.useState(false);
  const [newDagName, setNewDagName] = React.useState('');
  const [newDraftSpec, setNewDraftSpec] = React.useState(DEFAULT_DRAFT_SPEC);
  const [validation, setValidation] = React.useState<ValidationState | null>(
    null
  );
  const [isValidating, setIsValidating] = React.useState(false);
  const [isSaving, setIsSaving] = React.useState(false);
  const [leftPanelTab, setLeftPanelTab] =
    React.useState<LeftPanelTab>('workflows');
  const [dagSearch, setDagSearch] = React.useState('');

  const [cookie, setCookie] = useCookies(['flowchart']);
  const [flowchart, setFlowchart] = React.useState<FlowchartType>(
    cookie.flowchart || 'TD'
  );

  const dagListQuery = useQuery('/dags', {
    params: {
      query: {
        remoteNode,
        perPage: 200,
        ...workspaceQuery,
      },
    },
  });
  const selectedDagInWorkspace = React.useMemo(
    () =>
      !!selectedDagFile &&
      (dagListQuery.data?.dags ?? []).some(
        (dag) => dag.fileName === selectedDagFile
      ),
    [dagListQuery.data?.dags, selectedDagFile]
  );

  React.useEffect(() => {
    if (!selectedDagFile || !dagListQuery.data) return;
    if (selectedDagInWorkspace) return;

    const next = new URLSearchParams(searchParams);
    next.delete('dag');
    next.delete('step');
    setSearchParams(next, { replace: true });
  }, [
    dagListQuery.data,
    searchParams,
    selectedDagFile,
    selectedDagInWorkspace,
    setSearchParams,
  ]);

  const {
    data: specData,
    isLoading: isSpecLoading,
    mutate: mutateSpec,
  } = useQuery(
    '/dags/{fileName}/spec',
    whenEnabled(selectedDagInWorkspace, {
      params: {
        path: { fileName: selectedDagFile },
        query: { remoteNode },
      },
    })
  );

  const serverSpec = selectedDagInWorkspace ? (specData?.spec ?? null) : null;
  const {
    currentValue,
    setCurrentValue,
    hasUnsavedChanges: hasExistingUnsavedChanges,
    conflict,
    resolveConflict,
    markAsSaved,
    discardChanges,
  } = useContentEditor({
    key: JSON.stringify({
      remoteNode,
      workspace: workspaceKey,
      dag: selectedDagFile || NEW_DAG_VALUE,
    }),
    serverContent: serverSpec,
  });

  const editorValue = selectedDagFile
    ? (currentValue ?? serverSpec ?? '')
    : newDraftSpec;
  const hasUnsavedChanges = selectedDagFile
    ? hasExistingUnsavedChanges
    : newDraftSpec.trim() !== DEFAULT_DRAFT_SPEC.trim() || newDagName !== '';

  const [lastGoodLegacyDefinitions, setLastGoodLegacyDefinitions] =
    React.useState(
      () => extractLocalCustomDefinitionHints(editorValue).legacyDefinitions
    );
  const [lastGoodLocalActions, setLastGoodLocalActions] = React.useState(
    () => extractLocalCustomDefinitionHints(editorValue).actions
  );

  const parsedLocalDefinitions = React.useMemo(
    () => extractLocalCustomDefinitionHints(editorValue),
    [editorValue]
  );
  const inheritedLegacyDefinitions = React.useMemo(
    () => toInheritedLegacyDefinitionHints(undefined),
    []
  );
  const inheritedCustomActions = React.useMemo(
    () => toInheritedCustomActionHints(undefined),
    []
  );

  React.useEffect(() => {
    if (!parsedLocalDefinitions.ok) return;
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
    if (!parsedLocalDefinitions.ok) return lastGoodLegacyDefinitions;
    return legacyDefinitionHintsEqual(
      lastGoodLegacyDefinitions,
      parsedLocalDefinitions.legacyDefinitions
    )
      ? lastGoodLegacyDefinitions
      : parsedLocalDefinitions.legacyDefinitions;
  }, [lastGoodLegacyDefinitions, parsedLocalDefinitions]);
  const effectiveLocalActions = React.useMemo(() => {
    if (!parsedLocalDefinitions.ok) return lastGoodLocalActions;
    return customActionHintsEqual(
      lastGoodLocalActions,
      parsedLocalDefinitions.actions
    )
      ? lastGoodLocalActions
      : parsedLocalDefinitions.actions;
  }, [lastGoodLocalActions, parsedLocalDefinitions]);

  const editorSchema = React.useMemo(() => {
    if (!baseSchema) return null;
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
    inheritedLegacyDefinitions,
    effectiveLegacyDefinitions,
    inheritedCustomActions,
    effectiveLocalActions,
  ]);

  const editorModelUri = React.useMemo(() => {
    const workspaceSegment = encodeURIComponent(
      JSON.stringify({ workspace: workspaceKey })
    );
    return selectedDagFile
      ? `inmemory://dagu/${encodeURIComponent(remoteNode)}/${workspaceSegment}/design/${encodeURIComponent(selectedDagFile)}.yaml`
      : `inmemory://dagu/${encodeURIComponent(remoteNode)}/${workspaceSegment}/design/new.yaml`;
  }, [remoteNode, selectedDagFile, workspaceKey]);

  const debouncedSpec = useDebouncedValue(editorValue, 500);
  const validationName = selectedDagFile || newDagName || 'designed-dag';

  React.useEffect(() => {
    setHasUnsavedChanges(hasUnsavedChanges);
  }, [hasUnsavedChanges, setHasUnsavedChanges]);

  React.useEffect(() => {
    return () => setHasUnsavedChanges(false);
  }, [setHasUnsavedChanges]);

  React.useEffect(() => {
    setContext(
      selectedDagFile
        ? {
            dagFile: selectedDagFile,
            source: 'workflow-design-page',
          }
        : {
            source: 'workflow-design-page',
          }
    );

    return () => setContext(null);
  }, [selectedDagFile, setContext]);

  React.useEffect(() => {
    if (!debouncedSpec.trim()) {
      setValidation(null);
      setIsValidating(false);
      return;
    }

    let cancelled = false;
    setIsValidating(true);

    async function validateSpec() {
      try {
        const { data, error } = await client.POST('/dags/validate', {
          params: { query: { remoteNode } },
          body: {
            spec: debouncedSpec,
            name: validationName,
          },
        });
        if (cancelled) return;
        if (error || !data) {
          setValidation({
            valid: false,
            errors: [error?.message || 'Failed to validate DAG specification'],
          });
          return;
        }
        setValidation({
          valid: data.valid,
          dag: data.dag,
          errors: data.errors || [],
        });
      } catch (err) {
        if (cancelled) return;
        setValidation({
          valid: false,
          errors: [
            err instanceof Error
              ? err.message
              : 'Failed to validate DAG specification',
          ],
        });
      } finally {
        if (!cancelled) setIsValidating(false);
      }
    }

    void validateSpec();

    return () => {
      cancelled = true;
    };
  }, [client, debouncedSpec, remoteNode, validationName]);

  const wasAgentWorkingRef = React.useRef(false);
  React.useEffect(() => {
    if (wasAgentWorkingRef.current && !agent.isWorking && selectedDagFile) {
      mutateSpec();
    }
    wasAgentWorkingRef.current = agent.isWorking;
  }, [agent.isWorking, selectedDagFile, mutateSpec]);

  const dagFiles = dagListQuery.data?.dags || [];
  const selectedDag = validation?.dag || specData?.dag;
  const steps = selectedDag?.steps || [];
  const selectedStep = steps.find((step) => step.name === selectedStepName);
  const hasValidationErrors = !!validation?.errors?.length;
  const canSubmitDesignRequest =
    canWriteInSelectedScope &&
    config.agentEnabled &&
    requestText.trim().length > 0 &&
    !isSendingDesignRequest &&
    !agent.isWorking;
  const canSaveExisting = selectedDagFile && hasExistingUnsavedChanges;
  const newNameValidation = newDagName ? validateDAGName(newDagName) : null;
  const canCreateNew =
    !selectedDagFile &&
    !!newDagName &&
    !!validation?.valid &&
    newNameValidation?.isValid !== false;
  const canSave =
    canWriteInSelectedScope &&
    (selectedDagFile ? !!canSaveExisting : canCreateNew);

  const updateSearch = (dagFile: string, stepName?: string) => {
    const next = new URLSearchParams(searchParams);
    if (dagFile) {
      next.set('dag', dagFile);
    } else {
      next.delete('dag');
    }
    if (stepName) {
      next.set('step', stepName);
    } else {
      next.delete('step');
    }
    setSearchParams(next, { replace: true });
  };

  const handleSelectDag = (value: string) => {
    if (value === NEW_DAG_VALUE) {
      updateSearch('');
      return;
    }
    updateSearch(value);
  };

  const handleSelectStep = (stepName: string) => {
    if (stepName === NEW_DAG_VALUE) {
      updateSearch(selectedDagFile);
      return;
    }
    updateSearch(selectedDagFile, stepName);
  };

  const handleGraphNodeSelect = (nodeId: string) => {
    const step = steps.find((item) => toMermaidNodeId(item.name) === nodeId);
    if (step) {
      handleSelectStep(step.name);
    }
  };

  const handleFlowchartChange = (value: FlowchartType) => {
    if (!value) return;
    setCookie('flowchart', value, { path: '/' });
    setFlowchart(value);
  };

  const handleEditorChange = (value?: string) => {
    if (selectedDagFile) {
      setCurrentValue(value ?? '');
      return;
    }
    setNewDraftSpec(value ?? '');
  };

  const handleSave = async () => {
    if (!canWriteInSelectedScope) {
      showError(
        'Workspace is read-only',
        'Select default or a writable workspace before changing a DAG.'
      );
      return;
    }
    if (selectedDagFile) {
      if (!canSaveExisting || currentValue == null) return;
      setIsSaving(true);
      try {
        const { data, error } = await client.PUT('/dags/{fileName}/spec', {
          params: {
            path: { fileName: selectedDagFile },
            query: { remoteNode },
          },
          body: { spec: currentValue },
        });
        if (error) {
          showError(
            error.message || 'Failed to save DAG specification',
            'Please check the YAML syntax and try again.'
          );
          return;
        }
        if (data?.errors?.length) {
          showError('Validation errors', data.errors.join('\n'));
          return;
        }
        markAsSaved(currentValue);
        mutateSpec();
        showToast('DAG specification saved');
      } finally {
        setIsSaving(false);
      }
      return;
    }

    const nameResult = validateDAGName(newDagName);
    if (!nameResult.isValid) {
      showError('Invalid DAG name', nameResult.error || 'Choose another name.');
      return;
    }
    if (!validation?.valid) {
      showError(
        'Cannot create invalid DAG',
        'Fix validation errors before creating the DAG.'
      );
      return;
    }

    setIsSaving(true);
    const specToSave =
      workspaceSelection?.kind === WorkspaceKind.workspace && selectedWorkspace
        ? ensureWorkspaceLabelInDAGSpec(newDraftSpec, selectedWorkspace)
        : newDraftSpec;
    try {
      const { error } = await client.POST('/dags', {
        params: { query: { remoteNode } },
        body: {
          name: newDagName,
          spec: specToSave,
        },
      });
      if (error) {
        showError(
          error.message || 'Failed to create DAG',
          'Please check the name and YAML specification.'
        );
        return;
      }
      showToast('DAG created');
      updateSearch(newDagName);
      setNewDagName('');
      setNewDraftSpec(DEFAULT_DRAFT_SPEC);
    } finally {
      setIsSaving(false);
    }
  };

  const handleSendDesignRequest = async () => {
    const trimmed = requestText.trim();
    if (!trimmed) return;
    if (!canWriteInSelectedScope) {
      showError(
        'Workspace is read-only',
        'Select default or a writable workspace before asking the agent to change a DAG.'
      );
      return;
    }
    if (!config.agentEnabled) {
      showError(
        'Agent is disabled',
        'Enable the agent before starting an agentic workflow design session.'
      );
      return;
    }
    if (selectedDagFile && hasExistingUnsavedChanges) {
      showError(
        'Save or discard local edits first',
        'The agent edits the saved DAG file on disk, so local YAML edits must be resolved before asking it to update the DAG.'
      );
      return;
    }
    if (!selectedDagFile && !newDagName) {
      showError(
        'DAG name required',
        'Enter a target DAG name before asking the agent to create a workflow.'
      );
      return;
    }

    setIsSendingDesignRequest(true);
    setLeftPanelTab('agent');
    try {
      const dagContexts: AgentDAGContext[] | undefined = selectedDagFile
        ? [{ dag_file: selectedDagFile }]
        : undefined;
      await agent.sendMessage(
        buildWorkflowDesignPrompt({
          mode: selectedDagFile ? 'update' : 'create',
          dagFile: selectedDagFile || undefined,
          newDagName: newDagName || undefined,
          stepName: selectedStepName || undefined,
          remoteNode,
          selectedWorkspace,
          workspaceDescription,
          userPrompt: trimmed,
          draftSpec: selectedDagFile ? undefined : newDraftSpec,
          validationErrors: validation?.errors,
        }),
        undefined,
        dagContexts
      );
      setRequestText('');
    } finally {
      setIsSendingDesignRequest(false);
    }
  };

  return (
    <div className="h-full w-full overflow-hidden bg-background">
      <div
        ref={shellRef}
        className="grid h-full min-h-0 grid-cols-1 grid-rows-[minmax(320px,40vh)_minmax(0,1fr)] bg-background lg:grid-cols-[var(--design-left-panel-width)_var(--design-resize-handle-width)_minmax(0,1fr)] lg:grid-rows-1"
        style={layoutStyle}
      >
        <DesignLeftPanel
          activeTab={leftPanelTab}
          agent={agent}
          agentEnabled={config.agentEnabled}
          dagFiles={dagFiles}
          dagSearch={dagSearch}
          newDagName={newDagName}
          newNameError={newNameValidation?.error}
          selectedDagFile={selectedDagFile}
          onDagSearchChange={setDagSearch}
          onNewDagNameChange={setNewDagName}
          onSelectDag={handleSelectDag}
          onTabChange={setLeftPanelTab}
        />

        <DesignResizeHandle
          className="hidden lg:flex"
          isDragging={draggingSide === 'left'}
          label="Resize workflow list panel"
          max={DESIGN_PANEL_LIMITS.left.maxWidth}
          min={DESIGN_PANEL_LIMITS.left.minWidth}
          onKeyDown={(event) => handleSeparatorKeyDown(event, 'left')}
          onMouseDown={startResize('left')}
          value={getSeparatorValue('left')}
        />

        <div className="flex min-h-0 flex-col overflow-hidden">
          <DesignToolbar
            dagFiles={dagFiles}
            canSave={canSave}
            hasExistingUnsavedChanges={hasExistingUnsavedChanges}
            isSaving={isSaving}
            selectedDagFile={selectedDagFile}
            selectedStepName={selectedStepName}
            steps={steps}
            newDagName={newDagName}
            newNameError={newNameValidation?.error}
            onDiscardChanges={discardChanges}
            onSelectDag={handleSelectDag}
            onSelectStep={handleSelectStep}
            onNewDagNameChange={setNewDagName}
            onRefresh={() => selectedDagFile && mutateSpec()}
            onSave={handleSave}
            isRefreshing={isSpecLoading}
          />

          <div
            ref={contentRef}
            className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden xl:grid-cols-[minmax(0,1fr)_var(--design-resize-handle-width)_var(--design-right-panel-width)]"
          >
            <div className="min-h-0 overflow-auto p-4">
              <div className="space-y-4">
                <ValidationSummary
                  validation={validation}
                  isValidating={isValidating}
                />
                <section className="overflow-hidden rounded-lg border border-border bg-surface">
                  {validation?.dag?.steps?.length && !hasValidationErrors ? (
                    <Graph
                      steps={validation.dag.steps}
                      type="config"
                      flowchart={flowchart}
                      onChangeFlowchart={handleFlowchartChange}
                      onClickNode={handleGraphNodeSelect}
                      selectOnClick
                      showIcons={false}
                      height={360}
                    />
                  ) : (
                    <EmptyPreview
                      hasErrors={hasValidationErrors}
                      isLoading={isValidating}
                    />
                  )}
                </section>

                {validation?.dag && (
                  <section className="space-y-4 rounded-lg border border-border bg-background p-4">
                    <DAGAttributes dag={validation.dag} />
                    {validation.dag.steps?.length ? (
                      <DAGStepTable steps={validation.dag.steps} />
                    ) : null}
                  </section>
                )}

                <section className="min-h-[420px]">
                  <DAGEditorWithDocs
                    value={editorValue}
                    onChange={handleEditorChange}
                    readOnly={!canWriteInSelectedScope}
                    className="h-[56vh] min-h-[420px]"
                    modelUri={editorModelUri}
                    schema={editorSchema}
                    headerActions={
                      <div className="flex items-center gap-2">
                        <Badge variant="outline">
                          <FileCode className="mr-1 h-3 w-3" />
                          YAML
                        </Badge>
                        {selectedDagFile && isSpecLoading && (
                          <Badge variant="secondary">
                            <RefreshCw className="mr-1 h-3 w-3 animate-spin" />
                            Loading
                          </Badge>
                        )}
                        {hasUnsavedChanges && (
                          <Badge variant="secondary">Unsaved</Badge>
                        )}
                      </div>
                    }
                  />
                </section>
              </div>
            </div>

            <DesignResizeHandle
              className="hidden xl:flex"
              isDragging={draggingSide === 'right'}
              label="Resize step details panel"
              max={DESIGN_PANEL_LIMITS.right.maxWidth}
              min={DESIGN_PANEL_LIMITS.right.minWidth}
              onKeyDown={(event) => handleSeparatorKeyDown(event, 'right')}
              onMouseDown={startResize('right')}
              value={getSeparatorValue('right')}
            />

            <StepChangePanel
              agentEnabled={config.agentEnabled}
              canSubmit={canSubmitDesignRequest}
              dag={validation?.dag}
              isSending={isSendingDesignRequest}
              requestText={requestText}
              selectedStepName={selectedStepName}
              step={selectedStep}
              onRequestTextChange={setRequestText}
              onSend={handleSendDesignRequest}
            />
          </div>
        </div>
      </div>

      <ExternalChangeDialog
        visible={conflict.hasConflict}
        onDiscard={() => resolveConflict('discard')}
        onIgnore={() => resolveConflict('ignore')}
      />
    </div>
  );
}

function useDesignPanelLayout() {
  const shellRef = React.useRef<HTMLDivElement>(null);
  const contentRef = React.useRef<HTMLDivElement>(null);
  const dragStateRef = React.useRef<{
    side: DesignResizeSide;
    startX: number;
    startWidth: number;
  } | null>(null);

  const [leftPanelWidth, setLeftPanelWidth] = React.useState(() =>
    readStoredPanelWidth(
      DESIGN_LEFT_PANEL_STORAGE_KEY,
      DESIGN_PANEL_LIMITS.left
    )
  );
  const [rightPanelWidth, setRightPanelWidth] = React.useState(() =>
    readStoredPanelWidth(
      DESIGN_RIGHT_PANEL_STORAGE_KEY,
      DESIGN_PANEL_LIMITS.right
    )
  );
  const [draggingSide, setDraggingSide] =
    React.useState<DesignResizeSide | null>(null);

  const clampLeftPanelWidth = React.useCallback((width: number) => {
    const containerWidth = shellRef.current?.getBoundingClientRect().width;
    const maxWidthFromLayout =
      containerWidth == null
        ? DESIGN_PANEL_LIMITS.left.maxWidth
        : containerWidth -
          DESIGN_RESIZE_HANDLE_WIDTH -
          DESIGN_PANEL_LIMITS.minMainWidth;

    return clamp(
      width,
      DESIGN_PANEL_LIMITS.left.minWidth,
      Math.max(
        DESIGN_PANEL_LIMITS.left.minWidth,
        Math.min(DESIGN_PANEL_LIMITS.left.maxWidth, maxWidthFromLayout)
      )
    );
  }, []);

  const clampRightPanelWidth = React.useCallback((width: number) => {
    const containerWidth = contentRef.current?.getBoundingClientRect().width;
    const maxWidthFromLayout =
      containerWidth == null
        ? DESIGN_PANEL_LIMITS.right.maxWidth
        : containerWidth -
          DESIGN_RESIZE_HANDLE_WIDTH -
          DESIGN_PANEL_LIMITS.minWorkspaceWidth;

    return clamp(
      width,
      DESIGN_PANEL_LIMITS.right.minWidth,
      Math.max(
        DESIGN_PANEL_LIMITS.right.minWidth,
        Math.min(DESIGN_PANEL_LIMITS.right.maxWidth, maxWidthFromLayout)
      )
    );
  }, []);

  React.useEffect(() => {
    const clampPanelWidthsToLayout = () => {
      setLeftPanelWidth((width) => clampLeftPanelWidth(width));
      setRightPanelWidth((width) => clampRightPanelWidth(width));
    };

    clampPanelWidthsToLayout();
    window.addEventListener('resize', clampPanelWidthsToLayout);

    return () => {
      window.removeEventListener('resize', clampPanelWidthsToLayout);
    };
  }, [clampLeftPanelWidth, clampRightPanelWidth]);

  const setPanelWidth = React.useCallback(
    (side: DesignResizeSide, nextWidth: number) => {
      if (side === 'left') {
        setLeftPanelWidth(clampLeftPanelWidth(nextWidth));
        return;
      }
      setRightPanelWidth(clampRightPanelWidth(nextWidth));
    },
    [clampLeftPanelWidth, clampRightPanelWidth]
  );

  React.useEffect(() => {
    writeStoredPanelWidth(DESIGN_LEFT_PANEL_STORAGE_KEY, leftPanelWidth);
  }, [leftPanelWidth]);

  React.useEffect(() => {
    writeStoredPanelWidth(DESIGN_RIGHT_PANEL_STORAGE_KEY, rightPanelWidth);
  }, [rightPanelWidth]);

  const handleMouseMove = React.useCallback(
    (event: MouseEvent) => {
      const dragState = dragStateRef.current;
      if (!dragState) return;

      const delta = event.clientX - dragState.startX;
      const nextWidth =
        dragState.side === 'left'
          ? dragState.startWidth + delta
          : dragState.startWidth - delta;
      setPanelWidth(dragState.side, nextWidth);
    },
    [setPanelWidth]
  );

  const stopResize = React.useCallback(() => {
    dragStateRef.current = null;
    setDraggingSide(null);
  }, []);

  React.useEffect(() => {
    if (!draggingSide) return;

    const originalCursor = document.body.style.cursor;
    const originalUserSelect = document.body.style.userSelect;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', stopResize);

    return () => {
      document.body.style.cursor = originalCursor;
      document.body.style.userSelect = originalUserSelect;
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', stopResize);
    };
  }, [draggingSide, handleMouseMove, stopResize]);

  const startResize = React.useCallback(
    (side: DesignResizeSide) => (event: React.MouseEvent<HTMLDivElement>) => {
      event.preventDefault();
      dragStateRef.current = {
        side,
        startX: event.clientX,
        startWidth: side === 'left' ? leftPanelWidth : rightPanelWidth,
      };
      setDraggingSide(side);
    },
    [leftPanelWidth, rightPanelWidth]
  );

  const handleSeparatorKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>, side: DesignResizeSide) => {
      const resizeStep = event.shiftKey ? 40 : 16;
      const currentWidth = side === 'left' ? leftPanelWidth : rightPanelWidth;
      let nextWidth: number | null = null;

      if (event.key === 'ArrowLeft') {
        nextWidth =
          side === 'left'
            ? currentWidth - resizeStep
            : currentWidth + resizeStep;
      } else if (event.key === 'ArrowRight') {
        nextWidth =
          side === 'left'
            ? currentWidth + resizeStep
            : currentWidth - resizeStep;
      } else if (event.key === 'Home') {
        nextWidth = DESIGN_PANEL_LIMITS[side].minWidth;
      } else if (event.key === 'End') {
        nextWidth = DESIGN_PANEL_LIMITS[side].maxWidth;
      }

      if (nextWidth == null) return;
      event.preventDefault();
      setPanelWidth(side, nextWidth);
    },
    [leftPanelWidth, rightPanelWidth, setPanelWidth]
  );

  const getSeparatorValue = React.useCallback(
    (side: DesignResizeSide) =>
      side === 'left' ? leftPanelWidth : rightPanelWidth,
    [leftPanelWidth, rightPanelWidth]
  );

  const layoutStyle = React.useMemo(
    () =>
      ({
        '--design-left-panel-width': `${leftPanelWidth}px`,
        '--design-right-panel-width': `${rightPanelWidth}px`,
        '--design-resize-handle-width': `${DESIGN_RESIZE_HANDLE_WIDTH}px`,
      }) as React.CSSProperties,
    [leftPanelWidth, rightPanelWidth]
  );

  return {
    contentRef,
    draggingSide,
    getSeparatorValue,
    handleSeparatorKeyDown,
    layoutStyle,
    shellRef,
    startResize,
  };
}

function readStoredPanelWidth(
  storageKey: string,
  limits: {
    defaultWidth: number;
    minWidth: number;
    maxWidth: number;
  }
) {
  try {
    const saved = localStorage.getItem(storageKey);
    const parsed = saved == null ? NaN : Number(saved);
    if (!Number.isNaN(parsed)) {
      return clamp(parsed, limits.minWidth, limits.maxWidth);
    }
  } catch {
    // Ignore storage access errors.
  }
  return limits.defaultWidth;
}

function writeStoredPanelWidth(storageKey: string, width: number) {
  try {
    localStorage.setItem(storageKey, String(width));
  } catch {
    // Ignore storage access errors.
  }
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function DesignResizeHandle({
  className,
  isDragging,
  label,
  max,
  min,
  onKeyDown,
  onMouseDown,
  value,
}: {
  className?: string;
  isDragging: boolean;
  label: string;
  max: number;
  min: number;
  onKeyDown: (event: React.KeyboardEvent<HTMLDivElement>) => void;
  onMouseDown: (event: React.MouseEvent<HTMLDivElement>) => void;
  value: number;
}) {
  return (
    <div
      role="separator"
      aria-label={label}
      aria-orientation="vertical"
      aria-valuemax={max}
      aria-valuemin={min}
      aria-valuenow={Math.round(value)}
      tabIndex={0}
      className={cn(
        'group z-30 h-full cursor-col-resize items-center justify-center bg-border/40 outline-none transition-colors duration-200 hover:bg-primary/50 focus-visible:bg-primary/50',
        isDragging && 'bg-primary',
        className
      )}
      onKeyDown={onKeyDown}
      onMouseDown={onMouseDown}
    >
      <div
        className={cn(
          'h-8 w-1 rounded-full bg-muted-foreground/30 transition-all duration-200 group-hover:bg-primary group-focus-visible:bg-primary',
          isDragging &&
            'scale-110 bg-primary shadow-[0_0_10px_var(--color-primary)]'
        )}
      />
    </div>
  );
}

type DesignLeftPanelProps = {
  activeTab: LeftPanelTab;
  agent: AgentChatController;
  agentEnabled: boolean;
  dagFiles: components['schemas']['DAGFile'][];
  dagSearch: string;
  newDagName: string;
  newNameError?: string;
  selectedDagFile: string;
  onDagSearchChange: (value: string) => void;
  onNewDagNameChange: (value: string) => void;
  onSelectDag: (value: string) => void;
  onTabChange: (value: LeftPanelTab) => void;
};

function DesignLeftPanel({
  activeTab,
  agent,
  agentEnabled,
  dagFiles,
  dagSearch,
  newDagName,
  newNameError,
  selectedDagFile,
  onDagSearchChange,
  onNewDagNameChange,
  onSelectDag,
  onTabChange,
}: DesignLeftPanelProps) {
  const normalizedSearch = dagSearch.trim().toLowerCase();
  const filteredDagFiles = React.useMemo(() => {
    if (!normalizedSearch) return dagFiles;
    return dagFiles.filter((item) => {
      const metadataLabels = item.dag.labels?.length
        ? item.dag.labels
        : (item.dag.tags ?? []);
      const searchableText = [
        item.fileName,
        item.dag.name,
        item.dag.group,
        ...metadataLabels,
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return searchableText.includes(normalizedSearch);
    });
  }, [dagFiles, normalizedSearch]);

  return (
    <div className="flex min-h-0 flex-col border-b border-border bg-card lg:border-b-0">
      <div className="border-b border-border">
        <Tabs className="flex w-full">
          <Tab
            className="flex-1 gap-2"
            isActive={activeTab === 'workflows'}
            onClick={() => onTabChange('workflows')}
          >
            <Network className="h-4 w-4" />
            Workflows
          </Tab>
          <Tab
            className="flex-1 gap-2"
            isActive={activeTab === 'agent'}
            onClick={() => onTabChange('agent')}
          >
            <MessageSquare className="h-4 w-4" />
            Agent
          </Tab>
        </Tabs>
      </div>

      {activeTab === 'workflows' ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="border-b border-border p-4">
            <div className="mb-2 flex items-center justify-between gap-3">
              <h2 className="text-sm font-semibold">Open Workflow</h2>
              <Badge variant="outline">{dagFiles.length}</Badge>
            </div>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={dagSearch}
                onChange={(event) => onDagSearchChange(event.target.value)}
                placeholder="Search workflows"
                className="pl-9"
              />
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-auto p-2">
            {filteredDagFiles.length ? (
              <div className="space-y-1">
                {filteredDagFiles.map((item) => {
                  const isSelected = item.fileName === selectedDagFile;
                  return (
                    <button
                      key={item.fileName}
                      type="button"
                      onClick={() => onSelectDag(item.fileName)}
                      className={cn(
                        'flex w-full min-w-0 flex-col gap-2 rounded-md border px-3 py-2 text-left transition-colors',
                        isSelected
                          ? 'border-primary bg-primary/8'
                          : 'border-transparent hover:border-border hover:bg-muted'
                      )}
                    >
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">
                          {item.dag.name || item.fileName}
                        </div>
                        <div className="truncate text-xs text-muted-foreground">
                          {item.fileName}
                        </div>
                      </div>
                      <div className="flex min-w-0 flex-wrap gap-1">
                        {item.dag.group && (
                          <Badge variant="secondary">{item.dag.group}</Badge>
                        )}
                        {item.suspended && (
                          <Badge variant="outline">Suspended</Badge>
                        )}
                        {!!item.errors?.length && (
                          <Badge variant="outline">
                            {item.errors.length} errors
                          </Badge>
                        )}
                      </div>
                    </button>
                  );
                })}
              </div>
            ) : (
              <div className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">
                No workflows found
              </div>
            )}
          </div>

          <div className="border-t border-border p-4">
            <div className="mb-2 flex items-center justify-between gap-3">
              <h2 className="text-sm font-semibold">New Workflow</h2>
              {!selectedDagFile && <Badge variant="secondary">Draft</Badge>}
            </div>
            <Input
              value={newDagName}
              onChange={(event) => onNewDagNameChange(event.target.value)}
              disabled={!!selectedDagFile}
              placeholder="daily-report"
            />
            {!selectedDagFile && newNameError && (
              <p className="mt-1 text-xs text-destructive">{newNameError}</p>
            )}
            <Button
              className="mt-3 w-full"
              variant={selectedDagFile ? 'outline' : 'primary'}
              onClick={() => onSelectDag(NEW_DAG_VALUE)}
            >
              <Plus className="h-4 w-4" />
              Start New Workflow
            </Button>
          </div>
        </div>
      ) : agentEnabled ? (
        <AgentChatPanelView
          active
          controller={agent}
          className="h-full"
          defaultSidebarOpen={false}
          placeholder="Describe the workflow change..."
          rememberSidebarState={false}
        />
      ) : (
        <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
          <Sparkles className="h-8 w-8 text-muted-foreground" />
          <div>
            <h2 className="text-base font-semibold">Agent is disabled</h2>
            <p className="mt-1 max-w-sm text-sm text-muted-foreground">
              Enable the agent to use chat-driven workflow authoring.
            </p>
          </div>
        </div>
      )}
    </div>
  );
}

type DesignToolbarProps = {
  dagFiles: components['schemas']['DAGFile'][];
  canSave: boolean;
  hasExistingUnsavedChanges: boolean;
  isSaving: boolean;
  selectedDagFile: string;
  selectedStepName: string;
  steps: Step[];
  newDagName: string;
  newNameError?: string;
  onDiscardChanges: () => void;
  onSelectDag: (value: string) => void;
  onSelectStep: (value: string) => void;
  onNewDagNameChange: (value: string) => void;
  onRefresh: () => void;
  onSave: () => void;
  isRefreshing: boolean;
};

function DesignToolbar({
  dagFiles,
  canSave,
  hasExistingUnsavedChanges,
  isSaving,
  selectedDagFile,
  selectedStepName,
  steps,
  newDagName,
  newNameError,
  onDiscardChanges,
  onSelectDag,
  onSelectStep,
  onNewDagNameChange,
  onRefresh,
  onSave,
  isRefreshing,
}: DesignToolbarProps) {
  const selectedValue = selectedDagFile || NEW_DAG_VALUE;
  const exitPath = selectedDagFile
    ? `/dags/${encodeURIComponent(selectedDagFile)}/spec`
    : '/dags';
  const hasSelectedDagInList = dagFiles.some(
    (item) => item.fileName === selectedDagFile
  );

  return (
    <div className="flex flex-col gap-3 border-b border-border bg-card px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <GitBranch className="h-5 w-5 text-primary" />
          <div className="min-w-0">
            <h1 className="truncate text-lg font-semibold">Design</h1>
            <p className="truncate text-xs text-muted-foreground">
              Agentic workflow authoring and DAG preview
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            onClick={onSave}
            disabled={isSaving || !canSave}
            variant="outline"
          >
            <Save className="h-4 w-4" />
            {selectedDagFile ? 'Save YAML' : 'Create DAG'}
          </Button>
          {selectedDagFile && hasExistingUnsavedChanges && (
            <Button variant="ghost" onClick={onDiscardChanges}>
              Discard YAML
            </Button>
          )}
          <Button asChild>
            <Link to={exitPath}>
              <ArrowLeft className="h-4 w-4" />
              Exit Design
            </Link>
          </Button>
          <Button
            variant="ghost"
            onClick={onRefresh}
            disabled={!selectedDagFile || isRefreshing}
            title="Refresh selected DAG"
          >
            <RefreshCw
              className={cn('h-4 w-4', isRefreshing && 'animate-spin')}
            />
            Refresh
          </Button>
        </div>
      </div>

      <div className="grid gap-3 xl:grid-cols-[minmax(220px,0.8fr)_minmax(180px,0.6fr)_minmax(220px,1fr)]">
        <div className="min-w-0">
          <Label>Target DAG</Label>
          <Select value={selectedValue} onValueChange={onSelectDag}>
            <SelectTrigger className="mt-1 h-7 px-2 py-1">
              <SelectValue placeholder="Select DAG" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NEW_DAG_VALUE}>New DAG</SelectItem>
              {selectedDagFile && !hasSelectedDagInList && (
                <SelectItem value={selectedDagFile}>
                  {selectedDagFile}
                </SelectItem>
              )}
              {dagFiles.map((item) => (
                <SelectItem key={item.fileName} value={item.fileName}>
                  {item.fileName}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="min-w-0">
          <Label>Target Step</Label>
          <Select
            value={selectedStepName || NEW_DAG_VALUE}
            onValueChange={onSelectStep}
            disabled={!steps.length}
          >
            <SelectTrigger className="mt-1 h-7 px-2 py-1">
              <SelectValue placeholder="Select step" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NEW_DAG_VALUE}>Whole DAG</SelectItem>
              {steps.map((step) => (
                <SelectItem key={step.name} value={step.name}>
                  {step.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="min-w-0">
          <Label htmlFor="new-dag-name">New DAG Name</Label>
          <Input
            id="new-dag-name"
            className="mt-1 h-7 px-2 py-1"
            value={newDagName}
            onChange={(event) => onNewDagNameChange(event.target.value)}
            disabled={!!selectedDagFile}
            placeholder="daily-report"
          />
          {!selectedDagFile && newNameError && (
            <p className="mt-1 text-xs text-destructive">{newNameError}</p>
          )}
        </div>
      </div>
    </div>
  );
}

function ValidationSummary({
  validation,
  isValidating,
}: {
  validation: ValidationState | null;
  isValidating: boolean;
}) {
  if (isValidating) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <RefreshCw className="h-4 w-4 animate-spin" />
        Validating DAG spec...
      </div>
    );
  }

  if (!validation) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <AlertTriangle className="h-4 w-4" />
        Add a DAG spec to preview validation.
      </div>
    );
  }

  if (validation.valid) {
    return (
      <div className="flex flex-wrap items-center gap-2 text-sm text-success">
        <CheckCircle2 className="h-4 w-4" />
        Valid DAG
        {validation.dag?.name && (
          <Badge variant="outline">{validation.dag.name}</Badge>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-sm text-destructive">
        <XCircle className="h-4 w-4" />
        Validation failed
      </div>
      <div className="space-y-2">
        {validation.errors.map((error, index) => (
          <div
            key={`${error}-${index}`}
            className="flex items-start gap-2 rounded-md bg-danger-highlight p-2 text-xs text-danger"
          >
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
            <span className="break-words font-mono">{error}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function EmptyPreview({
  hasErrors,
  isLoading,
}: {
  hasErrors: boolean;
  isLoading: boolean;
}) {
  return (
    <div className="flex h-[360px] flex-col items-center justify-center gap-3 p-6 text-center text-muted-foreground">
      {isLoading ? (
        <RefreshCw className="h-8 w-8 animate-spin" />
      ) : (
        <AlertTriangle className="h-8 w-8" />
      )}
      <p className="max-w-sm text-sm">
        {hasErrors
          ? 'Fix validation errors to render the workflow graph.'
          : 'Valid steps will render here as a workflow graph.'}
      </p>
    </div>
  );
}

function StepChangePanel({
  agentEnabled,
  canSubmit,
  dag,
  isSending,
  onRequestTextChange,
  onSend,
  requestText,
  selectedStepName,
  step,
}: {
  agentEnabled: boolean;
  canSubmit: boolean;
  dag?: DAGDetails;
  isSending: boolean;
  requestText: string;
  selectedStepName: string;
  step?: Step;
  onRequestTextChange: (value: string) => void;
  onSend: () => void;
}) {
  const title = step
    ? step.name
    : selectedStepName
      ? selectedStepName
      : 'Whole Workflow';
  const subtitle = step
    ? 'Selected graph node'
    : selectedStepName
      ? 'Selected step is not available in the current preview'
      : 'Click a node in the graph to target a step';

  return (
    <aside className="flex min-h-[420px] flex-col border-t border-border bg-card xl:min-h-0 xl:border-t-0">
      <div className="border-b border-border px-4 py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold">{title}</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">{subtitle}</p>
          </div>
          <Badge variant={step ? 'primary' : 'outline'}>
            {step ? 'Step' : 'DAG'}
          </Badge>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-4">
        {step ? (
          <StepDetails step={step} />
        ) : (
          <div className="space-y-4 text-sm text-muted-foreground">
            <StepFact label="DAG" value={dag?.name || 'No DAG selected'} />
            <StepFact
              label="Steps"
              value={dag?.steps?.length ? `${dag.steps.length}` : 'None'}
            />
            <StepFact
              label="Schedule"
              value={dag?.schedule?.join(', ') || 'Manual'}
            />
          </div>
        )}
      </div>

      <div className="border-t border-border p-4">
        <Label htmlFor="selected-step-change-request">Change Request</Label>
        <Textarea
          id="selected-step-change-request"
          value={requestText}
          onChange={(event) => onRequestTextChange(event.target.value)}
          placeholder={
            step
              ? `Describe what to change in ${step.name}...`
              : 'Describe what to change in the workflow...'
          }
          className="mt-2 min-h-32 resize-y bg-background"
        />
        <Button
          onClick={onSend}
          disabled={!canSubmit}
          variant="primary"
          className="mt-3 w-full"
        >
          <Send className="h-4 w-4" />
          {isSending ? 'Sending...' : 'Send to Agent'}
        </Button>
        {!agentEnabled && (
          <p className="mt-2 text-xs text-muted-foreground">
            Enable the agent to send workflow change requests.
          </p>
        )}
      </div>
    </aside>
  );
}

function StepFact({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs font-medium uppercase text-muted-foreground">
        {label}
      </div>
      <div
        className={cn(
          'mt-1 whitespace-pre-wrap break-words text-sm text-foreground',
          mono && 'font-mono text-xs'
        )}
      >
        {value}
      </div>
    </div>
  );
}

export default WorkflowDesignPage;
