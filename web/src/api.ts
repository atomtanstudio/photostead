// Typed client for the Photostead API (/api/v1). Types mirror the server's OpenAPI document.

/** Adult content (nudity or sex): shown, blurred until clicked, or hidden everywhere but its own collection. */
export type AdultMode = 'show' | 'blur' | 'hide';

/** A step of the setup checklist (Setup.tsx). */
export type SetupSkip = 'photos' | 'phone' | 'away' | 'household' | 'copy';

export type User = {
  id: string;
  email: string;
  username: string;
  displayName: string;
  role: string;
  timeZone: string;
  timeZoneChosen: boolean;
  settings?: { organize?: boolean; hideMemories?: boolean; memoriesAi?: boolean; adult?: AdultMode; setupHidden?: boolean; setupSkipped?: SetupSkip[] };
};

export type Keyword = { name: string; items: number };

export type Color = 'red' | 'yellow' | 'green' | 'blue' | 'purple';
export type Flag = 'pick' | 'reject';

/** The viewer's own stars, flag and color label on an item. */
export type Marks = { rating?: number; flag?: Flag; label?: Color };

/** A change to marks: stars 0 (none) to 5, a flag or 'none', a color or 'none'. */
export type MarkChange = { rating?: number; flag?: Flag | 'none'; label?: Color | 'none' };

/** Narrowing a timeline by marks: at least this many stars, a flag, a color. */
export type MarkFilter = { rating?: number; flag?: Flag | 'unrejected'; label?: Color };

export type Bucket = { month: string; count: number };

/** '' for everything, or only photos or only videos. */
export type MediaType = '' | 'image' | 'video';

/** What a timeline shows: the library (by media type), or a collection: favorites, archive, album:{id} or person:{id}. */
export type Scope = { type: MediaType; collection?: string; folder?: string; hideUtility?: boolean; marks?: MarkFilter; whose?: Whose };
/** Whose items the timeline shows: everything, the viewer's own, or the Household's. */
export type Whose = '' | 'mine' | 'household';

export type Stack = {
  id: string;
  kind: 'raw_jpeg' | 'burst' | 'near_duplicate' | 'ai_batch' | 'manual';
  state: 'suggested' | 'confirmed';
  cover: string;
  members: TimelineAsset[];
};

export type Library = { id: string; name: string; kind: 'managed' | 'indexed'; readOnly: boolean; items: number };
export type Folder = { name: string; path: string; items: number };

export type ImportRun = {
  state: 'running' | 'done' | 'failed';
  step?: string;
  startedAt: string;
  finishedAt?: string;
  summary?: ImportSummary;
  error?: string;
};

export type ServerStatus = {
  indexing: number;
  move?: { files: number; done: number; failed: number; bytes: number; bytesDone: number };
  import?: ImportRun;
  watching?: { libraries: number; folders: number; since?: string; problem?: string };
  search?: MeaningStatus;
  backup?: string; // for the admin: the last backup failed, or none for a day and a half
};

export type BackupCopy = { name: string; bytes: number; at: string };
/** One thing Health looks at on the server: fine, worth a look, or needing fixing, with what to do. */
export type HealthCheck = { id: 'share' | 'space' | 'memory' | 'folders' | 'ai' | 'backups' | 'remote'; state: 'ok' | 'warn' | 'problem'; title: string; detail: string; fix?: string };
export type Backups = {
  on: boolean;
  dir: string;
  keep: number;
  status: { last?: BackupCopy; seconds?: number; error?: string; errorAt?: string };
  problem?: string;
  copies: BackupCopy[];
  running: boolean;
  nextAt: string;
  files: {
    files: number; // with a hash to check against
    checked: number; // of those, in the last 30 days
    damaged: { library: string; path: string; at: string }[];
    last: { at?: string; seconds?: number; checked?: number; bytes?: number; damaged?: number; missing?: number; changed?: number; finished?: boolean };
    running: boolean;
    nextAt: string;
  };
};

export type MeaningStatus = { available: boolean; embedded: number; waiting: number; textRead: number; textWaiting: number };

export type SearchMode = 'auto' | 'meaning' | 'names';

/** A question's plan, as the server made it: sent back with a follow-up or with filters to drop. */
export type AskPlan = Record<string, unknown> & { intent: string; text?: string };
export type AskChip = { key: string; kind: string; label: string };
export type AskResult = {
  plan: AskPlan;
  chips: AskChip[];
  answer: string;
  describe: string;
  assets: TimelineAsset[];
  total: number;
  exact: boolean;
  order: 'date' | 'relevance';
  truncated: boolean;
  notes: string[];
  alt?: { remove: string; count: number; describe: string };
};

export type SearchResult = {
  query: string;
  mode: 'meaning' | 'names' | 'similar';
  order: 'relevance' | 'date';
  assets: TimelineAsset[];
  truncated: boolean;
  meaning: MeaningStatus;
  found?: { meaning: number; prompts: number; text: number; details?: number; names: number };
};

