import { describe, expect, it } from 'vitest';
import {
  ALLOWED_LEGACY_KEYS,
  keySuffix,
  matchAllowedKey,
  refusalReason,
  REFUSED_LEGACY_KEYS,
} from '@/plugins/canvas/migration/keys';

describe('the legacy browser-state allowlist', () => {
  it('never lists a key on both sides', () => {
    // A key that is both allowed and refused would export or not depending on
    // which loop ran first. There is no correct answer, so it must not happen.
    for (const allowed of ALLOWED_LEGACY_KEYS) {
      const probe = allowed.prefix ? `${allowed.key}x` : allowed.key;
      expect(refusalReason(probe), `${allowed.key} is both allowed and refused`).toBe('');
    }
  });

  it('refuses the key that holds credentials', () => {
    // The legacy app stored an Azure Foundry apiKey in cleartext here. A
    // migration that copies it copies it into every backup taken afterwards.
    expect(matchAllowedKey('fleetlog.llmConfig')).toBeNull();
    expect(refusalReason('fleetlog.llmConfig')).toContain('apiKey');
  });

  it('gives every refusal a stated reason', () => {
    for (const refused of REFUSED_LEGACY_KEYS) {
      expect(refused.reason.length, `${refused.key} has no reason`).toBeGreaterThan(20);
    }
  });

  it('exports Session Canvas workspaces by session', () => {
    const matched = matchAllowedKey('fleetlog.canvasWorkspace.v1:0f9a4d1e-2b3c');
    expect(matched?.kind).toBe('workspace');
    expect(keySuffix(matched!, 'fleetlog.canvasWorkspace.v1:0f9a4d1e-2b3c')).toBe('0f9a4d1e-2b3c');
  });

  it('refuses a prefixed key with nothing after the prefix', () => {
    // `fleetlog.canvasWorkspace.v1:` with no session is not a workspace; it is
    // a malformed key, and importing it would create a record keyed by nothing.
    expect(matchAllowedKey('fleetlog.canvasWorkspace.v1:')).toBeNull();
  });

  it('carries each product preference separately rather than by prefix sweep', () => {
    expect(matchAllowedKey('fleetlog.dagamaProject.v1')?.kind).toBe('preference');
    expect(matchAllowedKey('fleetlog.atlasProject.v1')?.kind).toBe('preference');
    expect(matchAllowedKey('fleetlog.dagamaBoardId.v1.demo')?.kind).toBe('preference');
    expect(matchAllowedKey('fleetlog.atlasRunId.v1.demo')?.kind).toBe('preference');
  });

  it('carries an unsaved draft, which exists nowhere else', () => {
    expect(matchAllowedKey('fleetlog.dagamaDraft.v1')?.kind).toBe('draft');
    expect(matchAllowedKey('fleetlog.atlasDraft.v1')?.kind).toBe('draft');
  });

  it('leaves Columbus behind, and says so', () => {
    // Columbus is not one of the ported products; its state has no destination.
    expect(matchAllowedKey('fleetlog.columbusWorkspace.v1')).toBeNull();
    expect(refusalReason('fleetlog.columbusWorkspace.v1')).toContain('not one of the ported products');
  });

  it('refuses an unknown key rather than guessing', () => {
    // A key added to the legacy app after this list was written is not
    // exported. Silence is the safe default when the contents are unknown.
    expect(matchAllowedKey('fleetlog.somethingAddedLater')).toBeNull();
    expect(matchAllowedKey('unrelated.app.key')).toBeNull();
  });
});
