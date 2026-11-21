// photostead.com: the nav's glass once the page scrolls, sections fading in as they arrive, the
// search showcase (real results from the demo library, worked out when the site is built), the
// switch that stops the page's motion, and copying a prompt. No tracking, no third-party scripts.

document.documentElement.classList.add('js');
const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// Pause motion: stops the hero's wall, the typing and the showcase's rotation. Remembered in this
// browser only.
let calm = false;
const calmed = [];
const setCalm = (on) => {
  calm = on;
  document.documentElement.classList.toggle('calm', on);
  document.querySelectorAll('[data-calm] span').forEach((s) => (s.textContent = on ? 'Play motion' : 'Pause motion'));
  if (on) calmed.forEach((f) => f());
  try {
    localStorage.setItem('calm', on ? '1' : '');
  } catch {
    /* storage blocked: the choice lasts for this page */
  }
};
try {
  if (localStorage.getItem('calm')) setCalm(true);
} catch {
  /* storage blocked */
}
document.querySelectorAll('[data-calm]').forEach((b) => b.addEventListener('click', () => setCalm(!calm)));

// The nav turns to glass once the page has moved.
const nav = document.querySelector('[data-nav]');
if (nav) {
  const onScroll = () => nav.toggleAttribute('data-scrolled', window.scrollY > 12);
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });
}

// Sections fade up as they come into view.
const reveal = new IntersectionObserver(
  (entries) => {
    for (const e of entries) {
      if (e.isIntersecting) {
        e.target.classList.add('in');
        reveal.unobserve(e.target);
      }
    }
  },
  { rootMargin: '0px 0px -8% 0px', threshold: 0.08 },
);
document.querySelectorAll('[data-reveal]').forEach((el) => reveal.observe(el));

// The search showcase: type a query, show what the demo finds for it. Cycles while it's on screen,
// until a chip is picked or motion is paused. Only a query someone picks is announced.
const box = document.querySelector('[data-search]');
const data = document.getElementById('search-data');
if (box && data) {
  const queries = JSON.parse(data.textContent || '[]');
  const text = box.querySelector('[data-s-text]');
  const mode = box.querySelector('[data-s-mode]');
  const answer = box.querySelector('[data-s-answer]');
  const grid = box.querySelector('[data-s-grid]');
  const status = box.querySelector('[data-s-status]');
  const chips = [...box.querySelectorAll('[data-q]')];
  let at = -1;
  let timer = 0;
  let typing = 0;
  let auto = !still && !calm;
  let seen = false;
  calmed.push(() => {
    auto = false;
    clearTimeout(timer);
  });

  const show = (i, picked = false) => {
    at = i;
    const q = queries[i];
    chips.forEach((c, n) => c.setAttribute('aria-pressed', String(n === i)));
    mode.hidden = q.kind !== 'ask';
    clearInterval(typing);
    text.classList.remove('done');
    answer.textContent = '';
    grid.replaceChildren();
    const finish = () => {
      text.textContent = q.q;
      text.classList.add('done');
      answer.innerHTML = q.answer;
      if (picked) status.textContent = `${q.q}: ${answer.textContent}`;
      grid.replaceChildren(
        ...q.items.map((it) => {
          const f = document.createElement('figure');
          if (it.c) f.style.backgroundColor = it.c;
          const img = new Image();
          img.src = it.t;
          img.alt = '';
          img.loading = 'lazy';
          img.decoding = 'async';
          f.append(img);
          return f;
        }),
      );
    };
    if (still || calm) return finish();
    let n = 0;
    text.textContent = '';
    typing = setInterval(() => {
      n++;
      text.textContent = q.q.slice(0, n);
      if (n >= q.q.length) {
        clearInterval(typing);
        setTimeout(finish, 220);
      }
    }, 38);
  };
  const next = () => {
    show((at + 1) % queries.length);
    if (auto) timer = setTimeout(next, 6200);
  };
  chips.forEach((c, n) =>
    c.addEventListener('click', () => {
      auto = false;
      clearTimeout(timer);
      show(n, true);
    }),
  );
  // Start when it's on screen, so the first query is seen being typed; rest while it's off screen.
  new IntersectionObserver((entries) => {
    const on = entries.some((e) => e.isIntersecting);
    if (on && !seen) {
      seen = true;
      next();
    } else if (on && auto) {
      clearTimeout(timer);
      timer = setTimeout(next, 2400);
    } else if (!on) {
      clearTimeout(timer);
    }
  }, { threshold: 0.3 }).observe(box);
}

// Copy a prompt, and say so (the status next to the button is read out).
document.querySelectorAll('[data-copy]').forEach((b) =>
  b.addEventListener('click', async () => {
    const src = document.querySelector(b.getAttribute('data-copy'));
    const label = b.querySelector('span');
    const status = b.parentElement.querySelector('[role="status"]');
    let said;
    try {
      await navigator.clipboard.writeText(src.textContent.trim());
      label.textContent = 'Copied';
      said = 'Prompt copied';
    } catch {
      label.textContent = 'Couldn’t copy';
      said = 'Couldn’t copy the prompt';
    }
    if (status) status.textContent = said;
    setTimeout(() => {
      label.textContent = 'Copy prompt';
      if (status) status.textContent = '';
    }, 1800);
  }),
);
