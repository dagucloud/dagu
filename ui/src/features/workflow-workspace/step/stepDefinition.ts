// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { ApiDAGDetails } from '../contracts';

type ApiStep = components['schemas']['Step'];

/** The definition of a step of the DAG: a step of the list or a handler. */
export function stepDefinitionOf(
  dag: ApiDAGDetails | undefined,
  step: string
): ApiStep | undefined {
  return (
    dag?.steps?.find((s) => s.name === step) ??
    Object.values(dag?.handlerOn ?? {}).find((s) => s?.name === step)
  );
}
