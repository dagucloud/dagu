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
import { focusBox } from './focusField';
import {
  useInspectorView,
  useStepMemory,
  type InspectorSegment,
} from './InspectorViewContext';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { issuesUnder, stepIssues } from './issues';
import { Section } from './Section';
import { ActionSection, isRunStep } from './sections/Action';
import { AgentRunSections } from './sections/agentRun/AgentRunSections';
import {
  AGENT_LIMIT_KEYS,
  AGENT_MODEL_KEYS,
  AGENT_PROMPT_KEYS,
  AGENT_SKILL_KEYS,
  AGENT_SYSTEM_KEYS,
  AGENT_TOOL_KEYS,
  agentRunIssueCounts,
} from './sections/agentRun/model';
import { FlowSection } from './sections/Flow';
import { GeneralSection } from './sections/General';
import { ModelSection } from './sections/chat/ModelSection';
import {
  chatIssueCounts,
  MODEL_KEYS,
  OPTION_KEYS,
  otherChatKeys,
  PROMPT_KEYS,
  SYSTEM_KEYS,
} from './sections/chat/model';
import { OptionsSection } from './sections/chat/OptionsSection';
import { OutputSection } from './sections/chat/OutputSection';
import { PromptSection, SystemSection } from './sections/chat/PromptSections';
import { OtherKeys, ParametersSection } from './sections/Parameters';
import { SECURITY_FIELDS, SecuritySection } from './sections/Security';
import { StepYamlSection } from './sections/StepYaml';
import { SkillsSection } from './sections/skills/SkillsSection';
import { ToolsSection } from './sections/tools/ToolsSection';
import { AGENT_RUN_ACTION, dagHasAgentRun } from '../agentRun/agentRun';
import { CHAT_ACTION, TOOL_WITH_KEYS } from '../tools/model';
import { SKILL_WITH_KEYS } from '../tools/skills';
import { readAgentRunInfo, readChatInfo } from '../canvas/chatModel';
import { hasEffort } from '../canvas/modelEdits';

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
/** The Limits section of an agent step edits the timeout: the Flow section does not. */
const AGENT_FLOW_FIELDS = FLOW_FIELDS.filter(
  (field) => field !== 'timeout_sec'
);

type SectionId =
  | 'general'
  | 'action'
  | 'parameters'
  | 'model'
  | 'system'
  | 'prompt'
  | 'tools'
  | 'skills'
  | 'output'
  | 'options'
  | 'limits'
  | 'security'
  | 'flow';

/** The form of a step that has one section for each part: a chat step or an agent step. */
export type StepForm = 'chat' | 'agentRun';

/** The section of each key of `with`, from the key lists of the sections of a form. */
function sectionOfKey(
  sections: [SectionId, readonly string[]][]
): ReadonlyMap<string, SectionId> {
  return new Map(
    sections.flatMap(([section, keys]) =>
      keys.map((key) => [key, section] as const)
    )
  );
}

/** The section of the chat form that shows each key of `with`, by key. */
const CHAT_SECTION_OF_KEY = sectionOfKey([
  ['model', MODEL_KEYS],
  ['system', SYSTEM_KEYS],
  ['prompt', PROMPT_KEYS],
  ['options', OPTION_KEYS],
  ['skills', SKILL_WITH_KEYS],
]);

/** The section of the agent form that shows each key of `with`, by key (Spec 091). */
const AGENT_SECTION_OF_KEY = sectionOfKey([
  ['model', AGENT_MODEL_KEYS],
  ['system', AGENT_SYSTEM_KEYS],
  ['prompt', AGENT_PROMPT_KEYS],
  ['tools', AGENT_TOOL_KEYS],
  ['skills', AGENT_SKILL_KEYS],
  ['limits', AGENT_LIMIT_KEYS],
]);

/** True for a step that the chat form edits. */
function isChatStep(step: StepView): boolean {
  return !isRunStep(step) && (step.value.action ?? step.action) === CHAT_ACTION;
}

/** True for a step that the agent form edits. */
function isAgentRunStep(step: StepView): boolean {
  return (
    !isRunStep(step) && (step.value.action ?? step.action) === AGENT_RUN_ACTION
  );
}

const TOOL_FIELDS = TOOL_WITH_KEYS.map((key) => `with.${key}`);

/**
 * The section that shows a field path relative to the step, or null. A chat
 * step and an agent step (`form`) have one section for each part of their
 * form. An agent step shows its timeout in Limits.
 */