export type Counts = {
  all: number;
  images: number;
  videos: number;
  favorites: number;
  archived: number;
  albums: number;
  people: number;
  prompts: number;
  models: number;
  characters: number;
  ai: number;
  captured: number;
  labels: Partial<Record<string, number>>;
  trash: number;
  lookAlikes: number;
  adultScan?: number; // items still waiting to be looked at for adult content
};

export type Character = {
  id: string;
  name: string;
  patterns: string[];
  count: number;
  firstDate?: string;
  lastDate?: string;
  samples: Sample[];
  loras: string[];
  /** Groups of faces from People that are this character. */
  faceGroups: number;
};

export type Sample = { id: string; thumbhash?: string };

export type Prompt = {
  id: string;
  text: string;
  uses: number;
  firstDate?: string;
  lastDate?: string;
  samples: Sample[];
  models: string[];
  loras: string[];
};

export type AIModel = {
  id: string;
  kind: 'model' | 'lora';
  name: string;
  uses: number;
  firstDate?: string;
  lastDate?: string;
  samples: Sample[];
  with?: string;
};

/** A smart album's rules: every rule left out matches everything. */
export type SmartRules = {
  type?: 'image' | 'video';
  favorite?: boolean;
  rating?: number;
  flag?: Flag | 'unrejected';
  label?: Color;
  people?: string[];
  keywords?: string[];
  after?: string; // YYYY-MM-DD
  before?: string;
  source?: 'ai' | 'not_ai' | 'camera';
};

export type Album = {
  id: string;
  title: string;
  description?: string;
  kind: 'manual' | 'smart';
  rules?: SmartRules;
  parent?: string;
  path?: Ref[]; // the albums it's inside, outermost first
  children?: number; // albums inside it
  count: number;
  firstDate?: string;
  lastDate?: string;
  cover?: string;
  coverThumbhash?: string;
  role: 'owner' | 'contributor' | 'viewer'; // what you may do with it
  owner?: { id: string; name: string }; // whose it is, when it's shared with you
  members?: number; // people it's shared with (yours)
};

/** Someone an album is (or could be) shared with. */
export type AlbumPerson = { id: string; name: string; email: string; guest?: boolean; role?: 'viewer' | 'contributor'; link?: PersonalLink };

/** A guest's personal link, as whoever made it sees it. */
export type PersonalLink = { url: string; public: boolean; devices: number; maxDevices: number; opened: boolean; lastSeenAt: string };

export type Person = {
  id: string;
  /** Empty for someone face recognition found whom nobody has named yet. */
  name: string;
  birthDate?: string;
  count: number;
  /** Items where their face was recognized; the rest are an import's suggestions for items not yet looked at. */
  faceItems: number;
  origin: 'immich' | 'faces' | 'user';
  kind: 'human' | 'pet';
  /** For a pet. */
  species?: 'cat' | 'dog';
  hidden?: boolean;
  /** Left out of memories, with every item they're in. */
  keptOutOfMemories?: boolean;
  coverFace?: string;
  cover?: string;
  coverThumbhash?: string;
};

/** A card in the strip above the timeline: a day in an earlier year, or someone over the years. */
export type Memory = {
  key: string;
  kind: 'on_this_day' | 'person';
  title: string;
  subtitle: string;
  from: string; // the days it remembers
  to: string;
  person?: Pick<Person, 'id' | 'name' | 'kind' | 'species'>;
  count: number;
  assets: TimelineAsset[];
};

export type HiddenDays = { id: string; from: string; to: string };

/** A share link, as its owner sees it. */
export type ShareLink = {
  id: string;
  url: string;
  qr: string; // the address as a QR code (PNG data URL)
  public: boolean; // works from the internet (Funnel on, or a public address)
  hasPassword: boolean;
  expiresAt?: string;
  allowDownload: boolean;
  allowUpload: boolean;
  views: number;
  lastViewedAt?: string;
  createdAt: string;
};
export type LinkSettings = { password?: string; clearPassword?: boolean; expiresAt?: string; allowDownload?: boolean; allowUpload?: boolean };
/** A shared album, as a visitor sees it. */
export type SharedAlbum = { title: string; description?: string; owner: string; needsPassword: boolean; allowDownload: boolean; allowUpload: boolean; expiresAt?: string };

/** The household as one member sees it. */
export type Household = {
  id: string;
  name: string;
  role: 'owner' | 'adult' | 'member';
  canManage: boolean;
  shared: number;
  members: { id: string; name: string; email: string; role: 'owner' | 'adult' | 'member'; joinedAt: string; shared: number; me?: boolean }[];
  invites: { id: string; name?: string; email?: string; role: 'adult' | 'member'; expiresAt: string }[];
  guests: Guest[]; // only for the server's admin, who can remove them
  rule: ShareRule;
};
/** Someone albums are shared with from outside the household, as the server's admin sees them. */
export type Guest = { id: string; name: string; email?: string; addedBy?: string; addedAt: string; albums: number; devices: number };
/** A person's auto-share rule: their items from a day on (or all), with these people (or anyone). */
export type ShareRule = { enabled: boolean; from?: string; people?: string[]; includeAi?: boolean };

