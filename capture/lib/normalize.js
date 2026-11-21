import { isDiscoverMoreBoundary, instructionIsDiscoverMoreSection } from "./thread-filter.js";
import { isTweetStatusId } from "./tweet-ids.js";

/**
 * Extracts forensic tweet records from X GraphQL / API JSON payloads.
 */

function deepGet(obj, ...keys) {
  let cur = obj;
  for (const k of keys) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = cur[k];
  }
  return cur;
}

function walkObjects(root, visitor, seen = new WeakSet()) {
  if (root == null || typeof root !== "object") return;
  if (seen.has(root)) return;
  seen.add(root);

  visitor(root);

  if (Array.isArray(root)) {
    for (const item of root) walkObjects(item, visitor, seen);
    return;
  }

  for (const value of Object.values(root)) {
    walkObjects(value, visitor, seen);
  }
}

function unwrapTweetResult(node) {
  if (!node || typeof node !== "object") return null;
  if (node.__typename === "Tweet") return node;
  if (node.__typename === "TweetWithVisibilityResults") {
    return unwrapTweetResult(node.tweet || node.result);
  }
  if (node.tweet) return unwrapTweetResult(node.tweet);
  if (node.legacy && (node.rest_id || node.legacy.id_str)) return node;
  if (node.result) return unwrapTweetResult(node.result);
  if (node.tweet_results?.result) return unwrapTweetResult(node.tweet_results.result);
  if (node.tweetResult?.result) return unwrapTweetResult(node.tweetResult.result);
  return null;
}

function extractUser(userNode) {
  if (!userNode) return null;
  const core = userNode.core || userNode.legacy || userNode;
  const legacy = userNode.legacy || {};
  const avatar = userNode.avatar || {};
  return {
    rest_id: userNode.rest_id || legacy.id_str || core.user_id,
    id_str: legacy.id_str || userNode.rest_id,
    screen_name: core.screen_name || legacy.screen_name,
    name: core.name || legacy.name,
    created_at: legacy.created_at || core.created_at,
    description: legacy.description || core.description,
    location: legacy.location,
    url: legacy.url,
    verified: userNode.is_blue_verified ?? legacy.verified ?? false,
    verified_type: userNode.verification?.verified_type,
    followers_count: legacy.followers_count,
    friends_count: legacy.friends_count,
    statuses_count: legacy.statuses_count,
    profile_image_url_https:
      legacy.profile_image_url_https ||
      avatar.image_url ||
      deepGet(userNode, "avatar", "image_url"),
    profile_banner_url: legacy.profile_banner_url,
    pinned_tweet_ids_str: legacy.pinned_tweet_ids_str,
    professional: userNode.professional,
    _raw: userNode,
  };
}

function extractMediaEntities(mediaList) {
  if (!Array.isArray(mediaList)) return [];
  return mediaList.map((m) => ({
    id_str: m.id_str,
    media_key: m.media_key,
    type: m.type,
    url: m.url,
    media_url_https: m.media_url_https,
    display_url: m.display_url,
    expanded_url: m.expanded_url,
    ext_alt_text: m.ext_alt_text,
    sizes: m.sizes,
    original_info: m.original_info,
    video_info: m.video_info,
    additional_media_info: m.additional_media_info,
    features: m.features,
    source_status_id_str: m.source_status_id_str,
    source_user_id_str: m.source_user_id_str,
    allow_download_status: m.allow_download_status,
    _raw: m,
  }));
}

function extractArticle(noteTweet, articleResult) {
  const article = articleResult || deepGet(noteTweet, "article", "article_results", "result");
  if (!article && !noteTweet) return null;

  return {
    article_id: article?.rest_id || article?.id,
    title: article?.title,
    preview_text: article?.preview_text,
    cover_media: article?.cover_media,
    content_state: article?.content_state,
    plain_text: article?.plain_text,
    metadata: article?.metadata,
    lifecycle_state: article?.lifecycle_state,
    note_tweet_text: noteTweet?.note_tweet_results?.result?.text,
    note_tweet_richtext: noteTweet?.note_tweet_results?.result?.richtext,
    entity_set: noteTweet?.note_tweet_results?.result?.entity_set,
    media: noteTweet?.media?.inline_media,
    _rawArticle: article,
    _rawNoteTweet: noteTweet,
  };
}

