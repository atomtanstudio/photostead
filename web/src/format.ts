const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const MON = MONTHS.map((m) => m.slice(0, 3));
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export const monthName = (m: number) => MONTHS[m - 1];

/** "Fri 16 Oct 2026" for a YYYY-MM-DD day. */
export function formatDay(day: string): string {
  const [y, m, d] = day.split('-').map(Number);
  const wd = new Date(Date.UTC(y, m - 1, d)).getUTCDay();
  return `${WEEKDAYS[wd]} ${d} ${MON[m - 1]} ${y}`;
}

/** "May 2024", "May – Aug 2024" or "Dec 2023 – Jan 2024" for two YYYY-MM-DD days. */
export function formatSpan(first?: string, last?: string): string {
  if (!first || !last) return '';
  const [y1, m1] = first.split('-').map(Number);
  const [y2, m2] = last.split('-').map(Number);
  if (y1 === y2 && m1 === m2) return `${MON[m1 - 1]} ${y1}`;
  if (y1 === y2) return `${MON[m1 - 1]} – ${MON[m2 - 1]} ${y1}`;
  return `${MON[m1 - 1]} ${y1} – ${MON[m2 - 1]} ${y2}`;
}

/** "0:15", "12:04", "1:02:33". */
export function formatDuration(ms?: number): string {
  if (!ms) return '';
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/**
 * The capture time. With a recorded offset it's the clock where it was taken ("23:30 · UTC+9");
 * otherwise it's shown in the person's chosen time zone ("17:09 · PDT"). Never plain UTC.
 */
export function formatTime(takenAt: string, offsetMin: number | undefined, zone: string): string {
  const t = new Date(takenAt);
  if (offsetMin === undefined) {
    const time = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', timeZone: zone }).format(t);
    const tz = new Intl.DateTimeFormat(undefined, { timeZone: zone, timeZoneName: 'short' }).formatToParts(t).find((p) => p.type === 'timeZoneName')?.value ?? zone;
    return `${time} · ${tz}`;
  }
  const local = new Date(t.getTime() + offsetMin * 60000);
  const hh = String(local.getUTCHours()).padStart(2, '0');
  const mm = String(local.getUTCMinutes()).padStart(2, '0');
  const sign = offsetMin < 0 ? '−' : '+';
  const oh = Math.floor(Math.abs(offsetMin) / 60);
  const om = Math.abs(offsetMin) % 60;
  return `${hh}:${mm} · UTC${sign}${oh}${om ? `:${String(om).padStart(2, '0')}` : ''} where it was taken`;
}

/** When an item was taken as its clock read (YYYY-MM-DDTHH:MM), for a date-and-time field. */
export function wallTime(takenAt: string, offsetMin: number | undefined, zone: string): string {
  const t = new Date(takenAt);
  if (offsetMin !== undefined) return new Date(t.getTime() + offsetMin * 60000).toISOString().slice(0, 16);
  const p = Object.fromEntries(
    new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
      .formatToParts(t)
      .map((x) => [x.type, x.value]),
  );
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

const DATE_SOURCES: Record<string, string> = {
  'exif-original': 'Camera capture time (EXIF)',
  'exif-digitized': 'Camera digitized time (EXIF)',
  'xmp-original': 'Capture time (XMP)',
  'xmp-created': 'Date created (XMP)',
  'xmp-create': 'Create date (XMP)',
  'apple-creation': 'Capture time (Apple)',
  iptc: 'Date created (IPTC)',
  matroska: 'Recording date (video)',
  quicktime: 'Recording date (video, UTC)',
  png: 'Creation time (PNG)',
  name: 'Date in the file or folder name (no capture date in the file)',
  'file-modified': 'File date (no capture date in the file)',
  user: 'Set by you',
};

export const dateSourceLabel = (s: string) => DATE_SOURCES[s] ?? s;
