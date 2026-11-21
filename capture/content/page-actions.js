/**
 * Page-world helpers: deep capture via TweetDetail / TweetResultByRestId when user requests it.
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";

  function post(type, payload) {
    window.postMessage({ source: SOURCE, type, payload }, "*");
  }

  function getTweetIdFromUrl() {
    const m = location.pathname.match(/\/status\/(\d+)/);
    return m ? m[1] : null;
  }

  function readMetaTag(name) {
    const el = document.querySelector(`meta[name="${name}"]`);
    return el?.getAttribute("content") || null;
  }

  function getCsrfToken() {
    const match = document.cookie.match(/(?:^|;\s*)ct0=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : readMetaTag("csrf-token");
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

  async function graphqlGet(operationName, queryId, variables, fieldToggles) {
    const params = new URLSearchParams();
    params.set("variables", JSON.stringify(variables));
    params.set(
      "features",
      JSON.stringify({
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
        tweet_awards_web_tipping_enabled: false,
        longform_notetweets_rich_text_read_enabled: true,
        longform_notetweets_inline_media_enabled: true,
        profile_label_improvements_pcf_label_in_post_enabled: true,
        freedom_of_speech_not_reach_fetch_enabled: true,
        standardized_nudges_misinfo: true,
        rweb_video_timestamps_enabled: true,
        responsive_web_twitter_article_tweet_consumption_enabled: true,
      })
    );
    if (fieldToggles) {
      params.set("fieldToggles", JSON.stringify(fieldToggles));
    }

    const url = `https://x.com/i/api/graphql/${queryId}/${operationName}?${params.toString()}`;
    const csrf = getCsrfToken();
    const headers = {
      "Content-Type": "application/json",
      authorization: getBearerToken(),
    };
    if (csrf) headers["x-csrf-token"] = csrf;
    headers["x-twitter-active-user"] = "yes";
    headers["x-twitter-client-language"] = navigator.language || "en";

    const response = await fetch(url, {
      method: "GET",
      credentials: "include",
      headers,
    });
    const data = await response.json();
    return { url, data, status: response.status, ok: response.ok };
  }

  async function deepCaptureTweet(tweetId) {
    const id = tweetId || getTweetIdFromUrl();
    if (!id) {
      post("deep-capture-result", { ok: false, error: "No tweet ID in URL" });
      return;
    }

    const queryIds = {
      TweetDetail: "xIYgDwjboktoFeXe_fgacw",
      TweetResultByRestId: "zy39CwTyYhU-_0LP7dljjg",
    };

    const results = [];

    try {
      const detail = await graphqlGet("TweetDetail", queryIds.TweetDetail, {
        focalTweetId: id,
        referrer: "tweet",
        with_rux_injections: false,
        rankingMode: "Relevance",
        includePromotedContent: true,
        withCommunity: true,
        withQuickPromoteEligibilityTweetFields: true,
        withBirdwatchNotes: true,
        withVoice: true,
        withV2Timeline: true,
      }, {
        withArticleRichContentState: true,
        withArticlePlainText: true,
        withGrokAnalyze: true,
        withDisallowedReplyControls: false,
      });
      results.push({
        operationName: "TweetDetail",
        ...detail,
        capturedAt: new Date().toISOString(),
        pageUrl: location.href,
        manual: true,
      });
    } catch (e) {
      results.push({ operationName: "TweetDetail", ok: false, error: String(e) });
    }

    try {
      const single = await graphqlGet("TweetResultByRestId", queryIds.TweetResultByRestId, {
        tweetId: id,
        withCommunity: true,
        includePromotedContent: true,
        withBirdwatchNotes: true,
        withVoice: true,
      }, {
        withArticleRichContentState: true,
        withArticlePlainText: true,
      });
      results.push({
        operationName: "TweetResultByRestId",
        ...single,
        capturedAt: new Date().toISOString(),
        pageUrl: location.href,
        manual: true,
      });
    } catch (e) {
      results.push({ operationName: "TweetResultByRestId", ok: false, error: String(e) });
    }

    post("deep-capture-result", { ok: true, tweetId: id, results });
    for (const r of results) {
      if (r.data) {
        post("graphql-capture", {
          capturedAt: r.capturedAt || new Date().toISOString(),
          pageUrl: location.href,
          manual: true,
          request: {
            url: r.url,
            method: "GET",
            operationName: r.operationName,
            variables: { focalTweetId: id, tweetId: id },
          },
          response: r.data,
          httpStatus: r.status,
          ok: r.ok,
        });
      }
    }
  }

  window.addEventListener("message", (event) => {
    if (event.source !== window || !event.data || event.data.source !== SOURCE) return;
    if (event.data.type === "deep-capture-request") {
      deepCaptureTweet(event.data.payload?.tweetId);
    }
  });
})();
