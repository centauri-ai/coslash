import { useEffect, useRef, useState, type RefObject } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { apiFetch, readApiError } from '../lib/api';
import { isLocalSession, sessionKey, type Session } from '../lib/session';

export function DeleteSessionDialogContent({
  session,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  session: Session;
  pending: boolean;
  error: string | null;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <>
      <DialogHeader>
        <DialogTitle>Delete session?</DialogTitle>
        <DialogDescription>
          This permanently deletes the local session and its saved history. This cannot be undone. Close the
          session in its agent first.
        </DialogDescription>
      </DialogHeader>
      <div className="min-w-0 space-y-2">
        <p className="font-semibold break-words">
          {session.name ?? session.firstPrompt ?? 'Untitled session'}
        </p>
        <p className="font-mono text-xs break-all">
          {session.sourceId} / {session.agent} / {session.id}
        </p>
      </div>
      {error != null && (
        <p role="alert" className="text-danger-fg text-sm break-words">
          {error}
        </p>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="destructive" disabled={pending} onClick={() => void onConfirm()}>
          {pending ? 'Deleting session...' : 'Delete session'}
        </Button>
      </DialogFooter>
    </>
  );
}

export function DeleteSessionDialog({
  session,
  onClose,
  onDeleted,
  onRefresh,
  returnFocusRef,
}: {
  session: Session | null;
  onClose: () => void;
  onDeleted: (session: Session) => void;
  onRefresh: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const key = session == null ? null : sessionKey(session);
  const attempt = useRef({ key, generation: 0 });
  const inFlight = useRef(new Set<string>());
  const [pendingKeys, setPendingKeys] = useState(new Set<string>());
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null);
  /* oxlint-disable react/set-state-in-effect -- reset confirmation when its exact identity changes */
  useEffect(() => {
    attempt.current = { key, generation: attempt.current.generation + 1 };
    setFailure(null);
    return () => {
      attempt.current.generation += 1;
    };
  }, [key]);
  /* oxlint-enable react/set-state-in-effect */

  const close = () => {
    attempt.current = { key: null, generation: attempt.current.generation + 1 };
    setFailure(null);
    onClose();
  };
  const confirm = async () => {
    if (
      session == null ||
      !isLocalSession(session) ||
      key == null ||
      attempt.current.key !== key ||
      inFlight.current.has(key)
    )
      return;
    const generation = attempt.current.generation;
    inFlight.current.add(key);
    setFailure(null);
    setPendingKeys(new Set(inFlight.current));
    try {
      const query = new URLSearchParams({ source: session.sourceId, agent: session.agent, id: session.id });
      const response = await apiFetch(`/api/sessions?${query}`, { method: 'DELETE' });
      if (response.status !== 204) {
        const error = await readApiError(response);
        throw new Error(error?.error || `Session could not be deleted (${response.status}).`);
      }
      onRefresh();
      if (attempt.current.generation !== generation) return;
      onDeleted(session);
      close();
    } catch (error) {
      if (attempt.current.generation === generation)
        setFailure({
          key,
          message: error instanceof Error ? error.message : 'Session could not be deleted.',
        });
    } finally {
      inFlight.current.delete(key);
      setPendingKeys(new Set(inFlight.current));
    }
  };

  return (
    <Dialog
      open={session != null}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent
        className="coslash-shell max-h-[calc(100%-2rem)] overflow-y-auto"
        onCloseAutoFocus={(event) => {
          const target = returnFocusRef?.current;
          if (target?.isConnected) {
            event.preventDefault();
            target.focus();
          } else {
            event.preventDefault();
            document.querySelector<HTMLInputElement>('input[type="search"]')?.focus();
          }
        }}
      >
        {session != null && (
          <DeleteSessionDialogContent
            session={session}
            pending={key != null && pendingKeys.has(key)}
            error={failure?.key === key ? failure.message : null}
            onCancel={close}
            onConfirm={confirm}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
