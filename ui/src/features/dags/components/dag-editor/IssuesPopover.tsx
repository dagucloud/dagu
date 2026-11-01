// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The issues button of the canvas toolbar: a triangle and the count of the
 * issues. It shows only while the document has errors, and it takes the place
 * of the Save button. Its popover lists the validation warnings, the
 * validation errors and the value-reference notices of the shown document.
 *
 * @module features/dags/components/dag-editor
 */
import { AlertTriangle, Wand2 } from 'lucide-react';
import { issueRows, type IssueRow } from './issueRows';
import React from 'react';
import { createPortal } from 'react-dom';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import type { QuickFix } from '@/features/visual-editor/doc/quickFixes';
import {
  plainIssueMessage,
  refineIssue,
  relativeFieldPath,
} from '@/features/visual-editor/inspector/issues';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { ValueReferenceNoticesButton } from '../value-reference-notices';

type ValidationIssue = components['schemas']['ValidationIssue'];
type ValueReferenceNotice = components['schemas']['ValueReferenceNotice'];

export interface IssuesPopoverProps {
  /** Live warnings while the text is dirty, else the saved ones. */
  warnings: string[];
  /** Live errors while the text is dirty, else the saved ones. */
  errors: string[];
  valueReferences?: ValueReferenceNotice[];
  /** Structured issues of the same validation: they give the step of a row. */
  issues?: ValidationIssue[];
  /**
   * Called with the server step name of a row, and the field path in the
   * step (`with.url`) when the issue names one. Without it, rows are text.
   */
  onRevealStep?(stepName: string, fieldPath?: string): void;
  /**
   * Called with the structured issue of a row, when it has one. It takes
   * the place of `onRevealStep` for that row: the host moves the cursor to
   * the line of the issue and shows its step.
   */
  onRevealIssue?(issue: ValidationIssue): void;
  /** The line of an issue in the text; the rows are sorted by it. */
  lineOf?(issue: ValidationIssue): number | null;
  /** Opens the popover each time that the number changes. */
  openRequest?: number;
  /** The quick fix of an issue, when it has one: its row shows a button. */
  quickFixOf?(issue: ValidationIssue): QuickFix | undefined;
  /** Called with the fixes to apply as one edit. */
  onQuickFix?(fixes: QuickFix[]): void;
}

