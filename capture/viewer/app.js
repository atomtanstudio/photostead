import { getAllCapturesForExport } from "../lib/storage.js";
import { sortConversationTweets } from "../lib/conversation.js";
import {
  buildDiagnosticExport,
  buildTextThreadExport,
} from "../lib/diagnostic-export.js";
import {
  renderTweet,
  renderCaptureMeta,
  renderFeedHeader,
  renderEmpty,
} from "./render.js";

const feed = document.getElementById("feed");
const placeholder = document.getElementById("placeholder");
const sessionList = document.getElementById("session-list");
const sessionPicker = document.getElementById("session-picker");
const viewMode = document.getElementById("view-mode");
const statsLine = document.getElementById("stats-line");

let captures = [];
let selectedCaptureId = null;

function getQueryCaptureId() {
  return new URLSearchParams(location.search).get("captureId");
}

function dedupeTweets(tweetList) {
  const byId = new Map();
  for (const t of tweetList) {
    const id = t?.rest_id || t?.id_str;
    if (!id) continue;
    const existing = byId.get(id);
    if (!existing || (t.full_text?.length || 0) > (existing.full_text?.length || 0)) {
      byId.set(id, t);
    }
  }
  return [...byId.values()];
}

function parseTwitterDate(createdAt) {
  if (!createdAt) return 0;
  const d = new Date(createdAt);
  if (!Number.isNaN(d.getTime())) return d.getTime();
  return Date.parse(createdAt) || 0;
}

function sortTweetsChronologically(tweets) {
  return [...tweets].sort((a, b) => parseTwitterDate(a.created_at) - parseTwitterDate(b.created_at));
}

function sortTweetsForThread(tweets) {
  const byId = new Map();
  for (const t of tweets) {
    const id = t.rest_id || t.id_str;
    if (id) byId.set(id, t);
  }

  const roots = tweets.filter((t) => {
    const parent = t.in_reply_to_status_id_str;
    return !parent || !byId.has(parent);
  });

  const ordered = [];
  const seen = new Set();

  function walk(t) {
    const id = t.rest_id || t.id_str;
    if (!id || seen.has(id)) return;
    seen.add(id);
    ordered.push(t);
    const replies = tweets.filter((r) => r.in_reply_to_status_id_str === id);
    replies.sort((a, b) => parseTwitterDate(a.created_at) - parseTwitterDate(b.created_at));
    for (const r of replies) walk(r);
  }

  roots.sort((a, b) => parseTwitterDate(a.created_at) - parseTwitterDate(b.created_at));
  for (const r of roots) walk(r);

  for (const t of tweets) {
    const id = t.rest_id || t.id_str;
    if (id && !seen.has(id)) ordered.push(t);
  }

  return ordered;
}

function tweetsFromCapture(capture) {
  return capture.tweets || [];
}

function renderFeed(html) {
  placeholder?.remove();
  feed.innerHTML = html;
}

function buildSessionButton(capture) {
  const li = document.createElement("li");
  const btn = document.createElement("button");
  const op = capture.source?.operationName || "Capture";
  const time = new Date(capture.capturedAt).toLocaleString();
  const count = capture.summary?.tweetCount ?? capture.tweets?.length ?? 0;
  btn.innerHTML = `
    <span class="session-title">${op}</span>
    <span class="session-meta">${time} · ${count} tweet(s)</span>
  `;
  btn.dataset.captureId = capture.captureId;
  if (capture.captureId === selectedCaptureId) btn.classList.add("active");
  btn.addEventListener("click", () => {
    selectedCaptureId = capture.captureId;
    viewMode.value = "session";
    sessionPicker.classList.remove("hidden");
    sessionPicker.value = capture.captureId;
    syncSessionListActive();
    renderCurrentView();
    history.replaceState(null, "", `?captureId=${encodeURIComponent(capture.captureId)}`);
  });
  li.appendChild(btn);
  return li;
}

function syncSessionListActive() {
  sessionList.querySelectorAll("button").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.captureId === selectedCaptureId);
  });
}

function populateSessionPicker() {
  sessionPicker.innerHTML = "";
  for (const c of captures) {
    const opt = document.createElement("option");
    opt.value = c.captureId;
    const op = c.source?.operationName || "Capture";
    opt.textContent = `${op} — ${new Date(c.capturedAt).toLocaleString()}`;
    sessionPicker.appendChild(opt);
  }
  if (selectedCaptureId) sessionPicker.value = selectedCaptureId;
}

function renderSessionList() {
  sessionList.innerHTML = "";
  const sorted = [...captures].sort(
    (a, b) => new Date(b.capturedAt) - new Date(a.capturedAt)
  );
  for (const c of sorted) {
    sessionList.appendChild(buildSessionButton(c));
  }
}

