import { useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, faceURL, type Character, type Person } from './api';
import { Cover, plural } from './Collections';

/** A person's picture: their clearest face, else (known only from an import so far) an item of theirs. */
export function FaceCover({ person, small }: { person: Person; small?: boolean }) {
  if (!person.coverFace) return <Cover id={person.cover} hash={person.coverThumbhash} name={person.name || '?'} round />;
  return (
    <span className={`cover round${small ? ' small' : ''}`}>
      <img src={faceURL(person.coverFace)} alt="" loading="lazy" decoding="async" onLoad={(e) => e.currentTarget.classList.add('ok')} />
    </span>
  );
}

export const personName = (p: { name: string; kind?: string; species?: string }) =>
  p.name || (p.kind === 'pet' ? `Unnamed ${p.species ?? 'pet'}` : 'Unnamed person');

/** "Person", "Cat" or "Dog". */
export const personKind = (p: { kind?: string; species?: string }) =>
  p.kind === 'pet' ? (p.species === 'dog' ? 'Dog' : p.species === 'cat' ? 'Cat' : 'Pet') : 'Person';

/** Unnamed groups with fewer items than this are hidden until "Show more". */
const FEW = 5;

const itemsNote = (p: Person) => plural(p.count, 'item', 'items');

/**
 * Everyone: the people you've named, the pets (suggested until you name them: telling one animal
 * from another is less sure than faces), then the people face recognition found that nobody has
 * named yet. Hidden people and pets are a click away.
 */