function normalizeTweet(tweetNode) {
  const tweet = unwrapTweetResult(tweetNode);
  if (!tweet) return null;

  const legacy = tweet.legacy || {};
  const noteTweet = tweet.note_tweet || tweet.note_tweet_results;
  const views = tweet.views || legacy.ext_views;
  const editControl = tweet.edit_control;
  const card = tweet.card?.legacy || tweet.card;
  const articleBlock = extractArticle(
    noteTweet || tweet.note_tweet,
    deepGet(tweet, "article", "article_results", "result") || tweet.article
  );

  let quoted = null;
  const quotedResult =
    tweet.quoted_status_result?.result ||
    tweet.quoted_status_result ||
    legacy.quoted_status_result;
  if (quotedResult) {
    quoted = normalizeTweet(quotedResult);
  }

  const userResult =
    tweet.core?.user_results?.result ||
    tweet.core?.user_results ||
    tweet.user ||
    legacy.user;

  const normalized = {
    rest_id: tweet.rest_id || legacy.id_str,
    id_str: legacy.id_str || tweet.rest_id,
    conversation_id_str: legacy.conversation_id_str,
    created_at: legacy.created_at,
    full_text: legacy.full_text,
    lang: legacy.lang,
    source: legacy.source,
    possibly_sensitive: legacy.possibly_sensitive,
    in_reply_to_status_id_str: legacy.in_reply_to_status_id_str,
    in_reply_to_user_id_str: legacy.in_reply_to_user_id_str,
    in_reply_to_screen_name: legacy.in_reply_to_screen_name,
    is_quote_status: legacy.is_quote_status,
    retweeted_status_id_str: legacy.retweeted_status_id_str,
    retweeted_status_result: legacy.retweeted_status_result,
    favorite_count: legacy.favorite_count,
    retweet_count: legacy.retweet_count,
    reply_count: legacy.reply_count,
    quote_count: legacy.quote_count,
    bookmark_count: tweet.bookmark_count ?? legacy.bookmark_count,
    views: views,
    entities: legacy.entities || tweet.entities,
    extended_entities: legacy.extended_entities
      ? {
          ...legacy.extended_entities,
          media: extractMediaEntities(legacy.extended_entities.media),
        }
      : null,
    display_text_range: legacy.display_text_range,
    coordinates: legacy.coordinates,
    geo: legacy.geo,
    place: legacy.place,
    card,
    article: articleBlock,
    note_tweet: noteTweet,
    edit_control: editControl,
    edits: tweet.edits,
    unmention_data: tweet.unmention_data,
    community: tweet.community,
    birdwatch_pivot: tweet.birdwatch_pivot,
    grok_analysis: tweet.grok_analysis || tweet.grok_analysis_button,
    author: extractUser(userResult),
    quoted_tweet: quoted,
    withheld_scope: tweet.withheld_scope || legacy.withheld_scope,
    limited_actions: tweet.limited_actions,
    visibility_results: tweet.tweet_visibility_results,
    __typename: tweet.__typename,
    _rawTweet: tweet,
  };

  if (noteTweet && !normalized.full_text) {
    const noteText = deepGet(noteTweet, "note_tweet_results", "result", "text");
    if (noteText) normalized.full_text = noteText;
  }

  return normalized;
}

function extractTweetNodesFromEntry(entry) {
  const nodes = [];
  const push = (n) => { if (n) nodes.push(n); };
  push(entry?.content?.itemContent?.tweet_results?.result);
  push(entry?.itemContent?.tweet_results?.result);
  for (const item of entry?.content?.items || entry?.items || []) {
    push(item?.item?.itemContent?.tweet_results?.result);
    push(item?.itemContent?.tweet_results?.result);
    push(item?.item?.tweet_results?.result);
    push(item?.tweet_results?.result);
    push(item?.content?.itemContent?.tweet_results?.result);
  }
  return nodes;
}

function collectTweetsFromTimelineInstructions(instructions, options = {}) {
  const { stopAtDiscoverMore = false } = options;
  const tweets = [];
  if (!Array.isArray(instructions)) return tweets;

  for (const instruction of instructions) {
    const entries = instruction.entries || instruction.moduleItems || [];
    for (const entry of entries) {
      if (stopAtDiscoverMore && isDiscoverMoreBoundary(entry)) return tweets;

      for (const node of extractTweetNodesFromEntry(entry)) {
        const t = normalizeTweet(node);
        if (t) tweets.push(t);
      }
    }
  }
  return tweets;
}

/**
 * Forensic-safe extraction: no global walk (avoids unrelated threads / promos).
 */
