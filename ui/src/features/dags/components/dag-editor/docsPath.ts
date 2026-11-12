// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { YamlPathSegment } from '../../../../hooks/useYamlCursorPath';

/** The docs path: the cursor path when it is in the scope, else the scope. */
export function docsPathInScope(
  info: { path: string[]; segments: YamlPathSegment[] },
  scopePath: string[] | null | undefined
): { path: string[]; segments: YamlPathSegment[] } {
  if (!scopePath || scopePath.length === 0) return info;
  const inside =
    info.path.length >= scopePath.length &&
    scopePath.every((key, i) => info.path[i] === key);
  if (inside) return info;
  return {
    path: scopePath,
    segments: scopePath.map((key, i) => ({
      key,
      isArrayIndex: i > 0 && scopePath[0] === 'steps' && /^\d+$/.test(key),
    })),
  };
}
