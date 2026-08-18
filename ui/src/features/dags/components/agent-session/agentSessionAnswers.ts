// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export function combineQuestionAnswer(
  selected: string[],
  custom: string,
  multiple: boolean
): string[] {
  const value = custom.trim();
  if (!value) return selected;
  return multiple ? [...selected, value] : [value];
}