export function extractTweetsFromApiResponse(response, options = {}) {
  const { focalTweetId = null, forForensic = false } = options;
  const tweets = [];
  const seenIds = new Set();

  function add(t) {
    if (!t?.rest_id && !t?.id_str) return;
    const id = t.rest_id || t.id_str;
    if (seenIds.has(id)) return;
    seenIds.add(id);
    tweets.push(t);
  }

  const threaded = deepGet(
    response,
    "data",
    "threaded_conversation_with_injections_v2",
    "instructions"
  );
  if (threaded) {
    for (const t of collectTweetsFromTimelineInstructions(threaded, {
      stopAtDiscoverMore: true,
    })) {
      add(t);
    }
    return tweets;
  }

  const tweetResult = deepGet(response, "data", "tweetResult", "result");
  if (tweetResult) {
    add(normalizeTweet(tweetResult));
    return tweets;
  }

  const searchInstructions = deepGet(
    response,
    "data",
    "search_by_raw_query",
    "search_timeline",
    "timeline",
    "instructions"
  );
  if (searchInstructions) {
    for (const t of collectTweetsFromTimelineInstructions(searchInstructions, {
      stopAtDiscoverMore: true,
    })) {
      add(t);
    }
    return tweets;
  }

  if (forForensic) {
    return tweets;
  }

  return extractTweetsFromResponseLegacy(response);
}

function extractTweetsFromResponseLegacy(response) {
  const tweets = [];
  const seenIds = new Set();

  function add(t) {
    if (!t?.rest_id && !t?.id_str) return;
    const id = t.rest_id || t.id_str;
    if (seenIds.has(id)) return;
    seenIds.add(id);
    tweets.push(t);
  }

  walkObjects(response, (obj) => {
    if (obj?.__typename === "Tweet" && (obj.rest_id || obj.legacy?.id_str)) {
      add(normalizeTweet(obj));
      return;
    }
    if (obj?.tweetResult?.result) add(normalizeTweet(obj.tweetResult.result));
    if (obj?.tweet_results?.result) add(normalizeTweet(obj.tweet_results.result));
  });

  const homeInstructions =
    deepGet(response, "data", "home", "home_timeline_urt", "instructions") ||
    deepGet(response, "data", "user", "result", "timeline", "timeline", "instructions");
  if (homeInstructions) {
    for (const t of collectTweetsFromTimelineInstructions(homeInstructions)) add(t);
  }

  const searchInstructions = deepGet(
    response,
    "data",
    "search_by_raw_query",
    "search_timeline",
    "timeline",
    "instructions"
  );
  if (searchInstructions) {
    for (const t of collectTweetsFromTimelineInstructions(searchInstructions)) add(t);
  }

  return tweets;
}

export function extractTweetsFromResponse(response) {
  return extractTweetsFromApiResponse(response, { forForensic: false });
}

export function buildForensicRecord({
  captureId,
  capturedAt,
  pageUrl,
  request,
  response,
  httpStatus,
  ok,
  manual,
}) {
  const tweets = extractTweetsFromResponse(response);
  const operationName = request?.operationName || "unknown";

  return {
    schemaVersion: 1,
    captureId,
    capturedAt,
    pageUrl,
    manual: !!manual,
    source: {
      operationName,
      queryId: request?.queryId,
      method: request?.method,
      url: request?.url,
      variables: request?.variables,
      features: request?.features,
    },
    http: { status: httpStatus, ok },
    summary: {
      tweetCount: tweets.length,
      tweetIds: tweets.map((t) => t.rest_id || t.id_str).filter(Boolean),
    },
    tweets,
    rawResponse: response,
  };
}

export async function sha256Hex(text) {
  const data = new TextEncoder().encode(text);
  const hash = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}


export function harvestReplyIdsFromTweetDetail(data) {
  const ids = new Set();
  const instructions =
    data?.data?.threaded_conversation_with_injections_v2?.instructions || [];
  for (const instruction of instructions) {
    if (instructionIsDiscoverMoreSection(instruction)) break;
    for (const entry of instruction.entries || []) {
      if (isDiscoverMoreBoundary(entry)) continue;
      for (const node of extractTweetNodesFromEntry(entry)) {
        const t = unwrapTweetResult(node);
        const id = t?.rest_id || t?.legacy?.id_str;
        if (id && isTweetStatusId(id)) ids.add(String(id));
      }
    }
  }
  const focal = deepGet(data, "data", "tweetResult", "result");
  if (focal) {
    const t = unwrapTweetResult(focal);
    const id = t?.rest_id || t?.legacy?.id_str;
    if (id && isTweetStatusId(id)) ids.add(String(id));
  }
  return ids;
}