/** How the signed-in person signs in. */
export type Security = {
  hasPassword: boolean;
  twoStep: boolean;
  recoveryLeft: number;
  passkeys: { id: string; name: string; createdAt: string; lastUsedAt?: string }[];
  signOn?: string; // the single sign-on provider's name, when the server has one
  signOnLinks: { id: string; email: string }[];
};

type PasskeyOptions = { flow: string; options: { publicKey: Record<string, unknown> } };

/** Where Photostead can be reached. */
export type Addresses = { home?: string; tailnet?: string; public?: string };
export type TailscaleState = { status: 'off' | 'starting' | 'needs-login' | 'running' | 'error'; loginUrl?: string; url?: string; hostname: string; error?: string; funnel: boolean; funnelError?: string; devices: boolean };
export type Remote = { tailscale: TailscaleState; available: boolean; addresses: Addresses };
/** A browser or paired device signed in to the account. */
export type SignedIn = { id: string; current: boolean; device?: string; platform?: string; userAgent?: string; createdAt: string; lastSeenAt: string };
/** A pairing code that lasts, as the admin sees it (the code itself only once, when it's made), with its note ('' for none). */
export type LastingCode = { id: string; userId: string; name: string; email: string; devices: number; maxDevices: number; note: string; createdAt: string; expiresAt: string };
/** A pairing code as it's shown: the code, its link (the server's id and addresses) and that as a QR code. */
export type Pairing = { code: string; expiresAt: string; link: string; qr: string };

export type ImportSummary = {
  immichVersion: string;
  dryRun?: boolean;
  items: number;
  matched: number;
  byPath: number;
  byContent: number;
  unmatched: number;
  unmatchedExamples?: string[];
  folders: string[] | null;
  favorites: number;
  favoritesSet: number;
  favoritesOff: number;
  archived: number;
  archivedSet: number;
  archivedOff: number;
  albums: number;
  albumsNew: number;
  albumItems: number;
  albumsMissing: number;
  people: number;
  peopleNew: number;
  peopleItems: number;
  descriptions?: number;
  descriptionsSet?: number;
  ratings?: number;
  ratingsSet?: number;
  tags?: number;
  tagItems?: number;
  skipped?: string[];
};

export type ImmichCheck = { version: string; items: string; albums: string; people: string; tags?: string };

export type ImmichImport = { connected: boolean; url?: string; run?: ImportRun };

export type TimelineAsset = {
  id: string;
  type: 'image' | 'video';
  takenAt: string;
  localDate: string;
  width: number;
  height: number;
  durationMs?: number;
  thumbhash?: string;
  render: 'pending' | 'done' | 'failed';
  motionId?: string; // a Live Photo's motion clip
  stackSize?: number; // the top item of a stack (RAW+JPEG)
  favorite?: boolean;
  ai?: 'generated' | 'edited'; // only with evidence in the file
  shared?: boolean; // in the Household space
  fromOthers?: boolean; // another member's, shared with the Household
  adult?: boolean; // adult content (nudity or sex), by a scan or its owner
  copy?: boolean; // a video with a copy every browser plays (playURL): smaller, for previews
} & Marks;

/** An item in Recently added: as the timeline shows it, with when it came into Photostead. */
export type RecentAsset = TimelineAsset & { addedAt: string };

export type Generation = {
  kind: 'generated' | 'edited';
  source: 'comfyui' | 'a1111' | 'midjourney' | 'grok' | 'aigc' | 'c2pa' | 'iptc';
  evidence: string;
  tool?: string;
  toolVersion?: string;
  model?: string;
  modelId?: string;
  loras: { id: string; name: string; weight?: number }[];
  prompt?: string;
  promptId?: string;
  promptUses?: number;
  negative?: string;
  seed?: string;
  sampler?: string;
  scheduler?: string;
  steps?: number;
  cfg?: number;
  width?: number;
  height?: number;
  settings?: Record<string, unknown>;
  hasWorkflow: boolean;
  incomplete?: boolean;
  from?: Sample; // an image-to-video clip's start image
};

export type Ref = { id: string; name: string; source?: string; kind?: 'human' | 'pet' };

export type AssetDetail = TimelineAsset & {
  owner?: { id: string; name: string }; // another member's item
  mime?: string;
  offsetMin?: number;
  dateSource: string;
  sourceType: string;
  renderError?: string;
  fileName: string;
  folderPath: string;
  library: string;
  fileSize: number;
  camera?: { make?: string; model?: string; lens?: string; focalMm?: number; fNumber?: number; exposureS?: number; iso?: number };
  location?: { lat: number; lon: number; place?: string; placeId?: string };
  title?: string;
  description?: string;
  archived?: boolean;
  albums: Ref[];
  people: Ref[];
  characters: Ref[];
  generation?: Generation;
  text?: string;
  labels: string[];
  video?: VideoPlayback; // for a video, once probed
  trashed?: boolean;
  trashedAt?: string;
  keywords?: string[];
  edited?: boolean; // it has an edit; the original is as it was
};

