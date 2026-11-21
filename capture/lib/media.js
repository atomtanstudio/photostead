/**
 * Media normalization for tweets, articles, and DOM scrapes.
 */

export function bestVideoMp4Url(videoInfo) {
  const variants = videoInfo?.variants || [];
  const mp4s = variants.filter((v) => v.content_type === "video/mp4" && v.url);
  if (mp4s.length) {
    mp4s.sort((a, b) => (b.bitrate || 0) - (a.bitrate || 0));
    return mp4s[0].url;
  }
  return variants.find((v) => v.url)?.url || null;
}

export function normalizeMediaItem(m) {
  if (!m) return null;
  const type = m.type || (m.video_info ? "video" : "photo");
  const item = {
    type,
    id_str: m.id_str,
    media_key: m.media_key,
    url: m.url,
    media_url_https: m.media_url_https || m.media_url || m.src,
    ext_alt_text: m.ext_alt_text || m.alt || "",
    sizes: m.sizes,
    video_info: m.video_info,
    _raw: m,
  };
  if (type === "animated_gif" || type === "video") {
    item.mp4_url = bestVideoMp4Url(m.video_info);
  }
  return item;
}

export function mergeMediaLists(...lists) {
  const byKey = new Map();
  for (const list of lists) {
    for (const m of list || []) {
      const n = normalizeMediaItem(m);
      if (!n) continue;
      const key = n.media_key || n.id_str || n.media_url_https || n.mp4_url;
      if (!key) continue;
      if (!byKey.has(key)) byKey.set(key, n);
    }
  }
  return [...byKey.values()];
}

export function getTweetMediaList(tweet) {
  const legacy = tweet?._rawTweet?.legacy || {};
  const list = mergeMediaLists(
    tweet?.extended_entities?.media,
    tweet?.entities?.media,
    legacy.extended_entities?.media,
    legacy.entities?.media,
    tweet?.dom_media,
    extractNoteTweetMedia(tweet)
  );
  if (tweet?.article?.full_capture && tweet?.article?.body_segments?.some((s) => s.type === "media")) {
    return [];
  }
  return list;
}

function extractNoteTweetMedia(tweet) {
  const out = [];
  const note = tweet?.note_tweet || tweet?._rawTweet?.note_tweet;
  const inline = note?.media?.inline_media || note?.note_tweet_results?.result?.media?.inline_media;
  if (Array.isArray(inline)) {
    for (const m of inline) out.push(m);
  }
  const entitySet = note?.note_tweet_results?.result?.entity_set;
  if (entitySet && typeof entitySet === "object") {
    for (const ent of Object.values(entitySet)) {
      if (ent?.media_url_https || ent?.video_info) out.push(ent);
    }
  }
  return out;
}

/**
 * X Articles use Draft.js blocks + entityMap (array of {key, value}).
 */
export function buildArticleEntityLookup(contentState) {
  const lookup = new Map();
  if (!contentState) return lookup;

  const rawMap = contentState.entityMap || contentState.entity_map || contentState.media_entities;
  if (!rawMap) return lookup;

  const entries = Array.isArray(rawMap)
    ? rawMap
    : Object.entries(rawMap).map(([key, value]) => ({ key, value }));

  for (const entry of entries) {
    const key = entry?.key ?? entry?.entityKey;
    const value = entry?.value ?? entry;
    if (key == null) continue;
    lookup.set(String(key), value);
    const num = Number(key);
    if (!Number.isNaN(num)) lookup.set(num, value);
  }
  return lookup;
}

function mediaFromEntityValue(entityVal, mediaById) {
  if (!entityVal) return null;
  const data = entityVal.data || entityVal;
  const items = data.mediaItems || data.media_items || [];
  for (const item of items) {
    const mid = item.mediaId || item.media_id;
    if (mid && mediaById?.has(String(mid))) {
      return normalizeMediaItem(mediaById.get(String(mid)));
    }
  }
  if (data.media_url_https) return normalizeMediaItem(data);
  return null;
}

function collectMediaByIdFromArticle(article) {
  const byId = new Map();
  const walk = (obj, depth = 0) => {
    if (!obj || depth > 30) return;
    if (typeof obj !== "object") return;
    if (obj.media_url_https && (obj.id_str || obj.media_key)) {
      byId.set(String(obj.id_str || obj.media_key), obj);
    }
    if (Array.isArray(obj)) {
      for (const x of obj) walk(x, depth + 1);
      return;
    }
    for (const v of Object.values(obj)) walk(v, depth + 1);
  };
  walk(article?._raw);
  walk(article?._rawArticle);
  walk(article?.content_state);
  for (const m of article?.media || []) {
    if (m.id_str) byId.set(String(m.id_str), m);
  }
  return byId;
}

function blockText(block) {
  const text = block.text ?? block.content?.text ?? block.data?.text;
  if (!text) return "";
  return String(text).replace(/\s+/g, " ").trim();
}


/**
 * Stable key for twimg URLs (matches ?name= variants of the same file).
 */