export function sectionOfPath(path: string, form?: StepForm): SectionId | null {
  const head = path.split(/[.[]/)[0] ?? '';
  if (GENERAL_FIELDS.includes(head)) return 'general';
  if (ACTION_FIELDS.includes(head)) return 'action';
  if (form === 'agentRun') {
    if (head === 'output_schema') return 'output';
    if (head === 'timeout_sec') return 'limits';
    if (head === 'with') {
      const key = path.slice('with.'.length).split(/[.[]/)[0] ?? '';
      return (
        AGENT_SECTION_OF_KEY.get(key) ?? (path === 'with' ? 'prompt' : null)
      );
    }
  }
  if (
    TOOL_FIELDS.some(
      (field) =>
        path === field ||
        path.startsWith(`${field}[`) ||
        path.startsWith(`${field}.`)
    )
  ) {
    return 'tools';
  }
  if ((SECURITY_FIELDS as readonly string[]).includes(head)) return 'security';
  if (form === 'chat' && head === 'output_schema') return 'output';
  if (form === 'chat' && head === 'with') {
    const key = path.slice('with.'.length).split(/[.[]/)[0] ?? '';
    return CHAT_SECTION_OF_KEY.get(key) ?? (path === 'with' ? 'prompt' : null);
  }
  if (head === 'with') return 'parameters';
  if (FLOW_FIELDS.includes(head)) return 'flow';
  return null;
}

/** The segment of the split form that holds a section. */
export function segmentOfSection(section: SectionId): InspectorSegment {
  return section === 'flow' || section === 'security'
    ? 'settings'
    : 'parameters';
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
function newStepBox(
  root: HTMLElement,
  naming: 'id' | 'name'
): HTMLElement | null {
  const empty = Array.from(
    root.querySelectorAll<HTMLElement>(`${PARAMETER_BOX}[data-empty="true"]`)
  );
  return (
    empty.find(requiredToTop) ??
    root.querySelector<HTMLElement>(`[data-field-path="${naming}"]`)
  );
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
  /** The field that names a new step: its id when it has no name. */
  naming?: 'id' | 'name';
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
  const chat = step !== undefined && isChatStep(step);
  const agentRun = step !== undefined && isAgentRunStep(step);
  const form: StepForm | undefined = chat
    ? 'chat'
    : agentRun
      ? 'agentRun'
      : undefined;

  // A field to show, from the host.
  useEffect(() => {
    if (!split || stepKey === null || view.focusPath === null) return;
    const path = view.focusPath;
    const section = sectionOfPath(path, form);
    view.clearFocus();
    if (section) view.setSegment(segmentOfSection(section));
    setReveal((last) => ({
      sections: section ? [section] : [],
      path,
      seq: (last?.seq ?? 0) + 1,
    }));
  }, [split, stepKey, view, form]);

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
      naming:
        step.explicitName === undefined && step.id !== undefined
          ? 'id'
          : 'name',
    }));
  }, [memory, props.model, step, split, view]);

  // A request from the canvas: the section that a double click on the Model
  // node names opens, and its field takes the focus.
  const { onRevealed } = props;
  const requestedSection = props.reveal?.section;
  const requestSeq = props.reveal?.seq;
  const setSegment = view.setSegment;
  // The field that writes the model: the one of the block, or `with.model`
  // for a chat step of the old method and for a list.
  const modelEdit = !step
    ? undefined
    : agentRun
      ? readAgentRunInfo(step.value).edit
      : chat
        ? readChatInfo(step.value).edit
        : undefined;
  const modelPath = useRef('');
  modelPath.current =
    modelEdit && hasEffort(modelEdit) ? 'with.endpoint.model' : 'with.model';
  useEffect(() => {
    if (requestedSection === undefined || stepKey === null) return;
    setSegment(segmentOfSection(requestedSection));
    setReveal((last) => ({
      sections: [requestedSection],
      path: modelPath.current,
      seq: (last?.seq ?? 0) + 1,
    }));
    onRevealed?.();
  }, [requestedSection, requestSeq, stepKey, setSegment, onRevealed]);

  useEffect(() => {
    const root = rootRef.current;
    if (!reveal || !root) return;
    const box =
      reveal.path === null
        ? newStepBox(root, reveal.naming ?? 'name')
        : fieldBox(root, reveal.path);
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
  const isChat = chat;
  const isAgentRun = agentRun;
  const toolIssues = countUnder(fieldIssues, TOOL_FIELDS);
  const chatCounts = chatIssueCounts(fieldIssues);
  const chatTotal = isChat
    ? chatCounts.model +
      chatCounts.system +
      chatCounts.prompt +
      chatCounts.skills +
      chatCounts.output +
      chatCounts.options +
      chatCounts.other
    : 0;
  const otherKeys = isChat ? otherChatKeys(step.value) : [];
  const agentCounts = isAgentRun ? agentRunIssueCounts(fieldIssues) : null;
  const agentTotal = agentCounts
    ? agentCounts.model +
      agentCounts.system +
      agentCounts.prompt +
      agentCounts.tools +
      agentCounts.skills +
      agentCounts.limits +
      agentCounts.output +
      agentCounts.other
    : 0;
  const common = {
    step,
    readOnly: locked,
    onEdit: props.onEdit,
    fieldIssues,
    derivedName: props.serverStep?.name,
    candidates: props.candidates,
  };
  const counts = {
    general: countUnder(fieldIssues, GENERAL_FIELDS),
    action: countUnder(fieldIssues, ACTION_FIELDS),
    parameters:
      hasAction && !isChat && !isAgentRun
        ? countUnder(fieldIssues, ['with'])
        : 0,
    tools: isChat ? toolIssues : 0,
    flow: countUnder(fieldIssues, isAgentRun ? AGENT_FLOW_FIELDS : FLOW_FIELDS),
    security: countUnder(fieldIssues, [...SECURITY_FIELDS]),
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
  const parametersSection = hasAction && !isChat && !isAgentRun && (
    <Section
      title={<I18nText text="Parameters" />}
      count={counts.parameters}
      revealKey={revealKey('parameters')}
    >
      <ParametersSection
        {...common}
        schema={props.schema}
        customActions={props.customActions}
      />
    </Section>
  );
  // An agent step has the sections of a chat step where the keys are the same.
  const agentRunSections = isAgentRun && (
    <AgentRunSections
      {...common}
      candidates={props.candidates}
      dag={props.dag}
      revealKey={revealKey}
      {...(props.onOpenTool ? { onOpenTool: props.onOpenTool } : {})}
      {...(props.onAddTool ? { onAddTool: props.onAddTool } : {})}
    />
  );
  // A chat step has one section for each part of its form, in this order.
  const chatSections = isChat && (
    <>
      <Section
        title={<I18nText text="Model" />}
        count={chatCounts.model}
        revealKey={revealKey('model')}
      >
        <ModelSection
          {...common}
          candidates={props.candidates}
          dag={props.dag}
        />
      </Section>
      <Section
        title={<I18nText text="System message" />}
        count={chatCounts.system}
        revealKey={revealKey('system')}
      >
        <SystemSection {...common} candidates={props.candidates} />
      </Section>
      <Section
        title={<I18nText text="Prompt" />}
        count={chatCounts.prompt}
        revealKey={revealKey('prompt')}
      >
        <PromptSection {...common} candidates={props.candidates} />
      </Section>
      <Section
        title={<I18nText text="Tools" />}
        count={counts.tools}
        revealKey={revealKey('tools')}
      >
        <ToolsSection
          {...common}
          onOpenTool={props.onOpenTool}
          onAddTool={props.onAddTool}
        />
      </Section>
      <Section
        title={<I18nText text="Skills" />}
        count={chatCounts.skills}
        revealKey={revealKey('skills')}
      >
        <SkillsSection {...common} />
      </Section>
      <Section
        title={<I18nText text="Output" />}
        count={chatCounts.output}
        revealKey={revealKey('output')}
      >
        <OutputSection {...common} />
      </Section>
      <Section
        title={<I18nText text="Options" />}
        count={chatCounts.options}
        defaultOpen={false}
        revealKey={revealKey('options')}
      >
        <OptionsSection {...common} />
      </Section>
      {otherKeys.length > 0 && (
        <div className="space-y-1 border-b border-border px-3 py-2">
          <OtherKeys entries={otherKeys} />
          {otherKeys.map(([key]) => (
            <FieldIssues
              key={key}
              issues={issuesUnder(fieldIssues, `with.${key}`)}
            />
          ))}
        </div>
      )}
    </>
  );
  const flowSection = (
    <Section
      title={<I18nText text="Flow" />}
      count={counts.flow}
      revealKey={revealKey('flow')}
    >
      <FlowSection {...common} timeout={!isAgentRun} />
    </Section>
  );
  // The section draws nothing for a step that has no key of it to edit.
  const securitySection = (
    <SecuritySection
      {...common}
      candidates={props.candidates}
      count={counts.security}
      revealKey={revealKey('security')}
      agentDag={dagHasAgentRun(props.model)}
    />
  );
  const segments: { id: InspectorSegment; label: string; count: number }[] = [
    {
      id: 'parameters',
      label: ts('Parameters'),
      count:
        counts.general +
        counts.action +
        counts.parameters +
        counts.tools +
        chatTotal +
        agentTotal,
    },
    {
      id: 'settings',
      label: ts('Settings'),
      count: counts.flow + counts.security,
    },
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
              {chatSections}
              {agentRunSections}
            </div>
            <div data-segment="settings" hidden={view.segment !== 'settings'}>
              {flowSection}
              {securitySection}
            </div>
          </>
        ) : (
          <>
            {generalSection}
            {actionSection}
            {parametersSection}
            {chatSections}
            {agentRunSections}
            {flowSection}
            {securitySection}
            <Section title={<I18nText text="YAML" />} defaultOpen={false}>
              <StepYamlSection
                model={props.model}
                step={step}
                schema={props.schema}
                readOnly={locked}
                onEdit={props.onEdit}
                candidatesAtPath={
                  props.candidatesAtPath ?? (() => props.candidates)
                }
              />
            </Section>
          </>
        )}
        {props.runPanel}
      </div>
    </aside>
  );
}
