import {
  getTweetMediaList,
  parseArticleBodySegments,
  bestVideoMp4Url,
  isArticleCoverMedia,
} from "../lib/media.js";

const ICONS = {
  reply: `<svg viewBox="0 0 24 24"><path d="M1.751 10c0-4.42 3.584-8 8.005-8h4.366c4.49 0 8.129 3.64 8.129 8.13 0 2.96-1.607 5.68-4.196 7.11l-8.054 4.46v-3.69h-.067c-4.49.1-8.183-3.51-8.183-8.01zm8.005-6c-3.317 0-6.005 2.69-6.005 6 0 3.37 2.77 6.08 6.138 6.01l.351-.01h1.761v2.3l5.087-2.81c1.951-1.08 3.163-3.13 3.163-5.36 0-3.39-2.744-6.13-6.129-6.13H9.756z"/></svg>`,
  repost: `<svg viewBox="0 0 24 24"><path d="M4.5 3.88l4.432 4.14-1.364 1.46L5.5 7.55V16c0 1.1.896 2 2 2H13v2H7.5c-2.209 0-4-1.79-4-4V7.55L1.432 9.48.068 8.02 4.5 3.88zM16.5 6H11V4h5.5c2.209 0 4 1.79 4 4v8.45l2.068-1.93 1.364 1.46-4.432 4.14-4.432-4.14 1.364-1.46 2.068 1.93V8c0-1.1-.896-2-2-2z"/></svg>`,
  like: `<svg viewBox="0 0 24 24"><path d="M16.697 5.5c-1.222-.06-2.679.51-3.89 2.16l-.805 1.09-.806-1.09C9.984 6.01 8.526 5.44 7.304 5.5c-1.58.07-2.907 1.08-3.307 2.66-.38 1.49.27 3.19 1.72 4.92 1.213 1.4 3.28 3.36 5.5 5.5 2.22-2.14 4.287-4.1 5.5-5.5 1.45-1.73 2.1-3.43 1.72-4.92-.4-1.58-1.727-2.59-3.307-2.66z"/></svg>`,
  views: `<svg viewBox="0 0 24 24"><path d="M8.75 21V3h2v18h-2zM18 21V8.5h2V21h-2zM4 21l.004-10h2L6 21H4zm9.248 0v-7h2v7h-2z"/></svg>`,
  bookmark: `<svg viewBox="0 0 24 24"><path d="M4 4.5C4 3.12 5.119 2 6.5 2h11C18.881 2 20 3.12 20 4.5v18.44l-8-5.71-8 5.71V4.5z"/></svg>`,
  repostLabel: `<svg viewBox="0 0 24 24"><path d="M4.5 3.88l4.432 4.14-1.364 1.46L5.5 7.55V16c0 1.1.896 2 2 2H13v2H7.5c-2.209 0-4-1.79-4-4V7.55L1.432 9.48.068 8.02 4.5 3.88zM16.5 6H11V4h5.5c2.209 0 4 1.79 4 4v8.45l2.068-1.93 1.364 1.46-4.432 4.14-4.432-4.14 1.364-1.46 2.068 1.93V8c0-1.1-.896-2-2-2z"/></svg>`,
  verified: `<svg viewBox="0 0 22 22" class="verified"><path d="M20.396 11c-.018-.646-.215-1.275-.57-1.816-.354-.54-.852-.972-1.438-1.246.223-.607.27-1.264.14-1.897-.131-.634-.437-1.218-.882-1.687-.47-.445-1.053-.75-1.687-.882-.633-.13-1.29-.083-1.897.14-.273-.587-.704-1.086-1.245-1.44S11.647 1.62 11 1.604c-.646.017-1.273.213-1.813.568s-.969.854-1.24 1.44c-.608-.223-1.267-.272-1.902-.14-.635.13-1.22.436-1.69.882-.445.47-.749 1.055-.878 1.69-.13.635-.08 1.292.144 1.9-.587.274-1.087.705-1.443 1.245-.356.54-.555 1.17-.574 1.817.02.647.218 1.276.574 1.817.356.54.856.972 1.443 1.245-.224.608-.274 1.265-.144 1.9.13.636.433 1.221.878 1.69.47.445 1.055.751 1.69.882.636.13 1.294.083 1.902-.143.274.586.705 1.084 1.246 1.439.54.354 1.17.551 1.816.569.647-.016 1.276-.213 1.817-.567s-.969-.854 1.245-1.44c.604.239 1.266.296 1.903.164.636-.132 1.22-.447 1.68-.907.46-.46.776-1.044.908-1.681s.075-1.299-.165-1.903c.586-.274 1.084-.705 1.439-1.246.354-.54.551-1.17.569-1.816zM9.662 14.85l-3.429-3.428 1.293-1.302 2.072 2.072 4.4-4.794 1.347 1.246z"/></svg>`,
};

