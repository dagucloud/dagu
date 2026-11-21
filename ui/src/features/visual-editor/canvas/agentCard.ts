// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  repeatInterval,
  type RepeatPolicyView,
} from '@/components/ui/repeat-summary';
import type { TrustSource } from '../agentRun/agentRun';
import type { CanvasNode, ContinueOnView } from '../contracts';
import { continueLabel } from './conditions';
import { isRecord, type Rec } from '../record';

/** Translates an English source text, with `{name}` values. */
export type Translate = (
  text: string,
  values?: Record<string, string | number>
) => string;

/** A number, or the text of a `${...}` reference: the two things a limit can be. */
function numberOrReference(value: unknown): number | string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  return typeof value === 'string' && value.includes('${')
    ? value.trim()
    : undefined;
}

function firstLine(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  return value
    .split('\n')
    .map((line) => line.trim())
    .find((line) => line !== '');
}

/** The retry policy of a step, as the card shows it. */
export interface RetryFacts {
  limit?: number | string;
  intervalSec?: number | string;
  exitCodes?: number[];
}

/**
 * What the card of an `agent.run` step shows besides its name and its model:
 * the keys that decide how the step ends, and where its trust comes from.
 * They are keys of the step, not of `with`, except the prompt lines.
 */
export interface AgentFacts {
  retry?: RetryFacts;
  /** The names in `secrets`. */
  secrets?: string[];
  timeoutSec?: number | string;
  /** The property names of `output_schema`. Set when the step has a schema. */
  schema?: string[];
  /** First line of `with.prompt` and of `with.system`. */
  promptLine?: string;
  systemLine?: string;
  /** Where the effective value of `untrusted` comes from. */
  trust: TrustSource;
  /** Rows of chips under the header: the layout reserves their room. */
  chipRows: number;
}

/** Reads the `retry_policy` of a step. Undefined when the step has none that says anything. */
export function readRetry(value: unknown): RetryFacts | undefined {
  if (!isRecord(value)) return undefined;
  const retry: RetryFacts = {};
  const limit = numberOrReference(value.limit);
  if (limit !== undefined) retry.limit = limit;
  const interval = numberOrReference(value.interval_sec);
  if (interval !== undefined) retry.intervalSec = interval;
  const raw = value.exit_code;
  const codes = (Array.isArray(raw) ? raw : [raw]).filter(
    (code): code is number => typeof code === 'number'
  );
  if (codes.length > 0) retry.exitCodes = codes;
  return Object.keys(retry).length === 0 ? undefined : retry;
}

function readSecretNames(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const names = value.flatMap((item) => {
    if (typeof item === 'string') return [item];
    return isRecord(item) && typeof item.name === 'string' ? [item.name] : [];
  });
  return names.length > 0 ? names : undefined;
}

function readSchemaNames(value: unknown): string[] | undefined {
  if (!isRecord(value)) return undefined;
  return isRecord(value.properties) ? Object.keys(value.properties) : [];
}

/**
 * Reads the facts of an `agent.run` step from the plain value of the step
 * entry. It never throws; a key of the wrong type is left out. `chipRows` is
 * not set: `withChipRows` sets it once the node is whole.
 */
export function readAgentFacts(
  step: Rec,
  trust: TrustSource
): Omit<AgentFacts, 'chipRows'> {
  const facts: Omit<AgentFacts, 'chipRows'> = { trust };
  const retry = readRetry(step.retry_policy);
  if (retry) facts.retry = retry;
  const secrets = readSecretNames(step.secrets);
  if (secrets) facts.secrets = secrets;
  const timeout = numberOrReference(step.timeout_sec);
  if (timeout !== undefined) facts.timeoutSec = timeout;
  const schema = readSchemaNames(step.output_schema);
  if (schema) facts.schema = schema;
  const block = isRecord(step.with) ? step.with : {};
  const prompt = firstLine(block.prompt);
  if (prompt) facts.promptLine = prompt;
  const system = firstLine(block.system);
  if (system) facts.systemLine = system;
  return facts;
}

/** A duration in seconds as the UI reads it: "5m"; a reference stays as written. */
function seconds(value: number | string): string {
  return typeof value === 'number' ? repeatInterval(value) : value;
}

