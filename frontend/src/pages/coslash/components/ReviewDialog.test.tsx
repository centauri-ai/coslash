import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Dialog } from '@/components/ui/dialog';
import { ReviewDialog, ReviewDialogContent } from '@/pages/coslash/components/ReviewDialog';

describe('ReviewDialogContent', () => {
  it('renders the reviewer picker and both actions', () => {
    const markup = renderToStaticMarkup(
      <Dialog>
        <ReviewDialogContent
          reviewers={[
            { id: 'codex', label: 'Codex', available: true },
            { id: 'claude', label: 'Claude Code', available: true },
          ]}
          selected="codex"
          state="idle"
          error={null}
          onSelect={() => undefined}
          onClose={() => undefined}
          onStart={() => undefined}
        />
      </Dialog>,
    );
    expect(markup).toContain('Choose a reviewer');
    expect(markup).toContain('Codex');
    expect(markup).toContain('Claude Code');
    expect(markup).toContain('Start review');
    expect(markup).toContain('Close');
  });

  it('disables start and announces progress while launching', () => {
    const markup = renderToStaticMarkup(
      <Dialog>
        <ReviewDialogContent
          reviewers={[{ id: 'codex', label: 'Codex', available: true }]}
          selected="codex"
          state="launching"
          error={null}
          onSelect={() => undefined}
          onClose={() => undefined}
          onStart={() => undefined}
        />
      </Dialog>,
    );
    expect(markup).toContain('Starting review');
    expect(markup).toContain('disabled');
  });

  it('shows a background review failure', () => {
    const markup = renderToStaticMarkup(
      <Dialog>
        <ReviewDialogContent
          reviewers={[{ id: 'codex', label: 'Codex', available: true }]}
          selected="codex"
          state="idle"
          error="review process exited"
          onSelect={() => undefined}
          onClose={() => undefined}
          onStart={() => undefined}
        />
      </Dialog>,
    );
    expect(markup).toContain('review process exited');
    expect(markup).toContain('role="alert"');
  });

  it('keeps triggerless review errors out of the surrounding layout', () => {
    const markup = renderToStaticMarkup(
      <ReviewDialog
        origin={{ sourceId: 'local', agent: 'codex', id: 'session' }}
        reviewerOptions={[{ id: 'claude', label: 'Claude Code', available: true }]}
        reviewError="review process exited"
        showTrigger={false}
        onStarted={() => undefined}
      />,
    );

    expect(markup).not.toContain('review process exited');
    expect(markup).not.toContain('role="alert"');
  });
});