/** A photo's edit: turned, mirrored, cropped (in the turned picture, as shares of it), light and color. */
export type Recipe = {
  rotate: 0 | 90 | 180 | 270;
  flip?: boolean;
  crop?: { x: number; y: number; w: number; h: number };
  exposure?: number; // stops, -2 to 2
  contrast?: number; // -1 to 1
  saturation?: number; // -1 (black and white) to 1
  warmth?: number; // -1 to 1
};

export type PhotoEdit = { recipe: Recipe | null; origWidth: number; origHeight: number; width: number; height: number; updatedAt?: string };

/** The vault as this session sees it. Counts come only while it's unlocked. */
export type VaultState = {
  hasPin: boolean;
  unlocked: boolean;
  unlockedUntil?: string;
  waitUntil?: string; // after too many wrong PINs
  items?: number;
  deleted?: number;
};

export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    method,
    signal,
    credentials: 'same-origin',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const err = await res.json();
      message = err.detail || err.title || message;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, message);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

export const api = {
  setupStatus: () => request<{ needed: boolean; serverZone: string; signOn?: string; internet?: boolean }>('GET', '/api/v1/setup'),
  setup: (body: { displayName: string; email: string; password: string; timeZone: string }) => request<User>('POST', '/api/v1/setup', body),
  setTimeZone: (timeZone: string) =>
    request<{ user: User; cameraClocksMoved: number; daysChanged: number }>('PUT', '/api/v1/auth/me/time-zone', { timeZone }),
  me: () => request<User>('GET', '/api/v1/auth/me'),
  /** A user, or (two-step sign-in on) a ticket for the code that comes next. */
  login: (email: string, password: string) => request<{ user?: User; twoStep?: boolean; ticket?: string }>('POST', '/api/v1/auth/login', { email, password }),
  secondStep: (ticket: string, code: string) => request<User>('POST', '/api/v1/auth/login/two-step', { ticket, code }),
  passkeySignInBegin: () => request<PasskeyOptions>('POST', '/api/v1/auth/passkey/begin'),
  passkeySignInFinish: (flow: string, credential: unknown) => request<User>('POST', '/api/v1/auth/passkey/finish', { flow, credential }),
  security: () => request<Security>('GET', '/api/v1/auth/security'),
  twoStepSetup: (password: string) => request<{ secret: string; uri: string; qr: string }>('POST', '/api/v1/auth/two-step/setup', { password }),
  twoStepConfirm: (code: string) => request<{ recoveryCodes: string[] }>('POST', '/api/v1/auth/two-step/confirm', { code }),
  twoStepRecovery: (password: string) => request<{ recoveryCodes: string[] }>('POST', '/api/v1/auth/two-step/recovery', { password }),
  twoStepOff: (password: string) => request<void>('POST', '/api/v1/auth/two-step/off', { password }),
  passkeyAddBegin: (password: string) => request<PasskeyOptions>('POST', '/api/v1/auth/passkeys/begin', { password }),
  passkeyAddFinish: (flow: string, name: string, credential: unknown) => request<unknown>('POST', '/api/v1/auth/passkeys/finish', { flow, name, credential }),
  removePasskey: (id: string) => request<void>('DELETE', `/api/v1/auth/passkeys/${id}`),
  linkSignOn: (password: string) => request<{ url: string }>('POST', '/api/v1/auth/oidc/link', { password }),
  unlinkSignOn: (id: string) => request<void>('DELETE', `/api/v1/auth/oidc/${id}`),
  remote: () => request<Remote>('GET', '/api/v1/remote'),
  setFunnel: (on: boolean) => request<Remote>('POST', '/api/v1/remote/funnel', { on }),
  setFunnelDevices: (on: boolean) => request<Remote>('POST', '/api/v1/remote/funnel/devices', { on }),
  albumLinks: (albumId: string) => request<{ links: ShareLink[] }>('GET', `/api/v1/albums/${albumId}/links`),
  albumPeople: (albumId: string) => request<{ members: AlbumPerson[]; candidates: AlbumPerson[]; invites: { id: string; name?: string; role: string; expiresAt: string }[] }>('GET', `/api/v1/albums/${albumId}/people`),
  shareAlbumWith: (albumId: string, userId: string, role: 'viewer' | 'contributor') => request<unknown>('PUT', `/api/v1/albums/${albumId}/people/${userId}`, { role }),
  unshareAlbumWith: (albumId: string, userId: string) => request<unknown>('DELETE', `/api/v1/albums/${albumId}/people/${userId}`),
  cancelGuestInvite: (albumId: string, inviteId: string) => request<unknown>('DELETE', `/api/v1/albums/${albumId}/invites/${inviteId}`),
  shareWithSomeoneNew: (albumId: string, name: string) =>
    request<{ members: AlbumPerson[]; candidates: AlbumPerson[]; invites: { id: string; name?: string; role: string; expiresAt: string }[] }>('POST', `/api/v1/albums/${albumId}/people`, { name }),
  signOutLinkDevices: (guestId: string) => request<PersonalLink>('DELETE', `/api/v1/guests/${guestId}/devices`),
  replacePersonalLink: (guestId: string) => request<PersonalLink>('POST', `/api/v1/guests/${guestId}/link`),
  openPersonalLink: (token: string) => request<User>('POST', `/api/v1/public/personal/${token}`),
  inviteGuest: (albumId: string, body: { name?: string; role: 'viewer' | 'contributor' }) => request<{ token: string; url: string; public: boolean; expiresAt: string }>('POST', `/api/v1/albums/${albumId}/guests`, body),
  newLink: (albumId: string, s: LinkSettings) => request<ShareLink>('POST', `/api/v1/albums/${albumId}/links`, s),
  editLink: (id: string, s: LinkSettings) => request<ShareLink>('PATCH', `/api/v1/links/${id}`, s),
  deleteLink: (id: string) => request<void>('DELETE', `/api/v1/links/${id}`),
  sharedAlbum: (token: string) => request<SharedAlbum>('GET', `/api/v1/public/links/${token}`),
  unlockShared: (token: string, password: string) => request<void>('POST', `/api/v1/public/links/${token}/unlock`, { password }),
  sharedItems: (token: string) => request<{ items: (Omit<TimelineAsset, 'render'> & { thumbhash?: string })[] }>('GET', `/api/v1/public/links/${token}/items`),
  setTailscale: (on: boolean, hostname?: string) => request<Remote>('POST', '/api/v1/remote/tailscale', { on, hostname }),
  forgetTailscale: () => request<Remote>('POST', '/api/v1/remote/tailscale/forget'),
  pairingCode: () => request<Pairing>('POST', '/api/v1/devices/pairing'),
  lastingCodes: () => request<{ codes: LastingCode[] }>('GET', '/api/v1/devices/pairing/lasting'),
  makeLastingCode: (body: { userId?: string; days?: number; devices?: number; note?: string }) => request<Pairing & { lasting: LastingCode }>('POST', '/api/v1/devices/pairing/lasting', body),
  revokeLastingCode: (id: string) => request<void>('DELETE', `/api/v1/devices/pairing/lasting/${id}`),
  sessions: () => request<{ sessions: SignedIn[] }>('GET', '/api/v1/auth/sessions'),
  endSession: (id: string) => request<void>('DELETE', `/api/v1/auth/sessions/${id}`),
  logout: () => request<void>('POST', '/api/v1/auth/logout'),
  backups: () => request<Backups>('GET', '/api/v1/server/backups'),
  backUpNow: () => request<Backups>('POST', '/api/v1/server/backups'),
  checkFilesNow: () => request<Backups>('POST', '/api/v1/server/integrity'),
  buckets: (scope: Scope = { type: '' }) =>
    request<{ total: number; buckets: Bucket[] }>('GET', `/api/v1/timeline/buckets${query(scope)}`),
  month: (month: string, scope: Scope = { type: '' }, signal?: AbortSignal) =>
    request<{ month: string; assets: TimelineAsset[] }>('GET', `/api/v1/timeline${query(scope, { month })}`, undefined, signal),
  /** Recently added: what the timeline shows, newest-arrived first; next pages on from the last. */
  recent: (o: { limit?: number; cursor?: string; type?: MediaType } = {}) => {
    const q = new URLSearchParams();
    if (o.limit) q.set('limit', String(o.limit));
    if (o.cursor) q.set('cursor', o.cursor);
    if (o.type) q.set('type', o.type);
    return request<{ assets: RecentAsset[]; next?: string }>('GET', `/api/v1/timeline/recent?${q}`);
  },
  counts: () => request<Counts>('GET', '/api/v1/library/counts'),
  albums: (o: { parent?: string; all?: boolean } = {}) =>
    request<{ albums: Album[] }>('GET', `/api/v1/albums${o.all ? '?all=true' : o.parent ? `?parent=${o.parent}` : ''}`),
  album: (id: string) => request<Album>('GET', `/api/v1/albums/${id}`),
  createAlbum: (title: string, assetIds: string[], o: { parent?: string; rules?: SmartRules } = {}) =>
    request<Album>('POST', '/api/v1/albums', { title, assetIds, ...o }),
  editAlbum: (id: string, change: { title?: string; description?: string; rules?: SmartRules }) => request<Album>('PATCH', `/api/v1/albums/${id}`, change),
  moveAlbum: (id: string, parent?: string) => request<Album>('POST', `/api/v1/albums/${id}/move`, parent ? { parent } : {}),
  deleteAlbum: (id: string) => request<void>('DELETE', `/api/v1/albums/${id}`),
  stackOf: (assetId: string) => request<Stack>('GET', `/api/v1/assets/${assetId}/stack`),
  lookAlikes: (offset = 0) => request<{ total: number; stacks: Stack[] }>('GET', `/api/v1/stacks/suggested?limit=40&offset=${offset}`),
  makeStack: (assetIds: string[]) => request<Stack>('POST', '/api/v1/stacks', { assetIds }),
  stackCover: (id: string, assetId: string) => request<Stack>('POST', `/api/v1/stacks/${id}/cover`, { assetId }),
  confirmStack: (id: string) => request<Stack>('POST', `/api/v1/stacks/${id}/confirm`),
  /** Yes to every look-alike group at once; the ids undo it. */
  confirmAllStacks: () => request<{ changed: number; stackIds: string[] }>('POST', '/api/v1/stacks/suggested/confirm'),
  unconfirmStacks: (stackIds: string[]) => request<{ changed: number }>('POST', '/api/v1/stacks/unconfirm', { stackIds }),
  removeFromStack: (id: string, assetIds: string[]) => request<{ changed: number }>('POST', `/api/v1/stacks/${id}/remove`, { assetIds }),
  unstack: (id: string) => request<void>('DELETE', `/api/v1/stacks/${id}`),
  keepTopTrashRest: (id: string) => request<{ changed: number }>('POST', `/api/v1/stacks/${id}/trash-rest`),
  libraries: () => request<{ libraries: Library[] }>('GET', '/api/v1/folders'),
  folder: (library: string, path: string) =>
    request<{ library: Library; path: string; folders: Folder[] }>('GET', `/api/v1/folders/${library}?path=${encodeURIComponent(path)}`),
  addToAlbum: (id: string, assetIds: string[]) => request<Album>('POST', `/api/v1/albums/${id}/add`, { assetIds }),
  removeFromAlbum: (id: string, assetIds: string[]) => request<Album>('POST', `/api/v1/albums/${id}/remove`, { assetIds }),
  trash: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/assets/trash', { assetIds }),
  household: () => request<Household>('GET', '/api/v1/household'),
  renameHousehold: (name: string) => request<Household>('PATCH', '/api/v1/household', { name }),
  invite: (body: { name?: string; email?: string; role: 'adult' | 'member' }) => request<{ token: string; url: string; expiresAt: string }>('POST', '/api/v1/household/invites', body),
  cancelInvite: (id: string) => request<void>('DELETE', `/api/v1/household/invites/${id}`),
  setMemberRole: (id: string, role: 'adult' | 'member') => request<Household>('PATCH', `/api/v1/household/members/${id}`, { role }),
  removeGuest: (id: string) => request<void>('DELETE', `/api/v1/household/guests/${id}`),
  setShareRule: (rule: ShareRule) => request<Household>('PUT', '/api/v1/household/rule', rule),
  share: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/household/share', { assetIds }),
  unshare: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/household/unshare', { assetIds }),
  invitation: (token: string) => request<{ household: string; invitedBy: string; name?: string; email?: string; guest?: boolean }>('GET', `/api/v1/invites/${token}`),
  openGuestInvite: (token: string) => request<User>('POST', `/api/v1/invites/${token}/open`),
  acceptInvite: (token: string, body: { displayName: string; email: string; password: string }) => request<User>('POST', `/api/v1/invites/${token}`, body),
  restore: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/assets/restore', { assetIds }),
  emptyTrash: (assetIds?: string[]) => request<{ deleted: number; kept: number }>('POST', '/api/v1/trash/empty', { assetIds }),
  vault: () => request<VaultState>('GET', '/api/v1/vault'),
  /** The first PIN needs nothing else; a new one needs the current PIN, or the account password. */
  setVaultPin: (pin: string, proof?: { currentPin?: string; password?: string }) => request<VaultState>('POST', '/api/v1/vault/pin', { pin, ...proof }),
  unlockVault: (pin: string) => request<VaultState>('POST', '/api/v1/vault/unlock', { pin }),
  lockVault: () => request<VaultState>('POST', '/api/v1/vault/lock'),
  moveToVault: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/vault/add', { assetIds }),
  moveOutOfVault: (assetIds: string[]) => request<{ changed: number }>('POST', '/api/v1/vault/remove', { assetIds }),
  emptyVaultTrash: (assetIds?: string[]) => request<{ deleted: number; kept: number }>('POST', '/api/v1/vault/trash/empty', { assetIds }),
  favorite: (assetIds: string[], favorite: boolean) => request<{ changed: number }>('POST', '/api/v1/assets/favorite', { assetIds, favorite }),
  keywords: () => request<{ keywords: Keyword[] }>('GET', '/api/v1/keywords'),
  selectionKeywords: (assetIds: string[]) => request<{ keywords: Keyword[] }>('POST', '/api/v1/assets/keywords/list', { assetIds }),
  editKeywords: (assetIds: string[], change: { add?: string[]; remove?: string[] }) =>
    request<{ added: number; removed: number }>('POST', '/api/v1/assets/keywords', { assetIds, ...change }),
  setDescription: (assetIds: string[], text: string) => request<{ changed: number }>('POST', '/api/v1/assets/description', { assetIds, text }),
  moveDates: (assetIds: string[], how: { shiftSeconds?: number; setEarliest?: string }) =>
    request<{ changed: number; shiftSeconds: number }>('POST', '/api/v1/assets/date', { assetIds, ...how }),
  mark: (assetIds: string[], change: MarkChange) => request<{ changed: number }>('POST', '/api/v1/assets/marks', { assetIds, ...change }),
  setSettings: (settings: { organize?: boolean; hideMemories?: boolean; memoriesAi?: boolean; adult?: AdultMode; setupHidden?: boolean; setupSkipped?: SetupSkip[] }) =>
    request<User>('PATCH', '/api/v1/auth/me/settings', settings),
  people: (hidden = false) =>
    request<{ people: Person[]; faces: { scanned: number; waiting: number } }>('GET', `/api/v1/people${hidden ? '?hidden=true' : ''}`),
  person: (id: string) => request<Person>('GET', `/api/v1/people/${id}`),
  editPerson: (id: string, body: { name?: string; hidden?: boolean; inMemories?: boolean }) => request<Person>('PATCH', `/api/v1/people/${id}`, body),
  memories: () => request<{ date: string; memories: Memory[] }>('GET', '/api/v1/memories'),
  memorySettings: () => request<{ hiddenDays: HiddenDays[]; hiddenPeople: Person[] }>('GET', '/api/v1/memories/settings'),
  hideMemoryDays: (from: string, to?: string) => request<HiddenDays>('POST', '/api/v1/memories/hidden', { from, to }),
  unhideMemoryDays: (id: string) => request<void>('DELETE', `/api/v1/memories/hidden/${id}`),
  mergePerson: (id: string, into: string) => request<Person>('POST', `/api/v1/people/${id}/merge`, { into }),
  removeFromPerson: (id: string, assetIds: string[]) => request<{ changed: number }>('POST', `/api/v1/people/${id}/remove`, { assetIds }),
  prompts: (o: { q?: string; sort?: 'uses' | 'recent'; limit?: number; offset?: number }) => {
    const p = new URLSearchParams();
    if (o.q) p.set('q', o.q);
    if (o.sort) p.set('sort', o.sort);
    p.set('limit', String(o.limit ?? 40));
    p.set('offset', String(o.offset ?? 0));
    return request<{ total: number; prompts: Prompt[] }>('GET', `/api/v1/prompts?${p}`);
  },
  prompt: (id: string) => request<Prompt>('GET', `/api/v1/prompts/${id}`),
  models: () => request<{ models: AIModel[]; loras: AIModel[] }>('GET', '/api/v1/models'),
  model: (id: string) => request<AIModel>('GET', `/api/v1/models/${id}`),
  characters: () => request<{ characters: Character[] }>('GET', '/api/v1/characters'),
  character: (id: string) => request<Character>('GET', `/api/v1/characters/${id}`),
  createCharacter: (name: string, patterns: string[]) => request<Character>('POST', '/api/v1/characters', { name, patterns }),
  updateCharacter: (id: string, name: string, patterns: string[]) => request<Character>('PUT', `/api/v1/characters/${id}`, { name, patterns }),
  deleteCharacter: (id: string) => request<void>('DELETE', `/api/v1/characters/${id}`),
  removeFromCharacter: (id: string, assetIds: string[]) => request<Character>('POST', `/api/v1/characters/${id}/remove`, { assetIds }),
  personToCharacter: (id: string, to: { character?: string; name?: string }) => request<Character>('POST', `/api/v1/people/${id}/character`, to),
  personFromCharacter: (id: string) => request<Person>('DELETE', `/api/v1/people/${id}/character`),
  characterFacesToPeople: (id: string) => request<{ changed: number }>('DELETE', `/api/v1/characters/${id}/faces`),
  addLabel: (label: string, assetIds: string[]) => request<{ changed: number }>('POST', `/api/v1/labels/${label}/add`, { assetIds }),
  removeLabel: (label: string, assetIds: string[]) => request<{ changed: number }>('POST', `/api/v1/labels/${label}/remove`, { assetIds }),
  immich: () => request<ImmichImport>('GET', '/api/v1/imports/immich'),
  connectImmich: (url: string, apiKey: string) =>
    request<{ saved: boolean; check: ImmichCheck }>('PUT', '/api/v1/imports/immich', { url, apiKey }),
  forgetImmich: () => request<void>('DELETE', '/api/v1/imports/immich'),
  runImmich: (dryRun = false) => request<ImmichImport>('POST', '/api/v1/imports/immich/run', { dryRun }),
  search: (o: { q?: string; type?: MediaType; mode?: SearchMode; similar?: string }) => {
    const p = new URLSearchParams();
    if (o.similar) p.set('similar', o.similar);
    else p.set('q', o.q ?? '');
    if (o.type) p.set('type', o.type);
    if (o.mode && o.mode !== 'auto') p.set('mode', o.mode);
    return request<SearchResult>('GET', `/api/v1/search?${p}`);
  },
  ask: (q: string, prev?: AskPlan, exclude?: string[]) => request<AskResult>('POST', '/api/v1/ask', { q, prev, exclude }),
  createDownload: (ids: string[]) => request<{ url: string; count: number }>('POST', '/api/v1/downloads', { ids }),
  status: () => request<ServerStatus>('GET', '/api/v1/status'),
  health: () => request<{ checks: HealthCheck[] }>('GET', '/api/v1/server/health'),
  places: () => request<PlacesResult>('GET', '/api/v1/places'),
  mapPoints: () => request<{ points: MapPoint[] }>('GET', '/api/v1/map'),
  asset: (id: string, signal?: AbortSignal) => request<AssetDetail>('GET', `/api/v1/assets/${id}`, undefined, signal),
  photoEdit: (id: string) => request<PhotoEdit>('GET', `/api/v1/assets/${id}/edit`),
  savePhotoEdit: (id: string, recipe: Recipe) => request<PhotoEdit>('PUT', `/api/v1/assets/${id}/edit`, recipe),
  undoPhotoEdit: (id: string) => request<PhotoEdit>('DELETE', `/api/v1/assets/${id}/edit`),
  saveTrim: (id: string, startMs: number, endMs: number) => request<{ status: 'accepted' | 'duplicate'; path?: string }>('POST', `/api/v1/assets/${id}/trim`, { startMs, endMs }),
  saveFrame: (id: string, atMs: number) => request<{ status: 'accepted' | 'duplicate'; path?: string }>('POST', `/api/v1/assets/${id}/frame`, { atMs }),
};

