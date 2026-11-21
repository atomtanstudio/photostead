import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App, applyTheme } from './App';
import { mediaFromLink } from './api';
import { PersonalLinkPage } from './Guest';
import { SharedLink } from './SharedLink';
import { UpdateNotice } from './update';
import './styles.css';

// Apply the remembered appearance before the first paint (sign-in screens included).
try {
  const t = localStorage.getItem('ps.theme');
  if (t === 'light' || t === 'dark') applyTheme(t);
} catch {
  /* storage unavailable */
}

// The demo build answers the app's requests in the browser (src/demo).
const demo = import.meta.env.MODE === 'demo' ? await import('./demo').then((d) => d.installDemo()) : null;

// A share link's page (/s/{token}) is its own small app: no account, only the link. A personal
// link (/p/{token}) signs this device in as the guest it's for, then it's the guest's app.
const shared = window.location.pathname.match(/^\/s\/([A-Za-z0-9_-]+)\/?$/);
if (shared) mediaFromLink(shared[1]);
const personal = window.location.pathname.match(/^\/p\/([A-Za-z0-9_-]+)\/?$/);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {shared ? <SharedLink token={shared[1]} /> : personal ? <PersonalLinkPage token={personal[1]} /> : <App />}
    {demo ?? <UpdateNotice />}
  </StrictMode>,
);
