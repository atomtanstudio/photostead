// The demo household and how its pictures are filed: who, where, which albums, which camera.
// shots.json says what each picture shows and how it was made; this says the rest.

export const TIME_ZONE = 'America/Los_Angeles';

// The first member is the one the demo is signed in as.
export const MEMBERS = [
  { key: 'sam', name: 'Sam', email: 'sam@example.com', role: 'owner', joinedAt: '2024-01-06T18:00:00Z' },
  { key: 'maya', name: 'Maya', email: 'maya@example.com', role: 'adult', joinedAt: '2024-01-06T18:20:00Z' },
  { key: 'rosa', name: 'Rosa', email: 'rosa@example.com', role: 'adult', joinedAt: '2024-12-26T11:00:00Z' },
];

export const HOUSEHOLD = 'Home';

// People and pets. face: where the face is in their reference portrait (fractions of the image).
export const PEOPLE = [
  { key: 'Leo', kind: 'human', birthDate: '2020-02-14', face: { x: 0.215, y: 0.186, w: 0.58, h: 0.468 } },
  { key: 'June', kind: 'human', birthDate: '2023-06-13', face: { x: 0.2, y: 0.158, w: 0.6, h: 0.483 } },
  { key: 'Maya', kind: 'human', face: { x: 0.205, y: 0.11, w: 0.62, h: 0.5 } },
  { key: 'Sam', kind: 'human', face: { x: 0.2, y: 0.06, w: 0.62, h: 0.5 } },
  { key: 'Rosa', kind: 'human', face: { x: 0.19, y: 0.13, w: 0.62, h: 0.5 } },
  { key: 'Pepper', kind: 'pet', species: 'dog', face: { x: 0.205, y: 0.03, w: 0.6, h: 0.483 } },
  { key: 'Miso', kind: 'pet', species: 'cat', face: { x: 0.19, y: 0.17, w: 0.62, h: 0.5 } },
];

// Albums by the name shots.json uses; trips and family moments sit inside a parent album.
export const ALBUMS = [
  { key: 'Trips', description: 'Everywhere we’ve been' },
  { key: 'Family', description: 'Birthdays, holidays and the everyday' },
  { key: 'Lisbon', parent: 'Trips', description: 'A week with Rosa’s family' },
  { key: 'Kyoto', parent: 'Trips', description: 'Just the two of us, in maple season' },
  { key: 'Iceland', parent: 'Trips', description: 'The Ring Road in June' },
  { key: 'Cannon Beach', parent: 'Trips' },
  { key: 'Mt. Hood', parent: 'Trips' },
  { key: 'Crater Lake', parent: 'Trips' },
  { key: 'Hood River', parent: 'Trips' },
  { key: 'Columbia River Gorge', parent: 'Trips' },
  { key: 'Birthdays', parent: 'Family' },
  { key: 'Christmas', parent: 'Family' },
  { key: 'School', parent: 'Family' },
  { key: 'Autumn', parent: 'Family' },
  { key: 'Home', parent: 'Family' },
];

// Album covers: a picture, as shot#n (#1 when left out).
export const COVERS = {
  Trips: 'b08', Family: 'b13', Lisbon: 'b08', Kyoto: 'b23', Iceland: 'a08', 'Cannon Beach': 'b14#2', 'Mt. Hood': 'c02#2', 'Crater Lake': 'c13',
  'Hood River': 'c18', 'Columbia River Gorge': 'a03#2', Birthdays: 'c11#2', Christmas: 'b30', School: 'c17', Autumn: 'b22', Home: 'b19#2',
};

// Favorites, as shot#n (#1 when left out).
export const FAVORITES = ['a06#2', 'a08', 'a09', 'a13#2', 'b08', 'b14#2', 'b17#2', 'b18r', 'b20#2', 'b22', 'b23', 'b27', 'c02#2', 'c07', 'c11#2', 'c13', 'c18', 'd02', 'd09'];
export const ARCHIVED = ['a05#2'];

// The album Sam shares with the rest of the household, when Sam put the trip's pictures in it, and
// what Maya and Rosa said and picked there (pictures as shot#n, #1 when left out; none: the album).
export const SHARED = {
  album: 'Lisbon',
  addedAt: '2025-04-18T20:10:00Z',
  members: [{ member: 'maya', role: 'contributor' }, { member: 'rosa', role: 'viewer' }],
  comments: [
    { member: 'maya', at: '2025-04-19T08:02:00Z', body: 'Pick the ones you want printed, Mãe, and I’ll order them this week.' },
    { member: 'rosa', at: '2025-04-19T09:15:00Z', shot: 'b05', body: 'This one for the hallway, please. Look at her face!' },
    { member: 'rosa', at: '2025-04-19T09:21:00Z', shot: 'b08', body: 'My favourite of the whole week.' },
    { member: 'maya', at: '2025-04-19T12:40:00Z', shot: 'b08', body: 'Same. Big one for the living room?' },
  ],
  picks: [
    { member: 'rosa', shot: 'b05', at: '2025-04-19T09:14:00Z' },
    { member: 'rosa', shot: 'b08', at: '2025-04-19T09:20:00Z' },
    { member: 'rosa', shot: 'b09', at: '2025-04-19T09:24:00Z' },
    { member: 'maya', shot: 'b08', at: '2025-04-19T12:41:00Z' },
  ],
};

