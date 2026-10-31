// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FileCode, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import type {
  DagDocModel,
  StepInspectorProps,
  StepView,
  ValidationIssue,
} from '../contracts';
import { FieldIssues } from './FieldIssues';
import {
  useInspectorView,
  useStepMemory,
  type InspectorSegment,
} from './InspectorViewContext';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { issuesUnder, stepIssues } from './issues';
import { Section } from './Section';
import { ActionSection, isRunStep } from './sections/Action';
import { FlowSection } from './sections/Flow';
import { GeneralSection } from './sections/General';
import { ParametersSection } from './sections/Parameters';
import { StepYamlSection } from './sections/StepYaml';

const GENERAL_FIELDS = ['name', 'id', 'description'];
const ACTION_FIELDS = ['action', 'run', 'shell'];
const FLOW_FIELDS = [
  'depends',
  'timeout_sec',
  'retry_policy',
  'continue_on',
  'preconditions',
  'env',
  'outputs',
];

type SectionId = 'general' | 'action' | 'parameters' | 'flow';

/** The section that shows a field path relative to the step, or null. */
export function sectionOfPath(path: string): SectionId | null {
  const head = path.split(/[.[]/)[0] ?? '';
  if (GENERAL_FIELDS.includes(head)) return 'general';
  if (ACTION_FIELDS.includes(head)) return 'action';
  if (head === 'with') return 'parameters';
  if (FLOW_FIELDS.includes(head)) return 'flow';
  return null;
}

/** The segment of the split form that holds a section. */
export function segmentOfSection(section: SectionId): InspectorSegment {
  return section === 'flow' ? 'settings' : 'parameters';
}

/** A field path and its ancestors, nearest first: `with.a[0]`, `with.a`, `with`. */
export function fieldPathAncestors(path: string): string[] {
  const paths = [path];
  let rest = path;
  for (;;) {
    const cut = Math.max(rest.lastIndexOf('.'), rest.lastIndexOf('['));
    if (cut <= 0) return paths;
    rest = rest.slice(0, cut);
    paths.push(rest);
  }
}

const FOCUSABLE = [
  'input:not([disabled]):not([type="hidden"])',
  'textarea:not([disabled])',
  'select:not([disabled])',
  'button:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  // The frame of a code field that waits for Monaco: it hands the focus on.
  '[data-monaco-pending]',
].join(',');

/** The box of a field path, else of its nearest ancestor that has one. */
function fieldBox(root: HTMLElement, path: string): HTMLElement | null {
  const boxes = Array.from(
    root.querySelectorAll<HTMLElement>('[data-field-path]')
  );
  for (const candidate of fieldPathAncestors(path)) {
    const box = boxes.find((item) => item.dataset.fieldPath === candidate);
    if (box) return box;
  }
  return null;
}

const PARAMETER_BOX = '[data-field-path^="with."]';

/** True when the parameter and every object that holds it are required. */
function requiredToTop(box: HTMLElement): boolean {
  for (
    let item: HTMLElement | null = box;
    item;
    item = item.parentElement?.closest<HTMLElement>(PARAMETER_BOX) ?? null
  ) {
    if (item.dataset.required !== 'true') return false;
  }
  return true;
}

/** The field that a new step starts in: an empty required parameter, else the name. */
function newStepBox(root: HTMLElement): HTMLElement | null {
  const empty = Array.from(
    root.querySelectorAll<HTMLElement>(`${PARAMETER_BOX}[data-empty="true"]`)
  );
  return (
    empty.find(requiredToTop) ??
    root.querySelector<HTMLElement>('[data-field-path="name"]')
  );
}

/** Focuses the first control in the box, or the box itself when it has none. */
function focusBox(box: HTMLElement): void {
  for (let parent = box.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement) parent.open = true;
  }
  const target = box.querySelector<HTMLElement>(FOCUSABLE) ?? box;
  if (target === box && !box.hasAttribute('tabindex')) box.tabIndex = -1;
  target.focus();
  target.scrollIntoView?.({ block: 'nearest' });
}

/** What identifies a step across edits that move its key. */
function stepIdentity(step: StepView): string {
  return step.explicitName ?? step.id ?? step.key;
}

function stepIdentities(model: DagDocModel): Set<string> {
  return new Set([...model.steps, ...model.handlers].map(stepIdentity));
}

/** A request to open sections and focus a field (null path: a new step). */
interface Reveal {
  sections: SectionId[];
  path: string | null;
  seq: number;
}