/** The chips of an agent card, in this order. */
export type ChipKind =
  | 'untrusted'
  | 'retry'
  | 'timeout'
  | 'secrets'
  | 'schema'
  | 'repeat'
  | 'conditional'
  | 'continue';

export interface ChipSpec {
  kind: ChipKind;
  /** The text of the chip. */
  label: string;
}

type ChipNode = Pick<CanvasNode, 'untrusted' | 'preconditions'> & {
  continueOn?: ContinueOnView;
  repeat?: RepeatPolicyView;
  agent?: Omit<AgentFacts, 'chipRows'>;
};

/** The text of the retry chip: `retry ×12 · 5m · exit 75`, or `retry 3/12` in a run. */
export function retryLabel(
  ts: Translate,
  retry: RetryFacts,
  retried = 0
): string {
  const limit = retry.limit;
  const head =
    retried > 0 && limit !== undefined
      ? ts('retry {used}/{limit}', { used: retried, limit })
      : limit !== undefined
        ? ts('retry ×{limit}', { limit })
        : ts('retry');
  const parts = [head];
  if (retry.intervalSec !== undefined) parts.push(seconds(retry.intervalSec));
  if (retry.exitCodes) {
    parts.push(ts('exit {codes}', { codes: retry.exitCodes.join(', ') }));
  }
  return parts.join(' · ');
}

/** The tooltip of the retry chip: the whole policy as a sentence. */
export function retryTitle(ts: Translate, retry: RetryFacts): string {
  return [
    retry.limit !== undefined
      ? ts('Retries the step up to {limit} times.', { limit: retry.limit })
      : ts('Retries the step.'),
    retry.intervalSec !== undefined
      ? ts('Waits {interval} between the attempts.', {
          interval: seconds(retry.intervalSec),
        })
      : '',
    retry.exitCodes
      ? ts('Retries only after the exit code {codes}.', {
          codes: retry.exitCodes.join(', '),
        })
      : '',
  ]
    .filter(Boolean)
    .join('\n');
}

/** The text of the timeout chip: `timeout 30m`. */
export function timeoutLabel(ts: Translate, value: number | string): string {
  return ts('timeout {time}', { time: seconds(value) });
}

/** The text of the secrets chip: `2 secrets`. */
export function secretsLabel(ts: Translate, names: string[]): string {
  return ts(names.length === 1 ? '{count} secret' : '{count} secrets', {
    count: names.length,
  });
}

/** The text of the schema chip. */
export function schemaLabel(ts: Translate): string {
  return ts('output schema');
}

/**
 * The chips that an agent card shows, in order: the effective trust, the
 * retry policy, the timeout, the secrets, the output schema, the repeat
 * policy, `preconditions` and `continue_on`. `ts` gives the text: the layout
 * passes the English one to count the rows.
 */
export function agentChips(
  ts: Translate,
  node: ChipNode,
  retried = 0
): ChipSpec[] {
  const chips: ChipSpec[] = [];
  const agent = node.agent;
  if (node.untrusted) chips.push({ kind: 'untrusted', label: ts('Untrusted') });
  if (agent?.retry) {
    chips.push({ kind: 'retry', label: retryLabel(ts, agent.retry, retried) });
  }
  if (agent?.timeoutSec !== undefined) {
    chips.push({
      kind: 'timeout',
      label: timeoutLabel(ts, agent.timeoutSec),
    });
  }
  if (agent?.secrets) {
    chips.push({ kind: 'secrets', label: secretsLabel(ts, agent.secrets) });
  }
  if (agent?.schema) chips.push({ kind: 'schema', label: schemaLabel(ts) });
  if (node.repeat) {
    // The repeat badge draws its own text: this one sets the width to reserve.
    chips.push({ kind: 'repeat', label: 'every 10s' });
  }
  if (node.preconditions) {
    chips.push({ kind: 'conditional', label: ts('Conditional') });
  }
  if (node.continueOn) {
    chips.push({
      kind: 'continue',
      label: continueLabel(ts, node.continueOn),
    });
  }
  return chips;
}

