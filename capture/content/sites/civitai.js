/**
 * CivitAI: a button on every image and video (the Images and Videos feeds, posts, model galleries,
 * profiles) and beside the download button on an image's own page. A save sends the original file,
 * with its prompt and generation settings (negative prompt, models and LoRAs, seed, sampler, steps,
 * CFG), the creator, and the post's title, read from CivitAI's own API.
 */
(function () {
  "use strict";
  const P = window.PhotosteadSite;
  if (!P) return;

  // Everything that depends on CivitAI's markup or API, in one place to fix when it changes.
  const SELECTORS = {
    // A picture or video in a feed, post or gallery: a link to its page around the media.
    cardLink: 'a[href*="/images/"]',
    cardMedia: "img, video",
    // An image's own page (/images/123): its toolbar, found by the download button's icon.
    detailPath: /^\/images\/(\d+)/,
    detailDownloadIcon: "svg.tabler-icon-download",
  };
  const API = {
    image: (id) => `/api/trpc/image.get?input=${encodeURIComponent(JSON.stringify({ json: { id } }))}`,
    generation: (id) => `/api/trpc/image.getGenerationData?input=${encodeURIComponent(JSON.stringify({ json: { id } }))}`,
    post: (id) => `/api/trpc/post.get?input=${encodeURIComponent(JSON.stringify({ json: { id } }))}`,
    rest: (id) => `/api/v1/images?imageId=${id}&nsfw=X`,
    // Originals are at image.civitai.com/<key>/<uuid>/original=true/<uuid>.<ext>.
    cdn: "https://image.civitai.com/xG1nkqKTMzGDvpLrqFT7WA",
  };
  const ID_IN_LINK = /\/images\/(\d+)(?:[/?#]|$)/;

  const EXT = { "image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif", "image/avif": "avif", "video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov" };

  // The generation settings worth keeping, in order, as "Name: value" lines.
  function settingsText(meta = {}, resources = []) {
    const lines = [];
    if (meta.negativePrompt) lines.push(`Negative prompt: ${meta.negativePrompt}`);
    const checkpoint = resources.find((r) => /checkpoint/i.test(r.modelType || ""));
    const model = checkpoint ? [checkpoint.modelName, checkpoint.versionName].filter(Boolean).join(" · ") : meta.Model || meta.model;
    if (model) lines.push(`Model: ${model}`);
    const others = resources.filter((r) => r !== checkpoint && r.modelName);
    if (others.length) {
      lines.push(`Resources: ${others.map((r) => `${r.modelName}${r.versionName ? ` (${r.versionName})` : ""}${r.modelType ? ` [${r.modelType}]` : ""}${r.strength != null ? ` ${r.strength}` : ""}`).join("; ")}`);
    }
    const parts = [
      ["Seed", meta.seed], ["Steps", meta.steps], ["Sampler", meta.sampler], ["Scheduler", meta.scheduler],
      ["CFG", meta.cfgScale], ["Size", meta.Size || (meta.width && meta.height ? `${meta.width}x${meta.height}` : "")],
      ["Clip skip", meta.clipSkip], ["Denoise", meta.denoise ?? meta["Denoising strength"]], ["Workflow", meta.workflow],
    ].filter(([, v]) => v !== undefined && v !== null && v !== "");
    if (parts.length) lines.push(parts.map(([k, v]) => `${k}: ${v}`).join(" · "));
    return lines.join("\n");
  }

  // CivitAI's answers → the post to save (kept apart from the fetching so it can be tested).
  function toPost({ id, image, generation, post, rest, origin = "https://civitai.com" }) {
    const meta = generation?.meta || rest?.meta || {};
    const uuid = image?.url || (rest?.url || "").split("/")[4] || "";
    const mime = image?.mimeType || (rest?.type === "video" ? "video/mp4" : "image/jpeg");
    const video = (image?.type || rest?.type) === "video" || mime.startsWith("video/");
    const ext = EXT[mime] || (video ? "mp4" : "jpg");
    const url = uuid ? `${API.cdn}/${uuid}/original=true/${uuid}.${ext}` : rest?.url;
    const user = image?.user?.username || rest?.username || "";
    return {
      site: "civitai",
      id: String(id),
      url: `${origin}/images/${id}`,
      author: user,
      caption: [post?.title, post?.detail && post.detail.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim()].filter(Boolean).join("\n\n"),
      details: [meta.prompt && `Prompt: ${meta.prompt}`, settingsText(meta, generation?.resources || [])].filter(Boolean).join("\n"),
      prompt: meta.prompt || "",
      takenAt: image?.createdAt || rest?.createdAt || image?.publishedAt || "",
      media: url ? [{ url, video, ext, key: uuid || url, fallbacks: rest?.url && rest.url !== url ? [rest.url] : [] }] : [],
    };
  }

  const trpc = async (path) => (await P.fetchJson(path))?.result?.data?.json;

  async function collect(idText) {
    const id = Number(idText); // CivitAI's API takes ids as numbers, and refuses "123"
    const [image, generation] = await Promise.all([trpc(API.image(id)).catch(() => null), trpc(API.generation(id)).catch(() => null)]);
    const rest = image ? null : (await P.fetchJson(API.rest(id)).catch(() => null))?.items?.[0];
    if (!image && !rest) throw new Error("CivitAI didn’t give this image’s details. Open its page and try again.");
    const postId = image?.postId || rest?.postId;
    const post = postId ? await trpc(API.post(postId)).catch(() => null) : null;
    return toPost({ id, image, generation, post, rest, origin: location.origin });
  }

  function scan(place) {
    // Cards: each link to an image's page that holds the picture or video.
    for (const link of document.querySelectorAll(SELECTORS.cardLink)) {
      const id = (link.getAttribute("href") || "").match(ID_IN_LINK)?.[1];
      if (!id || !link.querySelector(SELECTORS.cardMedia) || !link.parentElement) continue;
      place({ owner: link, mount: P.overlay(link.parentElement), resolveId: () => id, variant: "card" });
    }
    // An image's own page: beside its download button.
    if (SELECTORS.detailPath.test(location.pathname)) {
      const download = document.querySelector(SELECTORS.detailDownloadIcon)?.closest("button, a");
      if (download?.parentElement) {
        place({
          owner: download.parentElement,
          mount: (slot) => download.after(slot),
          resolveId: () => location.pathname.match(SELECTORS.detailPath)?.[1],
          variant: "toolbar",
        });
      }
    }
  }

  P.start({ site: "civitai", scan, collect, toPost });
})();
