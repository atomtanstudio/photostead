import type { AdultMode, Counts, ServerStatus, User } from './api';
import { Icon } from './icons';
import { ADULT_MODE_LABEL, ADULT_MODE_TITLE, LABELS } from './utility';
import type { View } from './views';

export type Theme = 'system' | 'light' | 'dark';
export const THEMES: Theme[] = ['system', 'light', 'dark'];
const THEME_LABEL: Record<Theme, string> = { system: 'System', light: 'Light', dark: 'Dark' };

type Props = {
  user: User;
  view: View;
  searching: boolean;
  counts: Counts | null;
  immichConnected: boolean;
  collapsed: boolean;
  theme: Theme;
  status: ServerStatus | null;
  onView: (v: View) => void;
  onToggle: () => void;
  onImport: () => void;
  onUpload: (folder: boolean) => void;
  onTheme: () => void;
  onTimeZone: () => void;
  onSignOut: () => void;
  hideUtility: boolean;
  onHideUtility: () => void;
  adultMode: AdultMode;
  onAdultMode: () => void; // the next of shown, blurred, hidden
  organize: boolean;
  onOrganize: () => void;
  memories: boolean; // shown above the timeline
  onMemories: () => void;
  onSecurity: () => void;
  onDevices: () => void;
  onHousehold: () => void;
  onBackups: () => void;
  setupLeft: number; // steps of the setup checklist left, while it's put away (the admin's)
  onSetup: () => void;
  healthLook: number | null; // checks needing a look (the admin's); null: not looked yet, or not the admin
  onHealth: () => void;
};

type Ico = () => React.JSX.Element;

// What the rail will hold once the features behind it exist, and what each needs.
const COMING: { icon: Ico; label: string; why: string }[] = [];

// Which rail item a view belongs to: an album belongs to Albums, a person to People.
const section = (v: View) => {
  switch (v.kind) {
    case 'album':
      return 'albums';
    case 'folder':
      return 'folders';
    case 'person':
      return 'people';
    case 'place':
      return 'places';
    case 'prompt':
      return 'prompts';
    case 'character':
      return 'characters';
    case 'model':
    case 'lora':
      return 'models';
    case 'timeline':
      return `timeline:${v.type}`;
    case 'label':
      return v.label === 'suggestive' ? 'label:adult' : `label:${v.label}`; // one rail item, a switch on its page
  }
  return v.kind;
};

