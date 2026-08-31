import { describe, expect, it } from 'vitest';
import { REMOTE_TEST_TIMEOUT_MS, remoteTestTimeoutMessage } from '@/pages/coslash/lib/remote-api';

describe('remote test timeout', () => {
  it('describes where to look after a client timeout', () => {
    expect(remoteTestTimeoutMessage(95_000)).toBe(
      'Remote test timed out after 95s. Check the coslash terminal and ~/.coslash/logs.',
    );
    expect(REMOTE_TEST_TIMEOUT_MS).toBe(95_000);
  });
});
