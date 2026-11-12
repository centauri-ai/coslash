import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { apiFetch } from '@/pages/coslash/lib/api';

type Hub = { origin: string; primary: boolean; enabled: boolean; state: string; failure?: string };
type Flow = { kind: 'pair' | 'enable'; origin: string };
type Pairing = { id: string; code: string; url: string; pending: boolean };
type Notice = { text: string; alert: boolean };
type PairingStart = {
  id?: unknown;
  pairing?: { userCode?: unknown; verificationUri?: unknown; verificationUriComplete?: unknown };
};

const pairDisclosure =
  'Pairing this Hub stores a device credential but does not authorize or start personal sync. Enabling sync requires separate consent. Workspace sharing requires separate consent.';
const pairAgainDisclosure =
  'Pairing again turns sync off for this Hub until you enable it again. Uploads to this Hub then start over under the new approval.';
const enableDisclosure =
  'Enabling this Hub starts sync and authorizes automatic upload of eligible personal session records, including prompts, commands and source snippets, to this destination under its policy and Local privacy settings. This consent persists across restarts. Workspace sharing requires separate consent.';

const statusLabels: Record<string, string> = {
  disabled: 'Sync disabled',
  not_running: 'Not running',
  start_failed: 'Could not start',
  not_connected: 'Not connected',
  disconnected: 'Disconnected',
  connected_idle: 'Connected',
  connected_syncing: 'Syncing',
  paused: 'Paused on this computer',
  auto_sync_off: 'Auto-sync off',
  update_required: 'Update required',
};
const pairingResults: Record<string, Notice> = {
  pending: { text: 'Waiting for approval in Hub.', alert: false },
  retrying: { text: 'Hub is not ready. Check approval again.', alert: false },
  paired_disabled: { text: 'Paired. Sync is disabled until you separately enable it.', alert: false },
  declined: { text: 'Pairing was declined in Hub.', alert: true },
  expired: { text: 'Pairing expired. Start again.', alert: true },
  cancelled: { text: 'Pairing was cancelled.', alert: true },
  credential_store_failed: { text: 'The device credential could not be saved.', alert: true },
  failed: { text: 'Pairing could not be completed. Check the Hub status and try again.', alert: true },
};
const mayNeedPairing = new Set(['disabled', 'not_connected', 'disconnected', 'start_failed']);

function parseOrigin(raw: string): string | null {
  try {
    const url = new URL(raw.trim());
    const plain = !url.username && !url.password && url.pathname === '/' && !url.search && !url.hash;
    return url.protocol === 'https:' && plain ? url.origin : null;
  } catch {
    return null;
  }
}

function approvalUrl(raw: unknown, origin: string): string | null {
  try {
    const url = new URL(String(raw));
    return url.origin === origin && !url.username && !url.password ? url.href : null;
  } catch {
    return null;
  }
}

