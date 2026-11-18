import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { TooltipProvider } from '@/components/ui/tooltip';
import { SessionId, TokenBreakdown } from '@/pages/coslash/components/SessionCard';

describe('TokenBreakdown', () => {
  it('renders unavailable usage for an empty token map', () => {
    expect(renderToStaticMarkup(<TokenBreakdown tokens={{}} />)).toContain('—');
  });
});

describe('SessionId', () => {
  const render = (id: string) =>
    renderToStaticMarkup(
      <TooltipProvider>
        <SessionId id={id} shortened />
      </TooltipProvider>,
    );

  it('shows the first group of a UUID', () => {
    expect(render('0a1b2c3d-4e5f-6789-abcd-ef0123456789')).toContain('>0a1b2c3d<');
  });

  it('shortens a long ID without a dash and still copies the full ID', () => {
    const markup = render('ses_1f02be5380018eft6ZPPtCTXc1');
    expect(markup).toContain('>ses_1f02be53…<');
    expect(markup).toContain('Copy session ID ses_1f02be5380018eft6ZPPtCTXc1');
  });

  it('keeps a short ID as it is', () => {
    expect(render('abc123')).toContain('>abc123<');
  });
});
