import { extractTweetsFromApiResponse, harvestReplyIdsFromTweetDetail } from "./normalize.js";
import { filterTweetStatusIds, isTweetStatusId } from "./tweet-ids.js";
import {
  dedupeTweetsPreferComplete,
  conversationRootIdForTweet,
  filterTweetsForBulkFeed,
  filterTweetsToConversation,
  sortConversationTweets,
} from "./conversation.js";

/**
 * Collect every tweet ID referenced across TweetDetail pages (stops at Discover more per page).
 */
export function collectAllIdsFromApiResponses(apiResponses, focalTweetId) {
  const ids = new Set();
  if (isTweetStatusId(focalTweetId)) ids.add(String(focalTweetId));
  for (const block of apiResponses || []) {
    if (!block?.data) continue;
    for (const id of harvestReplyIdsFromTweetDetail(block.data)) {
      if (isTweetStatusId(id)) ids.add(id);
    }
    for (const t of extractTweetsFromApiResponse(block.data, { forForensic: true })) {
      if (t.rest_id && isTweetStatusId(t.rest_id)) ids.add(String(t.rest_id));
      if (t.in_reply_to_status_id_str && isTweetStatusId(t.in_reply_to_status_id_str)) {
        ids.add(String(t.in_reply_to_status_id_str));
      }
    }
  }
  return ids;
}

export function mergeApiResponsesIntoTweets(apiResponses, focalTweetId, options = {}) {
  const all = [];
  for (const block of apiResponses || []) {
    if (block?.data) {
      all.push(...extractTweetsFromApiResponse(block.data, { forForensic: true }));
    }
  }
  let tweets = dedupeTweetsPreferComplete(all);
  tweets = filterTweetsToConversation(tweets, focalTweetId);
  if (options.bulkFeed) {
    const focal = tweets.find((tweet) => String(tweet?.rest_id || tweet?.id_str) === String(focalTweetId));
    const bulkFocalId = focal ? conversationRootIdForTweet(focal, tweets) : String(focalTweetId);
    tweets = filterTweetsForBulkFeed(tweets, bulkFocalId, { includeTweetId: focalTweetId });
  }
  return sortConversationTweets(tweets, focalTweetId);
}

export function findMissingTweetIds(collectedIds, tweets) {
  const have = new Set(tweets.map((t) => String(t.rest_id || t.id_str)));
  return [...collectedIds].filter((id) => !have.has(id));
}

export function computeGapFillIds({ apiResponses, focalTweetId, tweets, domForensic, max = 20 }) {
  const collected = collectAllIdsFromApiResponses(apiResponses, focalTweetId);
  const missing = findMissingTweetIds(collected, tweets);
  const have = new Set(tweets.map((t) => String(t.rest_id || t.id_str)));
  for (const id of domForensic?.visibleReplyIds || []) {
    const sid = String(id);
    if (!have.has(sid) && !missing.includes(sid)) missing.push(sid);
  }
  return filterTweetStatusIds(missing).slice(0, max);
}
