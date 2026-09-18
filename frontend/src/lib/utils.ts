import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

// The session-view type ramp is a custom font-size scale, which tailwind-merge
// would otherwise read as a text colour and drop when one follows it.
const twMerge = extendTailwindMerge({
  extend: { classGroups: { 'font-size': ['text-meta', 'text-cell', 'text-ui'] } },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
