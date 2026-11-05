// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGRunDetails, ApiNode, DAGPageData } from './types';

/**
 * The page data of a run whose workflow file is gone, cannot be read or holds
 * another DAG: the DAG as the run stored it (its name, its steps and its
 * handlers) and the run as the latest one.
 */
export function standalonePageData(run: ApiDAGRunDetails): DAGPageData {
  const stepOf = (node: ApiNode | undefined) => node?.step;
  return {
    dag: {
      name: run.name,
      steps: (run.nodes ?? []).map((node) => node.step),
      handlerOn: {
        success: stepOf(run.onSuccess),
        failure: stepOf(run.onFailure),
        abort: stepOf(run.onAbort),
        exit: stepOf(run.onExit),
      },
    },
    latestDAGRun: run,
    suspended: false,
    pinned: false,
    localDags: [],
    errors: [],
  };
}
