/**
 * Instagram: a button beside Share on every post (the feed, a post's own page or its pop-up, Reels).
 * A save sends every slide of a carousel, each at its largest size, and the video of a Reel or video
 * slide, with the caption, the author and the post's date. The post is read from Instagram's own
 * data: its API when signed in, else the data embedded in the post's page.
 */
(function () {
  "use strict";
  const P = window.PhotosteadSite;
  if (!P) return;

  // Everything that depends on Instagram's markup or API, in one place to fix when it changes.
  const SELECTORS = {
    // The Share button's icon, by its label (English; other languages need their word here).
    shareIcon: 'svg[aria-label="Share"], svg[aria-label="Share Post"]',
    shareButton: '[role="button"], button',
    post: "article",
    // A link to the post itself (its date is one), which carries its shortcode.
    postLink: 'a[href*="/p/"], a[href*="/reel/"], a[href*="/tv/"]',
    // Pages that are about one post: /p/<code>/, /reel/<code>/, /reels/<code>/ (also /<user>/p/<code>/).
    postPath: /\/(?:p|reels?|tv)\/([A-Za-z0-9_-]{5,})/,
    embeddedData: 'script[type="application/json"]',
  };
  const API = {
    appId: "936619743392459", // the web app's id, which Instagram's API asks for
    info: (mediaId) => `/api/v1/media/${mediaId}/info/`,
    page: (code) => `/p/${code}/`,
  };

  // A shortcode is the media id in base 64.
  function mediaId(code) {
    const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let n = 0n;
    for (const c of code.slice(0, 11)) n = n * 64n + BigInt(abc.indexOf(c));
    return n.toString();
  }

  const size = (x) => (x.width || 0) * (x.height || 0);
  function mediaOf(item) {
    const video = P.largest(item.video_versions, size);
    if (video?.url) return { url: video.url, video: true, key: String(item.pk || item.id || video.url) };
    const image = P.largest(item.image_versions2?.candidates, size);
    return image?.url ? { url: image.url, video: false, key: String(item.pk || item.id || image.url) } : null;
  }

  // Instagram's media item → the post to save (kept apart from the fetching so it can be tested).
  function toPost(item, code) {
    const shortcode = item.code || code;
    const reel = item.product_type === "clips";
    const slides = item.carousel_media?.length ? item.carousel_media : [item];
    return {
      site: "instagram",
      id: shortcode,
      url: `https://www.instagram.com/${reel ? "reel" : "p"}/${shortcode}/`,
      author: item.user?.username || item.owner?.username || "",
      authorName: item.user?.full_name || item.owner?.full_name || "",
      caption: item.caption?.text || "",
      takenAt: item.taken_at ? new Date(item.taken_at * 1000).toISOString() : "",
      media: slides.map(mediaOf).filter(Boolean),
    };
  }

  const isItem = (code) => (o) => o.code === code && (o.image_versions2 || o.carousel_media || o.video_versions);
  function fromDocument(doc, code) {
    for (const data of P.scriptJson(doc, SELECTORS.embeddedData)) {
      const item = P.findDeep(data, isItem(code));
      if (item) return item;
    }
    return null;
  }

  async function collect(code) {
    let item = null;
    try { // signed in: the API has it all
      const data = await P.fetchJson(API.info(mediaId(code)), { "X-IG-App-ID": API.appId, "X-Requested-With": "XMLHttpRequest" });
      item = data?.items?.[0] || null;
    } catch (_) { /* signed out, or the API changed: the page has it too */ }
    if (!item?.code || item.code !== code) item = fromDocument(document, code) || fromDocument(await P.fetchDocument(API.page(code)), code);
    if (!item) throw new Error("Instagram didn’t give this post’s details. Open the post and try again.");
    return toPost(item, code);
  }

  const codeIn = (href) => (href || "").match(SELECTORS.postPath)?.[1] || "";
  function resolverFor(button) {
    return () => {
      const post = button.closest(SELECTORS.post);
      if (post) {
        const links = [...post.querySelectorAll(SELECTORS.postLink)];
        const own = links.find((a) => a.querySelector("time")) || links[0];
        const code = codeIn(own?.getAttribute("href"));
        if (code) return code;
      }
      return codeIn(location.pathname); // a post's own page, its pop-up, or the Reel in view
    };
  }

  function scan(place) {
    if (/^\/(stories|direct)\//.test(location.pathname)) return;
    for (const icon of document.querySelectorAll(SELECTORS.shareIcon)) {
      const button = icon.closest(SELECTORS.shareButton);
      if (!button?.parentElement || button.closest(".psc-slot")) continue;
      const resolveId = resolverFor(button);
      if (!button.closest(SELECTORS.post) && !codeIn(location.pathname)) continue; // a Share that isn't a post's
      place({ owner: button, mount: (slot) => button.after(slot), resolveId, variant: "bar" });
    }
  }

  P.start({ site: "instagram", scan, collect, toPost, mediaId });
})();
