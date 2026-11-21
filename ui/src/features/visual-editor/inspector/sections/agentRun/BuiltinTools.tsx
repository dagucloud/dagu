// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement } from 'react';
import { Switch } from '@/components/ui/switch';
import { useI18n } from '@/i18n/I18nProvider';
import { builtinLabel } from '../../../tools/builtin';
import { BUILTIN_TOOLS, type BuiltinToolName } from '../../../tools/model';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { isPlainObject } from '../../paramsForm';
import type { SectionProps } from '../../types';
import { makeSetField, YamlOnlyValue } from '../chat/fields';
import { withOf } from '../chat/model';
import { StringList } from '../tools/fields';

const BUILTIN_PATH = ['with', 'tools', 'builtin'] as const;

/** The value of `with.tools.builtin` of a step, undefined when the step has none. */
export function builtinOf(value: Record<string, unknown>): unknown {
  const tools = isPlainObject(value.with) ? value.with.tools : undefined;
  return isPlainObject(tools) ? tools.builtin : undefined;
}

/**
 * The entries of `with.tools` that no list of the Tools section edits: not
 * `builtin`, and not the list `mcp`, which the tool list above this editor
 * shows. `dags` is one of them: the DAG tools are in `with.dags`.
 */
function otherToolKeys(value: Record<string, unknown>): [string, unknown][] {
  const tools = isPlainObject(value.with) ? value.with.tools : undefined;
  return isPlainObject(tools)
    ? Object.entries(tools).filter(
        ([key]) => key !== 'builtin' && key !== 'mcp'
      )
    : [];
}

/** The items of a list of command patterns; null when it is not a list of text. */
function patternsOf(value: unknown): string[] | null {
  if (value === undefined) return [];
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
    ? (value as string[])
    : null;
}

function ToolSwitch({
  name,
  value,
  readOnly,
  onSet,
}: {
  name: Exclude<BuiltinToolName, 'shell'>;
  value: unknown;
  readOnly: boolean;
  onSet(value: true | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  if (value !== undefined && value !== true) {
    return <YamlOnlyValue name={`tools.builtin.${name}`} value={value} />;
  }
  return (
    <div
      className="flex items-center justify-between gap-2"
      data-field-path={`with.tools.builtin.${name}`}
    >
      <label htmlFor={id} className="text-xs">
        {builtinLabel(ts, name)}
      </label>
      <Switch
        id={id}
        checked={value === true}
        disabled={readOnly}
        // Off removes the key: the value of a built-in tool is `true`.
        onCheckedChange={(checked) => onSet(checked ? true : undefined)}
      />
    </div>
  );
}

/**
 * The built-in tools of an agent step (`with.tools.builtin`, Spec 091): a
 * switch for each of `read`, `write`, `edit` and `search`, and the command
 * patterns of `shell`, allowed and denied. The right panel of the built-in
 * tools shows the same editor, with no heading. Every change is a
 * `setStepField` on the key. A key of `with.tools` besides `builtin` and the
 * list `mcp` is unknown: it shows read-only, with the text of its validation
 * issue.
 */
export function BuiltinTools({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  heading = true,
}: SectionProps & {
  /** False when the host names the tools above: the tool panel has its own title. */
  heading?: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const tools = withOf(step.value).tools;
  const builtin = builtinOf(step.value);
  const others = otherToolKeys(step.value);
  if (tools !== undefined && !isPlainObject(tools)) {
    return (
      <div data-field-path="with.tools">
        <YamlOnlyValue name="tools" value={tools} />
        <FieldIssues issues={issuesUnder(fieldIssues, 'with.tools')} />
      </div>
    );
  }
  return (
    <div className="space-y-2" data-field-path="with.tools">
      <div
        className="space-y-2"
        data-field-path="with.tools.builtin"
        data-testid="ve-builtin-tools"
      >
        {heading && (
          <div className="text-xs font-medium text-muted-foreground">
            {ts('Built-in tools')}
          </div>
        )}
        {builtin === undefined || isPlainObject(builtin) ? (
          <BuiltinBody
            builtin={builtin ?? {}}
            readOnly={readOnly}
            set={(tail, value) => set([...BUILTIN_PATH, ...tail], value)}
          />
        ) : (
          <YamlOnlyValue name="tools.builtin" value={builtin} />
        )}
        <FieldIssues
          issues={issuesUnder(fieldIssues, BUILTIN_PATH.join('.'))}
        />
      </div>
      {others.map(([key, value]) => (
        <div key={key} data-field-path={`with.tools.${key}`}>
          <YamlOnlyValue name={`tools.${key}`} value={value} />
          <FieldIssues issues={issuesUnder(fieldIssues, `with.tools.${key}`)} />
        </div>
      ))}
      <FieldIssues issues={fieldIssues['with.tools']} />
    </div>
  );
}

function BuiltinBody({
  builtin,
  readOnly,
  set,
}: {
  builtin: Record<string, unknown>;
  readOnly: boolean;
  set(tail: string[], value: unknown): void;
}): ReactElement {
  const { ts } = useI18n();
  const shell = builtin.shell;
  const shellMap = isPlainObject(shell) ? shell : null;
  const allow = patternsOf(shellMap?.allow);
  const deny = patternsOf(shellMap?.deny);
  const listOrNull = (next: string[]) => (next.length > 0 ? next : undefined);
  return (
    <>
      {BUILTIN_TOOLS.filter((name) => name !== 'shell').map((name) => (
        <ToolSwitch
          key={name}
          name={name as Exclude<BuiltinToolName, 'shell'>}
          value={builtin[name]}
          readOnly={readOnly}
          onSet={(value) => set([name], value)}
        />
      ))}
      <div className="space-y-1" data-field-path="with.tools.builtin.shell">
        <div className="text-xs">{builtinLabel(ts, 'shell')}</div>
        {shell !== undefined && shellMap === null ? (
          <YamlOnlyValue name="tools.builtin.shell" value={shell} />
        ) : (
          <>
            {allow === null ? (
              <YamlOnlyValue
                name="tools.builtin.shell.allow"
                value={shellMap?.allow}
              />
            ) : (
              <StringList
                label={ts('Allowed commands')}
                values={allow}
                readOnly={readOnly}
                onChange={(next) => set(['shell', 'allow'], listOrNull(next))}
              />
            )}
            {deny === null ? (
              <YamlOnlyValue
                name="tools.builtin.shell.deny"
                value={shellMap?.deny}
              />
            ) : (
              <StringList
                label={ts('Denied commands')}
                values={deny}
                readOnly={readOnly}
                onChange={(next) => set(['shell', 'deny'], listOrNull(next))}
              />
            )}
            <p className="text-[11px] text-muted-foreground">
              {ts(
                'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.'
              )}
            </p>
          </>
        )}
      </div>
    </>
  );
}
