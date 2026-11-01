// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The YAML panel of the whole file: one component for the editor and for the
 * run canvas of the workspace, so that the two never differ. The host gives
 * the text and what is specific to it (tools, editing, markers).
 *
 * @module features/dags/components/dag-editor
 */
import { Lock } from 'lucide-react';
import type { ComponentProps, ReactNode } from 'react';
import { CopyButton } from '@/components/ui/copy-button';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import { shortRunId } from '@/lib/shortRunId';
import { cn, FOCUS_RING } from '@/lib/utils';
import DAGEditorWithDocs from './DAGEditorWithDocs';
import {
  NodeDetailsFrame,
  type NodeDetailsFrameProps,
} from './NodeDetailsFrame';

type EditorProps = ComponentProps<typeof DAGEditorWithDocs>;

export interface YamlFileBodyProps
  extends Pick<
    EditorProps,
    | 'schema'
    | 'markers'
    | 'quickFixSource'
    | 'modelUri'
    | 'onEditorMount'
    | 'referenceState'
    | 'scopePath'
    | 'changeBaseline'
    | 'stepLenses'
    | 'showDocsButton'
    | 'shown'
  > {
  /** The YAML of the file: the editor shows it and the Copy button copies it. */
  text: string;
  /** No edit: the editor takes no input and `onChange` is never called. */
  readOnly: boolean;
  onChange?(text: string): void;
  /** The frame is the border of the editor: no rounded corners. */
  inFrame?: boolean;
  /** The form of a step shows in place of the text, in the same body. */
  formMode?: boolean;
  /** The root where the form of a step renders. */
  formRootRef?(element: HTMLDivElement | null): void;
}

/**
 * The text of the file in the Monaco editor, with the Copy YAML button over
 * its top right. It stays mounted while the form shows in its place, to keep
 * the undo history, the cursor and the scroll position.
 */
export function YamlFileBody({
  text,
  readOnly,
  onChange,
  inFrame = false,
  formMode = false,
  formRootRef,
  showDocsButton = true,
  ...editor
}: YamlFileBodyProps) {
  const { ts } = useI18n();
  return (
    <>
      <div
        hidden={formMode}
        data-testid="yaml-file-body"
        className="relative flex min-h-0 flex-1 flex-col"
      >
        <CopyButton
          text={text}
          label={ts('Copy YAML')}
          copiedLabel={ts('YAML copied')}
          // Over the top right of the editor, clear of its scrollbar.
          className="absolute right-4 top-1.5 z-10 border-border bg-card/80 backdrop-blur-sm"
        />
        <DAGEditorWithDocs
          // The panel is narrow: the docs open on request.
          defaultDocsOpen={false}
          showDocsButton={showDocsButton}
          value={text}
          readOnly={readOnly}
          onChange={
            readOnly || !onChange ? undefined : (next) => onChange(next ?? '')
          }
          className={cn(
            'h-auto min-h-0 flex-1',
            inFrame && 'rounded-none border-0'
          )}
          {...editor}
        />
      </div>
      <div
        ref={formRootRef}
        hidden={!formMode}
        className="flex min-h-0 flex-1 flex-col"
      />
    </>
  );
}

/**
 * The mark of a definition that cannot be edited: a lock with a tooltip, not
 * a chip. With `runId`, the tooltip names the run whose definition it is.
 */
export function ReadOnlyMarker({
  runId,
  className,
}: {
  runId?: string;
  className?: string;
}) {
  const { ts } = useI18n();
  const label = runId
    ? ts('Read-only: definition of run {id}', { id: runId })
    : ts('Read-only');
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          role="img"
          tabIndex={0}
          aria-label={label}
          data-testid="readonly-marker"
          className={cn(
            'inline-flex size-7 shrink-0 items-center justify-center rounded text-muted-foreground [&_svg]:size-4',
            FOCUS_RING,
            className
          )}
        >
          <Lock aria-hidden="true" />
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  );
}

export interface YamlFilePanelProps
  extends Omit<NodeDetailsFrameProps, 'children' | 'yamlTools' | 'formMode'>,
    Omit<YamlFileBodyProps, 'inFrame' | 'formRootRef' | 'formMode'> {
  /** Text tools of the header: the editor gives them, a read-only view does not. */
  tools?: ReactNode;
  /** The run whose definition the panel shows: its identity follows the title. */
  runRef?: { id: string };
  /** After the title and the run identity: extra markers of the host. */
  note?: ReactNode;
  /** The step form replaces the text. */
  formMode: boolean;
  /** The root where the form renders. */
  formRootRef?(element: HTMLDivElement | null): void;
}

/**
 * The right panel that shows the whole definition: the 48 px header with
 * the YAML title and the close button, then the editor. Its URL flag is
 * `?ndv=docked`.
 */
export function YamlFilePanel({
  text,
  readOnly,
  onChange,
  showDocsButton = false,
  schema,
  markers,
  quickFixSource,
  modelUri,
  onEditorMount,
  referenceState,
  scopePath,
  changeBaseline,
  stepLenses,
  shown,
  tools,
  runRef,
  note,
  formMode,
  formRootRef,
  ...frame
}: YamlFilePanelProps) {
  // Read-only is a lock in every host; a run shows its identity before it.
  const headerNote =
    runRef || readOnly || note ? (
      <>
        {runRef && (
          <span
            data-testid="yaml-run-ref"
            title={runRef.id}
            className="min-w-0 truncate font-mono text-xs text-muted-foreground"
          >
            {shortRunId(runRef.id)}
          </span>
        )}
        {readOnly && <ReadOnlyMarker runId={runRef?.id} />}
        {note}
      </>
    ) : undefined;
  return (
    <NodeDetailsFrame
      {...frame}
      formMode={formMode}
      yamlTools={tools}
      headerNote={headerNote}
    >
      <YamlFileBody
        text={text}
        readOnly={readOnly}
        onChange={onChange}
        inFrame
        formMode={formMode}
        formRootRef={formRootRef}
        showDocsButton={showDocsButton}
        schema={schema}
        markers={markers}
        quickFixSource={quickFixSource}
        modelUri={modelUri}
        onEditorMount={onEditorMount}
        referenceState={referenceState}
        scopePath={scopePath}
        changeBaseline={changeBaseline}
        stepLenses={stepLenses}
        // The editor loads when the panel first opens.
        shown={shown ?? frame.state !== null}
      />
    </NodeDetailsFrame>
  );
}