// Pictures left out of the library. Each was looked at and found wanting: the wrong number of people
// (b16#1, b18#1, b18#2, b20#1), a sixth candle on a five-candle cake (b02#1), a foot in the fire
// (c14#1), juice like syrup (c20#1), a two-handled mug (c06#2), impossible torii gates (b23#2) and a
// passport of gibberish (d15#1); and real brands, which don't belong on the website: a Nike swoosh
// (a12#2), NASA's logo (b22#2) and an arena's name (b32#1). b18r and c14r replace the bonfire and
// the campfire.
export const EXCLUDE = ['a12#2', 'b02#1', 'b16#1', 'b18#1', 'b18#2', 'b20#1', 'b22#2', 'b23#2', 'b32#1', 'c06#2', 'c14#1', 'c20#1', 'd15#1'];

// Where each place is, and its time zone.
export const PLACES = {
  'Portland, Oregon': [45.5231, -122.6765, 'America/Los_Angeles'],
  'Portland Farmers Market': [45.5106, -122.6833, 'America/Los_Angeles'],
  'Multnomah Falls, Oregon': [45.5762, -122.1158, 'America/Los_Angeles'],
  'Sauvie Island, Oregon': [45.6865, -122.8222, 'America/Los_Angeles'],
  'Skógafoss, Iceland': [63.5321, -19.5114, 'Atlantic/Reykjavik'],
  'Reynisfjara, Iceland': [63.4057, -19.0446, 'Atlantic/Reykjavik'],
  'Jökulsárlón, Iceland': [64.0784, -16.2306, 'Atlantic/Reykjavik'],
  'Ring Road, Iceland': [63.7912, -18.0521, 'Atlantic/Reykjavik'],
  'Dyrhólaey, Iceland': [63.4022, -19.1306, 'Atlantic/Reykjavik'],
  'Alfama, Lisbon': [38.7114, -9.1305, 'Europe/Lisbon'],
  'Belém, Lisbon': [38.6916, -9.216, 'Europe/Lisbon'],
  'Sintra, Portugal': [38.7876, -9.3906, 'Europe/Lisbon'],
  'Miradouro da Senhora do Monte, Lisbon': [38.7193, -9.1327, 'Europe/Lisbon'],
  'Praça do Comércio, Lisbon': [38.7076, -9.1365, 'Europe/Lisbon'],
  Lisbon: [38.7223, -9.1393, 'Europe/Lisbon'],
  'Cannon Beach, Oregon': [45.8845, -123.9665, 'America/Los_Angeles'],
  'Fushimi Inari, Kyoto': [34.9671, 135.7727, 'Asia/Tokyo'],
  'Eikandō, Kyoto': [35.0149, 135.7944, 'Asia/Tokyo'],
  'Arashiyama, Kyoto': [35.017, 135.6713, 'Asia/Tokyo'],
  Kyoto: [35.0116, 135.7681, 'Asia/Tokyo'],
  'Mt. Hood, Oregon': [45.3311, -121.7113, 'America/Los_Angeles'],
  'Waterfront Park, Portland': [45.5188, -122.6716, 'America/Los_Angeles'],
  'Crater Lake, Oregon': [42.9446, -122.109, 'America/Los_Angeles'],
  'Hood River, Oregon': [45.6241, -121.5536, 'America/Los_Angeles'],
};

// The cameras behind the household's photos: make, model, lens, focal length and aperture.
export const CAMERAS = {
  phone: { make: 'Apple', model: 'iPhone 16 Pro', lens: 'iPhone 16 Pro back triple camera 6.765mm f/1.78', focalMm: 6.8, fNumber: 1.78, file: 'IMG_', ext: 'HEIC', mime: 'image/heic', mb: [1.8, 3.6] },
  'phone-front': { make: 'Apple', model: 'iPhone 16 Pro', lens: 'iPhone 16 Pro front camera 2.69mm f/1.9', focalMm: 2.7, fNumber: 1.9, file: 'IMG_', ext: 'HEIC', mime: 'image/heic', mb: [1.2, 2.2] },
  x100: { make: 'FUJIFILM', model: 'X100VI', lens: 'FUJINON 23mm F2', focalMm: 23, fNumber: 2.8, file: 'DSCF', ext: 'JPG', mime: 'image/jpeg', mb: [9, 15] },
  a7c: { make: 'SONY', model: 'ILCE-7CM2', lens: 'FE 24-105mm F4 G OSS', focalMm: 35, fNumber: 8, file: 'DSC0', ext: 'JPG', mime: 'image/jpeg', mb: [11, 19] },
  'a7c-tele': { make: 'SONY', model: 'ILCE-7CM2', lens: 'FE 70-200mm F4 Macro G OSS II', focalMm: 200, fNumber: 5.6, file: 'DSC0', ext: 'JPG', mime: 'image/jpeg', mb: [10, 16] },
  'a7c-night': { make: 'SONY', model: 'ILCE-7CM2', lens: 'FE 20mm F1.8 G', focalMm: 20, fNumber: 1.8, file: 'DSC0', ext: 'JPG', mime: 'image/jpeg', mb: [14, 22] },
};

// Who took a shot: the person behind the camera isn't in the picture.
export function owner(shot) {
  if (shot.ai) return 'sam';
  if (shot.cam === 'phone-front') return 'maya';
  if (shot.refs.includes('Rosa') && !shot.refs.includes('Maya') && shot.cam === 'phone') return 'maya';
  if (shot.refs.includes('Sam') && !shot.refs.includes('Maya')) return 'maya';
  return 'sam';
}

// The models behind the AI pictures, as Photostead's model catalog names them.
export const MODELS = {
  'nano-banana-2': { model: 'Nano Banana 2' },
  'flux-2': { model: 'FLUX.2 [dev]', steps: 28, cfg: 2.5 },
  'grok-imagine-20': { model: 'Grok Imagine 2.0' },
};
