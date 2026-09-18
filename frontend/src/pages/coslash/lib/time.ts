// Durations in milliseconds.
export const MINUTE = 60_000;
export const HOUR = 60 * MINUTE;
export const DAY = 24 * HOUR;

export function promptCacheTiming(lastAccessAt: number, now: number) {
  const age = now - lastAccessAt;
  const within5m = age <= 5 * MINUTE;
  const within1h = age <= HOUR;
  return {
    within5m,
    within1h,
    nextRefreshAt: within5m ? lastAccessAt + 5 * MINUTE + 1 : within1h ? lastAccessAt + HOUR + 1 : null,
  };
}
