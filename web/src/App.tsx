import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, type AdultMode, type HealthCheck, type AskPlan, type AskResult, type Bucket, type Album, type Character, type Counts, type Folder, type Library, type MarkChange, type MarkFilter, type Marks, type Person, type SmartRules, type MeaningStatus, type SearchMode, type ServerStatus, type TimelineAsset, type User, type VaultState, type Memory, type Household, type Whose } from './api';
import { CharacterDialog, CharactersPage } from './Characters';
import { AlbumCard, AlbumsPage } from './Collections';
import { MoveDialog, NewAlbumDialog, RenameDialog, SmartDialog } from './AlbumDialogs';
import { FolderList, FoldersPage } from './Folders';
import { PlacesPage } from './Places';
import { LookAlikesPage } from './Stacks';
import { PinDialog, VaultLock } from './Vault';
import { MemoriesDialog, MemoryStrip } from './Memories';
import { Login, SecurityDialog } from './SignIn';
import { BackupsDialog } from './Backups';
import { DevicesDialog } from './Devices';
import { AcceptInvite, HouseholdDialog } from './Household';
import { GuestApp } from './Guest';
import { AlbumUpload, ShareLinksDialog } from './ShareLinks';
import { AsCharacterDialog, NameDialog, PeoplePage, personKind, personName } from './People';
import { applyMarks, describeChange, describeFilter, filterActive, markKey, undoChange } from './marks';
import { EditDialog } from './EditDialog';
import { wallTime } from './format';
import { ModelsPage, PromptsPage } from './CreatePages';
import { Copy } from './Generation';
import { CommandBar, SelectionBar } from './CommandBar';
import { formatSpan } from './format';
import { Icon } from './icons';
import { ImportDialog, ImportGuide } from './ImportDialog';
import { SetupCard, stepsLeft, useSetup, type SetupStepId } from './Setup';
import { HealthDialog, needingLook } from './Health';
import { Uploads, pickFiles } from './Uploads';
import { AlbumTalk } from './Talk';
import { Rail, THEMES, type Theme } from './Rail';
import { Scrubber } from './Scrubber';
import { Timeline, ZOOMS, type TimelineHandle } from './timeline/Timeline';
import { TimelineModel } from './timeline/model';
import { Viewer } from './Viewer';
import { TimeZonePicker, detectedZone } from './TimeZonePicker';
import { AlbumPicker } from './AlbumPicker';
import { ADULT_MODES, labelInfo } from './utility';
import { parseHash, viewHash, viewScope, viewType, type View } from './views';

export function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [setup, setSetup] = useState<{ needed: boolean; serverZone: string; signOn?: string; internet?: boolean } | null>(null);
  useEffect(() => {
    api.setupStatus().then(setSetup).catch(() => setSetup({ needed: false, serverZone: '' }));
    api.me().then(setUser).catch(() => setUser(null));
  }, []);
  // The same function every time: Library's loaders depend on it, and a new one on each change to
  // the user (a setting) would load the whole timeline again.
  const signedOut = useCallback(() => setUser(null), []);
  if (user === undefined || setup === null) return <div className="boot" aria-busy="true" />;
  if (!user && setup.needed) return <Setup serverZone={setup.serverZone} onDone={setUser} />;
  const invite = window.location.hash.match(/^#\/invite\/([A-Za-z0-9_-]+)$/);
  if (invite) return <AcceptInvite token={invite[1]} user={user} mark={<Mark />} onDone={setUser} />;
  if (!user) return <Login signOn={setup.signOn} internet={setup.internet} onSignedIn={setUser} mark={<Mark />} />;
  if (user.role === 'guest') return <GuestApp user={user} onSignedOut={signedOut} />;
  if (!user.timeZoneChosen) return <TimeZoneStep user={user} onDone={setUser} />;
  return <Library user={user} onUser={setUser} onSignedOut={signedOut} />;
}

/** First run: the server has no accounts yet. Creates the admin and asks for their time zone. */
function Setup({ serverZone, onDone }: { serverZone: string; onDone: (u: User) => void }) {
  const [zone, setZone] = useState(() => detectedZone() || serverZone);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    if (f.get('password') !== f.get('confirm')) {
      setError('The two passwords don’t match.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      onDone(await api.setup({ displayName: String(f.get('name')), email: String(f.get('email')), password: String(f.get('password')), timeZone: zone }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t reach the server. Try again.');
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login">
      <form onSubmit={submit} className="setup">
        <div className="brand">
          <Mark /> <span>Photostead</span>
        </div>
        <h1>Welcome. Let’s set up your library.</h1>
        <p className="lede">This creates the administrator account for this server.</p>
        <label>
          Your name
          <input name="name" autoComplete="name" required autoFocus />
        </label>
        <label>
          Email
          <input name="email" type="email" autoComplete="username" required />
        </label>
        <label>
          Password <span className="hint">at least 12 characters</span>
          <input name="password" type="password" autoComplete="new-password" minLength={12} required />
        </label>
        <label>
          Confirm password
          <input name="confirm" type="password" autoComplete="new-password" minLength={12} required />
        </label>
        <label htmlFor="tz">
          Time zone <span className="hint">dates and days in your library follow it</span>
        </label>
        <TimeZonePicker id="tz" value={zone} onChange={setZone} />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" disabled={busy}>
          {busy ? 'Creating…' : 'Create account'}
        </button>
      </form>
    </main>
  );
}

/** Onboarding for accounts that haven't picked a time zone yet. */
function TimeZoneStep({ user, onDone }: { user: User; onDone: (u: User) => void }) {
  const [zone, setZone] = useState(() => detectedZone() || user.timeZone);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      onDone((await api.setTimeZone(zone)).user);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.');
      setBusy(false);
    }
  };
  return (
    <main className="login">
      <form onSubmit={save} className="setup">
        <div className="brand">
          <Mark /> <span>Photostead</span>
        </div>
        <h1>Which time zone should your library use?</h1>
        <p className="lede">
          Photos that record where they were taken keep that local time. Everything else, like downloads and most videos, is dated in this zone. You can change it later.
        </p>
        <TimeZonePicker value={zone} onChange={setZone} />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Continue'}
        </button>
      </form>
    </main>
  );
}

const pref = (k: string) => {
  try {
    return localStorage.getItem(k) ?? '';
  } catch {
    return '';
  }
};
const setPref = (k: string, v: string) => {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* storage unavailable */
  }
};
const isPhone = () => window.matchMedia('(max-width: 760px)').matches;

// The heading over a collection's timeline. A prompt's heading shows the prompt itself (body).
type Heading = {
  kicker: string;
  title: string;
  note?: string;
  body?: string;
  back?: View;
  backLabel?: string;
  character?: Character;
  trash?: boolean;
  vault?: 'open' | 'deleted'; // the vault, or its trash
  person?: Person;
  crumbs?: { label: string; view: View }[]; // where it is (albums inside albums, folders)
  album?: Album;
  subAlbums?: Album[];
  folder?: { library: Library; path: string; folders: Folder[] };
  adult?: 'adult' | 'suggestive'; // the Adult page, and which of its two lists
};

type AlbumDialog =
  | { kind: 'new'; parent?: Album }
  | { kind: 'smart'; album?: Album; start?: SmartRules; parent?: string }
  | { kind: 'rename'; album: Album }
  | { kind: 'move'; album: Album };

const items = (n: number) => `${n.toLocaleString()} ${n === 1 ? 'item' : 'items'}`;

// A search that reads like a question, offered to Ask.
const QUESTION = /^(how|when|what|which|who|where|show|find|did|do|have|list)\b|\?$|\b(from|since|before|in) (19|20)\d\d\b|\b(this|last) (week|month|year)\b/i;

