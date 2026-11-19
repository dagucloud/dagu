// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowLeft, Trash2 } from 'lucide-react';
import { useEffect, useMemo, useRef, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import type { DagIdentity } from '@/features/ai-endpoints/useEndpointChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type {
  DagDocModel,
  DagEditOp,
  ReferenceCandidate,
  StepView,
  ToolSelection,
  ValidationIssue,
} from './contracts';
import { FieldIssues } from './inspector/FieldIssues';
import { focusBox } from './inspector/focusField';
import { Section } from './inspector/Section';
import { BuiltinTools } from './inspector/sections/agentRun/BuiltinTools';
import { ToolEntrySections } from './inspector/sections/tools/ToolEntrySections';
import { YamlBox } from './inspector/sections/YamlBox';
import { issuesUnder, stepIssues } from './inspector/issues';
import { parseMappingYaml, stepYamlFromModel } from './inspector/stepYamlText';
import { KindBadge, KindIcon } from './tools/KindBadge';
import {
  entryIcon,
  kindIcon,
  kindLabel,
  kindOfCategory,
  type ToolKind,
} from './tools/kinds';
import { builtinToolsYaml, toolEntryYaml } from './tools/entryYaml';
import type { ToolEntryModel } from './tools/model';
import { entryPath } from './tools/model';
import { readStepTools } from './tools/parse';

export interface ToolPanelProps {
  model: DagDocModel;
  /** The step that holds the tool. */
  step: StepView;
  /** The name of the step, for the way back to it. */
  stepLabel: string;
  tool: ToolSelection;
  /** The issues of the step. */
  issues: ValidationIssue[];
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  /** The DAG that holds the step: the MCP server entries that it may use depend on it. */
  dag?: DagIdentity | undefined;
  onEdit(op: DagEditOp): void;
  /** Shows the dock of the step that holds this tool: the DAGs dock for a DAG tool, else the Tools dock. */
  onShowTools?(): void;
  /** Shows the step itself: its form. */
  onBack(): void;
  onClose(): void;
}

const ENTRY_FIELDS = ['name', 'params', 'url', 'headers', 'allow'];

/**
 * The issues of an entry that no section of the panel shows: the issues of
 * the entry itself and of a key that no section edits.
 */
function strayIssues(
  fieldIssues: Record<string, ValidationIssue[]>,
  path: string
): ValidationIssue[] {
  const owned = ENTRY_FIELDS.map((name) => `${path}.${name}`);
  return Object.entries(fieldIssues).flatMap(([key, list]) => {
    const inside =
      key === path || key.startsWith(`${path}.`) || key.startsWith(`${path}[`);
    const shown = owned.some(
      (field) =>
        key === field ||
        key.startsWith(`${field}.`) ||
        key.startsWith(`${field}[`)
    );
    return inside && !shown ? list : [];
  });
}

/** The tool entry that a selection names, or undefined when the step has no such entry. */
export function findToolEntry(
  step: StepView,
  tool: Pick<ToolSelection, 'category' | 'index'>
): ToolEntryModel | undefined {
  if (tool.category === 'builtin') return undefined;
  return readStepTools(step.value).entries.find(
    (entry) => entry.category === tool.category && entry.index === tool.index
  );
}

function entryTitle(
  ts: ReturnType<typeof useI18n>['ts'],
  entry: ToolEntryModel
): string {
  if (entry.kind === 'invalid') return ts('Invalid tool entry');
  return entry.label || entry.mcp?.host || kindLabel(ts, entry.kind);
}

/**
 * The right-hand panel of one tool entry of a step. It looks and works like
 * the step inspector: a title bar, collapsible sections with their issue
 * counts, the same fields, the issues under their field, and the YAML of the
 * entry in the last section. A DAG tool has General and Parameters. An MCP
 * server has General, Connection and Allowed tools. The
 * built-in tools of an agent step are one entry with one section. Every
 * change is an operation on the YAML, which stays the one source. The panel
 * is read-only when `readOnly` is set or the YAML has syntax errors. It
 * takes the focus on its first field when it opens, unless it is read-only.
 */
