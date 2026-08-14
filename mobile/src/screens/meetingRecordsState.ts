import type { MeetingRecordDetail, MeetingRecordIndexItem, MeetingRecordReference } from '../api/types';

export function meetingRecordDetailRemainsCurrent(
  rows: MeetingRecordIndexItem[],
  selectedId: string,
  detail: MeetingRecordDetail | null,
) {
  if (!selectedId || !detail || detail.id !== selectedId) return false;
  const row = rows.find(candidate => candidate.id === selectedId);
  return Boolean(row && row.recordRevision === detail.recordRevision);
}

export function isDefinitiveMeetingRecordDenial(status: number) {
  return status === 401 || status === 403 || status === 404;
}

export function meetingRecordSourceScrollOffset(sectionY: number, rowY: number, inset: number) {
  const values = [sectionY, rowY, inset].map(value => Number.isFinite(value) ? value : 0);
  return Math.max(0, values[0] + values[1] - Math.max(0, values[2]));
}

export function meetingRecordReturnLabel(mode: 'recap' | 'transcript') {
  return `Back to live ${mode}`;
}

export function meetingRecordReferenceHasExactDestination(reference: MeetingRecordReference): boolean {
  const openId = String(reference.openId ?? '').trim();
  if (!openId) return false;
  return reference.openKind === 'project' || reference.openKind === 'artifact';
}
