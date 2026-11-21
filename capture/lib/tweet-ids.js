/**
 * Distinguish tweet status snowflake IDs from shorter user/account IDs in API payloads.
 */

export function isTweetStatusId(id) {
  const s = String(id || "").trim();
  if (!/^\d+$/.test(s)) return false;
  // Status IDs are snowflakes (typically 18–19 digits). User IDs are often much shorter.
  return s.length >= 15;
}

export function filterTweetStatusIds(ids) {
  const out = [];
  const seen = new Set();
  for (const id of ids || []) {
    const s = String(id);
    if (!isTweetStatusId(s) || seen.has(s)) continue;
    seen.add(s);
    out.push(s);
  }
  return out;
}
