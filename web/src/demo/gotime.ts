// Go's time package, as far as the Ask planner (server/internal/ask) uses it: time.Date,
// AddDate, Date, Clock, Weekday, After, Format("2006-01-02") and LoadLocation, with strconv.Atoi.
// Ported with Go's arithmetic, so the demo's dates land where the server's do even at the edges:
// int64 that wraps and uint64 absolute days (so "the last 99999999999999999999 days" comes out as
// the server's does), and Go's way with a local midnight that a clock change skips (time.Date
// moves it, which can make it the day before: Santiago on the first Sunday of September). Zone
// offsets come from the browser's Intl, as the server's come from its tz database; the two can be
// different versions.

/** A time.Time: an instant (Unix seconds, an int64) and its nanoseconds, in a zone ('' is UTC). */
export type GoTime = { unix: bigint; nsec: number; zone: string };

const I64 = (x: bigint) => BigInt.asIntN(64, x);
const U64 = (x: bigint) => BigInt.asUintN(64, x);
const U32 = (x: bigint) => BigInt.asUintN(32, x);

// time.go's constants.
const DAY = 86400n;
const absoluteYears = 292277022400n;
const marchThruDecember = 306n;
const absoluteToInternal = -((absoluteYears * 3652425n) / 10000n + marchThruDecember) * DAY;
const unixToInternal = (1969n * 365n + 1969n / 4n - 1969n / 100n + 1969n / 400n) * DAY;
const absoluteToUnix = absoluteToInternal - unixToInternal;
const unixToAbsolute = unixToInternal - absoluteToInternal;
const maxInt64 = (1n << 63n) - 1n;

// ---- zones ----

