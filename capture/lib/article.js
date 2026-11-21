import { parseArticleBodySegments, filterInlineArticleMedia } from "./media.js";

/**
 * Extract and normalize X Article (long-form) payloads from GraphQL + DOM.
 */

function deepWalk(obj, visit, depth = 0, seen = new WeakSet()) {
  if (!obj || depth > 30) return;
  if (typeof obj !== "object") return;
  if (seen.has(obj)) return;
  seen.add(obj);
  visit(obj);
  if (Array.isArray(obj)) {
    for (const x of obj) deepWalk(x, visit, depth + 1, seen);
    return;
  }
  for (const v of Object.values(obj)) deepWalk(v, visit, depth + 1, seen);
}

export function findArticleNodesInGraphQL(data) {
  const articles = [];
  deepWalk(data, (node) => {
    if (node?.__typename === "Article" || (node?.title && node?.content_state)) {
      articles.push(node);
    }
    if (node?.article_results?.result) {
      articles.push(node.article_results.result);
    }
  });
  const byId = new Map();
  for (const a of articles) {
    const id = a.rest_id || a.id;
    if (id) byId.set(String(id), a);
  }
  return [...byId.values()];
}

export function normalizeArticleRecord(articleNode, domCapture = null) {
  if (!articleNode && !domCapture) return null;

  const article = articleNode || {};
  const contentState = article.content_state || article.rich_content_state || null;
  let plainText = article.plain_text || article.article_plain_text || null;

  if (!plainText && contentState?.blocks) {
    plainText = blocksToPlainText(contentState.blocks);
  }
  if (!plainText && domCapture?.plainText) {
    plainText = domCapture.plainText;
  }

  const domEmbedded = domCapture?.embeddedContentState;
  const mergedContentState = contentState || domEmbedded || null;
  if (!plainText && domEmbedded?.blocks) {
    plainText = blocksToPlainText(domEmbedded.blocks);
  }

  return {
    article_id: article.rest_id || article.id || domCapture?.articleId,
    title: article.title || domCapture?.title,
    preview_text: article.preview_text,
    plain_text: plainText,
    content_state: mergedContentState,
    cover_media: article.cover_media,
    metadata: article.metadata,
    lifecycle_state: article.lifecycle_state,
    blocks: mergedContentState?.blocks || domCapture?.blocks || null,
    media: extractArticleMedia(contentState, domCapture),
    source_url: domCapture?.url || (article.rest_id ? `https://x.com/i/article/${article.rest_id}` : null),
    dom_capture: domCapture,
    body_segments: null,
    _raw: articleNode,
  };
}

function blocksToPlainText(blocks) {
  if (!Array.isArray(blocks)) return "";
  const parts = [];
  for (const block of blocks) {
    if (block.text) parts.push(block.text);
    if (block.content?.text) parts.push(block.content.text);
    if (Array.isArray(block.entities)) {
      for (const e of block.entities) {
        if (e.text) parts.push(e.text);
      }
    }
  }
  return parts.join("\n\n").trim();
}

function extractArticleMedia(contentState, domCapture) {
  const media = [];
  const seen = new Set();

  const push = (item) => {
    const key = item.src || item.media_url_https || item.mp4_url || "";
    if (!key || seen.has(key)) return;
    seen.add(key);
    media.push(item);
  };

  if (domCapture?.images?.length) {
    for (const img of domCapture.images) {
      push({ type: "photo", src: img.src, alt: img.alt, media_url_https: img.src });
    }
  }
  if (domCapture?.mediaBlocks?.length) {
    for (const db of domCapture.mediaBlocks) {
      if (!db?.src) continue;
      if (db.mediaType === "video") {
        push({
          type: "video",
          src: db.src,
          mp4_url: db.src,
          media_url_https: db.poster || db.src,
          video_info: { variants: [{ url: db.src, content_type: "video/mp4" }] },
        });
      } else {
        push({ type: "photo", src: db.src, media_url_https: db.src, alt: db.alt || "" });
      }
    }
  }

  const entities = contentState?.media_entities || contentState?.entity_map || {};
  for (const ent of Object.values(entities || {})) {
    if (!ent || typeof ent !== "object") continue;
    if (ent.media_url_https) {
      push({
        type: ent.type || "photo",
        src: ent.media_url_https,
        media_url_https: ent.media_url_https,
        alt: ent.ext_alt_text,
        video_info: ent.video_info,
      });
    }
  }

  deepWalk(contentState, (node) => {
    if (node?.media_url_https) {
      push({
        type: node.type || "photo",
        src: node.media_url_https,
        media_url_https: node.media_url_https,
        alt: node.ext_alt_text,
        video_info: node.video_info,
      });
    }
    if (node?.video_info?.variants) {
      const mp4 = node.video_info.variants.find((v) => v.content_type === "video/mp4");
      if (mp4?.url) push({ type: "video", src: mp4.url, mp4_url: mp4.url, video_info: node.video_info });
    }
  });
  return media;
}

export function mergeArticleRecords(apiArticles, domCapture, options = {}) {
  const preferredArticleId = options.preferredArticleId ? String(options.preferredArticleId) : null;
  const scopedArticles = preferredArticleId
    ? apiArticles.filter((article) => String(article?.rest_id || article?.id || "") === preferredArticleId)
    : apiArticles;
  let primary = scopedArticles[0] || apiArticles[0];
  if (preferredArticleId) {
    const matched = apiArticles.find((article) => String(article?.rest_id || article?.id || "") === preferredArticleId);
    if (matched) primary = matched;
  }
  const rec = normalizeArticleRecord(primary, domCapture);
  if (rec) {
    if (domCapture?.focalMedia?.length) {
      rec.dom_inline_media = filterInlineArticleMedia(domCapture.focalMedia, rec);
    }
    rec.body_segments = parseArticleBodySegments(rec);
  }
  return rec;
}
