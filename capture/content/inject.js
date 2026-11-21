/**
 * Runs in the page MAIN world. Intercepts fetch/XHR to X GraphQL and REST APIs.
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";
  const GRAPHQL_PATH = "/i/api/graphql/";
  const REST_API_PATH = "/i/api/";

  const CAPTURE_OPERATIONS = new Set([
    "TweetDetail",
    "TweetResultByRestId",
    "TweetResultsByRestIds",
    "HomeTimeline",
    "HomeLatestTimeline",
    "UserTweets",
    "UserTweetsAndReplies",
    "UserMedia",
    "UserHighlightsTweets",
    "SearchTimeline",
    "ListLatestTweetsTimeline",
    "Likes",
    "Bookmarks",
    "BookmarkFolderTimeline",
    "CommunityTweetsTimeline",
    "TweetDetailWithInjectedTweet",
    "ArticleTimeline",
    "ArticleTweets",
    "TweetRelatedTweets",
    "ConversationTimeline",
    "ProfileSpotlights",
  ]);

  function post(type, payload) {
    window.postMessage({ source: SOURCE, type, payload }, "*");
  }

  function parseGraphQLOperation(url) {
    try {
      const u = new URL(url, location.origin);
      if (!u.pathname.includes(GRAPHQL_PATH)) return null;
      const parts = u.pathname.split("/");
      const operationName = parts[parts.length - 1];
      const queryId = parts[parts.length - 2];
      let variables = null;
      let features = null;
      const varsParam = u.searchParams.get("variables");
      const featParam = u.searchParams.get("features");
      if (varsParam) {
        try {
          variables = JSON.parse(varsParam);
        } catch (_) {
          variables = varsParam;
        }
      }
      if (featParam) {
        try {
          features = JSON.parse(featParam);
        } catch (_) {
          features = featParam;
        }
      }
      return { operationName, queryId, variables, features, method: "GET" };
    } catch (_) {
      return null;
    }
  }

  function parseGraphQLBody(body, url) {
    if (!body) return parseGraphQLOperation(url);
    try {
      const text = typeof body === "string" ? body : null;
      if (!text) return parseGraphQLOperation(url);
      const json = JSON.parse(text);
      const op =
        json.operationName ||
        (url && parseGraphQLOperation(url)?.operationName) ||
        null;
      return {
        operationName: op,
        queryId: json.queryId || (url && parseGraphQLOperation(url)?.queryId),
        variables: json.variables
          ? typeof json.variables === "string"
            ? JSON.parse(json.variables)
            : json.variables
          : null,
        features: json.features
          ? typeof json.features === "string"
            ? JSON.parse(json.features)
            : json.features
          : null,
        method: "POST",
      };
    } catch (_) {
      return parseGraphQLOperation(url);
    }
  }

  function shouldCapture(url, meta) {
    if (!url || typeof url !== "string") return false;
    const full = url.startsWith("http") ? url : location.origin + url;
    if (!full.includes("x.com") && !full.includes("twitter.com")) return false;

    if (meta?.operationName && CAPTURE_OPERATIONS.has(meta.operationName)) {
      return true;
    }

    if (full.includes(GRAPHQL_PATH)) {
      const parsed = parseGraphQLOperation(full);
      if (parsed?.operationName && CAPTURE_OPERATIONS.has(parsed.operationName)) {
        return true;
      }
      if (
        parsed?.operationName &&
        /tweet|timeline|conversation|article|bookmark/i.test(parsed.operationName)
      ) {
        return true;
      }
    }

    if (/\/i\/api\/.*\/status\//i.test(full) || /\/i\/api\/.*\/tweets/i.test(full)) {
      return true;
    }

    return false;
  }

  async function readResponseClone(response) {
    const clone = response.clone();
    const contentType = clone.headers.get("content-type") || "";
    if (!contentType.includes("json") && !contentType.includes("javascript")) {
      const text = await clone.text();
      try {
        return JSON.parse(text);
      } catch (_) {
        return { _rawText: text.slice(0, 500000) };
      }
    }
    return clone.json();
  }

  function emitCapture(requestMeta, responseData, status, ok) {
    post("graphql-capture", {
      capturedAt: new Date().toISOString(),
      pageUrl: location.href,
      request: requestMeta,
      response: responseData,
      httpStatus: status,
      ok,
    });
  }

  const originalFetch = window.fetch;
  window.fetch = async function (input, init) {
    const url =
      typeof input === "string"
        ? input
        : input instanceof Request
          ? input.url
          : input?.url || "";
    const method = (init?.method || (input instanceof Request ? input.method : "GET")).toUpperCase();
    let body = init?.body;
    if (!body && input instanceof Request && input.body) {
      try {
        body = await input.clone().text();
      } catch (_) {}
    }

    const meta =
      method === "POST" && body
        ? parseGraphQLBody(body, url)
        : parseGraphQLOperation(url);

    const response = await originalFetch.apply(this, arguments);

    if (shouldCapture(url, meta)) {
      try {
        const data = await readResponseClone(response);
        emitCapture(
          {
            url,
            method,
            operationName: meta?.operationName || null,
            queryId: meta?.queryId || null,
            variables: meta?.variables || null,
            features: meta?.features || null,
          },
          data,
          response.status,
          response.ok
        );
      } catch (err) {
        post("capture-error", { url, error: String(err) });
      }
    }

    return response;
  };

  const XHROpen = XMLHttpRequest.prototype.open;
  const XHRSend = XMLHttpRequest.prototype.send;

  XMLHttpRequest.prototype.open = function (method, url) {
    this._imtcUrl = url;
    this._imtcMethod = method;
    return XHROpen.apply(this, arguments);
  };

  XMLHttpRequest.prototype.send = function (body) {
    const xhr = this;
    const url = xhr._imtcUrl || "";
    const method = (xhr._imtcMethod || "GET").toUpperCase();
    const meta =
      method === "POST" && body
        ? parseGraphQLBody(body, url)
        : parseGraphQLOperation(url);

    if (shouldCapture(url, meta)) {
      xhr.addEventListener("load", function () {
        try {
          let data;
          const text = xhr.responseText;
          try {
            data = JSON.parse(text);
          } catch (_) {
            data = { _rawText: text?.slice(0, 500000) };
          }
          emitCapture(
            {
              url,
              method,
              operationName: meta?.operationName || null,
              queryId: meta?.queryId || null,
              variables: meta?.variables || null,
              features: meta?.features || null,
            },
            data,
            xhr.status,
            xhr.status >= 200 && xhr.status < 300
          );
        } catch (err) {
          post("capture-error", { url, error: String(err) });
        }
      });
    }

    return XHRSend.apply(this, arguments);
  };

  post("inject-ready", { href: location.href });
})();
