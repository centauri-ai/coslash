import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import {
  MACHINE_TONE_DOT,
  machineRetryable,
  machineStatusText,
  machineTone,
} from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  cancelRemoteAuthentication,
  remoteAuthenticationStatus,
  remoteStatus,
  retryRemoteRefreshAndWait,
  setupRemoteHelper,
  startRemoteAuthentication,
  testRemoteAlias,
  type RemoteAuthAttempt,
} from '@/pages/coslash/lib/remote-api';
import type { RemoteHostSettings } from '@/pages/coslash/lib/settings';

type SetupStage =
  | 'idle'
  | 'testing'
  | 'authentication_required'
  | 'authenticating'
  | 'saving'
  | 'consent'
  | 'installing'
  | 'ready'
  | 'connector_error'
  | 'error'
  | 'removing';

type AuthenticationRun = {
  cancelled: boolean;
  controller: AbortController;
  attemptID: string | null;
};

function waitForAuthPoll(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', abort);
      resolve();
    }, 600);
    const abort = () => {
      window.clearTimeout(timer);
      reject(signal.reason);
    };
    signal.addEventListener('abort', abort, { once: true });
  });
}

function testResultCopy(machine: MachineFact) {
  return machine.state === 'ok'
    ? 'Connected · SSH/SFTP is ready'
    : `${machine.label} · ${machine.error ?? 'Could not connect over SSH'}`;
}

