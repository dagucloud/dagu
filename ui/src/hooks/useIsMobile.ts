import { useMediaQuery } from '@/hooks/useMediaQuery';
import { PHONE_BELOW_PX, WIDE_QUERY } from '@/lib/breakpoints';

/** True on a window narrower than a tablet: the layout of a phone. */
export function useIsMobile(): boolean {
  // Without matchMedia (tests) the width of the window decides.
  const wide = useMediaQuery(
    WIDE_QUERY,
    typeof window === 'undefined' || window.innerWidth >= PHONE_BELOW_PX
  );
  return !wide;
}
