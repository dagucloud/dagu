// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import BorderedBox from '@/components/ui/bordered-box';
import { getLogMessageFromConfig } from '@/lib/executor-utils';
import { getHarnessStepSummary } from '@/lib/harness-step';
import React from 'react';
import { LogStepMessage } from '../dag-details/LogStepMessage';

type Step = components['schemas']['Step'];

const STEP_FIELD_ORDER = [
  'id',
  'description',
  'executorConfig',
  'commands',
  'script',
  'output',
  'stdout',
  'stderr',
  'dir',
  'depends',
  'call',
  'params',
  'parallel',
  'repeatPolicy',
  'timeoutSec',
  'mailOnError',
  'preconditions',
  'router',
  'approval',
];

const STEP_FIELD_LABELS: Record<string, string> = {
  approval: 'Approval',
  call: 'Sub DAG',
  commands: 'Commands',
  depends: 'Depends On',
  description: 'Description',
  dir: 'Working Directory',
  executorConfig: 'Executor',
  id: 'ID',
  mailOnError: 'Mail On Error',
  output: 'Output Variable',
  parallel: 'Parallel',
  params: 'Parameters',
  preconditions: 'Preconditions',
  repeatPolicy: 'Repeat Policy',
  router: 'Router',
  script: 'Script',
  stderr: 'stderr',
  stdout: 'stdout',
  timeoutSec: 'Timeout',
};

