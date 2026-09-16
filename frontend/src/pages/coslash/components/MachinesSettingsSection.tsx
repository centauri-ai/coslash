import { useEffect, useState } from 'react';
import { Check, ChevronRight, Circle, LoaderCircle, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { remoteStatus, setupRemoteHelper, testRemoteAlias } from '@/pages/coslash/lib/remote-api';
import { sshTestFailureMessage } from '@/pages/coslash/lib/remote-setup-copy';
import {
  remoteExecutablePathError,
  type RemoteExecutableAgent,
  type RemoteExecutableSettings,
  type RemoteHostSettings,
} from '@/pages/coslash/lib/settings';

type SetupStage = 'idle' | 'testing' | 'saving' | 'consent' | 'installing' | 'ready' | 'error' | 'removing';
type SetupStep = 1 | 2 | 3 | 4;

const completedSetupCopy = [
  'SSH connection verified',
  'Remote host saved',
  'Connector installed and verified',
] as const;
const failedSetupCopy = [
  'SSH verification failed',
  'Could not save remote host',
  'Connector setup failed',
  'Setup failed',
] as const;

function activeSetupCopy(stage: SetupStage, step: SetupStep) {
  if (step === 1) return 'Verifying SSH connection…';
  if (step === 2) return 'Saving remote host…';
  if (step === 3 && stage === 'consent') return 'Connector setup is optional';
  if (step === 3) return 'Installing and verifying connector…';
  return 'Remote host ready';
}

export function SetupProgress({
  stage,
  step,
  message,
}: {
  stage: SetupStage;
  step: SetupStep;
  message: string;
}) {
  const failed = stage === 'error';
  const complete = stage === 'ready';
  const running = stage === 'testing' || stage === 'saving' || stage === 'installing';
  const completedSteps = complete ? [] : completedSetupCopy.slice(0, step - 1);
  const currentCopy = complete
    ? 'Remote host ready'
    : failed
      ? failedSetupCopy[step - 1]
      : activeSetupCopy(stage, step);

  return (
    <div role={failed ? 'alert' : 'status'} className="bg-muted border-t px-4 py-3">
      <div className="flex flex-col gap-1.5 font-mono text-xs">
        {completedSteps.map((copy) => (
          <div key={copy} className="text-success-fg flex items-center gap-2">
            <Check aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.5} />
            <span>{copy}</span>
          </div>
        ))}
        <div
          className={cn('flex items-center gap-2', {
            'text-success-fg': complete,
            'text-destructive': failed,
            'text-foreground': !complete && !failed,
            'animate-pulse': running,
          })}
        >
          {complete ? (
            <Check aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.5} />
          ) : failed ? (
            <X aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.5} />
          ) : running ? (
            <LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin" />
          ) : (
            <Circle aria-hidden="true" className="size-3.5 shrink-0" />
          )}
          <span>{currentCopy}</span>
        </div>
      </div>
      <div
        className={cn('pt-2 text-xs leading-relaxed', {
          'text-destructive': failed,
          'text-muted-foreground': !failed,
        })}
      >
        {message}
      </div>
    </div>
  );
}

function connectorFailureCopy(machine: MachineFact | null) {
  return machine?.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

export function RemoteExecutableField({
  agent,
  path,
  disabled,
  onChange,
  onBlur,
}: {
  agent: RemoteExecutableAgent;
  path: string;
  disabled: boolean;
  onChange: (path: string) => void;
  onBlur: () => void;
}) {
  const name: Record<RemoteExecutableAgent, string> = {
    claude: 'Claude',
    codex: 'Codex',
    opencode: 'OpenCode',
  };
  const error = remoteExecutablePathError(path);
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[13px] font-semibold">{name[agent]}</span>
      <input
        aria-label={`${name[agent]} remote executable`}
        aria-invalid={error != null}
        value={path}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
        placeholder="Automatic discovery"
        className={cn(
          'border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 rounded-lg border px-2.5 font-mono text-xs outline-none focus-visible:ring-3 disabled:opacity-50',
          { 'border-destructive': error != null },
        )}
      />
      {error && <span className="text-destructive text-[11px]">{error}</span>}
    </label>
  );
}

