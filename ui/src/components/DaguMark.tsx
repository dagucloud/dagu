// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SVGProps } from 'react';

/** The Dagu logo mark, drawn in the current text color. */
export function DaguMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 48 48"
      fill="none"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <path
        d="M15 8.5C29.5 11 37.5 17 37.5 24C37.5 31 29.5 37 15 39.5"
        stroke="currentColor"
        strokeWidth="9.5"
        strokeLinecap="round"
      />
      <circle cx="11.5" cy="24" r="5.4" fill="currentColor" />
    </svg>
  );
}
