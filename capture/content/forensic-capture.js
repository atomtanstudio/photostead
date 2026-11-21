/**
 * Page-world: prepare tweet DOM, capture full articles & reply threads (stop at Discover more).
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";

  const FALLBACK_QUERY_IDS = {
    TweetDetail: "oCon7R-cgWRFy6EfZjaKfg",
    TweetResultByRestId: "tCVRZ3WCvoj0BVO7BKnL-Q",
    SearchTimeline: "rkp6b4vtR9u7v3naGoOzUQ",
  };

  const FEATURES = {
    articles_preview_enabled: true,
    longform_notetweets_consumption_enabled: true,
    responsive_web_edit_tweet_api_enabled: true,
    view_counts_everywhere_api_enabled: true,
    tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled: true,
    responsive_web_graphql_timeline_navigation_enabled: true,
    responsive_web_enhance_cards_enabled: false,
    graphql_is_translatable_rweb_tweet_is_translatable_enabled: true,
    creator_subscriptions_tweet_preview_api_enabled: true,
    rweb_tipjar_consumption_enabled: true,
    communities_web_enable_tweet_community_results_fetch: true,
    c9s_tweet_anatomy_moderator_badge_enabled: true,
    longform_notetweets_rich_text_read_enabled: true,
    longform_notetweets_inline_media_enabled: true,
    profile_label_improvements_pcf_label_in_post_enabled: true,
    freedom_of_speech_not_reach_fetch_enabled: true,
    standardized_nudges_misinfo: true,
    rweb_video_timestamps_enabled: true,
    responsive_web_twitter_article_tweet_consumption_enabled: true,
    responsive_web_media_download_video_enabled: true,
    premium_content_api_read_enabled: true,
  };

  const ARTICLE_FIELD_TOGGLES = {
    withArticleRichContentState: true,
    withArticlePlainText: true,
    withArticleSummaryText: true,
    withArticleVoiceOver: true,
    withGrokAnalyze: false,
    withDisallowedReplyControls: false,
  };

  function post(type, payload) {
    window.postMessage({ source: SOURCE, type, payload }, "*");
  }

  let activeCaptureTweetId = null;

  async function sleepForRateLimit(response, operationName, attempt) {
    if (response.status !== 429 || attempt >= 4) return false;
    const profile = pacing();
    const waitMs = Math.min(profile.rateLimitMaxMs, profile.rateLimitBaseMs * 2 ** attempt);
    post("forensic-capture-progress", {
      tweetId: activeCaptureTweetId,
      stage: "rate-limit",
      operationName,
      waitMs,
      attempt: attempt + 1,
    });
    await sleep(waitMs);
    return true;
  }


  async function parseResponseJson(response) {
    const text = await response.text();
    if (!text || !text.trim()) {
      return { _parseError: "empty response body", _httpStatus: response.status };
    }
    try {
      return JSON.parse(text);
    } catch (e) {
      return {
        _parseError: String(e.message || e),
        _rawSnippet: text.slice(0, 240),
        _httpStatus: response.status,
      };
    }
  }

  const CAPTURE_DEADLINE_MS = 105000;
  const GRAPHQL_FETCH_TIMEOUT_MS = 20000;

  function captureDeadlineMs() {
    return pacing().captureDeadlineMs || CAPTURE_DEADLINE_MS;
  }

  function captureDeadline() {
    return Date.now() + captureDeadlineMs();
  }

  function isTweetStatusId(id) {
    const s = String(id || "").trim();
    return /^\d{15,22}$/.test(s);
  }

  function addTweetStatusId(into, id) {
    const s = String(id || "");
    if (isTweetStatusId(s)) into.add(s);
  }
  const BACKFILL_MAX = 50;
  const CHUNK_SIZE = 4;

  const CAPTURE_PACING = {
    normal: {
      graphqlGapMs: 350,
      graphqlPreDelayMs: 0,
      backfillGapMs: 100,
      backfillMax: BACKFILL_MAX,
      tweetDetailMaxPages: 12,
      searchMaxPages: 15,
      skipSearch: false,
      skipRecencyPass: false,
      skipBackfill: false,
      rateLimitBaseMs: 15000,
      rateLimitMaxMs: 120000,
    },
    bulk: {
      graphqlGapMs: 1200,
      graphqlPreDelayMs: 800,
      backfillGapMs: 600,
      backfillMax: 4,
      tweetDetailMaxPages: 1,
      searchMaxPages: 0,
      skipSearch: true,
      skipRecencyPass: true,
      skipBackfill: true,
      skipTweetDetail: true,
      lightPrepare: true,
      skipArticleHtmlFetch: true,
      captureDeadlineMs: 90000,
      rateLimitBaseMs: 30000,
      rateLimitMaxMs: 180000,
    },
  };

  let activeCaptureOptions = { bulkCapture: false, feedArticle: false };
  let captureInFlight = false;

  function pacing() {
    if (!activeCaptureOptions.bulkCapture) return CAPTURE_PACING.normal;
    if (activeCaptureOptions.feedArticle) {
      return {
        ...CAPTURE_PACING.bulk,
        skipTweetDetail: false,
        tweetDetailMaxPages: 1,
        skipArticleHtmlFetch: false,
        captureDeadlineMs: 120000,
      };
    }
    return CAPTURE_PACING.bulk;
  }

  function sleep(ms) {
    return new Promise((r) => setTimeout(r, ms));
  }

  function pastDeadline(deadline) {
    return Date.now() > deadline;
  }

  async function yieldToBrowser() {
    await new Promise((r) => requestAnimationFrame(r));
    await new Promise((r) => setTimeout(r, 0));
  }

  async function fetchWithTimeout(url, options, timeoutMs = GRAPHQL_FETCH_TIMEOUT_MS) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      return await fetch(url, { ...options, signal: controller.signal });
    } catch (err) {
      if (err?.name === "AbortError") {
        throw new Error(`GraphQL request timed out after ${timeoutMs}ms`);
      }
      throw err;
    } finally {
      clearTimeout(timer);
    }
  }

  function harvestTweetsWithTextFromResponses(apiResponses) {
    const have = new Map();
    const walk = (obj, depth) => {
      if (!obj || depth > 22) return;
      if (obj.legacy?.id_str && typeof obj.legacy.full_text === "string") {
        const id = String(obj.rest_id || obj.legacy.id_str);
        const len = obj.legacy.full_text.length;
        if (!have.has(id) || have.get(id) < len) have.set(id, len);
      }
      if (typeof obj !== "object") return;
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    for (const block of apiResponses || []) {
      if (block?.data) walk(block.data, 0);
    }
    return have;
  }

  function harvestAllTweetIds(apiResponses, into) {
    const walk = (obj, depth) => {
      if (!obj || depth > 26) return;
      if (typeof obj !== "object") return;
      if (
        obj.__typename === "Tweet" ||
        (obj.legacy?.id_str && typeof obj.legacy.full_text === "string")
      ) {
        addTweetStatusId(into, obj.rest_id || obj.legacy.id_str);
        addTweetStatusId(into, obj.legacy?.in_reply_to_status_id_str);
      }
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    for (const block of apiResponses || []) {
      if (block?.data) walk(block.data, 0);
    }
  }

  function computeBackfillIds(allKnownIds, apiResponses, focalId, visibleReplyIds) {
    const have = harvestTweetsWithTextFromResponses(apiResponses);
    const missing = [];
    for (const id of allKnownIds) {
      if (id === String(focalId)) continue;
      const len = have.get(id) || 0;
      if (len < 8) missing.push(id);
    }
    const priority = new Set(visibleReplyIds || []);
    missing.sort((a, b) => {
      const pa = priority.has(a) ? 0 : 1;
      const pb = priority.has(b) ? 0 : 1;
      return pa - pb;
    });
    return missing;
  }
  function articleContentInApi(apiResponses, articleId) {
    if (!articleId) return false;
    let found = false;
    const want = String(articleId);
    const walk = (obj, depth) => {
      if (!obj || depth > 26 || found) return;
      if (typeof obj !== "object") return;
      const id = obj.rest_id || obj.id;
      if (id && String(id) === want) {
        if (obj.plain_text || obj.content_state?.blocks?.length || obj.title) {
          found = true;
          return;
        }
      }
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    for (const block of apiResponses || []) {
      if (block?.data) walk(block.data, 0);
      if (found) break;
    }
    return found;
  }

  function slimGraphQLPayload(data) {
    if (!data || typeof data !== "object") return data;
    const root = data.data || data;
    const slim = { data: {} };
    if (root.threaded_conversation_with_injections_v2) {
      slim.data.threaded_conversation_with_injections_v2 =
        root.threaded_conversation_with_injections_v2;
    }
    if (root.tweetResult) slim.data.tweetResult = root.tweetResult;
    if (root.search_by_raw_query) slim.data.search_by_raw_query = root.search_by_raw_query;
    if (!Object.keys(slim.data).length) return data;
    return slim;
  }

  function slimApiResponsesForTransfer(apiResponses) {
    return (apiResponses || []).map((block) => ({
      operationName: block?.operationName,
      ok: block?.ok,
      status: block?.status,
      parseError: block?.parseError,
      data: block?.data ? slimGraphQLPayload(block.data) : null,
    }));
  }

  function slimDomForensic(domForensic) {
    if (!domForensic) return domForensic;
    const { articleCapture, ...rest } = domForensic;
    const slim = { ...rest };
    if (articleCapture) {
      slim.articleCaptureMeta = {
        articleId: articleCapture.articleId,
        title: articleCapture.title,
        plainTextLen: (articleCapture.plainText || "").length,
        skippedHeavyParse: articleCapture.skippedHeavyParse,
        error: articleCapture.error,
      };
    }
    if (slim.repliesMedia && typeof slim.repliesMedia === "object") {
      const keys = Object.keys(slim.repliesMedia);
      if (keys.length > 40) {
        slim.repliesMedia = Object.fromEntries(
          keys.slice(0, 40).map((k) => [k, slim.repliesMedia[k]])
        );
        slim.repliesMediaTruncated = keys.length;
      }
    }
    return slim;
  }

  function slimArticleCapture(articleCapture) {
    if (!articleCapture) return null;
    return {
      articleId: articleCapture.articleId,
      url: articleCapture.url,
      title: articleCapture.title,
      plainText: articleCapture.plainText || null,
      blocks: articleCapture.blocks?.slice?.(0, 80) || articleCapture.blocks,
      mediaBlocks: articleCapture.mediaBlocks?.slice?.(0, 30) || articleCapture.mediaBlocks,
      embeddedContentState: articleCapture.embeddedContentState,
      skippedHeavyParse: articleCapture.skippedHeavyParse,
      error: articleCapture.error,
    };
  }



  async function postCaptureChunks(tweetId, pageUrl, apiResponses, domForensic, articleCapture, warnings, partial) {
    const sessionId = crypto.randomUUID();
    const slimResponses = slimApiResponsesForTransfer(apiResponses);
    const slimDom = slimDomForensic(domForensic);
    const slimArticle = slimArticleCapture(articleCapture);

    post("forensic-capture-active", { active: true, tweetId });
    post("forensic-capture-progress", { tweetId, stage: "saving" });
    post("forensic-capture-begin", { sessionId, tweetId, pageUrl, bulkCapture: activeCaptureOptions.bulkCapture });
    for (let i = 0; i < slimResponses.length; i += CHUNK_SIZE) {
      await yieldToBrowser();
      await sleep(15);
      post("forensic-capture-chunk", {
        sessionId,
        tweetId,
        chunk: slimResponses.slice(i, i + CHUNK_SIZE),
      });
    }
    await yieldToBrowser();
    post("forensic-capture-end", {
      sessionId,
      tweetId,
      pageUrl,
      domForensic: slimDom,
      articleCapture: slimArticle,
      warnings,
      partial,
      bulkCapture: activeCaptureOptions.bulkCapture,
    });
    post("forensic-capture-active", { active: false, tweetId });
  }

  function resolveQueryId(operationName) {
    try {
      for (const entry of performance.getEntriesByType("resource")) {
        const m = entry.name.match(
          new RegExp(`/graphql/([A-Za-z0-9_-]+)/${operationName}(?:\\?|$)`)
        );
        if (m) return m[1];
      }
    } catch (_) {}
    return FALLBACK_QUERY_IDS[operationName];
  }

  function getCsrfToken() {
    const match = document.cookie.match(/(?:^|;\s*)ct0=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : null;
  }

  function getBearerToken() {
    const scripts = document.querySelectorAll("script");
    for (const script of scripts) {
      const text = script.textContent || "";
      const m = text.match(/Bearer\s+(AAAAAA[a-zA-Z0-9%_-]+)/);
      if (m) return `Bearer ${m[1]}`;
    }
    return "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA";
  }

  async function graphqlGet(operationName, variables, fieldToggles, attempt = 0) {
    const profile = pacing();
    if (profile.graphqlPreDelayMs) await sleep(profile.graphqlPreDelayMs);
    const queryId = resolveQueryId(operationName);
    const params = new URLSearchParams();
    params.set("variables", JSON.stringify(variables));
    params.set("features", JSON.stringify(FEATURES));
    if (fieldToggles) params.set("fieldToggles", JSON.stringify(fieldToggles));

    const url = `https://x.com/i/api/graphql/${queryId}/${operationName}?${params.toString()}`;
    const csrf = getCsrfToken();
    const headers = {
      "Content-Type": "application/json",
      authorization: getBearerToken(),
      "x-twitter-active-user": "yes",
      "x-twitter-client-language": navigator.language || "en",
    };
    if (csrf) headers["x-csrf-token"] = csrf;

    const response = await fetchWithTimeout(url, {
      method: "GET",
      credentials: "include",
      headers,
    });
    if (await sleepForRateLimit(response, operationName, attempt)) {
      return graphqlGet(operationName, variables, fieldToggles, attempt + 1);
    }
    const data = await parseResponseJson(response);
    const parseError = data?._parseError || null;
    if (parseError) {
      await pauseAfterGraphqlCall();
      return {
        operationName,
        queryId,
        variables,
        url,
        data: null,
        status: response.status,
        ok: false,
        parseError,
      };
    }
    await pauseAfterGraphqlCall();
    return {
      operationName,
      queryId,
      variables,
      url,
      data,
      status: response.status,
      ok: response.ok,
    };
  }

  async function pauseAfterGraphqlCall() {
    const gap = pacing().graphqlGapMs;
    if (gap > 0) await sleep(gap);
  }

  async function graphqlPost(operationName, variables, fieldToggles, attempt = 0) {
    const profile = pacing();
    if (profile.graphqlPreDelayMs) await sleep(profile.graphqlPreDelayMs);
    const queryId = resolveQueryId(operationName);
    const url = `https://x.com/i/api/graphql/${queryId}/${operationName}`;
    const csrf = getCsrfToken();
    const headers = {
      "Content-Type": "application/json",
      authorization: getBearerToken(),
      "x-twitter-active-user": "yes",
      "x-twitter-client-language": navigator.language || "en",
    };
    if (csrf) headers["x-csrf-token"] = csrf;

    const payload = { variables, features: FEATURES, queryId };
    if (fieldToggles) payload.fieldToggles = fieldToggles;

    const response = await fetchWithTimeout(url, {
      method: "POST",
      credentials: "include",
      headers,
      body: JSON.stringify(payload),
    });
    if (await sleepForRateLimit(response, operationName, attempt)) {
      return graphqlPost(operationName, variables, fieldToggles, attempt + 1);
    }
    const data = await parseResponseJson(response);
    const parseError = data?._parseError || null;
    if (parseError) {
      await pauseAfterGraphqlCall();
      return {
        operationName,
        queryId,
        variables,
        url,
        data: null,
        status: response.status,
        ok: false,
        parseError,
        method: "POST",
      };
    }
    await pauseAfterGraphqlCall();
    return {
      operationName,
      queryId,
      variables,
      url,
      data,
      status: response.status,
      ok: response.ok,
      method: "POST",
    };
  }


  function findArticleEl(tweetId) {
    return (
      document.querySelector(`[data-imtc-root="${tweetId}"]`) ||
      document.querySelector(`article[data-testid="tweet"][data-imtc-root="${tweetId}"]`)
    );
  }

  function pageHasDiscoverMoreMarker() {
    const nodes = document.querySelectorAll("span, div, h2, button");
    for (const el of nodes) {
      const t = (el.textContent || "").trim();
      if (/^discover more$/i.test(t) && el.offsetParent !== null) return true;
    }
    return false;
  }

  function clickIfVisible(el) {
    if (!el || el.offsetParent === null) return false;
    el.click();
    return true;
  }

  async function prepareTweetArticle(article) {
    if (!article) return;

    article.scrollIntoView({ block: "center", behavior: "instant" });
    await sleep(pacing().lightPrepare ? 120 : 200);

    if (pacing().lightPrepare) {
      const showMore =
        article.querySelector('[data-testid="tweet-text-show-more-link"]') ||
        [...article.querySelectorAll("span, button, div[role='button']")].find((el) =>
          /^show more$/i.test((el.textContent || "").trim())
        );
      if (clickIfVisible(showMore)) await sleep(200);
      return;
    }

    for (let i = 0; i < 8; i++) {
      const showMore =
        article.querySelector('[data-testid="tweet-text-show-more-link"]') ||
        [...article.querySelectorAll("span, button, div[role='button']")].find((el) =>
          /^show more$/i.test((el.textContent || "").trim())
        );
      if (!clickIfVisible(showMore)) break;
      await sleep(350);
    }

    const articleLink = article.querySelector('a[href*="/i/article/"]');
    if (articleLink) {
      articleLink.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
      await sleep(200);
    }

    const clickMatchers = [
      (label) => /view\s*(this\s*)?(media|photo|image|content)/i.test(label),
      (label) => /show\s*(this\s*)?(media|photo|sensitive)/i.test(label),
      (label) => /display\s*(this\s*)?(media|photo)/i.test(label),
    ];

    for (let round = 0; round < 5; round++) {
      let clicked = false;
      const clickables = article.querySelectorAll("button, a, div[role='button'], span[role='button']");
      for (const el of clickables) {
        const label = (
          el.getAttribute("aria-label") ||
          el.getAttribute("title") ||
          el.textContent ||
          ""
        )
          .trim()
          .toLowerCase();
        if (!label) continue;
        if (clickMatchers.some((fn) => fn(label))) {
          if (clickIfVisible(el)) {
            clicked = true;
            await sleep(400);
          }
        }
      }
      const masks = article.querySelectorAll('[data-testid="mask"], [data-testid="interstitialGraphic"]');
      for (const mask of masks) {
        const btn = mask.closest("div")?.querySelector("button, [role='button']");
        if (clickIfVisible(btn)) {
          clicked = true;
          await sleep(400);
        }
      }
      if (!clicked) break;
    }

    for (const video of article.querySelectorAll("video")) {
      try {
        video.muted = true;
        await video.play();
        await sleep(500);
        video.pause();
      } catch (_) {}
    }
    await sleep(200);
  }

  function isOnFocalStatusPage(tweetId) {
    return new RegExp(`/status/${tweetId}(?:[^0-9]|$)`).test(location.pathname);
  }

  function collectAdjacentReplyIds(focalTweetId) {
    const ids = [];
    const seen = new Set();
    const articles = [...document.querySelectorAll('article[data-testid="tweet"]')];
    const focalIdx = articles.findIndex((a) => getTweetIdFromArticleEl(a) === String(focalTweetId));
    if (focalIdx < 0) return ids;
    for (let i = focalIdx + 1; i < articles.length; i++) {
      if (domHasDiscoverMoreAfter(articles[i - 1])) break;
      const id = getTweetIdFromArticleEl(articles[i]);
      if (id && id !== String(focalTweetId) && !seen.has(id)) {
        seen.add(id);
        ids.push(id);
      }
    }
    return ids;
  }

  async function expandThreadRepliesBeforeDiscoverMore(focalEl, tweetId) {
    if (!isOnFocalStatusPage(tweetId || getTweetIdFromArticleEl(focalEl))) return;
    const col =
      document.querySelector('[data-testid="primaryColumn"]') ||
      focalEl?.closest("main") ||
      document.body;

    for (let round = 0; round < 12; round++) {
      if (pageHasDiscoverMoreMarker()) break;

      const candidates = [...col.querySelectorAll("span, button, div[role='button'], a")];
      const showMore = candidates.find((el) => {
        const t = (el.textContent || "").trim();
        return /^(show more replies|more replies|show replies)$/i.test(t) && el.offsetParent !== null;
      });
      if (showMore) clickIfVisible(showMore);

      col.scrollBy({ top: 700, behavior: "instant" });
      window.scrollBy({ top: 400, behavior: "instant" });
      await sleep(700);
    }
  }

  function extractArticleIdFromNode(data) {
    let found = null;
    const walk = (obj, depth) => {
      if (!obj || depth > 28 || found) return;
      if (typeof obj !== "object") return;
      if (obj.rest_id && (obj.plain_text != null || obj.content_state || obj.title)) {
        found = String(obj.rest_id);
        return;
      }
      if (obj.article_results?.result?.rest_id) {
        found = String(obj.article_results.result.rest_id);
        return;
      }
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    walk(data, 0);
    return found;
  }


  function walkArticleDomInOrder(root, blocks, mediaBlocks) {
    if (!root) return;
    const seenText = new Set();
    const seenMedia = new Set();

    const pushText = (el) => {
      const t = (el.textContent || "").trim();
      if (!t || t.length < 2) return;
      const key = t.slice(0, 120);
      if (seenText.has(key)) return;
      seenText.add(key);
      blocks.push({ type: (el.tagName || "p").toLowerCase(), text: t });
    };

    const pushMedia = (item) => {
      const key = item.src || item.poster || "";
      if (!key || seenMedia.has(key)) return;
      seenMedia.add(key);
      mediaBlocks.push(item);
    };

    const walk = (el) => {
      if (!el || el.nodeType !== 1) return;
      const tag = el.tagName.toLowerCase();
      if (["p", "h2", "h3", "h4", "li", "blockquote"].includes(tag) || el.getAttribute("data-testid") === "tweetText") {
        if (!["p", "h2", "h3", "h4", "li", "blockquote"].includes(tag) || el.closest("article") === root || root.contains(el)) {
          pushText(el);
        }
        return;
      }
      if (tag === "video") {
        const src = el.src || el.currentSrc || "";
        const poster = el.poster || "";
        if (src) pushMedia({ type: "media", mediaType: "video", src, poster });
        return;
      }
      if (tag === "img") {
        const src = el.getAttribute("src") || "";
        if (src && src.includes("twimg.com") && !src.includes("profile_images") && !src.includes("emoji") && !src.includes("card_img")) {
          const large = src.replace(/&name=\w+/, "&name=large");
          pushMedia({ type: "media", mediaType: "photo", src: large, alt: el.getAttribute("alt") || "" });
        }
        return;
      }
      for (const child of el.children) walk(child);
    };

    walk(root);
  }


  function extractArticleJsonFromHtml(html) {
    const out = { blocks: [], mediaEntities: {}, plainText: "" };
    const patterns = [
      /<script[^>]*type="application\/json"[^>]*>([\s\S]*?)<\/script>/gi,
      /window\.__INITIAL_STATE__\s*=\s*({[\s\S]*?});\s*<\/script>/i,
    ];
    for (const re of patterns) {
      let m;
      const r = new RegExp(re.source, re.flags);
      while ((m = r.exec(html))) {
        try {
          const json = JSON.parse(m[1]);
          const walk = (obj, depth) => {
            if (!obj || depth > 24) return;
            if (typeof obj !== "object") return;
            if (obj.content_state?.blocks) {
              out.blocks.push(...obj.content_state.blocks);
              if (obj.content_state.media_entities) {
                Object.assign(out.mediaEntities, obj.content_state.media_entities);
              }
              if (obj.plain_text) out.plainText = obj.plain_text;
            }
            if (Array.isArray(obj)) {
              for (const x of obj) walk(x, depth + 1);
              return;
            }
            for (const v of Object.values(obj)) walk(v, depth + 1);
          };
          walk(json, 0);
        } catch (_) {}
      }
    }
    return out;
  }

  async function fetchArticlePageCapture(articleId) {
    const url = `https://x.com/i/article/${articleId}`;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 6000);
    try {
      const res = await fetch(url, {
        credentials: "include",
        signal: controller.signal,
        headers: { accept: "text/html" },
      });
      const html = await res.text();
      clearTimeout(timer);
      const capped = html.length > 250000 ? html.slice(0, 250000) : html;
      const title =
        capped.match(/<meta[^>]+property="og:title"[^>]+content="([^"]*)"/i)?.[1] ||
        capped.match(/<meta[^>]+content="([^"]*)"[^>]+property="og:title"/i)?.[1] ||
        "";
      const desc =
        capped.match(/<meta[^>]+property="og:description"[^>]+content="([^"]*)"/i)?.[1] ||
        "";
      return {
        articleId,
        url,
        title,
        plainText: desc || null,
        blocks: desc ? [{ type: "p", text: desc }] : [],
        mediaBlocks: [],
        images: [],
        skippedHeavyParse: true,
        htmlLength: html.length,
        fetchedAt: new Date().toISOString(),
      };
    } catch (e) {
      clearTimeout(timer);
      return { articleId, url, error: String(e), skippedHeavyParse: true };
    }
  }

  function scrapeDomForensic(article, tweetId) {
    if (!article) return { tweetId, scrapedAt: new Date().toISOString() };

    const quoteRoot =
      article.querySelector('[data-testid="card.wrapper"]') ||
      article.querySelector('[role="link"][href*="/status/"]');
    let textEl = null;
    for (const el of article.querySelectorAll('[data-testid="tweetText"]')) {
      if (quoteRoot && quoteRoot !== article && quoteRoot.contains(el)) continue;
      textEl = el;
      break;
    }
    if (!textEl) textEl = article.querySelector('[data-testid="tweetText"]');
    const articleLink = article.querySelector('a[href*="/i/article/"]');
    let articleId = null;
    if (articleLink) {
      const m = articleLink.href.match(/\/i\/article\/(\d+)/);
      if (m) articleId = m[1];
    }

    const images = [];
    article.querySelectorAll('img[src*="twimg.com"]').forEach((img) => {
      const src = img.src || img.getAttribute("src");
      if (src && !src.includes("profile_images") && !src.includes("emoji")) {
        images.push({ src, alt: img.alt || "" });
      }
    });

    const focalMedia = scrapeMediaFromTweetElement(article);

    return {
      tweetId,
      articleId,
      text: textEl?.innerText || "",
      images,
      focalMedia,
      scrapedAt: new Date().toISOString(),
    };
  }

  function responseHasDiscoverMore(data) {
    const blob = JSON.stringify(data || {}).toLowerCase();
    if (!blob.includes("discover more")) return false;
    const instructions = data?.data?.threaded_conversation_with_injections_v2?.instructions;
    if (!instructions) return false;
    for (const inst of instructions) {
      if (/discover\s*more/i.test(JSON.stringify(inst))) return true;
    }
    return false;
  }

  function extractReplyCursor(data) {
    const instructions = data?.data?.threaded_conversation_with_injections_v2?.instructions;
    if (!instructions) return null;
    let bottom = null;
    let showMore = null;
    let any = null;
    for (const instruction of instructions) {
      if (/discover\s*more/i.test(JSON.stringify(instruction)) && !instruction.entries?.length) continue;
      for (const entry of instruction.entries || []) {
        if (/discover\s*more/i.test(JSON.stringify(entry))) continue;
        const cursor = entry?.content?.timelineCursor || entry?.content?.cursor;
        const value = cursor?.value || entry?.content?.value;
        if (!value) continue;
        const label = (cursor?.displayText || cursor?.label || cursor?.text || "").toString();
        if (/discover\s*more/i.test(label)) continue;
        const id = String(entry.entryId || "").toLowerCase();
        any = value;
        if (id.includes("bottom") || cursor?.cursorType === "Bottom") bottom = value;
        if (/show\s*more|repl/i.test(label) || id.includes("showmore")) showMore = value;
      }
    }
    return bottom || showMore || any;
  }



  function extractExpectedReplyCountFromDetail(data) {
    let count = null;
    const walk = (obj, depth) => {
      if (!obj || depth > 24 || count != null) return;
      if (typeof obj !== "object") return;
      if (obj.legacy?.reply_count != null && (obj.rest_id || obj.legacy?.id_str)) {
        count = Number(obj.legacy.reply_count);
        return;
      }
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    walk(data, 0);
    return count;
  }

  function countDetailIdsInResponses(responses) {
    const ids = new Set();
    for (const block of responses || []) {
      if (block?.data) harvestIdsFromDetailData(block.data, ids);
    }
    return ids.size;
  }

  function extractConversationIdFromResponses(apiResponses, focalTweetId) {
    const focal = String(focalTweetId);
    let found = null;
    const walk = (obj, depth) => {
      if (!obj || depth > 28 || found) return;
      if (typeof obj !== "object") return;
      const id = obj.rest_id || obj.legacy?.id_str;
      if (id && String(id) === focal) {
        found = obj.legacy?.conversation_id_str || focal;
        return;
      }
      if (Array.isArray(obj)) {
        for (const x of obj) walk(x, depth + 1);
        return;
      }
      for (const v of Object.values(obj)) walk(v, depth + 1);
    };
    for (const block of apiResponses || []) {
      if (block?.data) walk(block.data, 0);
      if (found) break;
    }
    return found || focal;
  }

  function harvestIdsFromSearchData(data, into) {
    const instructions = data?.data?.search_by_raw_query?.search_timeline?.timeline?.instructions;
    if (!instructions) return;
    for (const instruction of instructions) {
      for (const entry of instruction.entries || []) {
        if (/discover\s*more/i.test(JSON.stringify(entry))) continue;
        const walk = (obj, depth) => {
          if (!obj || depth > 20) return;
          if (
            obj.__typename === "Tweet" ||
            (obj.legacy?.id_str && typeof obj.legacy.full_text === "string")
          ) {
            addTweetStatusId(into, obj.rest_id || obj.legacy.id_str);
            addTweetStatusId(into, obj.legacy?.in_reply_to_status_id_str);
          }
          if (typeof obj !== "object") return;
          for (const v of Object.values(obj)) {
            if (Array.isArray(v)) v.forEach((x) => walk(x, depth + 1));
            else walk(v, depth + 1);
          }
        };
        walk(entry, 0);
      }
    }
  }

  function harvestIdsFromDetailData(data, into) {
    const instructions =
      data?.data?.threaded_conversation_with_injections_v2?.instructions || [];
    for (const instruction of instructions) {
      for (const entry of instruction.entries || []) {
        if (/discover\s*more/i.test(JSON.stringify(entry))) continue;
        const walk = (obj, depth) => {
          if (!obj || depth > 20) return;
          if (
            obj.__typename === "Tweet" ||
            (obj.legacy?.id_str && typeof obj.legacy.full_text === "string")
          ) {
            addTweetStatusId(into, obj.rest_id || obj.legacy.id_str);
            addTweetStatusId(into, obj.legacy?.in_reply_to_status_id_str);
          }
          if (typeof obj !== "object") return;
          for (const v of Object.values(obj)) {
            if (Array.isArray(v)) v.forEach((x) => walk(x, depth + 1));
            else walk(v, depth + 1);
          }
        };
        walk(entry, 0);
      }
    }
  }

  async function fetchTweetDetailThread(tweetId, deadline, options = {}) {
    const profile = pacing();
    const responses = [];
    let cursor = null;
    let pages = 0;
    let staleCursorHits = 0;
    const seenCursors = new Set();
    let expectedReplies = null;
    let noGrowthPasses = 0;

    while (pages < profile.tweetDetailMaxPages) {
      if (deadline && pastDeadline(deadline)) break;
      const variables = {
        focalTweetId: tweetId,
        referrer: "tweet",
        with_rux_injections: false,
        rankingMode: options.rankingMode || "Relevance",
        includePromotedContent: false,
        withCommunity: true,
        withQuickPromoteEligibilityTweetFields: true,
        withBirdwatchNotes: true,
        withVoice: true,
        withV2Timeline: true,
      };
      if (cursor) variables.cursor = cursor;

      const idsBefore = countDetailIdsInResponses(responses);
      const detail = await graphqlGet("TweetDetail", variables, ARTICLE_FIELD_TOGGLES);
      responses.push(detail);

      if (expectedReplies == null && detail?.data) {
        expectedReplies = extractExpectedReplyCountFromDetail(detail.data);
      }
      const haveIds = countDetailIdsInResponses(responses);
      if (haveIds <= idsBefore) {
        noGrowthPasses++;
        if (noGrowthPasses >= 2) break;
      } else {
        noGrowthPasses = 0;
      }
      if (expectedReplies != null && haveIds >= expectedReplies + 3) break;

      const next = extractReplyCursor(detail.data);
      if (!next) break;
      if (seenCursors.has(next)) {
        staleCursorHits++;
        if (staleCursorHits >= 2) break;
      } else {
        staleCursorHits = 0;
        seenCursors.add(next);
      }
      if (next === cursor) {
        staleCursorHits++;
        if (staleCursorHits >= 2) break;
      }
      cursor = next;
      pages++;
      if (pages % 2 === 0) await yieldToBrowser();
    }

    return responses;
  }


  function getTweetIdFromArticleEl(article) {
    const timeLink = article.querySelector("time")?.closest('a[href*="/status/"]');
    if (timeLink) {
      const m = timeLink.href.match(/\/status\/(\d+)/);
      if (m) return m[1];
    }
    return null;
  }

  function domHasDiscoverMoreAfter(startEl) {
    const col = document.querySelector('[data-testid="primaryColumn"]');
    if (!col || !startEl) return false;
    const all = [...col.querySelectorAll("*")];
    const startIdx = all.indexOf(startEl);
    if (startIdx < 0) return false;
    for (let i = startIdx + 1; i < Math.min(startIdx + 120, all.length); i++) {
      const el = all[i];
      const t = (el.textContent || "").trim();
      if (el.childElementCount < 3 && /^discover more$/i.test(t)) return true;
    }
    return false;
  }


  async function lightCollectReplyIds(focalTweetId) {
    const onStatusPage = isOnFocalStatusPage(focalTweetId);
    const focalArticle = findArticleEl(focalTweetId);
    if (onStatusPage && focalArticle) {
      try {
        await expandThreadRepliesBeforeDiscoverMore(focalArticle, focalTweetId);
      } catch (_) {}
    }

    const ids = collectAdjacentReplyIds(focalTweetId);
    if (!onStatusPage) return ids;

    const seen = new Set(ids);
    const col = document.querySelector('[data-testid="primaryColumn"]') || document.body;
    for (let s = 0; s < 6; s++) {
      col.scrollBy({ top: 600, behavior: "instant" });
      await sleep(250);
      await yieldToBrowser();
      for (const id of collectAdjacentReplyIds(focalTweetId)) {
        if (!seen.has(id)) {
          seen.add(id);
          ids.push(id);
        }
      }
      if (pageHasDiscoverMoreMarker()) break;
    }
    return ids;
  }

  async function scrapeVisibleReplyIdsFromDom(focalTweetId) {
    const ids = [];
    const col = document.querySelector('[data-testid="primaryColumn"]') || document.body;
    const seen = new Set();

    const collect = () => {
      const articles = [...document.querySelectorAll('article[data-testid="tweet"]')];
      const focalIdx = articles.findIndex(
        (a) => getTweetIdFromArticleEl(a) === String(focalTweetId)
      );
      if (focalIdx < 0) return;
      for (let i = focalIdx + 1; i < articles.length; i++) {
        if (domHasDiscoverMoreAfter(articles[i - 1])) return;
        const id = getTweetIdFromArticleEl(articles[i]);
        if (id && id !== String(focalTweetId) && !seen.has(id)) {
          seen.add(id);
          ids.push(id);
        }
      }
    };

    collect();
    return ids;
  }

  function startInterceptCollector() {
    const captured = [];
    const handler = (event) => {
      if (event.source !== window || !event.data || event.data.source !== SOURCE) return;
      if (event.data.type !== "graphql-capture") return;
      const p = event.data.payload;
      const op = p?.request?.operationName;
      if ((op === "TweetDetail" || op === "SearchTimeline") && p?.response) {
        captured.push({
          operationName: op,
          data: p.response,
          status: p.httpStatus,
          ok: p.ok,
          intercepted: true,
        });
      }
    };
    window.addEventListener("message", handler);
    return () => {
      window.removeEventListener("message", handler);
      return captured;
    };
  }


  function scrapeMediaFromTweetElement(tweetEl) {
    const media = [];
    if (!tweetEl) return media;
    const seen = new Set();
    const add = (item) => {
      const key = item.media_url_https || item.mp4_url || "";
      if (!key || seen.has(key)) return;
      seen.add(key);
      media.push(item);
    };

    tweetEl.querySelectorAll('[data-testid="tweetPhoto"] img, img[src*="twimg.com"]').forEach((img) => {
      const src = img.src || img.getAttribute("src") || "";
      if (!src || src.includes("profile_images") || src.includes("emoji") || src.includes("_normal")) return;
      if (src.includes("card_img")) return;
      add({
        type: "photo",
        media_url_https: src.replace(/&name=\w+/, "&name=large"),
        ext_alt_text: img.alt || "",
      });
    });

    tweetEl.querySelectorAll("video, [data-testid='videoPlayer'] video").forEach((v) => {
      const source =
        v.querySelector("source[src*='video.twimg.com'], source[src*='.mp4']") ||
        null;
      const src =
        source?.getAttribute("src") ||
        (v.src && !/^blob:/i.test(v.src) ? v.src : "") ||
        v.currentSrc ||
        "";
      const poster = v.poster || "";
      if (!src && !poster) return;
      const isGif = v.getAttribute("loop") != null || (v.duration && v.duration < 30 && !v.controls);
      add({
        type: isGif ? "animated_gif" : "video",
        media_url_https: poster || src,
        mp4_url: src || undefined,
        video_info: src ? { variants: [{ url: src, content_type: "video/mp4" }] } : undefined,
      });
    });

    tweetEl.querySelectorAll("source[src*='video.twimg.com'], source[src*='.mp4']").forEach((s) => {
      const src = s.getAttribute("src") || "";
      if (src) {
        add({
          type: "video",
          mp4_url: src,
          media_url_https: src,
          video_info: { variants: [{ url: src, content_type: "video/mp4" }] },
        });
      }
    });

    return media;
  }

  function extractSearchCursor(data) {
    const instructions = data?.data?.search_by_raw_query?.search_timeline?.timeline?.instructions;
    if (!instructions) return null;
    let bottom = null;
    let any = null;
    for (const inst of instructions) {
      for (const entry of inst.entries || []) {
        if (/discover\s*more/i.test(JSON.stringify(entry))) return null;
        const eid = String(entry.entryId || "").toLowerCase();
        const c =
          entry?.content?.value ||
          entry?.content?.timelineCursor?.value ||
          entry?.content?.cursor?.value;
        if (!c || !eid.includes("cursor")) continue;
        any = c;
        if (eid.includes("bottom") || eid.includes("showmore")) bottom = c;
      }
    }
    return bottom || any;
  }

  function extractTweetsFromSearchData(data) {
    const tweets = [];
    const instructions = data?.data?.search_by_raw_query?.search_timeline?.timeline?.instructions;
    if (!instructions) return tweets;
    for (const inst of instructions) {
      for (const entry of inst.entries || []) {
        if (/discover\s*more/i.test(JSON.stringify(entry))) return tweets;
        const node = entry?.content?.itemContent?.tweet_results?.result;
        if (!node) continue;
        const walk = (obj, depth) => {
          if (!obj || depth > 12) return;
          if (obj.rest_id && (obj.legacy || obj.__typename === "Tweet")) {
            tweets.push({ __raw: obj, rest_id: obj.rest_id });
            return;
          }
          if (typeof obj !== "object") return;
          for (const v of Object.values(obj)) {
            if (Array.isArray(v)) v.forEach((x) => walk(x, depth + 1));
            else walk(v, depth + 1);
          }
        };
        walk(node, 0);
      }
    }
    return tweets;
  }

  function searchResponseHasTweets(block) {
    const root = block?.data;
    if (!root) return false;
  const instructions =
      root?.data?.search_by_raw_query?.search_timeline?.timeline?.instructions ||
      root?.search_by_raw_query?.search_timeline?.timeline?.instructions;
    return Array.isArray(instructions) && instructions.length > 0;
  }

  async function fetchConversationViaSearch(conversationId, deadline) {
    if (pacing().skipSearch) return [];
    if (!conversationId) return [];
    const profile = pacing();
    const responses = [];
    let cursor = null;
    let pages = 0;

    while (pages < profile.searchMaxPages) {
      if (deadline && pastDeadline(deadline)) break;
      const variables = {
        rawQuery: "conversation_id:" + conversationId,
        count: 40,
        querySource: "typed_query",
        product: pages < 8 ? "Latest" : "Top",
      };
      if (cursor) variables.cursor = cursor;

      let block = null;
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          block = await graphqlPost("SearchTimeline", variables, null);
          if (!block?.data?.data?.search_by_raw_query && !block?.parseError) {
            block = await graphqlGet("SearchTimeline", variables, null);
          }
          if (block?.data && block.ok !== false && !block.parseError) break;
          await sleep(350 * (attempt + 1));
        } catch (e) {
          if (attempt === 2 && pages === 0) {
            responses.push({
              operationName: "SearchTimeline",
              ok: false,
              parseError: String(e),
              data: null,
            });
          }
          await sleep(350 * (attempt + 1));
        }
      }
      if (!block?.data) break;

      responses.push(block);
      const next = extractSearchCursor(block.data);
      if (!next || next === cursor) break;
      cursor = next;
      pages++;
      await yieldToBrowser();
    }
    return responses;
  }

  async function scrapeAllReplyMedia(focalTweetId) {
    const repliesMedia = {};
    const articles = [...document.querySelectorAll('article[data-testid="tweet"]')];
    for (const art of articles) {
      const id = getTweetIdFromArticleEl(art);
      if (!id || id === String(focalTweetId)) continue;
      repliesMedia[id] = scrapeMediaFromTweetElement(art);
    }
    return repliesMedia;
  }

  async function fetchFocalTweetById(tweetId) {
    return graphqlGet(
      "TweetResultByRestId",
      {
        tweetId,
        withCommunity: true,
        includePromotedContent: false,
        withBirdwatchNotes: true,
        withVoice: true,
      },
      ARTICLE_FIELD_TOGGLES
    );
  }

  async function runForensicCapture(tweetId) {
    activeCaptureOptions = { bulkCapture: true, feedArticle: false };
    activeCaptureTweetId = tweetId;
    post("forensic-capture-active", { active: true, tweetId });
    try {
      const article = findArticleEl(tweetId);
      post("forensic-capture-progress", { tweetId, stage: "preparing" });
      await prepareTweetArticle(article);
      post("forensic-capture-progress", { tweetId, stage: "fetching" });
      const apiResponses = [];
      try { apiResponses.push(await fetchFocalTweetById(tweetId)); } catch (_) {}
      if (!harvestTweetsWithTextFromResponses(apiResponses).has(String(tweetId))) {
        apiResponses.push(await graphqlGet("TweetDetail", {
          focalTweetId: tweetId, referrer: "tweet", with_rux_injections: false,
          rankingMode: "Relevance", includePromotedContent: false,
          withCommunity: true, withBirdwatchNotes: true, withVoice: true,
          withV2Timeline: true,
        }, ARTICLE_FIELD_TOGGLES));
      }
      post("forensic-capture-end", { tweetId, selectedOnly: true });
      post("photostead-selected-bundle", { tweetId, pageUrl: location.href,
        apiResponses: slimApiResponsesForTransfer(apiResponses) });
    } finally {
      post("forensic-capture-active", { active: false, tweetId });
      activeCaptureTweetId = null;
      activeCaptureOptions = { bulkCapture: false, feedArticle: false };
    }
  }


  async function fetchMissingReplyPayloads({
    tweetId,
    missingIds = [],
    extraPagination = false,
    expectedReplyCount = null,
    deadline,
  }) {
    const apiResponses = [];
    const known = new Set([String(tweetId)]);

    if (extraPagination) {
      try {
        const moreDetail = await fetchTweetDetailThread(tweetId, deadline);
        apiResponses.push(...moreDetail);
        for (const block of moreDetail) {
          if (block?.data) harvestIdsFromDetailData(block.data, known);
        }
      } catch (e) {
        apiResponses.push({
          operationName: "TweetDetail",
          ok: false,
          parseError: "gap-fill detail: " + String(e),
          data: null,
        });
      }

      const conversationId = extractConversationIdFromResponses(apiResponses, tweetId);
      try {
        const searchPages = await fetchConversationViaSearch(conversationId, deadline);
        apiResponses.push(...searchPages);
        for (const block of searchPages) {
          if (block?.data) harvestIdsFromSearchData(block.data, known);
        }
      } catch (e) {
        apiResponses.push({
          operationName: "SearchTimeline",
          ok: false,
          parseError: "gap-fill search: " + String(e),
          data: null,
        });
      }
    }

    const domIds = isOnFocalStatusPage(tweetId) ? await lightCollectReplyIds(tweetId) : [];
    for (const id of domIds) known.add(String(id));

    const targets = new Set();
    for (const id of [...(missingIds || []), ...known]) {
      if (id !== String(tweetId) && isTweetStatusId(id)) targets.add(String(id));
    }

    if (extraPagination && !pastDeadline(deadline)) {
      try {
        const recency = await fetchTweetDetailThread(tweetId, deadline, { rankingMode: "Recency" });
        apiResponses.push(...recency);
      } catch (_) {}
    }

    let fetched = 0;
    for (const replyId of targets) {
      if (!isTweetStatusId(replyId)) continue;
      if (fetched >= 20) break;
      if (pastDeadline(deadline)) break;
      if (replyId === String(tweetId)) continue;
      try {
        const res = await graphqlGet(
          "TweetResultByRestId",
          {
            tweetId: replyId,
            withCommunity: true,
            includePromotedContent: false,
            withBirdwatchNotes: true,
            withVoice: true,
          },
          ARTICLE_FIELD_TOGGLES
        );
        apiResponses.push(res);
        fetched++;
        if (fetched % 2 === 0) await yieldToBrowser();
        await sleep(90);
      } catch (e) {
        apiResponses.push({
          operationName: "TweetResultByRestId",
          ok: false,
          parseError: String(e),
          data: null,
        });
      }
    }

    if (
      expectedReplyCount != null &&
      countDetailIdsInResponses(apiResponses) + 1 < Number(expectedReplyCount) + 1 &&
      !pastDeadline(deadline)
    ) {
      // one more detail pass if still short
      try {
        const extra = await fetchTweetDetailThread(tweetId, deadline);
        apiResponses.push(...extra);
      } catch (_) {}
    }

    return slimApiResponsesForTransfer(apiResponses);
  }


  if (!window.__imtcForensicCaptureInstalled) {
    window.__imtcForensicCaptureInstalled = true;
    window.addEventListener("message", (event) => {
    if (event.source !== window || !event.data || event.data.source !== SOURCE) return;

    if (event.data.type === "forensic-fetch-missing") {
      const payload = event.data.payload || {};
      const { tweetId, sessionId, missingIds, extraPagination, expectedReplyCount } = payload;
      if (!tweetId) return;
      const deadline = captureDeadline();
      fetchMissingReplyPayloads({
        tweetId,
        missingIds,
        extraPagination,
        expectedReplyCount,
        deadline,
      })
        .then((apiResponses) => {
          post("forensic-missing-complete", { tweetId, sessionId, apiResponses });
        })
        .catch((err) => {
          post("forensic-missing-complete", {
            tweetId,
            sessionId,
            apiResponses: [],
            error: String(err),
          });
        });
      return;
    }

    if (event.data.type === "forensic-capture-reset") {
      captureInFlight = false;
      activeCaptureTweetId = null;
      activeCaptureOptions = { bulkCapture: false, feedArticle: false };
      return;
    }

    if (event.data.type !== "forensic-capture-tweet") return;

    const { tweetId, bulkCapture = false, feedArticle = false } = event.data.payload || {};
    if (!tweetId) {
      post("forensic-capture-complete", { ok: false, error: "Missing tweet ID" });
      return;
    }

    if (captureInFlight) {
      post("forensic-ingest-error", { tweetId, error: "Capture already in progress" });
      return;
    }

    captureInFlight = true;
    runForensicCapture(tweetId, { bulkCapture: !!bulkCapture, feedArticle: !!feedArticle })
      .catch((err) => {
      post("forensic-capture-active", { active: false, tweetId });
      post("forensic-ingest-error", { tweetId, error: String(err) });
    })
      .finally(() => {
        captureInFlight = false;
      });
    });
  }
})();
