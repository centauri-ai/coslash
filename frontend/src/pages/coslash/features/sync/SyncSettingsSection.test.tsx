import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { SyncSettingsSection } from './SyncSettingsSection';

describe('SyncSettingsSection', () => {
  it('shows the connected status and local pause setting', () => {
    const markup = renderToStaticMarkup(
      <SyncSettingsSection
        status={{ state: 'connected_idle', hubOrigin: 'https://hub.example', sessions: {} }}
        statusLoadState="ready"
        paused={false}
        onPauseChange={() => {}}
      />,
    );

    expect(markup).toContain('Auto-sync is managed in Hub.');
    expect(markup).toContain('role="switch"');
    expect(markup).toContain('aria-checked="false"');
  });

  it('shows unavailable status without hiding the local pause setting', () => {
    const markup = renderToStaticMarkup(
      <SyncSettingsSection status={null} statusLoadState="error" paused onPauseChange={() => {}} />,
    );

    expect(markup).toContain('Sync status is unavailable.');
    expect(markup).toContain('role="switch"');
    expect(markup).toContain('aria-checked="true"');
    expect(markup).not.toContain('Not connected to Hub.');
  });
});

it('explains a rejected primary pairing while retaining its real sync status', () => {
  const markup = renderToStaticMarkup(
    <SyncSettingsSection
      status={{
        state: 'connected_idle',
        hubOrigin: 'https://hub.example',
        sessions: {},
        pairingIssue: 'binding_mismatch',
      }}
      statusLoadState="ready"
      paused={false}
      onPauseChange={() => {}}
    />,
  );
  expect(markup).toContain('Auto-sync is managed in Hub.');
  expect(markup).toContain('role="alert"');
  expect(markup).toContain('existing credential and pending');
  expect(markup).toContain('contact support');
});