// The names Go loads, as the server runs: the tz database's zones, from its container's zoneinfo
// directory, and from Go's own copy (time/tzdata, Go 1.27.3's) the older names the container's
// doesn't have (marked *; they're found by their exact name only). "Area/" starts an area's
// names, and "/" the names without one. Intl takes all of them (but Factory), and others besides:
// ICU's own (PST, SystemV/PST8PDT, US/Pacific-New) and any name in another case.
const tzList = `
Africa/ Abidjan Accra Addis_Ababa Algiers Asmara *Asmera Bamako Bangui Banjul Bissau Blantyre Brazzaville Bujumbura Cairo Casablanca Ceuta
Conakry Dakar Dar_es_Salaam Djibouti Douala El_Aaiun Freetown Gaborone Harare Johannesburg Juba Kampala Khartoum Kigali Kinshasa Lagos
Libreville Lome Luanda Lubumbashi Lusaka Malabo Maputo Maseru Mbabane Mogadishu Monrovia Nairobi Ndjamena Niamey Nouakchott Ouagadougou
Porto-Novo Sao_Tome Timbuktu Tripoli Tunis Windhoek
America/ Adak Anchorage Anguilla Antigua Araguaina Argentina/Buenos_Aires Argentina/Catamarca *Argentina/ComodRivadavia Argentina/Cordoba
Argentina/Jujuy Argentina/La_Rioja Argentina/Mendoza Argentina/Rio_Gallegos Argentina/Salta Argentina/San_Juan Argentina/San_Luis
Argentina/Tucuman Argentina/Ushuaia Aruba Asuncion Atikokan Atka Bahia Bahia_Banderas Barbados Belem Belize Blanc-Sablon Boa_Vista Bogota
Boise *Buenos_Aires Cambridge_Bay Campo_Grande Cancun Caracas *Catamarca Cayenne Cayman Chicago Chihuahua Ciudad_Juarez Coral_Harbour
*Cordoba Costa_Rica Coyhaique Creston Cuiaba Curacao Danmarkshavn Dawson Dawson_Creek Denver Detroit Dominica Edmonton Eirunepe
El_Salvador Ensenada Fort_Nelson *Fort_Wayne Fortaleza Glace_Bay *Godthab Goose_Bay Grand_Turk Grenada Guadeloupe Guatemala Guayaquil
Guyana Halifax Havana Hermosillo Indiana/Indianapolis Indiana/Knox Indiana/Marengo Indiana/Petersburg Indiana/Tell_City Indiana/Vevay
Indiana/Vincennes Indiana/Winamac *Indianapolis Inuvik Iqaluit Jamaica *Jujuy Juneau Kentucky/Louisville Kentucky/Monticello *Knox_IN
Kralendijk La_Paz Lima Los_Angeles *Louisville Lower_Princes Maceio Managua Manaus Marigot Martinique Matamoros Mazatlan *Mendoza
Menominee Merida Metlakatla Mexico_City Miquelon Moncton Monterrey Montevideo Montreal Montserrat Nassau New_York Nipigon Nome Noronha
North_Dakota/Beulah North_Dakota/Center North_Dakota/New_Salem Nuuk Ojinaga Panama Pangnirtung Paramaribo Phoenix Port-au-Prince
Port_of_Spain Porto_Acre Porto_Velho Puerto_Rico Punta_Arenas Rainy_River Rankin_Inlet Recife Regina Resolute Rio_Branco *Rosario
Santa_Isabel Santarem Santiago Santo_Domingo Sao_Paulo Scoresbysund Shiprock Sitka St_Barthelemy St_Johns St_Kitts St_Lucia St_Thomas
St_Vincent Swift_Current Tegucigalpa Thule Thunder_Bay Tijuana Toronto Tortola Vancouver Virgin Whitehorse Winnipeg Yakutat Yellowknife
Antarctica/ Casey Davis DumontDUrville Macquarie Mawson McMurdo Palmer Rothera *South_Pole Syowa Troll Vostok
Arctic/ Longyearbyen
Asia/ Aden Almaty Amman Anadyr Aqtau Aqtobe Ashgabat *Ashkhabad Atyrau Baghdad Bahrain Baku Bangkok Barnaul Beirut Bishkek Brunei
*Calcutta Chita Choibalsan Chongqing *Chungking Colombo *Dacca Damascus Dhaka Dili Dubai Dushanbe Famagusta Gaza Harbin Hebron Ho_Chi_Minh
Hong_Kong Hovd Irkutsk Istanbul Jakarta Jayapura Jerusalem Kabul Kamchatka Karachi Kashgar Kathmandu *Katmandu Khandyga Kolkata
Krasnoyarsk Kuala_Lumpur Kuching Kuwait *Macao Macau Magadan Makassar Manila Muscat Nicosia Novokuznetsk Novosibirsk Omsk Oral Phnom_Penh
Pontianak Pyongyang Qatar Qostanay Qyzylorda *Rangoon Riyadh *Saigon Sakhalin Samarkand Seoul Shanghai Singapore Srednekolymsk Taipei
Tashkent Tbilisi Tehran Tel_Aviv *Thimbu Thimphu Tokyo Tomsk *Ujung_Pandang Ulaanbaatar *Ulan_Bator Urumqi Ust-Nera Vientiane Vladivostok
Yakutsk Yangon Yekaterinburg Yerevan
Atlantic/ Azores Bermuda Canary Cape_Verde *Faeroe Faroe Jan_Mayen Madeira Reykjavik South_Georgia St_Helena Stanley
Australia/ *ACT Adelaide Brisbane Broken_Hill Canberra Currie Darwin Eucla Hobart *LHI Lindeman Lord_Howe Melbourne *NSW *North Perth
*Queensland *South Sydney *Tasmania *Victoria *West Yancowinna
Brazil/ *Acre *DeNoronha *East *West
Canada/ *Atlantic *Central *Eastern *Mountain *Newfoundland *Pacific *Saskatchewan *Yukon
Chile/ *Continental *EasterIsland
Etc/ GMT GMT+0 GMT+1 GMT+10 GMT+11 GMT+12 GMT+2 GMT+3 GMT+4 GMT+5 GMT+6 GMT+7 GMT+8 GMT+9 GMT-0 GMT-1 GMT-10 GMT-11 GMT-12 GMT-13 GMT-14
GMT-2 GMT-3 GMT-4 GMT-5 GMT-6 GMT-7 GMT-8 GMT-9 GMT0 Greenwich UCT UTC Universal Zulu
Europe/ Amsterdam Andorra Astrakhan Athens Belfast Belgrade Berlin Bratislava Brussels Bucharest Budapest Busingen Chisinau Copenhagen
Dublin Gibraltar Guernsey Helsinki Isle_of_Man Istanbul Jersey Kaliningrad *Kiev Kirov Kyiv Lisbon Ljubljana London Luxembourg Madrid
Malta Mariehamn Minsk Monaco Moscow Nicosia Oslo Paris Podgorica Prague Riga Rome Samara San_Marino Sarajevo Saratov Simferopol Skopje
Sofia Stockholm Tallinn Tirane Tiraspol Ulyanovsk *Uzhgorod Vaduz Vatican Vienna Vilnius Volgograd Warsaw Zagreb *Zaporozhye Zurich
Indian/ Antananarivo Chagos Christmas Cocos Comoro Kerguelen Mahe Maldives Mauritius Mayotte Reunion
Mexico/ *BajaNorte *BajaSur *General
Pacific/ Apia Auckland Bougainville Chatham Chuuk Easter Efate *Enderbury Fakaofo Fiji Funafuti Galapagos Gambier Guadalcanal Guam
Honolulu Johnston Kanton Kiritimati Kosrae Kwajalein Majuro Marquesas Midway Nauru Niue Norfolk Noumea Pago_Pago Palau Pitcairn Pohnpei
*Ponape Port_Moresby Rarotonga Saipan Samoa Tahiti Tarawa Tongatapu *Truk Wake Wallis Yap
US/ *Alaska *Aleutian *Arizona *Central *East-Indiana *Eastern *Hawaii *Indiana-Starke *Michigan *Mountain *Pacific *Samoa
/ CET CST6CDT *Cuba EET EST EST5EDT *Egypt *Eire Factory *GB *GB-Eire GMT *GMT+0 *GMT-0 *GMT0 *Greenwich HST *Hongkong *Iceland *Iran
*Israel *Jamaica *Japan *Kwajalein *Libya MET MST MST7MDT *NZ *NZ-CHAT *Navajo *PRC PST8PDT *Poland *Portugal *ROC *ROK *Singapore *Turkey
*UCT UTC *Universal *W-SU WET *Zulu
`;
let tzNames: Map<string, boolean> | undefined; // name -> found by its exact name only
function tzName(name: string): boolean | undefined {
  if (!tzNames) {
    tzNames = new Map();
    let area = '';
    for (const t of tzList.split(/\s+/)) {
      if (t.endsWith('/')) area = t === '/' ? '' : t;
      else if (t !== '') tzNames.set(area + t.replace(/^\*/, ''), t.startsWith('*'));
    }
  }
  return tzNames.get(name);
}

