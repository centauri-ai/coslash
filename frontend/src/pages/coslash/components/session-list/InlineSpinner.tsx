import { LoaderCircle } from 'lucide-react';

export function InlineSpinner() {
  return <LoaderCircle className="text-coslash-muted size-3.5 animate-spin" aria-hidden="true" />;
}
