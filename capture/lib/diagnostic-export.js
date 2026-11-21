/**
 * Small exports suitable for chat / issue reports (not full forensic dumps).
 */

export function buildDiagnosticExport(capture) {
  if (!capture) return null;

  const tweets = capture.tweets || [];
  const focalId = String(capture.focalTweetId || "");

  const articleMediaCount = (article) =>
    (article?.body_segments || []).filter((segment) => segment?.type === "media").length +
    (article?.dom_inline_media?.length || 0) +
    (article?.media?.length || 0);

  return {
    schema: "x-forensic-diagnostic-v1",
    captureId: capture.captureId,
    capturedAt: capture.capturedAt,
    pageUrl: capture.pageUrl,
    focalTweetId: focalId,
    forensic: !!capture.forensic,
    summary: capture.summary || {},
    photostead: capture.photostead
      ? {
          configured: capture.photostead.configured,
          ok: capture.photostead.ok,
          error: capture.photostead.error,
          errorName: capture.photostead.errorName,
          baseUrl: capture.photostead.baseUrl,
          endpoint: capture.photostead.endpoint,
          materialId: capture.photostead.materialId,
          materialUrl: capture.photostead.materialUrl,
          status: capture.photostead.status,
          duplicate: capture.photostead.duplicate,
        }
      : null,
    warnings: capture.summary?.warnings || [],
    tweetCount: tweets.length,
    apiCalls: (capture.apiResponses || []).map((b) => ({
      op: b.operationName,
      ok: b.ok,
      parseError: b.parseError,
    })),
    tweets: tweets.map((t) => {
      const id = String(t.rest_id || t.id_str || "");
      const article = t.article;
      return {
        id,
        isFocal: id === focalId,
        author: t.author?.screen_name,
        created_at: t.created_at,
        in_reply_to: t.in_reply_to_status_id_str || null,
        textLen: (t.full_text || "").length,
        textPreview: (t.full_text || "").slice(0, 500),
        hasArticle: !!(article?.plain_text || article?.title),
        articleTitle: article?.title || null,
        articleTextLen: (article?.plain_text || "").length,
        mediaCount:
          (t.extended_entities?.media?.length || 0) +
          (t.dom_media?.length || 0) +
          articleMediaCount(article),
      };
    }),
    dom: capture.domForensic
      ? {
          visibleReplyIds: capture.domForensic.visibleReplyIds?.length ?? 0,
          focalMedia: capture.domForensic.focalMedia?.length ?? 0,
          repliesWithMedia: capture.domForensic.repliesMedia
            ? Object.keys(capture.domForensic.repliesMedia).length
            : 0,
          articleId: capture.domForensic.articleId,
        }
      : null,
  };
}

export function buildTextThreadExport(capture) {
  if (!capture?.tweets?.length) return "";
  const lines = [
    `X Forensic Capture — ${capture.captureId}`,
    `Page: ${capture.pageUrl || ""}`,
    `Captured: ${capture.capturedAt}`,
    `Tweets: ${capture.tweets.length}`,
    "",
  ];
  for (const t of capture.tweets) {
    const id = t.rest_id || t.id_str;
    const who = t.author?.screen_name || "?";
    lines.push(`--- @${who} (${id}) ---`);
    if (t.full_text) lines.push(t.full_text);
    if (t.article?.plain_text) {
      lines.push(`[Article: ${t.article.title || "untitled"}]`);
      lines.push(t.article.plain_text);
    }
    lines.push("");
  }
  return lines.join("\n");
}