export function ToolPanel({
  model,
  step,
  stepLabel,
  tool,
  issues,
  candidates,
  readOnly,
  dag,
  onEdit,
  onShowTools,
  onBack,
  onClose,
}: ToolPanelProps): ReactElement {
  const { ts } = useI18n();
  const builtin = tool.category === 'builtin';
  const entry = useMemo(
    () => findToolEntry(step, tool),
    [step, tool.category, tool.index]
  );
  const grouped = useMemo(
    () => stepIssues(issues, step, ts),
    [issues, step, ts]
  );
  const fieldIssues = grouped.byField;
  const locked = readOnly || !model.ok || step.broken !== undefined;
  const bodyRef = useRef<HTMLDivElement>(null);
  // The panel opens on its first field, so that the new tool is ready to edit.
  useEffect(() => {
    if (locked) return;
    const box =
      bodyRef.current?.querySelector<HTMLElement>('[data-field-path]');
    if (box) focusBox(box);
    // Once for each tool: the host keys the panel by the selection.
  }, []);

  const missing = !builtin && !entry;
  if (missing) {
    return (
      <div
        data-testid="ve-tool-panel"
        className="flex min-h-0 flex-1 flex-col bg-card"
      >
        <PanelHeader
          title={<I18nText text="This tool no longer exists." />}
          closeLabel={ts('Close')}
          onClose={onClose}
        />
      </div>
    );
  }

  const kind: ToolKind = kindOfCategory(tool.category);
  const title = builtin
    ? ts('Built-in tools')
    : entryTitle(ts, entry as ToolEntryModel);
  const Icon = entry ? entryIcon(entry.kind) : kindIcon(kind);
  const path = entry ? entryPath(entry) : 'with.tools.builtin';
  const stepYaml = stepYamlFromModel(model, step);
  const total = builtin
    ? issuesUnder(fieldIssues, 'with.tools').length
    : issuesUnder(fieldIssues, path).length;
  const remove = () => {
    if (entry) {
      onEdit({
        kind: 'editStepTools',
        step: step.key,
        change: {
          kind: 'remove',
          category: entry.category,
          index: entry.index,
        },
      });
    } else {
      onEdit({
        kind: 'setStepField',
        step: step.key,
        path: ['with', 'tools', 'builtin'],
        value: undefined,
      });
    }
    onClose();
  };
  const yamlBox = entry ? (
    <YamlBox
      original={toolEntryYaml(
        stepYaml,
        entry.category,
        entry.index,
        entry.listForm === true
      )}
      readOnly={locked}
      ariaLabel={ts('Tool YAML')}
      check={(text) =>
        parseMappingYaml(text, 'The tool entry must be a YAML mapping.')
      }
      onApply={(text) =>
        onEdit({
          kind: 'editStepTools',
          step: step.key,
          change: {
            kind: 'replace',
            category: entry.category,
            index: entry.index,
            yaml: text,
          },
        })
      }
    />
  ) : (
    <YamlBox
      original={builtinToolsYaml(stepYaml)}
      readOnly={locked}
      ariaLabel={ts('Tool YAML')}
      check={(text) =>
        parseMappingYaml(text, 'The tool entry must be a YAML mapping.')
      }
      onApply={(text) =>
        onEdit({
          kind: 'setStepField',
          step: step.key,
          path: ['with', 'tools', 'builtin'],
          value: parseMappingYaml(text, '').value,
        })
      }
    />
  );

  return (
    <div
      data-testid="ve-tool-panel"
      data-kind={kind}
      className="flex min-h-0 flex-1 flex-col bg-card"
    >
      <PanelHeader closeLabel={ts('Close')} onClose={onClose}>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <KindIcon kind={kind} icon={Icon} className="size-5" />
          <div className="min-w-0 flex-1 leading-tight">
            <h2 className="truncate text-base font-semibold text-foreground">
              {title}
            </h2>
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
              <KindBadge kind={kind} />
              {onShowTools && (
                <button
                  type="button"
                  data-testid="ve-tool-all"
                  title={
                    tool.category === 'dags'
                      ? ts('Show the DAGs of the step {name}', {
                          name: stepLabel,
                        })
                      : ts('Show the tools of the step {name}', {
                          name: stepLabel,
                        })
                  }
                  className="flex shrink-0 items-center gap-0.5 rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={onShowTools}
                >
                  <ArrowLeft className="size-3 shrink-0" aria-hidden="true" />
                  {tool.category === 'dags' ? ts('DAGs') : ts('Tools')}
                </button>
              )}
              <button
                type="button"
                data-testid="ve-tool-back"
                title={ts('Show the step {name}', { name: stepLabel })}
                className="flex min-w-0 items-center gap-0.5 rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={onBack}
              >
                <ArrowLeft className="size-3 shrink-0" aria-hidden="true" />
                <span className="truncate">
                  {ts('Back to step {name}', { name: stepLabel })}
                </span>
              </button>
            </div>
          </div>
        </div>
        {total > 0 && (
          <span
            className="rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium text-destructive"
            aria-label={ts('{count} issues', { count: total })}
          >
            {total}
          </span>
        )}
        {!locked && (
          <IconButton
            tone="destructive"
            icon={Trash2}
            label={ts('Remove tool {name}', { name: title })}
            onClick={remove}
          />
        )}
      </PanelHeader>
      <div ref={bodyRef} className="min-h-0 flex-1 overflow-auto">
        {(!model.ok || step.broken !== undefined) && (
          <NoticeLine
            className="p-1"
            notices={[
              {
                id: 'tool-step-broken',
                tone: 'warning',
                priority: 0,
                message: (
                  <>
                    <I18nText text="Fix the YAML errors to edit this step." />
                    {step.broken !== undefined && ` ${step.broken}`}
                  </>
                ),
              },
            ]}
          />
        )}
        {entry && (
          <FieldIssues
            issues={strayIssues(fieldIssues, path)}
            className="border-b border-border px-2 py-1"
          />
        )}
        {entry ? (
          <ToolEntrySections
            entry={entry}
            path={path}
            fieldIssues={fieldIssues}
            candidates={candidates}
            readOnly={locked}
            dag={dag}
            onChange={(inside, value) =>
              onEdit({
                kind: 'editStepTools',
                step: step.key,
                change: {
                  kind: 'update',
                  category: entry.category,
                  index: entry.index,
                  path: inside,
                  value,
                },
              })
            }
          />
        ) : (
          <Section
            title={<I18nText text="Built-in tools" />}
            count={issuesUnder(fieldIssues, 'with.tools').length}
          >
            <BuiltinTools
              step={step}
              readOnly={locked}
              onEdit={onEdit}
              fieldIssues={fieldIssues}
              candidates={candidates}
              heading={false}
            />
          </Section>
        )}
        <Section title={<I18nText text="YAML" />} defaultOpen={false}>
          {yamlBox}
        </Section>
      </div>
    </div>
  );
}
