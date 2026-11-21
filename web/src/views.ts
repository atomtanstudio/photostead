// Where the app is: the library (all, photos or videos), a collection, or the albums or people
// pages. Kept in the address (#/albums/…), so a reload stays put and Back goes back.

import type { MediaType, Scope } from './api';
import { ALL_LABELS, type LabelName } from './utility';

export type View =
  | { kind: 'timeline'; type: MediaType }
  | { kind: 'recent' } // Recently added: newest-arrived first
  | { kind: 'favorites' }
  | { kind: 'archive' }
  | { kind: 'trash' }
  | { kind: 'vault'; deleted?: boolean } // deleted: the vault's own trash
  | { kind: 'albums' }
  | { kind: 'album'; id: string }
  | { kind: 'folders' }
  | { kind: 'places' }
  | { kind: 'place'; id: string }
  | { kind: 'lookalikes' }
  | { kind: 'folder'; library: string; path: string } // a folder on disk ('' for a library's top)
  | { kind: 'people' }
  | { kind: 'person'; id: string }
  | { kind: 'prompts' }
  | { kind: 'prompt'; id: string }
  | { kind: 'models' }
  | { kind: 'model'; id: string }
  | { kind: 'lora'; id: string }
  | { kind: 'characters' }
  | { kind: 'character'; id: string }
  | { kind: 'label'; label: LabelName }
  | { kind: 'ai' }
  | { kind: 'captured' };

export const TIMELINE: View = { kind: 'timeline', type: '' };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function parseHash(hash: string): View {
  const [a, b, ...rest] = hash.replace(/^#\/?/, '').split('/');
  switch (a) {
    case 'look-alikes':
      return { kind: 'lookalikes' };
    case 'vault':
      return b === 'deleted' ? { kind: 'vault', deleted: true } : { kind: 'vault' };
    case 'folders':
      return b && UUID.test(b) ? { kind: 'folder', library: b, path: rest.map(decodeURIComponent).join('/') } : { kind: 'folders' };
    case 'photos':
      return { kind: 'timeline', type: 'image' };
    case 'videos':
      return { kind: 'timeline', type: 'video' };
    case 'favorites':
    case 'archive':
    case 'trash':
    case 'ai':
    case 'captured':
    case 'recent':
      return { kind: a };
    case 'albums':
      return b && UUID.test(b) ? { kind: 'album', id: b } : { kind: 'albums' };
    case 'places':
      return b && UUID.test(b) ? { kind: 'place', id: b } : { kind: 'places' };
    case 'people':
      return b && UUID.test(b) ? { kind: 'person', id: b } : { kind: 'people' };
    case 'prompts':
      return b && UUID.test(b) ? { kind: 'prompt', id: b } : { kind: 'prompts' };
    case 'models':
      return b && UUID.test(b) ? { kind: 'model', id: b } : { kind: 'models' };
    case 'loras':
      return b && UUID.test(b) ? { kind: 'lora', id: b } : { kind: 'models' };
    case 'characters':
      return b && UUID.test(b) ? { kind: 'character', id: b } : { kind: 'characters' };
    default: {
      const l = ALL_LABELS.find((x) => x.hash === a);
      return l ? { kind: 'label', label: l.name } : TIMELINE;
    }
  }
}

export function viewHash(v: View): string {
  switch (v.kind) {
    case 'timeline':
      return v.type === 'image' ? '#/photos' : v.type === 'video' ? '#/videos' : '#/';
    case 'album':
      return `#/albums/${v.id}`;
    case 'lookalikes':
      return '#/look-alikes';
    case 'vault':
      return v.deleted ? '#/vault/deleted' : '#/vault';
    case 'folder':
      return `#/folders/${v.library}${v.path ? '/' + v.path.split('/').map(encodeURIComponent).join('/') : ''}`;
    case 'person':
      return `#/people/${v.id}`;
    case 'place':
      return `#/places/${v.id}`;
    case 'prompt':
      return `#/prompts/${v.id}`;
    case 'model':
      return `#/models/${v.id}`;
    case 'lora':
      return `#/loras/${v.id}`;
    case 'character':
      return `#/characters/${v.id}`;
    case 'label':
      return `#/${ALL_LABELS.find((x) => x.name === v.label)!.hash}`;
    default:
      return `#/${v.kind}`;
  }
}

/** What the timeline shows for a view, or null for the albums and people pages. */
export function viewScope(v: View): Scope | null {
  switch (v.kind) {
    case 'timeline':
      return { type: v.type };
    case 'favorites':
    case 'archive':
    case 'trash':
    case 'ai':
    case 'captured':
      return { type: '', collection: v.kind };
    case 'label':
      return { type: '', collection: v.label };
    case 'vault':
      return { type: '', collection: v.deleted ? 'vault-deleted' : 'vault' };
    case 'album':
    case 'person':
    case 'prompt':
    case 'model':
    case 'lora':
    case 'character':
    case 'place':
      return { type: '', collection: `${v.kind}:${v.id}` };
    case 'folder':
      return { type: '', collection: `folder:${v.library}`, folder: v.path };
    default:
      return null;
  }
}

/** The media type search keeps to in this view. */
export const viewType = (v: View): MediaType => (v.kind === 'timeline' ? v.type : '');
