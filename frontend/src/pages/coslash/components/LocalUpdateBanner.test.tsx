import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { isLocalUpdate } from '@/pages/coslash/lib/local-update';
import { LocalUpdateBanner } from './LocalUpdateBanner';

describe('Local manual update guidance', () => {
  it('shows the trusted download and identity guidance until the next supported check-in', () => {
    const html = renderToStaticMarkup(
      <LocalUpdateBanner
        update={{
          available: true,
          required: true,
          version: '0.0.6',
          downloadUrl: 'https://download.example/app',
        }}
      />,
    );
    expect(html).toContain('Update required to resume sync');
    expect(html).toContain('manually replace this app');
    expect(html).toContain('OS keychain');
    expect(html).toContain('href="https://download.example/app"');
  });

  it('hides the prompt once Local reports the current version', () => {
    expect(renderToStaticMarkup(<LocalUpdateBanner update={{ available: false, required: false }} />)).toBe(
      '',
    );
    expect(renderToStaticMarkup(<LocalUpdateBanner update={null} />)).toBe('');
  });

  it('rejects malformed prompt responses and omits missing download links', () => {
    expect(isLocalUpdate({ available: true, required: 'yes' })).toBe(false);
    expect(isLocalUpdate({ available: true, required: true, downloadUrl: 'javascript:alert(1)' })).toBe(
      false,
    );
    const html = renderToStaticMarkup(<LocalUpdateBanner update={{ available: true, required: false }} />);
    expect(html).toContain('update is available');
    expect(html).not.toContain('href=');
  });
});
