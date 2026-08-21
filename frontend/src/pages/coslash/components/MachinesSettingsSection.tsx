import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { firstTimeSshHint, formatTestConnectionResult } from '@/pages/coslash/lib/host-strip';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { testRemoteAlias } from '@/pages/coslash/lib/remote-api';
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
  const [testError, setTestError] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);

  const setAlias = (sshAlias: string) => {
    setTestResult(null);
    setTestError(null);
    setConfirmRemove(false);
    onChange({ ...draft, sshAlias });
  };

  const setEnabled = (enabled: boolean) => {
    setConfirmRemove(false);
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
    } catch (error: unknown) {
      setTestError(error instanceof Error ? error.message : String(error));
    } finally {
      setTesting(false);
    }
  };

  const removeHost = () => {
    if (!confirmRemove) {
      setConfirmRemove(true);
      return;
    }
    setConfirmRemove(false);
    setTestResult(null);
    setTestError(null);
    onChange(null);
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
            Monitor Claude Code and Codex through your existing SSH config. Linux only needs SSH/SFTP and
            readable agent files; do not install coSlash there.
          </div>
        </div>

        <div className="border-border flex flex-col gap-3 border-t p-4 sm:flex-row sm:items-center sm:justify-between">
          <label htmlFor="remote-ssh-alias" className="text-[13px] font-semibold">
            SSH alias
          </label>
          <input
            id="remote-ssh-alias"
            aria-label="SSH alias"
            value={draft.sshAlias}
            onChange={(event) => setAlias(event.target.value)}
            placeholder="gpu-server"
            className="border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 w-full rounded-lg border px-2.5 font-mono text-xs outline-none focus-visible:ring-3 sm:max-w-72"
          />
        </div>

        <div className="border-border flex items-center justify-between gap-4 border-t p-4">
          <div className="flex min-w-0 flex-col gap-1">
            <div className="text-[13px] font-semibold">Enabled</div>
            <div className="text-muted-foreground text-[11px]">
              {draft.enabled
                ? 'Monitor this host on the board'
                : 'Disabled — no remote cards or strip; cache retained'}
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
              <Button type="button" variant="outline" size="sm" onClick={removeHost}>
                {confirmRemove ? 'Confirm remove' : 'Remove host'}
              </Button>
            )}
          </div>
        </div>

        {(testResult != null || testError != null) && (
          <div
            role={testResult?.state === 'ok' ? 'status' : 'alert'}
            className={cn('border-t px-4 py-3 text-xs', {
              'bg-success-bg text-success-fg': testResult?.state === 'ok',
              'bg-warning-bg text-warning-fg': testResult != null && testResult.state !== 'ok',
              'bg-destructive/10 text-destructive': testError != null,
            })}
          >
            {testError ?? (testResult ? formatTestConnectionResult(testResult) : null)}
            {showFirstTimeHint && <div className="pt-1">{firstTimeSshHint(draft.sshAlias.trim())}</div>}
          </div>
        )}
      </div>
    </div>
  );
}
