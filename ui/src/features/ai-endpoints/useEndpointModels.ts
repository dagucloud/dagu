// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { modelInfos, type ModelInfo } from './modelInfo';
import { useEndpointChoices, type DagIdentity } from './useEndpointChoices';

export interface EndpointModels {
  /** The model names of the last good read. Empty when the endpoint has no list. */
  models: string[];
  /** The same models with their display name and their effort levels. */
  infos: ModelInfo[];
  /** True once the endpoints of the DAG have arrived. */
  loaded: boolean;
  /** True when the DAG may use an endpoint of this name. */
  known: boolean;
  /** The aliases of the CLI claude from the server. Undefined before the answer: no model check. */
  cliModelAliases: string[] | undefined;
}

const NONE: string[] = [];

/**
 * The models of the endpoint that a step names, for the suggestions of its
 * Model field (Spec 093, "Use in a step"). The names come from the route of
 * choices, so a role that edits DAGs can read them, and the request is the one
 * that the Endpoint select already makes. A step with no endpoint, an endpoint
 * that the DAG may not use and an endpoint with no list all give an empty list.
 * The server checks the model of a step against the list when the step starts
 * (see `modelNotListed`).
 */
export function useEndpointModels(
  endpointName: string | undefined,
  dag: DagIdentity | undefined
): EndpointModels {
  const name = endpointName?.trim() ?? '';
  // The request is the one of the Endpoint select: every caller shares it.
  const { choices, loaded, cliModelAliases } = useEndpointChoices(dag);
  return useMemo(() => {
    const choice =
      name === '' ? undefined : choices.find((item) => item.name === name);
    return {
      models: choice?.models ?? NONE,
      infos: modelInfos(choice),
      loaded: name !== '' && loaded,
      known: choice !== undefined,
      cliModelAliases,
    };
  }, [name, choices, loaded, cliModelAliases]);
}
