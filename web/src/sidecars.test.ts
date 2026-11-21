import { describe, expect, it } from 'vitest';
import { findNote, gps, mediaKeys, noteKey } from './sidecars';

describe('Takeout notes', () => {
  it('keys a note by the media file it belongs to', () => {
    expect(noteKey('IMG_1234.JPG.json')).toBe('IMG_1234.JPG');
    expect(noteKey('IMG_1234.JPG.supplemental-metadata.json')).toBe('IMG_1234.JPG');
    expect(noteKey('IMG_1234.JPG.supplemental-me.json')).toBe('IMG_1234.JPG');
    expect(noteKey('IMG_1234.JPG.suppl.json')).toBe('IMG_1234.JPG');
    expect(noteKey('IMG_1234.JPG(1).json')).toBe('IMG_1234.JPG(1)');
    expect(noteKey('IMG_1234.JPG.supplemental-metadata(1).json')).toBe('IMG_1234.JPG(1)');
  });

  it('finds the note for the photo, its copies, its edit and its Live Photo video', () => {
    const notes = new Map<string, string>([
      [noteKey('IMG_1234.JPG.supplemental-metadata.json'), 'a'],
      [noteKey('IMG_1234.JPG(1).json'), 'b'],
      [noteKey('IMG_5678.HEIC.json'), 'c'],
      [noteKey('a very long file name that Google cut short somewhere.json'), 'd'],
    ]);
    expect(findNote('IMG_1234.JPG', notes)).toBe('a');
    expect(findNote('IMG_1234(1).JPG', notes)).toBe('b');
    expect(findNote('IMG_1234-edited.JPG', notes)).toBe('a');
    expect(findNote('IMG_5678.MP4', notes)).toBe('c');
    expect(findNote('a very long file name that Google cut short somewhere else.jpg', notes)).toBe('d');
    expect(findNote('IMG_9999.JPG', notes)).toBeUndefined();
    expect(mediaKeys('IMG_1(2).png')[0]).toBe('IMG_1.png(2)');
  });
});

describe('XMP GPS', () => {
  it('reads degrees and minutes with a hemisphere, and plain numbers with a ref', () => {
    expect(gps('48,51.504N')).toBeCloseTo(48.8584, 3);
    expect(gps('2,17.67E')).toBeCloseTo(2.2945, 3);
    expect(gps('33,51.5S')).toBeCloseTo(-33.858, 3);
    expect(gps('151.2', 'E')).toBe(151.2);
    expect(gps('122.4', 'W')).toBe(-122.4);
    expect(gps('nonsense')).toBeUndefined();
  });
});

describe('reading a Takeout folder', () => {
  it('gives each photo its note, album and flags, and leaves out the trash', async () => {
    const { readSidecars } = await import('./sidecars');
    const json = (o: unknown) => new File([JSON.stringify(o)], 'x.json');
    const media = (name: string) => new File([new Uint8Array([1, 2, 3])], name, { type: 'image/jpeg' });
    const note = (title: string, extra: object = {}) => ({ title, photoTakenTime: { timestamp: '1562265000' }, ...extra });
    const picked = [
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_1.JPG', file: media('IMG_1.JPG') },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_1.JPG.supplemental-metadata.json', file: json(note('IMG_1.JPG', { favorited: true, geoData: { latitude: 48.8584, longitude: 2.2945 } })) },
      { path: 'Takeout/Google Photos/Paris trip/IMG_1.JPG', file: media('IMG_1.JPG') },
      { path: 'Takeout/Google Photos/Paris trip/IMG_1.JPG.json', file: json(note('IMG_1.JPG')) },
      { path: 'Takeout/Google Photos/Paris trip/metadata.json', file: json({ title: 'Paris, July 2019' }) },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_2.HEIC', file: media('IMG_2.HEIC') },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_2.MP4', file: media('IMG_2.MP4') },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_2.HEIC.json', file: json(note('IMG_2.HEIC', { description: 'Fireworks', archived: true })) },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_3.JPG', file: media('IMG_3.JPG') },
      { path: 'Takeout/Google Photos/Photos from 2019/IMG_3.JPG.json', file: json(note('IMG_3.JPG', { trashed: true })) },
    ];
    const { extras, from } = await readSidecars(picked);
    expect(from).toContain('Takeout');
    const year = extras.get('Takeout/Google Photos/Photos from 2019/IMG_1.JPG');
    expect(year).toMatchObject({ takenAt: '2019-07-04T18:30:00.000Z', favorite: true, latitude: 48.8584, longitude: 2.2945 });
    expect(year?.album).toBeUndefined();
    expect(extras.get('Takeout/Google Photos/Paris trip/IMG_1.JPG')?.album).toBe('Paris, July 2019');
    expect(extras.get('Takeout/Google Photos/Photos from 2019/IMG_2.MP4')).toMatchObject({ description: 'Fireworks', archived: true });
    expect(extras.get('Takeout/Google Photos/Photos from 2019/IMG_3.JPG')?.skip).toBe(true);
  });
});