async function request(path: string, body?: unknown): Promise<unknown> {
  const response = await apiFetch(
    path,
    body === undefined
      ? {}
      : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) },
  );
  if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status}).`);
  return response.json() as Promise<unknown>;
}

function decodeHubs(value: unknown): Hub[] {
  const valid =
    Array.isArray(value) &&
    value.every(
      (hub: Partial<Hub> | null) =>
        typeof hub?.origin === 'string' &&
        typeof hub.primary === 'boolean' &&
        typeof hub.enabled === 'boolean' &&
        typeof hub.state === 'string',
    );
  if (!valid) throw new Error('Invalid Hub status.');
  return value as Hub[];
}

function HubAction(props: { label: string; origin: string; disabled: boolean; onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      disabled={props.disabled}
      aria-label={`${props.label} ${props.origin}`}
      onClick={props.onClick}
    >
      {props.label}
    </Button>
  );
}

const errorText = (error: unknown) => (error instanceof Error ? error.message : 'Hub request failed.');

export function HubConnectionsSection() {
  const [hubs, setHubs] = useState<Hub[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [flow, setFlow] = useState<Flow | null>(null);
  const [originInput, setOriginInput] = useState('');
  const [consent, setConsent] = useState(false);
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  // Each counter names the current attempt. A completion from an older one, a
  // closed flow or an unmounted section is ignored.
  const attempt = useRef(0);
  const statusLoad = useRef(0);

  const refresh = useCallback(async () => {
    const load = ++statusLoad.current;
    try {
      const next = decodeHubs(await request('/api/hubs/status'));
      if (load !== statusLoad.current) return;
      setHubs(next);
      setLoadError(null);
    } catch (error) {
      if (load === statusLoad.current) setLoadError(errorText(error));
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(refresh);
    const interval = window.setInterval(() => {
      if (document.visibilityState !== 'hidden') void refresh();
    }, 10_000);
    return () => {
      attempt.current += 1;
      statusLoad.current += 1;
      window.clearInterval(interval);
    };
  }, [refresh]);

  const showFlow = (next: Flow | null) => {
    attempt.current += 1;
    busyRef.current = false;
    setBusy(false);
    setFlow(next);
    setOriginInput('');
    setConsent(false);
    setPairing(null);
    setNotice(null);
  };

  const perform = async (path: string, body: unknown, done: (value: unknown) => void) => {
    if (busyRef.current) return;
    const current = ++attempt.current;
    busyRef.current = true;
    setBusy(true);
    let failure: Notice | null = null;
    try {
      const value = await request(path, body);
      if (current === attempt.current) done(value);
    } catch (error) {
      failure = { text: errorText(error), alert: true };
    }
    if (current !== attempt.current) return;
    if (failure) setNotice(failure);
    busyRef.current = false;
    setBusy(false);
    void refresh();
  };

  const adding = flow?.kind === 'pair' && flow.origin === '';
  const flowOrigin = adding ? parseOrigin(originInput) : (flow?.origin ?? null);

  const submit = () => {
    if (!flow || !consent || !flowOrigin) return;
    const origin = flowOrigin;
    if (flow.kind === 'enable') {
      void perform('/api/hubs/enable', { origin, consent: true }, () => {
        setFlow(null);
        setNotice({ text: `Sync started for ${origin}.`, alert: false });
      });
      return;
    }
    void perform('/api/hubs/pairings', { origin, consent: true }, (value) => {
      const { id, pairing: info } = (value ?? {}) as PairingStart;
      if (typeof id !== 'string' || typeof info?.userCode !== 'string')
        throw new Error('Invalid pairing response.');
      const url = approvalUrl(info.verificationUriComplete || info.verificationUri, origin);
      if (!url) throw new Error('Invalid Hub approval link.');
      setPairing({ id, code: info.userCode, url, pending: true });
      setNotice({ text: 'Waiting for approval in Hub. Pairing does not start sync.', alert: false });
    });
  };
  const poll = () => {
    if (!pairing) return;
    void perform(`/api/hubs/pairings/${pairing.id}/poll`, {}, (value) => {
      const state = String((value as { state?: unknown } | null)?.state);
      if (!Object.hasOwn(pairingResults, state)) throw new Error('Unknown pairing status.');
      setPairing({ ...pairing, pending: state === 'pending' || state === 'retrying' });
      setNotice(pairingResults[state]);
    });
  };
  const control = (action: 'disable' | 'retry', origin: string) =>
    void perform(`/api/hubs/${action}`, { origin }, () =>
      setNotice({
        text: action === 'disable' ? `Sync disabled for ${origin}.` : `Retry requested for ${origin}.`,
        alert: false,
      }),
    );

  const unavailable = busy || !hubs || loadError != null;
  return (
    <div className="border-coslash-line bg-coslash-surface flex min-w-0 flex-col gap-3 rounded-xl border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-semibold">Hub connections</span>
        <Button
          type="button"
          variant="outline"
          onClick={() => showFlow({ kind: 'pair', origin: '' })}
          disabled={unavailable || !hubs?.some((hub) => hub.primary)}
        >
          Add Hub
        </Button>
      </div>
      {loadError && (
        <div role="alert" className="text-sm">
          Could not load Hub connections: {loadError}{' '}
          <Button type="button" variant="outline" onClick={() => void refresh()}>
            Retry loading
          </Button>
        </div>
      )}
      {!hubs && !loadError && <p className="text-coslash-muted text-sm">Loading Hub connections…</p>}
      {hubs?.length === 0 && (
        <p className="text-coslash-muted text-sm">Pair a primary Hub before adding another.</p>
      )}
      {hubs?.map((hub) => (
        <div key={hub.origin} className="border-coslash-line flex min-w-0 flex-col gap-2 border-t pt-3">
          <div className="min-w-0 text-sm">
            <span className="font-semibold break-all">{hub.origin}</span> {hub.primary && '(Primary)'}
            <div>{hub.failure ? 'Connection issue' : (statusLabels[hub.state] ?? 'Unknown status')}</div>
            {hub.failure && <div role="alert">{hub.failure}</div>}
          </div>
          {!hub.primary && (
            <div className="flex flex-wrap gap-2">
              {hub.enabled && (
                <HubAction
                  label="Retry"
                  onClick={() => control('retry', hub.origin)}
                  origin={hub.origin}
                  disabled={unavailable}
                />
              )}
              {hub.enabled && (
                <HubAction
                  label="Disable"
                  onClick={() => control('disable', hub.origin)}
                  origin={hub.origin}
                  disabled={unavailable}
                />
              )}
              {!hub.enabled && (
                <HubAction
                  label="Enable"
                  onClick={() => showFlow({ kind: 'enable', origin: hub.origin })}
                  origin={hub.origin}
                  disabled={unavailable}
                />
              )}
              {mayNeedPairing.has(hub.state) && (
                <HubAction
                  label="Pair again"
                  onClick={() => showFlow({ kind: 'pair', origin: hub.origin })}
                  origin={hub.origin}
                  disabled={unavailable}
                />
              )}
            </div>
          )}
        </div>
      ))}
      {notice && (
        <p role={notice.alert ? 'alert' : 'status'} className="text-sm">
          {notice.text}
        </p>
      )}
      {flow && (
        <div className="border-coslash-line bg-coslash-soft flex min-w-0 flex-col gap-3 rounded-lg border p-3">
          <div className="text-sm font-semibold">
            {adding
              ? 'Add a Hub'
              : flow.kind === 'pair'
                ? `Pair ${flow.origin} again`
                : `Enable sync for ${flow.origin}`}
          </div>
          {adding && !pairing && (
            <label className="flex flex-col gap-1 text-sm">
              Hub origin
              <input
                aria-label="Hub origin"
                value={originInput}
                onChange={(event) => {
                  setOriginInput(event.target.value);
                  setConsent(false);
                }}
                placeholder="https://hub.example"
                className="border-coslash-line bg-coslash-surface min-w-0 rounded border p-2"
              />
            </label>
          )}
          <p className="text-sm">
            {flow.kind === 'enable' ? enableDisclosure : pairDisclosure}
            {flow.kind === 'pair' && !adding && ` ${pairAgainDisclosure}`}
          </p>
          {pairing ? (
            <>
              <p className="text-sm">
                Approval code: <strong>{pairing.code}</strong>
              </p>
              <a
                href={pairing.url}
                target="_blank"
                rel="noreferrer"
                className="text-coslash-accent break-all underline"
              >
                Approve in Hub
              </a>
              <div className="flex flex-wrap gap-2">
                {pairing.pending && (
                  <Button type="button" disabled={busy} onClick={poll}>
                    Check approval
                  </Button>
                )}
                <Button type="button" variant="outline" onClick={() => showFlow(null)}>
                  Close
                </Button>
              </div>
              {pairing.pending && (
                <p className="text-coslash-muted text-xs">
                  Closing does not cancel the pending request. It expires and cannot start sync on its own.
                </p>
              )}
            </>
          ) : (
            <>
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={consent}
                  disabled={!flowOrigin}
                  onChange={(event) => setConsent(event.target.checked)}
                />
                I agree for {flowOrigin ?? 'the entered Hub'}.
              </label>
              {adding && originInput.trim() !== '' && !flowOrigin && (
                <p role="alert" className="text-sm">
                  Enter an HTTPS Hub origin without a path, credentials or query.
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                <Button type="button" disabled={unavailable || !consent} onClick={submit}>
                  {flow.kind === 'enable' ? 'Enable sync' : 'Start pairing'}
                </Button>
                <Button type="button" variant="outline" onClick={() => showFlow(null)}>
                  Cancel
                </Button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
