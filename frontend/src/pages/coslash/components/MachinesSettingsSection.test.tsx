import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import {
  RemoteExecutableField,
  SetupProgress,
} from '@/pages/coslash/components/MachinesSettingsSection';

describe('RemoteExecutableField', () => {
  it('shows inline validation before an invalid root path can be saved', () => {
    const markup = renderToStaticMarkup(
      <RemoteExecutableField agent="codex" path="/" disabled={false} onChange={vi.fn()} onBlur={vi.fn()} />,
    );

    expect(markup).toContain('aria-invalid="true"');
    expect(markup).toContain('Use an absolute path or a path beginning with ~/.');
  });
});

describe('SetupProgress', () => {
  it('shows the active milestone without inventing a percentage', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress
        stage="installing"
        step={3}
        message="Installing and verifying the connector. This can take a minute."
      />,
    );

    expect(markup).toContain('3 of 4 · Set up connector');
    expect(markup).toContain('animate-spin');
    expect(markup).not.toContain('%');
  });

  it('keeps a failure attached to the step that failed', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress stage="error" step={1} message="Authentication failed." />,
    );

    expect(markup).toContain('1 of 4 · Verify SSH failed');
    expect(markup).toContain('role="alert"');
  });

  it('shows successful completion', () => {
    const markup = renderToStaticMarkup(
      <SetupProgress stage="ready" step={4} message="SSH monitoring is active." />,
    );

    expect(markup).toContain('4 of 4 · Remote host ready');
    expect(markup).toContain('bg-success-bg');
  });
});
