import type { ReactNode } from 'react';
import { LoaderCircle } from 'lucide-react';

export function LoadingSpinner({ isLoading, children }: { isLoading: boolean; children: ReactNode }) {
  if (isLoading) {
    return (
      <div role="status" aria-label="Loading" className="grid h-full place-items-center">
        <LoaderCircle className="text-muted-foreground size-4 animate-spin" />
      </div>
    );
  }
  return <>{children}</>;
}
