import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { RemoteExecutableField } from '@/pages/coslash/components/MachinesSettingsSection';

describe('RemoteExecutableField', () => {
  it('shows inline validation before an invalid root path can be saved', () => {
    const markup = renderToStaticMarkup(
      <RemoteExecutableField agent="codex" path="/" disabled={false} onChange={vi.fn()} onBlur={vi.fn()} />,
    );

    expect(markup).toContain('aria-invalid="true"');
    expect(markup).toContain('Use an absolute path or a path beginning with ~/.');
  });
});
