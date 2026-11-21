// Copying to the clipboard from any address. The clipboard API only works over HTTPS (or
// localhost), and at home Photostead is often a plain http:// address, so this falls back to
// selecting the text and the browser's own copy command. The text is put next to the button
// that asked: inside a modal dialog, anything outside it can't be selected.

import { useState } from 'react';

export async function copyText(text: string, near?: Element | null): Promise<boolean> {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall back */
  }
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0';
  (near?.parentElement ?? document.body).appendChild(area);
  area.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  area.remove();
  return ok;
}

/** A Copy button: says Copied, or, when the browser won't, to copy it by hand. */
export function Copy({ text, label = 'Copy', className = 'link', icon }: { text: string; label?: string; className?: string; icon?: (copied: boolean) => React.ReactNode }) {
  const [state, setState] = useState<'' | 'done' | 'failed'>('');
  return (
    <button
      type="button"
      className={className}
      onClick={(e) => {
        void copyText(text, e.currentTarget).then((ok) => {
          setState(ok ? 'done' : 'failed');
          setTimeout(() => setState(''), ok ? 1500 : 4000);
        });
      }}
    >
      {icon?.(state === 'done')}
      {state === 'done' ? 'Copied' : state === 'failed' ? 'Select it and copy' : label}
    </button>
  );
}
