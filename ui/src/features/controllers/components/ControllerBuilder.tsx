// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import {
  AlertTriangle,
  Bot,
  CircleDot,
  FileText,
  ListTree,
  Plus,
  Trash2,
} from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { withWorkspaceLabel, withoutWorkspaceLabels } from '@/lib/workspace';
import {
  CONTROLLER_LLM_PROVIDER_OPTIONS,
  CONTROLLER_STATE_NAME_PATTERN,
  MAX_CONTROLLER_MAX_TURNS,
  MIN_CONTROLLER_MAX_TURNS,
} from '../constraints';
import type { ControllerDAGOption } from '../dagOptions';
import { ROUTER_INSTRUCTION, systemSuffix, withSystemSuffix } from '../draft';
import type { ControllerDefinition, ControllerState } from '../types';
import { ControllerGraph } from './ControllerGraph';

type BuilderSelection =
  | { kind: 'basics' }
  | { kind: 'router' }
  | { kind: 'dags' }
  | { kind: 'state'; state: string }
  | { kind: 'transition'; state: string; index: number };

type Props = {
  definition: ControllerDefinition;
  workspace: string;
  dagSearch: string;
  onDAGSearchChange: (value: string) => void;
  availableDAGs?: ControllerDAGOption[];
  availableDAGsError?: string;
  availableDAGsLoading?: boolean;
  onRetryAvailableDAGs?: () => void;
  readOnly?: boolean;
  onDraftDirtyChange?: (dirty: boolean) => void;
  onChange: (definition: ControllerDefinition) => void;
};

function cloneDefinition(value: ControllerDefinition): ControllerDefinition {
  return {
    ...value,
    labels: [...value.labels],
    dags: [...value.dags],
    llm: { ...value.llm },
    states: Object.fromEntries(
      Object.entries(value.states).map(([name, state]) => [
        name,
        {
          ...state,
          dags: [...state.dags],
          transitions: state.transitions.map((transition) => ({
            ...transition,
          })),
        },
      ])
    ),
  };
}

function parseList(value: string): string[] {
  return [
    ...new Set(
      value
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean)
    ),
  ];
}