function findStep(props: StepInspectorProps): StepView | undefined {
  return (
    props.model.steps.find((step) => step.key === props.step) ??
    props.model.handlers.find((step) => step.key === props.step)
  );
}

function countUnder(
  fieldIssues: Record<string, ValidationIssue[]>,
  fields: string[]
): number {
  return fields.reduce(
    (total, field) => total + issuesUnder(fieldIssues, field).length,
    0
  );
}

/**
 * Side panel that edits one step with forms. Every change goes out through
 * `onEdit` as the smallest edit operation. The panel is read-only when
 * `readOnly` is set or when the YAML has syntax errors (`model.ok` false).
 * `issues` are the issues of this step; field issues show next to their
 * field, the others at the top.
 *
 * In a split view (`InspectorViewContext`) the sections are in two
 * segments, Parameters and Settings, and the YAML section is left out.
 * Both segments stay mounted. A focus path shows and focuses its field;
 * a step that was not in the last model starts with focus in its form.
 */
export function StepInspector(props: StepInspectorProps): ReactElement {
  const { ts } = useI18n();
  const view = useInspectorView();
  const memory = useStepMemory();
  const step = findStep(props);
  const stepKey = step?.key ?? null;
  const split = view.split;
  const grouped = useMemo(
    () =>
      step ? stepIssues(props.issues, step, ts) : { byField: {}, general: [] },
    [props.issues, step, ts]
  );
  const rootRef = useRef<HTMLDivElement>(null);
  const [reveal, setReveal] = useState<Reveal | null>(null);
  const shownKey = useRef<string | null>(null);

  // A field to show, from the host.
  useEffect(() => {
    if (!split || stepKey === null || view.focusPath === null) return;
    const path = view.focusPath;
    const section = sectionOfPath(path);
    view.clearFocus();
    if (section) view.setSegment(segmentOfSection(section));
    setReveal((last) => ({
      sections: section ? [section] : [],
      path,
      seq: (last?.seq ?? 0) + 1,
    }));
  }, [split, stepKey, view]);

  // A step that the last model did not have was just inserted. A plain
  // selection of an existing step leaves the focus alone.
  useEffect(() => {
    if (!memory) return;
    const known = memory.current;
    memory.current = stepIdentities(props.model);
    if (!step || shownKey.current === step.key) return;
    shownKey.current = step.key;
    if (!split || !known || known.has(stepIdentity(step))) return;
    if (view.focusPath !== null) return;
    view.setSegment('parameters');
    setReveal((last) => ({
      sections: ['general', 'parameters'],
      path: null,
      seq: (last?.seq ?? 0) + 1,
    }));
  }, [memory, props.model, step, split, view]);

  useEffect(() => {
    const root = rootRef.current;
    if (!reveal || !root) return;
    const box =
      reveal.path === null ? newStepBox(root) : fieldBox(root, reveal.path);
    if (box) focusBox(box);
  }, [reveal]);
  const revealKey = (section: SectionId) =>
    reveal?.sections.includes(section) ? reveal.seq : undefined;

  const closeButton = (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      aria-label={ts('Close')}
      onClick={props.onClose}
    >
      <X />
    </Button>
  );

  if (!step) {
    return (
      <aside
        className="flex h-full min-h-0 flex-col bg-card"
        aria-label={ts('Step inspector')}
      >
        <header className="flex h-8 items-center justify-between border-b border-border px-2">
          <span className="text-xs text-muted-foreground">
            <I18nText text="This step no longer exists." />
          </span>
          {closeButton}
        </header>
      </aside>
    );
  }

  const locked = props.readOnly || !props.model.ok || step.broken !== undefined;
  const label =
    step.explicitName ?? step.id ?? props.serverStep?.name ?? step.key;
  const fieldIssues = grouped.byField;
  const total =
    grouped.general.length +
    Object.values(fieldIssues).reduce((sum, list) => sum + list.length, 0);
  const hasAction =
    !isRunStep(step) &&
    (typeof step.value.action === 'string' || Boolean(step.action));
  const common = {
    step,
    readOnly: locked,
    onEdit: props.onEdit,
    fieldIssues,
    derivedName: props.serverStep?.name,
  };
  const counts = {
    general: countUnder(fieldIssues, GENERAL_FIELDS),
    action: countUnder(fieldIssues, ACTION_FIELDS),
    parameters: hasAction ? countUnder(fieldIssues, ['with']) : 0,
    flow: countUnder(fieldIssues, FLOW_FIELDS),
  };

  const generalSection = (
    <Section
      title={<I18nText text="General" />}
      count={counts.general}
      revealKey={revealKey('general')}
    >
      <GeneralSection {...common} />
    </Section>
  );
  const actionSection = (
    <Section
      title={<I18nText text="What it does" />}
      count={counts.action}
      revealKey={revealKey('action')}
    >
      <ActionSection {...common} catalog={props.catalog} />
    </Section>
  );
  const parametersSection = hasAction && (
    <Section
      title={<I18nText text="Parameters" />}
      count={counts.parameters}
      revealKey={revealKey('parameters')}
    >
      <ParametersSection
        {...common}
        schema={props.schema}
        customActions={props.customActions}
        candidates={props.candidates}
      />
    </Section>
  );
  const flowSection = (
    <Section
      title={<I18nText text="Flow" />}
      count={counts.flow}
      revealKey={revealKey('flow')}
    >
      <FlowSection {...common} />
    </Section>
  );
  const segments: { id: InspectorSegment; label: string; count: number }[] = [
    {
      id: 'parameters',
      label: ts('Parameters'),
      count: counts.general + counts.action + counts.parameters,
    },
    { id: 'settings', label: ts('Settings'), count: counts.flow },
  ];

  return (
    <aside
      className="flex h-full min-h-0 flex-col bg-card"
      aria-label={ts('Step inspector')}
    >
      {!props.embedded && (
        <header className="flex h-8 shrink-0 items-center gap-2 border-b border-border px-2">
          <h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
            {label}
          </h2>
          <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">
            {step.handler ?? step.kind}
          </span>
          {total > 0 && (
            <span
              className="rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] font-medium text-destructive"
              aria-label={ts('{count} issues', { count: total })}
            >
              {total}
            </span>
          )}
          {props.onShowYaml && (
            <Button
              type="button"
              variant="ghost"
              className="h-6 gap-1 px-1.5 text-xs"
              title={ts('Show the YAML of this step')}
              onClick={props.onShowYaml}
            >
              <FileCode className="h-3.5 w-3.5" aria-hidden="true" />
              {ts('YAML')}
            </Button>
          )}
          {closeButton}
        </header>
      )}
      {split && (
        <div
          role="group"
          aria-label={ts('Part of the form to show')}
          className="flex shrink-0 gap-1 border-b border-border px-2 py-1"
        >
          {segments.map((segment) => (
            <button
              key={segment.id}
              type="button"
              aria-pressed={view.segment === segment.id}
              onClick={() => view.setSegment(segment.id)}
              className={cn(
                'flex h-6 items-center gap-1 rounded px-2 text-xs',
                view.segment === segment.id
                  ? 'bg-muted font-medium text-foreground'
                  : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {segment.label}
              {segment.count > 0 && (
                <span
                  className="rounded bg-destructive/10 px-1 text-[10px] font-medium text-destructive"
                  aria-label={ts(
                    segment.count === 1 ? '{count} issue' : '{count} issues',
                    { count: segment.count }
                  )}
                >
                  {segment.count}
                </span>
              )}
            </button>
          ))}
        </div>
      )}
      <div
        key={step.key}
        ref={rootRef}
        className="min-h-0 flex-1 overflow-auto"
      >
        {(!props.model.ok || step.broken !== undefined) && (
          <NoticeLine
            className="p-1"
            notices={[
              {
                id: 'step-broken',
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
        <FieldIssues
          issues={grouped.general}
          className="border-b border-border px-2 py-1"
        />
        {split ? (
          <>
            {/* Both segments stay mounted, so that their state survives a switch. */}
            <div
              data-segment="parameters"
              hidden={view.segment !== 'parameters'}
            >
              {generalSection}
              {actionSection}
              {parametersSection}
            </div>
            <div data-segment="settings" hidden={view.segment !== 'settings'}>
              {flowSection}
            </div>
          </>
        ) : (
          <>
            {generalSection}
            {actionSection}
            {parametersSection}
            {flowSection}
            <Section title={<I18nText text="YAML" />} defaultOpen={false}>
              <StepYamlSection
                model={props.model}
                step={step}
                schema={props.schema}
                readOnly={locked}
                onEdit={props.onEdit}
              />
            </Section>
          </>
        )}
        {props.runPanel}
      </div>
    </aside>
  );
}
