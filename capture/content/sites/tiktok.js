/**
 * TikTok: a button under Share in each video's action column (For You, Following, a video's own
 * page). A save sends the video at its highest resolution (or every picture of a photo post), with
 * the caption, the author and the date, read from the data TikTok embeds in the video's page.
 */
(function () {
  "use strict";
  const P = window.PhotosteadSite;
  if (!P) return;

  // Everything that depends on TikTok's markup, in one place to fix when it changes.
  const SELECTORS = {
    // The Share button in a video's action column (the feed's, or a video page's browse view).
    shareButton: '[data-e2e="share-icon"], [data-e2e="browse-share-icon"], [data-e2e="video-share-icon"]',
    // One video in a feed, and the player inside it, whose id ends with the video's id.
    feedItem: 'article[data-e2e="recommend-list-item-container"], [data-e2e="recommend-list-item-container"]',
    player: '[id^="xgwrapper-"]',
    videoLink: 'a[href*="/video/"], a[href*="/photo/"]',
    authorLink: 'a[href^="/@"]',
    // A video's own page: /@user/video/<id> or /@user/photo/<id>.
    postPath: /\/(?:video|photo)\/(\d{8,})/,
    // The data the page carries, and where the video is in it.
    embeddedData: "script#__UNIVERSAL_DATA_FOR_REHYDRATION__",
    itemPath: (data) => data?.__DEFAULT_SCOPE__?.["webapp.video-detail"]?.itemInfo?.itemStruct,
  };
  const PAGE = (id) => `/@_/video/${id}`; // TikTok finds the video by its id, whoever posted it

  // The best copy of the video: the largest picture, then H.264 (plays everywhere), then bitrate.
  function bestVideo(video = {}) {
    const variants = (video.bitrateInfo || []).map((b) => ({
      urls: b.PlayAddr?.UrlList || [],
      pixels: (b.PlayAddr?.Width || 0) * (b.PlayAddr?.Height || 0),
      h264: /h264|avc/i.test(b.CodecType || "") ? 1 : 0,
      bitrate: b.Bitrate || 0,
    })).filter((v) => v.urls.length);
    variants.sort((a, b) => b.pixels - a.pixels || b.h264 - a.h264 || b.bitrate - a.bitrate);
    if (variants[0]) return variants[0].urls;
    return [video.playAddr, video.downloadAddr].filter(Boolean);
  }

  // TikTok's item → the post to save (kept apart from the fetching so it can be tested).
  function toPost(item) {
    const author = (typeof item.author === "string" ? item.author : item.author?.uniqueId) || "";
    const images = item.imagePost?.images || [];
    const media = images.length
      ? images.map((image, i) => {
          const urls = image.imageURL?.urlList || [];
          return urls[0] ? { url: urls[0], fallbacks: urls.slice(1), video: false, key: `${item.id}-${i}` } : null;
        }).filter(Boolean)
      : (() => {
          const urls = bestVideo(item.video);
          return urls[0] ? [{ url: urls[0], fallbacks: urls.slice(1), video: true, ext: "mp4", key: String(item.id) }] : [];
        })();
    return {
      site: "tiktok",
      id: String(item.id),
      url: `https://www.tiktok.com/@${author || "_"}/${images.length ? "photo" : "video"}/${item.id}`,
      author,
      authorName: item.author?.nickname || "",
      caption: item.desc || "",
      takenAt: item.createTime ? new Date(Number(item.createTime) * 1000).toISOString() : "",
      media,
    };
  }

  function fromDocument(doc, id) {
    for (const data of P.scriptJson(doc, SELECTORS.embeddedData)) {
      const item = SELECTORS.itemPath(data);
      if (item && String(item.id) === String(id)) return item;
    }
    return null;
  }

  async function collect(id) {
    const item = fromDocument(document, id) || fromDocument(await P.fetchDocument(PAGE(id)), id);
    if (!item) throw new Error("TikTok didn’t give this video’s details. Open the video and try again.");
    return toPost(item);
  }

  function resolverFor(share) {
    return () => {
      const item = share.closest(SELECTORS.feedItem);
      if (item) {
        const player = item.querySelector(SELECTORS.player);
        const fromPlayer = player?.id.match(/(\d{8,})$/)?.[1];
        if (fromPlayer) return fromPlayer;
        const fromLink = item.querySelector(SELECTORS.videoLink)?.getAttribute("href")?.match(SELECTORS.postPath)?.[1];
        if (fromLink) return fromLink;
        // The first video of a video's own page (its player not yet made): the address says.
        const first = document.querySelector(SELECTORS.feedItem) === item;
        return first ? location.pathname.match(SELECTORS.postPath)?.[1] || "" : "";
      }
      return location.pathname.match(SELECTORS.postPath)?.[1] || "";
    };
  }

  function scan(place) {
    for (const share of document.querySelectorAll(SELECTORS.shareButton)) {
      // The share button's own place in the column: the child of the column that holds it.
      let entry = share;
      for (let up = 0; up < 4 && entry.parentElement && entry.parentElement.children.length < 3 && entry.parentElement !== document.body; up++) entry = entry.parentElement;
      if (!entry.parentElement || entry.closest(".psc-slot")) continue;
      place({ owner: entry, mount: (slot) => entry.after(slot), resolveId: resolverFor(share), variant: "column" });
    }
  }

  // TikTok's video servers turn away requests that aren't from its pages: the page downloads them.
  // (Its picture servers are the other way round: those the service worker downloads.)
  P.start({ site: "tiktok", scan, collect, toPost, pageDownload: true });
})();