/** The signed-in app: rail, timeline (or search results), scrubber, command bar and viewer. */
function Library({ user, onUser, onSignedOut }: { user: User; onUser: (u: User) => void; onSignedOut: () => void }) {
  const [view, setView] = useState<View>(() => parseHash(window.location.hash));
  const filter = viewType(view);
  const [search, setSearch] = useState(''); // what's typed in the command bar
  const [searchMode, setSearchMode] = useState<SearchMode>('auto');
  const [askMode, setAskMode] = useState(false); // the command bar asks questions instead of searching
  const [asking, setAsking] = useState<{ q: string; prev?: AskPlan; exclude?: string[] } | null>(null);
  const [answer, setAnswer] = useState<(AskResult & { q: string }) | null>(null);
  const [similar, setSimilar] = useState<TimelineAsset | null>(null); // "More like this"
  const [model, setModel] = useState<TimelineModel | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [results, setResults] = useState<{
    q: string;
    count: number;
    truncated: boolean;
    mode: 'meaning' | 'names' | 'similar' | 'ask';
    meaning: MeaningStatus;
    found?: { meaning: number; prompts: number; text: number; details?: number; names: number };
  } | null>(null);
  const [heading, setHeading] = useState<Heading | null>(null);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [characterEdit, setCharacterEdit] = useState<{ character?: Character } | null>(null);
  const [immichConnected, setImmichConnected] = useState(false);
  const [newer, setNewer] = useState(0);
  const [error, setError] = useState('');
  const [refused, setRefused] = useState(false); // the error is the server saying no, with why: trying again won't help
  const [zoom, setZoom] = useState(() => {
    const z = Number(pref('ps.zoom'));
    return Number.isInteger(z) && pref('ps.zoom') !== '' && z >= 0 && z < ZOOMS.length ? z : 3;
  });
  const [open, setOpen] = useState<TimelineAsset | null>(null);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [collapsed, setCollapsed] = useState(() => pref('ps.rail') === 'collapsed');
  const [scrubHidden, setScrubHidden] = useState(() => pref('ps.scrub') === 'hidden');
  const [hideUtility, setHideUtility] = useState(() => pref('ps.utility') === 'hidden');
  const organize = Boolean(user.settings?.organize);
  const adultMode: AdultMode = user.settings?.adult ?? 'show';
  const [markFilter, setMarkFilter] = useState<MarkFilter>({});
  const filtering = organize && filterActive(markFilter);
  const markedWhileOpen = useRef(false); // marks changed in the viewer while a filter is on: reload on close
  const [editFor, setEditFor] = useState<{ ids: string[]; takenAt?: string; description?: string } | null>(null);
  const [edited, setEdited] = useState(0); // bumps when the open item was edited, so the viewer reads it again
  const [looseModel, setLooseModel] = useState<TimelineModel | null>(null); // the viewer's list off the timeline (Look-alikes)
  const [drawer, setDrawer] = useState(false);
  const [theme, setTheme] = useState<Theme>(() => (THEMES.includes(pref('ps.theme') as Theme) ? (pref('ps.theme') as Theme) : 'system'));
  const [systemDark, setSystemDark] = useState(() => matchMedia('(prefers-color-scheme: dark)').matches);
  useEffect(() => {
    const q = matchMedia('(prefers-color-scheme: dark)');
    const on = () => setSystemDark(q.matches);
    q.addEventListener('change', on);
    return () => q.removeEventListener('change', on);
  }, []);
  const dark = theme === 'dark' || (theme === 'system' && systemDark);
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [visible, setVisible] = useState<ReadonlySet<string>>(new Set());
  const [zoneOpen, setZoneOpen] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [toast, setToast] = useState<{ text: string; undo?: () => void } | null>(null);
  // The vault as this session sees it (while the vault is shown), and a PIN being set or changed.
  const [vault, setVault] = useState<VaultState | null>(null);
  const [pinDialog, setPinDialog] = useState<{ mode: 'set' | 'change'; then?: () => void } | null>(null);
  // Today's memories (for the strip above the timeline), and their settings dialog.
  const [memories, setMemories] = useState<Memory[]>([]);
  const [memoriesOpen, setMemoriesOpen] = useState(false);
  const [securityOpen, setSecurityOpen] = useState(false);
  const [backupsOpen, setBackupsOpen] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const [talkOpen, setTalkOpen] = useState(false);
  const [devicesOpen, setDevicesOpen] = useState(false);
  // The household (for the timeline's switch and sharing), and whose items the timeline shows.
  const [household, setHousehold] = useState<Household | null>(null);
  const [householdOpen, setHouseholdOpen] = useState(false);
  const [linksFor, setLinksFor] = useState<Album | null>(null);
  const [whose, setWhose] = useState<Whose>(() => (pref('ps.whose') as Whose) || '');
  // The setup checklist (the admin's): looked at again when a dialog that can finish a step closes.
  const [setupAgain, setSetupAgain] = useState(0);
  const setupSteps = useSetup(user, counts, status, setupAgain);
  // Health (the admin's): looked at when the library opens, every five minutes, and after a dialog
  // that can mend something closes.
  const [health, setHealth] = useState<HealthCheck[] | null>(null);
  const [healthOpen, setHealthOpen] = useState(false);
  const admin = user.role === 'admin';
  const checkHealth = useCallback(() => (admin ? api.health().then((r) => setHealth(r.checks)) : Promise.resolve()), [admin]);
  useEffect(() => {
    if (!admin) return;
    void checkHealth().catch(() => {});
    const t = setInterval(() => void checkHealth().catch(() => {}), 5 * 60_000);
    return () => clearInterval(t);
  }, [admin, checkHealth]);
  const lookAgain = () => {
    setSetupAgain((n) => n + 1);
    void checkHealth().catch(() => {});
  };
  const loadHousehold = useCallback(() => api.household().then(setHousehold, () => setHousehold(null)), []);
  useEffect(() => void loadHousehold(), [loadHousehold]);
  const together = Boolean(household && (household.members.length > 1 || household.shared > 0));
  const [albumFor, setAlbumFor] = useState<string[] | null>(null); // items to add to an album
  const [naming, setNaming] = useState<Person | null>(null); // the person being named
  const [albumDialog, setAlbumDialog] = useState<AlbumDialog | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false); // the album's Delete was clicked once
  const [asCharacter, setAsCharacter] = useState<Person | null>(null); // the person being made a character
  const timeline = useRef<TimelineHandle>(null);
  const opener = useRef<HTMLElement | null>(null);
  const lastPick = useRef<string | null>(null);

  const fail = useCallback((e: unknown, what: string) => {
    if (e instanceof ApiError && e.status === 401) return onSignedOut();
    const no = e instanceof ApiError && e.status === 403 && e.message !== '';
    setRefused(no);
    setError(no ? (e as ApiError).message : what);
  }, [onSignedOut]);
  const pageFail = useCallback((e: unknown) => fail(e, 'Couldn’t load this page.'), [fail]);

  // The view follows the address, so Back and reloads work.
  useEffect(() => {
    const onHash = () => {
      setView(parseHash(window.location.hash));
      setSearch('');
      setSimilar(null);
      setOpen(null); // an album or person link in the viewer leaves it
      setLooseModel(null);
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);
  const go = useCallback((v: View) => {
    setSearch('');
    setSimilar(null);
    setDrawer(false);
    const h = viewHash(v);
    if (window.location.hash !== h) window.location.hash = h;
    else setView(v);
  }, []);

  // What's shown: a timeline (the library, a collection or search results), or the albums or
  // people pages.
  const load = useCallback(async () => {
    setSelected(new Set());
    lastPick.current = null;
    try {
      const base = viewScope(view);
      const unfiltered = base && view.kind === 'timeline' ? { ...base, ...(hideUtility ? { hideUtility: true } : {}), ...(whose ? { whose } : {}) } : base;
      const scope = unfiltered && filtering && view.kind !== 'trash' && view.kind !== 'vault' ? { ...unfiltered, marks: markFilter } : unfiltered;
      if (view.kind === 'vault' && !asking && !search && !similar) {
        const v = await api.vault();
        setVault(v);
        if (!v.unlocked) { // the lock screen instead
          setModel(null);
          setBuckets([]);
          setResults(null);
          setHeading(null);
          setError('');
          return;
        }
      }
      if (asking) {
        const r = await api.ask(asking.q, asking.prev, asking.exclude);
        const ranked = r.order === 'relevance';
        const m = ranked ? TimelineModel.ranked(r.assets, 'Best matches') : TimelineModel.fromAssets(r.assets);
        setModel(m);
        setBuckets(ranked ? [] : m.months.map((x) => ({ month: x.month, count: x.count })));
        setAnswer({ ...r, q: asking.q || answer?.q || '' });
        setResults({ q: asking.q || answer?.q || '', count: r.assets.length, truncated: r.truncated, mode: 'ask',
          meaning: { available: true, embedded: 0, waiting: 0, textRead: 0, textWaiting: 0 } });
        setHeading(null);
      } else if (search || similar) {
        const r = await api.search(similar ? { similar: similar.id, type: filter } : { q: search, type: filter, mode: searchMode });
        // By meaning, best match first in one group; by names, newest first by month.
        const ranked = r.order === 'relevance';
        const m = ranked ? TimelineModel.ranked(r.assets, similar ? 'Most alike' : 'Best matches') : TimelineModel.fromAssets(r.assets);
        setModel(m);
        setBuckets(ranked ? [] : m.months.map((x) => ({ month: x.month, count: x.count })));
        setResults({ q: search, count: r.assets.length, truncated: r.truncated, mode: r.mode, meaning: r.meaning, found: r.found });
        setHeading(null);
      } else if (view.kind === 'recent') {
        const r = await api.recent({ limit: RECENT_LIMIT });
        setModel(TimelineModel.added(r.assets));
        setBuckets([]); // grouped by the day they arrived, not by month: no scrubber
        setResults(null);
        setHeading({ kicker: 'Library', title: 'Recently added',
          note: r.next ? `the ${RECENT_LIMIT.toLocaleString()} that arrived last, newest first` : 'newest first, by when each came into Photostead' });
      } else if (scope) {
        const [b, h] = await Promise.all([api.buckets(scope), headingFor(view)]);
        setModel(new TimelineModel(b.buckets, b.total, scope));
        setBuckets(b.buckets);
        setResults(null);
        setHeading(h);
      } else {
        setModel(null);
        setBuckets([]);
        setResults(null);
        setHeading(null);
      }
      setNewer(0);
      setError('');
    } catch (e) {
      if (view.kind === 'vault' && e instanceof ApiError && e.status === 403) { // it locked itself meanwhile
        setVault((v) => v && { ...v, unlocked: false });
        setModel(null);
        return;
      }
      fail(e, e instanceof ApiError && e.status === 404 ? 'That album or person isn’t here anymore.' : 'Couldn’t load the library.');
    }
    // answer is read only to keep the question when filters are dropped
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, filter, search, searchMode, similar, asking, hideUtility, whose, filtering, markFilter, adultMode, fail]); // adultMode: the server hides by it
  const clearSearch = useCallback(() => {
    setSearch('');
    setSimilar(null);
    setSearchMode('auto');
    setAsking(null);
    setAnswer(null);
  }, []);
  const refine = (exclude: string[]) => answer && setAsking({ q: '', prev: answer.plan, exclude });
  const memoriesShown = !user.settings?.hideMemories;
  const memoriesAI = Boolean(user.settings?.memoriesAi);
  const loadMemories = useCallback(() => {
    if (!memoriesShown) return setMemories([]);
    api.memories().then((r) => setMemories(r.memories), () => setMemories([]));
  }, [memoriesShown, memoriesAI]); // either setting changed: read them again
  useEffect(loadMemories, [loadMemories]);
  // The vault locks when you leave it, or the page.
  const inVault = view.kind === 'vault';
  useEffect(() => {
    if (!inVault) return;
    const away = () => navigator.sendBeacon('/api/v1/vault/lock');
    window.addEventListener('pagehide', away);
    return () => {
      window.removeEventListener('pagehide', away);
      api.lockVault().catch(() => {});
      setVault(null);
    };
  }, [inVault]);
  useEffect(() => {
    void load();
  }, [load]);

  // Counts for the rail, and new items arriving while a library is being indexed.
  const refreshCounts = useCallback(async () => {
    const c = await api.counts();
    setCounts(c);
    return c;
  }, []);
  useEffect(() => {
    void refreshCounts().catch(() => {});
    api.immich().then((s) => setImmichConnected(s.connected), () => {});
  }, [refreshCounts]);
  useEffect(() => {
    let live = true;
    const tick = async () => {
      try {
        setStatus(await api.status());
        const c = await refreshCounts();
        if (model && !results && view.kind === 'timeline' && !model.scope.marks) { // a filtered timeline holds fewer by design
          // The rail's counts are the whole library: Mine, the Household's, or the timeline without
          // utility items is measured against its own total instead.
          const { whose: only, hideUtility: noUtility } = model.scope;
          const now = only || noUtility ? (await api.buckets(model.scope)).total
            : view.type === 'image' ? c.images : view.type === 'video' ? c.videos : c.all;
          if (live) setNewer(Math.max(0, now - model.total));
        }
      } catch {
        /* try again next time */
      }
    };
    void tick();
    const t = setInterval(tick, status?.import ? 3000 : 20000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [refreshCounts, model, results, view, status?.import]);

  // Appearance, remembered on this device. Applied before paint, so children that read the
  // theme's colors in their effects (the scrubber) see the new ones.
  useLayoutEffect(() => {
    applyTheme(theme);
    setPref('ps.theme', theme);
  }, [theme]);

  const changeZoom = useCallback((d: number) => {
    setZoom((z) => {
      const n = Math.max(0, Math.min(ZOOMS.length - 1, z + d));
      setPref('ps.zoom', String(n));
      return n;
    });
  }, []);
  const toggleRail = useCallback(() => {
    if (isPhone()) {
      setDrawer((d) => !d);
      return;
    }
    setCollapsed((c) => {
      setPref('ps.rail', c ? 'open' : 'collapsed');
      return !c;
    });
  }, []);
  const toggleScrub = useCallback(() => {
    setScrubHidden((h) => {
      setPref('ps.scrub', h ? 'shown' : 'hidden');
      return !h;
    });
  }, []);

  // Selection: click the circle (or shift/cmd-click a tile), shift-click for a range, select a day.
  const toggle = useCallback((a: TimelineAsset, shift: boolean) => {
    const from = lastPick.current; // read now: React may run the update below later
    setSelected((prev) => {
      const next = new Set(prev);
      if (shift && from && model) model.range(from, a.id).forEach((id) => next.add(id));
      else if (next.has(a.id)) next.delete(a.id);
      else next.add(a.id);
      return next;
    });
    lastPick.current = a.id;
  }, [model]);
  const selectDay = useCallback((ids: string[]) => {
    setSelected((prev) => {
      const all = ids.every((id) => prev.has(id));
      const next = new Set(prev);
      ids.forEach((id) => (all ? next.delete(id) : next.add(id)));
      return next;
    });
  }, []);
  const clearSelection = useCallback(() => setSelected(new Set()), []);

  // Edits: the trash, favorites and albums. Each refreshes what's on screen and says what it did,
  // with an Undo where one makes sense.
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 8000);
    return () => clearTimeout(t);
  }, [toast]);
  const afterEdit = useCallback(() => {
    void refreshCounts().catch(() => {});
    void load();
  }, [refreshCounts, load]);
  const facesToPeople = useCallback(async (c: Character) => {
    try {
      const r = await api.characterFacesToPeople(c.id);
      setToast({ text: `Gave ${r.changed === 1 ? 'a group' : `${r.changed} groups`} of faces back to People` });
      afterEdit();
    } catch (e) {
      fail(e, 'Couldn’t change this character.');
    }
  }, [afterEdit, fail]);
  const hidePerson = useCallback(async (p: Person) => {
    try {
      await api.editPerson(p.id, { hidden: !p.hidden });
      void refreshCounts().catch(() => {});
      if (p.hidden) void load();
      else {
        setToast({ text: `Hid ${personName(p)}`, undo: () => {
          api.editPerson(p.id, { hidden: false }).then(() => { setToast(null); void refreshCounts().catch(() => {}); }, (e) => fail(e, 'Couldn’t undo that.'));
        } });
        go({ kind: 'people' });
      }
    } catch (e) {
      fail(e, 'Couldn’t change this person.');
    }
  }, [refreshCounts, load, go, fail]);
  const restoreItems = useCallback(async (ids: string[]) => {
    try {
      await api.restore(ids);
      afterEdit();
      setToast({ text: `Restored ${items(ids.length)}` });
    } catch (e) {
      fail(e, 'Couldn’t restore them.');
    }
  }, [afterEdit, fail]);
  const trashItems = useCallback(async (ids: string[]) => {
    try {
      const r = await api.trash(ids);
      afterEdit();
      if (!r.changed) return setToast({ text: 'Only the person who added them can delete these. Take them out of the Household instead.' });
      setToast({ text: `Moved ${items(r.changed)} to the trash${r.changed < ids.length ? ` (${items(ids.length - r.changed)} of others’ stayed)` : ''}`, undo: () => void restoreItems(ids) });
    } catch (e) {
      fail(e, 'Couldn’t move them to the trash.');
    }
  }, [afterEdit, restoreItems, fail]);
  const deleteForever = useCallback(async (ids?: string[], fromVault = false) => {
    const what = ids ? items(ids.length) : fromVault ? 'everything deleted from the vault' : 'everything in the trash';
    if (!window.confirm(`Delete ${what} for good? This can’t be undone.\n\nPhotostead deletes its own copies. Files in read-only folders it indexes aren’t touched.`)) return;
    try {
      const r = await (fromVault ? api.emptyVaultTrash(ids) : api.emptyTrash(ids));
      afterEdit();
      setToast({ text: r.kept ? `Deleted ${items(r.deleted)}. ${items(r.kept)} couldn’t be deleted and stay in the trash.` : `Deleted ${items(r.deleted)} for good` });
    } catch (e) {
      fail(e, 'Couldn’t delete them.');
    }
  }, [afterEdit, fail]);
  // Into the vault (setting its PIN first, the first time), back out, or deleted inside it.
  const moveToVault = useCallback(async (ids: string[]) => {
    try {
      if (!(await api.vault()).hasPin) {
        setPinDialog({ mode: 'set', then: () => void api.lockVault().catch(() => {}).then(() => moveToVault(ids)) });
        return;
      }
      const r = await api.moveToVault(ids);
      afterEdit();
      setToast({ text: `Moved ${items(r.changed)} to the vault` });
    } catch (e) {
      fail(e, 'Couldn’t move them to the vault.');
    }
  }, [afterEdit, fail]);
  const moveOutOfVault = useCallback(async (ids: string[]) => {
    try {
      const r = await api.moveOutOfVault(ids);
      afterEdit();
      setToast({ text: `Moved ${items(r.changed)} back to the timeline`, undo: () => {
        api.moveToVault(ids).then(() => { setToast(null); afterEdit(); }, (e) => fail(e, 'Couldn’t undo that.'));
      } });
    } catch (e) {
      fail(e, 'Couldn’t move them out of the vault.');
    }
  }, [afterEdit, fail]);
  const deleteInVault = useCallback(async (ids: string[]) => {
    try {
      await api.trash(ids);
      afterEdit();
      setToast({ text: `Deleted ${items(ids.length)}. ${ids.length === 1 ? 'It waits' : 'They wait'} in the vault’s Deleted for 30 days.`, undo: () => void restoreItems(ids) });
    } catch (e) {
      fail(e, 'Couldn’t delete them.');
    }
  }, [afterEdit, restoreItems, fail]);
  const shareItems = useCallback(async (ids: string[], share: boolean) => {
    try {
      const r = await (share ? api.share(ids) : api.unshare(ids));
      afterEdit();
      void loadHousehold();
      setToast(share
        ? { text: r.changed ? `Shared ${items(r.changed)} with the Household` : 'Only your own items can be shared', undo: r.changed ? () => {
          api.unshare(ids).then(() => { setToast(null); afterEdit(); void loadHousehold(); }, (e) => fail(e, 'Couldn’t undo that.'));
        } : undefined }
        : { text: `Took ${items(r.changed)} out of the Household. They stay in their owner’s library.` });
    } catch (e) {
      fail(e, 'Couldn’t change the sharing.');
    }
  }, [afterEdit, loadHousehold, fail]);
  const lockVault = useCallback(() => {
    api.lockVault().then(() => void load(), (e) => fail(e, 'Couldn’t lock the vault.'));
  }, [load, fail]);
  const favoriteItems = useCallback(async (ids: string[], favorite: boolean) => {
    try {
      await api.favorite(ids, favorite);
      afterEdit();
      setToast({ text: favorite ? `Added ${items(ids.length)} to Favorites` : `Took ${items(ids.length)} out of Favorites`, undo: () => {
        api.favorite(ids, !favorite).then(() => { setToast(null); afterEdit(); }, (e) => fail(e, 'Couldn’t undo that.'));
      } });
    } catch (e) {
      fail(e, 'Couldn’t change Favorites.');
    }
  }, [afterEdit, fail]);
  const allFavorite = selected.size > 0 && [...selected].every((id) => model?.find(id)?.favorite);
  // Stars, flags and labels: shown at once (the model is patched in place), saved, and undoable.
  const markItems = useCallback(async (ids: string[], ch: MarkChange, fromViewer = false) => {
    const before = new Map<string, Marks>(ids.map((id) => [id, model?.find(id) ?? {}]));
    if (model) for (const id of ids) model.patch([id], applyMarks(before.get(id)!, ch));
    try {
      await api.mark(ids, ch);
      if (filtering) {
        if (fromViewer) markedWhileOpen.current = true;
        else void load();
      }
      if (fromViewer) return;
      setToast({ text: `${describeChange(ch)} · ${items(ids.length)}`, undo: () => {
        const groups = new Map<string, string[]>();
        for (const id of ids) {
          const back = JSON.stringify(undoChange(before.get(id)!, ch));
          groups.set(back, [...(groups.get(back) ?? []), id]);
        }
        Promise.all([...groups].map(([back, list]) => api.mark(list, JSON.parse(back)))).then(() => {
          setToast(null);
          if (model) for (const id of ids) model.patch([id], { rating: before.get(id)!.rating, flag: before.get(id)!.flag, label: before.get(id)!.label });
          if (filtering) void load();
        }, (e) => fail(e, 'Couldn’t undo that.'));
      } });
    } catch (e) {
      fail(e, 'Couldn’t save that.');
      if (model) for (const id of ids) model.patch([id], { rating: before.get(id)!.rating, flag: before.get(id)!.flag, label: before.get(id)!.label });
    }
  }, [model, filtering, load, fail]);
  // What the selection has in common, for the Mark menu and for keys that toggle (P, X, 6–9).
  const selectedMarks = useMemo(() => {
    const list: Marks[] = [...selected].map((id) => model?.find(id) ?? {});
    const same = <K extends 'rating' | 'flag' | 'label'>(k: K) => (list.length && list.every((m) => m[k] === list[0][k]) ? list[0][k] : undefined);
    return { rating: same('rating'), flag: same('flag'), label: same('label') };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, model, toast]);
  // "Not this one": the day (or the person) leaves memories, with Undo.
  const hideMemory = useCallback((m: Memory) => {
    const p = m.person;
    const done = (text: string, undo: () => Promise<unknown>) => {
      loadMemories();
      setToast({ text, undo: () => {
        undo().then(() => { setToast(null); loadMemories(); }, (e) => fail(e, 'Couldn’t undo that.'));
      } });
    };
    if (p) {
      api.editPerson(p.id, { inMemories: false }).then(() => done(`${personName(p)} is kept out of memories`, () => api.editPerson(p.id, { inMemories: true })),
        (e) => fail(e, 'Couldn’t change that.'));
    } else {
      api.hideMemoryDays(m.from, m.to).then((h) => done(`${m.subtitle} is kept out of memories`, () => api.unhideMemoryDays(h.id)),
        (e) => fail(e, 'Couldn’t change that.'));
    }
  }, [loadMemories, fail]);
  const nextAdultMode = useCallback(() => {
    const next = ADULT_MODES[(ADULT_MODES.indexOf(adultMode) + 1) % ADULT_MODES.length];
    api.setSettings({ adult: next }).then(onUser, (e) => fail(e, 'Couldn’t change that setting.'));
  }, [adultMode, onUser, fail]);
  const setOrganize = useCallback((on: boolean) => {
    api.setSettings({ organize: on }).then((u) => {
      onUser(u);
      if (!on) setMarkFilter({});
    }, (e) => fail(e, 'Couldn’t change that setting.'));
  }, [onUser, fail]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (open || zoneOpen || importOpen) return;
      const typing = (e.target as HTMLElement).closest('input, textarea, select');
      if (typing) return;
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a' && model) {
        e.preventDefault();
        setSelected(new Set(model.loaded().map((a) => a.id)));
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === 'Escape') {
        if (selected.size) clearSelection();
        else if (drawer) setDrawer(false);
        else if (search || similar) clearSearch();
      } else if (e.key === '[') toggleRail();
      else if (e.key === ']') toggleScrub();
      else if (e.key === '+' || e.key === '=') changeZoom(1);
      else if (e.key === '-') changeZoom(-1);
      else if ((e.key === 'Delete' || e.key === 'Backspace') && selected.size) {
        if (view.kind === 'trash' || (view.kind === 'vault' && view.deleted)) void deleteForever([...selected], view.kind === 'vault');
        else if (view.kind === 'vault') void deleteInVault([...selected]);
        else void trashItems([...selected]);
      } else if (e.key === 'f' && selected.size && view.kind !== 'trash' && view.kind !== 'vault') void favoriteItems([...selected], !allFavorite);
      else if (organize && selected.size && view.kind !== 'trash' && view.kind !== 'vault' && markKey(e.key, selectedMarks)) void markItems([...selected], markKey(e.key, selectedMarks)!);
      else return;
      e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, zoneOpen, importOpen, model, selected, drawer, search, similar, clearSearch, clearSelection, toggleRail, toggleScrub, changeZoom,
    view, deleteForever, deleteInVault, trashItems, favoriteItems, allFavorite, organize, selectedMarks, markItems]);

  const onOpen = useCallback((a: TimelineAsset, tile: HTMLElement) => {
    opener.current = tile;
    setOpen(a);
  }, []);
  // An item opened from somewhere that isn't a timeline (a pin on the map): on its own in the viewer.
  const openById = useCallback((id: string) => {
    api.asset(id).then((a) => {
      setLooseModel(TimelineModel.fromAssets([a]));
      setOpen(a);
    }, (e) => fail(e, 'Couldn’t open that item.'));
  }, [fail]);
  const onClose = useCallback(() => {
    setOpen(null);
    if (markedWhileOpen.current) { // marked out of the filter while looking
      markedWhileOpen.current = false;
      void load();
    }
    requestAnimationFrame(() => opener.current?.focus());
  }, [load]);
  const onNavigate = useCallback((a: TimelineAsset) => {
    setOpen(a);
    // An item that isn't on the page (a clip's start image) leaves focus to return where it was.
    opener.current = document.querySelector<HTMLElement>(`.tile[data-id="${a.id}"] .t-open`) ?? opener.current;
  }, []);
  const download = useCallback(async () => {
    setDownloading(true);
    try {
      const r = await api.createDownload([...selected]);
      window.location.assign(r.url); // streams to disk; the page stays
    } catch (e) {
      fail(e, 'Couldn’t prepare the download.');
    } finally {
      setTimeout(() => setDownloading(false), 1500);
    }
  }, [selected, fail]);

  const filterHead = filtering && !results && (
    <div className="filter-head">
      <Icon.filter />
      <span>{describeFilter(markFilter)}</span>
      <button type="button" onClick={() => setMarkFilter({})}>Clear</button>
    </div>
  );
  const albumDeleted = useCallback(async (a: Album) => {
    try {
      await api.deleteAlbum(a.id);
      setConfirmDelete(false);
      setToast({ text: `Deleted the album ${a.title}. Its items are still in your library.` });
      void refreshCounts().catch(() => {});
      go(a.parent ? { kind: 'album', id: a.parent } : { kind: 'albums' });
    } catch (e) {
      fail(e, 'Couldn’t delete the album.');
    }
  }, [refreshCounts, go, fail]);
  useEffect(() => setConfirmDelete(false), [heading]);
  // Uploads go into the album that's open when it can take them (yours, or one you add to).
  const uploadAlbum = view.kind === 'album' && heading?.album && heading.album.kind !== 'smart' && (heading.album.role === 'owner' || heading.album.role === 'contributor')
    ? heading.album : undefined;
  const subItems = heading && (
    <>
      {heading.subAlbums && heading.subAlbums.length > 0 && (
        <div className="cards sub-albums">
          {heading.subAlbums.map((a) => <AlbumCard key={a.id} a={a} onOpen={(id) => go({ kind: 'album', id })} />)}
        </div>
      )}
      {heading.folder && view.kind === 'folder' && (
        <FolderList folders={heading.folder.folders} onOpen={(path) => go({ kind: 'folder', library: view.library, path })} />
      )}
    </>
  );
  const hasSub = Boolean(heading?.subAlbums?.length || heading?.folder?.folders.length);
  const collectionHead = heading && model && (
    <>
    <div className="results-head">
      <div>
        {heading.crumbs ? (
          <p className="kicker crumbs">
            {heading.crumbs.map((c) => (
              <span key={c.label + viewHash(c.view)}>
                <button type="button" onClick={() => go(c.view)}>{c.label}</button> ›
              </span>
            ))}
            <span>{heading.kicker}</span>
          </p>
        ) : (
          <p className="kicker">{heading.kicker}</p>
        )}
        <h1>{heading.title}</h1>
        {heading.body && (
          <div className="h-body">
            <p>{heading.body}</p>
            <Copy text={heading.body} label="Copy prompt" />
          </div>
        )}
        <p className="muted">
          {model.total.toLocaleString()} {model.total === 1 ? 'item' : 'items'}
          {heading.note ? ` · ${heading.note}` : ''}
          {heading.adult && counts?.adultScan ? ` · still looking through ${counts.adultScan.toLocaleString()} more` : ''}
        </p>
      </div>
      <div className="h-actions">
        {heading.adult && (
          <div className="seg" role="group" aria-label="Which items">
            <button type="button" aria-pressed={heading.adult === 'adult'} onClick={() => go({ kind: 'label', label: 'adult' })}
              title="Nudity or sex">
              Adult{counts?.labels.adult ? ` · ${counts.labels.adult.toLocaleString()}` : ''}
            </button>
            <button type="button" aria-pressed={heading.adult === 'suggestive'} onClick={() => go({ kind: 'label', label: 'suggestive' })}
              title="Lingerie, swimwear or poses, neither nude nor explicit">
              Suggestive{counts?.labels.suggestive ? ` · ${counts.labels.suggestive.toLocaleString()}` : ''}
            </button>
          </div>
        )}
        {heading.vault === 'open' && (
          <>
            <button type="button" className="pill" onClick={() => go({ kind: 'vault', deleted: true })} title="Items deleted in the vault wait here for 30 days">
              <Icon.trash />
              Deleted{vault?.deleted ? ` · ${vault.deleted.toLocaleString()}` : ''}
            </button>
            <button type="button" className="pill" onClick={() => setPinDialog({ mode: 'change' })}>
              Change PIN
            </button>
            <button type="button" className="pill" onClick={lockVault} title="It also locks when you leave it, and 10 minutes after it was last used">
              <Icon.lock />
              Lock
            </button>
          </>
        )}
        {heading.vault === 'deleted' && (
          <>
            <button type="button" className="pill" onClick={() => go({ kind: 'vault' })}>
              <Icon.back />
              Back to the vault
            </button>
            {model.total > 0 && (
              <button type="button" className="pill danger" onClick={() => void deleteForever(undefined, true)}>
                <Icon.trash />
                Delete all for good
              </button>
            )}
          </>
        )}
        {heading.trash && model.total > 0 && (
          <button type="button" className="pill danger" onClick={() => void deleteForever()}>
            <Icon.trash />
            Empty trash
          </button>
        )}
        {heading.character && (
          <button type="button" className="pill" onClick={() => setCharacterEdit({ character: heading.character })}>
            Edit
          </button>
        )}
        {heading.character && heading.character.faceGroups > 0 && (
          <button type="button" className="pill" onClick={() => void facesToPeople(heading.character!)}
            title="These faces are someone real after all: give them back to People. Items matched by LoRAs stay">
            Faces back to People
          </button>
        )}
        {heading.person && (
          <>
            <button type="button" className="pill" onClick={() => setNaming(heading.person!)}>
              {heading.person.name ? 'Rename' : 'Add a name'}
            </button>
            {heading.person.kind !== 'pet' && (
              <button type="button" className="pill" onClick={() => setAsCharacter(heading.person!)} title="An AI character rather than someone real: move them to Characters">
                It’s a character
              </button>
            )}
            <button type="button" className="pill" onClick={() => void hidePerson(heading.person!)}
              title={heading.person.hidden ? 'Show them in People and in items’ details again' : 'Keep them out of People and of items’ details'}>
              {heading.person.hidden ? 'Unhide' : 'Hide'}
            </button>
            <button type="button" className="pill" onClick={() => {
              const p = heading.person!;
              api.editPerson(p.id, { inMemories: Boolean(p.keptOutOfMemories) }).then((np) => {
                setHeading((h) => h && { ...h, person: np });
                loadMemories();
                setToast({ text: np.keptOutOfMemories ? `${personName(np)} is kept out of memories, with every item they’re in` : `${personName(np)} is back in memories` });
              }, (e) => fail(e, 'Couldn’t change that.'));
            }} title={heading.person.keptOutOfMemories ? 'Let them, and the items they’re in, back into memories' : 'Keep them, and every item they’re in, out of memories'}>
              {heading.person.keptOutOfMemories ? 'Back in memories' : 'Keep out of memories'}
            </button>
          </>
        )}
        {heading.album && (heading.album.role !== 'owner' || (heading.album.members ?? 0) > 0) && (
          <button type="button" className="pill" onClick={() => setTalkOpen(!talkOpen)} aria-pressed={talkOpen} data-testid="activity">
            <Icon.household /> Activity
          </button>
        )}
        {heading.album && heading.album.role !== 'owner' && (
          <>
            <span className="muted h-shared"><Icon.household /> Shared by {heading.album.owner?.name}{heading.album.role === 'contributor' ? ' · you can add to it' : ''}</span>
            {heading.album.role === 'contributor' && <AlbumUpload albumId={heading.album.id} onDone={() => void load()} />}
          </>
        )}
        {heading.album && heading.album.role === 'owner' && (
          <>
            {heading.album.kind === 'smart' ? (
              <button type="button" className="pill" onClick={() => setAlbumDialog({ kind: 'smart', album: heading.album })}>
                <Icon.filter />
                Rules
              </button>
            ) : (
              <>
                <button type="button" className="pill" onClick={() => setAlbumDialog({ kind: 'new', parent: heading.album })} title="An album inside this one">
                  <Icon.plus />
                  Album inside
                </button>
                <button type="button" className="pill" onClick={() => setAlbumDialog({ kind: 'rename', album: heading.album! })}>
                  Rename
                </button>
                <button type="button" className="pill" onClick={() => setLinksFor(heading.album!)} title="With people (the household, or guests), or by a link anyone can open">
                  <Icon.share />
                  Share
                </button>
              </>
            )}
            <button type="button" className="pill" onClick={() => setAlbumDialog({ kind: 'move', album: heading.album! })}>
              Move
            </button>
            <button type="button" className={`pill${confirmDelete ? ' danger' : ''}`}
              onClick={() => (confirmDelete ? void albumDeleted(heading.album!) : setConfirmDelete(true))}
              title="Only the album goes: its items stay, and albums inside it move up">
              {confirmDelete ? 'Delete the album?' : 'Delete'}
            </button>
          </>
        )}
        {heading.back && (
          <button type="button" className="pill" onClick={() => go(heading.back!)}>
            <Icon.back />
            {heading.backLabel}
          </button>
        )}
      </div>
    </div>
    {subItems}
    </>
  );
  // Everything, only yours, or only the Household's (shown once there's a household to speak of).
  const whoseBar = !results && view.kind === 'timeline' && together && (
    <div className="whose" role="group" aria-label="Whose items">
      {([['', 'Everything'], ['mine', 'Mine'], ['household', household?.name || 'Household']] as [Whose, string][]).map(([w, label]) => (
        <button key={w} type="button" aria-pressed={whose === w} onClick={() => { setWhose(w); setPref('ps.whose', w); }}>
          {label}
        </button>
      ))}
    </div>
  );
  // Another member's items can be viewed, favorited, marked and put in your albums, not changed.
  const othersSelected = [...selected].some((id) => model?.find(id)?.fromOthers);
  // The setup checklist, at the top of the home timeline (all of it: not one type, not the household's).
  const setupHere = setupSteps.length > 0 && !user.settings?.setupHidden && !results && !filtering && view.kind === 'timeline' && view.type === '' && whose !== 'household';
  const changeSetup = (change: { setupHidden?: boolean; setupSkipped?: SetupStepId[] }) =>
    api.setSettings(change).then(onUser, (e) => fail(e, 'Couldn’t change that.'));
  const setupCard = setupHere && (
    <SetupCard
      steps={setupSteps}
      onAction={(id) => ({ photos: () => setGuideOpen(true), phone: () => setDevicesOpen(true), away: () => setDevicesOpen(true),
        household: () => setHouseholdOpen(true), copy: () => setBackupsOpen(true) })[id]()}
      onSkip={(id, skip) => {
        const now = user.settings?.setupSkipped ?? [];
        void changeSetup({ setupSkipped: skip ? [...now, id] : now.filter((x) => x !== id) });
      }}
      onHide={() => void changeSetup({ setupHidden: true })}
    />
  );
  const emptyKey = view.kind === 'vault' && view.deleted ? 'vaultDeleted' : view.kind;
  const reading = status?.indexing ?? 0;
  const emptyText: Record<string, [string, string]> = {
    timeline: reading > 0 ? ['Reading your photos', `${reading.toLocaleString()} ${reading === 1 ? 'file' : 'files'} to go. They show up here as they’re ready.`]
      : ['Nothing here yet', 'Drop photos, or a whole folder, anywhere on this page. Import shows every way in.'],
    favorites: ['No favorites yet', 'Mark a photo or video as a favorite and it shows here. Importing from another app brings its favorites too.'],
    archive: ['Nothing archived', 'Archived items stay out of the timeline and show here. Importing from another app brings its archive too.'],
    trash: ['The trash is empty', 'Deleted items wait here for 30 days, so you can restore them. Then they’re deleted for good.'],
    vault: ['The vault is empty', 'Select photos or videos anywhere and choose Vault to move them here. Only you see them, and only after typing the PIN.'],
    vaultDeleted: ['Nothing deleted', 'Items deleted in the vault wait here for 30 days, so you can restore them. They never show in the main trash.'],
    label: ['Nothing here', 'Nothing in your library has been recognized as this yet. Items are sorted after their text is read.'],
    ai: ['Nothing here', 'No items carry evidence of a generative model.'],
    captured: ['Nothing here', 'No items carry a camera’s or phone’s details.'],
    recent: ['Nothing here yet', 'Photos and videos appear here as they arrive: uploaded, saved from the web, or found in your folders.'],
    album: ['Nothing here yet', 'Select photos or videos and choose Album to put them here.'],
    folder: ['Nothing here', 'No photos or videos in this folder.'],
    person: ['Nothing to show', 'None of this person’s items are in the timeline.'],
    prompt: ['Nothing to show', 'None of this prompt’s items are in the timeline.'],
    model: ['Nothing to show', 'None of this model’s items are in the timeline.'],
    lora: ['Nothing to show', 'None of this LoRA’s items are in the timeline.'],
    character: ['Nothing to show yet', 'Nothing made with this character’s LoRAs is in the timeline. Edit the character to choose other LoRAs.'],
  };

  const foundNote = (f?: { prompts: number; text: number; details?: number; names: number }) => {
    const parts = [
      f?.prompts && `${f.prompts.toLocaleString()} match the prompt`,
      f?.text && `${f.text.toLocaleString()} the text in the picture`,
      f?.details && `${f.details.toLocaleString()} the description or tags`,
      f?.names && `${f.names.toLocaleString()} the file name`,
    ].filter(Boolean);
    return parts.length ? ` · ${parts.join(', ')}` : '';
  };
  const meaningNote = (m: MeaningStatus) =>
    m.waiting > 0
      ? ` · ${m.embedded.toLocaleString()} of ${(m.embedded + m.waiting).toLocaleString()} items can be searched this way so far`
      : m.textWaiting > 0
        ? ` · text read from ${m.textRead.toLocaleString()} of ${(m.textRead + m.textWaiting).toLocaleString()} items so far`
        : '';
  const resultsHead = results?.mode === 'ask' && answer ? (
    <div className="results-head">
      <div>
        <p className="kicker">Ask{answer.order === 'relevance' ? ' · closest first' : ''}</p>
        <h1>{answer.q}</h1>
        <p className="a-answer">{answer.answer}</p>
        {answer.chips.length > 0 && (
          <div className="a-chips">
            {answer.chips.map((c) => (
              <button key={`${c.key}:${c.label}`} type="button" className="a-chip" onClick={() => refine([c.key])} title="Leave this out">
                <span>{c.kind}</span>
                {c.label}
                <Icon.x />
              </button>
            ))}
          </div>
        )}
        {answer.alt && (
          <button type="button" className="link a-alt" onClick={() => refine([answer.alt!.remove])}>
            Show the {answer.alt.count.toLocaleString()} {answer.alt.describe}
          </button>
        )}
        {answer.truncated && <p className="muted small a-notes">Showing the first {answer.assets.length.toLocaleString()}.</p>}
        {answer.notes.map((n) => (
          <p key={n} className="muted small a-notes">
            {n}
          </p>
        ))}
      </div>
      <button type="button" className="pill" onClick={clearSearch}>
        Back to {view.kind === 'timeline' ? 'the timeline' : 'where you were'}
      </button>
    </div>
  ) : results && (
    <div className="results-head">
      <div>
        <p className="kicker">
          {results.mode === 'similar' ? 'Search · more like this' : results.mode === 'meaning' ? 'Search · by meaning' : 'Search · file and folder names'}
          {filter ? ` · ${filter === 'image' ? 'photos' : 'videos'}` : ''}
        </p>
        <h1>{results.mode === 'similar' ? 'More like this' : results.q}</h1>
        <p className="muted">
          {results.count === 0
            ? results.mode === 'similar'
              ? 'This item isn’t ready to compare yet.'
              : results.mode === 'meaning'
                ? 'Nothing matches yet.'
                : 'No file or folder names contain all of those words.'
            : results.mode === 'names'
              ? `${results.count.toLocaleString()} ${results.count === 1 ? 'item' : 'items'}${results.truncated ? ' (the newest 2,000)' : ''}`
              : `The ${results.count.toLocaleString()} closest matches, best first${foundNote(results.found)}${meaningNote(results.meaning)}`}
        </p>
        {results.mode !== 'similar' && (results.mode === 'meaning' || results.meaning.available) && (
          <button type="button" className="link r-switch" onClick={() => setSearchMode(results.mode === 'meaning' ? 'names' : 'meaning')}>
            {results.mode === 'meaning' ? 'Search file and folder names instead' : 'Search by meaning instead'}
          </button>
        )}
        {results.mode !== 'similar' && QUESTION.test(results.q.trim()) && (
          <button type="button" className="link r-switch" onClick={() => {
            setAskMode(true);
            setSearch('');
            setAsking({ q: results.q });
          }}>
            Ask this instead
          </button>
        )}
      </div>
      <button type="button" className="pill" onClick={clearSearch}>
        Back to {view.kind === 'timeline' ? 'the timeline' : 'where you were'}
      </button>
    </div>
  );

  const cls = ['app', collapsed && 'rail-collapsed', scrubHidden && 'scrub-hidden', drawer && 'drawer-open'].filter(Boolean).join(' ');
  return (
    <div className={cls}>
      <Rail
        user={user}
        view={view}
        searching={Boolean(results)}
        counts={counts}
        immichConnected={immichConnected}
        collapsed={collapsed}
        theme={theme}
        status={status}
        onView={go}
        onToggle={toggleRail}
        onImport={() => {
          setDrawer(false);
          setGuideOpen(true);
        }}
        onUpload={(folder) => {
          setDrawer(false);
          pickFiles(folder, uploadAlbum?.id);
        }}
        onTheme={() => setTheme((t) => THEMES[(THEMES.indexOf(t) + 1) % THEMES.length])}
        onTimeZone={() => setZoneOpen(true)}
        onSignOut={() => api.logout().finally(onSignedOut)}
        organize={organize}
        onOrganize={() => setOrganize(!organize)}
        memories={memoriesShown}
        onMemories={() => setMemoriesOpen(true)}
        onSecurity={() => setSecurityOpen(true)}
        onBackups={() => setBackupsOpen(true)}
        onDevices={() => setDevicesOpen(true)}
        onHousehold={() => setHouseholdOpen(true)}
        setupLeft={user.settings?.setupHidden ? stepsLeft(setupSteps) : 0}
        healthLook={admin && health ? needingLook(health) : null}
        onHealth={() => {
          setDrawer(false);
          setHealthOpen(true);
        }}
        onSetup={() => {
          setDrawer(false);
          go({ kind: 'timeline', type: '' });
          void changeSetup({ setupHidden: false });
        }}
        adultMode={adultMode}
        onAdultMode={nextAdultMode}
        hideUtility={hideUtility}
        onHideUtility={() =>
          setHideUtility((h) => {
            setPref('ps.utility', h ? 'shown' : 'hidden');
            return !h;
          })
        }
      />
      <main className="main">
        <button type="button" className="drawer-btn" onClick={() => setDrawer(true)} aria-label="Open the library menu">
          <Icon.menu />
        </button>
        <button type="button" className="scrub-open" onClick={toggleScrub} aria-label="Show the timeline scrubber (])" title="Show the timeline scrubber (])">
          <Icon.panelR />
        </button>
        {newer > 0 && (
          <button type="button" className="newer" onClick={() => void load()}>
            {newer.toLocaleString()} new · Show
          </button>
        )}
        {error && (
          <p className="notice" role="alert">
            {error}{' '}
            {!refused && (
              <button type="button" onClick={() => void load()}>
                Try again
              </button>
            )}
          </p>
        )}
        {!results && view.kind === 'albums' ? (
          <AlbumsPage key={String(counts?.albums)} onOpen={(id) => go({ kind: 'album', id })} onImport={() => setGuideOpen(true)} onFail={pageFail}
            onNew={() => setAlbumDialog({ kind: 'new' })} onNewSmart={() => setAlbumDialog({ kind: 'smart' })} />
        ) : !results && view.kind === 'lookalikes' ? (
          <LookAlikesPage
            onOpen={(list, a) => {
              setLooseModel(TimelineModel.fromAssets(list));
              setOpen(a);
            }}
            onFail={pageFail}
            onChanged={(text, undo) => {
              void refreshCounts().catch(() => {});
              setToast({ text, undo: undo && (() => {
                undo().then(() => {
                  setToast(null);
                  void refreshCounts().catch(() => {});
                }, (e) => fail(e, 'Couldn’t undo that.'));
              }) });
            }}
          />
        ) : !results && view.kind === 'vault' && vault && !vault.unlocked ? (
          <VaultLock key={String(vault.hasPin)} state={vault} onOpen={(v) => { setVault(v); void load(); }} />
        ) : !results && view.kind === 'folders' ? (
          <FoldersPage onOpen={(library) => go({ kind: 'folder', library, path: '' })} onFail={pageFail} />
        ) : !results && view.kind === 'people' ? (
          <PeoplePage onOpen={(id) => go({ kind: 'person', id })} onFail={pageFail} />
        ) : !results && view.kind === 'places' ? (
          <PlacesPage dark={dark} onOpenPlace={(id) => go({ kind: 'place', id })} onOpenAsset={openById} onFail={pageFail} />
        ) : !results && view.kind === 'characters' ? (
          <CharactersPage key={String(counts?.characters)} onOpen={(id) => go({ kind: 'character', id })} onNew={() => setCharacterEdit({})} onFail={pageFail} />
        ) : !results && view.kind === 'prompts' ? (
          <PromptsPage onOpen={(id) => go({ kind: 'prompt', id })} onFail={pageFail} />
        ) : !results && view.kind === 'models' ? (
          <ModelsPage onOpen={(m) => go({ kind: m.kind, id: m.id })} onFail={pageFail} />
        ) : model && model.total === 0 && !results ? (
          <div className="scroller">
            {collectionHead}
            {filterHead}
            {whoseBar}
            {setupCard}
            {hasSub && !filtering ? (
              <p className="sub-note">{view.kind === 'folder' ? 'No photos or videos directly in this folder.' : 'No items directly in this album.'}</p>
            ) : (
            <div className="empty page-empty">
              <h1>{filtering ? 'Nothing matches' : whose && view.kind === 'timeline' ? 'Nothing here' : emptyText[emptyKey]?.[0]}</h1>
              <p>{filtering ? 'Nothing here has those stars, flags or labels.'
                : whose === 'mine' && view.kind === 'timeline' ? 'None of these are yours: everything here was shared with the Household.'
                : whose === 'household' && view.kind === 'timeline' ? 'Nothing has been shared with the Household yet. Select photos and choose Share, or turn on auto-sharing in Household.'
                : emptyText[emptyKey]?.[1]}</p>
              {(view.kind === 'favorites' || view.kind === 'archive' || (view.kind === 'timeline' && !whose && !reading && !filtering)) && (
                <button type="button" className="pill" onClick={() => setGuideOpen(true)}>
                  Import…
                </button>
              )}
            </div>
            )}
          </div>
        ) : (
          model && (
            <Timeline
              key={`${viewHash(view)}:${results?.q ?? ''}:${results?.mode ?? ''}:${similar?.id ?? ''}`}
              ref={timeline}
              model={model}
              zoom={zoom}
              header={<>{resultsHead || collectionHead}{filterHead}{whoseBar}{setupCard}{!results && !filtering && view.kind === 'timeline' && view.type === '' && whose !== 'household' && memories.length > 0 && (
                <MemoryStrip memories={memories} onHide={hideMemory} onOpen={(m) => {
                  setLooseModel(TimelineModel.fromAssets(m.assets));
                  setOpen(m.assets[0]);
                }} />
              )}</>}
              organize={organize}
              blurAdult={adultMode === 'blur'}
              selected={selected}
              onOpen={onOpen}
              onToggle={toggle}
              onSelectDay={selectDay}
              onVisibleMonths={setVisible}
            />
          )
        )}
        {selected.size > 0 ? (
          <SelectionBar
            count={selected.size}
            busy={downloading}
            onClear={clearSelection}
            onDownload={() => void download()}
            favorite={allFavorite}
            onFavorite={() => void favoriteItems([...selected], !allFavorite)}
            marks={organize ? selectedMarks : undefined}
            onMark={(ch) => void markItems([...selected], ch)}
            onEdit={view.kind !== 'trash' && view.kind !== 'vault' && !othersSelected ? () => setEditFor({ ids: [...selected] }) : undefined}
            onStack={view.kind !== 'trash' && view.kind !== 'vault' && !othersSelected ? () => {
              const ids = [...selected];
              api.makeStack(ids).then((st) => {
                setToast({ text: `Stacked ${items(st.members.length)}`, undo: () => {
                  api.unstack(st.id).then(() => { setToast(null); afterEdit(); }, (e) => fail(e, 'Couldn’t undo that.'));
                } });
                afterEdit();
              }, (e) => fail(e, e instanceof ApiError ? e.message : 'Couldn’t stack them.'));
            } : undefined}
            onAlbum={() => setAlbumFor([...selected])}
            onTrash={() => void trashItems([...selected])}
            trash={(view.kind === 'trash' || (view.kind === 'vault' && view.deleted)) && !results
              ? { onRestore: () => void restoreItems([...selected]), onDelete: () => void deleteForever([...selected], view.kind === 'vault') } : undefined}
            vault={view.kind === 'vault' && !view.deleted && !results
              ? { onOut: () => void moveOutOfVault([...selected]), onDelete: () => void deleteInVault([...selected]) } : undefined}
            onVault={view.kind !== 'trash' && view.kind !== 'vault' && !othersSelected ? () => void moveToVault([...selected]) : undefined}
            share={household && view.kind !== 'trash' && view.kind !== 'vault' ? (() => {
              const ids = [...selected];
              const allShared = ids.every((id) => model?.find(id)?.shared);
              return { shared: allShared, onClick: () => void shareItems(ids, !allShared) };
            })() : undefined}
            extra={view.kind === 'album' && !results && heading?.album?.kind !== 'smart' ? {
              label: 'Remove from album',
              title: 'Take them out of this album; they stay in your library',
              onClick: () => {
                const id = view.id;
                const ids = [...selected];
                api.removeFromAlbum(id, ids).then(() => {
                  afterEdit();
                  setToast({ text: `Took ${items(ids.length)} out of the album`, undo: () => {
                    api.addToAlbum(id, ids).then(() => { setToast(null); afterEdit(); }, (e) => fail(e, 'Couldn’t undo that.'));
                  } });
                }, (e) => fail(e, 'Couldn’t change the album.'));
              },
            } : view.kind === 'label' ? {
              label: `Not ${labelInfo(view.label)!.one}`,
              title: 'Take these out; sorting won’t add them back',
              onClick: () => {
                api.removeLabel(view.label, [...selected]).then(() => {
                  void refreshCounts().catch(() => {});
                  void load();
                }, (e) => fail(e, 'Couldn’t change the collection.'));
              },
            } : view.kind === 'person' && !results ? {
              label: `Not ${heading?.person?.name || (heading?.person?.kind === 'pet' ? `this ${heading.person.species ?? 'pet'}` : 'this person')}`,
              title: 'Take these out; face recognition won’t put them back with this person',
              onClick: () => {
                const id = view.id;
                const ids = [...selected];
                api.removeFromPerson(id, ids).then((r) => {
                  setToast({ text: `Took ${items(r.changed)} out of ${heading?.person ? personName(heading.person) : 'this person'}` });
                  afterEdit();
                }, (e) => fail(e, 'Couldn’t change this person.'));
              },
            } : view.kind === 'character' ? {
              label: 'Not this character',
              title: 'Take these out of the character; matching won’t add them back',
              onClick: () => {
                const id = view.id;
                api.removeFromCharacter(id, [...selected]).then(() => {
                  void refreshCounts().catch(() => {});
                  void load();
                }, (e) => fail(e, 'Couldn’t change the character.'));
              },
            } : undefined}
          />
        ) : (
          <CommandBar
            uploadAlbum={uploadAlbum?.id}
            query={answer ? answer.q : search}
            placeholder={askMode ? 'Ask: when did I last make videos of…? How many receipts from 2025?' : status?.search?.available ? 'Search your library' : 'Search file and folder names'}
            ask={askMode}
            onAsk={() => setAskMode((a) => !a)}
            zoom={zoom}
            onSearch={(q) => {
              setSimilar(null);
              setSearchMode('auto');
              if (askMode && q) {
                setSearch('');
                setAsking({ q, prev: answer?.plan }); // a follow-up refines the last answer
              } else {
                setAsking(null);
                setAnswer(null);
                setSearch(q);
              }
            }}
            onZoom={changeZoom}
            filter={organize && !results ? markFilter : undefined}
            onFilter={setMarkFilter}
            onSaveSmart={() => setAlbumDialog({ kind: 'smart', start: { rating: markFilter.rating, flag: markFilter.flag, label: markFilter.label } })}
          />
        )}
      </main>
      <aside className="scrub" aria-label="Timeline scrubber">
        <div className="s-head">
          <button type="button" className="r-x" onClick={toggleScrub} aria-label="Hide the timeline scrubber (])" title="Hide the timeline scrubber (])">
            <Icon.panelR />
          </button>
        </div>
        <Scrubber buckets={buckets} visible={visible} theme={dark ? 'dark' : 'light'} onJump={(m) => timeline.current?.scrollToMonth(m)} />
      </aside>
      {drawer && <div className="scrim" onClick={() => setDrawer(false)} />}
      {open && (model ?? looseModel) && (
        <Viewer
          model={(model ?? looseModel)!}
          zone={user.timeZone}
          asset={open}
          onNavigate={onNavigate}
          onClose={onClose}
          onSimilar={(a) => {
            setOpen(null);
            setSearch('');
            setSimilar(a);
          }}
          album={view.kind === 'album' && heading?.album && (heading.album.role !== 'owner' || (heading.album.members ?? 0) > 0) ? heading.album.id : undefined}
          onTrash={(a) => trashItems([a.id])}
          onRestore={(a) => restoreItems([a.id])}
          onFavorite={(a, f) => favoriteItems([a.id], f)}
          organize={organize}
          blurAdult={adultMode === 'blur'}
          onMark={(a, ch) => markItems([a.id], ch, true)}
          onEdit={(a, d) => setEditFor({ ids: [a.id], takenAt: wallTime(d.takenAt, d.offsetMin, user.timeZone), description: d.description })}
          version={edited}
          onPhotoEdited={(a, change) => {
            (model ?? looseModel)?.patch([a.id], change);
            setOpen((o) => (o && o.id === a.id ? { ...o, ...change } : o));
          }}
          onStackChanged={(text) => {
            setToast({ text });
            markedWhileOpen.current = true; // the timeline shows the change when the viewer closes
          }}
        />
      )}
      {talkOpen && view.kind === 'album' && heading?.album && (
        <AlbumTalk
          albumId={heading.album.id}
          owner={heading.album.role === 'owner'}
          onClose={() => setTalkOpen(false)}
          onOpen={(id) => {
            const a = model?.loaded().find((x) => x.id === id);
            if (a) {
              setTalkOpen(false); // the viewer has the item's own comments
              setOpen(a);
            }
          }}
        />
      )}
      <Uploads
        album={uploadAlbum?.id}
        albumTitle={uploadAlbum?.title}
        onAdded={() => {
          void refreshCounts().catch(() => {});
          void load();
        }}
      />
      {toast && (
        <div className="toast" role="status">
          <span>{toast.text}</span>
          {toast.undo && (
            <button type="button" onClick={toast.undo}>
              Undo
            </button>
          )}
        </div>
      )}
      {albumDialog?.kind === 'new' && (
        <NewAlbumDialog parent={albumDialog.parent} onClose={() => setAlbumDialog(null)} onDone={(a) => {
          setAlbumDialog(null);
          void refreshCounts().catch(() => {});
          go({ kind: 'album', id: a.id });
        }} />
      )}
      {albumDialog?.kind === 'smart' && (
        <SmartDialog album={albumDialog.album} start={albumDialog.start} parent={albumDialog.parent} onClose={() => setAlbumDialog(null)} onDone={(a) => {
          setAlbumDialog(null);
          void refreshCounts().catch(() => {});
          if (view.kind === 'album' && view.id === a.id) void load();
          else go({ kind: 'album', id: a.id });
        }} />
      )}
      {albumDialog?.kind === 'rename' && (
        <RenameDialog album={albumDialog.album} onClose={() => setAlbumDialog(null)} onDone={() => {
          setAlbumDialog(null);
          void load();
        }} />
      )}
      {albumDialog?.kind === 'move' && (
        <MoveDialog album={albumDialog.album} onClose={() => setAlbumDialog(null)} onDone={() => {
          setAlbumDialog(null);
          void load();
        }} />
      )}
      {securityOpen && <SecurityDialog onClose={() => setSecurityOpen(false)} />}
      {healthOpen && (
        <HealthDialog
          checks={health}
          onCheck={checkHealth}
          onOpen={(id) => {
            setHealthOpen(false);
            if (id === 'backups') setBackupsOpen(true);
            else setDevicesOpen(true);
          }}
          onClose={() => setHealthOpen(false)}
        />
      )}
      {backupsOpen && <BackupsDialog onClose={() => { setBackupsOpen(false); lookAgain(); }} />}
      {devicesOpen && <DevicesDialog user={user} onClose={() => { setDevicesOpen(false); lookAgain(); }} />}
      {linksFor && <ShareLinksDialog album={linksFor} onClose={() => setLinksFor(null)} />}
      {householdOpen && <HouseholdDialog onClose={() => { setHouseholdOpen(false); void loadHousehold(); lookAgain(); }} onChanged={() => { void load(); void loadHousehold(); }} />}
      {memoriesOpen && (
        <MemoriesDialog
          user={user}
          onClose={() => setMemoriesOpen(false)}
          onChanged={(u) => (u ? onUser(u) : loadMemories())}
        />
      )}
      {pinDialog && (
        <PinDialog
          mode={pinDialog.mode}
          onClose={() => setPinDialog(null)}
          onDone={(v) => {
            const then = pinDialog.then;
            setPinDialog(null);
            if (view.kind === 'vault') setVault(v);
            if (then) then();
            else setToast({ text: 'The vault has a new PIN' });
          }}
        />
      )}
      {editFor && (
        <EditDialog
          ids={editFor.ids}
          takenAt={editFor.takenAt}
          description={editFor.description}
          onClose={() => setEditFor(null)}
          onChanged={(text, undo, dates) => {
            setEdited((n) => n + 1);
            const refresh = () => {
              if (open) markedWhileOpen.current = true; // the timeline reorders when the viewer closes
              else afterEdit();
            };
            if (dates) refresh();
            setToast({ text, undo: undo && (() => {
              undo().then(() => {
                setToast(null);
                setEdited((n) => n + 1);
                refresh();
              }, (e) => fail(e, 'Couldn’t undo that.'));
            }) });
          }}
        />
      )}
      {asCharacter && (
        <AsCharacterDialog
          person={asCharacter}
          onClose={() => setAsCharacter(null)}
          onDone={(c) => {
            const p = asCharacter;
            setAsCharacter(null);
            void refreshCounts().catch(() => {});
            setToast({ text: `${personName(p)} is ${c.name} now, in Characters`, undo: () => {
              api.personFromCharacter(p.id).then(async () => {
                // A character made just now, with nothing else to it, goes again.
                const left = await api.character(c.id).catch(() => null);
                if (left && left.count === 0 && left.faceGroups === 0 && !left.patterns.some((x) => x.trim())) await api.deleteCharacter(c.id).catch(() => {});
                setToast(null);
                void refreshCounts().catch(() => {});
                go({ kind: 'person', id: p.id });
              }, (e) => fail(e, 'Couldn’t undo that.'));
            } });
            go({ kind: 'character', id: c.id });
          }}
        />
      )}
      {naming && (
        <NameDialog
          person={naming}
          onClose={() => setNaming(null)}
          onRenamed={() => {
            setNaming(null);
            afterEdit();
          }}
          onMerged={(into) => {
            setNaming(null);
            void refreshCounts().catch(() => {});
            setToast({ text: `${personName(naming)} and ${personName(into)} are one ${into.kind === 'pet' ? 'pet' : 'person'} now` });
            go({ kind: 'person', id: into.id });
          }}
        />
      )}
      {albumFor && (
        <AlbumPicker
          ids={albumFor}
          onClose={() => setAlbumFor(null)}
          onDone={(album) => {
            const ids = albumFor;
            setAlbumFor(null);
            afterEdit();
            setToast({ text: `Added ${items(ids.length)} to ${album.title}`, undo: () => {
              api.removeFromAlbum(album.id, ids).then(() => { setToast(null); afterEdit(); }, (e) => fail(e, 'Couldn’t undo that.'));
            } });
          }}
        />
      )}
      {characterEdit && (
        <CharacterDialog
          character={characterEdit.character}
          onClose={() => setCharacterEdit(null)}
          onSaved={(c) => {
            setCharacterEdit(null);
            void refreshCounts().catch(() => {});
            if (!c) go({ kind: 'characters' });
            else if (view.kind === 'character' && view.id === c.id) void load();
            else go({ kind: 'character', id: c.id });
          }}
        />
      )}
      {guideOpen && (
        <ImportGuide
          onClose={() => setGuideOpen(false)}
          onImmich={() => {
            setGuideOpen(false);
            setImportOpen(true);
          }}
          onPick={() => {
            setGuideOpen(false);
            pickFiles(true);
          }}
        />
      )}
      {importOpen && (
        <ImportDialog
          onClose={() => {
            setImportOpen(false);
            api.immich().then((s) => setImmichConnected(s.connected), () => {});
          }}
          onImported={() => {
            void refreshCounts().catch(() => {});
            void load();
          }}
        />
      )}
      {zoneOpen && (
        <ZoneDialog
          user={user}
          onClose={() => setZoneOpen(false)}
          onSaved={(u) => {
            onUser(u);
            setZoneOpen(false);
            void load(); // days may have moved
          }}
        />
      )}
    </div>
  );
}

