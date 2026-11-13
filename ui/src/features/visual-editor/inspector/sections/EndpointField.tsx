// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement, type ReactNode } from 'react';
import {
  AIEndpointProtocol,
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { ENDPOINTS_PATH } from '@/features/ai-endpoints/endpoint';
import { Picker, type PickerItem } from '@/features/ai-endpoints/Picker';
import {
  useEndpointChoices,
  type DagIdentity,
} from '@/features/ai-endpoints/useEndpointChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ValidationIssue } from '../../contracts';
import {
  AGENT_NEEDS_ENDPOINT_MESSAGE,
  missingEndpointMessage,
  missingEndpointName,
} from '../../doc/issueMessages';
import { OpenTextLink } from '../../tools/OpenLink';
import { plainIssueMessage } from '../issues';
import { ChatField } from './chat/fields';

function warning(message: string): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.warning,
    source: ValidationIssueSource.build,
    message,
  };
}

function failure(message: string): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message,
  };
}

/** The protocols that the CLI claude of an agent step accepts. */
const CLAUDE_PROTOCOLS: readonly AIEndpointProtocol[] = [
  AIEndpointProtocol.anthropic,
];

/**
 * The picker of the endpoints that a step may name. The first item, "(none)",
 * is the old method. A name that the YAML holds and the list lacks stays an
 * item of the list. The form and the Model node of the canvas use it.
 */
export function EndpointSelect({
  id,
  value,
  readOnly,
  choices,
  onSet,
  className,
  label,
  emptyLabel,
  title,
  marker,
  required = false,
}: {
  id?: string;
  value: string;
  readOnly: boolean;
  /** The endpoints to offer: the ones of the protocols that the step accepts. */
  choices: readonly { name: string }[];
  onSet(value: string | undefined): void;
  className?: string;
  /** The accessible name, when no label points to the picker. */
  label?: string;
  /** The text of the closed picker while no endpoint is set. Default: "(none)". */
  emptyLabel?: string;
  /** A tooltip for the picker. */
  title?: string;
  /** A mark inside the picker, such as the warning sign of a problem. */
  marker?: ReactNode;
  /** True for a step that needs an endpoint: the list has no "(none)" item. */
  required?: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const items: PickerItem[] = [
    ...(required ? [] : [{ value: '', label: emptyLabel ?? ts('(none)') }]),
    ...choices.map((choice) => ({ value: choice.name, label: choice.name })),
  ];
  if (value !== '' && !choices.some((choice) => choice.name === value)) {
    items.push({ value, label: value });
  }
  return (
    <Picker
      id={id}
      label={label}
      title={title}
      marker={marker}
      className={className}
      disabled={readOnly}
      items={items}
      value={value}
      searchLabel={ts('Search the endpoints')}
      emptyText={ts('No endpoint matches.')}
      onSelect={(next) => onSet(next || undefined)}
    >
      <span className={cn('truncate', value === '' && 'text-muted-foreground')}>
        {value === '' ? (emptyLabel ?? ts('(none)')) : value}
      </span>
    </Picker>
  );
}

/**
 * The Endpoint select of a step: the endpoints that the DAG may use (Spec
 * 093), of the protocols that the step accepts. An agent step needs the
 * protocol anthropic, for the CLI claude. A chat step accepts every protocol,
 * because the type of its provider comes from the endpoint. A name that the
 * YAML holds and the list does not hold stays a value of the select, with a
 * warning. With no endpoint to choose, a muted line points to the AI page. The
 * key never shows here.
 *
 * `compact` is the form of a row of a model list: the label is only the
 * accessible name, there is no help line, and with no endpoint to choose the
 * field is not drawn.
 */
export function EndpointField({
  value,
  readOnly,
  dag,
  issues,
  onSet,
  protocols = CLAUDE_PROTOCOLS,
  help,
  label,
  compact = false,
  required = false,
  requiredMessage,
  path = 'with.endpoint',
}: {
  value: string;
  readOnly: boolean;
  dag: DagIdentity | undefined;
  issues: ValidationIssue[];
  onSet(value: string | undefined): void;
  protocols?: readonly AIEndpointProtocol[];
  help?: string;
  label?: string;
  compact?: boolean;
  /** True for a step that needs an endpoint (agent.run): an empty value is an error. */
  required?: boolean;
  /** The error of a required field with no value. Default: the one of a step with no endpoint. */
  requiredMessage?: string | undefined;
  path?: string;
}): ReactElement | null {
  const { ts } = useI18n();
  const id = useId();
  const { choices, loaded } = useEndpointChoices(dag);
  const usable = choices.filter((choice) =>
    protocols.includes(choice.protocol)
  );
  const known = choices.find((choice) => choice.name === value);
  // Warn only when the list has arrived: before that nothing is known.
  const problems = [...issues];
  // The issues of the file say it already when the YAML editor has found it.
  const said = (test: (message: string) => boolean) =>
    issues.some((issue) => test(plainIssueMessage(issue.message)));
  if (
    required &&
    value === '' &&
    !said(
      (message) =>
        message === AGENT_NEEDS_ENDPOINT_MESSAGE ||
        message === ts(AGENT_NEEDS_ENDPOINT_MESSAGE)
    )
  ) {
    problems.push(failure(requiredMessage ?? ts(AGENT_NEEDS_ENDPOINT_MESSAGE)));
  }
  if (loaded && value !== '') {
    if (
      !known &&
      said(
        (message) =>
          missingEndpointName(message) === value ||
          message === ts(missingEndpointMessage('{name}'), { name: value })
      )
    ) {
      // Said by an issue of the file.
    } else if (!known) {
      problems.push(
        warning(
          ts(
            'The server does not know the endpoint "{name}", or this DAG may not use it. The step fails when it starts.',
            { name: value }
          )
        )
      );
    } else if (!protocols.includes(known.protocol)) {
      problems.push(
        warning(
          ts(
            'The endpoint "{name}" does not have the protocol anthropic, which the CLI claude needs. The step fails when it starts.',
            { name: value }
          )
        )
      );
    }
  }
  const noChoice = loaded && usable.length === 0 && value === '';
  if (compact && noChoice) return null;
  const text = label ?? ts('Endpoint');
  return (
    <ChatField
      label={compact ? <span className="sr-only">{text}</span> : text}
      htmlFor={id}
      help={
        compact || noChoice
          ? undefined
          : (help ??
            (required
              ? ts('Required. The step uses the key of the endpoint.')
              : ts('Optional. The step uses the key of the endpoint.')))
      }
      issues={problems}
      path={path}
    >
      {noChoice ? (
        <p
          data-testid="ve-endpoint-empty"
          className="text-xs text-muted-foreground"
        >
          {ts('No endpoint is available for this DAG.')}{' '}
          <OpenTextLink page={{ name: 'AI', path: ENDPOINTS_PATH }}>
            {ts('Add one on the AI page')}
          </OpenTextLink>
        </p>
      ) : (
        <EndpointSelect
          id={id}
          value={value}
          readOnly={readOnly}
          choices={usable}
          required={required}
          emptyLabel={required ? ts('Select an endpoint') : undefined}
          onSet={onSet}
        />
      )}
    </ChatField>
  );
}
