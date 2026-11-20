// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowDown, ArrowUp, List, Trash2 } from 'lucide-react';
import { useId, useState, type ReactElement, type ReactNode } from 'react';
import { AIEndpointProtocol } from '@/api/v1/schema';
import { IconButton } from '@/components/ui/icon-button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import {
  useEndpointChoices,
  type DagIdentity,
} from '@/features/ai-endpoints/useEndpointChoices';
import {
  readChatInfo,
  type ModelEditData,
  type ModelEditRow,
} from '../../../canvas/chatModel';
import {
  endpointEdits,
  hasEffort,
  modelEdits,
} from '../../../canvas/modelEdits';
import type {
  DagEditOp,
  ReferenceCandidate,
  StepKey,
  ValidationIssue,
} from '../../../contracts';
import { issuesUnder } from '../../issues';
import { isPlainObject } from '../../paramsForm';
import { TextField } from '../../TextField';
import type { SectionProps } from '../../types';
import { EndpointField } from '../EndpointField';
import { EffortField } from '../endpointModel/EffortField';
import { EndpointModelField } from '../endpointModel/EndpointModelField';
import { useEndpointBlock } from '../endpointModel/useEndpointBlock';
import { SELECT_CLASS } from '../RunEditor';
import {
  AddButton,
  ChatField,
  errorIssue,
  makeSetField,
  ReferenceField,
  type SetField,
  YamlOnlyValue,
} from './fields';
import { LLM_PROVIDERS, takesBaseUrl, withOf } from './model';

export interface ChatSectionProps extends SectionProps {
  candidates: ReferenceCandidate[];
}

/** The protocols of the endpoints that a chat step accepts: the type of its provider comes from the endpoint. */
const CHAT_PROTOCOLS = [
  AIEndpointProtocol.anthropic,
  AIEndpointProtocol.openai,
] as const;

/** The value of the provider select that switches to a text field. */
const REFERENCE_CHOICE = '\u0000reference';

function isListed(provider: string): boolean {
  return (LLM_PROVIDERS as readonly string[]).includes(provider);
}

/**
 * The Provider select of a model: a name from `providers`, or a reference.
 * An agent step lists the CLI providers too, and words the empty choice and
 * the help for it.
 */
