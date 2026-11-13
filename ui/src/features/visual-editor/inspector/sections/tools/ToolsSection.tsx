// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, Trash2 } from 'lucide-react';
import { useMemo, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ToolSelection } from '../../../contracts';
import { KindBadge, KindIcon } from '../../../tools/KindBadge';
import { entryIcon, kindOfCategory } from '../../../tools/kinds';
import {
  DEFAULT_MAX_TOOLS,
  DEFAULT_TOOL_RESULT_MAX_BYTES,
  entryKey,
  entryPath,
  type ToolEntryModel,
} from '../../../tools/model';
import { readStepTools } from '../../../tools/parse';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { TextField } from '../../TextField';
import type { SectionProps } from '../../types';

export interface ToolsSectionProps extends SectionProps {
  /** Selects a tool entry: its panel opens at the right. */
  onOpenTool?(tool: ToolSelection): void;
  /**
   * Opens the dock that picks a DAG (`dags`) or a tool (`tools`: an MCP server
   * or a built-in tool) to add: the panel of the new tool follows.
   */
  onAddTool?(slot: 'tools' | 'dags'): void;
  /**
   * Only the entries and the add button: no empty note and no limits. The
   * form of an agent step sets it, as its built-in tools follow the list.
   */
  entriesOnly?: boolean;
}

function entryTitle(entry: ToolEntryModel): string {
  return entry.label || '…';
}

/** Whole-number text of a limit, or null when the text is not one. */
function parseLimit(text: string): number | undefined | null {
  const trimmed = text.trim();
  if (trimmed === '') return undefined;
  return /^\d+$/.test(trimmed) ? Number(trimmed) : null;
}

