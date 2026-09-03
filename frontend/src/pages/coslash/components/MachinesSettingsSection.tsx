import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  remoteSetupProgress,
  remoteStatus,
  setupRemoteHelper,
  testRemoteAlias,
  type SetupProgress,
} from '@/pages/coslash/lib/remote-api';
import type { RemoteHostSettings } from '@/pages/coslash/lib/settings';

type SetupStage = 'idle' | 'testing' | 'saving' | 'installing' | 'ready' | 'error' | 'removing';

function testResultCopy(machine: MachineFact) {
  return machine.state === 'ok'
    ? 'Connected · SSH/SFTP is ready'
    : `${machine.label} · ${machine.error ?? 'Could not connect over SSH'}`;
}

function setupProgressCopy(progress: SetupProgress) {
  switch (progress) {
    case 'checking':
      return 'Checking SSH…';
    case 'preparing':
      return 'Preparing connector…';
    case 'uploading':
      return 'Uploading connector…';
    case 'verifying':
      return 'Verifying connector…';
    default:
      return 'Installing connector…';
  }
}

function connectorFailureCopy(machine: MachineFact | null) {
  return machine?.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

export function MachinesSettingsSection({
  remote,
  onAddHost,
  onRemoveHost,
  onConnectionVerified,
  onBusyChange,
}: {
  remote: RemoteHostSettings | null | undefined;
  onAddHost: (sshAlias: string) => Promise<boolean>;
  onRemoveHost: () => Promise<boolean>;
  onConnectionVerified?: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [alias, setAlias] = useState('');
  const [stage, setStage] = useState<SetupStage>('idle');
  const [message, setMessage] = useState<string | null>(null);
  const [machine, setMachine] = useState<MachineFact | null>(null);
  const busy = stage === 'testing' || stage === 'saving' || stage === 'installing' || stage === 'removing';
  const setupFailed =
    stage === 'error' || (machine?.helper?.compatible === false && machine.helper.reason != null);

  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);
  useEffect(() => () => onBusyChange(false), [onBusyChange]);

  useEffect(() => {
    if (!remote?.id) {
      setMachine(null);
      return;
    }
    let cancelled = false;
    void remoteStatus()
      .then((status) => {
        if (!cancelled) setMachine(status);
      })
      .catch(() => {
        if (!cancelled) setMachine(null);
      });
    return () => {
      cancelled = true;
    };
  }, [remote?.id]);

  const installConnector = async (sshAlias: string) => {
    setStage('installing');
    setMessage('Installing connector…');
    let stopped = false;
    const pollProgress = () => {
      void remoteSetupProgress()
        .then((progress) => {
          if (!stopped) setMessage(setupProgressCopy(progress));
        })
        .catch(() => {});
    };
    pollProgress();
    const timer = setInterval(pollProgress, 500);
    try {
      const setup = await setupRemoteHelper(sshAlias, 'install');
      setMachine(setup.machine);
      if (setup.error != null) {
        setStage('error');
        setMessage(`Setup failed: ${setup.error}. Check SSH access and retry.`);
        return;
      }
      setStage('ready');
      setMessage('Connector installed and verified. SSH monitoring is active.');
      onConnectionVerified?.();
    } finally {
      stopped = true;
      clearInterval(timer);
    }
  };

  const addHost = async () => {
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
        setStage('error');
        const hint =
          test.reason === 'connection_failed' ||
          test.reason === 'authentication_failed' ||
          test.reason === 'host_key_failed'
            ? ` Run ssh ${sshAlias} once in Terminal, complete any prompt, then try again.`
            : '';
        setMessage(`${testResultCopy(test)}.${hint}`);
        return;
      }
      setStage('saving');
      setMessage('Connection succeeded. Adding SSH monitoring…');
      if (!(await onAddHost(sshAlias))) {
        setStage('error');
        setMessage('Could not add this SSH host.');
        return;
      }
      await installConnector(sshAlias);
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Could not add this SSH host.');
    }
  };

  const retryConnectorSetup = async () => {
    if (remote == null) return;
    try {
      await installConnector(remote.sshAlias);
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Setup failed. Check SSH access and retry.');
    }
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
              <div className="text-sm font-semibold">{remote.sshAlias} · SSH</div>
              <div className="text-muted-foreground text-xs">
                {setupFailed
                  ? 'Setup failed'
                  : machine?.refreshing || machine?.state === 'connecting'
                    ? 'Checking'
                    : machine?.state === 'stale' || machine?.state === 'error'
                      ? 'Offline'
                      : machine?.state === 'ok' && machine.sessionCount === 0
                        ? 'Connected · no recent agent sessions found'
                        : 'Connected'}
              </div>
            </div>
            <div className="flex shrink-0 gap-2">
              {setupFailed && (
                <Button type="button" size="sm" disabled={busy} onClick={() => void retryConnectorSetup()}>
                  Retry setup
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy}
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
                Connect through an alias already configured in your SSH config.
              </div>
            </div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                aria-label="SSH alias"
                value={alias}
                disabled={busy}
                onChange={(event) => setAlias(event.target.value)}
                placeholder="agent-box"
                className="border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 min-w-0 flex-1 rounded-lg border px-2.5 font-mono text-xs outline-none focus-visible:ring-3 disabled:opacity-50"
              />
              <Button type="button" size="sm" disabled={busy} onClick={() => void addHost()}>
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
            role={stage === 'error' ? 'alert' : 'status'}
            className={cn('border-t px-4 py-3 text-xs', {
              'bg-muted text-muted-foreground': busy,
              'bg-success-bg text-success-fg': stage === 'ready',
              'bg-destructive/10 text-destructive': stage === 'error',
            })}
          >
            <span className={cn({ 'animate-pulse': stage === 'installing' })}>{message}</span>
          </div>
        )}
        {message == null && setupFailed && (
          <div role="alert" className="bg-destructive/10 text-destructive border-t px-4 py-3 text-xs">
            Setup failed: {connectorFailureCopy(machine)}. Retry setup to verify the connector.
          </div>
        )}
      </div>
    </div>
  );
}
