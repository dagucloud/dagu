// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { StaticTranslationValues } from '@/i18n/staticMessages';
import type { EditErrorCode } from './contracts';

type Translate = (source: string, values?: StaticTranslationValues) => string;

// English sources of the i18n keys, one for each edit error code.
const MESSAGES: Record<Exclude<EditErrorCode, 'noop'>, string> = {
  parseError: 'Fix the YAML errors before you edit in the canvas.',
  unknownStep: 'This step does not exist any more.',
  unsupportedForm:
    'This edit needs the steps in the explicit form: a list of named steps.',
  chainType:
    'Steps of a chain DAG run in order. Convert the steps to the explicit form to edit dependencies.',
  agentType: 'The visual editor cannot edit the steps of an agent DAG.',
  duplicateName: 'Another step has this name.',
  invalidName: 'This step name is not valid.',
  cycle: 'This dependency makes a cycle.',
  invalidYaml: 'The step YAML is not valid.',
};

/**
 * Translated message for a failed edit, or null for `noop` (the edit
 * changed nothing and needs no message).
 */
export function editErrorMessage(
  code: EditErrorCode,
  ts: Translate
): string | null {
  return code === 'noop' ? null : ts(MESSAGES[code]);
}

/** Tells if converting the steps to the explicit form can make the edit work. */
export function canConvertForEdit(code: EditErrorCode): boolean {
  return code === 'unsupportedForm' || code === 'chainType';
}

/**
 * Translated text of the frozen canvas. With `unparsedLine`, no text has
 * parsed and the canvas shows the steps that the server read.
 */
export function frozenMessage(ts: Translate, unparsedLine?: number): string {
  return unparsedLine === undefined
    ? ts('YAML has errors: the canvas shows the last valid version')
    : ts(
        'The editor cannot read the YAML at line {line}: the canvas shows the steps the server read',
        { line: unparsedLine }
      );
}
