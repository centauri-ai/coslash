# T52 design data coverage

Source: `hub-tier5-ui/code/*/cases.json` `input` objects in the accepted tier5-02 design. Every distinct leaf path is listed; example device/session IDs remain in the path so coverage can be checked mechanically. `client` means no server fixture is needed.

| Design input path | Contract or client source | Fixture / evidence |
| --- | --- | --- |
| `actor` | Authenticated browser identity; client projection | `client` |
| `browserOs` | Browser platform detection | `client` |
| `devices[].appliedConfigVersion` | DeviceStatus.appliedConfigVersion | `device-status.json` |
| `devices[].contact` | DeviceStatus.lastContactAt | `device-status.json` |
| `devices[].id` | DeviceStatus.id | `device-status.json` |
| `devices[].installChannel` | DeviceStatus.installChannel | `device-status.json` |
| `devices[].online` | DeviceStatus.listening | `device-status.json` |
| `devices[].os` | DeviceStatus.os | `device-status.json` |
| `devices[].syncOff` | DeviceStatus.syncOff | `device-status.json` |
| `devices[].ver` | DeviceStatus.clientVersion vs ClientInstall.minVersion/recommendedVersion | `device-status.json`, `client-install.json` |
| `dialog` | coSlash Local UI state | `Local client` |
| `dialog.id` | coSlash Local UI state | `Local client` |
| `dialog.kind` | coSlash Local UI state | `Local client` |
| `mode` | coSlash Local UI state | `Local client` |
| `page` | Browser route | `client` |
| `selected` | coSlash Local UI state | `Local client` |
| `sessions[].agent` | coSlash Local session index agent | `Local client` |
| `sessions[].base` | coSlash Local session index base | `Local client` |
| `sessions[].id` | coSlash Local session index id | `Local client` |
| `sessions[].title` | coSlash Local session index title | `Local client` |
| `settings.configVersion` | GET /v4/settings configVersion | `settings.json` |
| `settings.paused` | GET /v4/settings paused | `settings.json` |
| `t5.catchUp.maya-mbp.done` | DeviceStatus.queue.firstSync.recentDone | `device-status.json` |
| `t5.catchUp.maya-mbp.total` | DeviceStatus.queue.firstSync.recentTotal | `device-status.json` |
| `t5.catchUp.nia-mbp.done` | DeviceStatus.queue.firstSync.recentDone | `device-status.json` |
| `t5.catchUp.nia-mbp.total` | DeviceStatus.queue.firstSync.recentTotal | `device-status.json` |
| `t5.checking` | SyncNowResponse.devices[].listening and browser request state | `sync-now-response.json` |
| `t5.checking.at.relMs` | SyncNowResponse.requestedAt; browser relative time | `sync-now-response.json` |
| `t5.checking.devs[]` | SyncNowResponse.devices[].id | `sync-now-response.json` |
| `t5.confirmOff` | Browser interaction state | `client` |
| `t5.lastContact.maya-air.relMs` | DeviceStatus.lastContactAt; relative time computed by browser | `device-status.json` |
| `t5.lastContact.maya-mbp.relMs` | DeviceStatus.lastContactAt; relative time computed by browser | `device-status.json` |
| `t5.lastContact.nia-mbp.relMs` | DeviceStatus.lastContactAt; relative time computed by browser | `device-status.json` |
| `t5.lastContact.omar-desktop.relMs` | DeviceStatus.lastContactAt; relative time computed by browser | `device-status.json` |
| `t5.lastSyncedAt.jo-desktop` | DeviceStatus.lastSyncedAt; browser formats timestamp | `device-status.json` |
| `t5.lastSyncedAt.maya-air` | DeviceStatus.lastSyncedAt; browser formats timestamp | `device-status.json` |
| `t5.lastSyncedAt.maya-mbp` | DeviceStatus.lastSyncedAt; browser formats timestamp | `device-status.json` |
| `t5.live.a3.relMs` | SSE session.updated event receipt time; browser clock | `client` |
| `t5.pending.maya-mbp[]` | DeviceStatus.queue.pending (count); browser supplies row IDs when needed | `device-status.json` |
| `t5.prompt` | Browser interaction state | `client` |
| `t5.prompt.dev` | Browser interaction state | `client` |
| `t5.prompt.kind` | Browser interaction state | `client` |
| `t5.setup` | Onboarding state object or null, assembled by browser | `client` |
| `t5.setup.auto` | GET /v1/device-onboardings/{id}.autoSync; local switch before approval | `onboarding-status.json` |
| `t5.setup.code` | POST /v1/device-onboardings.connectCode | `onboarding-create.json` |
| `t5.setup.copied` | Copy interaction count | `client` |
| `t5.setup.created.relMs` | POST /v1/device-onboardings.createdAt; browser relative time | `onboarding-create.json` |
| `t5.setup.devId` | GET /v1/device-onboardings/{id}.deviceId after exchange | `onboarding-status.json` |
| `t5.setup.dl` | Download interaction state | `client` |
| `t5.setup.exp.relMs` | POST /v1/device-onboardings.expiresAt; browser relative time | `onboarding-create.json` |
| `t5.setup.legacy` | Absence of connectCode on legacy create response | `onboarding-create-legacy.json` |
| `t5.setup.n` | Local setup retry/count state | `client` |
| `t5.setup.name` | GET /v1/device-onboardings/{id}.deviceName | `onboarding-status.json` |
| `t5.setup.stage` | GET /v1/device-onboardings/{id}.state | `onboarding-status.json` |
| `t5.setup.tab` | Selected install tab | `client` |
| `t5.setup.what` | Disclosure toggle | `client` |
| `t5.syncNowUnavailable` | POST /v4/devices/sync-now returns 404 when disabled | `sync-now-unavailable.json` |
| `t5.wake` | Browser interaction state | `client` |
| `t5.wake.at.relMs` | Browser interaction state | `client` |
| `t5.wake.dev` | Browser interaction state | `client` |
| `t5.wake.phase` | Browser interaction state | `client` |

Coverage: 61 distinct input leaf paths; 61 mapped. The device and setup fixtures are synthetic. Fields represented as relative times in the prototype use server timestamps and browser time at runtime.
