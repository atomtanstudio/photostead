import { useEffect, useMemo, useState } from 'react';

/** The browser's own time zone, e.g. "America/Los_Angeles". */
export const detectedZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || '';

function allZones(): string[] {
  try {
    const zones = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? [];
    return zones.includes('UTC') ? zones : [...zones, 'UTC'];
  } catch {
    return [detectedZone(), 'UTC'].filter(Boolean);
  }
}

const pretty = (z: string) => z.replace(/_/g, ' ').replace(/\//g, ' / ');

/** A time zone menu grouped by region, with the current time there as a check. */
export function TimeZonePicker({ value, onChange, id }: { value: string; onChange: (z: string) => void; id?: string }) {
  const zones = useMemo(allZones, []);
  const groups = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const z of zones) {
      const region = z.includes('/') ? z.split('/')[0] : 'Other';
      m.set(region, [...(m.get(region) ?? []), z]);
    }
    return [...m];
  }, [zones]);
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 30000);
    return () => clearInterval(t);
  }, []);
  let clock = '';
  try {
    clock = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit', timeZone: value, timeZoneName: 'short' }).format(now);
  } catch {
    clock = '';
  }
  const detected = detectedZone();
  return (
    <div className="tz-picker">
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        {detected && <option value={detected}>{pretty(detected)} (this device)</option>}
        {groups.map(([region, list]) => (
          <optgroup key={region} label={region}>
            {list.map((z) => (
              <option key={z} value={z}>
                {pretty(z)}
              </option>
            ))}
          </optgroup>
        ))}
      </select>
      {clock && <p className="tz-clock">It’s {clock} there now.</p>}
    </div>
  );
}
