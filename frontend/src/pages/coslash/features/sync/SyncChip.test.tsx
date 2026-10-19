import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { decodeSyncStatus, type SyncStatus } from './api';
import { SyncChip } from './SyncChip';
import { SyncHeader } from './SyncHeader';

describe('SyncChip', () => {
  it('exposes persistent row status as static text, not a live region', () => {
    const markup = renderToStaticMarkup(<SyncChip state="in_hub" />);

    expect(markup).toContain('In Hub');
    expect(markup).not.toContain('role="status"');
  });

  it('shows the stale-contact warning chip and its retry panel', () => {
    const status: SyncStatus = {
      state: 'connected_idle',
      hubOrigin: 'https://hub.example.test',
      sessions: {},
      lastFailure: {
        host: 'hub.example.test',
        cause: 'connect',
        sinceMs: 1,
        attempts: 14,
        sentence:
          "Hub: can't connect to hub.example.test since 08:12 (14 attempts) — Connection timed out. Check your network, VPN or proxy.",
        rawError: 'dial tcp: i/o timeout',
      },
      hubFailureActive: true,
    };
    const markup = renderToStaticMarkup(
      <SyncHeader status={status} loaded onSettings={() => undefined} onRetry={async () => undefined} />,
    );

    expect(markup).toContain('Can&#x27;t reach Hub · since');
    expect(markup).toContain('<details');
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('Connection timed out. Check your network, VPN or proxy.');
    expect(markup).toContain('Raw error: dial tcp: i/o timeout');
    expect(markup).toContain('Retry now');
  });

  it('renders the warning from the Local sync-status API response JSON', () => {
    const status = decodeSyncStatus(
      JSON.parse(
        `{"state":"connected_idle","hubOrigin":"https://localhost:18180","sessions":{},"sessionFailures":{},"lastFailure":{"host":"localhost:18180","cause":"connect","sinceMs":1791792720000,"attempts":242,"sentence":"Hub: can't connect to localhost:18180 since 08:12 (242 attempts) — Connection refused. Check your network, VPN or proxy.","rawError":"dial tcp: connect: connection refused"},"hubFailureActive":true}`,
      ),
    );
    const markup = renderToStaticMarkup(
      <SyncHeader status={status} loaded onSettings={() => undefined} onRetry={async () => undefined} />,
    );

    expect(markup).toContain('Can&#x27;t reach Hub · since');
    expect(markup).toContain('Connection refused. Check your network, VPN or proxy.');
    expect(markup).toContain('Retry now');
  });

  it('keeps the spinner for in-flight work', () => {
    const markup = renderToStaticMarkup(<SyncChip state="syncing" />);

    expect(markup).toContain('Syncing…');
    expect(markup).toContain('animate-spin');
  });

  it('shows a parked failure with its cause and retry hint, without a spinner', () => {
    const markup = renderToStaticMarkup(
      <SyncChip state="parked_failed" failureCode="unreadable_source" />,
    ).replaceAll('&#x27;', "'");

    expect(markup).toContain("Didn't sync");
    expect(markup).toContain("coSlash Local couldn't read the session file.");
    expect(markup).toContain('Syncs again when the session changes.');
    expect(markup).toContain('class="sr-only"');
    expect(markup).not.toContain('animate-spin');
  });
});
