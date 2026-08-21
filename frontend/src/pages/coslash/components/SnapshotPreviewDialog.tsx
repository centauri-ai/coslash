import { useEffect, useState } from 'react';
import { AlertTriangleIcon, CheckIcon, LockKeyholeIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  canonicalPayloadText,
  formatCanonicalJson,
  frozenCostDisclosure,
  isSnapshotPreview,
  PREVIEW_PRIVACY_COPY,
  previewNotices,
  previewRequestPath,
  STRUCTURALLY_EXCLUDED,
  type SnapshotPreview,
} from '@/pages/coslash/lib/preview';
import type { SessionDetail } from '@/pages/coslash/lib/session';

type LoadState =
  | { status: 'idle' | 'loading' }
  | { status: 'error'; message: string }
  | { status: 'loaded'; preview: SnapshotPreview };

function PreviewReady({ preview }: { preview: SnapshotPreview }) {
  let payload: string;
  try {
    payload = formatCanonicalJson(canonicalPayloadText(preview));
  } catch {
    return (
      <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg border p-3 text-sm">
        The canonical bytes no longer match this preview. Close it and try again; sharing is blocked.
      </div>
    );
  }
  const notices = previewNotices(preview);
  const hash = typeof preview.snapshot?.contentHash === 'string' ? preview.snapshot.contentHash : '—';

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto pr-1">
      <div className="bg-success-bg text-success-fg flex items-start gap-2 rounded-lg border p-3">
        <CheckIcon className="mt-0.5 size-4 shrink-0" />
        <div>
          <div className="text-sm font-semibold">Canonical snapshot ready</div>
          <div className="pt-1 text-xs">
            {preview.payloadBytes?.toLocaleString()} exact bytes · {frozenCostDisclosure(preview)}
          </div>
          <div className="max-w-full truncate pt-1 font-mono text-xs" title={hash}>
            {hash}
          </div>
        </div>
      </div>

      {notices.length > 0 && (
        <section aria-labelledby="preview-notices">
          <h3 id="preview-notices" className="text-warning-fg text-xs font-bold tracking-wide">
            BOUNDED OR REDACTED BEFORE SHARING
          </h3>
          <div className="mt-1 rounded-lg border">
            {notices.map((notice, index) => (
              <div
                key={`${notice.kind}-${notice.path}-${index}`}
                className="flex gap-2 border-b p-2 text-xs last:border-b-0"
              >
                <Badge variant="secondary">{notice.kind}</Badge>
                <span className="min-w-0 font-mono break-all">{notice.path}</span>
                <span className="text-muted-foreground ml-auto shrink-0">{notice.reason}</span>
              </div>
            ))}
          </div>
        </section>
      )}

      <section className="rounded-lg border p-3" aria-labelledby="preview-privacy">
        <h3 id="preview-privacy" className="flex items-center gap-1 text-xs font-bold tracking-wide">
          <LockKeyholeIcon className="size-3" /> PRIVACY BOUNDARY
        </h3>
        <p className="text-muted-foreground pt-2 text-xs">{PREVIEW_PRIVACY_COPY}</p>
        <ul className="text-muted-foreground grid list-disc gap-1 pt-2 pl-4 text-xs sm:grid-cols-2">
          {STRUCTURALLY_EXCLUDED.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </section>

      <section className="min-h-48" aria-labelledby="preview-payload">
        <div className="flex items-baseline justify-between gap-2">
          <h3 id="preview-payload" className="text-xs font-bold tracking-wide">
            EXACT DESTINATION-INDEPENDENT PAYLOAD
          </h3>
          <span className="text-muted-foreground text-xs">{preview.schemaVersion}</span>
        </div>
        <pre className="bg-muted mt-1 max-h-80 overflow-auto rounded-lg border p-3 font-mono text-[11px] leading-relaxed whitespace-pre">
          {payload}
        </pre>
      </section>
    </div>
  );
}

function PreviewBlocked({ preview }: { preview: SnapshotPreview }) {
  return (
    <div role="alert" className="bg-warning-bg text-warning-fg flex items-start gap-2 rounded-lg border p-3">
      <AlertTriangleIcon className="mt-0.5 size-4 shrink-0" />
      <div>
        <div className="text-sm font-semibold">This snapshot cannot be approved</div>
        <p className="pt-1 text-xs">{preview.problem?.message ?? 'The canonical preview is unavailable.'}</p>
        <p className="pt-2 text-xs font-semibold">
          {preview.problem?.action ?? 'Close this preview and try again.'}
        </p>
        <Badge variant="secondary" className="mt-3 font-mono">
          {preview.state}
        </Badge>
      </div>
    </div>
  );
}

export type SnapshotPreviewDialogProps = {
  detail: SessionDetail;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  previewOnly?: boolean;
};

export function SnapshotPreviewDialog({
  detail,
  open,
  onOpenChange,
  previewOnly = false,
}: SnapshotPreviewDialogProps) {
  const [load, setLoad] = useState<LoadState>({ status: 'idle' });

  useEffect(() => {
    if (!open) {
      setLoad({ status: 'idle' });
      return;
    }
    const controller = new AbortController();
    setLoad({ status: 'loading' });
    apiFetch(previewRequestPath(detail, detail.mtime), { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Preview request failed (${response.status})`);
        return response.json() as Promise<unknown>;
      })
      .then((preview) => {
        if (!isSnapshotPreview(preview)) throw new Error('Invalid preview response');
        if (!controller.signal.aborted) setLoad({ status: 'loaded', preview });
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setLoad({ status: 'error', message: 'Could not build the canonical snapshot preview.' });
        }
      });
    return () => controller.abort();
  }, [detail, open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] w-[min(56rem,calc(100vw-2rem))] max-w-none! flex-col">
        <DialogHeader>
          <DialogTitle>{previewOnly ? 'Team sharing preview' : 'Exact snapshot preview'}</DialogTitle>
          <DialogDescription>
            {previewOnly
              ? `Review the exact, destination-independent payload a future Team share could use for ${detail.name ?? detail.id}.`
              : `This is the canonical, destination-independent revision for ${detail.name ?? detail.id}. Sharing stays off until you explicitly approve this exact revision in Share to Hub.`}
          </DialogDescription>
        </DialogHeader>

        {previewOnly && (
          <div role="note" className="bg-info-bg text-info-fg rounded-lg border p-3 text-sm">
            <Badge variant="secondary">PREVIEW ONLY · TEAM FEATURE</Badge>
            <p className="pt-2">
              This local test screen cannot approve or upload anything. Sharing requires a Team workspace.
            </p>
          </div>
        )}

        {load.status === 'loading' && (
          <div
            role="status"
            className="text-muted-foreground flex min-h-48 items-center justify-center text-sm"
          >
            Building the canonical preview…
          </div>
        )}
        {load.status === 'error' && (
          <div role="alert" className="text-destructive min-h-48 rounded-lg border p-3 text-sm">
            {load.message} Close this dialog and try again; sharing is blocked.
          </div>
        )}
        {load.status === 'loaded' &&
          (load.preview.state === 'ready' && load.preview.approvalAllowed ? (
            <PreviewReady preview={load.preview} />
          ) : (
            <PreviewBlocked preview={load.preview} />
          ))}

        <DialogFooter showCloseButton />
      </DialogContent>
    </Dialog>
  );
}