export function mediaUrlKey(url) {
  if (!url) return "";
  const s = String(url);
  try {
    const u = new URL(s);
    const m = u.pathname.match(/\/media\/([^./]+)/);
    if (m) return m[1];
    return u.pathname;
  } catch (_) {
    return s.replace(/[?#].*$/, "");
  }
}

export function isSameMediaUrl(a, b) {
  if (!a || !b) return false;
  const ka = mediaUrlKey(a);
  const kb = mediaUrlKey(b);
  return ka.length > 2 && ka === kb;
}

/** URLs used for the article hero / cover (not inline body slots). */
export function getArticleCoverImageUrls(article) {
  if (!article) return [];
  const urls = [];
  const infos = [
    article.cover_media?.media_info,
    article._rawArticle?.cover_media?.media_info,
    article._raw?.cover_media?.media_info,
    article.cover_media,
  ];
  for (const info of infos) {
    if (!info) continue;
    for (const key of [
      "original_img_url",
      "original_img_url_https",
      "url",
      "media_url_https",
      "preview_image",
    ]) {
      if (info[key]) urls.push(info[key]);
    }
  }
  const meta = article.metadata || article._rawArticle?.metadata;
  if (meta?.preview_image_url) urls.push(meta.preview_image_url);
  return urls;
}

export function isArticleCoverMedia(mediaItem, article) {
  if (!mediaItem || !article) return false;
  const itemUrl =
    mediaItem.media_url_https || mediaItem.src || mediaItem.url || mediaItem.mp4_url;
  if (!itemUrl) return false;
  return getArticleCoverImageUrls(article).some((coverUrl) =>
    isSameMediaUrl(coverUrl, itemUrl)
  );
}

export function filterInlineArticleMedia(mediaList, article) {
  return (mediaList || [])
    .map((m) => normalizeMediaItem(m))
    .filter((m) => m && !isArticleCoverMedia(m, article));
}


/**
 * Build ordered article body segments (text + media inline).
 */
export function parseArticleBodySegments(article) {
  if (!article) return [];

  const contentState = article.content_state || article._raw?.content_state;
  const blocks = article.blocks || contentState?.blocks;
  const entityLookup = buildArticleEntityLookup(contentState);
  const mediaById = collectMediaByIdFromArticle(article);
  const domQueue = filterInlineArticleMedia(
    article.dom_inline_media || article.dom_capture?.focalMedia || [],
    article
  );

  const segments = [];

  const pushInlineMedia = (norm) => {
    if (!norm || isArticleCoverMedia(norm, article)) return false;
    const url = norm.media_url_https || norm.src || norm.mp4_url;
    const dup = segments.some(
      (s) =>
        s.type === "media" &&
        isSameMediaUrl(url, s.media?.media_url_https || s.media?.src || s.media?.mp4_url)
    );
    if (dup) return false;
    segments.push({ type: "media", media: norm });
    return true;
  };

  if (Array.isArray(blocks)) {
    for (const block of blocks) {
      const type = block.type || "unstyled";

      if (type === "atomic" || block.entityRanges?.length) {
        let placed = false;
        for (const range of block.entityRanges || []) {
          const key = range.key ?? range.entityKey;
          const entVal = entityLookup.get(key) ?? entityLookup.get(String(key));
          let norm = mediaFromEntityValue(entVal, mediaById);
          if (!norm && domQueue.length) norm = domQueue.shift();
          if (pushInlineMedia(norm)) placed = true;
        }
        if (!placed && domQueue.length) {
          const norm = domQueue.shift();
          pushInlineMedia(norm);
        }
        continue;
      }

      const text = blockText(block);
      if (!text) continue;

      if (type.startsWith("header")) {
        segments.push({ type: "text", text, style: type });
      } else {
        segments.push({ type: "text", text });
      }
    }
  }

  if (!segments.length && article.plain_text) {
    segments.push({ type: "text", text: article.plain_text });
  }

  const flatMedia = article.media || [];
  for (const m of flatMedia) {
    const norm = normalizeMediaItem(m);
    if (!norm) continue;
    pushInlineMedia(norm);
  }

  while (domQueue.length) {
    pushInlineMedia(domQueue.shift());
  }

  const domPieces = [
    ...(article.dom_capture?.blocks || []),
    ...(article.dom_capture?.mediaBlocks || []),
  ];
  if (domPieces.length) {
    return mergeDomSegments(segments, domPieces);
  }

  return segments;
}

function mergeDomSegments(apiSegments, domBlocks) {
  if (!domBlocks?.length) return apiSegments;
  if (!apiSegments?.length) {
    const out = [];
    for (const db of domBlocks) {
      if (db.text) out.push({ type: "text", text: db.text });
      else if (db.type === "media" && db.src) {
        out.push({
          type: "media",
          media: normalizeMediaItem({
            type: db.mediaType === "video" ? "video" : "photo",
            media_url_https: db.src,
            src: db.src,
            mp4_url: db.mediaType === "video" ? db.src : undefined,
          }),
        });
      }
    }
    return out;
  }
  const out = [...apiSegments];
  for (const db of domBlocks) {
    if (db.text && !out.some((s) => s.type === "text" && s.text === db.text)) {
      out.push({ type: "text", text: db.text });
    }
    if (db.type === "media" && db.src) {
      const exists = out.some(
        (s) =>
          s.type === "media" &&
          (s.media?.media_url_https === db.src || s.media?.mp4_url === db.src)
      );
      if (!exists) {
        out.push({
          type: "media",
          media: normalizeMediaItem({
            type: db.mediaType === "video" ? "video" : "photo",
            media_url_https: db.src,
            src: db.src,
            mp4_url: db.mediaType === "video" ? db.src : undefined,
          }),
        });
      }
    }
  }
  return out;
}
