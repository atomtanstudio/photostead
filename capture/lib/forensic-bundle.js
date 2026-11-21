import { sha256Hex, harvestReplyIdsFromTweetDetail } from "./normalize.js";
import { findArticleNodesInGraphQL, mergeArticleRecords } from "./article.js";
import { mergeApiResponsesIntoTweets } from "./reply-backfill.js";
import { conversationRootIdForTweet } from "./conversation.js";
import { getTweetMediaList, parseArticleBodySegments, filterInlineArticleMedia } from "./media.js";

function slimApiResponsesForStorage(apiResponses) {
  if (!Array.isArray(apiResponses)) return [];
  return apiResponses.map((block) => ({
    operationName: block?.operationName,
    queryId: block?.queryId,
    ok: block?.ok,
    status: block?.status,
    intercepted: block?.intercepted,
    parseError: block?.parseError,
    variables: block?.variables,
    data: block?.data,
  }));
}

function stripTweetForStorage(tweet) {
  if (!tweet) return tweet;
  const { _rawTweet, _raw, ...rest } = tweet;
  const out = { ...rest };
  if (out.quoted_tweet) out.quoted_tweet = stripTweetForStorage(out.quoted_tweet);
  if (out.article?._raw) {
    const { _raw, _rawArticle, ...articleRest } = out.article;
    out.article = articleRest;
  }
  return out;
}

function apiResponsesSummaryOnly(apiResponses) {
  return (apiResponses || []).map((b) => ({
    operationName: b?.operationName,
    ok: b?.ok,
    status: b?.status,
    parseError: b?.parseError,
  }));
}

function stableStringify(value) {
  const seen = new WeakSet();
  return JSON.stringify(value, (key, val) => {
    if (key === "_rawTweet" || key === "_raw" || key === "_rawArticle") return undefined;
    if (val && typeof val === "object") {
      if (seen.has(val)) return undefined;
      seen.add(val);
    }
    return val;
  });
}

export function collectTweetIdsFromGraphQL(data) {
  const ids = harvestReplyIdsFromTweetDetail(data);
  walkForIds(data, ids);
  return ids;
}

function walkForIds(obj, ids, depth = 0) {
  if (!obj || depth > 20) return;
  if (typeof obj !== "object") return;
  if (obj.rest_id && (obj.legacy || obj.__typename === "Tweet")) {
    ids.add(String(obj.rest_id));
  }
  if (obj.legacy?.id_str) ids.add(String(obj.legacy.id_str));
  if (Array.isArray(obj)) {
    for (const item of obj) walkForIds(item, ids, depth + 1);
    return;
  }
  for (const v of Object.values(obj)) walkForIds(v, ids, depth + 1);
}

export async function buildForensicBundleRecord(payload) {
  return buildForensicBundleRecordInner(payload);
}

function focalArticleIdFromTweet(focal) {
  if (!focal) return null;
  return (
    focal.article?.article_id ||
    focal.article?.rest_id ||
    focal.article_results?.result?.rest_id ||
    focal.article_results?.result?.id ||
    null
  );
}

async function buildForensicBundleRecordInner({
  captureId,
  focalTweetId,
  capturedAt,
  pageUrl,
  apiResponses = [],
  domForensic,
  articleCapture,
  captureWarnings = [],
  bulkCapture = false,
}) {
  const tweets = mergeApiResponsesIntoTweets(apiResponses, focalTweetId, { bulkFeed: bulkCapture });

  if (domForensic?.repliesMedia) {
    for (const t of tweets) {
      const id = String(t.rest_id || t.id_str || "");
      if (domForensic.repliesMedia[id]) {
        t.dom_media = domForensic.repliesMedia[id];
      }
    }
  }

  const focal = tweets.find((t) => String(t.rest_id || t.id_str) === String(focalTweetId));
  if (focal && domForensic?.focalMedia?.length) {
    focal.dom_media = domForensic.focalMedia;
  }

  const conversationRootId = focal ? conversationRootIdForTweet(focal, tweets) : null;
  const materialRoot =
    conversationRootId
      ? tweets.find((t) => String(t.rest_id || t.id_str) === String(conversationRootId)) || focal
      : focal;

  const apiArticles = [];
  for (const block of apiResponses) {
    if (block?.data) apiArticles.push(...findArticleNodesInGraphQL(block.data));
  }
  const articleDomCapture = {
    ...(domForensic?.articleCapture || articleCapture || {}),
    focalMedia: domForensic?.focalMedia,
  };
  const fullArticle = mergeArticleRecords(apiArticles, articleDomCapture, {
    preferredArticleId:
      focalArticleIdFromTweet(materialRoot) || focalArticleIdFromTweet(focal),
  });

  if (fullArticle && materialRoot) {
    const rawInline = materialRoot.dom_media?.length
      ? materialRoot.dom_media
      : domForensic?.focalMedia;
    const inlineMedia = filterInlineArticleMedia(rawInline, fullArticle);
    if (inlineMedia?.length) {
      fullArticle.dom_inline_media = inlineMedia;
    }
    fullArticle.body_segments = parseArticleBodySegments(fullArticle);
    materialRoot.article = { ...materialRoot.article, ...fullArticle, full_capture: true };
    materialRoot.dom_media = [];
  }

  const storedApiResponses = slimApiResponsesForStorage(apiResponses);
  const rawForHash = stableStringify({
    tweets,
    fullArticle,
    domForensic: domForensic
      ? { tweetId: domForensic.tweetId, articleId: domForensic.articleId }
      : null,
  });

  return {
    schemaVersion: 1,
    captureId,
    capturedAt,
    pageUrl,
    manual: true,
    forensic: true,
    focalTweetId: String(focalTweetId),
    source: {
      operationName: "ForensicTweetCapture",
      method: "MULTI",
      description: "Per-tweet deep capture (TweetDetail + reply backfill + DOM)",
    },
    http: { ok: true },
    summary: {
      tweetCount: tweets.length,
      tweetIds: tweets.map((t) => t.rest_id || t.id_str).filter(Boolean),
      apiCallCount: apiResponses.length,
      hasFullArticle: !!fullArticle?.plain_text,
      domReplyIdsFound: domForensic?.visibleReplyIds?.length ?? 0,
      searchApiPages: apiResponses.filter((r) => r.operationName === "SearchTimeline").length,
      domMediaReplyCount: domForensic?.repliesMedia
        ? Object.keys(domForensic.repliesMedia).length
        : 0,
      warnings: captureWarnings,
      expectedReplyCount: materialRoot?.reply_count ?? focal?.reply_count ?? null,
      captureIncomplete:
        (materialRoot?.reply_count ?? focal?.reply_count) != null &&
        tweets.length < Number(materialRoot?.reply_count ?? focal?.reply_count) + 1,
      focalHasMedia: !!(
        focal &&
        (getTweetMediaList(focal)?.length || domForensic?.focalMedia?.length)
      ),
    },
    tweets: tweets.map(stripTweetForStorage),
    apiResponses: apiResponsesSummaryOnly(storedApiResponses),
    domForensic: domForensic || null,
    fullArticle: fullArticle || null,
    rawResponse: null,
    partial:
      (focal?.reply_count != null && tweets.length < Number(focal.reply_count) + 1) ||
      (captureWarnings || []).some(
        (w) =>
          !String(w).startsWith("article:") && !String(w).startsWith("SearchTimeline:")
      ),
    integrity: {
      sha256: await sha256Hex(rawForHash),
      algorithm: "SHA-256",
      computedAt: new Date().toISOString(),
    },
  };
}
