// The demo's sample library, as site/demo-library/build.mjs writes it (library.json). Everything the
// demo server answers with is worked out from this in the browser: no Photostead server behind it.

import type { Color, Flag, Generation } from '../api';

export type DemoItem = {
  id: string;
  type: 'image' | 'video';
  takenAt: string; // ISO with offset
  offsetMin: number;
  width: number;
  height: number;
  thumbhash: string;
  durationMs?: number;
  fileName: string;
  folderPath: string;
  fileSize: number;
  mime: string;
  owner: string; // a member's id
  shared?: boolean; // in the Household space
  favorite?: boolean;
  archived?: boolean;
  rating?: number;
  flag?: Flag;
  label?: Color;
  title?: string;
  description?: string;
  place?: string;
  location?: { lat: number; lon: number };
  placeId?: string; // the town it was taken near, as the server names it (DemoPlace)
  addedAt: string; // when it came into Photostead
  camera?: { make?: string; model?: string; lens?: string; focalMm?: number; fNumber?: number; exposureS?: number; iso?: number };
  keywords: string[];
  labels: string[]; // utility labels: screenshot, document, receipt, id_card, qr_code
  people: string[]; // person ids
  albums: string[]; // album ids
  characters?: string[]; // character ids
  text?: string; // read from the picture
  sees: string; // what's in it, in words: stands in for search by meaning
  generation?: Omit<Generation, 'promptId' | 'promptUses' | 'modelId'>;
  group?: string; // shots of one moment: look-alikes, and an AI run's variations
};

export type DemoAlbum = { id: string; title: string; description?: string; parent?: string; cover?: string };
export type DemoPerson = { id: string; name: string; kind: 'human' | 'pet'; species?: 'cat' | 'dog'; birthDate?: string; face: string };
export type DemoMember = { id: string; name: string; email: string; role: 'owner' | 'adult' | 'member'; joinedAt: string };
/** Someone the shared album is shared with from outside the household: a guest, by personal link. */
export type DemoGuest = { id: string; name: string; addedBy: string; addedAt: string }; // addedBy: a member's id
export type DemoCharacter = { id: string; name: string; patterns: string[] };
/** A town from GeoNames, as the server's places table has it. */
export type DemoPlace = { id: string; name: string; region: string; country: string; countryCode: string; lat: number; lon: number };
/** The album shared with the rest of the household: who it's shared with, what they said and picked
 *  (each by a member's id, or a guest's). */
export type DemoShared = {
  album: string;
  addedAt: string; // when its pictures were put in it
  members: { member: string; role: 'viewer' | 'contributor' }[];
  comments: { id: string; member: string; assetId?: string; body: string; at: string }[];
  picks: { member: string; assetId: string; at: string }[];
};

export type DemoLibrary = {
  timeZone: string;
  household: { id: string; name: string };
  members: DemoMember[]; // the first is the signed-in viewer
  guests?: DemoGuest[]; // none in library.json yet
  items: DemoItem[];
  albums: DemoAlbum[];
  people: DemoPerson[];
  characters: DemoCharacter[];
  places: DemoPlace[];
  shared: DemoShared;
};
