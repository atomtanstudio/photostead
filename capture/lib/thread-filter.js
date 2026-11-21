/**
 * Stops thread extraction at X's "Discover more" boundary and drops unrelated modules.
 */

function entryText(entry) {
  try {
    return JSON.stringify(entry).toLowerCase();
  } catch (_) {
    return "";
  }
}

export function isDiscoverMoreBoundary(entry) {
  if (!entry) return false;
  const id = (entry.entryId || "").toLowerCase();
  const text = entryText(entry);

  if (/discover\s*more/i.test(text)) return true;

  const cursor = entry.content?.timelineCursor || entry.content?.cursor;
  if (cursor) {
    const label = (
      cursor.displayText ||
      cursor.label ||
      cursor.text ||
      cursor.cursorType ||
      ""
    ).toString();
    if (/discover\s*more/i.test(label)) return true;
    if (/show\s*more\s*repl|more\s*repl/i.test(label)) return false;
  }

  if (id.includes("who-to-follow") || id.includes("who_to_follow")) return true;
  if (id.includes("promoted") || id.includes("suggested")) return true;
  if (id.includes("profile-conversation") && !id.includes("conversationthread")) return true;

  return false;
}

export function shouldStopInstruction(instruction) {
  if (!instruction) return false;
  const type = instruction.type || instruction.__typename || "";
  const blob = JSON.stringify(instruction).toLowerCase();
  if (/discover\s*more/i.test(blob)) return true;
  if (type === "TimelineTerminateTimeline") {
    const dir = (instruction.direction || instruction.displayType || "").toString();
    if (/discover|bottom/i.test(dir) && /discover/i.test(blob)) return true;
  }
  return false;
}

export function isConversationEntry(entry) {
  const id = entry?.entryId || "";
  if (isDiscoverMoreBoundary(entry)) return false;
  if (id.includes("who-to-follow") || id.includes("promoted")) return false;
  if (entry?.content?.__typename === "TimelineTimelineCursor") {
    const label = entryText(entry);
    if (/discover\s*more/i.test(label)) return false;
    if (!/show more|replies|conversation/i.test(label) && id.includes("cursor")) {
      return false;
    }
  }
  return (
    id.includes("tweet-") ||
    id.includes("conversationthread") ||
    entry?.content?.itemContent?.tweet_results ||
    entry?.content?.items
  );
}

/**
 * Returns true if this instruction begins the unrelated "Discover more" feed.
 */
export function instructionIsDiscoverMoreSection(instruction) {
  if (shouldStopInstruction(instruction)) return true;
  for (const entry of instruction?.entries || []) {
    if (isDiscoverMoreBoundary(entry)) return true;
  }
  return false;
}


export function responseHasDiscoverMoreTermination(data) {
  const instructions =
    data?.data?.threaded_conversation_with_injections_v2?.instructions || [];
  for (const instruction of instructions) {
    if (instructionIsDiscoverMoreSection(instruction)) return true;
  }
  return false;
}

export function extractConversationReplyCursor(data) {
  const instructions =
    data?.data?.threaded_conversation_with_injections_v2?.instructions || [];
  let best = null;
  for (const instruction of instructions) {
    if (instructionIsDiscoverMoreSection(instruction)) continue;
    for (const entry of instruction.entries || []) {
      if (isDiscoverMoreBoundary(entry)) continue;
      const cursor = entry?.content?.timelineCursor || entry?.content?.cursor;
      if (!cursor?.value) continue;
      const label = (cursor.displayText || cursor.label || cursor.text || "").toString();
      if (/discover\s*more/i.test(label)) continue;
      const id = String(entry.entryId || "").toLowerCase();
      if (id.includes("cursor") || /show\s*more|repl/i.test(label)) best = cursor.value;
    }
  }
  return best;
}
