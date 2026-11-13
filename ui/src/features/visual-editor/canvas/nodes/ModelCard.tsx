// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import {
  modelProblem,
  type ModelProblem,
} from '@/features/ai-endpoints/modelCheck';
import type { ModelInfo } from '@/features/ai-endpoints/modelInfo';
import { Picker, type PickerItem } from '@/features/ai-endpoints/Picker';
import { useEndpointChoices } from '@/features/ai-endpoints/useEndpointChoices';
import { useEndpointModels } from '@/features/ai-endpoints/useEndpointModels';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { CanvasNode, DagEditOp, StepKey } from '../../contracts';
import { EndpointSelect } from '../../inspector/sections/EndpointField';
import { useCommittedDraft } from '../../inspector/sections/endpointModel/useCommittedDraft';
import { ReferenceInput } from '../../references';
import type { ModelEditData, ModelEditRow } from '../chatModel';
import { useCanvasActions } from '../context';
import {
  effortEdits,
  endpointEdits,
  endpointProtocols,
  hasEffort,
  listedModels,
  modelEdits,
  modelLevels,
  modelPickEdits,
} from '../modelEdits';
import type { ModelSatelliteData } from '../types';
import { modelIcon } from '../providerLogo';
import { CARD_OUTLINE } from './NodeParts';
import { SUB_ICON_SIZE } from '../subRow';
import { SUB_SELECTED_CLASS, SubHandle } from './SubLeaf';

type I18n = ReturnType<typeof useI18n>;

/** No reference to insert from the canvas: the field takes a typed `${...}`. */
const NO_CANDIDATES: never[] = [];

/**
 * The text of the amber marker of a problem, in one line. The line is the
 * tooltip and the accessible name of the marker.
 */
export function problemText(ts: I18n['ts'], problem: ModelProblem): string {
  switch (problem.kind) {
    case 'endpoint':
      return ts(
        'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.',
        { name: problem.name }
      );
    case 'protocol':
      return ts(
        'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.',
        { name: problem.name }
      );
    case 'model':
      return ts(
        'The model "{model}" is not in the list of the endpoint "{name}". The step fails when it starts, unless a refresh of the list finds it.',
        { model: problem.model, name: problem.endpoint }
      );
  }
}

/**
 * The problems that the server finds when the steps start, for the rows of
 * the Model node (Spec 093, "Use in a step"): one for each row, or null. The
 * request is the one that the Endpoint select makes, and nothing is requested
 * while `enabled` is false (a run view).
 */
export function useModelProblems(
  edit: ModelEditData | undefined,
  dag: { name: string; workspace?: string | undefined } | undefined,
  enabled: boolean
): (ModelProblem | null)[] {
  const { choices, loaded, cliModelAliases } = useEndpointChoices(
    dag,
    enabled && edit !== undefined
  );
  if (!edit) return [];
  const protocols = endpointProtocols(edit);
  return edit.rows.map((row) =>
    modelProblem({
      endpoint: row.endpoint,
      model: row.name,
      choices,
      loaded,
      cliProvider: edit.kind === 'agent' ? edit.cli : undefined,
      aliases: cliModelAliases,
      protocols,
    })
  );
}

/** The amber marker of a problem. It has a one-line tooltip. */
export function ProblemMark({
  text,
  className,
  error = false,
}: {
  text: string;
  className?: string;
  /** True for a step that cannot start (red): else it may fail later (amber). */
  error?: boolean;
}): ReactElement {
  return (
    <span
      role="img"
      aria-label={text}
      title={text}
      data-testid="ve-model-warning"
      className={cn(
        'inline-flex shrink-0',
        error ? 'text-destructive' : 'text-warning',
        className
      )}
    >
      <AlertTriangle className="h-3 w-3" aria-hidden="true" />
    </span>
  );
}

/**
 * The text field of the model of a row. It takes a `${...}` reference as free
 * text, and suggests the models of the endpoint of the row. It writes on
 * blur, with the edit of the form.
 */
