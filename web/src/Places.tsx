// Places: where items were taken, named offline by the server (GeoNames), as a map and as a list by
// country. The map (MapLibre) loads only when it's shown; its tiles come from the server's map style
// (OpenFreeMap by default), which sees the areas looked at, never where items are.

import { useEffect, useRef, useState } from 'react';
import type { GeoJSONSource, Map as MapLibre, Marker } from 'maplibre-gl';
import { api, thumbURL, type MapPoint, type Place, type PlacesResult } from './api';
import { Cover } from './Collections';
import { Icon } from './icons';
import { homeBounds } from './mapFrame';

const plural = (n: number, one: string, many: string) => `${n.toLocaleString()} ${n === 1 ? one : many}`;

type Mode = 'map' | 'list';

/** The Places page: a map of every item with a location, or the places as a list by country. */
export function PlacesPage({ dark, onOpenPlace, onOpenAsset, onFail }: {
  dark: boolean;
  onOpenPlace: (id: string) => void;
  onOpenAsset: (id: string) => void;
  onFail: (e: unknown) => void;
}) {
  const [data, setData] = useState<PlacesResult | null>(null);
  const [mode, setMode] = useState<Mode>(() => (localStorage.getItem('ps.places') === 'list' ? 'list' : 'map'));
  useEffect(() => {
    api.places().then(setData, onFail);
  }, [onFail]);
  const choose = (m: Mode) => {
    setMode(m);
    try {
      localStorage.setItem('ps.places', m);
    } catch {
      /* private window: not remembered */
    }
  };
  const total = data?.places.reduce((n, p) => n + p.count, 0) ?? 0;
  const countries = new Map<string, Place[]>();
  for (const p of data?.places ?? []) countries.set(p.country || 'Elsewhere', [...(countries.get(p.country || 'Elsewhere') ?? []), p]);
  const showMap = mode === 'map' && data?.map;
  return (
    <div className={`scroller page${showMap ? ' page-map' : ''}`}>
      <header className="results-head">
        <div>
          <p className="kicker">Browse</p>
          <h1>Places</h1>
          <p className="muted">
            {data ? (data.places.length ? `${plural(data.places.length, 'place', 'places')} · ${plural(total, 'item', 'items')}` : 'Nothing with a location yet') : ' '}
          </p>
        </div>
        {data?.map && data.places.length > 0 && (
          <div className="h-actions">
            <div className="seg" role="group" aria-label="Show as">
              <button type="button" aria-pressed={mode === 'map'} onClick={() => choose('map')}>Map</button>
              <button type="button" aria-pressed={mode === 'list'} onClick={() => choose('list')}>List</button>
            </div>
          </div>
        )}
      </header>
      {data && data.places.length === 0 && (
        <div className="empty page-empty">
          <h1>No places yet</h1>
          <p>Photos and videos that record where they were taken show up here, named offline.</p>
        </div>
      )}
      {showMap && data.places.length > 0 ? (
        <PlacesMap style={dark ? data.map!.dark : data.map!.light} onOpenAsset={onOpenAsset} onFail={onFail} />
      ) : (
        [...countries].map(([country, list]) => (
          <section key={country} className="place-country">
            <h2>{country}</h2>
            <div className="cards">
              {list.map((p) => (
                <button key={p.id} type="button" className="card" onClick={() => onOpenPlace(p.id)}>
                  <Cover id={p.cover} hash={p.coverThumbhash} name={p.name} />
                  <b>{p.name}</b>
                  <span>{[p.region && p.region !== p.name ? p.region : '', plural(p.count, 'item', 'items')].filter(Boolean).join(' · ')}</span>
                </button>
              ))}
            </div>
          </section>
        ))
      )}
      {data && data.places.length > 0 && <p className="muted small place-credit">{data.attribution}</p>}
    </div>
  );
}

type ML = typeof import('maplibre-gl');

// The library and its worker, fetched the first time a map is shown.
let library: Promise<ML> | null = null;
function maplibre(): Promise<ML> {
  library ??= Promise.all([
    import('maplibre-gl'),
    import('maplibre-gl/dist/maplibre-gl-worker.mjs?url'),
    import('maplibre-gl/dist/maplibre-gl.css'),
  ]).then(([ml, worker]) => {
    ml.setWorkerUrl(worker.default);
    return ml;
  });
  return library;
}

