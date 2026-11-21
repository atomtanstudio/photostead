/**
 * Relays chunked forensic capture to the service worker (build runs off the X page thread).
 */
const SOURCE = "photostead-capture";

function postToPage(type, payload) {
  window.postMessage({ source: SOURCE, type, payload }, "*");
}

function waitForPageMessage(type, tweetId, timeoutMs = 180000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      window.removeEventListener("message", handler);
      reject(new Error("Timed out waiting for missing replies"));
    }, timeoutMs);

    const handler = (event) => {
      if (event.source !== window || !event.data || event.data.source !== SOURCE) return;
      if (event.data.type !== type) return;
      if (tweetId && event.data.payload?.tweetId !== tweetId) return;
      clearTimeout(timer);
      window.removeEventListener("message", handler);
      resolve(event.data.payload);
    };
    window.addEventListener("message", handler);
  });
}

window.addEventListener("message", (event) => {
  if (event.source !== window || !event.data || event.data.source !== SOURCE) return;
  const { type, payload } = event.data;
  if (payload?.selectedOnly) return;
  const tweetId = payload?.tweetId;

  if (type === "forensic-capture-active") {
    chrome.runtime.sendMessage({ type: "set-forensic-active", payload }).catch(() => {});
    return;
  }

  if (type === "forensic-capture-begin") {
    chrome.runtime
      .sendMessage({ type: "forensic-capture-begin", payload })
      .catch((err) => postToPage("forensic-ingest-error", { tweetId, error: String(err) }));
    return;
  }

  if (type === "forensic-capture-chunk") {
    chrome.runtime.sendMessage({ type: "forensic-capture-chunk", payload }).catch(() => {});
    return;
  }

  if (type === "forensic-capture-end") {
    (async () => {
      try {
        let res = await chrome.runtime.sendMessage({ type: "forensic-capture-end", payload });
        if (res?.error) throw new Error(res.error);

        if (res.needsGapFill) {
          postToPage("forensic-capture-progress", {
            tweetId,
            stage: "gap-fill",
            count: res.missingIds?.length ?? 0,
            expectedReplyCount: res.expectedReplyCount ?? null,
          });
          postToPage("forensic-fetch-missing", {
            tweetId,
            sessionId: payload.sessionId,
            missingIds: res.missingIds || [],
            extraPagination: !!res.extraPagination,
            expectedReplyCount: res.expectedReplyCount ?? null,
          });
          let gapPayload = null;
          try {
            gapPayload = await waitForPageMessage("forensic-missing-complete", tweetId);
          } catch (_) {
            gapPayload = { apiResponses: [] };
          }
          res = await chrome.runtime.sendMessage({
            type: "forensic-gap-fill",
            payload: {
              sessionId: payload.sessionId,
              gapResponses: gapPayload.apiResponses || [],
            },
          });
          if (res?.error) throw new Error(res.error);
        }

        const settings = await chrome.runtime.sendMessage({ type: "get-stats" }).catch(() => ({}));
        const record = res.record;
        if (record && settings?.settings && !settings.settings.storeRawResponse) {
          delete record.rawResponse;
        }

        postToPage("forensic-record-saved", {
          tweetId,
          captureId: res.captureId,
          record: res.record,
          photostead: res.photostead,
          partial: record?.summary?.partial ?? payload?.partial,
          warnings: payload?.warnings,
        });
      } catch (err) {
        postToPage("forensic-ingest-error", { tweetId, error: String(err) });
      }
    })();
    return;
  }

  if (type === "forensic-capture-complete") {
    postToPage("forensic-ingest-error", {
      tweetId,
      error: payload?.error || "Legacy capture path failed; update extension",
    });
  }
});
