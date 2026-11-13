// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement } from 'react';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { MCP_PATH } from '@/features/ai-endpoints/mcpServer';
import type { DagIdentity } from '@/features/ai-endpoints/useEndpointChoices';
import { useMcpServerChoices } from '@/features/ai-endpoints/useMcpServerChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ReferenceCandidate, ValidationIssue } from '../../../contracts';
import type { ToolEntryModel } from '../../../tools/model';
import { OpenTextLink } from '../../../tools/OpenLink';
import { dagLink } from '../../../tools/targets';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { Section } from '../../Section';
import { TextField } from '../../TextField';
import { KeyValueEditor } from '../../widgets';
import { ChatField, ReferenceField } from '../chat/fields';
import { SELECT_CLASS } from '../RunEditor';
import { StringList } from './fields';
import { ToolParams, type ChangeEntry } from './ToolParams';
import { useDagInputs } from './useToolSources';

export interface ToolEntrySectionsProps {
  entry: ToolEntryModel;
  /** The field path of the entry: `with.dags[0]`. */
  path: string;
  fieldIssues: Record<string, ValidationIssue[]>;
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  /** The DAG that holds the step: the MCP server entries that it may use depend on it. */
  dag?: DagIdentity | undefined;
  /** A key of the entry changes: `path` is relative to it, undefined removes. */
  onChange: ChangeEntry;
}

/** The issues of the entry itself and of the fields under `names`. */
function countOf(
  fieldIssues: Record<string, ValidationIssue[]>,
  path: string,
  names: string[],
  own = false
): number {
  return (
    (own ? (fieldIssues[path]?.length ?? 0) : 0) +
    names.reduce(
      (total, name) =>
        total + issuesUnder(fieldIssues, `${path}.${name}`).length,
      0
    )
  );
}

/**
 * What a DAG tool leads to: a link to the page of the DAG. The link opens a
 * new tab, so that the draft of the step stays.
 */
function EntryTarget({
  entry,
}: {
  entry: ToolEntryModel;
}): ReactElement | null {
  const page = entry.kind === 'dag' ? dagLink(entry.label) : null;
  if (!page) return null;
  return (
    <div data-testid="ve-tool-target">
      <OpenTextLink page={page}>
        <I18nText text="Open the workflow" />
      </OpenTextLink>
    </div>
  );
}

function NameField({
  entry,
  path,
  fieldIssues,
  readOnly,
  help,
  onChange,
}: Pick<
  ToolEntrySectionsProps,
  'entry' | 'path' | 'fieldIssues' | 'readOnly' | 'onChange'
> & { help?: string }): ReactElement {
  return (
    <div data-field-path={`${path}.name`}>
      <TextField
        label={<I18nText text="Name" />}
        value={entry.label}
        help={help}
        readOnly={readOnly}
        issues={fieldIssues[`${path}.name`]}
        onCommit={(text) => onChange(['name'], text)}
      />
    </div>
  );
}

/** The sections of a DAG tool: General and Parameters. */
function CalledToolSections({
  entry,
  path,
  fieldIssues,
  candidates,
  readOnly,
  onChange,
}: ToolEntrySectionsProps): ReactElement {
  const { ts } = useI18n();
  // A DAG says what it takes.
  const known = useDagInputs(entry.label, entry.kind === 'dag');
  return (
    <>
      <Section
        title={<I18nText text="General" />}
        count={countOf(fieldIssues, path, ['name'], true)}
      >
        <NameField
          entry={entry}
          path={path}
          fieldIssues={fieldIssues}
          readOnly={readOnly}
          help={ts('The DAG that the tool runs.')}
          onChange={onChange}
        />
        <EntryTarget entry={entry} />
      </Section>
      <Section
        title={<I18nText text="Parameters" />}
        count={countOf(fieldIssues, path, ['params'])}
      >
        <ToolParams
          params={entry.params}
          known={known}
          path={`${path}.params`}
          fieldIssues={fieldIssues}
          candidates={candidates}
          readOnly={readOnly}
          onChange={onChange}
        />
      </Section>
    </>
  );
}

function warning(message: string) {
  return {
    severity: ValidationIssueSeverity.warning,
    source: ValidationIssueSource.build,
    message,
  };
}

/**
 * The name of a MCP server entry of the AI page (Spec 095): a select of the
 * entries that the DAG may use. A name that the list does not hold stays a
 * value of the select, with a warning. With no list, the name is plain text.
 */