export function StepDetails({ step }: { step: Step }) {
  const harnessPrompt = getHarnessStepSummary(step)?.prompt ?? null;
  const fields = React.useMemo(() => {
    const entries = Object.entries(step)
      .filter(([key]) => key !== 'name')
      .filter(([, value]) => hasMeaningfulValue(value));

    return entries.sort(([left], [right]) => {
      const leftIndex = STEP_FIELD_ORDER.indexOf(left);
      const rightIndex = STEP_FIELD_ORDER.indexOf(right);
      if (leftIndex === -1 && rightIndex === -1) {
        return left.localeCompare(right);
      }
      if (leftIndex === -1) return 1;
      if (rightIndex === -1) return -1;
      return leftIndex - rightIndex;
    });
  }, [step]);

  if (fields.length === 0) {
    return (
      <div className="rounded-md border border-border bg-muted/20 p-3 text-sm text-muted-foreground">
        No additional step fields are defined.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {fields.map(([key, value]) => {
        const isHarnessPrompt = key === 'commands' && harnessPrompt !== null;
        return (
          <StepField
            key={key}
            label={
              isHarnessPrompt
                ? 'Prompt'
                : STEP_FIELD_LABELS[key] || toTitleLabel(key)
            }
            name={isHarnessPrompt ? 'prompt' : key}
            value={isHarnessPrompt ? harnessPrompt : value}
          />
        );
      })}
    </div>
  );
}

function StepField({
  label,
  name,
  value,
}: {
  label: string;
  name: string;
  value: unknown;
}) {
  return (
    <section className="space-y-2">
      <div className="text-xs font-medium uppercase text-muted-foreground">
        {label}
      </div>
      {renderStepFieldValue(name, value)}
    </section>
  );
}

function renderStepFieldValue(name: string, value: unknown): React.ReactNode {
  if (typeof value === 'boolean') {
    return <Badge variant="outline">{value ? 'Enabled' : 'Disabled'}</Badge>;
  }

  if (typeof value === 'number') {
    const suffix = name.toLowerCase().includes('sec') ? ' sec' : '';
    return (
      <div className="text-sm text-foreground">
        {value}
        {suffix}
      </div>
    );
  }

  if (typeof value === 'string') {
    if (name === 'script' || value.includes('\n') || value.length > 120) {
      return (
        <CodeBlock
          value={value}
          language={name === 'script' ? 'shell' : 'text'}
        />
      );
    }
    return <div className="break-words text-sm text-foreground">{value}</div>;
  }

  if (Array.isArray(value)) {
    if (value.every((item) => isScalar(item))) {
      return (
        <div className="flex flex-wrap gap-1.5">
          {value.map((item, index) => (
            <Badge key={`${String(item)}-${index}`} variant="outline">
              {String(item)}
            </Badge>
          ))}
        </div>
      );
    }

    return (
      <div className="space-y-2">
        {value.map((item, index) => (
          <ObjectFieldCard key={index} value={item} />
        ))}
      </div>
    );
  }

  if (isPlainObject(value)) {
    if (name === 'executorConfig') {
      return <ExecutorConfigField value={value} />;
    }
    return <ObjectFieldCard value={value} />;
  }

  return <div className="text-sm text-foreground">{String(value)}</div>;
}

function ExecutorConfigField({ value }: { value: Record<string, unknown> }) {
  const type = typeof value.type === 'string' ? value.type : '';
  const config = isPlainObject(value.config) ? value.config : undefined;
  const logMessage = type === 'log' ? getLogMessageFromConfig(config) : null;

  if (logMessage === null) {
    return <ObjectFieldCard value={value} />;
  }

  return (
    <div className="space-y-3 rounded-md border border-border bg-background p-3">
      <div className="min-w-0">
        <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground">
          Type
        </div>
        <Badge variant="outline">log</Badge>
      </div>
      <div className="min-w-0">
        <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground">
          Message
        </div>
        <LogStepMessage message={logMessage} />
      </div>
    </div>
  );
}

function ObjectFieldCard({ value }: { value: unknown }) {
  if (!isPlainObject(value)) {
    return <div className="text-sm text-foreground">{String(value)}</div>;
  }

  const entries = Object.entries(value).filter(([, item]) =>
    hasMeaningfulValue(item)
  );

  if (entries.length === 0) return null;

  return (
    <div className="space-y-2 rounded-md border border-border bg-background p-3">
      {entries.map(([key, item]) => (
        <div key={key} className="min-w-0">
          <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground">
            {STEP_FIELD_LABELS[key] || toTitleLabel(key)}
          </div>
          {renderNestedFieldValue(key, item)}
        </div>
      ))}
    </div>
  );
}

function renderNestedFieldValue(name: string, value: unknown): React.ReactNode {
  if (
    typeof value === 'string' &&
    (value.includes('\n') || value.length > 120)
  ) {
    return (
      <CodeBlock
        value={value}
        language={name === 'script' ? 'shell' : 'text'}
      />
    );
  }
  if (isPlainObject(value) || Array.isArray(value)) {
    return renderStepFieldValue(name, value);
  }
  if (typeof value === 'boolean') {
    return <Badge variant="outline">{value ? 'Enabled' : 'Disabled'}</Badge>;
  }
  return (
    <div className="whitespace-pre-wrap break-words text-sm text-foreground">
      {String(value)}
    </div>
  );
}

function CodeBlock({
  value,
  language = 'text',
}: {
  value: string;
  language?: 'shell' | 'text';
}) {
  if (language === 'shell') {
    return <ShellCodeBlock value={value} />;
  }

  return (
    <BorderedBox className="bg-muted/20 p-3">
      <pre className="whitespace-pre-wrap break-words text-xs leading-5 text-foreground">
        {value}
      </pre>
    </BorderedBox>
  );
}

function ShellCodeBlock({ value }: { value: string }) {
  const lines = value.split('\n');

  return (
    <BorderedBox className="overflow-x-auto bg-muted/30 p-0 dark:border-slate-700 dark:bg-slate-950">
      <pre className="m-0 min-w-max text-xs leading-5 text-foreground selection:bg-primary/20 dark:text-slate-100 dark:selection:bg-violet-500/40 dark:selection:text-white">
        {lines.map((line, index) => (
          <div
            key={`${index}-${line}`}
            className="grid grid-cols-[3rem_minmax(0,1fr)] border-b border-border/70 last:border-b-0 dark:border-slate-800"
          >
            <span className="select-none bg-muted px-2 py-0.5 text-right text-muted-foreground dark:bg-slate-900 dark:text-slate-500">
              {index + 1}
            </span>
            <code className="whitespace-pre px-3 py-0.5 font-mono text-foreground selection:bg-primary/20 dark:text-slate-100 dark:selection:bg-violet-500/40 dark:selection:text-white">
              {line ? highlightShellLine(line) : '\u00a0'}
            </code>
          </div>
        ))}
      </pre>
    </BorderedBox>
  );
}

type ShellSegment = {
  text: string;
  type?: 'comment' | 'string';
};

const SHELL_KEYWORDS = new Set([
  'case',
  'do',
  'done',
  'elif',
  'else',
  'esac',
  'fi',
  'for',
  'function',
  'if',
  'in',
  'then',
  'until',
  'while',
]);

function highlightShellLine(line: string): React.ReactNode[] {
  return splitShellSegments(line).flatMap((segment, segmentIndex) => {
    if (segment.type === 'comment') {
      return (
        <span
          key={`${segmentIndex}-comment`}
          className="text-muted-foreground dark:text-slate-500"
        >
          {segment.text}
        </span>
      );
    }

    if (segment.type === 'string') {
      return (
        <span
          key={`${segmentIndex}-string`}
          className="text-emerald-700 dark:text-emerald-300"
        >
          {segment.text}
        </span>
      );
    }

    return highlightShellPlainSegment(segment.text, segmentIndex);
  });
}

function splitShellSegments(line: string): ShellSegment[] {
  const segments: ShellSegment[] = [];
  let cursor = 0;
  let plainStart = 0;

  function pushPlain(end: number) {
    if (end > plainStart) {
      segments.push({ text: line.slice(plainStart, end) });
    }
  }

  while (cursor < line.length) {
    const char = line[cursor];
    if (char === '#' && (cursor === 0 || /\s/.test(line[cursor - 1] || ''))) {
      pushPlain(cursor);
      segments.push({ text: line.slice(cursor), type: 'comment' });
      return segments;
    }

    if (char === "'" || char === '"') {
      const quote = char;
      pushPlain(cursor);
      let end = cursor + 1;
      while (end < line.length) {
        if (quote === '"' && line[end] === '\\') {
          end += 2;
          continue;
        }
        if (line[end] === quote) {
          end += 1;
          break;
        }
        end += 1;
      }
      segments.push({ text: line.slice(cursor, end), type: 'string' });
      cursor = end;
      plainStart = cursor;
      continue;
    }

    cursor += 1;
  }

  pushPlain(line.length);
  return segments;
}

function highlightShellPlainSegment(
  text: string,
  segmentIndex: number
): React.ReactNode[] {
  const pattern =
    /(\$\{?[A-Za-z_][A-Za-z0-9_]*\}?|\$\([^)]+\)|\$[0-9?@#*]|\b[A-Za-z_][A-Za-z0-9_]*\b|--?[A-Za-z0-9][\w-]*|\b\d+\b|[|&;()<>]=?)/g;
  const nodes: React.ReactNode[] = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }

    const token = match[0];
    nodes.push(
      <span
        key={`${segmentIndex}-${match.index}-${token}`}
        className={getShellTokenClassName(token)}
      >
        {token}
      </span>
    );
    lastIndex = match.index + token.length;
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }

  return nodes;
}

function getShellTokenClassName(token: string): string {
  if (token.startsWith('$')) return 'text-violet-700 dark:text-violet-300';
  if (SHELL_KEYWORDS.has(token))
    return 'font-medium text-purple-700 dark:text-purple-300';
  if (/^--?/.test(token)) return 'text-amber-700 dark:text-amber-300';
  if (/^[|&;()<>]/.test(token)) return 'text-orange-700 dark:text-orange-300';
  if (/^\d+$/.test(token)) return 'text-amber-700 dark:text-amber-300';
  return 'text-foreground dark:text-slate-100';
}

function hasMeaningfulValue(value: unknown): boolean {
  if (value == null) return false;
  if (typeof value === 'string') return value.trim().length > 0;
  if (typeof value === 'boolean') return true;
  if (Array.isArray(value))
    return value.some((item) => hasMeaningfulValue(item));
  if (isPlainObject(value)) {
    return Object.values(value).some((item) => hasMeaningfulValue(item));
  }
  return true;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isScalar(value: unknown): value is string | number | boolean {
  return (
    typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
  );
}

function toTitleLabel(value: string): string {
  return value
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .replace(/\b\w/g, (char) => char.toUpperCase());
}