function query(scope: Scope, extra: Record<string, string> = {}): string {
  const q = new URLSearchParams(extra);
  if (scope.type) q.set('type', scope.type);
  if (scope.collection) q.set('collection', scope.collection);
  if (scope.folder) q.set('folder', scope.folder);
  if (scope.hideUtility) q.set('hideUtility', 'true');
  if (scope.whose) q.set('library', scope.whose);
  if (scope.marks?.rating) q.set('rating', String(scope.marks.rating));
  if (scope.marks?.flag) q.set('flag', scope.marks.flag);
  if (scope.marks?.label) q.set('label', scope.marks.label);
  const s = q.toString();
  return s ? `?${s}` : '';
}

// Where items' pictures and files come from: the signed-in API, (on a share link's page) the link's,
// or (in the demo) static files. base: a photo as it is, for the editor; edited: the edited photo at
// full size; play: a video's copy that every browser plays.
type Media = (id: string, what: 'thumbnail' | 'preview' | 'original' | 'face' | 'base' | 'edited' | 'play') => string;
const signedIn: Media = (id, what) =>
  what === 'face' ? `/api/v1/faces/${id}/thumbnail` : what === 'base' ? `/api/v1/assets/${id}/edit/base` : `/api/v1/assets/${id}/${what}`;
