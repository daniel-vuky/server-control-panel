import type { ActivityEntry } from '../api/types.gen'

/** makeEntry is a finished restart of shop by a person at 10:12 on 1 Nov (+07:00), 4 s long; overrides win. */
export function makeEntry(overrides: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    id: 10,
    at: '2025-11-01T10:12:00+07:00',
    finishedAt: '2025-11-01T10:12:04+07:00',
    actor: 'dev@company.vn',
    actorKind: 'person',
    action: 'project.restart',
    kind: 'action',
    projectId: 1,
    projectTracked: true,
    target: 'shop',
    result: 'ok',
    reason: '',
    channels: [],
    ...overrides,
  }
}
