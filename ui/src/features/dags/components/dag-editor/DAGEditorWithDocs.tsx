/**
 * DAGEditorWithDocs component provides a Monaco editor with Schema Documentation sidebar.
 * This is a shared component used by both the editable DAGSpec and readonly DAGSpecReadOnly.
 *
 * @module features/dags/components/dag-editor
 */
import React, { useState, useCallback, useEffect } from 'react';
import { BookOpen } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { JSONSchema } from '@/lib/schema-utils';
import { Button } from '@/components/ui/button';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { useDebouncedValue } from '../../../../hooks/useDebouncedValue';
import { useYamlCursorPath } from '../../../../hooks/useYamlCursorPath';
import type { CursorPosition } from './DAGEditor';
import { SchemaDocSidebar } from './SchemaDocSidebar';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

const DAGEditor = React.lazy(() => import('./DAGEditor'));

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
  /** Additional content to render in the header (e.g., save button) */
  headerActions?: React.ReactNode;
  /** Optional document-specific schema */
  schema?: JSONSchema | null;
  /** Stable model URI used for per-document schema association */
  modelUri?: string;
  /** Server-side validation markers forwarded to the editor */
  markers?: import('monaco-editor').editor.IMarkerData[];
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
  headerActions,
  schema,
  modelUri,
  markers,
}: DAGEditorWithDocsProps) {
  // Schema documentation sidebar state (default open, remembers user preference)
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try {
      const saved = localStorage.getItem('schema-sidebar-open');
      // Default to open if no preference saved
      return saved === null ? true : saved === 'true';
    } catch {
      return true;
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
            <I18nProps><Button
              variant="secondary"
              size="xs"
              onClick={toggleSidebar}
              title="Toggle Schema Documentation (Ctrl+Shift+D)"
            >
              <BookOpen className="h-3.5 w-3.5" />
              <I18nText text={"Docs"} />
            </Button></I18nProps>
          ) : (
            <div />
          )}
          {headerActions}
        </div>
      )}

      {/* Editor and Sidebar */}
      <div className="flex-1 flex min-h-0">
        <div className="flex-1 min-w-0">
          <React.Suspense fallback={<LoadingIndicator />}>
            <DAGEditor
              value={value}
              readOnly={readOnly}
              lineNumbers={true}
              onChange={readOnly ? undefined : onChange}
              onCursorPositionChange={handleCursorPositionChange}
              modelUri={modelUri}
              schema={schema}
              markers={markers}
            />
          </React.Suspense>
        </div>
        <SchemaDocSidebar
          isOpen={sidebarOpen}
          onClose={toggleSidebar}
          path={yamlPathInfo.path}
          segments={yamlPathInfo.segments}
          yamlContent={value}
          schema={schema}
        />
      </div>
    </div>
  );
}

export default DAGEditorWithDocs;
