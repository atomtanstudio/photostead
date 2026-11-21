/**
 * Isolated content script: injects page script and relays captures to the service worker.
 */
(function () {
  "use strict";

  const SOURCE = "photostead-capture";

  function injectScript(file) {
    const script = document.createElement("script");
    script.src = chrome.runtime.getURL(file);
    script.async = false;
    (document.documentElement || document.head || document.body).appendChild(script);
    script.onload = () => script.remove();
  }

  if (document.documentElement) {
    injectScript("content/inject.js");
  } else {
    document.addEventListener(
      "DOMContentLoaded",
      () => injectScript("content/inject.js"),
      { once: true }
    );
  }

  window.addEventListener("message", (event) => {
    if (event.source !== window || !event.data || event.data.source !== SOURCE) return;

    const { type, payload } = event.data;
    if (type === "photostead-selected-bundle") {
      chrome.runtime.sendMessage({ type: "ingest-forensic-bundle", payload }).then(res => {
        if (res?.error) throw new Error(res.error);
        window.postMessage({ source: SOURCE, type: "forensic-record-saved",
          payload: { tweetId: payload.tweetId, photostead: res.photostead } }, "*");
      }).catch(err => window.postMessage({ source: SOURCE, type: "forensic-ingest-error",
        payload: { tweetId: payload.tweetId, error: String(err) } }, "*"));
      return;
    }
    if (type === "inject-ready") return;
    if (type === "forensic-capture-active") {
      chrome.runtime.sendMessage({ type: "set-forensic-active", payload }).catch(() => {});
      return;
    }
    if (type === "forensic-capture-begin" || type === "forensic-capture-chunk" || type === "forensic-capture-end") {
      return;
    }

    chrome.runtime.sendMessage({ type, payload }).catch(() => {});
  });

  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (message.type === "request-deep-capture") {
      const tweetId = message.payload?.tweetId;
      if (tweetId) {
        injectScript("content/forensic-capture.js");
        window.postMessage(
          { source: SOURCE, type: "forensic-capture-tweet", payload: { tweetId } },
          "*"
        );
      } else {
        injectScript("content/page-actions.js");
        window.postMessage(
          { source: SOURCE, type: "deep-capture-request", payload: message.payload },
          "*"
        );
      }
      sendResponse({ ok: true });
      return true;
    }

    if (message.type === "ping") {
      sendResponse({ ok: true, href: location.href });
      return true;
    }
  });
})();
