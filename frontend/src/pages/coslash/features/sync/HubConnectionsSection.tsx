import { useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { apiFetch } from '@/pages/coslash/lib/api';

type Hub = {
  origin: string;
  primary: boolean;
  enabled: boolean;
  state: string;
  quarantined: number;
  failure?: string;
};
type Flow = { kind: 'add' | 'recover' | 'enable'; origin: string };
type Pairing = { id: string; code: string; url: string; state: string };
type HubAction = 'retry' | 'disable' | 'enable' | 'recover';
const actionLabels: Record<HubAction, string> = {
  retry: 'Retry',
  disable: 'Disable',
  enable: 'Enable',
  recover: 'Recover pairing',
};

function hubActions(hub: Hub): HubAction[] {
  const actions: HubAction[] = hub.enabled ? ['retry', 'disable'] : ['enable'];
  if (!hub.enabled || ['disconnected', 'not_connected', 'start_failed'].includes(hub.state))
    actions.push('recover');
  return actions;
}

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
const resultLabels: Record<string, string> = {
  pending: 'Waiting for approval in Hub.',
  retrying: 'Hub is not ready. Check approval again.',
  declined: 'Pairing was declined in Hub.',
  expired: 'Pairing expired. Start again.',
  cancelled: 'Pairing was cancelled.',
  credential_store_failed: 'The device credential could not be saved.',
  failed: 'Pairing could not be completed. Check the Hub status and try again.',
  paired_disabled: 'Paired. Sync is disabled until you separately enable it.',
  recovered_disabled: 'Recovered. Sync is disabled until you separately enable it.',
  recovered_new_binding_disabled:
    'Connected with a new credential. Old pending data stays on this computer; sync is disabled.',
  binding_mismatch:
    'This connection does not match the previous one and may belong to a different account. Recover again and choose to keep old pending data on this computer if you want to connect it.',
  binding_unknown: 'The previous account could not be verified. Pending data remains on this computer.',
  recovery_status_unknown: 'Recovery is uncertain. Check Hub status before trying again.',
  quarantine_limit:
    'No more old pending data can be retained for this Hub. Existing data remains on this computer.',
};

function canonicalOrigin(raw: string): string | null {
  try {
    const url = new URL(raw.trim());
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.pathname !== '/' ||
      url.search ||
      url.hash
    )
      return null;
    return url.origin;
  } catch {
    return null;
  }
}

async function request(path: string, init: RequestInit = {}): Promise<unknown> {
  const response = await apiFetch(path, init);
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(message || `Request failed (${response.status}).`);
  }
  return response.json() as Promise<unknown>;
}

function decodeHubs(value: unknown): Hub[] {
  if (!Array.isArray(value)) throw new Error('Invalid Hub status.');
  return value.map((item: unknown) => {
    if (item == null || typeof item !== 'object') throw new Error('Invalid Hub status.');
    const hub = item as Record<string, unknown>;
    if (
      typeof hub.origin !== 'string' ||
      typeof hub.primary !== 'boolean' ||
      typeof hub.enabled !== 'boolean' ||
      typeof hub.state !== 'string' ||
      typeof hub.quarantined !== 'number' ||
      (hub.failure !== undefined && typeof hub.failure !== 'string')
    )
      throw new Error('Invalid Hub status.');
    return {
      origin: hub.origin,
      primary: hub.primary,
      enabled: hub.enabled,
      state: hub.state,
      quarantined: hub.quarantined,
      failure: hub.failure as string | undefined,
    };
  });
}

function post(body: unknown, signal: AbortSignal): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  };
}