/** The left rail: sections, settings and what the server is doing. Collapses to icons ([). */
export function Rail(p: Props) {
  const { user, view, searching, counts, collapsed, theme, status } = p;
  const current = searching ? '' : section(view);
  // Adult and suggestive items, once any are found (the switch is there to change it back, too).
  const adultSeen = Boolean(counts && (counts.labels.adult || counts.labels.suggestive || p.adultMode !== 'show'));
  const item = (v: View, icon: Ico, label: string, count?: number) => {
    const I = icon;
    return (
      <button key={section(v)} type="button" className="r-item" aria-current={current === section(v) ? 'page' : undefined} onClick={() => p.onView(v)} title={label}>
        <I />
        <span>{label}</span>
        {count !== undefined && <em>{count.toLocaleString()}</em>}
      </button>
    );
  };
  const soon = (icon: Ico, label: string, why: string) => {
    const I = icon;
    return (
      <button key={label} type="button" className="r-item soon" aria-disabled="true" title={`${label}: ${why}`}>
        <I />
        <span>{label}</span>
        <i>soon</i>
      </button>
    );
  };
  const ThemeIcon = Icon[theme];
  const toggleLabel = `${collapsed ? 'Expand' : 'Collapse'} the sidebar ([)`;
  return (
    <aside className="rail" aria-label="Library">
      <div className="r-head">
        <span className="r-brand">
          <Icon.mark />
          <span>Photostead</span>
        </span>
        <button type="button" className="r-x" onClick={p.onToggle} aria-expanded={!collapsed} aria-label={toggleLabel} title={toggleLabel}>
          <Icon.panelL />
        </button>
      </div>
      <nav className="r-nav" aria-label="Sections">
        <div className="r-sec">
          <h3>Library</h3>
          {item({ kind: 'timeline', type: '' }, Icon.photos, 'Timeline', counts?.all)}
          {item({ kind: 'timeline', type: 'image' }, Icon.image, 'Photos', counts?.images)}
          {item({ kind: 'timeline', type: 'video' }, Icon.film, 'Videos', counts?.videos)}
          {item({ kind: 'recent' }, Icon.clock, 'Recently added')}
          {item({ kind: 'favorites' }, Icon.heart, 'Favorites', counts?.favorites)}
          {item({ kind: 'archive' }, Icon.archive, 'Archive', counts?.archived)}
          {item({ kind: 'trash' }, Icon.trash, 'Trash', counts?.trash)}
          {item({ kind: 'vault' }, Icon.lock, 'Vault')}
        </div>
        <div className="r-sec">
          <h3>Browse</h3>
          {item({ kind: 'people' }, Icon.person, 'People', counts?.people)}
          {item({ kind: 'characters' }, Icon.spark, 'Characters', counts?.characters)}
          {item({ kind: 'albums' }, Icon.album, 'Albums', counts?.albums)}
          {item({ kind: 'folders' }, Icon.folder, 'Folders')}
          {item({ kind: 'places' }, Icon.pin, 'Places')}
        </div>
        {counts && (LABELS.some((l) => counts.labels[l.name]) || counts.ai > 0 || counts.captured > 0 || counts.lookAlikes > 0 || adultSeen) && (
          <div className="r-sec">
            <h3>Utilities</h3>
            {LABELS.filter((l) => counts.labels[l.name]).map((l) => item({ kind: 'label', label: l.name }, l.icon, l.title, counts.labels[l.name]))}
            {counts.captured > 0 && item({ kind: 'captured' }, Icon.camera, 'Captured by me', counts.captured)}
            {counts.ai > 0 && item({ kind: 'ai' }, Icon.wand, 'AI-generated', counts.ai)}
            {counts.lookAlikes > 0 && item({ kind: 'lookalikes' }, Icon.layers, 'Look-alikes', counts.lookAlikes)}
            {LABELS.some((l) => counts.labels[l.name]) && (
              <button type="button" className="r-item" aria-pressed={p.hideUtility} onClick={p.onHideUtility}
                title={p.hideUtility ? 'Screenshots, documents, receipts, IDs and QR codes are kept out of the timeline. Click to show them' : 'Keep screenshots, documents, receipts, IDs and QR codes out of the timeline'}>
                {p.hideUtility ? <Icon.eyeOff /> : <Icon.eye />}
                <span>In the timeline</span>
                <em>{p.hideUtility ? 'hidden' : 'shown'}</em>
              </button>
            )}
            {adultSeen && item({ kind: 'label', label: 'adult' }, Icon.adult, 'Adult', counts.labels.adult ?? 0)}
            {adultSeen && (
              <button type="button" className="r-item" onClick={p.onAdultMode} title={ADULT_MODE_TITLE[p.adultMode]} data-testid="adult-mode">
                {p.adultMode === 'show' ? <Icon.eye /> : <Icon.eyeOff />}
                <span>Adult content</span>
                <em>{ADULT_MODE_LABEL[p.adultMode]}</em>
              </button>
            )}
          </div>
        )}
        <div className="r-sec">
          <h3>Create</h3>
          {item({ kind: 'prompts' }, Icon.quote, 'Prompt library', counts?.prompts)}
          {item({ kind: 'models' }, Icon.layers, 'Models & LoRAs', counts?.models)}
        </div>
        {COMING.length > 0 && (
          <div className="r-sec">
            <h3>Coming next</h3>
            {COMING.map((c) => soon(c.icon, c.label, c.why))}
          </div>
        )}
        <div className="r-sec r-sec-end">
          <button type="button" className="r-item" onClick={(e) => p.onUpload(e.altKey)} data-testid="upload"
            title="Add photos and videos from this computer (Option-click for a whole folder). Or drop them anywhere on the page">
            <Icon.upload />
            <span>Upload</span>
          </button>
          <button type="button" className="r-item" onClick={p.onImport} data-testid="import"
            title="Bring photos in from Google Photos, Apple Photos, Immich or any folder">
            <Icon.importIn />
            <span>Import</span>
            {p.immichConnected && <b className="r-dot" aria-label="Connected" />}
          </button>
          {p.setupLeft > 0 && (
            <button type="button" className="r-item" onClick={p.onSetup} title="The setup checklist: what’s left to make Photostead ready for your household">
              <Icon.check />
              <span>Get started</span>
              <em>{p.setupLeft} left</em>
            </button>
          )}
          <button type="button" className="r-item" onClick={p.onTheme} title={`Appearance: ${THEME_LABEL[theme]}. Click to change`}>
            <ThemeIcon />
            <span>Appearance</span>
            <em>{THEME_LABEL[theme]}</em>
          </button>
          <button type="button" className="r-item" aria-pressed={p.organize} onClick={p.onOrganize}
            title={p.organize ? 'Star ratings, pick/reject flags and color labels are on (keys 0–5, P, X, U, 6–9). Click to turn them off' : 'Turn on star ratings, pick/reject flags and color labels, for culling and organizing'}>
            <Icon.star />
            <span>Ratings & flags</span>
            <em>{p.organize ? 'on' : 'off'}</em>
          </button>
          <button type="button" className="r-item" onClick={p.onMemories} title="Memories: on this day, and people over the years. Choose what’s left out">
            <Icon.history />
            <span>Memories</span>
            <em>{p.memories ? 'on' : 'off'}</em>
          </button>
          <button type="button" className="r-item" onClick={p.onTimeZone} title={`Time zone: ${user.timeZone}`}>
            <Icon.clock />
            <span>Time zone</span>
            <em>{user.timeZone.split('/').pop()!.replace(/_/g, ' ')}</em>
          </button>
          <button type="button" className="r-item" onClick={p.onHousehold} title="Who’s in the household, inviting someone, and what you share">
            <Icon.household />
            <span>Household</span>
          </button>
          <button type="button" className="r-item" onClick={p.onDevices} title="Reach Photostead from anywhere, pair a phone, and see where you’re signed in">
            <Icon.phone />
            <span>Devices & remote access</span>
          </button>
          {user.role === 'admin' && (
            <button type="button" className="r-item" onClick={p.onHealth} data-testid="health"
              title={p.healthLook ? 'Something on the server needs a look: what, and what to do' : 'The photo share, disk space, memory, folders, AI work, backups and remote access'}>
              <Icon.system />
              <span>Health</span>
              {p.healthLook ? <em className="r-warn">{p.healthLook} to look at</em> : p.healthLook === 0 ? <em>fine</em> : null}
            </button>
          )}
          {user.role === 'admin' && (
            <button type="button" className="r-item" onClick={p.onBackups} title={p.status?.backup || 'Nightly copies of the database: albums, people, sharing, descriptions and settings'}>
              <Icon.database />
              <span>Backups</span>
              {p.status?.backup ? <em className="r-warn">check</em> : <em>nightly</em>}
            </button>
          )}
          <button type="button" className="r-item" onClick={p.onSecurity} title="Two-step sign-in, passkeys and single sign-on">
            <Icon.shield />
            <span>Sign-in & security</span>
          </button>
          <button type="button" className="r-item" onClick={p.onSignOut} title={`Sign out ${user.email}`}>
            <Icon.person />
            <span>Sign out</span>
          </button>
        </div>
      </nav>
      <StatusLine status={status} />
    </aside>
  );
}

