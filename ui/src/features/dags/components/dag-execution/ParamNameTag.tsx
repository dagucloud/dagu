// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Shows a parameter's name beside a label that differs from it, so the
 * identifier used in commands and references stays visible.
 */
export function ParamNameTag({
  name,
  label,
}: {
  name?: string;
  label?: string;
}) {
  if (!name || name === label) {
    return null;
  }
  return (
    <code
      title={name}
      className="min-w-0 truncate font-mono text-xs text-muted-foreground"
    >
      {name}
    </code>
  );
}