// How many Recently added shows: the newest arrivals, enough for a big import day.
const RECENT_LIMIT = 1000;

/** The heading for a collection: what it is, its name, and the way back. */
async function headingFor(v: View): Promise<Heading | null> {
  switch (v.kind) {
    case 'favorites':
      return { kicker: 'Library', title: 'Favorites' };
    case 'archive':
      return { kicker: 'Library', title: 'Archive', note: 'kept out of the timeline' };
    case 'trash':
      return { kicker: 'Library', title: 'Trash', note: 'each is deleted for good 30 days after it was trashed; restore any until then', trash: true };
    case 'vault':
      return v.deleted
        ? { kicker: 'Vault', title: 'Deleted', note: 'each is deleted for good 30 days after; restore any until then', vault: 'deleted' }
        : { kicker: 'Library', title: 'Vault', note: 'only you see these, and only while it’s unlocked', vault: 'open' };
    case 'label':
      if (v.label === 'adult') return { kicker: 'Utilities', title: 'Adult', note: 'nudity or sex, found by Photostead; take out any that don’t belong', adult: 'adult' };
      if (v.label === 'suggestive') return { kicker: 'Utilities', title: 'Adult', note: 'suggestive: lingerie, swimwear or poses, neither nude nor explicit', adult: 'suggestive' };
      return { kicker: 'Utilities', title: labelInfo(v.label)!.title, note: 'recognized by Photostead; take out any that don’t belong' };
    case 'ai':
      return { kicker: 'Utilities', title: 'AI-generated', note: 'made or edited by a generative model, by the evidence in each file' };
    case 'captured':
      return { kicker: 'Utilities', title: 'Captured by me', note: 'photos and videos with a camera’s or phone’s details' };
    case 'place': {
      const p = (await api.places()).places.find((x) => x.id === v.id);
      if (!p) throw new ApiError(404, 'No such place.');
      return { kicker: 'Places', title: p.name, note: [p.region !== p.name ? p.region : '', p.country].filter(Boolean).join(', '),
        back: { kind: 'places' }, backLabel: 'Places' };
    }
    case 'album': {
      const [a, inside] = await Promise.all([api.album(v.id), api.albums({ parent: v.id })]);
      const span = formatSpan(a.firstDate, a.lastDate);
      const up = a.path?.[a.path.length - 1];
      return { kicker: a.kind === 'smart' ? 'Smart album' : 'Album', title: a.title,
        note: [span, a.description].filter(Boolean).join(' · '),
        back: up ? { kind: 'album', id: up.id } : { kind: 'albums' }, backLabel: up ? up.name : 'Albums',
        crumbs: a.path?.length ? [{ label: 'Albums', view: { kind: 'albums' } }, ...a.path.map((p) => ({ label: p.name, view: { kind: 'album' as const, id: p.id } }))] : undefined,
        album: a, subAlbums: inside.albums };
    }
    case 'folder': {
      const f = await api.folder(v.library, v.path);
      const parts = v.path ? v.path.split('/') : [];
      const crumbs = [{ label: 'Folders', view: { kind: 'folders' } as View }, ...(parts.length ? [{ label: f.library.name, view: { kind: 'folder', library: v.library, path: '' } as View }] : []),
        ...parts.slice(0, -1).map((p, i) => ({ label: p, view: { kind: 'folder', library: v.library, path: parts.slice(0, i + 1).join('/') } as View }))];
      const up = crumbs[crumbs.length - 1];
      return { kicker: 'Folder', title: parts.length ? parts[parts.length - 1] : f.library.name,
        note: f.library.readOnly ? 'read-only: browsing here changes nothing' : 'in Photostead’s share',
        back: up.view, backLabel: up.label, crumbs, folder: f };
    }
    case 'person': {
      const p = await api.person(v.id);
      const note = p.kind === 'pet' ? 'recognized in these photos; name them to confirm'
        : p.count === 0 || p.faceItems === p.count ? 'recognized by face'
        : p.faceItems === 0 ? 'suggested by an import, until face recognition looks at them'
        : `${p.faceItems.toLocaleString()} recognized by face, the rest suggested by an import`;
      return { kicker: p.hidden ? `${personKind(p)} · hidden` : personKind(p), title: personName(p), note, back: { kind: 'people' }, backLabel: 'People', person: p };
    }
    case 'prompt': {
      const p = await api.prompt(v.id);
      const used = [p.models.slice(0, 2).join(', '), p.loras.slice(0, 3).join(', ')].filter(Boolean).join(' · ');
      return { kicker: 'Prompt library', title: 'Prompt', body: p.text, note: [formatSpan(p.firstDate, p.lastDate), used].filter(Boolean).join(' · '),
        back: { kind: 'prompts' }, backLabel: 'Prompt library' };
    }
    case 'character': {
      const c = await api.character(v.id);
      return { kicker: 'Character', title: c.name, note: [formatSpan(c.firstDate, c.lastDate), c.loras.slice(0, 3).join(', ')].filter(Boolean).join(' · '),
        back: { kind: 'characters' }, backLabel: 'Characters', character: c };
    }
    case 'model':
    case 'lora': {
      const m = await api.model(v.id);
      const span = formatSpan(m.firstDate, m.lastDate);
      return { kicker: m.kind === 'lora' ? 'LoRA' : 'Model', title: m.name,
        note: [span, m.with && `${m.kind === 'lora' ? 'mostly with' : 'often with'} ${m.with}`].filter(Boolean).join(' · '),
        back: { kind: 'models' }, backLabel: 'Models & LoRAs' };
    }
    default:
      return null;
  }
}

/** Applies a theme to the page: a fixed light or dark, or following the system. */
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === 'system') root.removeAttribute('data-theme');
  else root.dataset.theme = theme;
}

function ZoneDialog({ user, onClose, onSaved }: { user: User; onClose: () => void; onSaved: (u: User) => void }) {
  const [zone, setZone] = useState(user.timeZone);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (zone === user.timeZone) return onClose();
    setBusy(true);
    try {
      onSaved((await api.setTimeZone(zone)).user);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.');
      setBusy(false);
    }
  };
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="zone-title">
      <form onSubmit={save}>
        <h2 id="zone-title">Time zone</h2>
        <p className="lede">Days and times in your library follow this zone. Photos that record where they were taken keep that local time.</p>
        <TimeZonePicker value={zone} onChange={setZone} />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </dialog>
  );
}

function Mark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="mark">
      <path d="M3.5 10.6 12 3.6l8.5 7v9.1a.8.8 0 0 1-.8.8H4.3a.8.8 0 0 1-.8-.8z" />
      <circle cx="12" cy="14.2" r="3.3" />
    </svg>
  );
}
