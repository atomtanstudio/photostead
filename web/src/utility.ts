// Utility collections: what the server recognizes as screenshots, documents, receipts, ID cards
// and QR codes (each correctable by hand), plus AI-generated items and camera photos. Adult and
// suggestive items are labels too (a worker's model rates every picture), shown apart in the rail.

import type { AdultMode } from './api';
import { Icon } from './icons';

export type UtilityName = 'screenshot' | 'document' | 'receipt' | 'id_card' | 'qr_code';
export type LabelName = UtilityName | 'adult' | 'suggestive';

export type LabelInfo = { name: LabelName; hash: string; title: string; tag: string; one: string; icon: () => React.JSX.Element };

/** The utility labels, which the rail lists one by one and "In the timeline: hidden" keeps out. */
export const LABELS: (LabelInfo & { name: UtilityName })[] = [
  { name: 'screenshot', hash: 'screenshots', title: 'Screenshots', tag: 'Screenshot', one: 'a screenshot', icon: Icon.phone },
  { name: 'document', hash: 'documents', title: 'Documents', tag: 'Document', one: 'a document', icon: Icon.doc },
  { name: 'receipt', hash: 'receipts', title: 'Receipts', tag: 'Receipt', one: 'a receipt', icon: Icon.receipt },
  { name: 'id_card', hash: 'ids', title: 'IDs', tag: 'ID', one: 'an ID', icon: Icon.idcard },
  { name: 'qr_code', hash: 'qr-codes', title: 'QR codes', tag: 'QR code', one: 'a QR code', icon: Icon.qr },
];

/** Adult (nudity or sex) and suggestive items: one rail item, with a switch between them on its page. */
export const ADULT_LABELS: LabelInfo[] = [
  { name: 'adult', hash: 'adult', title: 'Adult', tag: 'Adult', one: 'adult', icon: Icon.adult },
  { name: 'suggestive', hash: 'suggestive', title: 'Suggestive', tag: 'Suggestive', one: 'suggestive', icon: Icon.adult },
];

export const ALL_LABELS: LabelInfo[] = [...LABELS, ...ADULT_LABELS];

export const labelInfo = (name: string) => ALL_LABELS.find((l) => l.name === name);

export const isAdultLabel = (name: string) => name === 'adult' || name === 'suggestive';

/** The rail's switch goes round: shown, blurred, hidden. */
export const ADULT_MODES: AdultMode[] = ['show', 'blur', 'hide'];
export const ADULT_MODE_LABEL: Record<AdultMode, string> = { show: 'shown', blur: 'blurred', hide: 'hidden' };
export const ADULT_MODE_TITLE: Record<AdultMode, string> = {
  show: 'Adult content (nudity or sex) is shown like everything else. Click to blur it',
  blur: 'Adult content is blurred until you click it. Click to hide it',
  hide: 'Adult content is kept out of the timeline, albums, collections and search; it stays in Adult. Click to show it',
};