function ModelText({
  row,
  label,
  disabled,
  warned,
  missing,
  marker,
  dag,
  onCommit,
}: {
  row: ModelEditRow;
  /** The accessible name and the hint of the empty field. */
  label: string;
  disabled: boolean;
  warned: boolean;
  /** True when the model is required and empty: a red border. */
  missing: boolean;
  /** A mark in the field, left of its Insert button. */
  marker?: ReactNode;
  dag: { name: string; workspace?: string | undefined } | undefined;
  onCommit(text: string | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const { models } = useEndpointModels(row.endpoint, dag);
  const { draft, setDraft, commit } = useCommittedDraft({
    value: row.name,
    readOnly: disabled,
    onCommit,
  });
  return (
    <div
      className={cn(
        'nodrag nopan relative min-w-0 flex-1 [&_input]:h-7 [&_input]:bg-card [&_input]:text-ellipsis [&_input]:shadow-sm',
        marker !== undefined && '[&_input]:pr-14',
        missing
          ? '[&_input]:border-destructive'
          : warned && '[&_input]:border-warning'
      )}
    >
      <ReferenceInput
        aria-label={label}
        placeholder={label}
        value={draft}
        candidates={NO_CANDIDATES}
        disabled={disabled}
        suggestions={models}
        suggestionsLabel={ts('Models of the endpoint')}
        onChange={setDraft}
        onBlur={commit}
      />
      {marker !== undefined && (
        <span className="absolute right-8 top-1/2 -translate-y-1/2">
          {marker}
        </span>
      )}
    </div>
  );
}

/**
 * The Model control of a row: a styled picker among the models that the row can
 * use, so that a name that nobody listed is never typed. The models are the
 * ones of the endpoint, and for an agent step the aliases of its CLI. A name
 * that the lists lack is shown as "(choose a model)": the YAML keeps it, and the
 * problem marker names it. A `${...}` reference stays as it is. A row with no
 * endpoint offers no model.
 * The list shows the display name of a model with its id under it. A chat
 * step with a provider of the old method, and an endpoint that has no models
 * list (or that the server does not know), have the text field instead.
 */
function ModelPick({
  owner,
  edit,
  row,
  label,
  disabled,
  warned,
  missing,
  noEndpoint,
  marker,
  dag,
}: {
  owner: StepKey;
  edit: ModelEditData;
  row: ModelEditRow;
  label: string;
  disabled: boolean;
  warned: boolean;
  /** The marker of a problem, inside the control. */
  marker?: ReactNode;
  /** True for an agent step that names no model: an error. */
  missing: boolean;
  /** True while the row has no endpoint: no model can be chosen yet. */
  noEndpoint: boolean;
  dag: { name: string; workspace?: string | undefined } | undefined;
}): ReactElement {
  const { ts } = useI18n();
  const { onEdit } = useCanvasActions();
  const { listed, ownList } = useModelChoices(edit, row, dag);
  const apply = (ops: DagEditOp[]) => {
    for (const op of ops) onEdit?.(op);
  };
  // With no list of the endpoint, the aliases of the CLI alone would hide the
  // model of the YAML: the field takes any text.
  if (!ownList && !noEndpoint) {
    return (
      <ModelText
        row={row}
        label={label}
        disabled={disabled}
        warned={warned}
        missing={missing}
        marker={marker}
        dag={dag}
        onCommit={(text) => apply(modelEdits(owner, edit, row, text))}
      />
    );
  }
  // A name that the lists lack is not offered and not shown: the picker asks
  // for a model again. The YAML keeps the name, and the marker names the
  // problem. A `${...}` reference stays as it is.
  const reference = row.name.includes('${');
  const known = listed.some((info) => info.id === row.name);
  const current = !noEndpoint && (known || reference) ? row.name : '';
  // "(choose a model)" is the text of the empty picker, never an item to pick.
  const items: PickerItem[] = [
    ...(reference && !noEndpoint ? [{ value: row.name, label: row.name }] : []),
    ...(noEndpoint ? [] : listed).map((info) => ({
      value: info.id,
      label: info.displayName ?? info.id,
      detail: info.displayName ? info.id : undefined,
    })),
  ];
  const shown = listed.find((info) => info.id === row.name)?.displayName;
  return (
    <Picker
      label={label}
      testId="ve-model-select"
      className={cn(
        'min-w-0 flex-1',
        missing ? 'border-destructive' : warned && 'border-warning'
      )}
      disabled={disabled}
      marker={marker}
      items={items}
      emptyText={noEndpoint ? ts('Select an endpoint') : undefined}
      value={current}
      onSelect={(name) =>
        apply(
          modelPickEdits(
            owner,
            edit,
            row,
            name || undefined,
            listed.find((info) => info.id === name)
          )
        )
      }
    >
      <span
        className={cn('truncate', current === '' && 'text-muted-foreground')}
      >
        {current === '' ? ts('(choose a model)') : (shown ?? current)}
      </span>
    </Picker>
  );
}

/**
 * The effort control of a single model: a picker with "Default" (which removes
 * the key) and the levels that the model supports. It is drawn disabled, with
 * a dash, when the model offers no level and the step has no effort, so that
 * the node keeps its height. An effort that the YAML holds stays visible, and
 * "Default" can remove it.
 */
function EffortPick({
  owner,
  edit,
  row,
  disabled,
  dag,
}: {
  owner: StepKey;
  edit: ModelEditData;
  row: ModelEditRow;
  disabled: boolean;
  dag: { name: string; workspace?: string | undefined } | undefined;
}): ReactElement {
  const { ts } = useI18n();
  const { onEdit } = useCanvasActions();
  const { levelsOf } = useModelChoices(edit, row, dag);
  const levels = levelsOf(row.name);
  const effort = edit.effort ?? '';
  const items: PickerItem[] = [
    { value: '', label: ts('Default') },
    ...levels.map((level) => ({ value: level, label: level })),
  ];
  if (effort !== '' && !levels.includes(effort)) {
    items.push({ value: effort, label: effort });
  }
  const none = levels.length === 0 && effort === '';
  return (
    <Picker
      label={ts('Effort')}
      testId="ve-effort-select"
      className="min-w-0 flex-1"
      disabled={disabled || none}
      items={items}
      value={effort}
      onSelect={(level) => {
        for (const op of effortEdits(owner, edit, level || undefined)) {
          onEdit?.(op);
        }
      }}
    >
      <span className="shrink-0 text-muted-foreground">{ts('Effort')} ·</span>
      <span className="truncate">
        {' '}
        {none ? '—' : effort === '' ? ts('Default').toLowerCase() : effort}
      </span>
    </Picker>
  );
}

/**
 * The models that a row can use, with their effort levels, as the Model
 * picker lists them. `ownList` is true when the endpoint of the row has a
 * models list of its own.
 */
function useModelChoices(
  edit: ModelEditData,
  row: ModelEditRow,
  dag: { name: string; workspace?: string | undefined } | undefined
): {
  listed: ModelInfo[];
  ownList: boolean;
  levelsOf(name: string): string[];
} {
  const { infos, cliModelAliases } = useEndpointModels(row.endpoint, dag);
  const agent = edit.kind === 'agent';
  return {
    listed: listedModels(infos, cliModelAliases, agent),
    ownList: infos.length > 0,
    levelsOf: (name) => modelLevels(infos, cliModelAliases, agent, name),
  };
}

function ModelRow({
  owner,
  edit,
  row,
  position,
  problem,
  usable,
  disabled,
}: {
  owner: StepKey;
  edit: ModelEditData;
  row: ModelEditRow;
  position: number;
  problem: ModelProblem | null;
  usable: readonly { name: string }[];
  disabled: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const { onEdit, dag } = useCanvasActions();
  const list = edit.rows.length > 1;
  const { choices, cliModelAliases } = useEndpointChoices(dag);
  const apply = (ops: ReturnType<typeof endpointEdits>) => {
    for (const op of ops) onEdit?.(op);
  };
  // A model needs an endpoint, or the provider of the old method of a chat step.
  const noEndpoint =
    row.endpoint === '' && (edit.kind === 'agent' || row.provider === '');
  // A chat step on the old method names a provider, not an endpoint: the
  // picker shows that provider, so that a model never stands under "(none)".
  const legacy =
    row.endpoint === '' && edit.kind === 'chat' && row.provider !== '';
  // A step with no endpoint is refused when the DAG loads: an error.
  const endpointWarned =
    noEndpoint ||
    legacy ||
    problem?.kind === 'endpoint' ||
    problem?.kind === 'protocol';
  const modelWarned = problem?.kind === 'model';
  // A row with an endpoint, and an entry of a list, need a model when the DAG
  // loads. A single model of the old method can come from the `llm` of the DAG.
  const modelMissing =
    !noEndpoint &&
    row.name === '' &&
    (row.index !== null || row.endpoint !== '');
  const text = legacy
    ? ts(
        'The provider "{provider}" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.',
        { provider: row.provider }
      )
    : noEndpoint
      ? ts(
          edit.kind === 'agent'
            ? edit.shape === 'block'
              ? 'agent.run needs the name of an endpoint'
              : 'agent.run needs an endpoint; add one on the AI page and name it here'
            : edit.shape === 'block'
              ? 'chat.completion needs the name of an endpoint'
              : 'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here'
        )
      : problem
        ? problemText(ts, problem)
        : '';
  const modelOff = disabled || noEndpoint;
  return (
    <div
      data-testid="ve-model-row"
      className={cn('flex flex-col gap-1', position > 0 && 'pt-1.5')}
    >
      {list && (
        <span className="h-3.5 text-center text-[10px] font-semibold uppercase leading-[14px] tracking-wide text-muted-foreground">
          {ts('Model {index}', { index: position + 1 })}
        </span>
      )}
      <div className="nodrag nopan flex h-7 items-center gap-1">
        <EndpointSelect
          label={
            list
              ? ts('Endpoint of model {index}', { index: position + 1 })
              : ts('Endpoint')
          }
          value={row.endpoint}
          readOnly={disabled}
          choices={usable}
          required={
            edit.kind === 'agent' ||
            edit.shape === 'block' ||
            noEndpoint ||
            legacy
          }
          title={legacy ? text : undefined}
          className={cn(
            'shadow-sm',
            noEndpoint
              ? 'border-destructive'
              : endpointWarned && 'border-warning'
          )}
          emptyLabel={
            noEndpoint
              ? ts('Select an endpoint')
              : legacy
                ? ts('{provider} (provider)', { provider: row.provider })
                : undefined
          }
          marker={
            endpointWarned ? (
              <ProblemMark text={text} error={noEndpoint} />
            ) : undefined
          }
          onSet={(value) =>
            apply(
              endpointEdits(owner, edit, row, value, {
                choices,
                aliases: cliModelAliases,
              })
            )
          }
        />
      </div>
      <div className="flex h-7 items-center gap-1">
        <ModelPick
          owner={owner}
          edit={edit}
          row={row}
          label={
            list
              ? ts('Name of model {index}', { index: position + 1 })
              : ts('Model')
          }
          disabled={disabled}
          warned={modelWarned}
          missing={modelMissing}
          noEndpoint={noEndpoint}
          marker={
            modelMissing ? (
              <ProblemMark
                error
                text={
                  edit.kind === 'agent'
                    ? ts(
                        'agent.run needs a model; choose one that the endpoint lists'
                      )
                    : ts(
                        'chat.completion needs a model; choose one that the endpoint lists'
                      )
                }
              />
            ) : modelWarned ? (
              <ProblemMark text={text} />
            ) : undefined
          }
          dag={dag}
        />
      </div>
      {hasEffort(edit) && (
        <div className="flex h-7 items-center gap-1">
          <EffortPick
            owner={owner}
            edit={edit}
            row={row}
            disabled={modelOff}
            dag={dag}
          />
        </div>
      )}
    </div>
  );
}

/**
 * The Model node of a step in the edit view of a canvas with details: the round
 * icon of the provider with, under it, two floating controls for each model:
 * the Endpoint picker, the Model picker and, for a single model, the Effort picker. They write the `with` of the step
 * as the form does (Spec 093, "The UI contract"). A model of a list is a block
 * of its own. An agent step has a muted line with its CLI provider, which the
 * form edits. A problem that the server finds when the step starts has a
 * marker inside the control that it names. A click selects the node with its
 * step; a double click does nothing, because the pickers edit the model.
 */
export function ModelCard({
  node,
  model,
  selected,
}: {
  node: CanvasNode;
  model: ModelSatelliteData;
  selected: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const { editable, onEdit, dag } = useCanvasActions();
  const edit = model.edit as ModelEditData;
  const { choices } = useEndpointChoices(dag);
  const problems = useModelProblems(edit, dag, true);
  const protocols = endpointProtocols(edit);
  const usable = choices.filter((choice) =>
    protocols.includes(choice.protocol)
  );
  const disabled = !editable || !onEdit;
  const Icon = modelIcon(
    choices.find((choice) => choice.name === edit.rows[0]?.endpoint)?.preset,
    model.models[0]?.provider ?? edit.cli
  );
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="model-card"
      role="group"
      aria-label={ts('Model')}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      className="group relative flex h-full w-full flex-col items-stretch text-card-foreground"
    >
      <SubHandle slot="model" leaf />
      {/* The pickers edit the model: the icon only shows the provider. */}
      <span
        data-testid="ve-canvas-sub-icon"
        aria-hidden="true"
        style={{ width: SUB_ICON_SIZE, height: SUB_ICON_SIZE }}
        className={cn(
          'relative mx-auto mb-1.5 flex shrink-0 select-none items-center justify-center rounded-full bg-card',
          CARD_OUTLINE,
          selected && SUB_SELECTED_CLASS
        )}
      >
        <Icon className="h-7 w-7 text-muted-foreground" aria-hidden="true" />
      </span>
      {edit.rows.map((row, position) => (
        <ModelRow
          key={row.index ?? 'model'}
          owner={model.owner}
          edit={edit}
          row={row}
          position={position}
          problem={problems[position] ?? null}
          usable={usable}
          disabled={disabled}
        />
      ))}
      {edit.kind === 'agent' && edit.cli !== '' && (
        <p
          data-testid="ve-model-cli"
          title={ts('The CLI that runs the step. Change it in the form.')}
          className="h-[18px] truncate text-center text-[11px] leading-[18px] text-muted-foreground"
        >
          {edit.cli}
        </p>
      )}
    </div>
  );
}
