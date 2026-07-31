// Narrows a raw string from an untyped callback (e.g. Radix's onValueChange) to
// a known union. Throws on an unexpected value so drift fails loudly at the edge
// instead of being silently cast through.
export function assertOneOf<T extends string>(value: string, allowed: readonly T[]): T {
  const match = allowed.find((candidate) => candidate === value);
  if (match === undefined) {
    throw new Error(`Expected one of [${allowed.join(', ')}], got "${value}"`);
  }
  return match;
}