export function ProviderField({
  value,
  readOnly,
  candidates,
  issues,
  onSet,
  providers = LLM_PROVIDERS,
  help,
  emptyLabel,
  required,
}: {
  value: string;
  readOnly: boolean;
  candidates: ReferenceCandidate[];
  issues: ValidationIssue[];
  onSet(value: string | undefined): void;
  providers?: readonly string[];
  help?: ReactNode;
  emptyLabel?: string;
  /** The step needs a provider: the field is marked and tells when it is empty. */
  required?: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const [reference, setReference] = useState(false);
  const label = ts('Provider');
  const helpText =
    help ?? ts('Leave it empty to use the LLM settings of the DAG.');
  const listed = providers.includes(value);
  if (reference || (value !== '' && !listed)) {
    return (
      <ReferenceField
        label={label}
        help={ts('A name from the list, or a ${...} reference.')}
        value={value}
        candidates={candidates}
        readOnly={readOnly}
        issues={issues}
        path="with.provider"
        required={required}
        onCommit={onSet}
        action={
          <IconButton
            label={ts('Choose a provider from the list')}
            icon={List}
            disabled={readOnly}
            onClick={() => {
              setReference(false);
              if (value !== '' && !listed) onSet(undefined);
            }}
          />
        }
      />
    );
  }
  return (
    <ChatField
      label={label}
      htmlFor={id}
      help={helpText}
      issues={issues}
      path="with.provider"
      required={required}
      empty={value === ''}
    >
      <select
        id={id}
        className={SELECT_CLASS}
        value={value}
        disabled={readOnly}
        onChange={(event) => {
          if (event.target.value === REFERENCE_CHOICE) {
            setReference(true);
          } else {
            onSet(event.target.value || undefined);
          }
        }}
      >
        <option value="">{emptyLabel ?? ts('(default)')}</option>
        {providers.map((name) => (
          <option key={name} value={name}>
            {name}
          </option>
        ))}
        <option value={REFERENCE_CHOICE}>{ts('A ${...} reference')}</option>
      </select>
    </ChatField>
  );
}

/** The entry that "Add a model" appends: it takes the endpoint or the provider of the last entry. */
function freshRow(previous: ModelEditRow | undefined): Record<string, string> {
  if (previous?.endpoint) return { endpoint: previous.endpoint, name: '' };
  if (previous?.provider) return { provider: previous.provider, name: '' };
  return { provider: 'openai', name: '' };
}

/**
 * The list of fallback models of a chat step: one row for each entry, with
 * the edits of the Model node of the canvas.
 */
function ModelList({
  owner,
  edit,
  raw,
  readOnly,
  candidates,
  issues,
  fieldIssues,
  set,
  onEdit,
  dag,
}: {
  owner: StepKey;
  edit: ModelEditData;
  raw: unknown[];
  readOnly: boolean;
  candidates: ReferenceCandidate[];
  issues: ValidationIssue[];
  fieldIssues: Record<string, ValidationIssue[]>;
  set: SetField;
  onEdit(op: DagEditOp): void;
  dag: DagIdentity | undefined;
}): ReactElement {
  const { ts } = useI18n();
  const { choices, cliModelAliases } = useEndpointChoices(dag);
  const rows = edit.rows;
  const last = rows.length - 1;
  const apply = (ops: DagEditOp[]) => {
    for (const op of ops) onEdit(op);
  };
  // The issue of the endpoint of a row shows under that row, not under the list.
  const rowIssues = rows.map((_, index) =>
    issuesUnder(fieldIssues, `with.model[${index}].endpoint`)
  );
  const listIssues = issues.filter(
    (issue) => !rowIssues.some((own) => own.includes(issue))
  );
  const replace = (next: unknown[]) =>
    set(['with', 'model'], next.length === 0 ? undefined : next);
  const move = (from: number, to: number) => {
    const next = [...raw];
    [next[from], next[to]] = [next[to], next[from]];
    replace(next);
  };
  return (
    <ChatField
      label={ts('Models')}
      help={ts('The first model runs first. The next ones run when it fails.')}
      issues={listIssues}
      path="with.model"
    >
      <ol className="space-y-1">
        {rows.map((row, index) => {
          const known = isListed(row.provider) || row.provider === '';
          const name = row.name === '' ? `#${index + 1}` : row.name;
          return (
            <li
              key={index}
              data-testid="ve-model-row"
              className="flex flex-wrap items-start gap-1"
            >
              <div className="w-32 shrink-0 empty:hidden">
                <EndpointField
                  compact
                  label={ts('Endpoint of model {index}', { index: index + 1 })}
                  path={`with.model[${index}].endpoint`}
                  value={row.endpoint}
                  readOnly={readOnly}
                  dag={dag}
                  protocols={CHAT_PROTOCOLS}
                  issues={rowIssues[index] ?? []}
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
              {row.endpoint === '' && (
                <select
                  aria-label={ts('Provider of model {index}', {
                    index: index + 1,
                  })}
                  className={cn(SELECT_CLASS, 'w-32 shrink-0')}
                  value={row.provider}
                  disabled={readOnly}
                  onChange={(event) =>
                    set(
                      ['with', 'model', index, 'provider'],
                      event.target.value
                    )
                  }
                >
                  {!known && (
                    <option value={row.provider}>{row.provider}</option>
                  )}
                  {row.provider === '' && <option value="" />}
                  {LLM_PROVIDERS.map((provider) => (
                    <option key={provider} value={provider}>
                      {provider}
                    </option>
                  ))}
                </select>
              )}
              <div className="min-w-[8rem] flex-1">
                <ReferenceField
                  label={ts('Name of model {index}', { index: index + 1 })}
                  labelHidden
                  value={row.name}
                  candidates={candidates}
                  readOnly={readOnly}
                  onCommit={(text) => apply(modelEdits(owner, edit, row, text))}
                />
              </div>
              {!readOnly && (
                <>
                  <IconButton
                    size="md"
                    label={ts('Move {name} up', { name })}
                    icon={ArrowUp}
                    disabled={index === 0}
                    onClick={() => move(index, index - 1)}
                  />
                  <IconButton
                    size="md"
                    label={ts('Move {name} down', { name })}
                    icon={ArrowDown}
                    disabled={index === last}
                    onClick={() => move(index, index + 1)}
                  />
                  <IconButton
                    size="md"
                    tone="destructive"
                    label={ts('Remove model {name}', { name })}
                    icon={Trash2}
                    onClick={() =>
                      replace(raw.filter((_, position) => position !== index))
                    }
                  />
                </>
              )}
            </li>
          );
        })}
      </ol>
      <div className="pt-1">
        <AddButton
          label={ts('Add a model')}
          disabled={readOnly}
          onClick={() => replace([...raw, freshRow(rows[last])])}
        />
      </div>
    </ChatField>
  );
}

export interface ModelSectionProps extends ChatSectionProps {
  /** The DAG that holds the step, for the endpoints that it may use. */
  dag?: DagIdentity | undefined;
}

/**
 * Model of a chat step. The new method names an endpoint (Spec 094): the
 * block `with.endpoint` holds its `name`, its `model` and its `effort`, as for
 * an agent step. The first endpoint of a step of the old method converts it:
 * the old keys go. The old method has the provider, the model name and, for a
 * provider that the user runs, the base URL; the name of the variable that
 * holds the API key is under Advanced. A step in the block form shows the
 * fields of the old method only while the YAML holds them. A `model` written as
 * a list is a list of fallback models, one row each, and a row names its own
 * endpoint or provider.
 */
export function ModelSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  candidates,
  dag,
}: ModelSectionProps): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const withMap = withOf(step.value);
  const {
    endpoint,
    provider,
    model,
    base_url: baseUrl,
    api_key_name: keyName,
  } = withMap;
  const edit = readChatInfo(step.value).edit;
  const block = isPlainObject(endpoint);
  const isList = !block && Array.isArray(model);
  const { row, levels, setEndpoint, commitModel, setEffort } = useEndpointBlock(
    step.key,
    edit,
    dag,
    onEdit
  );
  // The single model of a step with no model key yet goes in the block too.
  const writesBlock = edit !== undefined && hasEffort(edit);
  const text = (value: unknown) => (typeof value === 'string' ? value : '');
  const issues = (key: string) => issuesUnder(fieldIssues, `with.${key}`);
  const nonText = (key: string, value: unknown) =>
    value !== undefined && typeof value !== 'string' ? (
      <YamlOnlyValue name={key} value={value} />
    ) : null;
  return (
    <div className="space-y-3">
      {endpoint !== undefined && !block ? (
        <YamlOnlyValue name="endpoint" value={endpoint} />
      ) : edit && row ? (
        <EndpointField
          value={row.endpoint}
          readOnly={readOnly}
          dag={dag}
          issues={[
            ...(fieldIssues['with.endpoint'] ?? []),
            ...issues('endpoint.name'),
          ]}
          protocols={CHAT_PROTOCOLS}
          required={block}
          requiredMessage={ts('chat.completion needs the name of an endpoint')}
          help={ts(
            'Optional. The endpoint gives the provider type and the key.'
          )}
          path="with.endpoint.name"
          onSet={setEndpoint}
        />
      ) : null}
      {(!isList || provider !== undefined) &&
        (!block || provider !== undefined) &&
        (isPlainObject(provider) || Array.isArray(provider) ? (
          nonText('provider', provider)
        ) : (
          <ProviderField
            value={text(provider)}
            readOnly={readOnly}
            candidates={candidates}
            issues={issues('provider')}
            onSet={(value) => set(['with', 'provider'], value)}
          />
        ))}
      {isList && edit ? (
        <ModelList
          owner={step.key}
          edit={edit}
          raw={model as unknown[]}
          readOnly={readOnly}
          candidates={candidates}
          issues={issues('model')}
          fieldIssues={fieldIssues}
          set={set}
          onEdit={onEdit}
          dag={dag}
        />
      ) : isList ? (
        <YamlOnlyValue name="model" value={model} />
      ) : edit && row ? (
        <EndpointModelField
          label={ts('Model')}
          help={ts('The name of the model. It can be a ${...} reference.')}
          value={row.name}
          endpoint={row.endpoint}
          dag={dag}
          candidates={candidates}
          readOnly={readOnly}
          issues={[
            ...issues(block ? 'endpoint.model' : 'model'),
            ...(block && row.endpoint !== '' && row.name === ''
              ? [
                  errorIssue(
                    ts(
                      'chat.completion needs a model; choose one that the endpoint lists'
                    )
                  ),
                ]
              : []),
          ]}
          path={writesBlock ? 'with.endpoint.model' : 'with.model'}
          required={block}
          onCommit={commitModel}
        />
      ) : (
        nonText('model', model)
      )}
      {edit && row && writesBlock && (
        <EffortField
          help={ts('Optional. Default uses the level of the model.')}
          value={edit.effort ?? ''}
          levels={levels}
          readOnly={readOnly || row.endpoint === ''}
          issues={issues('endpoint.effort')}
          onSet={setEffort}
        />
      )}
      {(takesBaseUrl(provider) || baseUrl !== undefined) &&
        (typeof baseUrl === 'string' || baseUrl === undefined ? (
          <ReferenceField
            label={ts('Base URL')}
            help={
              block
                ? ts(
                    'An endpoint holds its own base URL. Remove this key, or remove the endpoint.'
                  )
                : ts('The endpoint of the server. It replaces the default one.')
            }
            value={text(baseUrl)}
            candidates={candidates}
            readOnly={readOnly}
            issues={issues('base_url')}
            path="with.base_url"
            onCommit={(value) => set(['with', 'base_url'], value)}
          />
        ) : (
          nonText('base_url', baseUrl)
        ))}
      {(!block || keyName !== undefined) && (
        <details
          className="space-y-1"
          data-testid="ve-model-advanced"
          open={keyName !== undefined || issues('api_key_name').length > 0}
        >
          <summary className="cursor-pointer text-xs font-medium text-muted-foreground">
            <I18nText text="Advanced" />
          </summary>
          <div className="pt-1" data-field-path="with.api_key_name">
            {typeof keyName === 'string' || keyName === undefined ? (
              <TextField
                label={ts('API key variable')}
                help={
                  block
                    ? ts(
                        'An endpoint holds its own key. Remove this key, or remove the endpoint.'
                      )
                    : ts(
                        'The name of the environment variable that holds the API key.'
                      )
                }
                value={text(keyName)}
                readOnly={readOnly}
                issues={issues('api_key_name')}
                onCommit={(value) =>
                  set(
                    ['with', 'api_key_name'],
                    value === '' ? undefined : value
                  )
                }
              />
            ) : (
              nonText('api_key_name', keyName)
            )}
          </div>
        </details>
      )}
    </div>
  );
}
