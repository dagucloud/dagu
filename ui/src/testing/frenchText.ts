// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The rules that every text of the French catalogs follows, for the tests
 * that check those catalogs.
 *
 * @module testing/frenchText
 */

/** English texts that French writes the same way. */
export const SAME_IN_FRENCH: ReadonlySet<string> = new Set([
  '(Parent)',
  '(sha256=<hex>)',
  'Action',
  'Actions',
  'Administration',
  'Agent',
  'Attribution',
  'Audit',
  'Auto',
  'Base',
  'Bastion',
  'Cc',
  'Chat',
  'Cockpit',
  'Community',
  'Condition',
  'Conditions',
  'Configuration',
  'Cron',
  'Ctrl',
  'D',
  'DAG',
  'DAGU-XXXX-XXXX-XXXX-XXXX',
  'Dagu v',
  'Date',
  'Dates',
  'Description',
  'Document',
  'Documentation',
  'F',
  'Format',
  'Global',
  'Grep',
  'Harness',
  'Header-Name: value',
  'ID',
  'Incidents',
  'Infrastructure',
  'LOG_LEVEL',
  'MCP',
  'Message',
  'Microsoft 365',
  'Node.js',
  'Notification',
  'Notifications',
  'OAuth 2.0',
  'OK',
  'Options',
  'POST',
  'Page',
  'Page 1',
  'Page {page}',
  'Pages',
  'Port',
  'Prompt',
  'Public',
  'REST',
  'Routes',
  'Script',
  'Secret',
  'Secrets',
  'Service',
  'Services',
  'Session',
  'Shell',
  'Shell (OpenSSL)',
  'Source',
  'Strict',
  'Terminal',
  'Type',
  'URL',
  'Variable',
  'Variables',
  'Version',
  'Webhook',
  'Webhooks',
  'Wiki',
  'Worker',
  'Workers',
  'Workflow',
  'Workflow > {tab}',
  'Workflows',
  'YAML',
  'Zoom',
  'community',
  'console.dagu.sh',
  'exit:[{codes}]',
  'false',
  'https://dagu.example.com:8080/api/v1',
  'local',
  'option',
  'pagination',
  'stderr',
  'stdout',
  'step-a, step-b',
  'true',
  'via {calendar}',
  'wiki',
  'worker',
  'workers',
  'workflow',
  '{count} option',
  '{count} options',
  '{count} route',
  '{count} workflow',
  '{count} {type}',
  '{detail}, {live}',
  '{state}, {history}',
]);

/**
 * English texts whose French keeps the English punctuation: code that the
 * text shows, or one half of a quote whose other half the call site writes.
 * The punctuation rules skip them.
 */
export const KEEPS_ENGLISH_PUNCTUATION: ReadonlySet<string> = new Set([
  'Header-Name: value',
]);

/** Words that every locale writes as in English. */
const TECHNICAL =
  /\b(?:DAG|YAML|JSON|ID|URL|SSE|stdout|stderr|cron|HTTP|SQL|ZIP|PNG|SVG)\b/g;
const LETTER = /\p{L}/u;
const PLACEHOLDER = /\{(\w+)\}/g;
// A colon, semicolon, exclamation or question mark that ends a phrase, with
// the character before it. The colon of a time (12:30) or of a URL is
// followed by no space, so it is not one.
const HIGH_PUNCTUATION = /(.?)([:;!?])(?=\s|$)/gu;
const NO_BREAK_SPACES = ['\u00a0', '\u202f'];

// Placeholders such as {count} are not text that a reader sees.
export function onlyTechnical(text: string): boolean {
  return !LETTER.test(text.replace(PLACEHOLDER, ' ').replace(TECHNICAL, ' '));
}

function placeholders(text: string): string[] {
  const names = [...text.matchAll(PLACEHOLDER)].map((match) => match[1] ?? '');
  return [...new Set(names)].sort();
}

// A call site may join the text to other text, so the French text keeps the
// spaces at the ends of the English text. Past them, it may start with a
// no-break space only before high punctuation or a closing guillemet, and
// end with one only after an opening guillemet.
function keepsEnds(value: string, english: string): boolean {
  const lead = /^ */.exec(english)?.[0] ?? '';
  const tail = / *$/.exec(english)?.[0] ?? '';
  if (
    !value.startsWith(lead) ||
    !value.endsWith(tail) ||
    value.length < lead.length + tail.length
  ) {
    return false;
  }
  const core = value.slice(lead.length, value.length - tail.length);
  if (/^\s/u.test(core) && !/^[\u00a0\u202f][:;!?»]/u.test(core)) {
    return false;
  }
  return !/\s$/u.test(core) || /«[\u00a0\u202f]$/u.test(core);
}

function findPunctuationProblems(
  id: string,
  key: string,
  value: string
): string[] {
  const problems: string[] = [];
  for (const [, before, mark] of value.matchAll(HIGH_PUNCTUATION)) {
    if (!NO_BREAK_SPACES.includes(before ?? '')) {
      problems.push(`${id}: no no-break space before "${mark}" in "${key}"`);
    }
  }
  if (/«(?![\u00a0\u202f])|(?<![\u00a0\u202f])»/u.test(value)) {
    problems.push(`${id}: no no-break space inside « » in "${key}"`);
  }
  if (value.includes("'")) {
    problems.push(`${id}: straight apostrophe in "${key}"; write ’`);
  }
  if (/["“”]/u.test(value)) {
    problems.push(`${id}: English quotes in "${key}"; write « »`);
  }
  if (value.includes('...')) {
    problems.push(`${id}: three dots in "${key}"; write …`);
  }
  return problems;
}

/**
 * Returns one line for each rule that the French text of a key breaks; an
 * empty list means a valid text. id names the catalog in the lines.
 */
export function findFrenchTextProblems(
  id: string,
  key: string,
  value: string,
  english: string
): string[] {
  if (value.trim() === '') {
    return [`${id}: empty fr value for "${key}"`];
  }
  const problems: string[] = [];
  if (!keepsEnds(value, english)) {
    problems.push(`${id}: fr value of "${key}" changes the spaces at its ends`);
  }
  if (placeholders(value).join() !== placeholders(english).join()) {
    problems.push(`${id}: fr placeholders of "${key}" differ from en`);
  }
  if (
    value === english &&
    !onlyTechnical(value) &&
    !SAME_IN_FRENCH.has(english)
  ) {
    problems.push(`${id}: "${key}" is not translated in fr`);
  }
  if (!KEEPS_ENGLISH_PUNCTUATION.has(english)) {
    problems.push(...findPunctuationProblems(id, key, value));
  }
  return problems;
}
