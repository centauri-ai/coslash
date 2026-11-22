import { useState } from 'react';
import { SheetFooter } from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  ArtifactStats,
  CommandsSection,
  CommitsAndTodos,
  FilesChangedList,
} from '@/pages/coslash/components/session-inspector/ArtifactSections';
import { DigestSection } from '@/pages/coslash/components/session-inspector/DigestSection';
import {
  HandoffSection,
  ResumeSessionButton,
} from '@/pages/coslash/components/session-inspector/HandoffSection';
import { RecapSection } from '@/pages/coslash/components/session-inspector/RecapSection';
import { SkillsSection } from '@/pages/coslash/components/SkillsSection';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import {
  getVendor,
  isLocalSession,
  sessionKey,
  type DecisionEvidence,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

export function InspectorBody({
  detail,
  onOpenSynthesisSettings,
  exactDetailsAvailable,
  onSelectFile,
  remoteLaunchable,
  remoteLaunchHint,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  onOpenSynthesisSettings?: () => void;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const currentSessionKey = sessionKey(detail);
  const citationKey = `${currentSessionKey}@${detail.revision}`;
  const [citation, setCitation] = useState<{ key: string; evidence: DecisionEvidence } | null>(null);
  const [designDocRequest, setDesignDocRequest] = useState<{ key: string; path: string } | null>(null);
  const [tabSelection, setTabSelection] = useState<{
    sessionKey: string;
    tab: 'session' | 'files' | 'skills';
  }>(() => ({ sessionKey: currentSessionKey, tab: 'session' }));
  if (tabSelection.sessionKey !== currentSessionKey) {
    setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  }
  const selectedTab = tabSelection.sessionKey === currentSessionKey ? tabSelection.tab : 'session';
  const selectedTabAvailable =
    selectedTab === 'session' ||
    (selectedTab === 'files' && detail.fileEdits.length > 0) ||
    (selectedTab === 'skills' && (detail.skills?.length ?? 0) > 0);
  if (!selectedTabAvailable) setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  const activeTab = selectedTabAvailable ? selectedTab : 'session';
  const tabClassName =
    'text-coslash-muted h-auto flex-none rounded-none px-0 py-3 data-[state=active]:text-coslash-ink data-[state=active]:after:opacity-100 group-data-horizontal/tabs:after:bottom-0';
  // scroll on the outer div, layout on the inner one — flex children of a
  // scroll container shrink to fit instead of overflowing, which collapses
  // the overflow-hidden stat grids
  return (
    <Tabs
      key={currentSessionKey}
      value={activeTab}
      onValueChange={(value) =>
        setTabSelection({ sessionKey: currentSessionKey, tab: value as 'session' | 'files' | 'skills' })
      }
      className="min-h-0 flex-1 gap-0"
    >
      <TabsList
        variant="line"
        aria-label="Session views"
        className="w-full shrink-0 justify-start gap-4 border-b px-4 py-0 group-data-horizontal/tabs:h-auto"
      >
        <TabsTrigger value="session" className={tabClassName}>
          Session
        </TabsTrigger>
        {detail.fileEdits.length > 0 && (
          <TabsTrigger value="files" className={tabClassName}>
            Files · {detail.fileEdits.length}
          </TabsTrigger>
        )}
        {(detail.skills?.length ?? 0) > 0 && (
          <TabsTrigger value="skills" className={tabClassName}>
            Skills
          </TabsTrigger>
        )}
      </TabsList>
      <div className="flex-1 overflow-x-hidden overflow-y-auto">
        <TabsContent
          value="session"
          forceMount
          className="flex flex-col gap-2 px-4 py-4 data-[state=inactive]:hidden"
        >
          <HandoffSection
            detail={detail}
            exactDetailsAvailable={exactDetailsAvailable}
            remoteLaunchable={remoteLaunchable}
            remoteLaunchHint={remoteLaunchHint}
            handoff={handoff}
            onHandoffStarted={onHandoffStarted}
            onOpenTarget={onOpenTarget}
          />
          <RecapSection
            detail={detail}
            onOpenSynthesisSettings={onOpenSynthesisSettings}
            onEvidence={(evidence) => setCitation({ key: citationKey, evidence: { ...evidence } })}
            onOpenDesignDoc={(path) => {
              setTabSelection({ sessionKey: currentSessionKey, tab: 'files' });
              setDesignDocRequest({ key: currentSessionKey, path });
            }}
          />
          <ArtifactStats detail={detail} />
          <CommandsSection detail={detail} />
          <CommitsAndTodos detail={detail} />
          <DigestSection
            detail={detail}
            evidence={citation?.key === citationKey ? citation.evidence : null}
          />
        </TabsContent>
        <TabsContent value="files" forceMount className="px-4 py-4 data-[state=inactive]:hidden">
          <FilesChangedList
            detail={detail}
            onSelectFile={onSelectFile}
            focusRequest={designDocRequest?.key === currentSessionKey ? designDocRequest : null}
          />
        </TabsContent>
        <TabsContent value="skills" forceMount className="px-4 pb-4 data-[state=inactive]:hidden">
          <SkillsSection skills={detail.skills} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

export function InspectorFooter({
  detail,
  remoteLaunchable,
  remoteResumeHint,
}: {
  detail: SessionDetail;
  remoteLaunchable: boolean;
  remoteResumeHint?: string;
}) {
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';

  return (
    <SheetFooter className="bg-coslash-soft flex-row items-center justify-between gap-4 border-t">
      <div className="flex min-w-0 flex-col">
        <span className="text-xs">
          {opensCursor ? 'Open workspace in Cursor' : 'Resume this exact session'}
        </span>
        <span className="text-coslash-muted text-xs font-light">
          {isLocalSession(detail)
            ? opensCursor
              ? 'Cursor does not expose an IDE deep link, so this opens the workspace without restoring this chat.'
              : `Reopens this session in ${getVendor(detail.agent).label} with its full context.`
            : detail.agent === 'pi'
              ? 'Pi CLI resume is available locally only.'
              : remoteLaunchable
                ? `Opens this session in ${getVendor(detail.agent).label} through SSH.`
                : 'Available when the remote SSH host is connected.'}
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <ResumeSessionButton detail={detail} disabledHint={remoteResumeHint} />
      </div>
    </SheetFooter>
  );
}
