/**
 * Bulk capture for X Likes and Bookmarks pages.
 * Scrolls the list slowly and runs feed-safe forensic capture per top-level post.
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";
  const BULK_INTER_CAPTURE_MS = 18000;
  const BULK_ARTICLE_EXTRA_MS = 12000;
  const BULK_DUPLICATE_DELAY_MS = 1500;
  const BULK_ERROR_RETRY_MS = 8000;
  const BULK_SCROLL_MS = 2500;
  const BULK_CAPTURE_TIMEOUT_MS = 180000;
  const BULK_MAX_RETRIES = 3;
  const BULK_SESSION_KEY = "imtc-bulk-processed-v1";

  let isBulkCapturing = false;
  let bulkStats = { scanned: 0, captured: 0, skipped: 0, errors: 0, completed: 0 };
  const processedIds = new Set();
  const capturingNow = new Set();
  const retryCounts = new Map();
  let rateLimitPauseUntil = 0;
  let forensicScriptReady = false;

  function isArticlePost(article) {
    return Boolean(article?.querySelector('a[href*="/i/article/"]'));
  }

  function isRetryableBulkError(error) {
    const msg = String(error || "").toLowerCase();
    return /rate limit|429|timed out|timeout|network|fetch|photostead|abort|capture already|unknown forensic session|failed to save|save failed|\[p20|503|502|504|econn|socket/.test(
      msg
    );
  }

  async function waitForRateLimitCooldown() {
    const wait = rateLimitPauseUntil - Date.now();
    if (wait <= 0) return;
    updateHud(`Rate limited — pausing ${Math.ceil(wait / 1000)}s…`);
    await sleep(wait);
  }

  async function delayAfterResult(article, result) {
    await waitForRateLimitCooldown();
    if (result.duplicate || result.alreadySaved) {
      await sleep(BULK_DUPLICATE_DELAY_MS);
      return;
    }
    const delay = BULK_INTER_CAPTURE_MS + (isArticlePost(article) ? BULK_ARTICLE_EXTRA_MS : 0);
    await sleep(delay);
  }

  function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  async function ensureForensicScript() {
    if (forensicScriptReady || window.__imtcForensicCaptureInstalled) {
      forensicScriptReady = true;
      return;
    }
    await injectPageScript("content/forensic-capture.js");
    forensicScriptReady = true;
  }

  function injectPageScript(file) {
    return new Promise((resolve) => {
      const script = document.createElement("script");
      script.src = chrome.runtime.getURL(file);
      script.onload = () => {
        script.remove();
        resolve();
      };
      (document.documentElement || document.head).appendChild(script);
    });
  }

  function getTweetId(article) {
    const markedRoot = article?.dataset?.imtcRoot;
    if (markedRoot && /^\d{15,22}$/.test(markedRoot)) return markedRoot;

    const timeLink = article.querySelector("time")?.closest('a[href*="/status/"]');
    if (timeLink) {
      const match = timeLink.href.match(/\/status\/(\d+)/);
      if (match) return match[1];
    }
    for (const link of article.querySelectorAll('a[href*="/status/"]')) {
      const match = link.href.match(/\/status\/(\d+)/);
      if (match) return match[1];
    }
    return null;
  }

  function isThreadReply(article) {
    const cell = article.closest('[data-testid="cellInnerDiv"]');
    if (!cell) return false;
    if (/Replying to\s+@\w+/i.test(cell.textContent || "")) return true;
    return Boolean(cell.querySelector('div[style*="width: 2px"]'));
  }

  async function expandTweetInPlace(article) {
    if (!article) return;
    article.scrollIntoView({ block: "center", behavior: "instant" });
    await sleep(120);

    for (let round = 0; round < 3; round++) {
      const showMore =
        article.querySelector('[data-testid="tweet-text-show-more-link"]') ||
        [...article.querySelectorAll("span, button, div[role='button']")].find((el) =>
          /show more/i.test((el.textContent || "").trim())
        );
      if (!showMore) break;
      showMore.click();
      await sleep(250);
    }

    for (let round = 0; round < 2; round++) {
      let clicked = false;
      for (const button of article.querySelectorAll("button, a, div[role='button'], span[role='button']")) {
        const label = (button.textContent || "").trim();
        if (!/show\s*(this\s*)?(media|photo|sensitive)|view\s*(this\s*)?(media|photo|image|content)/i.test(label)) {
          continue;
        }
        button.click();
        clicked = true;
        await sleep(150);
      }
      for (const mask of article.querySelectorAll('[data-testid="mask"], [data-testid="interstitialGraphic"]')) {
        const target = mask.closest("button, a, div[role='button']") || mask;
        target.click();
        clicked = true;
        await sleep(150);
      }
      if (!clicked) break;
    }
  }

  function loadProcessedIds() {
    try {
      const raw = sessionStorage.getItem(BULK_SESSION_KEY);
      if (!raw) return;
      for (const id of JSON.parse(raw)) processedIds.add(String(id));
    } catch (_) {}
  }

  function persistProcessedIds() {
    try {
      sessionStorage.setItem(BULK_SESSION_KEY, JSON.stringify([...processedIds].slice(-500)));
    } catch (_) {}
  }

  function createHud() {
    document.getElementById("imtc-bulk-hud")?.remove();
    const hud = document.createElement("div");
    hud.id = "imtc-bulk-hud";
    hud.innerHTML = `
      <div class="imtc-bulk-hud-header">
        <div>
          <div class="imtc-bulk-hud-kicker">Photostead</div>
          <div class="imtc-bulk-hud-title">Bulk capture</div>
        </div>
        <button type="button" id="imtc-bulk-stop">Stop</button>
      </div>
      <div class="imtc-bulk-hud-stats">
        <div>Scanned <span id="imtc-bulk-scanned">0</span></div>
        <div class="captured">Captured <span id="imtc-bulk-captured">0</span></div>
        <div class="skipped">Skipped <span id="imtc-bulk-skipped">0</span></div>
        <div class="errors">Errors <span id="imtc-bulk-errors">0</span></div>
        <div>Completed <span id="imtc-bulk-completed">0</span></div>
      </div>
      <div class="imtc-bulk-hud-status" id="imtc-bulk-status">Starting…</div>
    `;
    document.body.appendChild(hud);
    hud.querySelector("#imtc-bulk-stop").addEventListener("click", () => {
      isBulkCapturing = false;
    });
  }

  function updateHud(statusText) {
    const set = (id, value) => {
      const el = document.getElementById(id);
      if (el) el.textContent = String(value);
    };
    set("imtc-bulk-scanned", bulkStats.scanned);
    set("imtc-bulk-captured", bulkStats.captured);
    set("imtc-bulk-skipped", bulkStats.skipped);
    set("imtc-bulk-errors", bulkStats.errors);
    set("imtc-bulk-completed", bulkStats.completed);
    if (statusText) {
      const status = document.getElementById("imtc-bulk-status");
      if (status) status.textContent = statusText;
    }
  }

  async function checkAlreadySaved(tweetId) {
    try {
      const response = await chrome.runtime.sendMessage({
        type: "check-capture-exists",
        payload: { externalId: tweetId, platform: "x" },
      });
      return Boolean(response?.exists);
    } catch (_) {
      return false;
    }
  }

  async function captureTweetForensic(tweetId, article) {
    if (capturingNow.has(tweetId)) return { ok: false, skipped: true };
    capturingNow.add(tweetId);
    article.dataset.imtcRoot = tweetId;
    await expandTweetInPlace(article);
    await ensureForensicScript();

    return new Promise((resolve) => {
      const timeout = setTimeout(() => {
        window.removeEventListener("message", onMessage);
        resolve({ ok: false, error: "Timed out waiting for capture" });
      }, BULK_CAPTURE_TIMEOUT_MS);

      function onMessage(event) {
        if (event.source !== window || event.data?.source !== SOURCE) return;
        const { type, payload } = event.data;
        if (payload?.tweetId !== tweetId) return;

        if (type === "forensic-capture-progress") {
          if (payload.stage === "rate-limit") {
            const waitMs = Number(payload.waitMs) || 60000;
            rateLimitPauseUntil = Math.max(rateLimitPauseUntil, Date.now() + waitMs);
            updateHud(`Rate limited (${payload.operationName || "API"}) — pausing ${Math.ceil(waitMs / 1000)}s…`);
          } else if (payload.stage) {
            updateHud(`${payload.stage}… ${tweetId}`);
          }
          return;
        }

        if (type === "forensic-ingest-error") {
          clearTimeout(timeout);
          window.removeEventListener("message", onMessage);
          resolve({ ok: false, error: payload.error || "Capture failed" });
        }

        if (type === "forensic-record-saved") {
          clearTimeout(timeout);
          window.removeEventListener("message", onMessage);
          const photostead = payload.photostead;
          if (photostead?.ok && photostead.duplicate) {
            resolve({ ok: true, duplicate: true, photostead });
            return;
          }
          if (photostead?.configured && !photostead?.ok) {
            resolve({ ok: false, error: photostead.error || "Photostead save failed" });
            return;
          }
          resolve({
            ok: true,
            photostead,
            partial: Boolean(payload.partial || payload.record?.summary?.partial),
          });
        }
      }

      window.addEventListener("message", onMessage);
      window.postMessage(
        {
          source: SOURCE,
          type: "forensic-capture-tweet",
          payload: {
            tweetId,
            bulkCapture: true,
            feedArticle: isArticlePost(article),
          },
        },
        "*"
      );
    }).finally(() => {
      capturingNow.delete(tweetId);
    });
  }

  async function captureWithRetries(tweetId, article) {
    let result = await captureTweetForensic(tweetId, article);
    let retries = retryCounts.get(tweetId) || 0;

    while (!result.ok && !result.skipped && retries < BULK_MAX_RETRIES && isRetryableBulkError(result.error)) {
      retries += 1;
      retryCounts.set(tweetId, retries);
      updateHud(`Retry ${retries}/${BULK_MAX_RETRIES} for ${tweetId}…`);
      await waitForRateLimitCooldown();
      await sleep(BULK_ERROR_RETRY_MS * retries);
      result = await captureTweetForensic(tweetId, article);
    }

    return result;
  }

  function pageKind() {
    const path = location.pathname;
    if (path.includes("/i/bookmarks")) return "bookmarks";
    if (/\/likes$/i.test(path)) return "likes";
    return "timeline";
  }

  async function startBulkCapture() {
    if (isBulkCapturing) return { started: false, reason: "already-running" };
    isBulkCapturing = true;
    bulkStats = { scanned: 0, captured: 0, skipped: 0, errors: 0, completed: 0 };
    processedIds.clear();
    retryCounts.clear();
    loadProcessedIds();
    createHud();
    updateHud(`Running on ${pageKind()} page…`);
    await ensureForensicScript();

    let unchangedScrollCount = 0;
    let lastScrollHeight = 0;

    while (isBulkCapturing) {
      const articles = [...document.querySelectorAll('article[data-testid="tweet"]')];
      for (const article of articles) {
        if (!isBulkCapturing) break;

        const tweetId = getTweetId(article);
        if (!tweetId || processedIds.has(tweetId)) continue;
        processedIds.add(tweetId);
        persistProcessedIds();

        if (isThreadReply(article)) {
          bulkStats.skipped += 1;
          updateHud(`Skipping thread reply ${tweetId}`);
          continue;
        }

        bulkStats.scanned += 1;
        updateHud(`Checking ${tweetId}…`);
        article.style.outline = "2px solid rgba(245, 158, 11, 0.45)";

        if (await checkAlreadySaved(tweetId)) {
          bulkStats.skipped += 1;
          bulkStats.completed += 1;
          article.style.outline = "2px solid rgba(113, 118, 123, 0.45)";
          updateHud(`Already saved ${tweetId}`);
          await delayAfterResult(article, { alreadySaved: true });
          continue;
        }

        updateHud(`Capturing ${tweetId}…`);
        const result = await captureWithRetries(tweetId, article);
        bulkStats.completed += 1;

        if (result.duplicate) {
          bulkStats.skipped += 1;
          article.style.outline = "2px solid rgba(113, 118, 123, 0.45)";
        } else if (result.ok) {
          bulkStats.captured += 1;
          article.style.outline = result.partial
            ? "2px solid rgba(245, 158, 11, 0.55)"
            : "2px solid rgba(0, 186, 124, 0.55)";
        } else if (result.skipped) {
          bulkStats.skipped += 1;
        } else {
          bulkStats.errors += 1;
          article.style.outline = "2px solid rgba(244, 33, 46, 0.55)";
        }

        updateHud(
          result.ok
            ? `${result.partial ? "Partial save" : "Saved"} ${tweetId}`
            : `${result.error || "Failed"} ${tweetId}`
        );
        await delayAfterResult(article, result);
      }

      const currentHeight = document.documentElement.scrollHeight;
      if (currentHeight === lastScrollHeight) {
        unchangedScrollCount += 1;
        if (unchangedScrollCount >= 24) break;
      } else {
        unchangedScrollCount = 0;
      }
      lastScrollHeight = currentHeight;

      window.scrollBy({ top: 700, behavior: "instant" });
      await sleep(BULK_SCROLL_MS);
    }

    isBulkCapturing = false;
    const title = document.querySelector(".imtc-bulk-hud-title");
    if (title) title.textContent = "Capture complete";
    updateHud(`Done — ${bulkStats.captured} saved, ${bulkStats.skipped} skipped, ${bulkStats.errors} errors`);
    const stopBtn = document.getElementById("imtc-bulk-stop");
    if (stopBtn) stopBtn.textContent = "Close";
    return { started: true, stats: { ...bulkStats } };
  }

  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (message.type === "start-bulk-capture") {
      startBulkCapture()
        .then(sendResponse)
        .catch((error) => sendResponse({ started: false, error: String(error) }));
      return true;
    }
    if (message.type === "stop-bulk-capture") {
      isBulkCapturing = false;
      sendResponse({ stopped: true });
      return true;
    }
    return false;
  });
})();
