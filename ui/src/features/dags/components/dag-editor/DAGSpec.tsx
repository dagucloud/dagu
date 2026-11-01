// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGSpec component displays and allows editing of a DAG specification.
 *
 * @module features/dags/components/dag-editor
 */
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { useRightPanelClaim } from '@/contexts/RightPanelContext';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { StepDetailsDrawer } from '@/features/dags/components/step-details';
import {
  panelShellClass,
  usePanelPlacement,
  PanelHeader,
  panelWidth,
  useViewportWidth,
} from '@/features/dags/components/step-details/PanelShell';
import { getStoredDrawerWidth } from '@/features/dags/components/step-details/StepSideDrawer';
import { cn } from '@/lib/utils';
import { workspaceNameFromLabels } from '@/lib/workspace';
import { AlertTriangle, FileCode, Save } from 'lucide-react';
import React, { useEffect } from 'react';
import { flushSync } from 'react-dom';
import type { KeyedMutator } from 'swr';
import type { components, paths } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { DialogLayerProvider } from '@/components/ui/dialog';
import { Spinner } from '@/components/ui/spinner';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useSchema } from '../../../../contexts/SchemaContext';
import { useUnsavedChanges } from '../../../../contexts/UnsavedChangesContext';
import { useClient, useQuery } from '../../../../hooks/api';
import { useContentEditor } from '../../../../hooks/useContentEditor';
import { useDebouncedValue } from '../../../../hooks/useDebouncedValue';
import { useDAGSSE } from '../../../../hooks/useDAGSSE';
import type { SSEState } from '../../../../hooks/useSSE';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../../../hooks/useSSECacheSync';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGContext } from '../../contexts/DAGContext';
import { ValueReferenceNoticesButton } from '../value-reference-notices';
import {
  buildAugmentedDAGSchema,
  customActionHintsEqual,
  type EditorCustomActionHint,
  type EditorLegacyDefinitionHint,
  extractLocalCustomDefinitionHints,
  harnessNamesEqual,
  legacyDefinitionHintsEqual,
  mergeCustomActionHints,
  mergeLegacyDefinitionHints,
  toInheritedCustomActionHints,
  toInheritedLegacyDefinitionHints,
} from './customActionSchema';
import type { QuickFixSource } from './quickFixProvider';
import { DocumentSwitcher } from './DocumentSwitcher';
import { buildIssueMarkers } from './issueMarkers';
import { issueRows } from './issueRows';
import { IssuesPopover } from './IssuesPopover';
import { DraftRecoveryCard } from './DraftRecoveryCard';
import { NoticeLine, type NoticeLineItem } from './NoticeLine';
import { YamlTools } from './YamlTools';
import { CountBadge } from '@/features/visual-editor/canvas/CountBadge';
import { attributeIssues } from '@/features/visual-editor/doc/issues';
import { applyTextEdits } from '@/features/visual-editor/doc/patch';
import {
  formatStepRange,
  formatYaml,
  isFormattable,
  isSameYaml,
  minimalEdit,
} from '@/features/visual-editor/doc/format';
import type { QuickFix } from '@/features/visual-editor/doc/quickFixes';
import {
  parseDagDoc,
  parseDagDocLenient,
  resolveStepNames,
} from '@/features/visual-editor/doc/parse';
import {
  plainIssueMessage,
  refineIssue,
  relativeFieldPath,
  requiredFieldOf,
} from '@/features/visual-editor/inspector/issues';
import type { ReferenceEditorState } from '@/features/visual-editor/references/editorState';
import ExternalChangeDialog from './ExternalChangeDialog';
import { useStepViewForm } from './stepViewMode';
import { preloadDAGEditor } from './loadDAGEditor';
import { ReadOnlyMarker, YamlFileBody, YamlFilePanel } from './YamlFilePanel';
import { useYamlSection } from './useYamlSection';
import { WorkflowPanel } from './WorkflowPanel';
import type {
  CodeEditorLike,
  VisualEditorValidation,
} from '@/features/visual-editor';
import type { EditResult, TextEdit } from '@/features/visual-editor/contracts';
import { frozenMessage } from '@/features/visual-editor/editMessages';
import {
  InspectorViewProvider,
  useInspectorViewState,
} from '@/features/visual-editor/inspector/InspectorViewContext';
import { useDraftRecovery } from '@/features/visual-editor/run/useDraftRecovery';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import type {
  DAGSpecHostProps,
  EditorNotice,
  SaveState,
  VisualEditorHandle,
  VisualEditorState,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
} from './hostTypes';

export type {
  DAGSpecHostProps,
  SaveState,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
  WorkspaceNotice,
} from './hostTypes';

// The visual editor and its canvas library load only when the view opens.
const VisualEditor = React.lazy(() => import('@/features/visual-editor'));
const DetailsCanvas = React.lazy(
  () => import('@/features/visual-editor/DetailsCanvas')
);
type ValidationIssue = components['schemas']['ValidationIssue'];
// The second pass of the validation loads with the editor, not before.
type ClientIssuesModule =
  typeof import('@/features/visual-editor/doc/clientIssues');
const SaveDiffDialog = React.lazy(() =>
  import('@/features/visual-editor/run/SaveDiffDialog').then((module) => ({
    default: module.SaveDiffDialog,
  }))
);

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
  /**
   * False turns off the document Ctrl/Cmd+S shortcut, for a host that keeps
   * the editor mounted while it is hidden. Default true.
   */
  keyboardActive?: boolean;
} & DAGSpecHostProps;

/** The visual editor state until the editor reports one. */
const DEFAULT_VISUAL_STATE: VisualEditorState = {
  frozen: false,
  canUndo: false,
  canRedo: false,
  selectedStepName: null,
  drawer: 'none',
  direction: 'TB',
};

/** What the handle returns for an edit while no visual editor shows. */
const NO_VISUAL_EDITOR: EditResult = {
  ok: false,
  code: 'noop',
  message: 'The visual editor is not open.',
};

// The notices of the editor rank above every host notice; a frozen canvas
// does not show the text that is edited, so it ranks first.
const NOTICE_PRIORITY = {
  frozen: 1_000_000,
  editRefused: 900_000,
} as const;

/** The lines that differ between two texts, after the common start and end. */
function changedLineCount(before: string, after: string): number {
  const a = before.split('\n');
  const b = after.split('\n');
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++;
  }
  return Math.max(a.length - head - tail, b.length - head - tail);
}

/**
 * Sends the text of the focused field, which a field of the step form sends
 * only on blur, and gives the field its focus back.
 */
function commitFocusedField(): void {
  const field = document.activeElement;
  if (!(field instanceof HTMLElement) || field === document.body) return;
  flushSync(() => field.blur());
  field.focus({ preventScroll: true });
}

// The last text that draftStepTitle parsed: a render parses it once.
type ApiStepList = NonNullable<components['schemas']['DAGDetails']['steps']>;
let parsedDraft: {
  text: string;
  model: ReturnType<typeof parseDagDoc>;
} | null = null;

/**
 * The name that the draft gives the step with this server name. The DAG of
 * the server is one check behind a rename. Undefined when the draft and the
 * DAG do not line up.
 */
function draftStepTitle(
  text: string,
  steps: ApiStepList | undefined,
  name: string
): string | undefined {
  if (text === '' || !steps) return undefined;
  const index = steps.findIndex((step) => step.name === name);
  if (index < 0) return undefined;
  if (parsedDraft?.text !== text) {
    parsedDraft = { text, model: parseDagDoc(text) };
  }
  const { model } = parsedDraft;
  return model.steps.length === steps.length
    ? model.steps[index]?.explicitName
    : undefined;
}

/**
 * DAGSpec displays and allows editing of a DAG specification
 * including visualization, attributes, steps, and YAML definition
 */
