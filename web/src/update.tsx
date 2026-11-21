// Telling an open tab that Photostead has been updated. A tab runs the version that was there when
// it was opened, so after an update it can show, and make, things that have since changed. When the
// tab comes back into view (at most every 5 minutes) it looks at which version the server has,
// and offers a reload.

import { useEffect, useState } from 'react';

const running = document.querySelector<HTMLScriptElement>('script[src*="/assets/index-"]')?.getAttribute('src') ?? '';

export function UpdateNotice() {
  const [updated, setUpdated] = useState(false);
  useEffect(() => {
    if (!running) return; // the dev server: nothing to compare
    let last = Date.now();
    const look = () => {
      if (document.visibilityState !== 'visible' || Date.now() - last < 5 * 60_000) return;
      last = Date.now();
      fetch('/', { cache: 'no-store' })
        .then((r) => r.text())
        .then((html) => {
          const now = html.match(/src="(\/assets\/index-[^"]+\.js)"/)?.[1];
          if (now && now !== running) setUpdated(true);
        })
        .catch(() => {});
    };
    document.addEventListener('visibilitychange', look);
    const every = setInterval(look, 10 * 60_000);
    return () => {
      document.removeEventListener('visibilitychange', look);
      clearInterval(every);
    };
  }, []);
  if (!updated) return null;
  return (
    <div className="toast" role="status">
      <span>Photostead has been updated.</span>
      <button type="button" onClick={() => window.location.reload()}>Reload</button>
    </div>
  );
}
