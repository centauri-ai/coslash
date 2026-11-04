import { useState } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { resolveHubSwitch, type HubSwitch } from './api';

const host = (origin: string) => new URL(origin).host;

export function HubSwitchDialogContent({
  request,
  pending,
  error,
  onResolve,
}: {
  request: HubSwitch;
  pending: boolean;
  error: string | null;
  onResolve: (confirm: boolean) => void;
}) {
  const from = host(request.from);
  const to = host(request.to);
  const From = <span className="whitespace-nowrap">{from}</span>;
  const To = <span className="whitespace-nowrap">{to}</span>;
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {request.canSwitch ? 'Switch to another Hub?' : 'This computer syncs to another Hub'}
        </DialogTitle>
        <DialogDescription>
          {request.canSwitch ? (
            <>
              This computer syncs to {From}. {To} asked to connect it. If you switch, sync to {From} stops on
              this computer.
            </>
          ) : (
            <>
              This computer syncs to {From}, not {To}. To sync to {To} instead, open {To} and choose Connect a
              computer.
            </>
          )}
        </DialogDescription>
      </DialogHeader>
      {error && (
        <div role="alert" className="text-destructive text-sm">
          {error}
        </div>
      )}
      <DialogFooter>
        {request.canSwitch ? (
          <>
            <Button variant="outline" disabled={pending} onClick={() => onResolve(false)}>
              Keep {from}
            </Button>
            <Button disabled={pending} onClick={() => onResolve(true)}>
              Switch to {to}
            </Button>
          </>
        ) : (
          <Button disabled={pending} onClick={() => onResolve(false)}>
            OK
          </Button>
        )}
      </DialogFooter>
    </>
  );
}

export function HubSwitchDialog({ request }: { request: HubSwitch | undefined }) {
  const [resolvedAt, setResolvedAt] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const open = request != null && request.requestedAt !== resolvedAt;

  const resolve = (confirm: boolean) => {
    if (!request) return;
    setPending(true);
    setError(null);
    resolveHubSwitch(request.to, confirm)
      .then(
        () => setResolvedAt(request.requestedAt),
        (failure: unknown) =>
          setError(failure instanceof Error ? failure.message : 'coSlash Local could not switch Hubs.'),
      )
      .finally(() => setPending(false));
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !pending && resolve(false)}>
      {request && (
        <DialogContent onInteractOutside={(event) => event.preventDefault()}>
          <HubSwitchDialogContent request={request} pending={pending} error={error} onResolve={resolve} />
        </DialogContent>
      )}
    </Dialog>
  );
}
