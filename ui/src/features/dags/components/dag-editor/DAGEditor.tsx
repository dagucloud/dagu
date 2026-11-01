// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGEditor component provides a Monaco editor for editing DAG YAML definitions.
 *
 * @module features/dags/components/dag-editor
 */
import { KILN_DARK, KILN_LIGHT, registerKilnThemes } from '@/lib/monaco-theme';
import type { JSONSchema } from '@/lib/schema-utils';
import { cn } from '@/lib/utils';
import MonacoEditor, { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import { monacoYaml } from '@/lib/monaco-yaml';
import { registerReferenceCompletion } from '@/features/visual-editor/references/monacoCompletion';
import type { ReferenceEditorState } from '@/features/visual-editor/references/editorState';
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { buildEditorOptions } from './editorOptions';
import { ISSUE_MARKER_OWNER } from './issueMarkers';
import {
  registerQuickFixSource,
  type QuickFixSource,
} from './quickFixProvider';
import { useChangeBars, type ChangeBarEditor } from './useChangeBars';
import {
  registerStepLenses,
  type StepLensRegistration,
  type StepLensSource,
} from './stepLenses';
import {
  buildSchemaRegistration,
  removeSchemaRegistration,
  toMonacoYamlSchemas,
  upsertSchemaRegistration,
  type SchemaRegistrationOwner,
  type StoredSchemaRegistration,
} from './schemaRegistration';

// Get schema URL from config (getConfig() is available at module load time)
declare function getConfig(): { basePath: string };
const schemaUrl = `${getConfig().basePath}/assets/dag.schema.json`;

const schemaRegistrations = new Map<string, StoredSchemaRegistration>();

loader.config({ monaco });

registerKilnThemes();

async function refreshRegisteredSchemas() {
  await monacoYaml.update({
    ...monacoYaml.getOptions(),
    schemas: toMonacoYamlSchemas(schemaRegistrations),
  });
}

/**
 * Cursor position information
 */
export interface CursorPosition {
  lineNumber: number;
  column: number;
}

/**
 * Props for the DAGEditor component
 */
type Props = {
  /** Current YAML content */
  value: string;
  /** Callback function when content changes */
  onChange?: (value?: string) => void;
  /** Whether the editor is in read-only mode */
  readOnly?: boolean;
  /** Whether to show line numbers */
  lineNumbers?: boolean;
  /** Line number to highlight */
  highlightLine?: number;
  /** Additional class name */
  className?: string;
  /** Callback when cursor position changes */
  onCursorPositionChange?: (position: CursorPosition) => void;
  /** Stable model URI used for schema association */
  modelUri?: string;
  /** Optional document-specific schema */
  schema?: JSONSchema | null;
  /** Server-side validation markers rendered under the 'dagu-server' owner */
  markers?: monaco.editor.IMarkerData[];
  /**
   * The issues behind the markers. With it, the editor offers their quick
   * fixes as code actions: hover, lightbulb and Ctrl+.
   */
  quickFixSource?: QuickFixSource;
  /** Receives the editor after it mounts, and null when it unmounts. */
  onEditorMount?: (editor: monaco.editor.IStandaloneCodeEditor | null) => void;
  /**
   * Scope of the value references, read at each completion. When set, the
   * editor completes and explains `${...}` references.
   */
  referenceState?: () => ReferenceEditorState;
  /**
   * The saved text. With it, a bar in the margin marks each line that
   * differs from it: added, modified, and where lines are gone.
   */
  changeBaseline?: string;
  /**
   * The lenses above the steps of the whole file: each opens its step in
   * the full-screen step view.
   */
  stepLenses?: StepLensSource;
};

/**
 * DAGEditor component provides a Monaco editor with YAML schema validation
 * for editing or viewing DAG definitions
 */
function DAGEditor({
  value,
  onChange,
  readOnly = false,
  lineNumbers = true,
  className,
  onCursorPositionChange,
  modelUri,
  schema,
  markers,
  quickFixSource,
  onEditorMount,
  referenceState,
  changeBaseline,
  stepLenses,
}: Omit<Props, 'highlightLine'>) {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const onEditorMountRef = useRef(onEditorMount);
  onEditorMountRef.current = onEditorMount;
  const activeSchemaRegistrationRef = useRef<SchemaRegistrationOwner | null>(
    null
  );
  const effectiveModelUri = modelUri ?? 'inmemory://dagu/editor/default.yaml';
  const schemaRegistrationOwnerId = useId();
  // The fingerprint serialises the whole schema: build it when an input
  // changes, not at each render.
  const nextSchemaRegistration = useMemo(
    () =>
      buildSchemaRegistration(
        schemaRegistrationOwnerId,
        effectiveModelUri,
        schema,
        schemaUrl
      ),
    [schemaRegistrationOwnerId, effectiveModelUri, schema]
  );
  const nextSchemaRegistrationRef = useRef(nextSchemaRegistration);
  nextSchemaRegistrationRef.current = nextSchemaRegistration;

  // Reference completion is global to YAML models, so only an editor that
  // gives a scope registers it, once for its lifetime.
  const referenceStateRef = useRef(referenceState);
  referenceStateRef.current = referenceState;
  const wantsReferences = referenceState !== undefined;
  useEffect(() => {
    if (!wantsReferences) {
      return;
    }
    const registration = registerReferenceCompletion(
      monaco,
      () => referenceStateRef.current?.() ?? {}
    );
    return () => registration.dispose();
  }, [wantsReferences]);

  // The code actions of the quick fixes go to this editor's model only, and
  // go away with it.
  const quickFixSourceRef = useRef(quickFixSource);
  quickFixSourceRef.current = quickFixSource;
  const wantsQuickFixes = quickFixSource !== undefined;
  useEffect(() => {
    if (!wantsQuickFixes) {
      return;
    }
    const registration = registerQuickFixSource(monaco, effectiveModelUri, {
      getPlan: () => quickFixSourceRef.current?.getPlan() ?? null,
      onApplied: (text) => quickFixSourceRef.current?.onApplied?.(text),
    });
    return () => registration.dispose();
  }, [wantsQuickFixes, effectiveModelUri]);

  // Clean up editor on unmount
  useEffect(() => {
    return () => {
      onEditorMountRef.current?.(null);
      editorRef.current?.dispose();
    };
  }, []);

  useEffect(() => {
    const next = nextSchemaRegistrationRef.current;
    const previous = activeSchemaRegistrationRef.current;
    let changed = false;

    if (previous && previous.modelUri !== next.modelUri) {
      changed =
        removeSchemaRegistration(
          schemaRegistrations,
          previous.modelUri,
          previous.ownerId,
          previous.fingerprint
        ) || changed;
    }

    changed = upsertSchemaRegistration(schemaRegistrations, next) || changed;
    activeSchemaRegistrationRef.current = next;

    if (changed) {
      void refreshRegisteredSchemas();
    }
  }, [nextSchemaRegistration.fingerprint]);

  useEffect(() => {
    return () => {
      const active = activeSchemaRegistrationRef.current;
      if (!active) {
        return;
      }
      if (
        removeSchemaRegistration(
          schemaRegistrations,
          active.modelUri,
          active.ownerId,
          active.fingerprint
        )
      ) {
        void refreshRegisteredSchemas();
      }
      activeSchemaRegistrationRef.current = null;
    };
  }, []);

  // Update editor theme when dark mode changes
  useEffect(() => {
    if (editorRef.current) {
      const newTheme = document.documentElement.classList.contains('dark')
        ? KILN_DARK
        : KILN_LIGHT;
      monaco.editor.setTheme(newTheme);
    }
  }, []);

  // Apply server-side validation markers to the model under a dedicated
  // owner so monaco-yaml's own schema diagnostics are untouched. The model
  // exists only after the editor mounts.
  const [isEditorMounted, setIsEditorMounted] = useState(false);
  useChangeBars(
    isEditorMounted ? (editorRef.current as ChangeBarEditor | null) : null,
    changeBaseline,
    value
  );
  // The lenses of the steps stand in the editor that gets them, and follow
  // the steps and the label.
  const stepLensesRef = useRef(stepLenses);
  stepLensesRef.current = stepLenses;
  const wantsStepLenses = stepLenses !== undefined;
  const stepLensRegistration = useRef<StepLensRegistration | null>(null);
  useEffect(() => {
    const codeEditor = editorRef.current;
    if (!isEditorMounted || !wantsStepLenses || !codeEditor) {
      return;
    }
    const registration = registerStepLenses(
      monaco,
      codeEditor,
      () => stepLensesRef.current ?? null
    );
    stepLensRegistration.current = registration;
    return () => {
      stepLensRegistration.current = null;
      registration.dispose();
    };
  }, [isEditorMounted, wantsStepLenses]);
  const lensKey = stepLenses
    ? `${stepLenses.label}\n${stepLenses.steps.map((step) => `${step.line}:${step.name}`).join('\n')}`
    : '';
  useEffect(() => {
    stepLensRegistration.current?.refresh();
  }, [lensKey]);
  useEffect(() => {
    if (!isEditorMounted) {
      return;
    }
    const model = monaco.editor.getModel(monaco.Uri.parse(effectiveModelUri));
    if (!model) {
      return;
    }
    monaco.editor.setModelMarkers(model, ISSUE_MARKER_OWNER, markers ?? []);
    return () => {
      if (!model.isDisposed()) {
        monaco.editor.setModelMarkers(model, ISSUE_MARKER_OWNER, []);
      }
    };
  }, [markers, effectiveModelUri, isEditorMounted]);

  // Listen for theme changes
  useEffect(() => {
    const observer = new MutationObserver((mutations) => {
      mutations.forEach((mutation) => {
        if (
          mutation.type === 'attributes' &&
          mutation.attributeName === 'class'
        ) {
          if (editorRef.current) {
            const newTheme = document.documentElement.classList.contains('dark')
              ? KILN_DARK
              : KILN_LIGHT;
            monaco.editor.setTheme(newTheme);
          }
        }
      });
    });

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    });

    return () => observer.disconnect();
  }, []);

  /**
   * Initialize editor after mounting
   */
  const editorDidMount = (editor: monaco.editor.IStandaloneCodeEditor) => {
    editorRef.current = editor;
    setIsEditorMounted(true);
    onEditorMountRef.current?.(editor);

    if (!readOnly) {
      editor.addAction({
        id: 'dagu.triggerSuggest',
        label: 'Trigger Autocomplete',
        precondition:
          '!editorReadonly && editorHasCompletionItemProvider && !suggestWidgetVisible',
        keybindings: [
          monaco.KeyMod.CtrlCmd | monaco.KeyCode.Space,
          monaco.KeyMod.WinCtrl | monaco.KeyCode.Space,
        ],
        keybindingContext: 'textInputFocus',
        run: async (activeEditor) => {
          await activeEditor.getAction('editor.action.triggerSuggest')?.run();
        },
      });
    }

    // Prevent 'f' key from propagating to prevent fullscreen shortcuts
    // when user is typing in the editor
    editor.onKeyDown((e) => {
      if (e.code === 'KeyF' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        // Stop the 'f' key from propagating to parent components
        // that might have fullscreen shortcuts
        e.stopPropagation();
      }
    });

    // Listen for cursor position changes
    if (onCursorPositionChange) {
      // Initial position
      const position = editor.getPosition();
      if (position) {
        onCursorPositionChange({
          lineNumber: position.lineNumber,
          column: position.column,
        });
      }

      // Subscribe to cursor changes
      editor.onDidChangeCursorPosition((e) => {
        onCursorPositionChange({
          lineNumber: e.position.lineNumber,
          column: e.position.column,
        });
      });
    }
  };

  // One options object per setting: the editor applies the options again when
  // the object changes, and would reset the line numbers that the step view sets.
  const options = useMemo(
    () => buildEditorOptions({ readOnly, lineNumbers }),
    [readOnly, lineNumbers]
  );

  // Detect dark mode
  const isDarkMode =
    typeof window !== 'undefined' &&
    document.documentElement.classList.contains('dark');

  return (
    <div className={cn('h-full', className)}>
      <MonacoEditor
        height="100%"
        language="yaml"
        path={effectiveModelUri}
        theme={isDarkMode ? KILN_DARK : KILN_LIGHT}
        value={value}
        onChange={readOnly ? undefined : onChange}
        onMount={editorDidMount}
        options={options}
      />
    </div>
  );
}

export default DAGEditor;