function escapeHtml(str) {
  if (str == null) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function formatCount(n) {
  if (n == null || n === "") return "";
  const num = Number(n);
  if (Number.isNaN(num)) return String(n);
  if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  if (num >= 1_000) return `${(num / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  return String(num);
}

function formatTime(createdAt) {
  if (!createdAt) return "";
  const d = new Date(createdAt);
  if (Number.isNaN(d.getTime())) return escapeHtml(createdAt);
  const now = new Date();
  const diff = (now - d) / 1000;
  if (diff < 60) return `${Math.floor(diff)}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  const opts = { month: "short", day: "numeric" };
  if (d.getFullYear() !== now.getFullYear()) opts.year = "numeric";
  return d.toLocaleDateString(undefined, opts);
}

function avatarUrl(author) {
  const url = author?.profile_image_url_https;
  if (!url) return "";
  return url.replace("_normal", "_400x400");
}

function renderEntities(text, entities) {
  if (!text) return "";
  const spans = [];
  const push = (list, type, cls) => {
    for (const e of list || []) {
      const start = e.indices?.[0];
      const end = e.indices?.[1];
      if (start == null || end == null) continue;
      spans.push({ start, end, type, cls, entity: e });
    }
  };
  push(entities?.hashtags, "hashtag", "hash");
  push(entities?.user_mentions, "mention", "mention");
  push(entities?.urls, "url", "url");
  push(entities?.symbols, "symbol", "hash");

  if (!spans.length) return escapeHtml(text).replace(/\n/g, "<br>");

  spans.sort((a, b) => a.start - b.start);
  let html = "";
  let cursor = 0;
  for (const s of spans) {
    if (s.start < cursor) continue;
    html += escapeHtml(text.slice(cursor, s.start));
    const slice = text.slice(s.start, s.end);
    if (s.type === "url") {
      const href = escapeHtml(s.entity.expanded_url || s.entity.url || slice);
      const label = escapeHtml(s.entity.display_url || slice);
      html += `<a href="${href}" target="_blank" rel="noopener noreferrer">${label}</a>`;
    } else if (s.type === "hashtag" || s.type === "symbol") {
      const tag = escapeHtml(slice);
      html += `<a href="https://x.com/hashtag/${encodeURIComponent(slice.replace(/^#/, ""))}" class="${s.cls}" target="_blank" rel="noopener">${tag}</a>`;
    } else if (s.type === "mention") {
      const sn = s.entity.screen_name || slice.replace(/^@/, "");
      html += `<a href="https://x.com/${encodeURIComponent(sn)}" class="${s.cls}" target="_blank" rel="noopener">@${escapeHtml(sn)}</a>`;
    } else {
      html += escapeHtml(slice);
    }
    cursor = s.end;
  }
  html += escapeHtml(text.slice(cursor));
  return html.replace(/\n/g, "<br>");
}

function bestVideoVariant(videoInfo) {
  const variants = videoInfo?.variants || [];
  const mp4s = variants.filter((v) => v.content_type === "video/mp4" && v.url);
  if (!mp4s.length) return variants.find((v) => v.url)?.url || null;
  mp4s.sort((a, b) => (b.bitrate || 0) - (a.bitrate || 0));
  return mp4s[0].url;
}

function renderOneMedia(m) {
  if (!m) return "";
  const type = m.type || "photo";
  if (type === "photo") {
    const src = m.media_url_https || m.url || m.src;
    if (!src) return "";
    const alt = m.ext_alt_text ? `<span class="media-alt">${escapeHtml(m.ext_alt_text)}</span>` : "";
    return `<div class="media-inline"><img src="${escapeHtml(src)}" alt="${escapeHtml(m.ext_alt_text || "")}" loading="lazy" />${alt}</div>`;
  }
  const poster = m.media_url_https || m.src || "";
  const src = m.mp4_url || bestVideoMp4Url(m.video_info) || bestVideoVariant(m.video_info);
  if (!src) return "";
  return `<div class="media-inline"><video controls preload="metadata" poster="${escapeHtml(poster)}" src="${escapeHtml(src)}"></video></div>`;
}

function renderMedia(mediaList) {
  const items = (mediaList || []).filter(
    (m) => m && (m.type === "photo" || m.type === "video" || m.type === "animated_gif" || m.video_info || m.media_url_https)
  );
  if (!items.length) return "";

  if (items.length === 1) return renderOneMedia(items[0]);

  const count = Math.min(items.length, 4);
  const cells = items.slice(0, 4).map((m) => {
    const html = renderOneMedia(m);
    return html ? html.replace('media-inline', 'media-cell') : "";
  }).filter(Boolean);
  if (!cells.length) return "";
  let html = `<div class="media-grid count-${count}">${cells.join("")}</div>`;
  if (items.length > 4) {
    html += items.slice(4).map((m) => renderOneMedia(m)).join("");
  }
  return html;
}

function renderArticle(article) {
  if (!article) return "";
  const title = article.title || article.note_tweet_text;
  const fullText =
    article.plain_text ||
    article.dom_capture?.plainText ||
    article.preview_text;
  const isFull = article.full_capture || (fullText && fullText.length > 320);
  const preview = isFull ? fullText : article.preview_text || fullText;
  let cover = "";
  const coverMedia =
    article.cover_media?.media_info ||
    article._rawArticle?.cover_media?.media_info;
  if (coverMedia?.original_img_url) {
    cover = `<img class="article-cover" src="${escapeHtml(coverMedia.original_img_url)}" alt="" loading="lazy" />`;
  }
  const inlineMedia = (article.media || [])
    .filter((m) => m.src)
    .map(
      (m) =>
        `<img class="article-inline-img" src="${escapeHtml(m.src)}" alt="${escapeHtml(m.alt || "")}" loading="lazy" />`
    )
    .join("");

  if (!title && !preview && !cover && !inlineMedia) return "";

  const segments = article.body_segments || parseArticleBodySegments(article);
  let bodyHtml = "";
  if (segments?.length) {
    bodyHtml = segments
      .map((seg) => {
        if (seg.type === "text" && seg.text) {
          return `<p class="article-paragraph">${escapeHtml(seg.text).replace(/\n/g, "<br>")}</p>`;
        }
        if (seg.type === "media" && seg.media) {
          if (cover && isArticleCoverMedia(seg.media, article)) return "";
          return renderOneMedia(seg.media);
        }
        return "";
      })
      .join("");
  } else if (isFull) {
    bodyHtml = `<div class="article-full-text">${escapeHtml(preview).replace(/\n\n/g, "</p><p>").replace(/\n/g, "<br>")}</div>`;
  } else if (preview) {
    bodyHtml = `<p class="article-preview">${escapeHtml(preview.slice(0, 500))}</p>`;
  }

  const link = article.source_url
    ? `<a class="article-link" href="${escapeHtml(article.source_url)}" target="_blank" rel="noopener">Read on X</a>`
    : `<span class="article-link">Article</span>`;

  return `
    <div class="article-card${isFull ? " article-full" : ""}">
      ${cover}
      <div class="article-body">
        ${title ? `<h3 class="article-title">${escapeHtml(title)}</h3>` : ""}
        ${bodyHtml}
        ${link}
      </div>
    </div>`;
}

function renderLinkCard(card) {
  if (!card?.binding_values) return "";
  const vals = {};
  for (const b of card.binding_values) {
    if (b.key) vals[b.key] = b.value?.string_value || b.value?.image_value?.url;
  }
  const title = vals.title || vals.card_title;
  const desc = vals.description;
  const domain = vals.domain || vals.card_url;
  const img = vals.thumbnail_image || vals.summary_photo_image || vals.photo_image_full_size;
  if (!title && !desc && !img) return "";

  return `
    <a class="link-card" href="${escapeHtml(vals.card_url || "#")}" target="_blank" rel="noopener noreferrer">
      ${img ? `<img src="${escapeHtml(img)}" alt="" loading="lazy" />` : ""}
      <div class="link-body">
        ${domain ? `<div class="link-domain">${escapeHtml(domain)}</div>` : ""}
        ${title ? `<div class="link-title">${escapeHtml(title)}</div>` : ""}
        ${desc ? `<div class="link-desc">${escapeHtml(desc)}</div>` : ""}
      </div>
    </a>`;
}

function getRetweetedTweet(tweet) {
  const raw = tweet._rawTweet;
  const rt =
    raw?.legacy?.retweeted_status_result?.result ||
    raw?.retweeted_status_result?.result ||
    tweet.retweeted_status_result?.result;
  if (rt) return unwrapForDisplay(rt);
  return null;
}

function unwrapForDisplay(node) {
  if (!node) return null;
  if (node.rest_id || node.full_text || node.legacy) {
    return node.legacy ? { ...node, full_text: node.full_text || node.legacy?.full_text } : node;
  }
  if (node.tweet) return unwrapForDisplay(node.tweet);
  if (node.result) return unwrapForDisplay(node.result);
  return node;
}

function renderTweetHeader(tweet, compact = false) {
  const author = tweet.author || {};
  const name = escapeHtml(author.name || "Unknown");
  const handle = escapeHtml(author.screen_name || "unknown");
  const verified = author.verified ? ICONS.verified : "";
  const time = formatTime(tweet.created_at);
  const id = tweet.rest_id || tweet.id_str;
  const link = id ? `https://x.com/${handle}/status/${id}` : "#";

  return `
    <div class="tweet-header">
      <span class="name" title="${name}">${name}</span>
      ${verified}
      <span class="handle">@${handle}</span>
      <span class="dot">·</span>
      <a class="time" href="${escapeHtml(link)}" target="_blank" rel="noopener" title="${escapeHtml(tweet.created_at || "")}">${time}</a>
    </div>`;
}

function renderTweetActions(tweet) {
  const views = tweet.views?.count ?? tweet.views;
  return `
    <div class="tweet-actions">
      <span class="action reply" title="Replies">${ICONS.reply}<span>${formatCount(tweet.reply_count)}</span></span>
      <span class="action repost" title="Reposts">${ICONS.repost}<span>${formatCount(tweet.retweet_count)}</span></span>
      <span class="action like" title="Likes">${ICONS.like}<span>${formatCount(tweet.favorite_count)}</span></span>
      <span class="action views" title="Views">${ICONS.views}<span>${formatCount(views)}</span></span>
      <span class="action bookmark" title="Bookmarks">${ICONS.bookmark}<span>${formatCount(tweet.bookmark_count)}</span></span>
    </div>`;
}

export function renderTweet(tweet, { nested = false, showForensicId = false } = {}) {
  if (!tweet) return "";

  const retweeted = getRetweetedTweet(tweet);
  const displayTweet = retweeted || tweet;
  const retweeter = retweeted ? tweet.author : null;

  const author = displayTweet.author || {};
  const avatar = avatarUrl(author);
  const replyTo = displayTweet.in_reply_to_screen_name;
  const replyContext = replyTo
    ? `<div class="reply-context">Replying to <a href="https://x.com/${escapeHtml(replyTo)}">@${escapeHtml(replyTo)}</a></div>`
    : "";

  const text = renderEntities(displayTweet.full_text || "", displayTweet.entities);
  const media = renderMedia(getTweetMediaList(displayTweet));
  const article = renderArticle(displayTweet.article);
  const card = renderLinkCard(displayTweet.card);
  const quote = displayTweet.quoted_tweet
    ? `<div class="quote-tweet">${renderTweet(displayTweet.quoted_tweet, { nested: true })}</div>`
    : "";

  const forensic =
    showForensicId && (tweet.rest_id || tweet.id_str)
      ? `<div class="forensic-badge">ID ${escapeHtml(tweet.rest_id || tweet.id_str)}</div>`
      : "";

  const body = `
    <img class="avatar" src="${escapeHtml(avatar)}" alt="" width="40" height="40" loading="lazy" />
    <div class="tweet-body">
      ${renderTweetHeader(displayTweet, nested)}
      ${replyContext}
      <div class="tweet-text">${text || "<em>No text</em>"}</div>
      ${media}
      ${article}
      ${card}
      ${quote}
      ${renderTweetActions(displayTweet)}
      ${forensic}
    </div>`;

  if (retweeted && retweeter && !nested) {
    const rName = escapeHtml(retweeter.name || retweeter.screen_name || "User");
    return `
      <article class="tweet retweet-header-wrap" data-tweet-id="${escapeHtml(tweet.rest_id || tweet.id_str || "")}">
        <div class="retweet-label">${ICONS.repostLabel}${rName} reposted</div>
        <div class="tweet-inner">${body}</div>
      </article>`;
  }

  const tag = nested ? "div" : "article";
  const cls = nested ? "quote-inner" : "tweet";
  return `
    <${tag} class="${cls}" data-tweet-id="${escapeHtml(displayTweet.rest_id || displayTweet.id_str || "")}">
      ${nested ? "" : '<div class="tweet-inner">'}
      ${body}
      ${nested ? "" : "</div>"}
    </${tag}>`;
}

export function renderCaptureMeta(capture) {
  const op = capture.forensic
    ? `Forensic capture (focal ${capture.focalTweetId || ""})`
    : capture.source?.operationName || "Capture";
  const time = new Date(capture.capturedAt).toLocaleString();
  const url = capture.pageUrl
    ? `<a href="${escapeHtml(capture.pageUrl)}" target="_blank" rel="noopener">source page</a>`
    : "";
  return `
    <div class="capture-meta-bar">
      <strong>${escapeHtml(op)}</strong> · ${escapeHtml(time)} ·
      ${capture.summary?.tweetCount ?? 0} tweet(s)
      ${url ? ` · ${url}` : ""}
      ${capture.integrity?.sha256 ? `<br><span title="SHA-256">hash ${escapeHtml(capture.integrity.sha256.slice(0, 16))}…</span>` : ""}
      ${(capture.summary?.warnings || []).length ? `<br><span class="capture-warn">Warnings: ${escapeHtml((capture.summary.warnings || []).join("; "))}</span>` : ""}
      ${capture.summary?.focalHasMedia === false ? `<br><span class="capture-warn">No media detected on focal tweet — export JSON to debug</span>` : ""}
    </div>`;
}

export function renderFeedHeader(title, subtitle) {
  return `
    <header class="feed-header">
      <h2>${escapeHtml(title)}</h2>
      ${subtitle ? `<p>${escapeHtml(subtitle)}</p>` : ""}
    </header>`;
}

export function renderEmpty() {
  return `
    <div class="empty-state">
      <h2>No captures yet</h2>
      <p>Browse X with the extension enabled, then open this viewer again.</p>
    </div>`;
}