const POSITION_PREFIX = /^\[\d+:\d+\]\s*/;
/** The step key at the start of a DAG field path: `steps[3]`, `handler_on.exit`. */
const STEP_KEY_PREFIX = /^(steps\[\d+\]|handler_on\.[^.[]+)\./;

interface StepTarget {
  step: string;
  field?: string;
}

interface ShownRow {
  message: string;
  target?: StepTarget;
  issue?: ValidationIssue;
  line?: number;
  fix?: QuickFix;
}

/** The field path inside its step, as the step form names it. */
function fieldInStep(
  stepPath: string | undefined,
  fieldPath: string | undefined
): string | undefined {
  const key = stepPath ?? (fieldPath && STEP_KEY_PREFIX.exec(fieldPath)?.[1]);
  return (key && relativeFieldPath(key, fieldPath)) || undefined;
}

function stepOnly(target: StepTarget | undefined): StepTarget | undefined {
  return target && { step: target.step };
}

/** Finds the step, and the field, of a message through the structured issues. */
function stepLocator(issues: ValidationIssue[]) {
  const byMessage = new Map<string, StepTarget>();
  const byField = new Map<string, StepTarget>();
  const keep = (
    map: Map<string, StepTarget>,
    key: string,
    target: StepTarget
  ) => {
    if (!map.has(key)) {
      map.set(key, target);
    }
  };
  for (const issue of issues) {
    if (!issue.stepName) {
      continue;
    }
    const target = {
      step: issue.stepName,
      field: fieldInStep(issue.stepPath, issue.fieldPath),
    };
    keep(byMessage, issue.message, target);
    keep(byMessage, issue.message.replace(POSITION_PREFIX, ''), target);
    if (issue.fieldPath) {
      keep(byField, issue.fieldPath, target);
    }
  }
  return (message: string, fieldPath?: string): StepTarget | undefined => {
    const found =
      byMessage.get(message) ??
      byMessage.get(message.replace(POSITION_PREFIX, ''));
    if (found) {
      return found;
    }
    return fieldPath ? byField.get(fieldPath) : undefined;
  };
}

export function IssuesPopover({
  warnings,
  errors,
  valueReferences = [],
  issues = [],
  onRevealStep,
  onRevealIssue,
  lineOf,
  openRequest = 0,
  quickFixOf,
  onQuickFix,
}: IssuesPopoverProps) {
  const { ts } = useI18n();
  const [open, setOpen] = React.useState(false);
  const anchorRef = React.useRef<HTMLButtonElement>(null);
  const panelId = React.useId();
  const rows = React.useMemo(
    () => issueRows({ warnings, errors, issues }),
    [warnings, errors, issues]
  );
  const count =
    rows.warnings.length + rows.errors.length + valueReferences.length;
  // Warnings alone show no button: the toolbar counts them on Save.
  const shownCount = rows.errors.length > 0 ? count : 0;

  // A count that falls to zero is announced too, once there were issues.
  const [hadIssues, setHadIssues] = React.useState(shownCount > 0);
  React.useEffect(() => {
    if (shownCount > 0) {
      setHadIssues(true);
    } else {
      setOpen(false);
    }
  }, [shownCount]);

  const stepOf = React.useMemo(() => stepLocator(issues), [issues]);

  // A request from the host (Ctrl+S with errors) opens the list.
  const handledRequest = React.useRef(openRequest);
  React.useEffect(() => {
    if (openRequest === handledRequest.current) return;
    handledRequest.current = openRequest;
    if (shownCount > 0) setOpen(true);
  }, [openRequest, shownCount]);

  const shown = React.useCallback(
    (list: IssueRow[]): ShownRow[] => {
      const result = list.map((row, index) => {
        // The issue of the row names its step: two steps can share a message.
        const target = row.issue?.stepName
          ? {
              step: row.issue.stepName,
              field: fieldInStep(row.issue.stepPath, row.issue.fieldPath),
            }
          : stepOf(row.message);
        const line = row.issue ? lineOf?.(row.issue) : undefined;
        return {
          index,
          row: {
            message: row.issue
              ? refineIssue(row.issue, ts).message
              : row.message,
            target,
            issue: row.issue,
            line: line ?? undefined,
            fix: row.issue ? quickFixOf?.(row.issue) : undefined,
          } as ShownRow,
        };
      });
      // By line; a message without a line is about the whole file.
      return result
        .sort(
          (a, b) => (a.row.line ?? 0) - (b.row.line ?? 0) || a.index - b.index
        )
        .map((item) => item.row);
    },
    [lineOf, quickFixOf, stepOf, ts]
  );
  const errorRows = React.useMemo(() => shown(rows.errors), [shown, rows]);
  const warningRows = React.useMemo(() => shown(rows.warnings), [shown, rows]);
  const close = React.useCallback(() => setOpen(false), []);
  // Each fix once: one fix can answer several issues of a step.
  const fixes = React.useMemo(
    () => [
      ...new Set(
        [...errorRows, ...warningRows].flatMap((row) =>
          row.fix ? [row.fix] : []
        )
      ),
    ],
    [errorRows, warningRows]
  );
  const applyFixes = onQuickFix
    ? (list: QuickFix[]) => {
        onQuickFix(list);
      }
    : undefined;
  const reveal =
    onRevealStep || onRevealIssue
      ? (row: ShownRow) => {
          setOpen(false);
          if (row.issue && onRevealIssue) {
            onRevealIssue(row.issue);
          } else if (row.target && onRevealStep) {
            if (row.target.field) {
              onRevealStep(row.target.step, row.target.field);
            } else {
              onRevealStep(row.target.step);
            }
          }
        }
      : undefined;
  // A row is a button only when `reveal` acts on it.
  const canReveal = (row: ShownRow) =>
    row.issue && onRevealIssue
      ? row.target !== undefined || row.line !== undefined
      : row.target !== undefined && !!onRevealStep;

  const announcement =
    shownCount > 0
      ? ts(shownCount === 1 ? '{count} issue' : '{count} issues', {
          count: shownCount,
        })
      : hadIssues
        ? ts('No issues')
        : '';

  return (
    <>
      <span role="status" aria-live="polite" className="sr-only">
        {announcement}
      </span>
      {shownCount > 0 && (
        <>
          <Button
            ref={anchorRef}
            type="button"
            variant="outline"
            size="xs"
            aria-haspopup="dialog"
            aria-expanded={open}
            aria-controls={panelId}
            aria-label={ts('Issues ({count})', { count: shownCount })}
            title={ts('Fix the issues before you save ({count})', {
              count: shownCount,
            })}
            data-testid="issues-slot"
            // The height of the Save button, which this one replaces. The
            // toolbar gives both the same box, so that nothing moves. The red
            // is the one of the trash button: the destructive token.
            className="h-7 justify-start gap-1 border-destructive px-2 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={() => setOpen((open) => !open)}
          >
            <AlertTriangle className="h-3.5 w-3.5" aria-hidden="true" />
            <span
              aria-hidden="true"
              data-testid="issues-slot-count"
              className="text-xs font-semibold tabular-nums leading-none"
            >
              {shownCount}
            </span>
          </Button>
          <AnchoredPopover
            id={panelId}
            open={open}
            onClose={close}
            anchorRef={anchorRef}
            label={ts('Issues')}
            // The value-reference dialog lives in the panel: it must stay
            // mounted while the dialog shows.
            keepMounted
          >
            {applyFixes && fixes.length > 1 && (
              <div className="flex justify-end px-2 pt-1">
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  data-testid="issues-fix-all"
                  title={ts('Apply every quick fix as one edit')}
                  onClick={() => applyFixes(fixes)}
                >
                  <Wand2 className="h-3.5 w-3.5" aria-hidden="true" />
                  {ts('Fix all ({count})', { count: fixes.length })}
                </Button>
              </div>
            )}
            <IssueGroup
              title={ts('Warnings')}
              rows={warningRows}
              onReveal={reveal}
              canReveal={canReveal}
              onFix={applyFixes}
            />
            <IssueGroup
              title={ts('Errors')}
              tone="error"
              rows={errorRows}
              onReveal={reveal}
              canReveal={canReveal}
              onFix={applyFixes}
            />
            <IssueGroup
              title={ts('Value references')}
              rows={valueReferences.map((notice) => ({
                message: notice.message,
                // A notice is about a reference in a value: its row shows
                // the step only.
                target: stepOnly(stepOf(notice.message, notice.fieldPath)),
              }))}
              onReveal={reveal}
              canReveal={canReveal}
            >
              {/* The dialog opens over the page: the popover closes first. */}
              <div className="px-2 pt-1" onClickCapture={close}>
                <ValueReferenceNoticesButton
                  notices={valueReferences}
                  label="Details"
                  description={ts(
                    'Value-reference notices produced while loading this spec.'
                  )}
                  variant="outline"
                />
              </div>
            </IssueGroup>
          </AnchoredPopover>
        </>
      )}
    </>
  );
}

function IssueGroup({
  title,
  rows,
  tone = 'warning',
  onReveal,
  canReveal,
  onFix,
  children,
}: {
  title: string;
  rows: ShownRow[];
  tone?: 'warning' | 'error';
  onReveal?(row: ShownRow): void;
  /** True when `onReveal` acts on the row; any other row is text. */
  canReveal?(row: ShownRow): boolean;
  onFix?(fixes: QuickFix[]): void;
  children?: React.ReactNode;
}) {
  const { ts } = useI18n();
  const headingId = React.useId();
  if (rows.length === 0) {
    return null;
  }
  return (
    <section aria-labelledby={headingId} className="py-1">
      <h3
        id={headingId}
        className={cn(
          'px-2 py-1 text-xs font-semibold',
          tone === 'error' ? 'text-destructive' : 'text-warning'
        )}
      >
        {title} ({rows.length})
      </h3>
      <ul className="space-y-0.5">
        {rows.map((row, index) => {
          const meta = [
            row.target?.step,
            row.line !== undefined
              ? ts('line {line}', { line: row.line })
              : undefined,
          ]
            .filter(Boolean)
            .join(' \u00b7 ');
          const actionable = onReveal && canReveal?.(row);
          return (
            <li
              key={`${index}:${row.message}`}
              className="flex items-start gap-1"
            >
              <div className="min-w-0 flex-1">
                {actionable ? (
                  <button
                    type="button"
                    title={
                      row.target
                        ? ts('Show step {name}', { name: row.target.step })
                        : undefined
                    }
                    className="w-full rounded-md px-2 py-1 text-left text-xs hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                    onClick={() => onReveal(row)}
                  >
                    <span className="block whitespace-normal break-words">
                      {plainIssueMessage(row.message)}
                    </span>
                    <span className="block font-mono text-[11px] text-muted-foreground">
                      {meta}
                    </span>
                  </button>
                ) : (
                  <p className="whitespace-normal break-words px-2 py-1 text-xs">
                    <span className="block">
                      {plainIssueMessage(row.message)}
                    </span>
                    {(meta || tone === 'error') && (
                      <span className="block text-[11px] text-muted-foreground">
                        {meta || ts('Workflow-level')}
                      </span>
                    )}
                  </p>
                )}
              </div>
              {row.fix && onFix && (
                <button
                  type="button"
                  data-testid="issue-quick-fix"
                  aria-label={ts('Quick fix: {label}', {
                    label: ts(row.fix.message, row.fix.params),
                  })}
                  title={ts(row.fix.message, row.fix.params)}
                  className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={() => onFix([row.fix!])}
                >
                  <Wand2 className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </li>
          );
        })}
      </ul>
      {children}
    </section>
  );
}

export interface AnchoredPopoverProps {
  id?: string;
  open: boolean;
  onClose(): void;
  anchorRef: React.RefObject<HTMLElement | null>;
  label: string;
  /** Keeps the content mounted, hidden, while the popover is closed. */
  keepMounted?: boolean;
  children: React.ReactNode;
}

/**
 * A non-modal panel under its anchor, on the right edge of the anchor. It
 * closes on Escape and on a pointer down outside the anchor and the panel.
 */
export function AnchoredPopover({
  id,
  open,
  onClose,
  anchorRef,
  label,
  keepMounted = false,
  children,
}: AnchoredPopoverProps) {
  const panelRef = React.useRef<HTMLDivElement>(null);
  // Set by a pointer down in the React tree of the panel, which includes
  // the dialogs that its content opens in portals.
  const pointerInsideRef = React.useRef(false);
  const [position, setPosition] = React.useState<{
    top: number;
    right: number;
  } | null>(null);

  React.useLayoutEffect(() => {
    if (!open) {
      return;
    }
    const place = () => {
      const rect = anchorRef.current?.getBoundingClientRect();
      if (rect) {
        setPosition({
          top: rect.bottom + 4,
          right: Math.max(8, window.innerWidth - rect.right),
        });
      }
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [anchorRef, open]);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    panelRef.current?.focus();
    const onPointerDown = (event: Event) => {
      const inside = pointerInsideRef.current;
      pointerInsideRef.current = false;
      if (inside || anchorRef.current?.contains(event.target as Node)) {
        return;
      }
      onClose();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        anchorRef.current?.focus();
        onClose();
      }
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [anchorRef, onClose, open]);

  if (!open && !keepMounted) {
    return null;
  }
  return createPortal(
    <div
      ref={panelRef}
      id={id}
      role="dialog"
      aria-label={label}
      tabIndex={-1}
      hidden={!open}
      onPointerDownCapture={() => {
        pointerInsideRef.current = true;
      }}
      className="fixed z-[70] max-h-[60vh] w-80 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg outline-none"
      style={position ?? undefined}
    >
      {children}
    </div>,
    document.body
  );
}
