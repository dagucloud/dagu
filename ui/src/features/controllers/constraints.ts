// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ControllerRouterLLMConfigProvider } from '@/api/v1/schema';
import { isValidWorkspaceName } from '@/lib/workspace';

export const DEFAULT_CONTROLLER_MAX_TURNS = 100;
export const MIN_CONTROLLER_MAX_TURNS = 2;
export const MAX_CONTROLLER_MAX_TURNS = 1_000;
export const MAX_CONTROLLER_NAME_CODE_POINTS = 100;
export const MAX_CONTROLLER_NAME_BYTES = 256;
export const MAX_CONTROLLER_DESCRIPTION_BYTES = 4_096;
export const MAX_CONTROLLER_DAGS = 64;
export const MAX_CONTROLLER_STATES = 64;
export const MAX_CONTROLLER_TRANSITIONS = 256;
export const MAX_CONTROLLER_PROMPT_BYTES = 16_384;
export const MAX_CONTROLLER_SYSTEM_PROMPT_BYTES = 16_384;
export const CONTROLLER_ID_PATTERN = /^ctrl_[a-z2-7]{16}$/;
export const CONTROLLER_STATE_NAME_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
export const CONTROLLER_DAG_NAME_PATTERN = /^[A-Za-z0-9_-]{1,40}$/;
const CONTROLLER_LABEL_KEY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/;
const CONTROLLER_LABEL_VALUE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_./-]*$/;
const MAX_CONTROLLER_LABEL_KEY_BYTES = 63;
const MAX_CONTROLLER_LABEL_VALUE_BYTES = 255;
export const DEFAULT_CONTROLLER_LLM_PROVIDER =
  ControllerRouterLLMConfigProvider.openai;

export const CONTROLLER_LLM_PROVIDER_OPTIONS = [
  { value: DEFAULT_CONTROLLER_LLM_PROVIDER, label: 'OpenAI' },
  { value: ControllerRouterLLMConfigProvider.anthropic, label: 'Anthropic' },
  { value: ControllerRouterLLMConfigProvider.gemini, label: 'Gemini' },
] as const;

export const CONTROLLER_LLM_PROVIDERS = CONTROLLER_LLM_PROVIDER_OPTIONS.map(
  ({ value }) => value
);

export function isControllerLLMProvider(
  value: string
): value is ControllerRouterLLMConfigProvider {
  return CONTROLLER_LLM_PROVIDERS.some((provider) => provider === value);
}

export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function hasNonWhitespace(value: string): boolean {
  return !/^\p{White_Space}*$/u.test(value);
}

export function validateControllerPrompt(value: string): string | null {
  if (!hasNonWhitespace(value)) return 'Enter a prompt.';
  if (utf8ByteLength(value) > MAX_CONTROLLER_PROMPT_BYTES) {
    return `The prompt must be ${MAX_CONTROLLER_PROMPT_BYTES / 1_024} KiB or less.`;
  }
  return null;
}

export function validateControllerLabels(labels: string[]): string | null {
  let workspaceLabels = 0;
  for (const raw of labels) {
    const label = raw.trim();
    if (!label) return 'Labels must not be empty.';

    const separator = label.indexOf('=');
    const key = (separator < 0 ? label : label.slice(0, separator))
      .trim()
      .toLowerCase();
    const value = (separator < 0 ? '' : label.slice(separator + 1))
      .trim()
      .toLowerCase();
    if (
      utf8ByteLength(key) > MAX_CONTROLLER_LABEL_KEY_BYTES ||
      !CONTROLLER_LABEL_KEY_PATTERN.test(key)
    ) {
      return `Label ${raw} has an invalid key.`;
    }
    if (
      utf8ByteLength(value) > MAX_CONTROLLER_LABEL_VALUE_BYTES ||
      (value !== '' && !CONTROLLER_LABEL_VALUE_PATTERN.test(value))
    ) {
      return `Label ${raw} has an invalid value.`;
    }
    if (key !== 'workspace') continue;

    workspaceLabels++;
    if (!isValidWorkspaceName(value)) {
      return 'Workspace label is invalid.';
    }
  }
  return workspaceLabels > 1
    ? 'At most one workspace label is allowed.'
    : null;
}