/**
 * time.LoadLocation: the zone's name for Intl, '' for UTC, or null when Go wouldn't load it (the
 * server then uses UTC). Go refuses a name with ".." in it or that starts with a slash, then opens
 * the file the name makes in the zoneinfo directory (so "Europe//Paris" and "./Europe/Paris" are
 * Paris there, and posixrules and localtime are files too), then looks the name up in its own
 * copy. A zone newer than the browser's tz database can't be worked out here: null.
 */
export function loadLocation(name: string): string | null {
  if (name === '' || name === 'UTC') return '';
  if (name === 'Local') return new Intl.DateTimeFormat().resolvedOptions().timeZone; // the server's own zone: here, the browser's
  if (name.includes('..') || name.startsWith('/') || name.startsWith('\\')) return null;
  // The file's path, as the system reads it ("//" and "/./" are "/"); a path that ends in "/" or "." is no file.
  const parts = name.split('/');
  const last = parts[parts.length - 1];
  const file = last === '' || last === '.' ? '' : parts.filter((x) => x !== '' && x !== '.').join('/');
  let zone: string;
  if (file === 'posixrules' || file === 'localtime' || tzName(file) === false) zone = file;
  else if (tzName(name) !== undefined) zone = name;
  else return null;
  if (zone === 'posixrules') return 'America/New_York'; // the tz database's rules for a POSIX TZ string, which are New York's
  if (zone === 'localtime' || zone === 'Factory') return ''; // the server's own clock (its container's is UTC), and the zone at offset 0 that Intl lacks
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
  } catch {
    return null;
  }
  return zone;
}