export function HubConnectionsSection() {
  const [hubs, setHubs] = useState<Hub[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [flow, setFlow] = useState<Flow | null>(null);
  const [originInput, setOriginInput] = useState('');
  const [disclosure, setDisclosure] = useState<string | null>(null);
  const [disclosureRetry, setDisclosureRetry] = useState(0);
  const [consent, setConsent] = useState(false);
  const [newBindingConsent, setNewBindingConsent] = useState(false);
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const operation = useRef<AbortController | null>(null);
  const statusRequest = useRef<AbortController | null>(null);
  const alive = useRef(false);

  const refresh = useCallback(async () => {
    statusRequest.current?.abort();
    const controller = new AbortController();
    statusRequest.current = controller;
    try {
      const next = decodeHubs(await request('/api/hubs/status', { signal: controller.signal }));
      if (alive.current && !controller.signal.aborted) {
        setHubs(next);
        setLoadError(null);
      }
    } catch (e) {
      if (alive.current && !controller.signal.aborted)
        setLoadError(e instanceof Error ? e.message : 'Could not load Hub status.');
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    void Promise.resolve().then(() => {
      if (alive.current) void refresh();
    });
    const interval = window.setInterval(() => {
      if (document.visibilityState !== 'hidden') void refresh();
    }, 10_000);
    return () => {
      alive.current = false;
      operation.current?.abort();
      statusRequest.current?.abort();
      window.clearInterval(interval);
    };
  }, [refresh]);

  useEffect(() => {
    if (!flow) return;
    const controller = new AbortController();
    const path = flow.kind === 'add' ? 'pairing' : flow.kind === 'recover' ? 'recovery' : 'enable';
    void request(`/api/hubs/${path}-disclosure`, { signal: controller.signal })
      .then((value) => {
        if (controller.signal.aborted || !alive.current) return;
        if (
          value == null ||
          typeof value !== 'object' ||
          typeof (value as { disclosure?: unknown }).disclosure !== 'string'
        )
          throw new Error('Invalid disclosure.');
        setDisclosure((value as { disclosure: string }).disclosure);
      })
      .catch((e: unknown) => {
        if (!controller.signal.aborted && alive.current)
          setError(e instanceof Error ? e.message : 'Could not load disclosure.');
      });
    return () => controller.abort();
  }, [flow, disclosureRetry]);

  const changeFlow = (next: Flow | null) => {
    operation.current?.abort();
    setFlow(next);
    setDisclosure(null);
    setOriginInput(next?.origin ?? '');
    setConsent(false);
    setNewBindingConsent(false);
    setPairing(null);
    setMessage(null);
    setError(null);
    setBusy(false);
    busyRef.current = false;
  };
  const openFlow = (kind: Flow['kind'], origin = '') => changeFlow({ kind, origin });
  const closeFlow = () => changeFlow(null);
  const perform = async (path: string, body: unknown, done: (value: unknown) => void) => {
    if (busyRef.current) return;
    const controller = new AbortController();
    operation.current = controller;
    busyRef.current = true;
    setBusy(true);
    setError(null);
    try {
      const value = await request(path, post(body, controller.signal));
      if (!alive.current || controller.signal.aborted) return;
      done(value);
      void refresh();
    } catch (e) {
      if (!alive.current || controller.signal.aborted) return;
      setError(e instanceof Error ? e.message : 'Hub request failed.');
      void refresh();
    } finally {
      if (alive.current && !controller.signal.aborted) {
        busyRef.current = false;
        setBusy(false);
      }
    }
  };
  const submitFlow = () => {
    if (!flow || !consent || !disclosure || !hubs || loadError) return;
    const origin = canonicalOrigin(flow.kind === 'add' ? originInput : flow.origin);
    if (!origin) {
      setError('Enter an HTTPS Hub origin without a path, credentials or query.');
      return;
    }
    if (flow.kind === 'enable') {
      void perform('/api/hubs/enable', { origin, consent: true }, () => {
        setMessage(`Sync started for ${origin}.`);
        setFlow(null);
      });
      return;
    }
    const path = flow.kind === 'recover' ? '/api/hubs/re-pairings' : '/api/hubs/pairings';
    void perform(
      path,
      { origin, consent: true, ...(flow.kind === 'recover' ? { newBindingConsent } : {}) },
      (value) => {
        if (value == null || typeof value !== 'object') throw new Error('Invalid pairing response.');
        const data = value as {
          id?: unknown;
          pairing?: { userCode?: unknown; verificationUri?: unknown; verificationUriComplete?: unknown };
        };
        const info = data.pairing;
        if (
          typeof data.id !== 'string' ||
          !/^[0-9a-f]{32}$/.test(data.id) ||
          !info ||
          typeof info.userCode !== 'string' ||
          typeof info.verificationUri !== 'string'
        )
          throw new Error('Invalid pairing response.');
        const url =
          typeof info.verificationUriComplete === 'string' && info.verificationUriComplete
            ? info.verificationUriComplete
            : info.verificationUri;
        let verified: URL;
        try {
          verified = new URL(url);
        } catch {
          throw new Error('Invalid Hub approval link.');
        }
        if (
          verified.protocol !== 'https:' ||
          verified.origin !== origin ||
          verified.username ||
          verified.password
        )
          throw new Error('Invalid Hub approval link.');
        setPairing({ id: data.id, code: info.userCode, url: verified.href, state: 'pending' });
        setMessage('Waiting for approval in Hub. Pairing does not start sync.');
      },
    );
  };
  const poll = () => {
    if (!pairing) return;
    void perform(`/api/hubs/pairings/${pairing.id}/poll`, {}, (value) => {
      if (
        value == null ||
        typeof value !== 'object' ||
        typeof (value as { state?: unknown }).state !== 'string'
      )
        throw new Error('Invalid pairing status.');
      const state = (value as { state: string }).state;
      if (!Object.hasOwn(resultLabels, state)) throw new Error('Unknown pairing status.');
      setPairing({ ...pairing, state });
      setMessage(resultLabels[state]);
    });
  };
  const control = (action: 'disable' | 'retry', origin: string) => {
    if (!hubs || loadError) return;
    void perform(`/api/hubs/${action}`, { origin }, () =>
      setMessage(action === 'disable' ? `Sync disabled for ${origin}.` : `Retry requested for ${origin}.`),
    );
  };

  return (
    <div className="border-coslash-line bg-coslash-surface flex min-w-0 flex-col gap-3 rounded-xl border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-semibold">Hub connections</span>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={() => void refresh()} disabled={busy}>
            Refresh
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => openFlow('add')}
            disabled={busy || !hubs?.some((hub) => hub.primary) || loadError != null}
          >
            Add Hub
          </Button>
        </div>
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
            <div>
              {hub.failure ? 'Connection issue' : (statusLabels[hub.state] ?? 'Unknown status')}
              {hub.quarantined > 0 && ` · Old pending data kept on this computer (${hub.quarantined})`}
            </div>
            {hub.failure && <div role="alert">{hub.failure}</div>}
          </div>
          {!hub.primary && (
            <div className="flex flex-wrap gap-2">
              {hubActions(hub).map((action) => (
                <Button
                  key={action}
                  type="button"
                  variant="outline"
                  disabled={busy || loadError != null}
                  aria-label={`${action === 'recover' ? 'Recover' : actionLabels[action]} ${hub.origin}`}
                  onClick={() =>
                    action === 'enable' || action === 'recover'
                      ? openFlow(action, hub.origin)
                      : control(action, hub.origin)
                  }
                >
                  {actionLabels[action]}
                </Button>
              ))}
            </div>
          )}
        </div>
      ))}
      {message && (
        <p
          role={
            pairing &&
            [
              'declined',
              'expired',
              'cancelled',
              'credential_store_failed',
              'failed',
              'binding_mismatch',
              'binding_unknown',
              'recovery_status_unknown',
              'quarantine_limit',
            ].includes(pairing.state)
              ? 'alert'
              : 'status'
          }
          className="text-sm"
        >
          {message}
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm">
          {error}
        </p>
      )}
      {flow && (
        <div className="border-coslash-line bg-coslash-soft flex min-w-0 flex-col gap-3 rounded-lg border p-3">
          <div className="text-sm font-semibold">
            {flow.kind === 'add'
              ? 'Add a Hub'
              : flow.kind === 'recover'
                ? `Recover ${flow.origin}`
                : `Enable sync for ${flow.origin}`}
          </div>
          {flow.kind === 'add' && !pairing && (
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
          {disclosure ? (
            <p className="text-sm">{disclosure}</p>
          ) : (
            <div className="text-coslash-muted text-sm">
              {error ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setError(null);
                    setDisclosureRetry((value) => value + 1);
                  }}
                >
                  Retry disclosure
                </Button>
              ) : (
                'Loading disclosure…'
              )}
            </div>
          )}
          {!pairing && (
            <>
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={consent}
                  disabled={flow.kind === 'add' && canonicalOrigin(originInput) == null}
                  onChange={(event) => setConsent(event.target.checked)}
                />
                I agree for{' '}
                {flow.kind === 'add' ? (canonicalOrigin(originInput) ?? 'the entered Hub') : flow.origin}.
              </label>
              {flow.kind === 'recover' && (
                <label className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={newBindingConsent}
                    onChange={(event) => setNewBindingConsent(event.target.checked)}
                  />
                  Keep the old pending data on this computer and allow a different connection, which may
                  belong to another account.
                </label>
              )}
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  disabled={busy || !consent || !disclosure || loadError != null}
                  onClick={submitFlow}
                >
                  {flow.kind === 'enable'
                    ? 'Enable sync'
                    : flow.kind === 'recover'
                      ? 'Start recovery'
                      : 'Start pairing'}
                </Button>
                <Button type="button" variant="outline" disabled={busy} onClick={closeFlow}>
                  Cancel
                </Button>
              </div>
            </>
          )}
          {pairing && (
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
                {(pairing.state === 'pending' || pairing.state === 'retrying') && (
                  <Button type="button" disabled={busy} onClick={poll}>
                    Check approval
                  </Button>
                )}
                <Button type="button" variant="outline" disabled={busy} onClick={closeFlow}>
                  Close
                </Button>
              </div>
              {(pairing.state === 'pending' || pairing.state === 'retrying') && (
                <p className="text-coslash-muted text-xs">
                  Closing does not cancel the pending request. It expires and cannot start sync on its own.
                </p>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