function DAGSpec({
  fileName,
  localDags,
  editorHints,
  keyboardActive = true,
  fillHeight,
  ndv,
  notices,
  editorHandleRef,
  onEditorStateChange,
  onStateChange,
  handleRef,
  runSource,
  onRunStarted,
  selectedStepName,
  onSelectedStepNameChange,
  onStepClick,
  openNdvOn,
  renderStepRun,
  renderStepOutput,
  inputColumnRoot,
  outputColumnRoot,
  onStepCommand,
  stepCommandGates,
  followedRuns,
  inputRun,
  chrome,
  onNotice,
  launcherHost,
  renderWorkflowPanel,
}: Props) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const client = useClient();
  const { schema: baseSchema } = useSchema();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const { setHasUnsavedChanges } = useUnsavedChanges();

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
      // The visual editor unmounts with the parent document.
      setEditorNotice(null);
      setSelectedSpecStepName(null);
      closeSpecStepDetails();
    },
    [closeSpecStepDetails]
  );

  // A renamed or removed sub-DAG gives the parent back.
  React.useEffect(() => {
    if (
      activeTab !== 'parent' &&
      !localDags?.some((localDag) => localDag.name === activeTab)
    ) {
      handleActiveTabChange('parent');
    }
  }, [activeTab, handleActiveTabChange, localDags]);

  // Reference to save function and refresh callback for keyboard shortcut
  const saveHandlerRef = React.useRef<(() => Promise<void>) | null>(null);
  const refreshCallbackRef = React.useRef<(() => void) | null>(null);

  // The editor polls until SpecLiveSync tells that the SSE feed is connected.
  const [poll, setPoll] = React.useState(true);

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
    sseFallbackOptions(poll ? FEED_DOWN : FEED_UP)
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
    // A blank line or a trailing space is not a change of the document.
    isSame: isSameYaml,
  });

  const { copy: copySpec } = useCopyFeedback();

  // Unsaved text stays in browser storage and is offered back after a reload.
  const { draft, discard: discardDraft } = useDraftRecovery({
    storageKey: `dagu:draft:${remoteNode}:${fileName}`,
    serverText: serverSpec ?? undefined,
    currentText: currentValue ?? undefined,
    isDirty: localHasUnsavedChanges,
    // Edits that a newer version waits to replace have no known base.
    baseSha256: conflict.hasConflict ? undefined : data?.sha256,
    isSame: isSameYaml,
  });
  // A draft of an older version is restored only when the user ignores the
  // newer one, which its save would replace.
  const [staleDraftOpen, setStaleDraftOpen] = React.useState(false);
  const restoreDraft = React.useCallback(() => {
    if (draft) {
      setCurrentValue(draft.text);
      discardDraft();
    }
  }, [discardDraft, draft, setCurrentValue]);

  // Hash of the newest server version, sent with a save so that the server
  // refuses to overwrite a file that changed on disk in the meantime. A live
  // event carries no hash: the last one stays until the fresh read, so that
  // a save in between is refused rather than unconditional.
  const baseShaRef = React.useRef<string | undefined>(undefined);
  React.useEffect(() => {
    if (data?.sha256 !== undefined) {
      baseShaRef.current = data.sha256;
    }
  }, [data?.sha256]);
  // A cache update without a hash (a live event) needs a fresh read.
  React.useEffect(() => {
    if (data?.spec != null && data.sha256 === undefined) {
      void mutateSpec();
    }
  }, [data?.spec, data?.sha256, mutateSpec]);

  // Live server-side validation of the edited buffer. Cleared whenever the
  // buffer stops being dirty (save or discard), which also clears the markers.
  // Kept while the next check is pending so the preview does not flip back to
  // the saved spec on every keystroke.
  const [liveValidation, setLiveValidation] = React.useState<{
    /** The text that was validated. */
    spec: string;
    errors: string[];
    warnings: string[];
    dag?: components['schemas']['DAGDetails'];
    issues: components['schemas']['ValidationIssue'][];
    referenceScope?: components['schemas']['ReferenceScope'];
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
              spec: currentValue,
              errors: result.errors ?? [],
              warnings: result.warnings ?? [],
              dag: result.dag,
              issues: result.issues ?? [],
              referenceScope: result.referenceScope,
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

  // The visual editor needs the scope and the inferred edges also for a clean
  // buffer, so the saved spec is validated once each time it changes. The
  // definition that the server builds from it also changes with an inherited
  // base configuration or another node; the last result shows until the new
  // one comes.
  const savedContext = React.useMemo(
    () => JSON.stringify([remoteNode, data?.dag ?? null]),
    [remoteNode, data?.dag]
  );
  const [savedValidation, setSavedValidation] = React.useState<{
    spec: string;
    context: string;
    result: VisualEditorValidation | null;
  } | null>(null);

  React.useEffect(() => {
    if (
      serverSpec == null ||
      (savedValidation?.spec === serverSpec &&
        savedValidation.context === savedContext)
    ) {
      return;
    }
    let cancelled = false;
    const done = (result: VisualEditorValidation | null) => {
      if (!cancelled) {
        setSavedValidation({ spec: serverSpec, context: savedContext, result });
      }
    };
    void client
      .POST('/dags/validate', {
        params: { query: { remoteNode } },
        body: { spec: serverSpec, name: fileName },
      })
      .then(({ data: result, error: requestError }) =>
        done(
          !requestError && result
            ? {
                dag: result.dag,
                issues: result.issues ?? [],
                referenceScope: result.referenceScope,
              }
            : null
        )
      )
      .catch(() => done(null));
    return () => {
      cancelled = true;
    };
  }, [
    client,
    fileName,
    remoteNode,
    savedContext,
    savedValidation?.context,
    savedValidation?.spec,
    serverSpec,
  ]);

  const savedResult =
    savedValidation?.spec === serverSpec ? savedValidation.result : null;
  const visualValidation = React.useMemo<VisualEditorValidation>(() => {
    if (!localHasUnsavedChanges || !liveValidation) {
      return savedResult
        ? {
            ...savedResult,
            issues: attributeIssues(
              parseDagDoc(serverSpec ?? ''),
              savedResult.issues
            ),
          }
        : { dag: data?.dag, issues: [] };
    }
    // A draft that does not build comes back without steps and without
    // reference data: the saved ones stay, so the views of the step keep
    // its outputs, its upstream steps and its neighbours while it is fixed.
    const unbuilt =
      !!liveValidation.dag &&
      !liveValidation.dag.steps?.length &&
      liveValidation.issues.some((issue) => issue.severity === 'error');
    return {
      dag: unbuilt
        ? (savedResult?.dag ?? data?.dag)
        : (liveValidation.dag ?? data?.dag),
      issues: attributeIssues(
        parseDagDoc(liveValidation.spec),
        liveValidation.issues
      ),
      referenceScope: unbuilt
        ? (savedResult?.referenceScope ?? liveValidation.referenceScope)
        : (liveValidation.referenceScope ?? savedResult?.referenceScope),
    };
  }, [
    data?.dag,
    liveValidation,
    localHasUnsavedChanges,
    savedResult,
    serverSpec,
  ]);

  // Read by the `${...}` completion of the YAML editor at each request.
  const referenceSource = React.useRef({
    text: '',
    state: {} as ReferenceEditorState,
  });
  const latestReference = React.useRef({
    text: '',
    scope: undefined as ReferenceEditorState['scope'],
  });
  latestReference.current = {
    text: currentValue ?? serverSpec ?? '',
    scope: visualValidation.referenceScope,
  };
  const referenceState = React.useCallback((): ReferenceEditorState => {
    const { text, scope } = latestReference.current;
    const cached = referenceSource.current;
    if (cached.text !== text || cached.state.scope !== scope) {
      referenceSource.current = {
        text,
        state: { scope, model: parseDagDoc(text) },
      };
    }
    return referenceSource.current.state;
  }, []);

  const [lastGoodLegacyDefinitions, setLastGoodLegacyDefinitions] =
    React.useState(
      () =>
        extractLocalCustomDefinitionHints(serverSpec ?? '').legacyDefinitions
    );
  const [lastGoodLocalActions, setLastGoodLocalActions] = React.useState(
    () => extractLocalCustomDefinitionHints(serverSpec ?? '').actions
  );
  const [lastGoodLocalHarnesses, setLastGoodLocalHarnesses] = React.useState(
    () => extractLocalCustomDefinitionHints(serverSpec ?? '').harnesses
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
    setLastGoodLocalHarnesses((previous) =>
      harnessNamesEqual(previous, parsedLocalDefinitions.harnesses)
        ? previous
        : parsedLocalDefinitions.harnesses
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
  const effectiveLocalHarnesses = React.useMemo(() => {
    if (!parsedLocalDefinitions.ok) {
      return lastGoodLocalHarnesses;
    }
    return harnessNamesEqual(
      lastGoodLocalHarnesses,
      parsedLocalDefinitions.harnesses
    )
      ? lastGoodLocalHarnesses
      : parsedLocalDefinitions.harnesses;
  }, [lastGoodLocalHarnesses, parsedLocalDefinitions]);

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
      mergeCustomActionHints(inheritedCustomActions, effectiveLocalActions),
      effectiveLocalHarnesses
    );
  }, [
    baseSchema,
    effectiveLocalActions,
    effectiveLocalHarnesses,
    effectiveLegacyDefinitions,
    inheritedCustomActions,
    inheritedLegacyDefinitions,
  ]);

  const visualCustomActions = React.useMemo(
    () => mergeCustomActionHints(inheritedCustomActions, effectiveLocalActions),
    [inheritedCustomActions, effectiveLocalActions]
  );

  // The server validation stops at the first error. A second pass reads the
  // text, never stops at the first error, and its issues join those of the
  // server in one list, without duplicates, sorted by line.
  const [clientIssues, setClientIssues] =
    React.useState<ClientIssuesModule | null>(null);
  React.useEffect(() => {
    let alive = true;
    import('@/features/visual-editor/doc/clientIssues')
      .then((module) => {
        if (alive) setClientIssues(module);
      })
      .catch((error: unknown) => {
        // The server issues stay; the second pass and its quick fixes are off.
        console.warn('Failed to load the client issue checks:', error);
      });
    return () => {
      alive = false;
    };
  }, []);
  const issueText = editable
    ? (currentValue ?? serverSpec ?? '')
    : (serverSpec ?? '');
  const debouncedText = useDebouncedValue(issueText, 150);
  // The text that a quick fix just wrote is checked at once, not 150 ms later.
  const [fixedText, setFixedText] = React.useState<string | null>(null);
  const checkedText =
    fixedText !== null && fixedText === issueText ? issueText : debouncedText;
  const textModel = React.useMemo(
    () => parseDagDocLenient(checkedText),
    [checkedText]
  );
  const textNames = React.useMemo(
    () => resolveStepNames(textModel, visualValidation.dag),
    [textModel, visualValidation.dag]
  );
  // A lens above each step of the whole file opens the step in the
  // full-screen step view. The steps follow the checked text.
  const lensSteps = React.useMemo(
    () =>
      textModel.steps.flatMap((step) => {
        const name = textNames.byKey[step.key] ?? step.explicitName;
        return name ? [{ line: step.range.startLine, name }] : [];
      }),
    [textModel, textNames]
  );
  const stepLensLabel = ts('Open step');
  const stepLenses = React.useMemo(
    () => ({
      steps: lensSteps,
      label: stepLensLabel,
      open: (name: string) => {
        visualHandleRef.current?.openYaml(name);
      },
    }),
    [lensSteps, stepLensLabel]
  );
  const allIssues = React.useMemo(
    () =>
      !clientIssues
        ? visualValidation.issues
        : clientIssues.mergeIssues(
            textModel,
            textNames,
            visualValidation.issues,
            clientIssues.collectClientIssues({
              model: textModel,
              schema: editorSchema,
              names: textNames,
              scope: visualValidation.referenceScope,
              customActions: visualCustomActions.map((hint) => hint.name),
            })
          ),
    [
      clientIssues,
      editorSchema,
      textModel,
      textNames,
      visualCustomActions,
      visualValidation.issues,
      visualValidation.referenceScope,
    ]
  );
  const shownValidation = React.useMemo<VisualEditorValidation>(
    () => ({ ...visualValidation, issues: allIssues }),
    [visualValidation, allIssues]
  );
  const lineOfIssue = React.useCallback(
    (issue: components['schemas']['ValidationIssue']): number | null =>
      clientIssues
        ? clientIssues.issueLine(textModel, textNames, issue)
        : (issue.line ?? null),
    [clientIssues, textModel, textNames]
  );

  // The quick fixes of the issues, planned together so that two fixes never
  // give the same id. They hold offsets of `textModel`.
  const quickFixes = React.useMemo(
    () =>
      editable && clientIssues
        ? clientIssues.planQuickFixes(textModel, allIssues)
        : new Map<ValidationIssue, QuickFix>(),
    [allIssues, clientIssues, editable, textModel]
  );
  const quickFixOf = React.useCallback(
    (issue: ValidationIssue) => quickFixes.get(issue),
    [quickFixes]
  );
  // Edits of the text go through the editor when it shows this text, between
  // two undo stops, so that one undo takes all of them back.
  const applyTextChange = React.useCallback(
    (text: string, edits: TextEdit[], source: string) => {
      const next = applyTextEdits(text, edits);
      const editor = yamlEditorRef.current;
      const model = editor?.getModel();
      if (editor && model && model.getValue() === text) {
        editor.pushUndoStop();
        editor.executeEdits(
          source,
          edits.map((edit) => {
            const start = model.getPositionAt(edit.from);
            const end = model.getPositionAt(edit.to);
            return {
              range: {
                startLineNumber: start.lineNumber,
                startColumn: start.column,
                endLineNumber: end.lineNumber,
                endColumn: end.column,
              },
              text: edit.insert,
            };
          })
        );
        editor.pushUndoStop();
      }
      setFixedText(next);
      setCurrentValue(next);
    },
    [setCurrentValue]
  );
  // The fixes are one edit, so that one undo takes all of them back. A text
  // that changed since the plan was made is not edited: the plan is stale.
  const applyQuickFixes = React.useCallback(
    (fixes: QuickFix[]) => {
      const text = currentValue ?? serverSpec ?? '';
      if (!clientIssues || text !== textModel.text) {
        return;
      }
      const edits = clientIssues.mergeQuickFixes(fixes);
      if (edits.length === 0) {
        return;
      }
      applyTextChange(text, edits, 'dagu-quick-fix');
    },
    [applyTextChange, clientIssues, currentValue, serverSpec, textModel.text]
  );
  // Every quick fix of the text, in one edit.
  const allFixes = React.useMemo(
    () => [...new Set(quickFixes.values())],
    [quickFixes]
  );
  const applyAllQuickFixes = React.useCallback(
    () => applyQuickFixes(allFixes),
    [applyQuickFixes, allFixes]
  );
  // The canonical form of the text, in one edit.
  const formatText = React.useCallback(() => {
    const text = currentValue ?? serverSpec ?? '';
    const edit = minimalEdit(text, formatYaml(text));
    if (!edit) {
      showToast(ts('The YAML is already formatted'), { variant: 'info' });
      return;
    }
    applyTextChange(text, [edit], 'dagu-format');
  }, [applyTextChange, currentValue, serverSpec, showToast, ts]);
  const canFormat = React.useMemo(
    () => isFormattable(currentValue ?? serverSpec ?? ''),
    [currentValue, serverSpec]
  );
  // The format button is off for a text that formatting would not change.
  // The check follows the text a little behind, so that typing stays light.
  const deferredText = React.useDeferredValue(currentValue ?? serverSpec ?? '');
  const textFormatted = React.useMemo(
    () =>
      isFormattable(deferredText) &&
      minimalEdit(deferredText, formatYaml(deferredText)) === null,
    [deferredText]
  );

  // The markers of the text: every issue on its line, saved or not.
  const markerErrors = liveValidation ? liveValidation.errors : data?.errors;
  const markers = React.useMemo(
    () =>
      buildIssueMarkers(
        checkedText,
        allIssues,
        markerErrors ?? [],
        visualValidation.dag
      ),
    [checkedText, allIssues, markerErrors, visualValidation.dag]
  );

  // The editor offers the quick fixes as code actions (hover, lightbulb,
  // Ctrl+.), from the same issues and the same plan as the wand. The source
  // reads the latest plan, so that it keeps its identity between renders.
  const quickFixPlanRef = React.useRef({
    model: textModel,
    issues: allIssues,
    names: textNames,
  });
  quickFixPlanRef.current = {
    model: textModel,
    issues: allIssues,
    names: textNames,
  };
  const quickFixSource = React.useMemo<QuickFixSource | undefined>(
    () =>
      editable && clientIssues
        ? {
            getPlan: () => quickFixPlanRef.current,
            // An edit by a code action is checked at once, as the wand's.
            onApplied: (text) => setFixedText(text),
          }
        : undefined,
    [editable, clientIssues]
  );

  // The Monaco editor of the YAML pane, for visual edits while it shows.
  const yamlEditorRef = React.useRef<CodeEditorLike | null>(null);
  const [, setEditorEpoch] = React.useState(0);
  const pendingRevealRef = React.useRef<(() => void) | null>(null);
  // What the YAML shows: the whole file (the YAML button, the panel) or one
  // step (its node view). With the ndv prop, the host owns whether it is open.
  const { yamlKind, setYamlKind, sectionOpen, setSectionOpen } =
    useYamlSection(ndv);
  // The step that the YAML shows alone: the schema docs follow it.
  const [yamlStepPath, setYamlStepPath] = React.useState<string[] | null>(null);
  // Where the visual editor puts its controls in the bar of the YAML drawer.
  const [yamlToolbarRoot, setYamlToolbarRoot] =
    React.useState<HTMLDivElement | null>(null);
  // The same drawer shows the form of a step in place of the text.
  const [drawerBodyRoot, setDrawerBodyRoot] =
    React.useState<HTMLDivElement | null>(null);
  // The INPUT and OUTPUT columns of the node details view.
  const [ndvInputRoot, setNdvInputRoot] = React.useState<HTMLDivElement | null>(
    null
  );
  const [ndvOutputRoot, setNdvOutputRoot] =
    React.useState<HTMLDivElement | null>(null);
  // The step that the visual editor selected, for the header of the view.
  const [visualStepName, setVisualStepName] = React.useState<string | null>(
    null
  );
  // The same drawer shows the form of a step in place of the text. The mode
  // of a step view is the last one of the session, and only the pane that is
  // shown writes it (a hidden editor under the run snapshot does not).
  const [drawerForm, setDrawerForm] = useStepViewForm(
    ndv?.state === 'expanded' &&
      (ndv.active ?? true) &&
      (selectedStepName ?? visualStepName) != null
  );

  const closeSection = React.useCallback(() => {
    setSectionOpen(false);
    setDrawerForm(false);
  }, [setSectionOpen]);
  // The view and the form of a step leave with the visual editor: another
  // document shows the YAML of the whole file.
  const switchDocument = React.useCallback(
    (tab: string) => {
      setDrawerForm(false);
      setYamlKind('file');
      setYamlStepPath(null);
      handleActiveTabChange(tab);
    },
    [handleActiveTabChange, setDrawerForm, setYamlKind]
  );
  // The node details view claims its own panel.
  useRightPanelClaim(ndv ? null : 'yaml', sectionOpen, closeSection);

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
      showError(ts('No changes to save'), ts('Make some edits before saving.'));
      return;
    }
    if (conflict.hasConflict) {
      return;
    }

    beginSave(currentValue);
    const expectedSha256 = baseShaRef.current;

    setIsSaving(true);
    try {
      const {
        data: responseData,
        error,
        response,
      } = await client.PUT('/dags/{fileName}/spec', {
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
          ...(expectedSha256 ? { expectedSha256 } : {}),
        },
      });

      if (response?.status === 409) {
        cancelSave();
        showError(
          ts('The file changed on disk'),
          ts('A newer version was saved. Review it before you save again.')
        );
        // The newer version comes in as an external change.
        void mutateSpec();
        return;
      }

      if (error) {
        cancelSave();
        showError(
          error.message || ts('Failed to save spec'),
          ts('Please check the YAML syntax and try again.')
        );
        return;
      }

      if (responseData?.errors?.length) {
        cancelSave();
        // Feed the rejected save into the same markers/panel as live validation.
        setLiveValidation((prev) => ({
          spec: currentValue,
          errors: responseData.errors,
          warnings: prev?.warnings ?? [],
          dag: prev?.dag,
          issues: prev?.issues ?? [],
          referenceScope: prev?.referenceScope,
        }));
        showError(
          ts('The spec was not saved'),
          undefined,
          ts('Validation errors'),
          responseData.errors.map(plainIssueMessage)
        );
        return;
      }

      // Mark as saved to prevent false conflict detection on our own save
      markAsSaved(currentValue);
      if (responseData?.sha256) {
        baseShaRef.current = responseData.sha256;
      }

      // Revalidate SWR cache from server as safety net
      mutateSpec();
    } catch {
      cancelSave();
      showError(ts('Failed to save spec'), ts('Please try again.'));
    } finally {
      setIsSaving(false);
    }
  }, [
    isSaving,
    currentValue,
    conflict.hasConflict,
    ts,
    fileName,
    remoteNode,
    client,
    showError,
    beginSave,
    cancelSave,
    markAsSaved,
    mutateSpec,
  ]);

  // A text with errors cannot be saved: the Issues button takes the place of
  // Save, and Ctrl+S opens the list of issues. Warnings do not block. Only
  // the toolbar that has the Issues popover swaps the buttons.
  const errorRowCount = React.useMemo(
    () =>
      issueRows({
        warnings: [],
        errors: (liveValidation ? liveValidation.errors : data?.errors) ?? [],
        issues: allIssues,
      }).errors.length,
    [allIssues, data?.errors, liveValidation]
  );
  const savingBlocked =
    !!chrome?.compactNotices &&
    editable &&
    activeTab === 'parent' &&
    errorRowCount > 0;
  const [issuesRequest, setIssuesRequest] = React.useState(0);

  // The editor hides the text, so a save from it shows the diff first.
  const [reviewOpen, setReviewOpen] = React.useState(false);
  // The server cannot read a text with a YAML syntax error: the save waits.
  const syntaxError = React.useMemo(
    () =>
      editable &&
      localHasUnsavedChanges &&
      currentValue != null &&
      !parseDagDoc(currentValue).ok,
    [currentValue, editable, localHasUnsavedChanges]
  );
  // More than a line of edits is not thrown away without a question.
  const [discardOpen, setDiscardOpen] = React.useState(false);
  const discardLines = discardOpen
    ? changedLineCount(serverSpec ?? '', currentValue ?? '')
    : 0;
  const requestDiscard = React.useCallback(() => {
    if (changedLineCount(serverSpec ?? '', currentValue ?? '') > 1) {
      setDiscardOpen(true);
    } else {
      discardChanges();
    }
  }, [currentValue, discardChanges, serverSpec]);
  const requestSave = React.useCallback(async () => {
    if (savingBlocked) {
      setIssuesRequest((count) => count + 1);
      return;
    }
    if (!localHasUnsavedChanges || isSaving) {
      return;
    }
    if (syntaxError) {
      showToast(ts('Fix the YAML syntax error before you save'), {
        variant: 'error',
      });
      return;
    }
    setReviewOpen(true);
  }, [
    isSaving,
    localHasUnsavedChanges,
    savingBlocked,
    showToast,
    syntaxError,
    ts,
  ]);

  // Update save handler ref when handleSave changes
  useEffect(() => {
    saveHandlerRef.current = requestSave;
  }, [requestSave]);

  // Add keyboard shortcut for saving (Ctrl+S / Cmd+S)
  useEffect(() => {
    if (!editable || !keyboardActive) {
      return;
    }

    const handleKeyDown = async (event: KeyboardEvent) => {
      // Check for Ctrl+S (Windows/Linux) or Cmd+S (macOS)
      if ((event.ctrlKey || event.metaKey) && event.key === 's') {
        event.preventDefault(); // Prevent browser's default save dialog
        commitFocusedField();

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
  }, [editable, keyboardActive]);

  // The Save button carries the state of the validation of the edited text.
  const saveState: SaveState = !localHasUnsavedChanges
    ? 'clean'
    : isValidating
      ? 'validating'
      : !liveValidation
        ? 'unchecked'
        : liveValidation.errors.length > 0
          ? 'errors'
          : liveValidation.warnings.length > 0
            ? 'warnings'
            : 'valid';

  // The visual editor reports its handle and its state to DAGSpec, which
  // passes them on to the host.
  const visualHandleRef = React.useRef<VisualEditorHandle | null>(null);
  // A panel that the host asks for before the lazy editor has mounted opens
  // when the editor binds its handle.
  const pendingPanelRef = React.useRef<
    ((visual: VisualEditorHandle) => void) | null
  >(null);
  // The last step that the host lights before then is lit when the editor
  // binds; a null request drops it.
  const pendingHighlightRef = React.useRef<string | null>(null);
  const setVisualHandle = React.useCallback(
    (handle: VisualEditorHandle | null) => {
      visualHandleRef.current = handle;
      if (handle && pendingPanelRef.current) {
        const open = pendingPanelRef.current;
        pendingPanelRef.current = null;
        open(handle);
      }
      if (handle && pendingHighlightRef.current !== null) {
        const name = pendingHighlightRef.current;
        pendingHighlightRef.current = null;
        handle.highlightStep(name);
      }
      if (typeof handleRef === 'function') {
        handleRef(handle);
      } else if (handleRef) {
        handleRef.current = handle;
      }
    },
    [handleRef]
  );

  const loaded = !isLoading && !!data?.dag;
  const editorFields = {
    loaded,
    editable,
    dirty: localHasUnsavedChanges,
    saving: isSaving,
    saveState,
    hasDraft: editable && !!draft,
    conflict: conflict.hasConflict,
  };
  const editorFieldsRef = React.useRef(editorFields);
  editorFieldsRef.current = editorFields;
  const onEditorStateChangeRef = React.useRef(onEditorStateChange);
  onEditorStateChangeRef.current = onEditorStateChange;
  const visualStateRef = React.useRef(DEFAULT_VISUAL_STATE);
  const emittedStateRef = React.useRef<WorkspaceEditorState | null>(null);
  // Calls onEditorStateChange only when a field changed.
  const emitEditorState = React.useCallback(() => {
    const listener = onEditorStateChangeRef.current;
    if (!listener) {
      return;
    }
    const next: WorkspaceEditorState = {
      ...visualStateRef.current,
      ...editorFieldsRef.current,
    };
    const previous = emittedStateRef.current;
    if (
      previous &&
      (Object.keys(next) as (keyof WorkspaceEditorState)[]).every(
        (key) => previous[key] === next[key]
      )
    ) {
      return;
    }
    emittedStateRef.current = next;
    listener(next);
  }, []);
  React.useEffect(() => {
    emitEditorState();
  }, [
    emitEditorState,
    loaded,
    editable,
    localHasUnsavedChanges,
    isSaving,
    saveState,
    draft,
    conflict.hasConflict,
    onEditorStateChange,
  ]);
  // With compact notices, the frozen canvas and the refused edit show in the
  // notice line. A host that listens to onNotice shows the refusal itself.
  const compactNotices = !!chrome?.compactNotices;
  const panelPlacement = usePanelPlacement();
  const viewport = useViewportWidth();
  const [visualFrozen, setVisualFrozen] = React.useState(false);
  const [unparsedLine, setUnparsedLine] = React.useState<number>();
  const [editorNotice, setEditorNotice] = React.useState<EditorNotice | null>(
    null
  );
  const handleVisualStateChange = React.useCallback(
    (state: VisualEditorState) => {
      visualStateRef.current = state;
      setVisualStepName(state.selectedStepName);
      setVisualFrozen(state.frozen);
      setUnparsedLine(state.unparsedLine);
      onStateChange?.(state);
      emitEditorState();
    },
    [emitEditorState, onStateChange]
  );

  // The handle reads the newest values, so that it keeps one identity.
  const handleSourceRef = React.useRef({
    requestSave,
    discardChanges,
    setCurrentValue,
    copySpec,
    editable,
    dirty: localHasUnsavedChanges,
    buffer: currentValue ?? serverSpec ?? '',
    dag: visualValidation.dag,
    sectionOpen,
    setSectionOpen,
    closeSection,
  });
  handleSourceRef.current = {
    requestSave,
    discardChanges,
    setCurrentValue,
    copySpec,
    editable,
    dirty: localHasUnsavedChanges,
    buffer: currentValue ?? serverSpec ?? '',
    dag: visualValidation.dag,
    sectionOpen,
    setSectionOpen,
    closeSection,
  };
  const editorHandle = React.useMemo<WorkspaceEditorHandle>(() => {
    const source = () => handleSourceRef.current;
    const visual = () => visualHandleRef.current;
    return {
      save: () => void source().requestSave(),
      discard: () => {
        if (source().editable) {
          source().discardChanges();
        }
      },
      replaceBuffer: (text) => {
        if (source().editable) {
          source().setCurrentValue(text);
        }
      },
      isDirty: () => source().dirty,
      getBuffer: () => source().buffer,
      getDag: () => source().dag,
      copyYaml: () => source().copySpec(source().buffer),
      toggleYaml: (open) => {
        // The visual editor also closes its own panels, as its YAML button does.
        const editor = visual();
        if (editor) {
          editor.toggleYaml(open);
          return;
        }
        const { sectionOpen: isOpen, setSectionOpen: setOpen } = source();
        if (open ?? !isOpen) {
          setDrawerForm(false);
          setOpen(true, 'panel');
        } else {
          source().closeSection();
        }
      },
      fitView: () => visual()?.fitView(),
      revealStep: (name) => visual()?.revealStep(name) ?? false,
      highlightStep: (name) => {
        const v = visual();
        if (v) v.highlightStep(name);
        else pendingHighlightRef.current = name;
      },
      addStep: (target) => visual()?.addStep(target),
      openWorkflowPanel: (open) => {
        const run = (v: VisualEditorHandle) => v.openWorkflowPanel(open);
        const v = visual();
        if (v) run(v);
        else if (open) pendingPanelRef.current = run;
      },
      openLauncher: (id) => {
        const run = (v: VisualEditorHandle) => v.openLauncher(id);
        const v = visual();
        if (v) run(v);
        else pendingPanelRef.current = run;
      },
      openForm: (name) => visual()?.openForm(name) ?? false,
      openYaml: (name) => visual()?.openYaml(name) ?? false,
      copySelection: () => visual()?.copySelection() ?? null,
      paste: (text) => visual()?.paste(text) ?? NO_VISUAL_EDITOR,
      duplicateSelection: () =>
        visual()?.duplicateSelection() ?? NO_VISUAL_EDITOR,
      undo: () => visual()?.undo(),
      redo: () => visual()?.redo(),
    };
  }, []);
  React.useImperativeHandle(
    loaded ? editorHandleRef : undefined,
    () => editorHandle,
    [editorHandle]
  );
  // A host shows the step form in Parameters | Settings segments; the
  // segment outlives the selection of a step.
  const hostMode = !!ndv || !!chrome;
  const inspectorView = useInspectorViewState();

  // The step that the host selected, for the header of the node details view
  // and for the tools of the step YAML.
  const ndvStepName =
    selectedStepName !== undefined ? selectedStepName : visualStepName;
  // The YAML that shows one step alone has the tools of that step: the quick
  // fixes and the format act inside its range and leave the rest of the text.
  const yamlStep = React.useMemo(() => {
    if (yamlKind !== 'step' || ndvStepName === null) return null;
    const key = textNames.byName[ndvStepName];
    return (
      [...textModel.steps, ...textModel.handlers].find((step) =>
        key !== undefined ? step.key === key : step.explicitName === ndvStepName
      ) ?? null
    );
  }, [yamlKind, ndvStepName, textNames, textModel]);
  const stepFixes = React.useMemo(
    () =>
      editable && clientIssues && yamlStep
        ? [
            ...new Set(
              clientIssues
                .planQuickFixesInRange(textModel, allIssues, yamlStep.range)
                .values()
            ),
          ]
        : [],
    [editable, clientIssues, yamlStep, textModel, allIssues]
  );
  const applyStepQuickFixes = React.useCallback(
    () => applyQuickFixes(stepFixes),
    [applyQuickFixes, stepFixes]
  );
  const formatStep = React.useCallback(() => {
    const text = currentValue ?? serverSpec ?? '';
    // The checked model can be one edit behind the text: read the step again.
    const model = yamlStep ? parseDagDocLenient(text) : null;
    const fresh = model
      ? [...model.steps, ...model.handlers].find(
          (step) => step.key === yamlStep?.key
        )
      : undefined;
    if (!fresh) return;
    const edit = formatStepRange(text, fresh);
    if (!edit) {
      showToast(ts('The step is already formatted'), { variant: 'info' });
      return;
    }
    applyTextChange(text, [edit], 'dagu-format');
  }, [applyTextChange, currentValue, serverSpec, showToast, ts, yamlStep]);
  const canFormatStep =
    yamlStep !== null && yamlStep.broken === undefined && canFormat;
  const stepFormatted = React.useMemo(() => {
    if (!canFormatStep) return false;
    const model = parseDagDocLenient(deferredText);
    const fresh = [...model.steps, ...model.handlers].find(
      (step) => step.key === yamlStep?.key
    );
    return !!fresh && formatStepRange(deferredText, fresh) === null;
  }, [canFormatStep, deferredText, yamlStep]);
  const yamlTools = !editable ? null : yamlKind === 'step' ? (
    <YamlTools
      scope="step"
      fixCount={stepFixes.length}
      canFormat={canFormatStep}
      formatted={stepFormatted}
      formatOffReason={
        yamlStep === null ? ts('Select a step first') : undefined
      }
      onFixAll={applyStepQuickFixes}
      onFormat={formatStep}
    />
  ) : (
    <YamlTools
      fixCount={allFixes.length}
      canFormat={canFormat}
      formatted={textFormatted}
      onFixAll={applyAllQuickFixes}
      onFormat={formatText}
    />
  );

  // Show loading indicator while fetching data
  if (isLoading) {
    return <LoadingIndicator fill />;
  }

  const warnings = localHasUnsavedChanges
    ? (liveValidation?.warnings ?? [])
    : (data?.warnings ?? []);

  // Check if we have local DAGs
  const hasLocalDags = localDags && localDags.length > 0;

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
        className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning"
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

  // A file with several documents shows one at a time. The parent is the
  // one that the editor edits; a sub-DAG shows read-only, as its YAML is part
  // of the same text.
  const documentSelector = hasLocalDags ? (
    <DocumentSwitcher
      value={activeTab}
      onChange={switchDocument}
      documents={[
        {
          value: 'parent',
          label: `${data?.dag?.name ?? ''} ${ts('(Parent)')}`,
        },
        ...(localDags ?? []).map(
          (localDag: components['schemas']['LocalDag']) => ({
            value: localDag.name,
            label: localDag.name,
          })
        ),
      ]}
    />
  ) : null;

  const selectedStep = selectedSpecStepName
    ? preview.dag?.steps?.find((step) => step.name === selectedSpecStepName)
    : undefined;

  // The header of the node details view follows the selection of the host.
  const runNdvStep =
    onStepCommand && stepCommandGates?.canRunStep && ndvStepName !== null
      ? () => onStepCommand({ kind: 'runStep', step: ndvStepName })
      : undefined;
  // The name that the draft gives the step, for the title: the DAG of the
  // server is one check behind a rename.
  const ndvTitle =
    ndv?.state && ndvStepName !== null
      ? draftStepTitle(
          editable ? (currentValue ?? serverSpec ?? '') : '',
          (shownValidation.dag ?? data?.dag)?.steps,
          ndvStepName
        )
      : undefined;
  // The errors of the draft for the step, with their keys. A draft that does
  // not validate gives no DAG: the form still says why.
  const ndvIssues =
    ndvStepName === null
      ? []
      : shownValidation.issues.filter(
          (issue) =>
            issue.severity === 'error' &&
            issue.stepName === ndvStepName &&
            // A required field shows under its own field in the form.
            !(
              drawerForm &&
              (requiredFieldOf(issue) ||
                (issue.fieldPath && /\bis required\.?$/.test(issue.message)))
            )
        );
  const ndvProblems =
    ndvStepName === null
      ? undefined
      : ndvIssues.map((issue) => refineIssue(issue, ts).message);
  const ndvProblemFixes = ndvIssues.map((issue) => quickFixOf(issue));
  const selectNdvStep = (name: string) => {
    if (onSelectedStepNameChange) {
      onSelectedStepNameChange(name);
    } else {
      visualHandleRef.current?.revealStep(name);
    }
  };

  // The text of the file, as the editor and the run canvas show it. It stays
  // mounted so the editor keeps its undo history, cursor and scroll position;
  // the form shows in place of the text, in the same panel.
  const yamlFileProps = {
    text: editable ? (currentValue ?? serverSpec ?? '') : (serverSpec ?? ''),
    readOnly: !editable,
    onChange: editable
      ? (newValue: string) => {
          setCurrentValue(newValue);
        }
      : undefined,
    modelUri: editorModelUri,
    schema: editorSchema,
    markers,
    quickFixSource,
    onEditorMount: (
      editor: import('monaco-editor').editor.IStandaloneCodeEditor | null
    ) => {
      yamlEditorRef.current = editor;
      // The lazy editor mounts after the views that read it: they render
      // again to see it.
      setEditorEpoch((epoch) => epoch + 1);
      // A reveal that waited for the first mount of the editor.
      const pending = pendingRevealRef.current;
      pendingRevealRef.current = null;
      if (editor && pending) pending();
    },
    // The editor mounts when the panel first opens.
    shown: sectionOpen,
    referenceState: editable ? referenceState : undefined,
    scopePath: yamlStepPath,
    // The whole file shows a bar for each line that differs from the last
    // save.
    changeBaseline:
      editable && yamlKind === 'file' ? (serverSpec ?? undefined) : undefined,
    stepLenses: yamlKind === 'file' ? stepLenses : undefined,
    formMode: drawerForm,
    formRootRef: setDrawerBodyRoot,
  };

  // A row of the Issues popover: the cursor of the YAML goes to the line of
  // the issue, and the step of the issue is selected on the canvas. With no
  // YAML file on screen, a field of a step opens the form of the step on it,
  // and any other row opens the file at the line.
  const revealIssue = (issue: components['schemas']['ValidationIssue']) => {
    const line = lineOfIssue(issue);
    type Revealer = CodeEditorLike & {
      setSelection?(range: object): void;
      revealLineInCenter?(line: number): void;
      focus?(): void;
    };
    const editorNow = () => yamlEditorRef.current as Revealer | null;
    const place = () => {
      const editor = editorNow();
      if (line === null || !editor?.setSelection) return;
      const model = editor.getModel() as {
        getLineMaxColumn?(l: number): number;
      };
      editor.setSelection({
        startLineNumber: line,
        startColumn: Math.max(issue.column ?? 1, 1),
        endLineNumber: line,
        endColumn: model?.getLineMaxColumn?.(line) ?? 1,
      });
      editor.revealLineInCenter?.(line);
    };
    const fileShown = sectionOpen && yamlKind === 'file';
    const field = relativeFieldPath(issue.stepPath ?? '', issue.fieldPath);
    if (!fileShown && issue.stepName && field && hostMode) {
      if (editorHandle.openForm(issue.stepName)) {
        inspectorView.setFocusPath(field);
        return;
      }
    }
    if (issue.stepName) visualHandleRef.current?.revealStep(issue.stepName);
    if (!fileShown) {
      // A row about a step only shows the step; a row about the file opens it.
      if (issue.stepName || line === null) return;
      editorHandle.toggleYaml(true);
      // The editor lays out once the panel shows. A first open mounts it
      // later: the reveal waits for the mount.
      const reveal = () =>
        window.setTimeout(() => {
          place();
          editorNow()?.focus?.();
        }, 60);
      if (editorNow()) reveal();
      else pendingRevealRef.current = reveal;
      return;
    }
    place();
    editorNow()?.focus?.();
  };

  // Compact notices: one issues button in the toolbar, which takes the place
  // of Save while there are errors, and one notice line. A sub-DAG shows only
  // its own saved errors, as its banner did.
  const issuesPopover = !compactNotices ? null : activeTab === 'parent' ? (
    <IssuesPopover
      warnings={warnings}
      errors={parentErrors ?? []}
      valueReferences={valueReferenceNotices}
      issues={shownValidation.issues}
      openRequest={issuesRequest}
      lineOf={lineOfIssue}
      quickFixOf={quickFixOf}
      onQuickFix={applyQuickFixes}
      onRevealIssue={revealIssue}
      onRevealStep={(step, field) => {
        // A row that names a field opens the form of the step on it.
        if (hostMode && field && editorHandle.openForm(step)) {
          inspectorView.setFocusPath(field);
          return;
        }
        visualHandleRef.current?.revealStep(step);
      }}
    />
  ) : (
    <IssuesPopover warnings={[]} errors={preview.errors ?? []} />
  );
  // Warnings do not block a save: Save carries their count.
  const warningCount =
    compactNotices && activeTab === 'parent'
      ? issueRows({
          warnings,
          errors: [],
          issues: shownValidation.issues,
        }).warnings.length
      : 0;

  const noticeItems: NoticeLineItem[] = [];
  if (compactNotices) {
    if (activeTab === 'parent' && visualFrozen) {
      noticeItems.push({
        id: 'frozen',
        tone: 'warning',
        message: frozenMessage(ts, unparsedLine),
        priority: NOTICE_PRIORITY.frozen,
      });
    }
    if (activeTab === 'parent' && editorNotice) {
      noticeItems.push({
        id: 'edit-refused',
        tone: editorNotice.kind === 'edit-refused' ? 'warning' : 'info',
        role: 'alert',
        message: editorNotice.message,
        action: editorNotice.convertible
          ? { label: ts('Convert steps'), run: editorNotice.convert }
          : undefined,
        dismissible: true,
        onDismiss: editorNotice.dismiss,
        priority: NOTICE_PRIORITY.editRefused,
      });
    }
    noticeItems.push(...(notices ?? []));
  }

  const issueCount = liveValidation?.errors.length ?? 0;
  // The button says Save in every state. Its icon shows the state, and the
  // text of the state is for the tooltip and for screen readers.
  const saveStatus = {
    clean: '',
    unchecked: '',
    validating: ts('Validating...'),
    valid: ts('Valid'),
    warnings: ts('Valid with warnings'),
    errors: ts(issueCount === 1 ? '{count} issue' : '{count} issues', {
      count: issueCount,
    }),
  }[saveState];
  const saveButtonTitle = [
    syntaxError ? ts('Fix the YAML syntax error before you save') : saveStatus,
    warningCount > 0 ? ts('Warnings: {count}', { count: warningCount }) : '',
    ts('Save changes (Ctrl+S / Cmd+S)'),
  ]
    .filter(Boolean)
    .join(' \u2022 ');

  return (
    <DAGContext.Consumer>
      {(props) => {
        // Update refresh callback ref directly (safe in render)
        refreshCallbackRef.current = props.refresh;
        const specActions = (
          <div className="flex items-center gap-2">
            {/* A canvas that cannot save shows its issues button here. */}
            {!savingBlocked && issuesPopover}
            {!compactNotices && valueReferenceNotices.length > 0 && (
              <I18nProps>
                <ValueReferenceNoticesButton
                  notices={valueReferenceNotices}
                  description="Value-reference notices produced while loading this spec."
                />
              </I18nProps>
            )}
            {editable && (
              // Save and the issues button share this box: the same place,
              // the same height and the same width, so that nothing moves.
              <div
                data-testid="save-slot"
                className="flex h-7 w-14 items-center justify-start"
              >
                {savingBlocked && issuesPopover}
                {!savingBlocked && (
                  <I18nProps>
                    <Button
                      id="save-config"
                      variant="primary"
                      size="xs"
                      aria-label={ts('Save')}
                      aria-describedby="save-status"
                      title={saveButtonTitle}
                      // Icon only, with one size in every state. The count on
                      // its corner tells the warnings.
                      className="relative h-7 w-7 justify-center p-0"
                      disabled={
                        isSaving || !localHasUnsavedChanges || syntaxError
                      }
                      onClick={async () => {
                        await requestSave();
                        props.refresh();
                      }}
                    >
                      <SaveStatusIcon
                        busy={isSaving || saveState === 'validating'}
                      />
                      <span id="save-status" className="sr-only">
                        {saveStatus}
                      </span>
                      {warningCount > 0 && (
                        <CountBadge
                          data-testid="save-warning-count"
                          role="img"
                          aria-label={ts('Warnings: {count}', {
                            count: warningCount,
                          })}
                          tone="warning"
                          count={warningCount}
                          corner
                        />
                      )}
                    </Button>
                  </I18nProps>
                )}
              </div>
            )}
          </div>
        );

        return (
          data?.dag && (
            // In the graph zone the node view is above the page: the dialogs
            // of the editor (the diff before a save, a conflict) go above it.
            <DialogLayerProvider
              value={panelPlacement === 'zone' ? 'raised' : 'default'}
            >
              <SpecLiveSync
                fileName={fileName}
                current={data}
                mutate={mutateSpec}
                onPollChange={setPoll}
              />
              {/* External changes conflict dialog */}
              <ExternalChangeDialog
                visible={conflict.hasConflict}
                onDiscard={() => resolveConflict('discard')}
                onIgnore={() => resolveConflict('ignore')}
              />
              <ExternalChangeDialog
                visible={staleDraftOpen}
                onDiscard={() => {
                  setStaleDraftOpen(false);
                  discardDraft();
                }}
                onIgnore={() => {
                  setStaleDraftOpen(false);
                  restoreDraft();
                }}
              />
              <ConfirmModal
                title={ts('Discard changes')}
                buttonText={ts('Discard')}
                visible={discardOpen}
                dismissModal={() => setDiscardOpen(false)}
                onSubmit={() => {
                  setDiscardOpen(false);
                  discardChanges();
                }}
              >
                <p>
                  {ts('Discard {count} changed lines? This cannot be undone.', {
                    count: discardLines,
                  })}
                </p>
              </ConfirmModal>
              {reviewOpen && (
                <React.Suspense fallback={null}>
                  <SaveDiffDialog
                    open
                    original={serverSpec ?? ''}
                    edited={currentValue ?? ''}
                    saving={isSaving}
                    onConfirm={async () => {
                      await handleSave();
                      setReviewOpen(false);
                      props.refresh();
                    }}
                    onCancel={() => setReviewOpen(false)}
                  />
                </React.Suspense>
              )}

              <div
                className={cn(
                  'flex min-h-0 flex-1 flex-col gap-3',
                  fillHeight && 'h-full'
                )}
              >
                {/* A sub-DAG without a canvas has no toolbar for the
                    Issues popover: its errors keep the banner. */}
                {(!compactNotices ||
                  (activeTab !== 'parent' && !preview.dag)) &&
                  (activeTab === 'parent'
                    ? !!warningsBanner || !!parentErrors?.length
                    : !!preview.errors?.length) && (
                    <div className="max-h-32 flex-shrink-0 space-y-2 overflow-y-auto">
                      {activeTab === 'parent' && warningsBanner}
                      {renderErrors(
                        activeTab === 'parent' ? parentErrors : preview.errors
                      )}
                    </div>
                  )}

                <div className="flex min-h-80 min-w-0 flex-1 flex-col">
                  {activeTab === 'parent' ? (
                    <React.Suspense fallback={null}>
                      <InspectorViewProvider
                        value={hostMode ? inspectorView : null}
                      >
                        <VisualEditor
                          fileName={fileName}
                          dagName={data.dag.name}
                          text={
                            editable
                              ? (currentValue ?? serverSpec ?? '')
                              : (serverSpec ?? '')
                          }
                          onTextChange={(next) => {
                            if (editable) {
                              setCurrentValue(next);
                            }
                          }}
                          validation={shownValidation}
                          editable={editable}
                          isDirty={localHasUnsavedChanges}
                          savedText={serverSpec ?? undefined}
                          canvasOverlay={
                            (compactNotices && noticeItems.length > 0) ||
                            (editable && draft) ? (
                              // The notices are cards over the canvas, like
                              // the draft: under the toolbars, with no room
                              // taken from the canvas.
                              <div className="flex min-w-0 max-w-full flex-col items-center gap-1">
                                {editable && draft && (
                                  <DraftRecoveryCard
                                    savedAt={draft.savedAt}
                                    onRestore={() => {
                                      if (draft.baseSha256 !== data.sha256) {
                                        setStaleDraftOpen(true);
                                      } else {
                                        restoreDraft();
                                      }
                                    }}
                                    onDiscard={discardDraft}
                                  />
                                )}
                                {compactNotices && (
                                  <NoticeLine
                                    className="pointer-events-auto max-w-full rounded-md bg-card shadow-md"
                                    notices={noticeItems}
                                  />
                                )}
                              </div>
                            ) : undefined
                          }
                          customActions={visualCustomActions}
                          harnesses={effectiveLocalHarnesses}
                          yamlEditorRef={yamlEditorRef}
                          yamlVisible={sectionOpen}
                          onYamlVisibleChange={setSectionOpen}
                          onYamlIntent={preloadDAGEditor}
                          onYamlModeChange={setYamlKind}
                          initialYamlMode={yamlKind}
                          toolbarLead={documentSelector}
                          toolbarTrail={specActions}
                          onRevertToSave={editable ? requestDiscard : undefined}
                          yamlToolbarRoot={yamlToolbarRoot}
                          onYamlStepPathChange={setYamlStepPath}
                          drawerBodyRoot={drawerBodyRoot}
                          formVisible={drawerForm}
                          onFormVisibleChange={setDrawerForm}
                          workflowPanel={
                            preview.dag ? (
                              renderWorkflowPanel ? (
                                renderWorkflowPanel(
                                  <WorkflowPanel dag={preview.dag} />,
                                  preview.dag
                                )
                              ) : (
                                <WorkflowPanel dag={preview.dag} />
                              )
                            ) : undefined
                          }
                          workflowPanelBare={!!renderWorkflowPanel}
                          launcherHost={launcherHost}
                          runSource={runSource}
                          onRunStarted={onRunStarted}
                          selectedStepName={selectedStepName}
                          onSelectedStepNameChange={onSelectedStepNameChange}
                          onStepClick={onStepClick}
                          openNdvOn={openNdvOn}
                          renderStepRun={renderStepRun}
                          renderStepOutput={renderStepOutput}
                          inputColumnRoot={
                            inputColumnRoot ?? (ndv ? ndvInputRoot : undefined)
                          }
                          outputColumnRoot={
                            outputColumnRoot ??
                            (ndv ? ndvOutputRoot : undefined)
                          }
                          onStepCommand={onStepCommand}
                          stepCommandGates={stepCommandGates}
                          followedRuns={followedRuns}
                          inputRun={inputRun}
                          chrome={chrome}
                          onNotice={
                            compactNotices && !onNotice
                              ? setEditorNotice
                              : onNotice
                          }
                          onStateChange={handleVisualStateChange}
                          handleRef={setVisualHandle}
                        />
                      </InspectorViewProvider>
                    </React.Suspense>
                  ) : preview.dag ? (
                    <React.Suspense fallback={null}>
                      <DetailsCanvas
                        dag={preview.dag}
                        workflowPanel={<WorkflowPanel dag={preview.dag} />}
                        // The order of the parent: selector, Workflow, YAML,
                        // then the actions.
                        toolbarLead={documentSelector}
                        toolbarTrail={
                          <>
                            <Button
                              type="button"
                              variant="ghost"
                              size="xs"
                              aria-pressed={sectionOpen}
                              onPointerEnter={preloadDAGEditor}
                              onFocus={preloadDAGEditor}
                              onClick={() =>
                                setSectionOpen(!sectionOpen, 'panel')
                              }
                            >
                              <FileCode className="h-3.5 w-3.5" />
                              {ts('YAML')}
                            </Button>
                            {/* The YAML drawer edits the whole file: it
                                keeps Save. */}
                            {specActions}
                          </>
                        }
                        onSelectStep={(name) => {
                          setSelectedSpecStepName(name);
                          setIsSpecStepDetailsOpen(name !== null);
                        }}
                      />
                    </React.Suspense>
                  ) : null}
                </div>

                <StepDetailsDrawer
                  dagName={preview.dag?.name}
                  isOpen={isSpecStepDetailsOpen}
                  step={selectedStep}
                  onClose={closeSpecStepDetails}
                  panelId={activeTab === 'parent' ? undefined : 'subdag-step'}
                />

                {ndv ? (
                  <YamlFilePanel
                    {...yamlFileProps}
                    state={ndv.state}
                    scope={yamlKind}
                    onClose={closeSection}
                    stepName={ndvStepName}
                    title={ndvTitle}
                    dag={shownValidation.dag ?? data.dag}
                    runDetails={runSource?.details}
                    onSelectStep={selectNdvStep}
                    problems={ndvProblems}
                    problemFixes={ndvProblemFixes}
                    onQuickFix={applyQuickFixes}
                    tools={yamlTools}
                    hasRunFacts={
                      renderStepRun !== undefined ||
                      renderStepOutput !== undefined
                    }
                    onRunStep={runNdvStep}
                    canForm={ndvStepName !== null}
                    onFormModeChange={setDrawerForm}
                    toolbarRef={setYamlToolbarRoot}
                    inputRef={setNdvInputRoot}
                    outputRef={setNdvOutputRoot}
                  />
                ) : (
                  // It opens in the side drawer of the step details, and is
                  // not moved, to keep the state of the editor.
                  <section
                    hidden={!sectionOpen}
                    aria-label={ts('YAML')}
                    data-panel-shell="right"
                    className={panelShellClass(panelPlacement)}
                    style={{
                      width: panelWidth(getStoredDrawerWidth(), viewport),
                    }}
                  >
                    {/* The slots of YamlFilePanel: the title, the lock, the
                        tools; the choice of mode in the row under the bar. */}
                    <PanelHeader
                      closeLabel={ts('Close')}
                      onClose={closeSection}
                    >
                      <FileCode
                        className="h-4 w-4 shrink-0 text-muted-foreground"
                        aria-hidden="true"
                      />
                      <h2 className="min-w-[6rem] truncate text-base font-semibold">
                        {ts('YAML')}
                      </h2>
                      {!editable && <ReadOnlyMarker />}
                      <div className="ml-auto flex shrink-0 items-center gap-0.5">
                        {!drawerForm && yamlTools}
                      </div>
                    </PanelHeader>
                    <div
                      ref={setYamlToolbarRoot}
                      className="flex min-w-0 shrink-0 items-center border-b border-border px-3 py-1.5 empty:hidden"
                    />
                    <YamlFileBody {...yamlFileProps} />
                  </section>
                )}
              </div>
            </DialogLayerProvider>
          )
        );
      }}
    </DAGContext.Consumer>
  );
}

/**
 * The icon of the Save button: always the floppy disk, a spinner while it
 * saves or validates. The result is in the status text and the issues popover.
 */
function SaveStatusIcon({ busy }: { busy: boolean }) {
  return busy ? (
    <Spinner className="shrink-0" />
  ) : (
    <Save aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
  );
}

// The two states of the feed that the polling of the spec depends on.
const FEED_UP: SSEState<unknown> = {
  data: null,
  error: null,
  isConnected: true,
  isConnecting: false,
  shouldUseFallback: false,
};
const FEED_DOWN: SSEState<unknown> = { ...FEED_UP, isConnected: false };

type SpecResponse =
  paths['/dags/{fileName}/spec']['get']['responses']['200']['content']['application/json'];

/**
 * The SSE feed of the DAG, for the spec of the editor. It holds the feed
 * state, so that an event renders this component and not the editor. It
 * writes the event into the SWR cache of the spec, and tells the editor when
 * the feed is down (the editor polls then).
 */
function SpecLiveSync({
  fileName,
  current,
  mutate,
  onPollChange,
}: {
  fileName: string;
  /** The spec in the cache now: an event with the same text keeps its hash. */
  current: SpecResponse | undefined;
  mutate: KeyedMutator<SpecResponse>;
  onPollChange(poll: boolean): void;
}) {
  const remoteNode = useRemoteNode();
  const dagSSE = useDAGSSE(fileName, !!fileName, remoteNode);
  const poll = sseFallbackOptions(dagSSE).refreshInterval > 0;
  React.useEffect(() => onPollChange(poll), [poll, onPollChange]);
  useSSECacheSync(dagSSE, mutate, (next) =>
    next.spec === undefined
      ? undefined
      : {
          dag: next.dag,
          errors: next.errors ?? [],
          warnings: next.warnings ?? [],
          valueReferenceNotices: current?.valueReferenceNotices ?? [],
          spec: next.spec,
          // The event carries no hash; the cached one still fits the same text.
          sha256: next.spec === current?.spec ? current?.sha256 : undefined,
        }
  );
  return null;
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

function useStableCustomActionHints(
  hints: EditorCustomActionHint[]
): EditorCustomActionHint[] {
  const stableRef = React.useRef(hints);
  if (!customActionHintsEqual(stableRef.current, hints)) {
    stableRef.current = hints;
  }
  return stableRef.current;
}

// A host that renders again with the same props (a run tick, an event of the
// run) does not render the editor again.
export default React.memo(DAGSpec);
