import type { ReactNode } from 'react';
import { AlertTriangle, Moon, RefreshCw, Settings, Sun } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { Theme } from '@/lib/theme';
import { cn } from '@/lib/utils';
import { styles } from '@/pages/coslash/components/session-list/styles';
import { machineStatusText, needsBanner, needsSettings } from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';

export function CoslashHeader({
  machines,
  diagnostics,
  onSettings,
  theme,
  onThemeChange,
  themeDisabled,
  onRetry,
  retrying,
  actions,
}: {
  machines: MachineFact[];
  diagnostics: ReactNode;
  onSettings: () => void;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  themeDisabled: boolean;
  onRetry: () => void;
  retrying: boolean;
  actions?: ReactNode;
}) {
  const problems = machines.filter(needsBanner);
  return (
    <>
      <div className={styles.header}>
        <div className="flex min-w-0 shrink-0 items-center gap-3.5">
          <span role="img" aria-label="coSlash">
            <img src="/brand/coslash-logo.svg" alt="" className="w-[104px] dark:hidden" />
            <img src="/brand/coslash-logo-reverse.svg" alt="" className="hidden w-[104px] dark:block" />
          </span>
          <span className="text-meta text-coslash-muted max-sidebar:hidden truncate">
            Run more agents. Lose less context.
          </span>
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {actions}
          <div className="bg-coslash-soft rounded-[10px] p-0.5">{diagnostics}</div>
          <div className="bg-coslash-soft rounded-[10px] p-0.5">
            <Button
              variant="ghost"
              size="sm"
              className="text-meta min-h-7 cursor-pointer gap-1.5 rounded-[7px] px-2.5 font-[550] [&>svg]:size-3.5"
              onClick={onSettings}
            >
              <Settings /> Settings
            </Button>
          </div>
          <div
            className="bg-coslash-soft inline-flex items-center gap-0.5 rounded-[10px] p-0.5"
            aria-label="Theme"
          >
            {(
              [
                ['light', Sun],
                ['dark', Moon],
              ] as const
            ).map(([value, Icon]) => (
              <button
                key={value}
                type="button"
                className={cn(
                  'text-coslash-muted hover:bg-coslash-surface grid size-7 cursor-pointer place-items-center rounded-[7px] disabled:pointer-events-none disabled:opacity-50 [&>svg]:size-3.5',
                  { 'bg-coslash-surface text-coslash-ink shadow-sm': theme === value },
                )}
                aria-label={`${value === 'light' ? 'Light' : 'Dark'} theme`}
                aria-pressed={theme === value}
                disabled={themeDisabled}
                onClick={() => onThemeChange(value)}
              >
                <Icon aria-hidden="true" />
              </button>
            ))}
          </div>
        </div>
      </div>
      {problems.length > 0 && (
        <div className={styles.banner} role="alert">
          <AlertTriangle />
          <span>
            <strong className="font-[650]">{problems.map((machine) => machine.label).join(', ')}</strong>{' '}
            {problems.length === 1
              ? machineStatusText(problems[0])
              : 'need attention — open Settings for guidance.'}
          </span>
          {problems.some(needsSettings) ? (
            <Button
              variant="outline"
              size="sm"
              className="border-danger-fg text-danger-fg ml-auto bg-transparent"
              onClick={onSettings}
            >
              <Settings /> Open Settings
            </Button>
          ) : (
            <Button
              variant="outline"
              size="sm"
              className="border-danger-fg text-danger-fg ml-auto bg-transparent"
              onClick={onRetry}
              disabled={retrying}
            >
              <RefreshCw className={cn({ 'animate-spin': retrying })} /> Retry
            </Button>
          )}
        </div>
      )}
    </>
  );
}