/** Every item with a location, grouped into circles that open as you zoom in; each item a photo. */
function PlacesMap({ style, onOpenAsset, onFail }: { style: string; onOpenAsset: (id: string) => void; onFail: (e: unknown) => void }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let map: MapLibre | null = null;
    let gone = false;
    const shown = new Map<string, Marker>();
    (async () => {
      const [ml, { points }] = await Promise.all([maplibre(), api.mapPoints()]);
      if (gone || !box.current || points.length === 0) return;
      const m = new ml.Map({ container: box.current, style, attributionControl: { compact: true }, maxZoom: 18, dragRotate: false });
      map = m;
      m.addControl(new ml.NavigationControl({ showCompass: false }), 'top-right');
      // Home and what's near it; trips abroad are a zoom out away.
      m.fitBounds(homeBounds(points)!, { padding: 80, maxZoom: 12, duration: 0 });
      m.on('load', () => {
        m.addSource('items', {
          type: 'geojson',
          data: {
            type: 'FeatureCollection',
            features: points.map((p: MapPoint, i: number) => ({ type: 'Feature', geometry: { type: 'Point', coordinates: [p.lon, p.lat] }, properties: { i } })),
          },
          cluster: true,
          clusterRadius: 56,
          clusterMaxZoom: 17,
          clusterProperties: { newest: ['min', ['get', 'i']] }, // points come newest first: a group shows its newest
        });
        // An invisible layer, so the source's features are worked out; the pins are drawn as HTML.
        m.addLayer({ id: 'items', type: 'circle', source: 'items', paint: { 'circle-radius': 0, 'circle-opacity': 0 } });
        const draw = () => {
          if (!m.isSourceLoaded('items')) return;
          const keep = new Set<string>();
          for (const f of m.querySourceFeatures('items')) {
            const props = f.properties as { cluster?: boolean; cluster_id?: number; point_count?: number; newest?: number; i?: number };
            const key = props.cluster ? `c${props.cluster_id}` : `p${props.i}`;
            if (keep.has(key)) continue;
            keep.add(key);
            if (shown.has(key)) continue;
            const p = points[props.cluster ? props.newest! : props.i!];
            const el = document.createElement('button');
            el.type = 'button';
            el.className = `map-pin${props.cluster ? ' group' : ''}`;
            el.setAttribute('aria-label', props.cluster ? `${props.point_count} items here: zoom in` : 'Open this item');
            const img = document.createElement('img');
            img.src = thumbURL(p.id, p.thumbhash);
            img.alt = '';
            img.decoding = 'async';
            el.append(img);
            if (props.cluster) {
              const n = document.createElement('b');
              n.textContent = props.point_count! > 999 ? `${Math.round(props.point_count! / 100) / 10}k` : String(props.point_count);
              el.append(n);
            } else if (p.video) {
              el.classList.add('video');
            }
            const at = (f.geometry as { coordinates: [number, number] }).coordinates;
            el.addEventListener('click', (e) => {
              e.stopPropagation();
              if (!props.cluster) return onOpenAsset(p.id);
              const src = m.getSource('items') as GeoJSONSource;
              void src.getClusterExpansionZoom(props.cluster_id!).then((z) => m.easeTo({ center: at, zoom: z + 0.5 }));
            });
            shown.set(key, new ml.Marker({ element: el }).setLngLat(at).addTo(m));
          }
          for (const [key, marker] of shown) {
            if (!keep.has(key)) {
              marker.remove();
              shown.delete(key);
            }
          }
        };
        m.on('data', (e) => {
          if ((e as { sourceId?: string }).sourceId === 'items') draw();
        });
        m.on('moveend', draw);
        m.on('idle', draw); // once tiles from the zoom before are gone, so are their groups
        draw();
      });
    })().catch(onFail);
    return () => {
      gone = true;
      for (const marker of shown.values()) marker.remove();
      map?.remove();
    };
  }, [style, onOpenAsset, onFail]);
  return (
    <div className="places-map" ref={box}>
      <span className="map-hint"><Icon.pin /> Click a group to zoom in, a photo to open it</span>
    </div>
  );
}