// Width of the chips, in px, for the count of rows. A chip has its padding,
// the gap that follows it, an icon, and its text at 10 px. The estimate is a
// little wide, so that a chip never wraps to a row that the card has no room
// for.
const CHIP_PAD = 10;
const CHIP_GAP = 4;
const CHIP_ICON = 14;
const CHAR_WIDTH = 5.1;
const WIDE_CHAR_WIDTH = 11;

function textWidth(text: string): number {
  let width = 0;
  for (const char of text) {
    width += char.charCodeAt(0) > 0x2e80 ? WIDE_CHAR_WIDTH : CHAR_WIDTH;
  }
  return width;
}

/** English source text with its `{name}` values put in. */
const english: Translate = (text, values = {}) =>
  text.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name] ?? ''));

/** Rows that the chips need in a card whose chips have `width` px. */
export function chipRows(node: ChipNode, width: number, retried = 0): number {
  const chips = agentChips(english, node, retried);
  // The label of `continue` depends on the keys of `continue_on`.
  const widths = chips.map(
    (chip) => CHIP_PAD + CHIP_ICON + textWidth(chip.label) + CHIP_GAP
  );
  let rows = widths.length === 0 ? 0 : 1;
  let used = 0;
  for (const chipWidth of widths) {
    if (used > 0 && used + chipWidth > width) {
      rows++;
      used = 0;
    }
    used += chipWidth;
  }
  return rows;
}

/**
 * The lines of the tooltip of an agent card: the first line of the system
 * message and of the prompt, and, when the card has no chips (`chips` is
 * false: a compact card), what the keys of the step say.
 */
export function agentFactLines(
  ts: Translate,
  node: Pick<ChipNode, 'agent'>,
  chips: boolean
): string[] {
  const agent = node.agent;
  if (!agent) return [];
  const lines: string[] = [];
  if (agent.systemLine) {
    lines.push(ts('System: {line}', { line: agent.systemLine }));
  }
  if (agent.promptLine) {
    lines.push(ts('Prompt: {line}', { line: agent.promptLine }));
  }
  if (chips) return lines;
  if (agent.retry) {
    lines.push(retryLabel(ts, agent.retry));
  }
  if (agent.timeoutSec !== undefined) {
    lines.push(timeoutLabel(ts, agent.timeoutSec));
  }
  if (agent.secrets) {
    lines.push(ts('Secrets: {names}', { names: agent.secrets.join(', ') }));
  }
  if (agent.schema) {
    lines.push(
      agent.schema.length > 0
        ? ts('Output schema: {names}', { names: agent.schema.join(', ') })
        : schemaLabel(ts)
    );
  }
  return lines;
}

/**
 * The number of facts that a compact agent card does not draw as a mark of
 * its own: the retry policy, the timeout, the secrets and the output schema.
 * The card shows it as a counter.
 */
export function hiddenFactCount(node: Pick<ChipNode, 'agent'>): number {
  const agent = node.agent;
  if (!agent) return 0;
  return [agent.retry, agent.timeoutSec, agent.secrets, agent.schema].filter(
    (fact) => fact !== undefined
  ).length;
}

/** Width of the card of an agent step. */
export const AGENT_CARD_WIDTH = 360;
/** Room at each side of the chips and the rows of the card. */
export const AGENT_CARD_PAD = 12;
/**
 * Heights of the parts of the card, in px. The header is as tall as the icon
 * of the step (48 px): the name and the subtitle are two lines in a block
 * beside it, centred on it.
 */
const HEAD = 48;
const CHIP_ROW = 20;
const RUN_ROW = 22;
const GAP = 6;
const PAD_Y = 10;

/** Width that the chips of the card have. */
export const AGENT_CHIPS_WIDTH = AGENT_CARD_WIDTH - 2 * AGENT_CARD_PAD;

/**
 * Height of the card of an agent step: the header (the icon, with the name
 * and the subtitle beside it), a row for each row of chips, and, when a run
 * is shown, the row with its result.
 */
export function agentCardHeight(rows: number, withRun: boolean): number {
  return (
    2 * PAD_Y +
    HEAD +
    (rows > 0 ? GAP + rows * CHIP_ROW : 0) +
    (withRun ? GAP + RUN_ROW : 0)
  );
}

/** Sizes the layout and the card read. */
export const AGENT_CARD_PARTS = { HEAD, CHIP_ROW, RUN_ROW, GAP, PAD_Y };
