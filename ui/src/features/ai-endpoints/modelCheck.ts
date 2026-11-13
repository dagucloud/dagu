// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { AIEndpointChoice } from './useEndpointChoices';

/** The CLI provider whose model aliases the model check accepts (Spec 093). */
export const CLI_MODEL_PROVIDER = 'claude';

const LONG_CONTEXT_SUFFIX = '[1m]';

/**
 * True when the CLI claude accepts the name with no entry in a models list:
 * an alias of `aliases`, or an alias with the suffix `[1m]`. The server gives
 * `aliases` in the route of choices. This is the rule of the server.
 */
export function isCliModelAlias(
  name: string,
  aliases: readonly string[]
): boolean {
  const base = name.endsWith(LONG_CONTEXT_SUFFIX)
    ? name.slice(0, -LONG_CONTEXT_SUFFIX.length)
    : name;
  return aliases.includes(base.toLowerCase());
}

/**
 * True when the server fails a step that names `model` for an endpoint whose
 * models list is `models`: the model is a literal, the list is not empty, and
 * the list lacks the model. The CLI claude also accepts its aliases, and a
 * listed model with the suffix `[1m]`. An empty list never fails a step, and
 * the check claims nothing before the aliases of the server arrive.
 */
export function modelNotListed({
  models,
  model,
  cliProvider,
  aliases,
}: {
  models: readonly string[];
  model: string;
  /** The CLI provider of an agent step. It is empty for a chat step. */
  cliProvider?: string | undefined;
  /** The aliases of the CLI from the server. Undefined before the answer: no check. */
  aliases: readonly string[] | undefined;
}): boolean {
  if (aliases === undefined) return false;
  if (model === '' || models.length === 0 || model.includes('${')) return false;
  const cli = cliProvider === CLI_MODEL_PROVIDER;
  if (cli && isCliModelAlias(model, aliases)) return false;
  if (models.includes(model)) return false;
  if (
    cli &&
    model.endsWith(LONG_CONTEXT_SUFFIX) &&
    models.includes(model.slice(0, -LONG_CONTEXT_SUFFIX.length))
  ) {
    return false;
  }
  return true;
}

/** What is wrong with the endpoint and the model of a step, as the node says it. */
export type ModelProblem =
  | { kind: 'endpoint'; name: string }
  | { kind: 'protocol'; name: string }
  | { kind: 'model'; endpoint: string; model: string };

/**
 * The problem that the server finds when the step starts, as far as the UI
 * can know it (Spec 093, "Use in a step"). An endpoint that the server does
 * not know fails the step, and so does an endpoint of a protocol that the step
 * does not accept. So does a model that the models list of a known endpoint
 * lacks (see `modelNotListed`). The UI claims nothing before the
 * choices have arrived. The server may find the model after a refresh of the
 * list.
 */
export function modelProblem({
  endpoint,
  model,
  choices,
  loaded,
  cliProvider,
  aliases,
  protocols,
}: {
  endpoint: string;
  model: string;
  choices: readonly AIEndpointChoice[];
  loaded: boolean;
  cliProvider?: string | undefined;
  aliases: readonly string[] | undefined;
  /** The protocols that the step accepts. Absent: every protocol. */
  protocols?: readonly string[] | undefined;
}): ModelProblem | null {
  if (!loaded || endpoint === '') return null;
  const known = choices.find((choice) => choice.name === endpoint);
  if (!known) return { kind: 'endpoint', name: endpoint };
  if (protocols && !protocols.includes(known.protocol)) {
    return { kind: 'protocol', name: endpoint };
  }
  return modelNotListed({
    models: known.models ?? [],
    model,
    cliProvider,
    aliases,
  })
    ? { kind: 'model', endpoint, model }
    : null;
}