function Field({
  label,
  help,
  required = false,
  children,
}: {
  label: string;
  help?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs font-semibold">
        {label}
        {required && <span className="ml-1 text-destructive">*</span>}
      </Label>
      {children}
      {help && (
        <p className="text-[11px] leading-relaxed text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  );
}

function InspectorHeading({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string;
  title: string;
  description: string;
}) {
  return (
    <div className="border-b border-border pb-4">
      <p className="text-[10px] font-bold uppercase tracking-[0.14em] text-primary">
        {eyebrow}
      </p>
      <h2 className="mt-1 font-mono text-lg font-semibold text-foreground">
        {title}
      </h2>
      <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
        {description}
      </p>
    </div>
  );
}

function SectionHeading({
  title,
  help,
  count,
  action,
}: {
  title: string;
  help: string;
  count?: number;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div>
        <p className="text-xs font-semibold text-foreground">{title}</p>
        <p className="mt-0.5 text-[11px] text-muted-foreground">{help}</p>
      </div>
      {action ??
        (count !== undefined && (
          <Badge variant="secondary" className="font-mono text-[10px]">
            {count}
          </Badge>
        ))}
    </div>
  );
}

function StateInspector({
  name,
  state,
  controllerDAGs,
  readOnly,
  onDraftDirtyChange,
  onRename,
  onRemove,
  onChange,
  onSelectTransition,
  onAddTransition,
}: {
  name: string;
  state: ControllerState;
  controllerDAGs: string[];
  readOnly: boolean;
  onDraftDirtyChange: (dirty: boolean) => void;
  onRename: (nextName: string) => boolean;
  onRemove: () => void;
  onChange: (state: ControllerState) => void;
  onSelectTransition: (index: number) => void;
  onAddTransition: () => void;
}) {
  const [nameDraft, setNameDraft] = React.useState(name);

  React.useEffect(() => setNameDraft(name), [name]);

  const update = (patch: Partial<ControllerState>) =>
    onChange({ ...state, ...patch });

  return (
    <div className="space-y-5">
      <InspectorHeading
        eyebrow="State"
        title={name}
        description="A user-defined routing State. Runtime Status remains owned by Dagu."
      />

      <Field
        label="State key"
        required
        help="Used by YAML, graph edges, and the Router tool schema."
      >
        <Input
          aria-label={`State name ${name}`}
          value={nameDraft}
          readOnly={name === 'default'}
          disabled={readOnly}
          onChange={(event) => {
            setNameDraft(event.target.value);
            onDraftDirtyChange(event.target.value !== name);
          }}
          onBlur={() => {
            if (!onRename(nameDraft)) setNameDraft(name);
            onDraftDirtyChange(false);
          }}
          className="font-mono"
        />
      </Field>

      <Field
        label="Description"
        help="Natural-language context for the Router; not an expression."
      >
        <Textarea
          value={state.description ?? ''}
          disabled={readOnly}
          onChange={(event) => update({ description: event.target.value })}
          className="min-h-24"
        />
      </Field>

      <Field
        label="Terminal outcome"
        help="Dagu derives the final Controller Status from a completed terminal route."
      >
        <div
          className="grid grid-cols-3 rounded-md border border-border bg-muted/30 p-1"
          role="group"
          aria-label="Terminal outcome"
        >
          {[
            ['', 'None'],
            ['succeeded', 'Success'],
            ['failed', 'Failed'],
          ].map(([value, label]) => (
            <button
              key={value || 'none'}
              type="button"
              disabled={readOnly}
              className={cn(
                'min-h-8 rounded px-2 text-xs font-medium transition-colors',
                (state.terminal ?? '') === value
                  ? 'bg-primary/20 text-foreground shadow-sm'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground'
              )}
              onClick={() =>
                update({
                  terminal:
                    value === 'succeeded' || value === 'failed'
                      ? value
                      : undefined,
                })
              }
            >
              {label}
            </button>
          ))}
        </div>
      </Field>

      {state.terminal &&
        (state.dags.length > 0 || state.transitions.length > 0) && (
          <Alert variant="destructive">
            <AlertDescription>
              Remove State DAGs and outgoing transitions before saving a
              terminal State.
            </AlertDescription>
          </Alert>
        )}

      <div className="border-t border-border pt-5">
        <SectionHeading
          title="DAGs in this State"
          help="Available after routing into this State."
          count={state.dags.length}
        />
        <div className="mt-3 space-y-2">
          {controllerDAGs.map((dag) => {
            const checked = state.dags.includes(dag);
            return (
              <label
                key={dag}
                className="flex cursor-pointer items-center gap-3 rounded-md border border-border bg-muted/20 px-3 py-2.5 hover:bg-muted/40"
              >
                <input
                  type="checkbox"
                  aria-label={`${dag} for ${name}`}
                  checked={checked}
                  disabled={readOnly || Boolean(state.terminal)}
                  onChange={() =>
                    update({
                      dags: checked
                        ? state.dags.filter((candidate) => candidate !== dag)
                        : [...state.dags, dag],
                    })
                  }
                  className="size-4 accent-primary"
                />
                <span className="min-w-0 truncate font-mono text-xs">
                  {dag}
                </span>
              </label>
            );
          })}
          {controllerDAGs.length === 0 && (
            <p className="rounded-md border border-dashed border-border p-3 text-center text-[11px] text-muted-foreground">
              No Controller-level DAGs are enabled.
            </p>
          )}
        </div>
      </div>

      <div className="border-t border-border pt-5">
        <SectionHeading
          title="Outgoing transitions"
          help="Natural-language evidence evaluated by the Router."
          action={
            <Button
              variant="secondary"
              size="xs"
              disabled={readOnly || Boolean(state.terminal)}
              onClick={onAddTransition}
            >
              <Plus className="h-3.5 w-3.5" />
              Add
            </Button>
          }
        />
        <div className="mt-3 space-y-2">
          {state.transitions.map((transition, index) => (
            <button
              key={`${transition.to}-${index}`}
              type="button"
              className="w-full rounded-md border border-border bg-muted/20 p-3 text-left transition-colors hover:border-primary/40 hover:bg-primary/5"
              onClick={() => onSelectTransition(index)}
            >
              <span className="flex items-center gap-2 text-xs">
                <Badge variant="secondary" className="font-mono text-[9px]">
                  T{index + 1}
                </Badge>
                <code className="truncate">{name}</code>
                <span className="text-muted-foreground">→</span>
                <code className="truncate text-primary">{transition.to}</code>
              </span>
              <span className="mt-1.5 line-clamp-2 block text-[11px] leading-relaxed text-muted-foreground">
                {transition.when || 'Transition condition is required.'}
              </span>
            </button>
          ))}
          {state.transitions.length === 0 && (
            <p className="rounded-md border border-dashed border-border p-3 text-center text-[11px] text-muted-foreground">
              No outgoing transitions.
            </p>
          )}
        </div>
      </div>

      {name !== 'default' && (
        <div className="border-t border-border pt-5">
          <Button
            variant="destructive"
            size="sm"
            disabled={readOnly}
            onClick={onRemove}
            aria-label={`Delete state ${name}`}
          >
            <Trash2 className="h-4 w-4" />
            Delete State
          </Button>
        </div>
      )}
    </div>
  );
}

function TransitionInspector({
  definition,
  stateName,
  index,
  readOnly,
  onChange,
  onDelete,
}: {
  definition: ControllerDefinition;
  stateName: string;
  index: number;
  readOnly: boolean;
  onChange: (patch: { to?: string; when?: string }) => void;
  onDelete: () => void;
}) {
  const transition = definition.states[stateName]?.transitions[index];
  if (!transition) return null;

  return (
    <div className="space-y-5">
      <InspectorHeading
        eyebrow={`Transition · T${index + 1}`}
        title={`${stateName} → ${transition.to}`}
        description="The Router evaluates this natural-language condition against current evidence."
      />
      <Field label="From">
        <Input value={stateName} readOnly className="font-mono" />
      </Field>
      <Field label="Destination State" required>
        <select
          aria-label={`Transition ${index + 1} destination`}
          value={transition.to}
          disabled={readOnly}
          onChange={(event) => onChange({ to: event.target.value })}
          className="h-9 w-full rounded-md border border-input bg-card px-3 text-sm"
        >
          {Object.keys(definition.states).map((stateNameOption) => (
            <option key={stateNameOption} value={stateNameOption}>
              {stateNameOption}
            </option>
          ))}
        </select>
      </Field>
      <Field
        label="When"
        required
        help="Describe observable conditions. This is LLM guidance, not a boolean expression."
      >
        <Textarea
          aria-label={`Transition ${index + 1} condition`}
          value={transition.when}
          disabled={readOnly}
          onChange={(event) => onChange({ when: event.target.value })}
          className="min-h-40"
          placeholder="What evidence makes this transition appropriate?"
        />
      </Field>
      <Alert>
        <AlertDescription>
          A transition alone does not execute. One Router decision couples the
          next State with run, wait, or complete.
        </AlertDescription>
      </Alert>
      <Button
        variant="destructive"
        size="sm"
        disabled={readOnly}
        onClick={onDelete}
        aria-label={`Delete transition ${index + 1}`}
      >
        <Trash2 className="h-4 w-4" />
        Delete transition
      </Button>
    </div>
  );
}

function OutlineButton({
  active,
  icon,
  label,
  count,
  onClick,
}: {
  active: boolean;
  icon: React.ReactNode;
  label: string;
  count?: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-label={count === undefined ? label : `${label} ${count}`}
      className={cn(
        'flex min-h-9 w-full items-center gap-2 rounded-md border px-2.5 text-left text-xs transition-colors',
        active
          ? 'border-primary/40 bg-primary/10 text-foreground'
          : 'border-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground'
      )}
      onClick={onClick}
    >
      <span className="grid size-5 place-items-center rounded border border-border bg-muted/40">
        {icon}
      </span>
      <span>{label}</span>
      {count !== undefined && (
        <span className="ml-auto font-mono text-[10px]">{count}</span>
      )}
    </button>
  );
}

export function ControllerBuilder({
  definition,
  workspace,
  dagSearch,
  onDAGSearchChange,
  availableDAGs = [],
  availableDAGsError,
  availableDAGsLoading = false,
  onRetryAvailableDAGs,
  readOnly = false,
  onDraftDirtyChange = () => {},
  onChange,
}: Props) {
  const [selection, setSelection] = React.useState<BuilderSelection>({
    kind: 'state',
    state: 'default',
  });
  const [builderMessage, setBuilderMessage] = React.useState<string | null>(
    null
  );
  const serializedLabels = withoutWorkspaceLabels(definition.labels).join(', ');
  const [labelListDraft, setLabelListDraft] = React.useState(serializedLabels);
  const suffix = systemSuffix(definition.llm.system);
  const builderReadOnly = readOnly || suffix === null;

  React.useEffect(() => {
    setLabelListDraft(serializedLabels);
  }, [serializedLabels]);

  React.useEffect(() => {
    if (
      (selection.kind === 'state' || selection.kind === 'transition') &&
      !definition.states[selection.state]
    ) {
      setSelection({ kind: 'state', state: 'default' });
      return;
    }
    if (
      selection.kind === 'transition' &&
      !definition.states[selection.state]?.transitions[selection.index]
    ) {
      setSelection({ kind: 'state', state: selection.state });
    }
  }, [definition.states, selection]);

  const commit = React.useCallback(
    (mutate: (draft: ControllerDefinition) => void) => {
      const draft = cloneDefinition(definition);
      mutate(draft);
      onChange(draft);
      setBuilderMessage(null);
    },
    [definition, onChange]
  );

  const renameState = (name: string, nextName: string) => {
    if (nextName === name) return true;
    if (!CONTROLLER_STATE_NAME_PATTERN.test(nextName)) {
      setBuilderMessage(
        'State names must start with a letter and use letters, numbers, _ or -.'
      );
      return false;
    }
    if (Object.prototype.hasOwnProperty.call(definition.states, nextName)) {
      setBuilderMessage(`State ${nextName} already exists.`);
      return false;
    }
    commit((draft) => {
      const renamed = draft.states[name];
      if (!renamed) return;
      draft.states = Object.fromEntries(
        Object.entries(draft.states).map(([stateName, state]) => [
          stateName === name ? nextName : stateName,
          {
            ...state,
            transitions: state.transitions.map((transition) => ({
              ...transition,
              to: transition.to === name ? nextName : transition.to,
            })),
          },
        ])
      );
    });
    setSelection({ kind: 'state', state: nextName });
    return true;
  };

  const removeState = (name: string) => {
    const state = definition.states[name];
    if (state && (state.dags.length > 0 || state.transitions.length > 0)) {
      setBuilderMessage(
        `Clear DAGs and transitions from ${name} before deleting the state.`
      );
      return;
    }
    const inbound = Object.entries(definition.states).find(([, candidate]) =>
      candidate.transitions.some((transition) => transition.to === name)
    );
    if (inbound) {
      setBuilderMessage(
        `Remove transitions to ${name} before deleting the state.`
      );
      return;
    }
    commit((draft) => {
      delete draft.states[name];
    });
    setSelection({ kind: 'state', state: 'default' });
  };

  const updateControllerDAGs = (nextDAGs: string[]) => {
    const requested = parseList(nextDAGs.join(','));
    const referenced = new Set(
      Object.values(definition.states).flatMap((state) => state.dags)
    );
    const removedReference = [...referenced].find(
      (dag) => !requested.includes(dag)
    );
    if (removedReference) {
      setBuilderMessage(
        `Remove ${removedReference} from every state before removing it from the allowlist.`
      );
      return false;
    }
    commit((draft) => {
      draft.dags = requested;
    });
    return true;
  };

  const addState = () => {
    let index = Object.keys(definition.states).length + 1;
    let name = `state${index}`;
    while (definition.states[name]) name = `state${++index}`;
    commit((draft) => {
      draft.states[name] = {
        description: '',
        dags: [],
        transitions: [],
      };
    });
    setSelection({ kind: 'state', state: name });
  };

  const addTransition = (stateName: string) => {
    const state = definition.states[stateName];
    if (!state) return;
    const index = state.transitions.length;
    commit((draft) => {
      draft.states[stateName]?.transitions.push({
        to:
          Object.keys(draft.states).find(
            (candidate) => candidate !== stateName
          ) ?? stateName,
        when: '',
      });
    });
    setSelection({ kind: 'transition', state: stateName, index });
  };

  const selectedState =
    selection.kind === 'state' || selection.kind === 'transition'
      ? selection.state
      : undefined;

  const renderInspector = () => {
    if (selection.kind === 'basics') {
      return (
        <div className="space-y-5">
          <InspectorHeading
            eyebrow="Controller"
            title="Basics"
            description="Identity and descriptive metadata for this Controller definition."
          />
          <Field
            label="Name"
            required
            help="Editable display name. It does not change the immutable ID."
          >
            <Input
              value={definition.name}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.name = event.target.value;
                })
              }
            />
          </Field>
          <Field
            label="Controller ID"
            help="Generated on creation and immutable afterwards."
          >
            <Input
              value={definition.id ?? 'Assigned after save'}
              readOnly
              className="font-mono"
            />
          </Field>
          <Field
            label="Description"
            help="Shown in lists and routing metadata."
          >
            <Textarea
              value={definition.description ?? ''}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.description = event.target.value;
                })
              }
              className="min-h-24"
            />
          </Field>
          <Field label="Maximum turns">
            <Input
              type="number"
              min={MIN_CONTROLLER_MAX_TURNS}
              max={MAX_CONTROLLER_MAX_TURNS}
              value={definition.maxTurns}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.maxTurns = Number.parseInt(event.target.value, 10) || 0;
                })
              }
            />
          </Field>
          <Field label="Workspace">
            <Input value={workspace || 'default'} readOnly />
          </Field>
          <Field
            label="Labels"
            help="Comma-separated. Workspace scope is immutable after creation."
          >
            <Input
              aria-label="Controller labels"
              value={labelListDraft}
              disabled={builderReadOnly}
              onChange={(event) => {
                setLabelListDraft(event.target.value);
                onDraftDirtyChange(event.target.value !== serializedLabels);
              }}
              onBlur={() => {
                const labels = parseList(labelListDraft);
                setLabelListDraft(labels.join(', '));
                commit((draft) => {
                  draft.labels = withWorkspaceLabel(labels, workspace);
                });
                onDraftDirtyChange(false);
              }}
            />
          </Field>
          <Alert>
            <AlertDescription>
              State is not Status. States are user-defined routing nodes;
              runtime Status remains Dagu-owned.
            </AlertDescription>
          </Alert>
          <div className="grid grid-cols-3 overflow-hidden rounded-md border border-border text-center">
            {[
              ['type', 'controller'],
              ['version', '1'],
              ['initial State', 'default'],
            ].map(([label, value]) => (
              <span
                key={label}
                className="border-r border-border px-2 py-2 last:border-r-0"
              >
                <code className="block text-[9px] text-muted-foreground">
                  {label}
                </code>
                <strong className="mt-1 block text-[10px]">{value}</strong>
              </span>
            ))}
          </div>
        </div>
      );
    }

    if (selection.kind === 'router') {
      return (
        <div className="space-y-5">
          <InspectorHeading
            eyebrow="Router LLM"
            title="Decision model"
            description="Uses the same providers as chat.completion through a strict Controller profile."
          />
          <Field label="Provider" required>
            <select
              value={definition.llm.provider}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.llm.provider = event.target.value;
                })
              }
              className="h-9 w-full rounded-md border border-input bg-card px-3 text-sm"
            >
              {CONTROLLER_LLM_PROVIDER_OPTIONS.map(({ value, label }) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Model" required>
            <Input
              value={definition.llm.model}
              disabled={builderReadOnly}
              placeholder="Provider model ID"
              onChange={(event) =>
                commit((draft) => {
                  draft.llm.model = event.target.value;
                })
              }
            />
          </Field>
          <Field
            label="Built-in Router instruction"
            help="Required at byte 0. The reserved action remains read-only."
          >
            <div className="flex items-center justify-between gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2">
              <code className="truncate text-xs text-primary">
                {ROUTER_INSTRUCTION}
              </code>
              <Badge variant="primary" className="text-[9px]">
                Required
              </Badge>
            </div>
          </Field>
          <Field
            label="Additional system instructions"
            help="Literal text appended after one blank line."
          >
            <Textarea
              value={suffix ?? ''}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.llm.system = withSystemSuffix(event.target.value);
                })
              }
              className="min-h-32"
            />
          </Field>
          <Alert variant="warning">
            <AlertTriangle className="h-4 w-4" />
            <AlertDescription>
              System instructions, user prompts, and bounded DAG outputs may be
              sent to the configured provider. Do not include secrets.
            </AlertDescription>
          </Alert>
        </div>
      );
    }

    if (selection.kind === 'dags') {
      return (
        <div className="space-y-5">
          <InspectorHeading
            eyebrow="Hard allowlist"
            title="Allowed DAGs"
            description="The Router can never start a DAG outside this Controller-level list."
          />
          <Alert>
            <AlertDescription>
              State-level DAG lists must be subsets of this allowlist. A DAG
              referenced by a State cannot be removed here.
            </AlertDescription>
          </Alert>

          <SectionHeading
            title="Selected DAGs"
            help="Available to one or more Controller States."
            count={definition.dags.length}
          />
          <div className="space-y-2">
            {definition.dags.map((dag) => {
              const usedBy = Object.entries(definition.states)
                .filter(([, state]) => state.dags.includes(dag))
                .map(([name]) => name);
              return (
                <label
                  key={dag}
                  className="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-muted/20 p-3 hover:bg-muted/40"
                >
                  <input
                    type="checkbox"
                    checked
                    disabled={builderReadOnly}
                    onChange={() =>
                      updateControllerDAGs(
                        definition.dags.filter((candidate) => candidate !== dag)
                      )
                    }
                    className="mt-0.5 size-4 accent-primary"
                  />
                  <span className="min-w-0">
                    <code className="block truncate text-xs">{dag}</code>
                    <span className="mt-1 block text-[10px] text-muted-foreground">
                      {usedBy.length > 0
                        ? `Used by: ${usedBy.join(', ')}`
                        : 'Not assigned to a State'}
                    </span>
                  </span>
                </label>
              );
            })}
            {definition.dags.length === 0 && (
              <p className="rounded-md border border-dashed border-border p-3 text-center text-[11px] text-muted-foreground">
                No DAGs are allowed yet.
              </p>
            )}
          </div>

          <div className="border-t border-border pt-5">
            <SectionHeading
              title="Find compatible DAGs"
              help="Searches this workspace only; no full DAG list is loaded."
            />
            <Input
              aria-label="Search compatible DAGs"
              value={dagSearch}
              onChange={(event) => onDAGSearchChange(event.target.value)}
              placeholder="Search by DAG name…"
              disabled={builderReadOnly}
              className="mt-3"
            />
            {!dagSearch.trim() && (
              <p className="mt-2 text-[11px] text-muted-foreground">
                Enter a DAG name to load up to 20 compatible results.
              </p>
            )}
            {availableDAGsError && dagSearch.trim() && (
              <Alert variant="destructive" className="mt-3">
                <AlertDescription className="space-y-2">
                  <span className="block">{availableDAGsError}</span>
                  {onRetryAvailableDAGs && (
                    <Button
                      type="button"
                      size="xs"
                      variant="outline"
                      disabled={availableDAGsLoading}
                      onClick={onRetryAvailableDAGs}
                    >
                      Retry
                    </Button>
                  )}
                </AlertDescription>
              </Alert>
            )}
            {availableDAGsLoading && dagSearch.trim() && (
              <p className="mt-3 text-[11px] text-muted-foreground">
                Searching compatible DAGs…
              </p>
            )}
            {dagSearch.trim() &&
              !availableDAGsLoading &&
              !availableDAGsError && (
                <div className="mt-3 space-y-2">
                  {availableDAGs.map((dag) => {
                    const selected = definition.dags.includes(dag.fileName);
                    return (
                      <label
                        key={dag.fileName}
                        className="flex cursor-pointer items-start gap-3 rounded-md border border-border bg-muted/20 p-3 hover:bg-muted/40"
                      >
                        <input
                          type="checkbox"
                          checked={selected}
                          disabled={builderReadOnly}
                          onChange={() =>
                            updateControllerDAGs(
                              selected
                                ? definition.dags.filter(
                                    (candidate) => candidate !== dag.fileName
                                  )
                                : [...definition.dags, dag.fileName]
                            )
                          }
                          className="mt-0.5 size-4 accent-primary"
                        />
                        <span className="min-w-0">
                          <code className="block truncate text-xs">
                            {dag.fileName}
                          </code>
                          {dag.description && (
                            <span className="mt-1 line-clamp-2 block text-[10px] text-muted-foreground">
                              {dag.description}
                            </span>
                          )}
                        </span>
                      </label>
                    );
                  })}
                  {availableDAGs.length === 0 && (
                    <p className="rounded-md border border-dashed border-border p-3 text-center text-[11px] text-muted-foreground">
                      No compatible DAGs match this search.
                    </p>
                  )}
                </div>
              )}
          </div>
        </div>
      );
    }

    if (selection.kind === 'transition') {
      return (
        <TransitionInspector
          definition={definition}
          stateName={selection.state}
          index={selection.index}
          readOnly={builderReadOnly}
          onChange={(patch) =>
            commit((draft) => {
              const transition =
                draft.states[selection.state]?.transitions[selection.index];
              if (transition) Object.assign(transition, patch);
            })
          }
          onDelete={() => {
            commit((draft) => {
              draft.states[selection.state]?.transitions.splice(
                selection.index,
                1
              );
            });
            setSelection({ kind: 'state', state: selection.state });
          }}
        />
      );
    }

    const state = definition.states[selection.state];
    if (!state) return null;
    return (
      <StateInspector
        key={selection.state}
        name={selection.state}
        state={state}
        controllerDAGs={definition.dags}
        readOnly={builderReadOnly}
        onDraftDirtyChange={onDraftDirtyChange}
        onRename={(nextName) => renameState(selection.state, nextName)}
        onRemove={() => removeState(selection.state)}
        onChange={(nextState) =>
          commit((draft) => {
            draft.states[selection.state] = nextState;
          })
        }
        onSelectTransition={(index) =>
          setSelection({
            kind: 'transition',
            state: selection.state,
            index,
          })
        }
        onAddTransition={() => addTransition(selection.state)}
      />
    );
  };

  return (
    <div>
      {suffix === null && (
        <Alert variant="warning" className="mb-3">
          <AlertDescription>
            The raw system prompt cannot be represented safely in Builder. Fix
            it in Advanced YAML first.
          </AlertDescription>
        </Alert>
      )}
      {builderMessage && (
        <Alert variant="warning" className="mb-3">
          <AlertDescription>{builderMessage}</AlertDescription>
        </Alert>
      )}

      <div className="grid min-h-[680px] overflow-hidden rounded-b-md border border-border bg-card lg:h-[calc(100vh-15rem)] lg:grid-cols-[13.5rem_minmax(25rem,1fr)_22rem]">
        <aside
          className="flex min-h-0 flex-col border-b border-border bg-muted/10 lg:border-b-0 lg:border-r"
          aria-label="Builder outline"
        >
          <div className="flex min-h-11 items-center justify-between border-b border-border px-3">
            <span className="text-xs font-semibold">Outline</span>
            <span className="text-[9px] font-semibold uppercase tracking-wider text-muted-foreground">
              {Object.keys(definition.states).length} states
            </span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-2">
            <div className="space-y-1">
              <OutlineButton
                active={selection.kind === 'basics'}
                icon={<FileText className="h-3.5 w-3.5" />}
                label="Basics"
                onClick={() => setSelection({ kind: 'basics' })}
              />
              <OutlineButton
                active={selection.kind === 'router'}
                icon={<Bot className="h-3.5 w-3.5" />}
                label="Router LLM"
                onClick={() => setSelection({ kind: 'router' })}
              />
              <OutlineButton
                active={selection.kind === 'dags'}
                icon={<ListTree className="h-3.5 w-3.5" />}
                label="Allowed DAGs"
                count={definition.dags.length}
                onClick={() => setSelection({ kind: 'dags' })}
              />
            </div>

            <p className="mb-2 mt-5 px-2 text-[9px] font-bold uppercase tracking-[0.14em] text-muted-foreground">
              States
            </p>
            <div className="space-y-1">
              {Object.entries(definition.states).map(([name, state]) => {
                const active = selectedState === name;
                return (
                  <button
                    key={name}
                    type="button"
                    aria-label={[
                      name,
                      name === 'default' ? 'initial' : state.terminal,
                    ]
                      .filter(Boolean)
                      .join(' ')}
                    className={cn(
                      'flex min-h-9 w-full items-center gap-2 rounded-md border px-2.5 text-left transition-colors',
                      active
                        ? 'border-primary/40 bg-primary/10 text-foreground'
                        : 'border-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                    )}
                    onClick={() => setSelection({ kind: 'state', state: name })}
                  >
                    <CircleDot
                      className={cn(
                        'h-3 w-3 shrink-0',
                        state.terminal === 'succeeded' && 'text-success',
                        state.terminal === 'failed' && 'text-destructive',
                        !state.terminal && active && 'text-primary'
                      )}
                    />
                    <code className="min-w-0 flex-1 truncate text-[11px]">
                      {name}
                    </code>
                    <span className="text-[8px] font-semibold uppercase text-muted-foreground">
                      {name === 'default' ? 'initial' : state.terminal}
                    </span>
                  </button>
                );
              })}
            </div>
            <Button
              variant="outline"
              size="sm"
              disabled={builderReadOnly}
              onClick={addState}
              aria-label="Add state"
              className="mt-3 w-full border-dashed"
            >
              <Plus className="h-4 w-4" />
              Add State
            </Button>
          </div>
        </aside>

        <section
          className="flex min-h-[520px] min-w-0 flex-col bg-background/40"
          aria-label="State graph"
        >
          <div className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-3">
            <div>
              <div
                id="controller-state-graph-heading"
                className="text-xs font-semibold"
              >
                State graph
                <span className="ml-2 text-[9px] font-bold uppercase tracking-wider text-primary">
                  Mermaid
                </span>
              </div>
              <p className="mt-0.5 text-[10px] text-muted-foreground">
                Auto-layout from States and transitions · positions are not
                stored in YAML
              </p>
            </div>
            <span className="hidden items-center gap-1.5 text-[9px] text-muted-foreground sm:flex">
              <span className="size-1.5 rounded-full bg-muted-foreground" />
              Definition only · not running
            </span>
          </div>
          <div className="flex min-h-10 items-center gap-2 border-b border-border bg-muted/10 px-3 text-[10px] text-muted-foreground">
            {selection.kind === 'transition' ? (
              <>
                <Badge variant="primary" className="font-mono text-[9px]">
                  T{selection.index + 1}
                </Badge>
                <code>
                  {selection.state} →{' '}
                  {
                    definition.states[selection.state]?.transitions[
                      selection.index
                    ]?.to
                  }
                </code>
                <span className="truncate">
                  {
                    definition.states[selection.state]?.transitions[
                      selection.index
                    ]?.when
                  }
                </span>
              </>
            ) : (
              <>
                <Badge variant="secondary" className="font-mono text-[9px]">
                  T#
                </Badge>
                Select a transition in Inspector to read its full condition.
              </>
            )}
          </div>
          <div className="min-h-0 flex-1 p-3">
            <ControllerGraph
              definition={definition}
              currentState={selectedState}
              className="h-full min-h-[420px] border-0 bg-transparent shadow-none"
            />
          </div>
          <div className="flex min-h-9 flex-wrap items-center gap-4 border-t border-border px-3 text-[9px] text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span className="size-2 rounded-full border border-primary bg-primary/40" />
              Initial / selected
            </span>
            <span className="flex items-center gap-1.5">
              <span className="size-2 rounded-full border border-success bg-success/40" />
              Success terminal
            </span>
            <span className="flex items-center gap-1.5">
              <span className="size-2 rounded-full border border-destructive bg-destructive/40" />
              Failed terminal
            </span>
          </div>
        </section>

        <aside
          className="flex min-h-0 flex-col border-t border-border bg-muted/10 lg:border-l lg:border-t-0"
          aria-label="Definition inspector"
        >
          <div className="flex min-h-11 items-center justify-between border-b border-border px-3">
            <span className="text-xs font-semibold">Inspector</span>
            <span className="text-[9px] font-semibold uppercase tracking-wider text-muted-foreground">
              {selection.kind}
            </span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-4">
            {renderInspector()}
          </div>
        </aside>
      </div>
    </div>
  );
}
