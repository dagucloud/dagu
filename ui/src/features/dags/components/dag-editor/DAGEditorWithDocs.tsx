/**
 * DAGEditorWithDocs component provides a Monaco editor with Schema Documentation sidebar.
 * This is a shared component used by both the editable DAGSpec and readonly DAGSpecReadOnly.
 *
 * @module features/dags/components/dag-editor
 */
import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { BookOpen } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { JSONSchema } from '@/lib/schema-utils';
import { Button } from '@/components/ui/button';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { useDebouncedValue } from '../../../../hooks/useDebouncedValue';
import { useYamlCursorPath } from '../../../../hooks/useYamlCursorPath';
import { docsPathInScope } from './docsPath';
import { loadDAGEditor } from './loadDAGEditor';
import type { CursorPosition } from './DAGEditor';
import type { QuickFixSource } from './quickFixProvider';
import type { StepLensSource } from './stepLenses';
import { SchemaDocSidebar } from './SchemaDocSidebar';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import type { ReferenceEditorState } from '@/features/visual-editor/references/editorState';

const DAGEditor = React.lazy(loadDAGEditor);

/**
 * Props for the DAGEditorWithDocs component
 */
type DAGEditorWithDocsProps = {
  /** Current YAML content */
  value: string;
  /** Callback function when content changes */
  onChange?: (value?: string) => void;
  /** Whether the editor is in read-only mode */
  readOnly?: boolean;
  /** Additional class name for the container */
  className?: string;
  /** Whether to show the docs toggle button (default: true) */
  showDocsButton?: boolean;
  /** Whether the schema docs show when the user has not chosen yet. */
  defaultDocsOpen?: boolean;
  /** Additional content to render in the header (e.g., save button) */
  headerActions?: React.ReactNode;
  /** Optional document-specific schema */
  schema?: JSONSchema | null;
  /** Stable model URI used for per-document schema association */
  modelUri?: string;
  /** Server-side validation markers forwarded to the editor */
  markers?: import('monaco-editor').editor.IMarkerData[];
  /** The issues behind the markers: the editor offers their quick fixes. */
  quickFixSource?: QuickFixSource;
  /** Receives the Monaco editor after it mounts, and null when it unmounts */
  onEditorMount?: (
    editor: import('monaco-editor').editor.IStandaloneCodeEditor | null
  ) => void;
  /** Scope of the value references for `${...}` completion */
  referenceState?: () => ReferenceEditorState;
  /**
   * The YAML path of the one step that the editor shows (the step scope).
   * The docs follow the cursor inside it, and the step when the cursor is
   * in a line that the scope hides.
   */
  scopePath?: string[] | null;
  /** The saved text: the editor marks the lines that differ from it. */
  changeBaseline?: string;
  /** The lenses above the steps of the whole file. */
  stepLenses?: StepLensSource;
  /**
   * False while a host keeps the editor hidden. The code editor loads and
   * mounts when this first becomes true, then stays mounted. Default: true.
   */
  shown?: boolean;
};

/**
 * DAGEditorWithDocs provides a Monaco YAML editor with an integrated Schema Documentation sidebar.
 * It handles cursor position tracking, YAML path resolution, and sidebar toggle state.
 */
function DAGEditorWithDocs({
  value,
  onChange,
  readOnly = false,
  className,
  showDocsButton = true,
  defaultDocsOpen = true,
  headerActions,
  schema,
  modelUri,
  markers,
  quickFixSource,
  onEditorMount,
  referenceState,
  scopePath,
  changeBaseline,
  stepLenses,
  shown = true,
}: DAGEditorWithDocsProps) {
  const everShown = React.useRef(shown);
  if (shown) everShown.current = true;
  // Schema documentation sidebar state (remembers user preference)
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      const saved = localStorage.getItem('schema-sidebar-open');
      return saved === null ? defaultDocsOpen : saved === 'true';
    } catch {
      return defaultDocsOpen;
    }
  });

  const [cursorPosition, setCursorPosition] = useState<CursorPosition>({
    lineNumber: 1,
    column: 1,
  });

  // Debounce cursor position to avoid too many re-renders
  const debouncedCursorPosition = useDebouncedValue(cursorPosition, 150);

  // Get YAML path from cursor position
  const yamlPathInfo = useYamlCursorPath(
    value,
    debouncedCursorPosition.lineNumber,
    debouncedCursorPosition.column
  );

  const docsPath = useMemo(
    () => docsPathInScope(yamlPathInfo, scopePath),
    [yamlPathInfo, scopePath]
  );

  // Handle cursor position changes from the editor
  const handleCursorPositionChange = useCallback((position: CursorPosition) => {
    setCursorPosition(position);
  }, []);

  // Toggle sidebar and persist preference
  const toggleSidebar = useCallback(() => {
    setSidebarOpen((prev) => {
      const newValue = !prev;
      try {
        localStorage.setItem('schema-sidebar-open', String(newValue));
      } catch {
        // Ignore localStorage errors
      }
      return newValue;
    });
  }, []);

  // Keyboard shortcut for toggling docs sidebar (Ctrl+Shift+D)
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.ctrlKey && event.shiftKey && event.key === 'D') {
        event.preventDefault();
        toggleSidebar();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [toggleSidebar]);

  return (
    <div
      className={cn(
        'flex flex-col bg-surface border border-border rounded-lg overflow-hidden min-h-[300px] h-[70vh]',
        className
      )}
    >
      {/* Header with toggle button and optional actions */}
      {(showDocsButton || headerActions) && (
        <div className="flex-shrink-0 flex justify-between items-center p-2 border-b border-border">
          {showDocsButton ? (
            <I18nProps>
              <Button
                variant="secondary"
                size="xs"
                onClick={toggleSidebar}
                title="Toggle Schema Documentation (Ctrl+Shift+D)"
              >
                <BookOpen className="h-3.5 w-3.5" />
                <I18nText text={'Docs'} />
              </Button>
            </I18nProps>
          ) : (
            <div />
          )}
          {headerActions && (
            <div className="flex items-center gap-2">{headerActions}</div>
          )}
        </div>
      )}

      {/* Editor and Sidebar */}
      {/* In a narrow container the docs go under the editor, not beside it. */}
      <div className="@container flex-1 flex min-h-0 flex-col @[40rem]:flex-row">
        <div className="flex-1 min-w-0 min-h-0">
          <React.Suspense fallback={<LoadingIndicator fill />}>
            {everShown.current ? (
              <DAGEditor
                value={value}
                readOnly={readOnly}
                lineNumbers={true}
                onChange={readOnly ? undefined : onChange}
                onCursorPositionChange={handleCursorPositionChange}
                modelUri={modelUri}
                schema={schema}
                markers={markers}
                quickFixSource={quickFixSource}
                onEditorMount={onEditorMount}
                referenceState={referenceState}
                changeBaseline={changeBaseline}
                stepLenses={stepLenses}
              />
            ) : (
              <LoadingIndicator fill />
            )}
          </React.Suspense>
        </div>
        <SchemaDocSidebar
          className="h-2/5 w-full border-l-0 border-t @[40rem]:h-auto @[40rem]:w-80 @[40rem]:border-l @[40rem]:border-t-0"
          isOpen={sidebarOpen}
          onClose={toggleSidebar}
          path={docsPath.path}
          segments={docsPath.segments}
          yamlContent={value}
          schema={schema}
        />
      </div>
    </div>
  );
}

export default DAGEditorWithDocs;