let media: Media = signedIn;
export function mediaFromLink(token: string) {
  media = (id, what) =>
    what === 'thumbnail' || what === 'preview' || what === 'original'
      ? `/api/v1/public/links/${token}/assets/${id}/${what === 'original' ? 'video' : what}`
      : signedIn(id, what);
}
export function mediaFrom(m: Media) {
  media = m;
}
// A picture's version, from its ThumbHash: an edited photo's previews are new, and the browser's
// copy of the old ones (kept a day) isn't used for them. (The demo's edited previews are blob: URLs,
// new with each edit, which take no query.)
const version = (url: string, thumbhash?: string) => {
  if (!thumbhash || url.startsWith('blob:')) return url;
  let h = 5381;
  for (let i = 0; i < thumbhash.length; i++) h = ((h * 33) ^ thumbhash.charCodeAt(i)) >>> 0;
  return `${url}?v=${h.toString(36)}`;
};
export const thumbURL = (id: string, thumbhash?: string) => version(media(id, 'thumbnail'), thumbhash);
export const faceURL = (id: string) => media(id, 'face');
export const previewURL = (id: string, thumbhash?: string) => version(media(id, 'preview'), thumbhash);
export const uneditedURL = (id: string) => media(id, 'base');
export const editedURL = (id: string) => media(id, 'edited');
export const workflowURL = (id: string) => `/api/v1/assets/${id}/workflow`;
export const originalURL = (id: string) => media(id, 'original');
/** A place items were taken, named offline: the nearest town, its region and country. */
export type Place = { id: string; name: string; region?: string; country?: string; countryCode?: string; lat: number; lon: number;
  count: number; cover?: string; coverThumbhash?: string };
export type PlacesResult = { places: Place[]; map?: { light: string; dark: string }; attribution: string };
export type MapPoint = { id: string; lat: number; lon: number; video?: boolean; thumbhash?: string };

/** A video's copy that every browser plays (H.264 in MP4, SDR, at most 1920 px). */
export const playURL = (id: string) => media(id, 'play');

/** What the server knows about playing a video: the original's type, and whether there's a copy. */
export type VideoPlayback = { type?: string; codec: string; hdr?: boolean; direct: boolean; copy?: boolean; waiting?: boolean };

/** Whether this browser plays a video's original as it is (otherwise its copy, when there is one). */
export function playsOriginal(v?: VideoPlayback): boolean {
  if (!v || v.direct || !v.copy) return true; // nothing better to offer
  return Boolean(v.type) && document.createElement('video').canPlayType(v.type!) === 'probably';
}
