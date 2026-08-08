import { KILN_DARK, KILN_LIGHT, registerKilnThemes } from '@/lib/monaco-theme';
import { cn } from '@/lib/utils';
import MonacoEditor, { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import { useEffect, useRef } from 'react';

loader.config({ monaco });

registerKilnThemes();

export type MarkdownEditorInstance = monaco.editor.IStandaloneCodeEditor;

type Props = {
  value: string;
  onChange?: (value?: string) => void;
  readOnly?: boolean;
  className?: string;
  onEditorMount?: (editor: MarkdownEditorInstance) => void;
};

function MarkdownEditor({
  value,
  onChange,
  readOnly = false,
  className,
  onEditorMount,
}: Props) {
  const editorRef = useRef<MarkdownEditorInstance | null>(null);

  useEffect(() => {
    return () => {
      editorRef.current?.dispose();
    };
  }, []);

  useEffect(() => {
    if (editorRef.current) {
      const newTheme = document.documentElement.classList.contains('dark')
        ? KILN_DARK
        : KILN_LIGHT;
      monaco.editor.setTheme(newTheme);
    }
  }, []);

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

  const editorDidMount = (editor: MarkdownEditorInstance) => {
    editorRef.current = editor;
    editor.onKeyDown((e) => {
      if (e.code === 'KeyF' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.stopPropagation();
      }
    });
    onEditorMount?.(editor);
  };

  const isDarkMode =
    typeof window !== 'undefined' &&
    document.documentElement.classList.contains('dark');

  return (
    <div className={cn('h-full', className)}>
      <MonacoEditor
        height="100%"
        language="markdown"
        theme={isDarkMode ? KILN_DARK : KILN_LIGHT}
        value={value}
        onChange={readOnly ? undefined : onChange}
        onMount={editorDidMount}
        options={{
          readOnly,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          wordWrap: 'on',
          lineNumbers: 'on',
          glyphMargin: false,
          fontFamily:
            "'JetBrains Mono', 'Fira Code', Menlo, Monaco, 'Courier New', monospace",
          fontSize: 13,
          padding: { top: 8, bottom: 8 },
          quickSuggestions: false,
          renderValidationDecorations: 'off',
        }}
      />
    </div>
  );
}

export default MarkdownEditor;