const formats = new Map<string, Intl.DateTimeFormat>();
const FAR = 8_600_000_000_000n; // seconds: well inside JS Date's range (8.64e12), and long before or after any zone's last change

/** The zone's offset from UTC at an instant, in seconds (Location.lookup). */
function offset(zone: string, unix: bigint): bigint {
  if (zone === '') return 0n;
  let s = unix;
  if (s < -FAR) {
    s = -FAR; // before the zone's first change: its first offset, as Go's lookupFirstZone
  } else if (s > FAR) {
    // After its last change Go follows the zone's rule, which repeats every 400 years: the same
    // place in a 400-year cycle (from Go's absolute days, which wrap), after the year 2400.
    const days = U64(s + unixToAbsolute) / DAY;
    s = (march2400 + (days % 146097n)) * DAY + (s % DAY) + absoluteToUnix;
  }
  let f = formats.get(zone);
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', { timeZone: zone, hourCycle: 'h23', era: 'short', year: 'numeric', month: 'numeric', day: 'numeric',
      hour: 'numeric', minute: 'numeric', second: 'numeric' });
    formats.set(zone, f);
  }
  const ms = Number(s) * 1000;
  const p: Record<string, string> = {};
  for (const x of f.formatToParts(ms)) p[x.type] = x.value;
  const year = p.era === 'BC' || p.era === 'B' ? 1 - Number(p.year) : Number(p.year);
  const local = new Date(0);
  local.setUTCFullYear(year, Number(p.month) - 1, Number(p.day));
  local.setUTCHours(Number(p.hour), Number(p.minute), Number(p.second));
  return BigInt((local.getTime() - ms) / 1000);
}

// ---- dates ----

// norm returns nhi, nlo such that hi * base + lo == nhi * base + nlo and 0 <= nlo < base.
function norm(hi: bigint, lo: bigint, base: bigint): [bigint, bigint] {
  if (lo < 0n) {
    const n = I64(I64(-lo - 1n) / base + 1n);
    hi = I64(hi - n);
    lo = I64(lo + n * base);
  }
  if (lo >= base) {
    const n = lo / base;
    hi = I64(hi + n);
    lo = I64(lo - n * base);
  }
  return [hi, lo];
}

// dateToAbsDays: the days from the absolute epoch to a day (which can be out of range).
function absDays(year: bigint, month: bigint, day: bigint): bigint {
  let amonth = U32(month);
  const janFeb = amonth < 3n ? 1n : 0n;
  amonth = U32(amonth + 12n * janFeb);
  const y = U64(U64(year) - janFeb + absoluteYears);
  const ayday = U32(979n * amonth - 2919n) >> 5n;
  const century = y / 100n;
  const cyear = U32(y % 100n);
  const cday = U32(1461n * cyear) / 4n;
  const centurydays = U64(146097n * century) / 4n;
  return U64(centurydays + U32(cday + ayday) + day - 1n);
}

const march2400 = absDays(2400n, 3n, 1n); // the start of a 400-year cycle

