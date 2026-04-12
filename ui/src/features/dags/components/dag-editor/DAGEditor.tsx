// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGEditor component provides a Monaco editor for editing DAG YAML definitions.
 *
 * @module features/dags/components/dag-editor
 */
import type { JSONSchema } from '@/lib/schema-utils';
import { cn } from '@/lib/utils';
import MonacoEditor, { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import {
  configureMonacoYaml,
  type JSONSchema as MonacoJSONSchema,
} from 'monaco-yaml';
import { useEffect, useRef } from 'react';

// Get schema URL from config (getConfig() is available at module load time)
declare function getConfig(): { basePath: string };
const schemaUrl = `${getConfig().basePath}/assets/dag.schema.json`;

type SchemaRegistration = {
  fileMatch: string;
  uri: string;
  schema?: MonacoJSONSchema;
};

const schemaRegistrations = new Map<string, SchemaRegistration>();

// Configure YAML language service once at module load time.
const monacoYaml = configureMonacoYaml(monaco, {
  enableSchemaRequest: true,
  hover: true,
  completion: true,
  validate: true,
  format: true,
  schemas: [],
});

loader.config({ monaco });

async function refreshRegisteredSchemas() {
  const registrations = Array.from(schemaRegistrations.values()).map(
    ({ fileMatch, ...registration }) => ({
      ...registration,
      fileMatch: [fileMatch],
    })
  );

  await monacoYaml.update({
    ...monacoYaml.getOptions(),
    schemas: registrations,
  });
}

function getDocumentSchemaUri(modelUri: string): string {
  const stableId = modelUri
    .replace(/^[A-Za-z][A-Za-z0-9+.-]*:\/\//, '')
    .replace(/[^A-Za-z0-9._-]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return `inmemory://dagu-schema/${stableId || 'document'}.schema.json`;
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
}: Omit<Props, 'highlightLine'>) {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const effectiveModelUri = modelUri ?? 'inmemory://dagu/editor/default.yaml';

  // Clean up editor on unmount
  useEffect(() => {
    return () => {
      editorRef.current?.dispose();
    };
  }, []);

  useEffect(() => {
    const documentSchemaUri = getDocumentSchemaUri(effectiveModelUri);
    schemaRegistrations.set(effectiveModelUri, {
      fileMatch: effectiveModelUri,
      uri: schema ? documentSchemaUri : schemaUrl,
      schema: schema
        ? ({
            ...schema,
            $id: documentSchemaUri,
          } as MonacoJSONSchema)
        : undefined,
    });
    void refreshRegisteredSchemas();

    return () => {
      schemaRegistrations.delete(effectiveModelUri);
      void refreshRegisteredSchemas();
    };
  }, [effectiveModelUri, schema]);

  // Update editor theme when dark mode changes
  useEffect(() => {
    if (editorRef.current) {
      const newTheme = document.documentElement.classList.contains('dark')
        ? 'vs-dark'
        : 'vs';
      monaco.editor.setTheme(newTheme);
    }
  }, []);

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
              ? 'vs-dark'
              : 'vs';
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

    // Format document after a short delay
    setTimeout(() => {
      editor.getAction('editor.action.formatDocument')?.run();
    }, 100);

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
        theme={isDarkMode ? 'vs-dark' : 'vs'}
        value={value}
        onChange={readOnly ? undefined : onChange}
        onMount={editorDidMount}
        options={{
          readOnly: readOnly,
          // automaticLayout: true,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          quickSuggestions: readOnly
            ? false
            : { other: true, comments: false, strings: true },
          suggestOnTriggerCharacters: !readOnly,
          formatOnType: !readOnly,
          formatOnPaste: !readOnly,
          renderValidationDecorations: readOnly ? 'off' : 'on',
          lineNumbers: lineNumbers ? 'on' : 'off',
          glyphMargin: true,
          fontFamily:
            "'JetBrains Mono', 'Fira Code', Menlo, Monaco, 'Courier New', monospace",
          fontSize: 13,
          padding: {
            top: 8,
            bottom: 8,
          },
        }}
      />
    </div>
  );
}

export default DAGEditor;