function connectorFailureCopy(machine: MachineFact | null) {
  return machine?.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

function cancellationFailureCopy(error: unknown) {
  const reason = error instanceof Error ? `: ${error.message}` : '';
  return `Could not cancel authentication${reason}. The Terminal attempt is still active.`;
}

export function MachinesSettingsSection({
  remote,
  onAddHost,
  onRemoveHost,
  onConnectionVerified,
  onBusyChange,
  onRetry,
  retrying = false,
}: {
  remote: RemoteHostSettings | null | undefined;
  onAddHost: (sshAlias: string) => Promise<boolean>;
  onRemoveHost: () => Promise<boolean>;
  onConnectionVerified?: () => void;
  onBusyChange: (busy: boolean) => void;
  onRetry?: () => Promise<MachineFact | undefined>;
  retrying?: boolean;
}) {
  const [alias, setAlias] = useState('');
  const [stage, setStage] = useState<SetupStage>('idle');
  const [message, setMessage] = useState<string | null>(null);
  const [machineResult, setMachineResult] = useState<{ sshAlias: string; machine: MachineFact } | null>(null);
  const [authAlias, setAuthAlias] = useState<string | null>(null);
  const [authAttemptID, setAuthAttemptID] = useState<string | null>(null);
  const [authReconnect, setAuthReconnect] = useState(false);
  const authenticationRun = useRef<AuthenticationRun | null>(null);
  const currentMachine =
    machineResult != null && machineResult.sshAlias === remote?.sshAlias ? machineResult.machine : null;
  const busy = stage === 'testing' || stage === 'saving' || stage === 'installing' || stage === 'removing';
  // Authentication must not use the dialog-wide busy state because that disables Cancel.
  const authenticationPending = stage === 'authentication_required' || stage === 'authenticating';
  const setupActionsLocked = busy || authenticationPending;
  const messageIsError = stage === 'error' || stage === 'connector_error';
  const canAuthenticate = currentMachine?.actionRequired === 'authenticate' && !authenticationPending;
  const setupFailed =
    stage === 'connector_error' ||
    (stage === 'idle' &&
      currentMachine?.helper?.compatible === false &&
      currentMachine.helper.reason != null);
  const showRetry =
    !setupFailed &&
    !authenticationPending &&
    onRetry != null &&
    currentMachine != null &&
    machineRetryable(currentMachine);

  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);
  useEffect(() => () => onBusyChange(false), [onBusyChange]);
  useEffect(
    () => () => {
      const run = authenticationRun.current;
      if (run == null) return;
      run.cancelled = true;
      run.controller.abort();
      if (run.attemptID != null) void cancelRemoteAuthentication(run.attemptID).catch(() => undefined);
    },
    [],
  );

  useEffect(() => {
    if (!remote?.sshAlias) return;
    const sshAlias = remote.sshAlias;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = () => {
      void remoteStatus()
        .then((status) => {
          if (cancelled) return;
          setMachineResult({ sshAlias, machine: status });
          if (
            status.refreshing ||
            status.state === 'connecting' ||
            status.reason === 'initial_refresh' ||
            status.helperProbeState === 'probing'
          ) {
            timer = setTimeout(poll, 400);
          }
        })
        .catch(() => {
          if (!cancelled) setMachineResult(null);
        });
    };
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [remote?.sshAlias]);

  const installConnector = async (sshAlias: string) => {
    setStage('installing');
    setMessage('Installing connector…');
    try {
      const setup = await setupRemoteHelper(sshAlias, 'install');
      setMachineResult({ sshAlias, machine: setup.machine });
      if (setup.error != null) {
        setStage('connector_error');
        setMessage(`Setup failed: ${setup.error}. Check SSH access and retry.`);
        return;
      }
      setStage('ready');
      setMessage('Connector installed and verified. SSH monitoring is active.');
      onConnectionVerified?.();
    } catch (error: unknown) {
      setStage('connector_error');
      setMessage(error instanceof Error ? error.message : 'Setup failed. Check SSH access and retry.');
    }
  };

  const saveVerifiedHost = async (sshAlias: string) => {
    setStage('saving');
    setMessage('Connection succeeded. Adding SSH monitoring…');
    if (!(await onAddHost(sshAlias))) {
      setStage('error');
      setMessage('Could not add this SSH host.');
      return;
    }
    setStage('consent');
    setMessage('Install a private connector on this host, or skip installation.');
  };

  const finishReconnect = async (run: AuthenticationRun) => {
    const refreshed = await retryRemoteRefreshAndWait(run.controller.signal);
    if (authenticationRun.current !== run || run.cancelled || remote?.sshAlias == null) return;
    setMachineResult({ sshAlias: remote.sshAlias, machine: refreshed });
    setStage(refreshed.state === 'ok' ? 'ready' : 'error');
    setMessage(refreshed.state === 'ok' ? 'SSH monitoring reconnected.' : `${testResultCopy(refreshed)}.`);
    if (refreshed.state === 'ok') onConnectionVerified?.();
  };

  const waitForAuthentication = async (
    sshAlias: string,
    id: string,
    reconnect: boolean,
    run: AuthenticationRun,
  ) => {
    try {
      for (;;) {
        await waitForAuthPoll(run.controller.signal);
        const attempt = await remoteAuthenticationStatus(id, run.controller.signal);
        if (attempt.state === 'waiting') continue;
        if (authenticationRun.current !== run || run.cancelled) return;
        setAuthAttemptID(null);
        run.attemptID = null;
        if (attempt.state !== 'ready') {
          setStage('error');
          setMessage(
            attempt.state === 'timed_out'
              ? 'Authentication timed out. Try again.'
              : attempt.state === 'failed'
                ? 'Authentication did not complete. Check the Terminal prompt and try again.'
                : 'Authentication was cancelled. Try again.',
          );
          return;
        }
        if (reconnect) {
          await finishReconnect(run);
          return;
        }
        const test = await testRemoteAlias(sshAlias);
        if (authenticationRun.current !== run || run.cancelled) return;
        if (test.state !== 'ok') {
          setStage('error');
          setMessage(`${testResultCopy(test)}.`);
          return;
        }
        await saveVerifiedHost(sshAlias);
        return;
      }
    } catch (error: unknown) {
      if (run.cancelled || run.controller.signal.aborted || authenticationRun.current !== run) return;
      // A failed status request must not leave an interactive server attempt
      // consuming its full TTL with no client observing it.
      await cancelRemoteAuthentication(id).catch(() => undefined);
      if (authenticationRun.current !== run || run.cancelled) return;
      setAuthAttemptID(null);
      setStage('error');
      setMessage(
        error instanceof Error ? error.message : 'Authentication status could not be checked. Try again.',
      );
    }
  };

  const authenticateInTerminal = async () => {
    if (authAlias == null || authenticationRun.current != null) return;
    const run: AuthenticationRun = { cancelled: false, controller: new AbortController(), attemptID: null };
    authenticationRun.current = run;
    setStage('authenticating');
    setMessage('Checking SSH connection…');
    try {
      const attempt = await startRemoteAuthentication(authAlias, run.controller.signal);
      if (run.cancelled || authenticationRun.current !== run) {
        if (attempt.state !== 'not_required') {
          await cancelRemoteAuthentication(attempt.id).catch(() => undefined);
        }
        return;
      }
      if (attempt.state === 'not_required') {
        if (authReconnect) {
          await finishReconnect(run);
        } else {
          await saveVerifiedHost(authAlias);
        }
        return;
      }
      run.attemptID = attempt.id;
      setAuthAttemptID(attempt.id);
      setMessage('Terminal opened. Complete the SSH prompt there; setup will continue automatically.');
      await waitForAuthentication(authAlias, attempt.id, authReconnect, run);
    } catch (error: unknown) {
      if (run.cancelled || authenticationRun.current !== run) return;
      setAuthAttemptID(null);
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Could not start terminal authentication.');
    } finally {
      if (authenticationRun.current === run) authenticationRun.current = null;
    }
  };

  const cancelAuthentication = async () => {
    const run = authenticationRun.current;
    if (run != null) {
      if (run.attemptID == null) return;
      let attempt: RemoteAuthAttempt;
      try {
        attempt = await cancelRemoteAuthentication(run.attemptID);
      } catch (error: unknown) {
        if (authenticationRun.current !== run) return;
        setMessage(cancellationFailureCopy(error));
        return;
      }
      if (authenticationRun.current !== run) return;
      if (attempt.state === 'ready') {
        run.attemptID = null;
        setAuthAttemptID(null);
        return;
      }
      if (attempt.state !== 'cancelled') return;
      run.cancelled = true;
      run.controller.abort();
    } else if (authAttemptID != null) {
      try {
        const attempt = await cancelRemoteAuthentication(authAttemptID);
        if (attempt.state !== 'cancelled') return;
      } catch (error: unknown) {
        setMessage(cancellationFailureCopy(error));
        return;
      }
    }
    setAuthAttemptID(null);
    setStage('error');
    setMessage('Authentication was cancelled. Try again.');
  };

  const reconnect = () => {
    if (remote == null) return;
    setAuthAlias(remote.sshAlias);
    setAuthReconnect(true);
    setStage('authentication_required');
    setMessage('Authentication required. Authenticate in Terminal to reconnect monitoring.');
  };

  const addHost = async () => {
    if (authenticationRun.current != null) return;
    const sshAlias = alias.trim();
    if (!sshAlias) {
      setStage('error');
      setMessage('Enter an SSH alias first.');
      return;
    }
    setMessage('Checking SSH connection…');
    setStage('testing');
    try {
      const test = await testRemoteAlias(sshAlias);
      if (test.state !== 'ok') {
        if (test.actionRequired === 'authenticate') {
          setAuthAlias(sshAlias);
          setAuthReconnect(false);
          setStage('authentication_required');
          setMessage(`${testResultCopy(test)}. Authenticate in Terminal to continue.`);
          return;
        }
        setStage('error');
        const hint =
          test.actionRequired === 'verify_host_key'
            ? ` Verify the identity and host key for ${sshAlias} in Terminal before trying again.`
            : test.reason === 'connection_failed' || test.reason === 'host_key_failed'
              ? ` Run ssh ${sshAlias} once in Terminal, complete any prompt, then try again.`
              : '';
        setMessage(`${testResultCopy(test)}.${hint}`);
        return;
      }
      await saveVerifiedHost(sshAlias);
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Could not add this SSH host.');
    }
  };

  const retryConnectorSetup = () => {
    if (remote == null) return;
    setStage('consent');
    setMessage('Install a private connector on this host, or skip installation.');
  };

  const retryConnection = async () => {
    if (remote == null || onRetry == null) return;
    const sshAlias = remote.sshAlias;
    const machine = await onRetry();
    if (machine != null) setMachineResult({ sshAlias, machine });
  };

  const skipInstallation = () => {
    setStage('ready');
    setMessage('Connector installation skipped. SSH monitoring is active.');
    onConnectionVerified?.();
  };

  const removeHost = async () => {
    setStage('removing');
    setMessage('Removing SSH monitoring…');
    try {
      if (!(await onRemoveHost())) {
        setStage('error');
        setMessage('Could not remove this SSH host.');
        return;
      }
      setStage('idle');
      setMessage(null);
      setAlias('');
      setMachineResult(null);
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Could not remove this SSH host.');
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 px-0.5">
        <span className="text-muted-foreground text-[11px] font-semibold tracking-widest uppercase">
          SSH monitoring
        </span>
        <span className="bg-border h-px flex-1" />
      </div>
      <div className="border-border bg-card overflow-hidden rounded-xl border">
        {remote ? (
          <div className="flex items-center justify-between gap-4 p-4">
            <div className="flex min-w-0 flex-col gap-1">
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'coslash-shell size-[7px] shrink-0 rounded-full',
                    MACHINE_TONE_DOT[currentMachine == null ? 'checking' : machineTone(currentMachine)],
                  )}
                  aria-hidden="true"
                />
                <span className="text-sm font-semibold">{remote.sshAlias} · SSH</span>
              </div>
              <div className="text-muted-foreground text-xs">
                {setupFailed
                  ? 'Setup failed'
                  : currentMachine == null
                    ? 'Checking the connection.'
                    : machineStatusText(currentMachine)}
              </div>
              {retrying ? (
                <div className="text-muted-foreground text-xs">Retrying…</div>
              ) : (
                showRetry && (
                  <button
                    type="button"
                    className="cursor-pointer text-left text-xs font-semibold underline underline-offset-2"
                    onClick={() => void retryConnection()}
                  >
                    Retry the connection
                  </button>
                )
              )}
            </div>
            <div className="flex shrink-0 gap-2">
              {stage === 'consent' ? (
                <>
                  <Button type="button" variant="outline" size="sm" onClick={skipInstallation}>
                    Skip installation
                  </Button>
                  <Button type="button" size="sm" onClick={() => void installConnector(remote.sshAlias)}>
                    Install connector
                  </Button>
                </>
              ) : setupFailed ? (
                <Button type="button" size="sm" disabled={busy} onClick={retryConnectorSetup}>
                  Retry setup
                </Button>
              ) : null}
              {canAuthenticate && (
                <Button type="button" size="sm" disabled={busy} onClick={reconnect}>
                  Authenticate in Terminal
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={setupActionsLocked}
                onClick={() => void removeHost()}
              >
                {stage === 'removing' ? 'Removing…' : 'Remove'}
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-3 p-4">
            <div>
              <div className="text-sm font-semibold">Add remote host</div>
              <div className="text-muted-foreground mt-1 text-xs text-pretty">
                Connect through an SSH alias or a simple user@host destination.
              </div>
            </div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                aria-label="SSH alias"
                value={alias}
                disabled={setupActionsLocked}
                onChange={(event) => setAlias(event.target.value)}
                placeholder="agent-box"
                className="border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 min-w-0 flex-1 rounded-lg border px-2.5 font-mono text-xs outline-none focus-visible:ring-3 disabled:opacity-50"
              />
              <Button type="button" size="sm" disabled={setupActionsLocked} onClick={() => void addHost()}>
                {stage === 'testing'
                  ? 'Checking SSH…'
                  : stage === 'saving'
                    ? 'Adding…'
                    : stage === 'installing'
                      ? 'Installing connector…'
                      : 'Add remote host'}
              </Button>
            </div>
          </div>
        )}
        {message != null && (
          <div
            role={messageIsError ? 'alert' : 'status'}
            className={cn('flex items-center gap-3 border-t px-4 py-3 text-xs', {
              'bg-muted text-muted-foreground': busy || stage === 'consent' || authenticationPending,
              'bg-success-bg text-success-fg': stage === 'ready',
              'bg-destructive/10 text-destructive': messageIsError,
            })}
          >
            <span className={cn({ 'animate-pulse': stage === 'installing' || stage === 'authenticating' })}>
              {message}
            </span>
            {stage === 'authentication_required' && (
              <Button type="button" size="sm" onClick={() => void authenticateInTerminal()}>
                Authenticate in Terminal
              </Button>
            )}
            {stage === 'authenticating' && authAttemptID != null && (
              <Button type="button" variant="outline" size="sm" onClick={() => void cancelAuthentication()}>
                Cancel
              </Button>
            )}
          </div>
        )}
        {message == null && setupFailed && (
          <div role="alert" className="bg-destructive/10 text-destructive border-t px-4 py-3 text-xs">
            Setup failed: {connectorFailureCopy(currentMachine)}. Retry setup to verify the connector.
          </div>
        )}
        {message == null && currentMachine?.actionRequired != null && !setupFailed && (
          <div role="alert" className="bg-muted text-destructive border-t px-4 py-3 text-xs">
            {currentMachine.actionRequired === 'verify_host_key'
              ? `SSH host identity changed. Verify ${remote?.sshAlias}'s host key in Terminal before reconnecting.`
              : `Run ssh ${remote?.sshAlias} in Terminal and complete the authentication prompt.`}
          </div>
        )}
      </div>
    </div>
  );
}
