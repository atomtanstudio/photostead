/**
 * Keeps only tweets belonging to one conversation / reply thread.
 */

export function scoreTweetCompleteness(tweet) {
  if (!tweet) return 0;
  let score = (tweet.full_text?.length || 0) * 2;
  score += (tweet.article?.plain_text?.length || 0) * 3;
  score += (tweet.extended_entities?.media?.length || 0) * 50;
  if (tweet.card?.binding_values?.length || tweet.card?.legacy?.binding_values?.length) score += 500;
  if (tweet.note_tweet) score += 200;
  if (tweet._rawTweet) score += 50;
  return score;
}

export function dedupeTweetsPreferComplete(tweets) {
  const byId = new Map();
  for (const t of tweets) {
    const id = t?.rest_id || t?.id_str;
    if (!id) continue;
    const key = String(id);
    const existing = byId.get(key);
    if (!existing || scoreTweetCompleteness(t) > scoreTweetCompleteness(existing)) {
      byId.set(key, t);
    }
  }
  return [...byId.values()];
}

function isReplyDescendantOf(tweet, ancestorId, byId) {
  const seen = new Set();
  let cur = tweet;
  while (cur?.in_reply_to_status_id_str) {
    const pid = String(cur.in_reply_to_status_id_str);
    if (pid === String(ancestorId)) return true;
    if (seen.has(pid)) break;
    seen.add(pid);
    cur = byId.get(pid);
  }
  return false;
}

/**
 * Restrict to the focal post's conversation. Drops Discover-more tweets,
 * unrelated threads, and duplicate standalone quote cards.
 */
/**
 * Bulk likes/bookmarks should keep the focal post plus direct replies only.
 * When the liked tweet is itself a reply, use the conversation root so sibling
 * replies on article posts are not dropped.
 */
export function conversationRootIdForTweet(focal, tweets) {
  if (!focal) return null;
  const focalId = String(focal.rest_id || focal.id_str || "");
  if (!focalId) return null;

  const byId = new Map();
  for (const tweet of tweets || []) {
    const id = tweet?.rest_id || tweet?.id_str;
    if (id) byId.set(String(id), tweet);
  }

  const conversationId = focal.conversation_id_str ? String(focal.conversation_id_str) : "";
  if (conversationId && conversationId !== focalId) return conversationId;

  const parentId = focal.in_reply_to_status_id_str ? String(focal.in_reply_to_status_id_str) : "";
  if (!parentId || parentId === focalId) return focalId;

  const seen = new Set([focalId]);
  let currentParentId = parentId;
  while (currentParentId && !seen.has(currentParentId)) {
    seen.add(currentParentId);
    const parent = byId.get(currentParentId);
    if (!parent) return currentParentId;
    const parentConversationId = parent.conversation_id_str ? String(parent.conversation_id_str) : "";
    const parentTweetId = String(parent.rest_id || parent.id_str || "");
    if (parentConversationId && parentConversationId !== parentTweetId) {
      return parentConversationId;
    }
    const grandParentId = parent.in_reply_to_status_id_str ? String(parent.in_reply_to_status_id_str) : "";
    if (!grandParentId || grandParentId === currentParentId) return currentParentId;
    currentParentId = grandParentId;
  }

  return parentId;
}

export function filterTweetsForBulkFeed(tweets, focalTweetId, options = {}) {
  if (!tweets?.length || !focalTweetId) return tweets || [];
  const focalId = String(focalTweetId);
  const includeIds = new Set([focalId]);
  if (options.includeTweetId) includeIds.add(String(options.includeTweetId));
  return tweets.filter((tweet) => {
    const id = String(tweet?.rest_id || tweet?.id_str || "");
    if (includeIds.has(id)) return true;
    return String(tweet?.in_reply_to_status_id_str || "") === focalId;
  });
}

export function filterTweetsToConversation(tweets, focalTweetId) {
  if (!tweets?.length || !focalTweetId) return tweets || [];

  const byId = new Map();
  for (const t of tweets) {
    const id = t?.rest_id || t?.id_str;
    if (id) byId.set(String(id), t);
  }

  const focal = byId.get(String(focalTweetId));
  if (!focal) {
    return tweets.filter((t) => String(t.rest_id || t.id_str) === String(focalTweetId));
  }

  const conversationId = String(focal.conversation_id_str || focalTweetId);

  const quotedOnlyIds = new Set();
  for (const t of byId.values()) {
    const qid = t.quoted_tweet?.rest_id || t.quoted_tweet?.id_str;
    if (qid) quotedOnlyIds.add(String(qid));
  }

  const kept = [];
  for (const t of byId.values()) {
    const id = String(t.rest_id || t.id_str);

    if (id === String(focalTweetId)) {
      kept.push(t);
      continue;
    }

    if (String(t.conversation_id_str || "") !== conversationId) {
      continue;
    }

    if (
      quotedOnlyIds.has(id) &&
      !isReplyDescendantOf(t, focalTweetId, byId) &&
      String(t.in_reply_to_status_id_str || "") !== String(focalTweetId)
    ) {
      continue;
    }

    kept.push(t);
  }

  return kept;
}

export function sortConversationTweets(tweets, focalTweetId) {
  const focal = tweets.find((t) => String(t.rest_id || t.id_str) === String(focalTweetId));
  const convId = focal?.conversation_id_str || focalTweetId;

  const roots = tweets.filter((t) => {
    const parent = t.in_reply_to_status_id_str;
    if (!parent) return true;
    return !tweets.some((x) => String(x.rest_id || x.id_str) === String(parent));
  });

  const ordered = [];
  const seen = new Set();

  function walk(t) {
    const id = String(t.rest_id || t.id_str);
    if (seen.has(id)) return;
    seen.add(id);
    ordered.push(t);
    const replies = tweets
      .filter((r) => String(r.in_reply_to_status_id_str) === id)
      .sort((a, b) => parseTweetDate(a) - parseTweetDate(b));
    for (const r of replies) walk(r);
  }

  const focalRoot =
    roots.find((r) => String(r.rest_id || r.id_str) === String(convId)) ||
    roots.find((r) => String(r.rest_id || r.id_str) === String(focalTweetId)) ||
    focal;

  if (focalRoot) walk(focalRoot);
  for (const r of roots.sort((a, b) => parseTweetDate(a) - parseTweetDate(b))) {
    walk(r);
  }
  for (const t of tweets) {
    const id = String(t.rest_id || t.id_str);
    if (!seen.has(id)) ordered.push(t);
  }

  return pinFocalFirst(ordered, focalTweetId);
}

export function pinFocalFirst(tweets, focalTweetId) {
  if (!focalTweetId || !tweets?.length) return tweets || [];
  const id = String(focalTweetId);
  const idx = tweets.findIndex((t) => String(t.rest_id || t.id_str) === id);
  if (idx <= 0) return tweets;
  const copy = [...tweets];
  const [focal] = copy.splice(idx, 1);
  copy.unshift(focal);
  return copy;
}

function parseTweetDate(t) {
  const d = new Date(t?.created_at || 0);
  return Number.isNaN(d.getTime()) ? 0 : d.getTime();
}
