import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { SyncChip } from './SyncChip';

describe('SyncChip', () => {
  it('exposes persistent row status as static text, not a live region', () => {
    const markup = renderToStaticMarkup(<SyncChip state="in_hub" />);

    expect(markup).toContain('In Hub');
    expect(markup).not.toContain('role="status"');
  });
});