export function LimitField({
  label,
  value,
  fallback,
  hint,
  readOnly,
  onSet,
}: {
  label: string;
  value: number | undefined;
  fallback: number;
  hint?: string;
  readOnly: boolean;
  onSet(value: number | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const [bad, setBad] = useState(false);
  return (
    <TextField
      label={label}
      value={value === undefined ? '' : String(value)}
      placeholder={ts('Default: {count}', { count: fallback })}
      readOnly={readOnly}
      issues={
        bad
          ? [
              {
                severity: ValidationIssueSeverity.error,
                source: ValidationIssueSource.build,
                message: hint ?? ts('Enter a whole number'),
              },
            ]
          : undefined
      }
      onCommit={(text) => {
        const parsed = parseLimit(text);
        setBad(parsed === null);
        if (parsed !== null) onSet(parsed);
      }}
    />
  );
}

/**
 * Tools of a `chat.completion` step or an `agent.run` step: a compact list of
 * the entries of `with.dags` and of `with.tools.mcp`, in this order. A row has
 * the icon and the badge of its kind, the name, the count of its issues and a
 * button that removes it. A click on a row selects the entry: its panel opens
 * at the right, and the panel edits it as the step form edits a step. The add
 * buttons open the dock that picks a DAG and the dock that picks a tool, and
 * the panel of the new tool follows. An agent step also uses the list
 * (`entriesOnly`). A chat step has the two limits under an Advanced
 * disclosure. The list form `tools: [name]` shows as `dags` entries with a
 * note; the first change of an entry rewrites it to the key `dags`. Every
 * change is an `editStepTools` operation on the YAML, which stays the one
 * source: the list is read again from it.
 */
export function ToolsSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  onOpenTool,
  onAddTool,
  entriesOnly = false,
}: ToolsSectionProps): ReactElement {
  const { ts } = useI18n();
  const tools = useMemo(() => readStepTools(step.value), [step.value]);
  return (
    <div className="space-y-2" data-field-path="with.tools">
      {tools.listForm && (
        <p
          className="text-xs text-warning"
          data-testid="ve-tools-list-form-note"
        >
          <I18nText text="The list form of tools is deprecated. Use the dags key." />
        </p>
      )}
      {tools.entries.length === 0 && !entriesOnly && (
        <p className="text-xs text-muted-foreground">
          <I18nText text="No tools yet" />
        </p>
      )}
      <ol className="space-y-1">
        {tools.entries.map((entry) => {
          const label = entryTitle(entry);
          const issues = issuesUnder(fieldIssues, entryPath(entry)).length;
          return (
            <li
              key={entryKey(entry)}
              data-testid="ve-tool-entry"
              data-kind={entry.kind}
              data-field-path={entryPath(entry)}
              className="flex items-center gap-1 rounded border border-border px-1 py-0.5"
            >
              <button
                type="button"
                aria-label={ts('Edit tool {name}', { name: label })}
                disabled={!onOpenTool}
                className="flex min-w-0 flex-1 items-center gap-1.5 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={() =>
                  onOpenTool?.({
                    step: step.key,
                    category: entry.category,
                    index: entry.index,
                  })
                }
              >
                <KindIcon
                  kind={kindOfCategory(entry.category)}
                  icon={entryIcon(entry.kind)}
                  className="size-3.5"
                />
                <span className="min-w-0 flex-1 truncate text-xs font-medium">
                  {label}
                </span>
                <KindBadge kind={kindOfCategory(entry.category)} />
                {issues > 0 && (
                  <span
                    className="rounded bg-destructive/10 px-1 text-[10px] font-medium text-destructive"
                    aria-label={ts(
                      issues === 1 ? '{count} issue' : '{count} issues',
                      { count: issues }
                    )}
                  >
                    {issues}
                  </span>
                )}
              </button>
              {!readOnly && (
                <IconButton
                  size="sm"
                  tone="destructive"
                  label={ts('Remove tool {name}', { name: label })}
                  icon={Trash2}
                  onClick={() =>
                    onEdit({
                      kind: 'editStepTools',
                      step: step.key,
                      change: {
                        kind: 'remove',
                        category: entry.category,
                        index: entry.index,
                      },
                    })
                  }
                />
              )}
            </li>
          );
        })}
      </ol>
      <div data-field-path="with.dags">
        <FieldIssues issues={fieldIssues['with.dags']} />
      </div>
      {!entriesOnly && <FieldIssues issues={fieldIssues['with.tools']} />}
      {onAddTool && (
        <div className="flex flex-wrap gap-1">
          <Button
            type="button"
            variant="outline"
            className="h-7 gap-1 px-2 text-xs"
            disabled={readOnly}
            data-testid="ve-dag-add"
            onClick={() => onAddTool('dags')}
          >
            <Plus className="size-3.5" aria-hidden />
            <I18nText text="Add a DAG" />
          </Button>
          <Button
            type="button"
            variant="outline"
            className="h-7 gap-1 px-2 text-xs"
            disabled={readOnly}
            data-testid="ve-tool-add"
            onClick={() => onAddTool('tools')}
          >
            <Plus className="size-3.5" aria-hidden />
            <I18nText text="Add a tool" />
          </Button>
        </div>
      )}
      {!entriesOnly && (
        <details className="space-y-1" data-testid="ve-tool-advanced">
          <summary className="cursor-pointer text-xs font-medium text-muted-foreground">
            <I18nText text="Advanced" />
          </summary>
          <div className="space-y-1 pt-1" data-field-path="with.max_tools">
            <LimitField
              label={ts('Maximum number of tools')}
              value={tools.maxTools}
              fallback={DEFAULT_MAX_TOOLS}
              readOnly={readOnly}
              onSet={(value) =>
                onEdit({
                  kind: 'editStepTools',
                  step: step.key,
                  change: { kind: 'setLimit', key: 'max_tools', value },
                })
              }
            />
            <FieldIssues issues={issuesUnder(fieldIssues, 'with.max_tools')} />
            <div data-field-path="with.tool_result_max_bytes">
              <LimitField
                label={ts(
                  'Maximum size of a tool result, in bytes (0: no limit)'
                )}
                value={tools.resultMaxBytes}
                fallback={DEFAULT_TOOL_RESULT_MAX_BYTES}
                readOnly={readOnly}
                onSet={(value) =>
                  onEdit({
                    kind: 'editStepTools',
                    step: step.key,
                    change: {
                      kind: 'setLimit',
                      key: 'tool_result_max_bytes',
                      value,
                    },
                  })
                }
              />
              <FieldIssues
                issues={issuesUnder(fieldIssues, 'with.tool_result_max_bytes')}
              />
            </div>
          </div>
        </details>
      )}
    </div>
  );
}
