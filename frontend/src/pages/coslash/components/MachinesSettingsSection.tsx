import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { firstTimeSshHint, formatTestConnectionResult } from '@/pages/coslash/lib/host-strip';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  releaseRemoteHelperOwnership,
  remoteStatus,
  setupRemoteHelper,
  testRemoteAlias,
  uninstallRemoteHelper,
  type HelperSetupResult,
} from '@/pages/coslash/lib/remote-api';
import type { RemoteHostSettings } from '@/pages/coslash/lib/settings';

export function MachinesSettingsSection({
  remote,
  onChange,
}: {
  remote: RemoteHostSettings | null | undefined;
  onChange: (remote: RemoteHostSettings | null) => void;
}) {
  const draft = remote ?? { sshAlias: '', enabled: true };
  const hasHost = remote != null;
  const [testResult, setTestResult] = useState<MachineFact | null>(null);
  const [hostStatus, setHostStatus] = useState<MachineFact | null>(null);
  const [setupResult, setSetupResult] = useState<HelperSetupResult | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [helperAction, setHelperAction] = useState<'install' | 'upgrade' | null>(null);
  const [removeAction, setRemoveAction] = useState<'only' | 'uninstall' | null>(null);
  const [pendingAlias, setPendingAlias] = useState<string | null>(null);
  const displayedAlias = pendingAlias ?? draft.sshAlias;
  const helperOwned = hostStatus?.helper?.version != null;

  useEffect(() => {
    if (!remote?.id) {
      setHostStatus(null);
      return;
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const status = await remoteStatus();
        if (cancelled) return;
        setHostStatus(status);
        if (status.helperProbeState === 'probing') timer = setTimeout(() => void load(), 400);
      } catch {
        if (!cancelled) setHostStatus(null);
      }
    };
    void load();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [remote?.id]);

  const setAlias = (sshAlias: string) => {
    setTestResult(null);
    setSetupResult(null);
    setTestError(null);
    setRemoveAction(null);
    if (hasHost && helperOwned && sshAlias !== draft.sshAlias) {
      setPendingAlias(sshAlias);
      return;
    }
    setPendingAlias(null);
    onChange({ ...draft, sshAlias });
  };

  const setEnabled = (enabled: boolean) => {
    setRemoveAction(null);
    onChange({ ...draft, enabled });
  };

  const runTest = async () => {
    const alias = draft.sshAlias.trim();
    if (!alias) {
      setTestError('Enter an SSH alias first');
      return;
    }
    setTesting(true);
    setTestError(null);
    setTestResult(null);
    try {
      setTestResult(await testRemoteAlias(alias));
      try {
        setHostStatus(await remoteStatus());
      } catch {
        // SFTP test success remains useful if the optional helper inspection
        // endpoint is temporarily unavailable.
      }
    } catch (error: unknown) {
      setTestError(error instanceof Error ? error.message : String(error));
    } finally {
      setTesting(false);
    }
  };

  const setupHelper = async (consent: 'install' | 'upgrade') => {
    setHelperAction(consent);
    setTestError(null);
    try {
      const result = await setupRemoteHelper(consent);
      setSetupResult(result);
      setTestResult(result.machine);
      setHostStatus(result.machine);
    } catch (error: unknown) {
      setTestError(error instanceof Error ? error.message : String(error));
    } finally {
      setHelperAction(null);
    }
  };

  const removeHost = async (action: 'only' | 'uninstall') => {
    if (removeAction !== action) {
      setRemoveAction(action);
      return;
    }
    if (action === 'uninstall') {
      setTesting(true);
      setTestError(null);
      try {
        await uninstallRemoteHelper();
      } catch (error: unknown) {
        setTestError(error instanceof Error ? error.message : String(error));
        setTesting(false);
        return;
      }
      setTesting(false);
    } else {
      try {
        await releaseRemoteHelperOwnership();
      } catch (error: unknown) {
        setTestError(error instanceof Error ? error.message : String(error));
        return;
      }
    }
    setRemoveAction(null);
    setTestResult(null);
    setTestError(null);
    onChange(null);
  };

  const resolveAliasChange = async (action: 'uninstall' | 'leave' | 'cancel') => {
    if (pendingAlias == null) return;
    if (action === 'cancel') {
      setPendingAlias(null);
      return;
    }
    setTesting(true);
    setTestError(null);
    try {
      if (action === 'uninstall') await uninstallRemoteHelper();
      else await releaseRemoteHelperOwnership();
      onChange({ ...draft, sshAlias: pendingAlias });
      setPendingAlias(null);
      setHostStatus(null);
    } catch (error: unknown) {
      setTestError(error instanceof Error ? error.message : String(error));
    } finally {
      setTesting(false);
    }
  };

  const showFirstTimeHint = testResult?.state === 'error' && testResult.reason === 'connection_failed';

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 px-0.5">
        <span className="text-muted-foreground text-[11px] font-semibold tracking-widest uppercase">
          Machines
        </span>
        <span className="bg-border h-px flex-1" />
      </div>
      <div className="border-border bg-card overflow-hidden rounded-xl border">
        <div className="flex flex-col gap-1 p-4">
          <div className="text-sm font-semibold">Remote host</div>
          <div className="text-muted-foreground text-xs text-pretty">
            Monitor Claude Code and Codex through your existing SSH config. SFTP works without Linux code;
            you can explicitly install the optional, verified collection helper for faster refreshes.
          </div>
        </div>

        <div className="border-border flex flex-col gap-3 border-t p-4 sm:flex-row sm:items-center sm:justify-between">
          <label htmlFor="remote-ssh-alias" className="text-[13px] font-semibold">
            SSH alias
          </label>
          <input
            id="remote-ssh-alias"
            aria-label="SSH alias"
            value={displayedAlias}
            onChange={(event) => setAlias(event.target.value)}
            placeholder="gpu-server"
            className="border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 w-full rounded-lg border px-2.5 font-mono text-xs outline-none focus-visible:ring-3 sm:max-w-72"
          />
        </div>

        {pendingAlias != null && (
          <div className="border-warning-fg/30 bg-warning-bg text-warning-fg flex flex-col gap-2 border-t p-4 text-xs">
            A helper is owned by {draft.sshAlias}. Choose how to change this host before saving the new alias.
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" disabled={testing} onClick={() => void resolveAliasChange('uninstall')}>
                Uninstall helper and change host
              </Button>
              <Button type="button" variant="outline" size="sm" disabled={testing} onClick={() => void resolveAliasChange('leave')}>
                Leave helper installed and change host
              </Button>
              <Button type="button" variant="ghost" size="sm" disabled={testing} onClick={() => void resolveAliasChange('cancel')}>
                Cancel
              </Button>
            </div>
          </div>
        )}

        <div className="border-border flex items-center justify-between gap-4 border-t p-4">
          <div className="flex min-w-0 flex-col gap-1">
            <div className="text-[13px] font-semibold">Enabled</div>
            <div className="text-muted-foreground text-[11px]">
              {draft.enabled
                ? 'Monitor this host on the board'
                : 'Disabled — no remote cards or strip; cache and any installed helper are retained'}
            </div>
          </div>
          <button
            type="button"
            role="switch"
            aria-label="Remote host enabled"
            aria-checked={draft.enabled}
            disabled={!draft.sshAlias.trim()}
            onClick={() => setEnabled(!draft.enabled)}
            className="bg-input focus-visible:border-ring focus-visible:ring-ring aria-checked:bg-primary relative inline-flex h-6 w-10 shrink-0 cursor-pointer items-center rounded-full p-0.5 transition-colors outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <span
              className={cn(
                'bg-background pointer-events-none size-5 rounded-full shadow-sm transition-transform',
                {
                  'translate-x-4': draft.enabled,
                  'translate-x-0': !draft.enabled,
                },
              )}
            />
          </button>
        </div>

        <div className="border-border flex flex-wrap items-center justify-between gap-3 border-t p-4">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={testing || !draft.sshAlias.trim()}
              onClick={() => void runTest()}
            >
              {testing ? 'Testing…' : 'Test connection'}
            </Button>
            {hasHost && (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={testing || helperAction != null || testResult?.state !== 'ok' || hostStatus?.helperInstallationAvailable !== true || hostStatus?.helperProbeState === 'probing'}
                  onClick={() => void setupHelper(hostStatus?.helper?.state === 'deprecated' ? 'upgrade' : 'install')}
                >
                  {helperAction != null
                    ? 'Setting up helper…'
                    : hostStatus?.helper?.state === 'deprecated'
                      ? 'Upgrade helper'
                      : 'Install helper'}
                </Button>
                <Button type="button" variant="outline" size="sm" onClick={() => void removeHost('only')}>
                  {removeAction === 'only' ? 'Confirm remove only' : 'Remove host only'}
                </Button>
                <Button
                  type="button"
                  variant="destructive"
                  size="sm"
                  disabled={testing || helperAction != null}
                  onClick={() => void removeHost('uninstall')}
                >
                  {removeAction === 'uninstall' ? 'Confirm uninstall and remove' : 'Uninstall helper and remove'}
                </Button>
              </>
            )}
          </div>
        </div>

        {hasHost && (
          <div className="border-border text-muted-foreground flex flex-col gap-1 border-t p-4 text-xs">
            <div>Active transport: {hostStatus?.transport === 'helper' ? 'Verified helper' : 'SFTP fallback'}</div>
            <div>
              Helper: {hostStatus?.helperProbeState === 'probing' ? 'Checking installed helper…' : hostStatus?.helper?.state ?? 'Not checked'}
              {hostStatus?.helper?.version ? ` · ${hostStatus.helper.version}` : ''}
              {hostStatus?.helper?.reason ? ` · ${hostStatus.helper.reason.replaceAll('_', ' ')}` : ''}
            </div>
            {hostStatus?.helperInstallationAvailable === false && <div>Helper installation is unavailable in this build.</div>}
            {testResult?.state !== 'ok' && hostStatus?.helperInstallationAvailable !== false && <div>Test SSH/SFTP before installing or upgrading the helper.</div>}
          </div>
        )}

        {(testResult != null || testError != null) && (
          <div
            role={testResult?.state === 'ok' ? 'status' : 'alert'}
            className={cn('border-t px-4 py-3 text-xs', {
              'bg-success-bg text-success-fg': testResult?.state === 'ok',
              'bg-warning-bg text-warning-fg': testResult != null && testResult.state !== 'ok',
              'bg-destructive/10 text-destructive': testError != null,
            })}
          >
            {testError ?? setupResult?.error ?? (testResult ? formatTestConnectionResult(testResult) : null)}
            {testResult?.helper && (
              <div className="pt-1">
                Helper: {testResult.helper.version ? `${testResult.helper.version} · ` : ''}
                {testResult.helper.state.replaceAll('_', ' ')} · {testResult.helper.compatible ? 'compatible' : 'not ready'}
                {testResult.helper.fallback ? ' · using SFTP fallback' : ''}
              </div>
            )}
            {showFirstTimeHint && <div className="pt-1">{firstTimeSshHint(draft.sshAlias.trim())}</div>}
          </div>
        )}
      </div>
    </div>
  );
}