export function MachinesSettingsSection({
  remote,
  onAddHost,
  onRemoveHost,
  onConnectionVerified,
  onBusyChange,
  executables,
  onExecutablesChange,
  onExecutablesCommit,
}: {
  remote: RemoteHostSettings | null | undefined;
  onAddHost: (sshAlias: string) => Promise<boolean>;
  onRemoveHost: () => Promise<boolean>;
  onConnectionVerified?: () => void;
  onBusyChange: (busy: boolean) => void;
  executables: RemoteExecutableSettings | undefined;
  onExecutablesChange: (executables: RemoteExecutableSettings) => void;
  onExecutablesCommit: (executables: RemoteExecutableSettings) => void;
}) {
  const [alias, setAlias] = useState('');
  const [stage, setStage] = useState<SetupStage>('idle');
  const [setupStep, setSetupStep] = useState<SetupStep | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [machine, setMachine] = useState<MachineFact | null>(null);
  const [executablesOpen, setExecutablesOpen] = useState(false);
  const busy = stage === 'testing' || stage === 'saving' || stage === 'installing' || stage === 'removing';
  const setupFailed =
    stage === 'error' ||
    (stage === 'idle' && machine?.helper?.compatible === false && machine.helper.reason != null);

  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);
  useEffect(() => () => onBusyChange(false), [onBusyChange]);

  useEffect(() => {
    if (!remote?.sshAlias) {
      setMachine(null);
      return;
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = () => {
      void remoteStatus()
        .then((status) => {
          if (cancelled) return;
          setMachine(status);
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
          if (!cancelled) setMachine(null);
        });
    };
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [remote?.sshAlias]);

  const installConnector = async (sshAlias: string) => {
    setSetupStep(3);
    setStage('installing');
    setMessage('This can take a minute.');
    try {
      const setup = await setupRemoteHelper(sshAlias, 'install');
      setMachine(setup.machine);
      if (setup.error != null) {
        setStage('error');
        setMessage(`Setup failed: ${setup.error}. Check SSH access and retry.`);
        return;
      }
      setStage('ready');
      setSetupStep(4);
      setMessage('SSH monitoring is active.');
      onConnectionVerified?.();
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Setup failed. Check SSH access and retry.');
    }
  };

  const addHost = async () => {
    const sshAlias = alias.trim();
    if (!sshAlias) {
      setStage('error');
      setMessage('Enter an SSH alias first.');
      return;
    }
    setSetupStep(1);
    setMessage('Waiting for the remote host to respond.');
    setStage('testing');
    try {
      const test = await testRemoteAlias(sshAlias);
      if (test.state !== 'ok') {
        setStage('error');
        setMessage(sshTestFailureMessage(test, sshAlias));
        return;
      }
      setSetupStep(2);
      setStage('saving');
      setMessage('SSH is ready. Saving this host to coSlash.');
      if (!(await onAddHost(sshAlias))) {
        setStage('error');
        setMessage('Could not add this SSH host.');
        return;
      }
      setSetupStep(3);
      setStage('consent');
      setMessage('Install a private connector on this host, or skip installation.');
    } catch (error: unknown) {
      setStage('error');
      setMessage(error instanceof Error ? error.message : 'Could not add this SSH host.');
    }
  };

  const retryConnectorSetup = () => {
    if (remote == null) return;
    setSetupStep(3);
    setStage('consent');
    setMessage('Install a private connector on this host, or skip installation.');
  };

  const skipInstallation = () => {
    setSetupStep(4);
    setStage('ready');
    setMessage('Connector skipped. SSH monitoring is active.');
    onConnectionVerified?.();
  };

  const removeHost = async () => {
    setSetupStep(null);
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

  const setExecutable = (agent: RemoteExecutableAgent, path: string) => {
    onExecutablesChange({ ...executables, [agent]: path });
  };

  const commitExecutables = () => {
    if (Object.values(executables ?? {}).some((path) => remoteExecutablePathError(path) != null)) return;
    onExecutablesCommit(executables ?? {});
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
          <>
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
            <div className="border-t px-4 py-3">
              <button
                type="button"
                aria-expanded={executablesOpen}
                onClick={() => setExecutablesOpen((open) => !open)}
                className="text-muted-foreground flex cursor-pointer items-center gap-1.5 text-[11px] font-semibold"
              >
                <ChevronRight
                  aria-hidden="true"
                  className={cn('size-3 transition-transform', { 'rotate-90': executablesOpen })}
                  strokeWidth={2.5}
                />
                Remote agent executables
              </button>
              {executablesOpen && (
                <div className="mt-3 flex flex-col gap-3">
                  <div className="text-muted-foreground text-[11px] leading-relaxed">
                    Optional advanced overrides. Leave blank to discover the executable on this host.
                  </div>
                  {(['claude', 'codex', 'opencode'] as const).map((agent) => {
                    const path = executables?.[agent] ?? '';
                    return (
                      <RemoteExecutableField
                        key={agent}
                        agent={agent}
                        path={path}
                        disabled={busy}
                        onChange={(value) => setExecutable(agent, value)}
                        onBlur={commitExecutables}
                      />
                    );
                  })}
                </div>
              )}
            </div>
          </>
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
        {message != null && setupStep != null && (
          <SetupProgress stage={stage} step={setupStep} message={message} />
        )}
        {message != null && setupStep == null && (
          <div
            role={stage === 'error' ? 'alert' : 'status'}
            className={cn('border-t px-4 py-3 text-xs', {
              'bg-muted text-muted-foreground': busy || stage === 'consent',
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
