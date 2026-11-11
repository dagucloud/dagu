// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CircleAlert, CircleCheck, TriangleAlert } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { SkillIssueSeverity } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import {
  groupIssues,
  skillState,
  sortIssues,
  type SkillIssue,
  type SkillState,
} from './skillIssues';

/** The most issues that a tooltip lists. The detail of the skill lists all. */
const TOOLTIP_LIMIT = 8;

function IssueMark({
  severity,
}: {
  severity: SkillIssue['severity'];
}): ReactElement {
  const { ts } = useI18n();
  const error = severity === SkillIssueSeverity.error;
  const Icon = error ? CircleAlert : TriangleAlert;
  return (
    <Icon
      role="img"
      aria-label={error ? ts('Error') : ts('Warning')}
      className={cn(
        'mt-0.5 size-3.5 shrink-0',
        error ? 'text-destructive' : 'text-warning'
      )}
    />
  );
}

/** The issues of a skill in a tooltip: the message, the file and the rule. */
function IssueTooltipList({
  issues,
}: {
  issues: readonly SkillIssue[];
}): ReactElement {
  const { ts } = useI18n();
  const ordered = sortIssues(issues);
  const shown = ordered.slice(0, TOOLTIP_LIMIT);
  return (
    <div className="max-w-80 space-y-1">
      <ul className="space-y-1">
        {shown.map((issue, index) => (
          <li key={index} className="flex items-start gap-1.5">
            <IssueMark severity={issue.severity} />
            <span className="min-w-0 break-words">
              {issue.file && (
                <span className="font-mono font-medium">{issue.file}: </span>
              )}
              {issue.message}
            </span>
          </li>
        ))}
      </ul>
      {ordered.length > shown.length && (
        <p className="text-muted-foreground">
          {ts('and {count} more', { count: ordered.length - shown.length })}
        </p>
      )}
    </div>
  );
}

/**
 * The state of a skill: ERROR when the server refuses it, a warning mark when
 * it only has warnings, and, with `showValid`, a Valid badge. The tooltip
 * lists the issues. A valid skill shows nothing unless `showValid` is set.
 */
export function SkillStateBadge({
  skill,
  showValid = false,
  className,
}: {
  skill: {
    valid?: boolean;
    errors?: readonly SkillIssue[];
    warnings?: readonly SkillIssue[];
  };
  showValid?: boolean;
  className?: string;
}): ReactElement | null {
  const { ts } = useI18n();
  const state: SkillState = skillState(skill);
  if (state === 'valid' && !showValid) return null;
  const errors = skill.errors ?? [];
  const warnings = skill.warnings ?? [];
  const issues = [...errors, ...warnings];

  const badge =
    state === 'error' ? (
      <Badge variant="error" className={className} data-state-kind="error">
        <CircleAlert aria-hidden="true" />
        {ts('Error')}
        {errors.length > 1 && <span aria-hidden="true">{errors.length}</span>}
      </Badge>
    ) : state === 'warning' ? (
      <Badge variant="warning" className={className} data-state-kind="warning">
        <TriangleAlert aria-hidden="true" />
        {ts('Warning')}
        {warnings.length > 1 && (
          <span aria-hidden="true">{warnings.length}</span>
        )}
      </Badge>
    ) : (
      <Badge variant="success" className={className} data-state-kind="valid">
        <CircleCheck aria-hidden="true" />
        {ts('Valid')}
      </Badge>
    );
  if (issues.length === 0) return badge;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{badge}</TooltipTrigger>
      <TooltipContent side="bottom" align="end">
        <IssueTooltipList issues={issues} />
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * The panel of the errors and the warnings of the whole skill, by file. Each
 * line has the file, the message and the rule. A click on a file name opens
 * that file. The panel is absent when there is no issue.
 */
export function SkillIssuePanel({
  issues,
  heading,
  onOpenFile,
  className,
}: {
  issues: readonly SkillIssue[];
  /** A title that replaces "Validation", for the answer of a refused save. */
  heading?: string;
  onOpenFile?(file: string): void;
  className?: string;
}): ReactElement | null {
  const { ts } = useI18n();
  const [open, setOpen] = useState(true);
  if (issues.length === 0) return null;
  const groups = groupIssues(issues);
  const errors = issues.filter(
    (issue) => issue.severity === SkillIssueSeverity.error
  ).length;
  const warnings = issues.length - errors;
  return (
    <section
      aria-label={heading ?? ts('Validation')}
      data-testid="skill-issues"
      className={cn(
        'rounded-lg border',
        errors > 0
          ? 'border-destructive/40 bg-destructive/5'
          : 'border-warning/40 bg-warning/5',
        className
      )}
    >
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs font-medium"
      >
        <span>{heading ?? ts('Validation')}</span>
        {errors > 0 && (
          <Badge variant="error">
            {ts('Errors: {count}', { count: errors })}
          </Badge>
        )}
        {warnings > 0 && (
          <Badge variant="warning">
            {ts('Warnings: {count}', { count: warnings })}
          </Badge>
        )}
      </button>
      {open && (
        <div className="max-h-44 space-y-2 overflow-y-auto border-t border-border/60 px-3 py-2">
          {groups.map((group) => (
            <div key={group.file} className="space-y-1">
              <p className="text-[11px] font-medium text-muted-foreground">
                {group.file === '' ? (
                  ts('The skill folder')
                ) : onOpenFile ? (
                  <button
                    type="button"
                    className="font-mono underline-offset-2 hover:underline"
                    onClick={() => onOpenFile(group.file)}
                  >
                    {group.file}
                  </button>
                ) : (
                  <span className="font-mono">{group.file}</span>
                )}
              </p>
              <ul className="space-y-1">
                {[...group.errors, ...group.warnings].map((issue, index) => (
                  <li
                    key={index}
                    data-severity={issue.severity}
                    className="flex items-start gap-1.5 text-xs"
                  >
                    <IssueMark severity={issue.severity} />
                    <span className="min-w-0 flex-1 break-words">
                      {issue.message}
                    </span>
                    <code className="shrink-0 rounded bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
                      {issue.rule}
                    </code>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
