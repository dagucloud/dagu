// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export type ControllerDAGOption = {
  fileName: string;
  description?: string;
};

type ControllerDAGCandidate = {
  fileName: string;
  dag: {
    name: string;
    description?: string;
    params?: string[];
  };
};

export function controllerDAGOption(
  value: ControllerDAGCandidate
): ControllerDAGOption | null {
  if (
    value.fileName !== value.dag.name ||
    value.dag.params?.some((param) => /^\d+=/.test(param))
  ) {
    return null;
  }
  return {
    fileName: value.fileName,
    description: value.dag.description,
  };
}