function StatusLine({ status }: { status: ServerStatus | null }) {
  const w = status?.watching;
  let text = w && w.libraries > 0 ? 'Watching for new files' : 'Up to date';
  let busy = false;
  const m = status?.move;
  const warn = !!status?.backup;
  if (status?.backup) {
    text = 'Backup needs a look';
  } else if (status?.import) {
    text = `Importing from Immich${status.import.step ? ` · ${status.import.step}` : ''}`;
    busy = true;
  } else if (m && m.done + m.failed < m.files) {
    const pct = m.bytes ? Math.floor((100 * m.bytesDone) / m.bytes) : 0;
    text = `Copying to the share · ${pct}%`;
    busy = true;
  } else if (status && status.indexing > 0) {
    text = `Indexing · ${status.indexing.toLocaleString()} files`;
    busy = true;
  }
  const lines = [text];
  if (m) lines.push(`Move: ${m.done.toLocaleString()} of ${m.files.toLocaleString()} files copied${m.failed ? `, ${m.failed} failed` : ''}`);
  if (w && w.libraries > 0) {
    lines.push(`Watching ${w.libraries} ${w.libraries === 1 ? 'library' : 'libraries'} (${w.folders.toLocaleString()} folders) for new files. New photos show up on their own; everything is rescanned nightly too.`);
  }
  if (w?.problem) lines.push(w.problem);
  if (status?.backup) lines.push(`${status.backup} See Backups.`);
  return (
    <div className={`r-foot${busy ? ' busy' : ''}${warn ? ' warn' : ''}`} title={lines.join('\n')} role="status">
      <i />
      <span>{text}</span>
    </div>
  );
}
