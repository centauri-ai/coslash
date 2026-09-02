import { useEffect, useState, type ReactNode } from 'react';
import { Check, ChevronDown, ChevronRight, Settings, TriangleAlert } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { setTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';
import { MachinesSettingsSection } from '@/pages/coslash/components/MachinesSettingsSection';
import {
  availableSynthesisBackends,
  initialSettingsDraft,
  modelForBackend,
  requiresFirstRunConsent,
  type BackendOption,
  type CoslashSettings,
  type RemoteOwnershipAction,
  type SettingsResponse,
} from '@/pages/coslash/lib/settings';

function SectionLabel({ children }: { children: string }) {
  return (
    <div className="flex items-center gap-2 px-0.5">
      <span className="text-muted-foreground text-[11px] font-semibold tracking-widest uppercase">
        {children}
      </span>
      <span className="bg-border h-px flex-1" />
    </div>
  );
}

function SelectControl({
  id,
  label,
  value,
  onChange,
  mono = false,
  children,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  mono?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="relative w-full sm:w-auto">
      <select
        id={id}
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className={cn(
          'border-border bg-background text-foreground focus-visible:border-ring focus-visible:ring-ring h-8 w-full cursor-pointer appearance-none rounded-lg border pr-8 pl-2.5 text-[13px] font-medium outline-none focus-visible:ring-3 sm:min-w-52',
          { 'font-mono text-xs': mono },
        )}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden="true"
        className="text-muted-foreground pointer-events-none absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2"
      />
    </div>
  );
}

function SynthesisPreference({
  enabled,
  onChange,
}: {
  enabled: boolean;
  onChange: (enabled: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-4 p-4">
      <div className="flex min-w-0 flex-col gap-1">
        <div className="text-sm font-semibold">AI synthesis</div>
        <div className="text-muted-foreground text-xs text-pretty">
          Summarize eligible session transcripts through a local CLI.
        </div>
      </div>
      <button
        type="button"
        role="switch"
        aria-label="AI synthesis"
        aria-checked={enabled}
        onClick={() => onChange(!enabled)}
        className="bg-input focus-visible:border-ring focus-visible:ring-ring aria-checked:bg-primary relative inline-flex h-6 w-10 shrink-0 cursor-pointer items-center rounded-full p-0.5 transition-colors outline-none focus-visible:ring-3"
      >
        <span
          className={cn(
            'bg-background pointer-events-none size-5 rounded-full shadow-sm transition-transform',
            {
              'translate-x-4': enabled,
              'translate-x-0': !enabled,
            },
          )}
        />
      </button>
    </div>
  );
}

function backendName(option: BackendOption): string {
  return option.label.replace(/ CLI$/, '');
}

const OPENCODE_DEFAULT_MODEL = 'default';

const BACKEND_BINARY: Record<string, string> = {
  'claude-cli': 'claude',
  'codex_exec': 'codex',
  'opencode': 'opencode',
};

function BackendChoice({
  option,
  selected,
  onSelect,
}: {
  option: BackendOption;
  selected: boolean;
  onSelect: () => void;
}) {
  const name = backendName(option);
  return (
    <button
      type="button"
      disabled={!option.available}
      aria-pressed={selected}
      title={option.available ? name : `The ${BACKEND_BINARY[option.id] ?? option.id} CLI is not available`}
      onClick={onSelect}
      className={cn(
        'border-border bg-background text-foreground focus-visible:ring-ring flex cursor-pointer items-center justify-between gap-2 rounded-xl border px-3 py-3 text-left transition-shadow outline-none focus-visible:ring-3',
        {
          'ring-foreground ring-2 ring-inset': selected,
          'cursor-not-allowed opacity-50': !option.available,
        },
      )}
    >
      <span className="flex items-center gap-2">
        <span
          className={cn('size-2 rounded-full', {
            'bg-claude': option.id === 'claude-cli',
            'bg-codex': option.id === 'codex_exec',
            'bg-opencode': option.id === 'opencode',
          })}
        />
        <span className="text-[13px] font-semibold">{name}</span>
      </span>
      {!option.available ? (
        <span className="bg-warning-bg text-warning-fg inline-flex items-center gap-1 rounded-full px-2 py-1 text-[11px] font-semibold">
          <span className="size-1 rounded-full bg-current" />
          Not detected
        </span>
      ) : (
        selected && <Check aria-hidden="true" className="size-3.5" strokeWidth={3} />
      )}
    </button>
  );
}

export function SettingsButton({ onClick, hasError }: { onClick: () => void; hasError: boolean }) {
  return (
    <Button type="button" variant="outline" size="sm" className="relative" onClick={onClick}>
      <Settings aria-hidden="true" />
      Settings
      {hasError && (
        <span className="bg-destructive absolute -top-1 -right-1 size-2 rounded-full" aria-hidden="true" />
      )}
    </Button>
  );
}

export type SettingsDialogMode = 'synthesis-consent' | 'full-settings';

export function SettingsDialog({
  open,
  mode,
  onOpenChange,
  response,
  isLoading,
  loadError,
  saveError,
  isSaving,
  onSave,
}: {
  open: boolean;
  mode: SettingsDialogMode;
  onOpenChange: (open: boolean) => void;
  response: SettingsResponse | null;
  isLoading: boolean;
  loadError: string | null;
  saveError: string | null;
  isSaving: boolean;
  onSave: (settings: CoslashSettings, remoteOwnershipAction?: RemoteOwnershipAction) => Promise<boolean>;
}) {
  const [draft, setDraft] = useState<CoslashSettings | null>(
    response ? initialSettingsDraft(response) : null,
  );
  const [disclosureOpen, setDisclosureOpen] = useState(false);
  const [remoteOwnershipAction, setRemoteOwnershipAction] = useState<RemoteOwnershipAction | null>(null);
  const isFirstRun = requiresFirstRunConsent(response);
  const requiresConsent = mode === 'synthesis-consent' && isFirstRun;
  const showFullSettings = mode === 'full-settings';

  useEffect(() => {
    if (!open || !response) return;
    setDraft(initialSettingsDraft(response));
    setDisclosureOpen(false);
    setRemoteOwnershipAction(null);
  }, [open, response]);

  const initialDraft = response ? initialSettingsDraft(response) : null;
  // Both sides are spreads of the same response.settings, so key order matches.
  const isDirty = JSON.stringify(draft) !== JSON.stringify(initialDraft);
  const synthesisBackends = availableSynthesisBackends(response?.options.synthesisBackends ?? []);
  const hasSynthesisBackends = synthesisBackends.length > 0;
  const selectedBackend = synthesisBackends.find((option) => option.id === draft?.synthesis.backend);
  const selectedModel = selectedBackend?.models.find((option) => option.id === draft?.synthesis.model);
  const selectedTerminal = response?.options.terminals.find((option) => option.id === draft?.launch.terminal);
  const synthesisBackendAvailable = draft?.synthesis.enabled !== true || selectedBackend?.available === true;
  const canSave = (isFirstRun || response?.valid === false || isDirty) && synthesisBackendAvailable;

  const save = async () => {
    if (!draft || !synthesisBackendAvailable) return;
    if (await onSave(draft, remoteOwnershipAction ?? undefined)) {
      setTheme(draft.appearance.theme);
      onOpenChange(false);
    }
  };

  const close = () => {
    setTheme(response?.settings.appearance.theme ?? 'light');
    onOpenChange(false);
  };

  const status = saveError
    ? { label: 'Save failed', className: 'text-destructive' }
    : isSaving
      ? { label: 'Saving…', className: 'text-muted-foreground' }
      : !synthesisBackendAvailable
        ? { label: 'Backend unavailable', className: 'text-warning-fg' }
        : response?.valid === false
          ? { label: 'Repair required', className: 'text-warning-fg' }
          : isDirty
            ? { label: 'Unsaved changes', className: 'text-warning-fg' }
            : isFirstRun
              ? { label: 'Not saved yet', className: 'text-warning-fg' }
              : { label: 'No changes', className: 'text-muted-foreground' };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (requiresConsent && !next) return;
        if (next) onOpenChange(true);
        else close();
      }}
    >
      <DialogContent
        className="flex max-h-[calc(100svh-2rem)] flex-col gap-0 overflow-hidden p-0 shadow-2xl sm:max-w-xl"
        showCloseButton={!requiresConsent}
        onEscapeKeyDown={(event) => requiresConsent && event.preventDefault()}
        onPointerDownOutside={(event) => requiresConsent && event.preventDefault()}
      >
        <DialogHeader className="shrink-0 gap-1.5 p-4 pr-12 pb-3">
          <DialogTitle>{showFullSettings ? 'Settings' : 'Choose how coSlash handles synthesis'}</DialogTitle>
          <DialogDescription className="flex items-center gap-1.5 text-xs">
            <span>Machine-wide.</span>
            <code className="bg-muted rounded-md px-1.5 py-0.5 font-mono text-[11px]">
              ~/.coslash/settings.json
            </code>
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
          {isLoading ? (
            <div className="text-muted-foreground py-8 text-sm">Loading settings…</div>
          ) : loadError ? (
            <div role="alert" className="text-destructive py-4 text-sm">
              Could not load settings: {loadError}
            </div>
          ) : response && draft ? (
            <div className="flex flex-col gap-5">
              {!response.valid && (
                <div role="alert" className="text-destructive rounded-xl border p-3 text-sm">
                  {response.error} Save valid settings below to repair the file.
                </div>
              )}

              <div className="flex flex-col gap-2">
                <SectionLabel>Synthesis</SectionLabel>
                <div className="border-border bg-card overflow-hidden rounded-xl border">
                  <SynthesisPreference
                    enabled={draft.synthesis.enabled}
                    onChange={(enabled) => setDraft({ ...draft, synthesis: { ...draft.synthesis, enabled } })}
                  />

                  {draft.synthesis.enabled ? (
                    <div className="bg-muted flex flex-col gap-4 border-t p-4">
                      <div className="flex flex-col gap-2">
                        <div className="flex items-baseline justify-between gap-3">
                          <div className="text-[13px] font-semibold">Backend</div>
                          <div className="text-muted-foreground text-[11px]">
                            Runs through your existing CLI account
                          </div>
                        </div>
                        <div className="grid gap-2.5 sm:grid-cols-2">
                          {synthesisBackends.map((option) => (
                            <BackendChoice
                              key={option.id}
                              option={option}
                              selected={option.id === draft.synthesis.backend}
                              onSelect={() =>
                                setDraft({
                                  ...draft,
                                  synthesis: {
                                    ...draft.synthesis,
                                    backend: option.id,
                                    model: modelForBackend(response, option.id),
                                  },
                                })
                              }
                            />
                          ))}
                          {!hasSynthesisBackends && (
                            <div className="text-muted-foreground py-2 text-xs sm:col-span-2">
                              No supported CLI detected
                            </div>
                          )}
                        </div>
                      </div>

                      <div
                        className={cn(
                          'border-border flex flex-col gap-3 border-t pt-3 sm:flex-row sm:items-center sm:justify-between',
                          { hidden: !hasSynthesisBackends },
                        )}
                      >
                        <div className="flex min-w-0 flex-col gap-1">
                          <div className="text-[13px] font-semibold">Model</div>
                          <div className="text-muted-foreground text-[11px]">
                            {selectedBackend ? `${backendName(selectedBackend)} models` : 'Select a backend'}
                          </div>
                        </div>
                        <div className="flex items-center gap-2">
                          {selectedModel?.default && (
                            <span className="border-border bg-background text-muted-foreground inline-flex rounded-full border px-2 py-1 text-[11px] font-semibold">
                              Default
                            </span>
                          )}
                          <SelectControl
                            id="synthesis-model"
                            label="Synthesis model"
                            value={draft.synthesis.model}
                            mono
                            onChange={(model) =>
                              setDraft({ ...draft, synthesis: { ...draft.synthesis, model } })
                            }
                          >
                            {selectedBackend?.models.map((option) => (
                              <option key={option.id} value={option.id}>
                                {option.id === OPENCODE_DEFAULT_MODEL ? option.label : option.id}
                              </option>
                            ))}
                          </SelectControl>
                        </div>
                      </div>

                      <div className="border-border border-t pt-3">
                        <button
                          type="button"
                          aria-expanded={disclosureOpen}
                          onClick={() => setDisclosureOpen((current) => !current)}
                          className="text-muted-foreground flex cursor-pointer items-center gap-1.5 text-[11px] font-semibold"
                        >
                          <ChevronRight
                            aria-hidden="true"
                            className={cn('size-3 transition-transform', { 'rotate-90': disclosureOpen })}
                            strokeWidth={2.5}
                          />
                          What synthesis sends
                        </button>
                        {disclosureOpen && (
                          <div className="text-muted-foreground pt-2 text-[11px] leading-relaxed text-pretty">
                            coSlash sends derived session facts through the selected CLI using your existing
                            account. This may consume account usage. Results are cached under{' '}
                            <code className="font-mono">~/.coslash</code>. Source transcripts are never
                            modified, and coSlash does not store API keys or tokens in settings.
                          </div>
                        )}
                      </div>
                    </div>
                  ) : (
                    <div className="bg-muted text-muted-foreground border-t px-4 py-3 text-[11px]">
                      Off — sessions get deterministic debriefs only. Nothing leaves this machine.
                    </div>
                  )}
                </div>
              </div>

              {showFullSettings && (
                <div className="flex flex-col gap-2">
                  <SectionLabel>Appearance</SectionLabel>
                  <div className="border-border bg-card flex items-center justify-between gap-4 rounded-xl border p-4">
                    <div className="flex min-w-0 flex-col gap-1">
                      <div className="text-sm font-semibold">Dark mode</div>
                      <div className="text-muted-foreground text-xs">Use a darker color palette.</div>
                    </div>
                    <button
                      type="button"
                      role="switch"
                      aria-label="Dark mode"
                      aria-checked={draft.appearance.theme === 'dark'}
                      onClick={() => {
                        const theme = draft.appearance.theme === 'dark' ? 'light' : 'dark';
                        setTheme(theme);
                        setDraft({
                          ...draft,
                          appearance: { theme },
                        });
                      }}
                      className="bg-input focus-visible:border-ring focus-visible:ring-ring aria-checked:bg-primary relative inline-flex h-6 w-10 shrink-0 cursor-pointer items-center rounded-full p-0.5 transition-colors outline-none focus-visible:ring-3"
                    >
                      <span
                        className={cn(
                          'bg-background pointer-events-none size-5 rounded-full shadow-sm transition-transform',
                          { 'translate-x-4': draft.appearance.theme === 'dark' },
                        )}
                      />
                    </button>
                  </div>
                </div>
              )}

              {showFullSettings && (
                <div className="flex flex-col gap-2">
                  <SectionLabel>Launch</SectionLabel>
                  <div className="border-border bg-card overflow-hidden rounded-xl border">
                    <div className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
                      <div className="flex min-w-0 flex-col gap-1">
                        <div className="text-sm font-semibold">Terminal</div>
                        <div className="text-muted-foreground text-xs">
                          Opens new, resumed, and handoff sessions.
                        </div>
                      </div>
                      <SelectControl
                        id="launch-terminal"
                        label="Terminal for new, resumed, and handoff sessions"
                        value={draft.launch.terminal}
                        onChange={(terminal) => setDraft({ ...draft, launch: { terminal } })}
                      >
                        {response.options.terminals.map((option) => (
                          <option key={option.id} value={option.id} disabled={!option.available}>
                            {option.label}
                            {option.available ? '' : ' — not detected'}
                          </option>
                        ))}
                      </SelectControl>
                    </div>
                    {selectedTerminal && !selectedTerminal.available && (
                      <div
                        role="alert"
                        className="bg-muted text-warning-fg flex items-start gap-2 border-t px-4 py-3 text-[11px] text-pretty"
                      >
                        <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
                        <span>
                          {selectedTerminal.label} is not available. Session launches will show an error
                          rather than use a different terminal.
                        </span>
                      </div>
                    )}
                  </div>
                </div>
              )}

              {showFullSettings && (
                <MachinesSettingsSection
                  remote={draft.remote}
                  onChange={(remote) => {
                    if (remote == null) {
                      const { remote: _removed, ...rest } = draft;
                      setDraft({ ...rest, remote: null });
                      return;
                    }
                    // Blank alias with no saved host stays omitted so local-only settings stay unchanged.
                    if (!remote.sshAlias.trim() && draft.remote == null) {
                      const { remote: _removed, ...rest } = draft;
                      setDraft(rest);
                      return;
                    }
                    setDraft({ ...draft, remote: { ...remote, sshAlias: remote.sshAlias.trim() } });
                  }}
                  onOwnershipActionChange={setRemoteOwnershipAction}
                />
              )}

              {saveError && (
                <div role="alert" className="text-destructive text-sm">
                  {saveError}
                </div>
              )}
            </div>
          ) : null}
        </div>

        <div className="bg-muted relative z-10 flex shrink-0 items-center justify-between gap-4 border-t px-4 py-3 shadow-[0_-8px_16px_-12px_rgba(0,0,0,0.35)]">
          <div className={cn('flex items-center gap-2 text-xs', status.className)}>
            <span className="size-1.5 rounded-full bg-current" />
            {status.label}
          </div>
          <div className="flex items-center gap-2">
            {!requiresConsent && (
              <Button variant="outline" onClick={close} disabled={isSaving}>
                Cancel
              </Button>
            )}
            <Button onClick={() => void save()} disabled={!draft || isSaving || !canSave}>
              {isFirstRun ? 'Save settings' : 'Save changes'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
