/**
 * Assembles chunked forensic API payloads in the service worker (off the X tab main thread).
 */

import { buildForensicBundleRecord } from "./forensic-bundle.js";
import {
  collectAllIdsFromApiResponses,
  computeGapFillIds,
  findMissingTweetIds,
} from "./reply-backfill.js";

const sessions = new Map();

const SESSION_TTL_MS = 10 * 60 * 1000;
const MAX_GAP_FILL = 20;

function cleanupSessions() {
  const now = Date.now();
  for (const [id, s] of sessions) {
    if (now - s.startedAt > SESSION_TTL_MS) sessions.delete(id);
  }
}

export function beginForensicSession({ sessionId, tweetId, pageUrl, bulkCapture = false }) {
  cleanupSessions();
  sessions.set(sessionId, {
    sessionId,
    tweetId,
    pageUrl,
    bulkCapture: !!bulkCapture,
    apiResponses: [],
    domForensic: null,
    articleCapture: null,
    warnings: [],
    partial: false,
    gapFillResponseCount: 0,
    startedAt: Date.now(),
  });
}

export function appendForensicChunk(sessionId, chunk) {
  const s = sessions.get(sessionId);
  if (!s) return { error: "Unknown forensic session" };
  if (Array.isArray(chunk)) s.apiResponses.push(...chunk);
  return { ok: true, count: s.apiResponses.length };
}

async function buildRecordFromSession(s) {
  const captureId = crypto.randomUUID();
  const record = await buildForensicBundleRecord({
    captureId,
    focalTweetId: s.tweetId,
    capturedAt: new Date().toISOString(),
    pageUrl: s.pageUrl || "",
    apiResponses: s.apiResponses,
    domForensic: s.domForensic,
    articleCapture: s.articleCapture,
    captureWarnings: s.warnings,
    bulkCapture: s.bulkCapture,
  });

  const allIds = collectAllIdsFromApiResponses(s.apiResponses, s.tweetId);
  const missingFromApi = findMissingTweetIds(allIds, record.tweets);
  const gapIds = computeGapFillIds({
    apiResponses: s.apiResponses,
    focalTweetId: s.tweetId,
    tweets: record.tweets,
    domForensic: s.domForensic,
    max: MAX_GAP_FILL,
  });
  const expectedReplyCount = Number(record.summary?.expectedReplyCount);
  const tweetCount = Number(record.summary?.tweetCount ?? 0);
  const publicReplyCountShortfall = Number.isFinite(expectedReplyCount)
    ? Math.max(0, expectedReplyCount + 1 - tweetCount)
    : 0;
  const knownIncomplete = missingFromApi.length > 0 || gapIds.length > 0;
  const unresolvedPublicCountShortfall =
    publicReplyCountShortfall > 0 && !knownIncomplete && !s.gapFillResponseCount;
  const captureIncomplete = knownIncomplete || unresolvedPublicCountShortfall;

  record.summary = {
    ...record.summary,
    captureIncomplete,
    replyCountShortfall: publicReplyCountShortfall,
    missingReplyIds: gapIds.slice(0, 30),
    missingReplyCount: gapIds.length,
    apiIdsNotInCapture: missingFromApi.slice(0, 20),
    partial:
      s.partial ||
      captureIncomplete ||
      gapIds.length > 0,
  };
  record.partial = record.summary.partial;

  return { record, missing: gapIds, captureIncomplete };
}

export async function finishForensicSession({
  sessionId,
  domForensic,
  articleCapture,
  warnings = [],
  partial = false,
  bulkCapture = false,
}) {
  const s = sessions.get(sessionId);
  if (!s) return { error: "Unknown forensic session" };

  s.domForensic = domForensic;
  s.articleCapture = articleCapture;
  s.warnings = warnings;
  s.partial = partial;
  if (bulkCapture) s.bulkCapture = true;

  const { record, missing, captureIncomplete } = await buildRecordFromSession(s);
  const missingIds = missing.slice(0, MAX_GAP_FILL);
  const skipGapFill = !!s.bulkCapture;
  const needsGapFill = !skipGapFill && (missingIds.length > 0 || captureIncomplete);

  if (skipGapFill && (missingIds.length > 0 || captureIncomplete)) {
    record.summary = {
      ...record.summary,
      partial: true,
      bulkCaptureSkippedGapFill: true,
    };
    record.partial = true;
  }

  if (needsGapFill) {
    return {
      needsGapFill: true,
      sessionId,
      missingIds,
      extraPagination: captureIncomplete,
      expectedReplyCount: record.summary?.expectedReplyCount ?? null,
      record,
      tweetId: s.tweetId,
    };
  }

  sessions.delete(sessionId);
  return { record, needsGapFill: false, missingIds: [] };
}

export async function finishForensicGapFill({ sessionId, gapResponses = [] }) {
  const s = sessions.get(sessionId);
  if (!s) return { error: "Unknown forensic session" };

  if (Array.isArray(gapResponses) && gapResponses.length) {
    s.apiResponses.push(...gapResponses);
    s.gapFillResponseCount += gapResponses.length;
    s.warnings.push(`gap-fill: fetched ${gapResponses.length} missing reply(s)`);
  }

  const { record, missing } = await buildRecordFromSession(s);
  if (missing.length) {
    s.warnings.push(`still missing ${missing.length} reply id(s) after gap-fill`);
    record.summary = {
      ...record.summary,
      stillMissingReplyIds: missing.slice(0, 20),
      partial: true,
    };
  }

  sessions.delete(sessionId);
  return { record };
}
