import { extractTweetsFromApiResponse } from "./normalize.js";
import { isDiscoverMoreBoundary } from "./thread-filter.js";

const SEARCH_QUERY_ID = "Yw6L66Pw54NHKuq4Dp7b4Q";

/**
 * Extract tweets from SearchTimeline response (stops at Discover more in results).
 */
export function extractTweetsFromSearchResponse(data) {
  const instructions = deepGet(
    data,
    "data",
    "search_by_raw_query",
    "search_timeline",
    "timeline",
    "instructions"
  );
  if (!instructions) return [];

  const tweets = [];
  for (const instruction of instructions) {
    for (const entry of instruction.entries || []) {
      if (isDiscoverMoreBoundary(entry)) return tweets;
      const node = entry?.content?.itemContent?.tweet_results?.result;
      if (node) {
        const fromApi = extractTweetsFromApiResponse({ data: { tweetResult: { result: node } } }, {
          forForensic: true,
        });
        tweets.push(...fromApi);
      }
    }
  }
  return tweets;
}

function deepGet(obj, ...keys) {
  let cur = obj;
  for (const k of keys) {
    if (cur == null) return undefined;
    cur = cur[k];
  }
  return cur;
}

export function extractSearchCursor(data) {
  const instructions = deepGet(
    data,
    "data",
    "search_by_raw_query",
    "search_timeline",
    "timeline",
    "instructions"
  );
  if (!instructions) return null;
  let bottomCursor = null;
  let anyCursor = null;
  for (const instruction of instructions) {
    for (const entry of instruction.entries || []) {
      if (isDiscoverMoreBoundary(entry)) return null;
      const entryId = String(entry.entryId || "").toLowerCase();
      const c =
        entry?.content?.value ||
        entry?.content?.timelineCursor?.value ||
        entry?.content?.cursor?.value;
      if (!c || !entryId.includes("cursor")) continue;
      anyCursor = c;
      if (entryId.includes("bottom") || entryId.includes("showmore")) {
        bottomCursor = c;
      }
    }
  }
  return bottomCursor || anyCursor;
}

export function getSearchConversationQueryId() {
  return SEARCH_QUERY_ID;
}