function ServerEntryField({
  entry,
  path,
  fieldIssues,
  readOnly,
  dag,
  onChange,
}: Pick<
  ToolEntrySectionsProps,
  'entry' | 'path' | 'fieldIssues' | 'readOnly' | 'dag' | 'onChange'
>): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const { choices, loaded } = useMcpServerChoices(dag);
  if (!loaded || choices.length === 0) {
    return (
      <NameField
        entry={entry}
        path={path}
        fieldIssues={fieldIssues}
        readOnly={readOnly}
        help={ts(
          'The name of a MCP server entry of the AI page. The tools of the server start with it.'
        )}
        onChange={onChange}
      />
    );
  }
  const known = choices.some((choice) => choice.name === entry.label);
  const issues = [...(fieldIssues[`${path}.name`] ?? [])];
  if (entry.label !== '' && !known) {
    issues.push(
      warning(
        ts(
          'The server does not know the MCP server "{name}", or this DAG may not use it. The step fails when it starts.',
          { name: entry.label }
        )
      )
    );
  }
  return (
    <ChatField
      label={ts('MCP server')}
      htmlFor={id}
      help={ts(
        'A MCP server entry of the AI page. The tools of the server start with its name.'
      )}
      issues={issues}
      path={`${path}.name`}
    >
      <select
        id={id}
        className={SELECT_CLASS}
        value={entry.label}
        disabled={readOnly}
        onChange={(event) => onChange(['name'], event.target.value)}
      >
        {entry.label === '' && <option value="">{ts('(none)')}</option>}
        {choices.map((choice) => (
          <option key={choice.name} value={choice.name}>
            {choice.name}
          </option>
        ))}
        {entry.label !== '' && !known && (
          <option value={entry.label}>{entry.label}</option>
        )}
      </select>
    </ChatField>
  );
}

/**
 * The sections of an MCP server: General, Connection and Allowed tools. An
 * entry with no url names a server entry of the AI page: it has no Connection
 * section, because the URL and the credentials stay in the coordinator.
 */
function McpSections({
  entry,
  path,
  fieldIssues,
  candidates,
  readOnly,
  dag,
  onChange,
}: ToolEntrySectionsProps): ReactElement {
  const { ts } = useI18n();
  const mcp = entry.mcp;
  const named = mcp?.named === true;
  return (
    <>
      <Section
        title={<I18nText text="General" />}
        count={countOf(fieldIssues, path, ['name'], true)}
      >
        {named ? (
          <>
            <ServerEntryField
              entry={entry}
              path={path}
              fieldIssues={fieldIssues}
              readOnly={readOnly}
              dag={dag}
              onChange={onChange}
            />
            <p className="text-[11px] text-muted-foreground">
              <I18nText text="The coordinator calls this server with the credentials of the entry. The step never sees them." />{' '}
              <OpenTextLink page={{ name: 'MCP', path: MCP_PATH }}>
                <I18nText text="Open the MCP servers" />
              </OpenTextLink>
            </p>
          </>
        ) : (
          <NameField
            entry={entry}
            path={path}
            fieldIssues={fieldIssues}
            readOnly={readOnly}
            help={ts(
              'The label of the server. It starts the tool names that the model sees.'
            )}
            onChange={onChange}
          />
        )}
      </Section>
      {!named && (
        <Section
          title={<I18nText text="Connection" />}
          count={countOf(fieldIssues, path, ['url', 'headers'])}
        >
          <ReferenceField
            label={ts('Server URL')}
            value={mcp?.url ?? ''}
            candidates={candidates}
            readOnly={readOnly}
            path={`${path}.url`}
            issues={fieldIssues[`${path}.url`]}
            onCommit={(text) => onChange(['url'], text ?? '')}
          />
          <div className="space-y-0.5" data-field-path={`${path}.headers`}>
            <div className="text-xs font-medium text-muted-foreground">
              <I18nText text="Headers" />
            </div>
            <KeyValueEditor
              label={ts('Headers')}
              value={mcp?.headers ?? {}}
              candidates={candidates}
              readOnly={readOnly}
              onChange={(next) =>
                onChange(
                  ['headers'],
                  Object.keys(next).length > 0 ? next : undefined
                )
              }
            />
            <FieldIssues issues={issuesUnder(fieldIssues, `${path}.headers`)} />
          </div>
        </Section>
      )}
      <Section
        title={<I18nText text="Allowed tools" />}
        count={countOf(fieldIssues, path, ['allow'])}
      >
        <div data-field-path={`${path}.allow`}>
          <StringList
            label={<I18nText text="Allowed server tools" />}
            values={mcp?.allow ?? []}
            readOnly={readOnly}
            onChange={(next) => onChange(['allow'], next)}
          />
          <p className="text-[11px] text-muted-foreground">
            <I18nText text="Only the tools in this list join the tools of the model. The list cannot be empty." />
          </p>
          <FieldIssues issues={issuesUnder(fieldIssues, `${path}.allow`)} />
        </div>
      </Section>
    </>
  );
}

/**
 * The sections of one tool entry, by kind. A DAG tool has General and
 * Parameters. An MCP server has General, Connection and Allowed tools. An entry that is not a mapping has no section: the panel shows the
 * reason. Each change goes out through `onChange` with a path relative to the
 * entry.
 */
export function ToolEntrySections(props: ToolEntrySectionsProps): ReactElement {
  switch (props.entry.kind) {
    case 'dag':
      return <CalledToolSections {...props} />;
    case 'mcp':
      return <McpSections {...props} />;
    case 'invalid':
      return (
        <p className="border-b border-border px-3 py-2 text-xs text-destructive">
          {props.entry.problem}
        </p>
      );
  }
}
