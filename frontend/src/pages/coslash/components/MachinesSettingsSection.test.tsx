import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { RemoteExecutableField, SetupProgress } from '@/pages/coslash/components/MachinesSettingsSection';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { sshTestFailureMessage } from '@/pages/coslash/lib/remote-setup-copy';

describe('RemoteExecutableField', () => {
  it('shows inline validation before an invalid root path can be saved', () => {
    const markup = renderToStaticMarkup(
      <RemoteExecutableField agent="codex" path="/" disabled={false} onChange={vi.fn()} onBlur={vi.fn()} />,
    );

    expect(markup).toContain('aria-invalid="true"');
    expect(markup).toContain('Use an absolute path or a path beginning with ~/.');
  });

  it('renders an OpenCode override', () => {
    const markup = renderToStaticMarkup(
      <RemoteExecutableField agent="opencode" path="" disabled={false} onChange={vi.fn()} onBlur={vi.fn()} />,
    );

    expect(markup).toContain('OpenCode remote executable');
  });
});

describe('SetupProgress', () => {
  it('shows completed work and the active task like a command log', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress
        stage="installing"
        step={3}
        message="Installing and verifying the connector. This can take a minute."
      />,
    );

    expect(markup).toContain('SSH connection verified');
    expect(markup).toContain('Remote host saved');
    expect(markup).toContain('Installing and verifying connector…');
    expect(markup).toContain('animate-spin');
    expect(markup).not.toContain('%');
  });

  it('keeps a failure attached to the step that failed', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress stage="error" step={1} message="Authentication failed." />,
    );

    expect(markup).toContain('SSH verification failed');
    expect(markup).toContain('Authentication failed.');
    expect(markup).toContain('role="alert"');
  });

  it('shows successful completion', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress stage="ready" step={4} message="SSH monitoring is active." />,
    );

    expect(markup).toContain('Remote host ready');
    expect(markup).toContain('text-success-fg');
  });
});

describe('sshTestFailureMessage', () => {
  const machine = (reason: MachineFact['reason']): MachineFact => ({
    sourceId: 'remote',
    label: 'SSH workspace',
    state: 'error',
    complete: false,
    reason,
  });

  it.each([
    ['refresh_timeout', 'did not respond before the connection timed out', 'network or VPN'],
    ['authentication_failed', 'authentication was rejected', 'key, agent, or login prompt'],
    ['host_key_failed', 'host key is not trusted', 'confirm the host key'],
    ['connection_failed', 'could not reach the SSH host', 'alias, network or VPN, and SSH port'],
    ['sftp_unavailable', 'SFTP subsystem is unavailable', 'sftp dev-box'],
  ] as const)('explains and mitigates %s', (reason, cause, mitigation) => {
    const message = sshTestFailureMessage(machine(reason), 'dev-box');

    expect(message).toContain(cause);
    expect(message).toContain(mitigation);
  });
});