function renderCurrentView() {
  if (!captures.length) {
    renderFeed(renderEmpty());
    return;
  }

  if (viewMode.value === "session" && selectedCaptureId) {
    const capture = captures.find((c) => c.captureId === selectedCaptureId);
    if (!capture) {
      renderFeed(renderEmpty());
      return;
    }
    let tweets = tweetsFromCapture(capture);
    if (capture.fullArticle && capture.focalTweetId) {
      const focal = tweets.find(
        (t) => String(t.rest_id || t.id_str) === String(capture.focalTweetId)
      );
      if (focal) {
        focal.article = { ...focal.article, ...capture.fullArticle, full_capture: true };
      }
    }
    const isThread =
      capture.forensic ||
      capture.source?.operationName === "TweetDetail" ||
      tweets.some((t) => t.in_reply_to_status_id_str);
    tweets = capture.forensic
      ? sortConversationTweets(tweets, capture.focalTweetId || tweets[0]?.rest_id)
      : isThread
        ? sortTweetsForThread(tweets)
        : sortTweetsChronologically(tweets);

    const html = [
      renderFeedHeader(
        capture.source?.operationName || "Capture session",
        `${tweets.length} tweet(s) · captured ${new Date(capture.capturedAt).toLocaleString()}`
      ),
      renderCaptureMeta(capture),
      ...tweets.map((t) => renderTweet(t, { showForensicId: true })),
    ].join("");
    renderFeed(html);
    return;
  }

  const allTweets = dedupeTweets(captures.flatMap(tweetsFromCapture));
  const sorted = sortTweetsChronologically(allTweets).reverse();

  const html = [
    renderFeedHeader("All captured tweets", `${sorted.length} unique tweets`),
    ...sorted.map((t) => renderTweet(t, { showForensicId: false })),
  ].join("");
  renderFeed(html);
}

async function load() {
  feed.innerHTML = `<div class="feed-placeholder"><p>Loading captures…</p></div>`;
  captures = await getAllCapturesForExport();
  captures.sort((a, b) => new Date(b.capturedAt) - new Date(a.capturedAt));

  const totalTweets = dedupeTweets(captures.flatMap(tweetsFromCapture)).length;
  statsLine.textContent = `${captures.length} session(s) · ${totalTweets} unique tweet(s)`;

  const qid = getQueryCaptureId();
  if (qid && captures.some((c) => c.captureId === qid)) {
    selectedCaptureId = qid;
    viewMode.value = "session";
    sessionPicker.classList.remove("hidden");
  }

  populateSessionPicker();
  renderSessionList();
  renderCurrentView();
}

viewMode.addEventListener("change", () => {
  const isSession = viewMode.value === "session";
  sessionPicker.classList.toggle("hidden", !isSession);
  if (isSession && !selectedCaptureId && captures.length) {
    selectedCaptureId = captures[0].captureId;
    sessionPicker.value = selectedCaptureId;
  }
  if (!isSession) {
    history.replaceState(null, "", location.pathname);
  }
  syncSessionListActive();
  renderCurrentView();
});

sessionPicker.addEventListener("change", () => {
  selectedCaptureId = sessionPicker.value;
  syncSessionListActive();
  renderCurrentView();
  history.replaceState(null, "", `?captureId=${encodeURIComponent(selectedCaptureId)}`);
});


function downloadJson(data, filename) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

function getSelectedCapture() {
  if (!selectedCaptureId) return null;
  return captures.find((c) => c.captureId === selectedCaptureId) || null;
}

document.getElementById("btn-export-diagnostic")?.addEventListener("click", () => {
  const capture = getSelectedCapture();
  if (!capture) {
    alert("Select a capture session first.");
    return;
  }
  const diag = buildDiagnosticExport(capture);
  downloadJson(diag, `x-forensic-diagnostic-${selectedCaptureId}.json`);
});

document.getElementById("btn-copy-diagnostic")?.addEventListener("click", async () => {
  const capture = getSelectedCapture();
  if (!capture) {
    alert("Select a capture session first.");
    return;
  }
  const diag = buildDiagnosticExport(capture);
  const text = JSON.stringify(diag, null, 2);
  try {
    await navigator.clipboard.writeText(text);
    alert(`Copied diagnostic (${text.length} chars) — paste into chat.`);
  } catch (_) {
    downloadJson(diag, `x-forensic-diagnostic-${selectedCaptureId}.json`);
    alert("Clipboard blocked — downloaded diagnostic JSON instead.");
  }
});

document.getElementById("btn-export-text")?.addEventListener("click", () => {
  const capture = getSelectedCapture();
  if (!capture) {
    alert("Select a capture session first.");
    return;
  }
  const text = buildTextThreadExport(capture);
  const blob = new Blob([text], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `x-forensic-thread-${selectedCaptureId}.txt`;
  a.click();
  URL.revokeObjectURL(url);
});

document.getElementById("btn-export-session")?.addEventListener("click", () => {
  if (!selectedCaptureId) {
    alert("Select a capture session first (By capture session mode).");
    return;
  }
  const capture = captures.find((c) => c.captureId === selectedCaptureId);
  if (!capture) return;
  downloadJson(capture, `x-forensic-${selectedCaptureId}.json`);
});

document.getElementById("btn-refresh").addEventListener("click", load);

load();
