import type { LocalUpdate } from '@/pages/coslash/lib/local-update';

export function LocalUpdateBanner({ update }: { update: LocalUpdate | null }) {
  if (!update?.available) return null;
  return (
    <div
      role="alert"
      className="bg-info-bg text-info-fg flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-sm"
    >
      <span>
        {update.required ? 'Update required to resume sync.' : 'A coSlash Local update is available.'}{' '}
        Download {update.version ? `version ${update.version}` : 'the new version'} and manually replace this
        app. Your paired device identity stays in the OS keychain.
      </span>
      {update.downloadUrl && (
        <a className="underline" href={update.downloadUrl} target="_blank" rel="noopener noreferrer">
          Download coSlash Local
        </a>
      )}
    </div>
  );
}
