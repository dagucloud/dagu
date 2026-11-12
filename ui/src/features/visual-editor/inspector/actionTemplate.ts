// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { JSONSchema, NewStep } from '../contracts';

function firstType(schema: JSONSchema): string | undefined {
  if (typeof schema.type === 'string') {
    return schema.type;
  }
  if (Array.isArray(schema.type)) {
    return schema.type[0];
  }
  const branch = schema.oneOf?.[0] ?? schema.anyOf?.[0];
  return branch ? firstType(branch) : undefined;
}

/**
 * Start value of a required field: the schema default, else the first enum
 * member, else an empty value of the type. Numbers have no empty value and
 * give undefined, so the template leaves them out.
 */
export function initialFieldValue(schema: JSONSchema | undefined): unknown {
  if (!schema) {
    return '';
  }
  if (schema.default !== undefined) {
    return schema.default;
  }
  if (Array.isArray(schema.enum) && schema.enum.length > 0) {
    return schema.enum[0];
  }
  switch (firstType(schema)) {
    case 'boolean':
      return false;
    case 'object':
      return {};
    case 'array':
      return [];
    case 'number':
    case 'integer':
      return undefined;
    default:
      return '';
  }
}

/** The required `with` fields of a flat schema with their start values. */
export function requiredWithValues(
  withSchema: JSONSchema | null
): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const name of withSchema?.required ?? []) {
    const value = initialFieldValue(withSchema?.properties?.[name]);
    if (value !== undefined) {
      values[name] = value;
    }
  }
  return values;
}

const LLM = { provider: 'anthropic', model: 'claude-sonnet-4-5' };
const XLSX = 'file.xlsx';

/**
 * Valid start values of the actions whose empty required fields fail the
 * validation of the server. A step that the palette adds passes the
 * validation at once; the user then edits the values. `with` replaces the
 * fields that the schema marks as required.
 */
const STARTER: Record<string, Record<string, unknown>> = {
  'artifact.read': { path: 'file.txt' },
  'artifact.write': { path: 'file.txt', content: 'TODO' },
  'browser.extract': {
    url: 'https://example.com',
    instruction: 'TODO',
    schema: { type: 'object' },
    llm: LLM,
  },
  'browser.run': { do: [{ goto: 'https://example.com' }] },
  'chat.completion': { prompt: 'TODO' },
  'clickhouse.query': { url: 'http://localhost:8123', query: 'SELECT 1' },
  'computer.extract': {
    instruction: 'TODO',
    schema: { type: 'object' },
    llm: LLM,
  },
  'computer.run': { do: [{ wait: '1s' }] },
  'dag.enqueue': { dag: 'child-workflow' },
  'dag.run': { dag: 'child-workflow' },
  'data.convert': { from: 'json', to: 'yaml', input: '{}' },
  'data.pick': { from: 'json', select: '$.a', input: '{}' },
  'decision.evaluate': {
    provider: 'openrouter',
    model: 'TODO',
    state: 'TODO',
    questions: { ok: { type: 'noul', instructions: 'Is it fine?' } },
  },
  exec: { command: 'echo', args: ['TODO'] },
  'file.copy': { source: 'a.txt', destination: 'b.txt' },
  'file.delete': { path: 'file.txt' },
  'file.list': { path: '.' },
  'file.mkdir': { path: 'dir' },
  'file.move': { source: 'a.txt', destination: 'b.txt' },
  'file.read': { path: 'file.txt' },
  'file.stat': { path: 'file.txt' },
  'file.write': { path: 'file.txt', content: 'TODO' },
  'git.checkout': { repository: 'https://example.com/repo.git', path: 'repo' },
  'git.worktree.remove': { path: 'tree' },
  'harness.run': { prompt: 'TODO', provider: 'claude' },
  'http.request': { method: 'GET', url: 'https://example.com' },
  'human.task': { prompt: 'TODO' },
  'jq.filter': { filter: '.' },
  'js.run': { script: 'return 1;' },
  'k8s.run': { image: 'alpine' },
  'kubernetes.run': { image: 'alpine' },
  'log.write': { message: 'TODO' },
  'mail.send': { message: 'TODO' },
  'mail.organize': { mailbox: 'INBOX', emails: ['1'], mark: 'read' },
  'mail.search': { mailbox: 'INBOX' },
  'outputs.write': { values: { result: 'TODO' } },
  'postgres.import': {
    dsn: 'postgres://localhost/db',
    import: { input_file: 'data.csv', table: 'my_table' },
  },
  'postgres.query': { query: 'SELECT 1' },
  // The routes need a target step: the add operation creates it.
  'router.route': { value: 'ok', routes: {} },
  'sftp.download': { source: 'a.txt', destination: 'b.txt' },
  'sftp.upload': { source: 'a.txt', destination: 'b.txt' },
  'sqlite.import': {
    dsn: 'file:data.db',
    import: { input_file: 'data.csv', table: 'my_table' },
  },
  'sqlite.query': { query: 'SELECT 1' },
  'ssh.run': { command: 'echo "TODO"' },
  'state.delete': { key: 'TODO' },
  'state.diff': { key: 'TODO', value: 'TODO' },
  'state.get': { key: 'TODO' },
  'state.set': { key: 'TODO', value: 'TODO' },
  'template.render': { template: 'TODO' },
  'wait.duration': { duration: '1s' },
  'wait.file': { path: 'file.txt' },
  'wait.http': { url: 'https://example.com' },
  'wait.until': { until: '2030-01-01T00:00:00Z' },
  'xlsx.append': { path: XLSX, rows: [] },
  'xlsx.convert': { path: XLSX, output: 'out.csv' },
  'xlsx.extract': {
    path: XLSX,
    instruction: 'TODO',
    schema: { type: 'object' },
    llm: LLM,
  },
  'xlsx.info': { path: XLSX },
  'xlsx.list_sheets': { path: XLSX },
  'xlsx.read': { path: XLSX },
  'xlsx.sheet': { path: XLSX, operation: 'add', sheet: 'Sheet2' },
  'xlsx.update_rows': { path: XLSX, key: 'id', rows: [] },
  'xlsx.validate': { path: XLSX, required: ['id'] },
  'xlsx.write': { path: XLSX, rows: [] },
  'xlsx.write_cells': { path: XLSX, cells: { A1: 'TODO' } },
};

/**
 * The step that the palette inserts for an action. `with` holds the
 * required fields only; it is left out when there is none, unless
 * `keepEmptyWith` is set (custom actions always take a `with` block).
 */
export function actionTemplate(
  action: string,
  withSchema: JSONSchema | null,
  keepEmptyWith = false
): NewStep {
  const starter = keepEmptyWith ? undefined : STARTER[action];
  const values = starter
    ? structuredClone(starter)
    : requiredWithValues(withSchema);
  if (Object.keys(values).length === 0 && !keepEmptyWith) {
    return { action };
  }
  return { action, with: values };
}
