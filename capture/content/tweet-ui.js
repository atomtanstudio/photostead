/**
 * Injects a forensic capture button at the end of each tweet action bar.
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";
  const BTN_ATTR = "data-imtc-injected";
  const DEFAULT_LABEL = "Save tweet images/video and text to Photostead";
  const SAVED_LABEL = "Saved in Photostead — click to recapture";

  // Photostead's mark (its favicon), drawn in the colour of X's own action icons.
  const LOGO_SVG = `<svg class="imtc-logo" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 10.6 12 3.6l8.5 7v9.1a.8.8 0 0 1-.8.8H4.3a.8.8 0 0 1-.8-.8z"/><circle cx="12" cy="14.2" r="3.3"/></svg>`;

  const CHECK_SVG = `<svg class="imtc-saved-check" viewBox="0 0 16 16" aria-hidden="true"><path d="M6.2 11.1 3.4 8.3l-1.1 1.1 3.9 3.9 8.4-8.4-1.1-1.1z"/></svg>`;

  const capturing = new Set();
  const savedCache = new Map();
  const pendingSavedChecks = new Map();
  let savedCheckTimer = null;

  function showToast(message, isError) {
    const existing = document.querySelector(".imtc-toast");
    existing?.remove();
    const el = document.createElement("div");
    el.className = "imtc-toast" + (isError ? " error" : "");
    el.textContent = message;
    document.body.appendChild(el);
    setTimeout(() => el.remove(), isError ? 5000 : 3500);
  }

  function setButtonLabel(btn, label) {
    btn.title = label;
    btn.setAttribute("aria-label", label);
  }

  function buttonMarkup() {
    return `<span class="imtc-btn-icon">${LOGO_SVG}<span class="imtc-saved-badge">${CHECK_SVG}</span></span>`;
  }

  function markButtonSaved(btn, tweetId) {
    btn.classList.add("imtc-saved");
    btn.classList.remove("imtc-done");
    setButtonLabel(btn, SAVED_LABEL);
    if (tweetId) savedCache.set(tweetId, true);
  }

  async function checkAlreadySaved(tweetId) {
    if (savedCache.has(tweetId)) {
      return savedCache.get(tweetId);
    }

    try {
      const response = await chrome.runtime.sendMessage({
        type: "check-capture-exists",
        payload: { externalId: tweetId, platform: "x" },
      });
      const exists = Boolean(response?.exists);
      savedCache.set(tweetId, exists);
      return exists;
    } catch (_) {
      return false;
    }
  }

  async function flushSavedChecks() {
    savedCheckTimer = null;
    const entries = [...pendingSavedChecks.entries()];
    pendingSavedChecks.clear();
    if (!entries.length) return;

    const concurrency = 6;
    for (let index = 0; index < entries.length; index += concurrency) {
      const batch = entries.slice(index, index + concurrency);
      await Promise.all(
        batch.map(async ([tweetId, btn]) => {
          if (!btn.isConnected) return;
          if (btn.classList.contains("imtc-saved") || btn.classList.contains("imtc-loading")) return;
          const exists = await checkAlreadySaved(tweetId);
          if (exists && btn.isConnected) markButtonSaved(btn, tweetId);
        })
      );
    }
  }

  function queueSavedCheck(tweetId, btn) {
    if (savedCache.get(tweetId) === true) {
      markButtonSaved(btn, tweetId);
      return;
    }
    if (savedCache.get(tweetId) === false) return;
    pendingSavedChecks.set(tweetId, btn);
    clearTimeout(savedCheckTimer);
    savedCheckTimer = setTimeout(flushSavedChecks, 150);
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
    const timeLink = article.querySelector("time")?.closest('a[href*="/status/"]');
    if (timeLink) {
      const m = timeLink.href.match(/\/status\/(\d+)/);
      if (m) return m[1];
    }
    const ids = [];
    for (const a of article.querySelectorAll('a[href*="/status/"]')) {
      const m = a.href.match(/\/status\/(\d+)/);
      if (m) ids.push(m[1]);
    }
    if (!ids.length) return null;
    const unique = [...new Set(ids)];
    if (unique.length === 1) return unique[0];
    return unique.sort((a, b) => {
      try {
        return Number(BigInt(b) - BigInt(a));
      } catch (_) {
        return b.localeCompare(a);
      }
    })[0];
  }

  function findActionBar(article) {
    const groups = article.querySelectorAll('[role="group"]');
    for (const group of groups) {
      const buttons = group.querySelectorAll("button, [role='button']");
      if (buttons.length >= 4) return group;
    }
    return (
      article.querySelector('[data-testid="reply"]')?.closest('[role="group"]') ||
      groups[groups.length - 1] ||
      null
    );
  }

  function findActionBarChild(actionBar, testId) {
    for (const child of actionBar.children) {
      if (child.querySelector(`[data-testid="${testId}"]`)) return child;
    }
    return null;
  }

  function measureIconGap(actionBar) {
    const pairs = [
      ["reply", "retweet"],
      ["retweet", "like"],
      ["like", "bookmark"],
    ];

    for (const [leftId, rightId] of pairs) {
      const leftSlot = findActionBarChild(actionBar, leftId);
      const rightSlot =
        findActionBarChild(actionBar, rightId) ||
        (rightId === "retweet" ? findActionBarChild(actionBar, "unretweet") : null);
      if (!leftSlot || !rightSlot) continue;
      const gap = rightSlot.getBoundingClientRect().left - leftSlot.getBoundingClientRect().right;
      if (gap >= 0 && gap < 80) return gap;
    }

    return 12;
  }

  function createCaptureSlot(actionBar, article, tweetId) {
    const slot = document.createElement("div");
    slot.className = "imtc-forensic-slot";

    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "imtc-forensic-btn";
    btn.setAttribute("data-testid", "photostead-capture");
    btn.setAttribute("data-tweet-id", tweetId);
    setButtonLabel(btn, DEFAULT_LABEL);
    btn.innerHTML = `<span class="imtc-btn-wrap"><span class="imtc-btn-inner">${buttonMarkup()}</span></span>`;

    btn.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      startCapture(article, tweetId, btn);
    });

    slot.appendChild(btn);
    return { slot, btn };
  }

  function insertCaptureButton(actionBar, slot) {
    // A normal flex item before Share stays clear of FeedSilo's after-Share overlay.
    const anchor = findActionBarChild(actionBar, "share");
    if (anchor) actionBar.insertBefore(slot, anchor);
    else actionBar.appendChild(slot);
    slot.style.flex = "0 0 35px";
  }

  function progressMessage(stage, payload) {
    if (stage === "preparing") return "Capturing… expanding text & revealing media";
    if (stage === "replies") return "Capturing… expanding reply thread";
    if (stage === "search") return "Capturing… fetching conversation via search";
    if (stage === "backfill") {
      return `Capturing… backfilling ${payload?.count ?? 0} replies`;
    }
    if (stage === "gap-fill") {
      const target =
        payload?.expectedReplyCount != null ? ` (target ${Number(payload.expectedReplyCount) + 1} posts)` : "";
      return `Capturing… filling ${payload?.count ?? 0} missing replies${target}`;
    }
    if (stage === "article") return "Capturing… fetching full article";
    return "Capturing… fetching tweet media from X";
  }

  async function startCapture(article, tweetId, btn) {
    if (capturing.has(tweetId)) return;
    capturing.add(tweetId);
    btn.classList.remove("imtc-saved", "imtc-done");
    btn.classList.add("imtc-loading");
    article.dataset.imtcRoot = tweetId;
    setButtonLabel(btn, "Capturing… started");
    showToast("Capturing tweet text and media…");

    await injectPageScript("content/forensic-capture.js");

    const finishUi = (isError, saved) => {
      capturing.delete(tweetId);
      btn.classList.remove("imtc-loading");
      if (saved) {
        markButtonSaved(btn, tweetId);
        return;
      }
      if (!isError) {
        btn.classList.add("imtc-done");
        setButtonLabel(btn, DEFAULT_LABEL);
        setTimeout(() => btn.classList.remove("imtc-done"), 2500);
        return;
      }
      if (btn.classList.contains("imtc-saved")) {
        setButtonLabel(btn, SAVED_LABEL);
      } else {
        setButtonLabel(btn, DEFAULT_LABEL);
      }
    };

    const onCaptureMessage = (event) => {
      if (event.source !== window || !event.data || event.data.source !== SOURCE) return;
      const { type, payload } = event.data;
      if (payload?.tweetId && payload.tweetId !== tweetId) return;

      if (type === "forensic-capture-progress" && payload?.tweetId === tweetId) {
        const stage = payload.stage || "";
        const msg = progressMessage(stage, payload);
        setButtonLabel(btn, msg);
        const toastMsg =
          stage === "preparing"
            ? "Expanding text & revealing media…"
            : stage === "replies"
              ? "Expanding reply thread…"
              : stage === "search"
                ? "Fetching conversation via search…"
                : stage === "backfill"
                  ? `Backfilling ${payload.count ?? 0} replies…`
                  : stage === "gap-fill"
                    ? `Filling ${payload.count ?? 0} missing replies…`
                    : stage === "article"
                      ? "Fetching full article…"
                      : "Fetching tweet media from X…";
        showToast(toastMsg);
        return;
      }

      if (type === "forensic-capture-end" && payload?.tweetId === tweetId) {
        setButtonLabel(btn, "Capturing… saving to Photostead");
        showToast("Saving capture in background…");
        return;
      }

      if (type === "forensic-ingest-error" && payload?.tweetId === tweetId) {
        window.removeEventListener("message", onCaptureMessage);
        finishUi(true, false);
        showToast(payload.error || "Failed to save capture", true);
        return;
      }

      if (type === "forensic-record-saved" && payload?.tweetId === tweetId) {
        window.removeEventListener("message", onCaptureMessage);
        const photostead = payload.photostead;
        const countHint = ` (${photostead?.assetCount || 0} attachment(s))`;
        const partialLabel = "";
        const saved = Boolean(photostead?.ok);
        finishUi(!saved, saved);
        if (photostead?.ok) {
          showToast(`Saved to Photostead${countHint}${partialLabel}.`);
          if (photostead.openAfterSave !== false && photostead.materialUrl) {
            chrome.runtime.sendMessage({
              type: "open-url",
              payload: { url: photostead.materialUrl },
            });
          }
        } else if (photostead?.configured) {
          showToast(`Photostead save failed: ${photostead.error || "unknown error"}`, true);
        } else {
          showToast(`Capture finished${countHint}${partialLabel}. Configure Photostead in the extension popup to save there.`, true);
        }
      }
    };

    window.addEventListener("message", onCaptureMessage);

    window.postMessage(
      {
        source: SOURCE,
        type: "forensic-capture-tweet",
        payload: { tweetId },
      },
      "*"
    );
  }

  function injectIntoArticle(article) {
    if (article.querySelector(".imtc-forensic-btn")) return;

    const tweetId = getTweetId(article);
    if (!tweetId) return;

    const actionBar = findActionBar(article);
    if (!actionBar) return;

    const { slot, btn } = createCaptureSlot(actionBar, article, tweetId);
    insertCaptureButton(actionBar, slot);

    article.setAttribute(BTN_ATTR, "1");
    queueSavedCheck(tweetId, btn);
  }

  function scan() {
    document.querySelectorAll('article[data-testid="tweet"]').forEach(injectIntoArticle);
  }

  const observer = new MutationObserver(() => {
    scan();
  });

  function init() {
    scan();
    observer.observe(document.body, { childList: true, subtree: true });
  }

  if (document.body) {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  }
})();
