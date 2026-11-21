/**
 * Pinterest: a button on every pin in a grid (home, search, boards) and beside Share on a pin's own
 * page. A save sends the pin's largest image (its original), or its video (an MP4, or its HLS stream
 * joined into one), or every page of an idea pin, with the title, description, the pinner and the
 * date, read from Pinterest's own API.
 */
(function () {
  "use strict";
  const P = window.PhotosteadSite;
  if (!P) return;

  // Everything that depends on Pinterest's markup or API, in one place to fix when it changes.
  const SELECTORS = {
    // A pin in a grid: its link to the pin's page, around the picture.
    gridPin: '[data-test-id="pin"], [data-grid-item="true"]',
    pinLink: 'a[href*="/pin/"]',
    pinMedia: "img, video",
    // A pin's own page: its action bar and the Share button in it.
    actionBar: '[data-test-id="pin-action-bar-container"], [data-test-id="closeup-action-items"]',
    shareButton: '[data-test-id="share-button"], [data-test-id="closeup-share-button"]',
    // /pin/<id>/ (sometimes /pin/<title>--<id>/).
    pinPath: /\/pin\/(?:[^/]*--)?(\d{6,})/,
  };
  const API = {
    pin: (id) => `/resource/PinResource/get/?source_url=${encodeURIComponent(`/pin/${id}/`)}&data=${encodeURIComponent(JSON.stringify({ options: { id, field_set_key: "detailed" }, context: {} }))}`,
    headers: { "X-Requested-With": "XMLHttpRequest", "X-Pinterest-PWS-Handler": "www/pin/[id].js" },
  };

  const width = (x) => x?.width || 0;
  // A picture's addresses, best first: the original, then the original under its other file types
  // (an idea pin's page names its original .jpg when Pinterest keeps it as .webp, and the .jpg
  // answers 403), then the widest copy Pinterest made of it.
  const ORIGINAL = /\/originals\/(.+)\.(jpe?g|png|webp|gif)(\?.*)?$/i;
  function imageSources(images = {}) {
    const originals = [images.orig?.url, images.originals?.url].filter(Boolean);
    const sized = Object.values(images || {}).filter((v) => v?.url && !ORIGINAL.test(v.url));
    const variants = originals.flatMap((url) => {
      const m = url.match(ORIGINAL);
      return m ? ["webp", "jpg", "png", "gif"].map((ext) => url.replace(ORIGINAL, `/originals/${m[1]}.${ext}`)) : [];
    });
    return [...new Set([...originals, ...variants, P.largest(sized, width)?.url].filter(Boolean))];
  }
  // A video's addresses, best first: its MP4s (the full 720p copy, then the larger "expMp4" tiers),
  // else the 720p MP4 older videos have beside their stream, then the HLS stream itself, which is all
  // Pinterest keeps of most videos uploaded lately (TikToks pinned among them): the service worker
  // joins its picture and sound into one MP4 (lib/hls.js).
  const tier = (url) => Number(url.match(/_t(\d+)\.mp4/)?.[1] || 0);
  function videoSources(list = {}) {
    const entries = Object.values(list || {}).filter((v) => typeof v?.url === "string");
    const mp4 = entries.filter((v) => /\.mp4(\?|$)/.test(v.url))
      .sort((a, b) => /\/720p\//.test(b.url) - /\/720p\//.test(a.url) || tier(b.url) - tier(a.url) || width(b) - width(a))
      .map((v) => v.url).slice(0, 3); // the small tiers would be worse than the stream
    const streams = entries.map((v) => v.url).filter((url) => /\.m3u8(\?|$)/.test(url));
    const derived = streams.map((url) => url.replace("/hls/", "/720p/").replace(/\.m3u8(\?.*)?$/, ".mp4")).filter((url) => /\/720p\//.test(url));
    return [...new Set([...mp4, ...(mp4.length ? [] : derived), ...streams.slice(0, 1)])];
  }
  // A still for a video block with no video Pinterest gives (its first frame).
  const thumbnail = (list = {}) => Object.values(list || {}).find((v) => v?.thumbnail)?.thumbnail || "";

  // Pinterest's pin → the post to save (kept apart from the fetching so it can be tested).
  function toPost(pin) {
    const media = [];
    const add = (url, video, key) => url && media.push({ url, video, key });
    const addImage = (images, key) => {
      const [url, ...fallbacks] = imageSources(images);
      if (url) media.push({ url, fallbacks, video: false, key });
      return Boolean(url);
    };
    const addVideo = (list, key) => {
      const [url, ...fallbacks] = videoSources(list);
      if (url) media.push({ url, fallbacks, video: true, ext: "mp4", key });
      return Boolean(url);
    };
    const pages = pin.story_pin_data?.pages || [];
    const slots = pin.carousel_data?.carousel_slots || [];
    if (pages.length) {
      pages.forEach((page, p) => (page.blocks || []).forEach((block, b) => {
        const key = `${pin.id}-${p}-${b}`;
        if (!addVideo(block.video?.video_list, key) && !addImage(block.image?.images, key)) add(thumbnail(block.video?.video_list), false, key);
      }));
      if (!media.length) addImage(pin.images, String(pin.id)); // a page we can't read: the pin's picture
    } else if (slots.length) {
      slots.forEach((slot, i) => addImage(slot.images, `${pin.id}-${i}`));
    } else if (!addVideo(pin.videos?.video_list, String(pin.id))) {
      addImage(pin.images, String(pin.id));
    }
    const title = (pin.title || pin.grid_title || "").trim();
    const description = (pin.description || pin.closeup_unified_description || "").trim();
    const user = pin.pinner?.username ? pin.pinner : pin.origin_pinner || {}; // whose pin it is
    const outbound = pin.link && /^https?:\/\//.test(pin.link) ? `From ${pin.link}` : "";
    return {
      site: "pinterest",
      id: String(pin.id),
      url: `https://www.pinterest.com/pin/${pin.id}/`,
      author: user.username || "",
      authorName: user.full_name || "",
      caption: [title, description !== title && description].filter(Boolean).join("\n\n"),
      details: outbound.length < 300 ? outbound : "",
      takenAt: pin.created_at || "",
      media,
    };
  }

  async function collect(id) {
    const pin = (await P.fetchJson(API.pin(id), API.headers))?.resource_response?.data;
    if (!pin?.id) throw new Error("Pinterest didn’t give this pin’s details. Open the pin and try again.");
    return toPost(pin);
  }

  const idIn = (href) => (href || "").match(SELECTORS.pinPath)?.[1] || "";

  function scan(place) {
    // Grids: the pin's link, with the picture in it.
    for (const pin of document.querySelectorAll(SELECTORS.gridPin)) {
      const link = pin.querySelector(SELECTORS.pinLink);
      const id = idIn(link?.getAttribute("href"));
      if (!id || !link.querySelector(SELECTORS.pinMedia) || !link.parentElement) continue;
      place({ owner: pin, mount: P.overlay(link.parentElement), resolveId: () => id, variant: "card" });
    }
    // A pin's own page: beside Share.
    if (SELECTORS.pinPath.test(location.pathname)) {
      for (const bar of document.querySelectorAll(SELECTORS.actionBar)) {
        const share = bar.querySelector(SELECTORS.shareButton);
        let entry = share;
        while (entry && entry.parentElement && entry.parentElement !== bar && entry.parentElement.children.length < 2) entry = entry.parentElement;
        const mount = entry ? (slot) => entry.after(slot) : (slot) => bar.appendChild(slot);
        place({ owner: bar, mount, resolveId: () => idIn(location.pathname), variant: "bar" });
      }
    }
  }

  P.start({ site: "pinterest", scan, collect, toPost });
})();