/** time.Date. */
export function goDate(year: bigint, month: bigint, day: bigint, hour: bigint, min: bigint, sec: bigint, nsec: bigint, zone: string): GoTime {
  let m: bigint;
  [year, m] = norm(year, I64(month - 1n), 12n);
  month = I64(m + 1n);
  [sec, nsec] = norm(sec, nsec, 1_000_000_000n);
  [min, sec] = norm(min, sec, 60n);
  [hour, min] = norm(hour, min, 60n);
  [day, hour] = norm(day, hour, 24n);
  let unix = I64(absDays(year, month, day) * DAY + hour * 3600n + min * 60n + sec + absoluteToUnix);
  // The zone's offset for that wall time: looked up at the wall time as if it were UTC, then again
  // at the instant that gives, when that falls under another offset.
  let off = offset(zone, unix);
  if (off !== 0n) {
    const again = offset(zone, I64(unix - off));
    if (again !== off) off = again;
    unix = I64(unix - off);
  }
  return { unix, nsec: Number(nsec), zone };
}

/** The local calendar: Date, Clock and Weekday. */
export function civil(t: GoTime) {
  const abs = U64(t.unix + offset(t.zone, t.unix) + unixToAbsolute);
  const days = abs / DAY;
  const d = U64(4n * days + 3n);
  const century = d / 146097n;
  const cd = U32(d % 146097n) | 3n;
  const p = 2939745n * cd;
  const cyear = p >> 32n;
  const ayday = (p & 0xffffffffn) / 2939745n / 4n;
  const md = U32(2141n * ayday + 197913n);
  const janFeb = ayday >= marchThruDecember ? 1n : 0n;
  const secs = abs % DAY;
  return {
    year: I64(century * 100n - absoluteYears + cyear + janFeb),
    month: (md >> 16n) - janFeb * 12n,
    day: 1n + (md & 0xffffn) / 2141n,
    hour: secs / 3600n,
    min: (secs % 3600n) / 60n,
    sec: secs % 60n,
    weekday: Number((days + 3n) % 7n), // 0 is Sunday
  };
}

/** t.AddDate(years, months, days). */
export function addDate(t: GoTime, years: bigint, months: bigint, days: bigint): GoTime {
  const c = civil(t);
  return goDate(I64(c.year + years), I64(c.month + months), I64(c.day + days), c.hour, c.min, c.sec, BigInt(t.nsec), t.zone);
}

/** t.After(u). */
export function after(t: GoTime, u: GoTime): boolean {
  const ts = I64(t.unix + unixToInternal), us = I64(u.unix + unixToInternal);
  return ts > us || (ts === us && t.nsec > u.nsec);
}

// appendInt: x in at least width digits, after its sign.
const digits = (x: bigint, width: number) => (x < 0n ? '-' + U64(-x).toString().padStart(width, '0') : x.toString().padStart(width, '0'));

/** t.Format("2006-01-02"). */
export function formatDay(t: GoTime): string {
  const c = civil(t);
  return `${digits(c.year, 4)}-${digits(c.month, 2)}-${digits(c.day, 2)}`;
}

/** time.Now().In(loc), for a zone as the server stores it: one Go can't load is UTC. */
export function nowIn(zone: string, at = new Date()): GoTime {
  const ms = BigInt(Math.floor(at.getTime()));
  const unix = ms >= 0n ? ms / 1000n : -((-ms + 999n) / 1000n);
  return { unix, nsec: Number(ms - unix * 1000n) * 1_000_000, zone: loadLocation(zone) ?? '' };
}

/** strconv.Atoi on digits, its error ignored: too big a number is the largest int. */
export const atoi = (s: string): bigint => {
  const n = BigInt(s);
  return n > maxInt64 ? maxInt64 : n;
};

/** time.Parse("2006-01-02", s) succeeds. */
export function isDay(s: string): boolean {
  const m = /^([0-9]{4})-([0-9]{2})-([0-9]{2})$/.exec(s);
  if (!m) return false;
  const y = Number(m[1]), mo = Number(m[2]), d = Number(m[3]);
  const leap = y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0);
  return mo >= 1 && mo <= 12 && d >= 1 && d <= [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][mo - 1];
}
