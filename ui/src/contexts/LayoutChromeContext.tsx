// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, {
  createContext,
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
} from 'react';

/** Lets a page change the frame that Layout draws around it. */
export interface LayoutChromeValue {
  /** Requests full bleed and returns the function that releases the request. */
  claimFullBleed(): () => void;
  /** Hides the breadcrumb strip and returns the function that releases the request. */
  claimHideBreadcrumb(): () => void;
}

export const LayoutChromeContext = createContext<LayoutChromeValue | null>(
  null
);

/** A counter of the callers that hold a claim, with the claim function. */
function useClaims(): { active: boolean; claim: () => () => void } {
  const [claims, setClaims] = useState(0);
  const claim = useCallback(() => {
    setClaims((count) => count + 1);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      setClaims((count) => Math.max(0, count - 1));
    };
  }, []);
  return { active: claims > 0, claim };
}

/**
 * Holds the layout chrome state for Layout. Full bleed and the hidden
 * breadcrumb are each on while at least one caller holds a claim.
 */
export function useLayoutChromeState(): {
  fullBleed: boolean;
  hideBreadcrumb: boolean;
  value: LayoutChromeValue;
} {
  const bleed = useClaims();
  const breadcrumb = useClaims();
  const claimFullBleed = bleed.claim;
  const claimHideBreadcrumb = breadcrumb.claim;
  const value = useMemo(
    () => ({ claimFullBleed, claimHideBreadcrumb }),
    [claimFullBleed, claimHideBreadcrumb]
  );
  return {
    fullBleed: bleed.active,
    hideBreadcrumb: breadcrumb.active,
    value,
  };
}

export function LayoutChromeProvider({
  value,
  children,
}: {
  value: LayoutChromeValue;
  children: React.ReactNode;
}) {
  return (
    <LayoutChromeContext.Provider value={value}>
      {children}
    </LayoutChromeContext.Provider>
  );
}

/**
 * Removes the padding and the scroll of the Layout content area while the
 * caller is mounted and `on` is true. Without a provider it does nothing.
 */
export function useFullBleed(on: boolean): void {
  const chrome = useContext(LayoutChromeContext);
  // A layout effect applies the frame before the first paint, with no jump.
  useLayoutEffect(() => {
    if (!chrome || !on) return;
    return chrome.claimFullBleed();
  }, [chrome, on]);
}

/**
 * Hides the breadcrumb strip above the content while the caller is mounted
 * and `on` is true, so that the page draws its own first row. Without a
 * provider it does nothing.
 */
export function useHideBreadcrumb(on: boolean): void {
  const chrome = useContext(LayoutChromeContext);
  useLayoutEffect(() => {
    if (!chrome || !on) return;
    return chrome.claimHideBreadcrumb();
  }, [chrome, on]);
}