export function PeoplePage({ onOpen, onFail }: { onOpen: (id: string) => void; onFail: (e: unknown) => void }) {
  const [people, setPeople] = useState<Person[] | null>(null);
  const [progress, setProgress] = useState<{ scanned: number; waiting: number } | null>(null);
  const [hidden, setHidden] = useState(false);
  useEffect(() => {
    setPeople(null);
    api.people(hidden).then((r) => {
      setPeople(r.people);
      setProgress(r.faces);
    }, onFail);
  }, [hidden, onFail]);
  const [showAll, setShowAll] = useState(false);
  const humans = people?.filter((p) => p.kind !== 'pet') ?? [];
  const pets = people?.filter((p) => p.kind === 'pet') ?? [];
  const named = humans.filter((p) => p.name);
  const unnamed = humans.filter((p) => !p.name);
  // Groups seen in only a few items are mostly passers-by (people, or animals): tucked away until asked for.
  const small = (p: Person) => !p.name && p.count < FEW;
  const few = [...unnamed, ...pets].filter(small);
  const shownUnnamed = showAll ? unnamed : unnamed.filter((p) => !small(p));
  const shownPets = showAll ? pets : pets.filter((p) => !small(p));
  const grid = (list: Person[]) => (
    <div className="cards people">
      {list.map((p) => (
        <button key={p.id} type="button" className="card" onClick={() => onOpen(p.id)}>
          <FaceCover person={p} />
          <b className={p.name ? undefined : 'unnamed'}>{p.name || (p.kind === 'pet' ? `Name this ${p.species ?? 'pet'}` : 'Add a name')}</b>
          <span>{itemsNote(p)}</span>
        </button>
      ))}
    </div>
  );
  return (
    <div className="scroller page">
      <header className="results-head">
        <div>
          <p className="kicker">Collections</p>
          <h1>{hidden ? 'Hidden people' : 'People'}</h1>
          <p className="muted">
            {people === null
              ? ' '
              : hidden
                ? plural(people.length, 'person you’ve hidden', 'people you’ve hidden')
                : [named.length && `${named.length.toLocaleString()} named`, unnamed.length && `${unnamed.length.toLocaleString()} found by face recognition`,
                    pets.length && plural(pets.length, 'pet', 'pets')]
                    .filter(Boolean)
                    .join(' · ') || 'Nobody yet'}
            {!hidden && progress && progress.waiting > 0 && (
              <>
                {' · '}face recognition has looked at {progress.scanned.toLocaleString()} of {(progress.scanned + progress.waiting).toLocaleString()} items so far
              </>
            )}
          </p>
        </div>
        <div className="h-actions">
          <button type="button" className="pill" onClick={() => setHidden((h) => !h)}>
            {hidden ? 'Back to People' : 'Hidden people'}
          </button>
        </div>
      </header>
      {people?.length === 0 && (
        <div className="empty page-empty">
          <h1>{hidden ? 'Nobody hidden' : 'Nobody yet'}</h1>
          <p>
            {hidden
              ? 'People you hide are kept out of People and of items’ details, and listed here.'
              : 'Face recognition groups the faces in your photos into people as it works through your library. Name them as they appear.'}
          </p>
        </div>
      )}
      {named.length > 0 && grid(named)}
      {shownPets.length > 0 && (
        <>
          {humans.length > 0 && <h2 className="people-sub">Pets</h2>}
          {grid(shownPets)}
        </>
      )}
      {(shownUnnamed.length > 0 || few.length > 0) && (
        <>
          {(named.length > 0 || shownPets.length > 0) && shownUnnamed.length > 0 && <h2 className="people-sub">Not named yet</h2>}
          {grid(shownUnnamed)}
          {few.length > 0 && (
            <div className="people-more">
              <button type="button" className="pill" onClick={() => setShowAll((s) => !s)}>
                {showAll ? `Hide the ${few.length.toLocaleString()} seen fewer than ${FEW} times` : `Show ${few.length.toLocaleString()} more, seen fewer than ${FEW} times`}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

type NameProps = { person: Person; onClose: () => void; onRenamed: () => void; onMerged: (into: Person) => void };

/**
 * Names a person, or says they're someone you've already named (the two become one). Typing a
 * name that's taken offers the merge rather than making a second person of the same name.
 */
export function NameDialog({ person, onClose, onRenamed, onMerged }: NameProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [others, setOthers] = useState<Person[] | null>(null);
  const [q, setQ] = useState(person.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    dialog.current?.showModal();
    dialog.current?.querySelector('input')?.focus(); // showModal focuses the dialog itself
    // Only someone of the same kind: a person, or a pet of the same species.
    api.people().then((r) => setOthers(r.people.filter((p) => p.name && p.id !== person.id && p.kind === person.kind && p.species === person.species)),
      () => setOthers([]));
  }, [person.id, person.kind, person.species]);
  const t = q.trim().toLowerCase();
  const shown = useMemo(() => (others ?? []).filter((p) => t && p.name.toLowerCase().includes(t)).slice(0, 8), [others, t]);
  const taken = (others ?? []).find((p) => p.name.toLowerCase() === t);
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try {
      await fn();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.');
      setBusy(false);
    }
  };
  const merge = (into: Person) => run(async () => onMerged(await api.mergePerson(person.id, into.id)));
  const who = person.name || (person.kind === 'pet' ? `this ${person.species ?? 'pet'}` : 'this person');
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="pn-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (taken) void merge(taken);
          else if (q.trim() !== person.name) void run(async () => { await api.editPerson(person.id, { name: q.trim() }); onRenamed(); });
          else onClose();
        }}
      >
        <h2 id="pn-title">{person.name ? `Rename ${person.name}` : person.kind === 'pet' ? `Name this ${person.species ?? 'pet'}` : 'Who is this?'}</h2>
        <div className="ap-new">
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Their name" maxLength={200} autoFocus aria-label="Name" />
          <button type="submit" disabled={busy || (!q.trim() && !person.name)}>
            {taken ? 'Same person' : 'Save'}
          </button>
        </div>
        {taken ? (
          <p className="muted small">You’ve already named someone {taken.name}. Saving makes {who} and {taken.name} one {person.kind === 'pet' ? 'pet' : 'person'}.</p>
        ) : (
          shown.length > 0 && <p className="muted small">Or is it someone you’ve named?</p>
        )}
        <ul className="ap-list pn-list">
          {shown.map((p) => (
            <li key={p.id}>
              <button type="button" disabled={busy} onClick={() => void merge(p)}>
                <FaceCover person={p} small />
                <span>{p.name}</span>
                <em>{p.count.toLocaleString()}</em>
              </button>
            </li>
          ))}
        </ul>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </dialog>
  );
}

type CharacterProps = { person: Person; onClose: () => void; onDone: (c: Character) => void };

/**
 * Says a group of faces is an AI character rather than someone real: one of your characters, or a
 * new one. The group leaves People, and its items (now and as more faces join it) are the
 * character's.
 */
export function AsCharacterDialog({ person, onClose, onDone }: CharacterProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [list, setList] = useState<Character[] | null>(null);
  const [q, setQ] = useState(person.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    dialog.current?.showModal();
    dialog.current?.querySelector('input')?.focus(); // showModal focuses the dialog itself
    api.characters().then((r) => setList(r.characters), () => setList([]));
  }, []);
  const t = q.trim().toLowerCase();
  const shown = useMemo(() => (list ?? []).filter((c) => !t || c.name.toLowerCase().includes(t)), [list, t]);
  const exact = (list ?? []).find((c) => c.name.toLowerCase() === t);
  const run = async (to: { character?: string; name?: string }) => {
    setBusy(true);
    setError('');
    try {
      onDone(await api.personToCharacter(person.id, to));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t change this. Try again.');
      setBusy(false);
    }
  };
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="pc-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (exact) void run({ character: exact.id });
          else if (q.trim()) void run({ name: q.trim() });
        }}
      >
        <h2 id="pc-title">Which character is this?</h2>
        <p className="muted small">
          For faces of an AI character rather than someone real. {personName(person)} leaves People, and every item with this face, now and
          later, shows the character.
        </p>
        <div className="ap-new">
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find a character, or name a new one" maxLength={100} autoFocus aria-label="Character" />
          <button type="submit" disabled={busy || !q.trim()}>
            {exact ? 'Choose' : 'New character'}
          </button>
        </div>
        {list === null && <p className="muted small">Loading characters…</p>}
        <ul className="ap-list">
          {shown.map((c) => (
            <li key={c.id}>
              <button type="button" disabled={busy} onClick={() => void run({ character: c.id })}>
                <span>{c.name}</span>
                <em>{c.count.toLocaleString()}</em>
              </button>
            </li>
          ))}
        </ul>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </dialog>
  );
}
