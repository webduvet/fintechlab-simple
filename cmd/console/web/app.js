/* fintech sim lab console.
   Vanilla, no framework, no bundler: the whole point of the lab is that it
   runs offline from one `make up`, and a UI that needs a package registry
   would be the first thing to break. State is small enough to re-render
   the active view wholesale on every change. */

const state = {
  view: 'flow',
  open: new Set(),      // "view:id" of expanded cards, so a poll does not collapse them
  data: {},
  activity: {},         // service id -> {logs} | {error}, fetched only while a card is open
  timer: null,
};

/* One view per kind, rather than one page listing every service under three
   headings. The headings were doing the work of navigation while the nav
   item was still called "Standalone" — a leftover from an architecture this
   tree no longer has. */
const VIEWS = {
  flow: {
    title: 'System in test',
    sub: 'One settlement run as it happens: the platform down the middle, the vendors it talks to either side, and every hop between them.',
  },
  vendors: {
    title: 'Vendors',
    sub: 'The third parties this lab simulates. These are the deliverable: point your code at one and it should not notice the difference.',
    kind: 'vendor',
  },
  platform: {
    title: 'Platform',
    sub: 'Your own stack — the local runner that actually runs the settle path, and the stand-ins that exist so a hop can be proved connected.',
    kind: 'platform',
  },
  verification: {
    title: 'Verification',
    sub: 'The checks a merchant passes before any money exists. Every one answers positively by default, and every outcome can be flipped.',
    kind: 'verification',
  },
  banks: {
    title: 'Banks',
    sub: 'The sim banks. The core ledger is a generic bank shape; Banking Circle is a vendor whose accounts happen to hold the safeguarding money.',
  },
  merchants: {
    title: 'Merchants',
    sub: 'Distributor → partner → merchant → outlet. The outlet is what the money cares about: Worldline calls it a submerchant and identifies it by MID.',
  },
  config: {
    title: 'Configuration',
    sub: 'The knobs that change what this lab does, and the two config files behind the timings.',
  },
};

/* --- utilities --------------------------------------------------------- */

const $ = (sel) => document.querySelector(sel);

function esc(v) {
  return String(v == null ? '' : v).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let parsed = null;
  try { parsed = text ? JSON.parse(text) : null; } catch (_) { /* keep the raw text */ }
  if (!res.ok) {
    // The peer's own message, verbatim. A control panel that swallows a
    // vendor's 422 and shows "request failed" is worse than curl.
    const msg = (parsed && (parsed.error || parsed.message)) || text || `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return parsed;
}

function toast(title, body, kind = '') {
  const el = document.createElement('div');
  el.className = `toast ${kind}`;
  el.innerHTML = `<div class="t-title">${esc(title)}</div>` +
    (body ? `<div class="t-body">${esc(typeof body === 'string' ? body : JSON.stringify(body, null, 2))}</div>` : '');
  $('#toasts').appendChild(el);
  // Failures linger: they carry a vendor's own message, which is usually
  // the thing worth reading twice.
  setTimeout(() => el.remove(), kind === 'bad' ? 12000 : 6000);
}

function ago(iso) {
  if (!iso) return '—';
  const secs = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (secs < 60) return `${Math.floor(secs)}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h`;
  return `${Math.floor(secs / 86400)}d`;
}

function spark(history) {
  if (!history || !history.length) return '';
  return `<span class="spark">${history.map((ok) => `<i class="${ok ? 'on' : 'off'}"></i>`).join('')}</span>`;
}

function cardKey(id) { return `${state.view}:${id}`; }
function isOpen(id) { return state.open.has(cardKey(id)); }

function bindCards(root) {
  root.querySelectorAll('.card-head[data-card]').forEach((head) => {
    head.addEventListener('click', (ev) => {
      // .form is a div, not a <form>, so it has to be named explicitly --
      // otherwise clicking the gap between two fields collapses the card
      // the operator is filling in.
      if (ev.target.closest('button, a, input, select, textarea, label, .form')) return;
      const key = cardKey(head.dataset.card);
      if (state.open.has(key)) state.open.delete(key); else state.open.add(key);
      renderView();
      // Opening a card can reveal a panel whose data is only fetched while
      // it is being read. Without this the first thing an operator sees is
      // "reading…" until the next poll comes round, which reads as broken.
      load();
    });
  });
}

/* Buttons declare what they do in data-action; one delegated listener runs
   it, reports the result and refreshes. Keeps every handler a one-liner
   and every failure surfaced the same way. */
function bindActions(root) {
  root.querySelectorAll('[data-action]').forEach((btn) => {
    btn.addEventListener('click', async (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      const fn = ACTIONS[btn.dataset.action];
      if (!fn) return;
      btn.disabled = true;
      const label = btn.textContent;
      btn.textContent = '…';
      try {
        await fn(btn.dataset, btn);
      } catch (err) {
        toast('Failed', err.message, 'bad');
      } finally {
        btn.disabled = false;
        btn.textContent = label;
        await load();
      }
    });
  });
}

function fieldsIn(scope) {
  const out = {};
  scope.querySelectorAll('[data-field]').forEach((el) => { out[el.dataset.field] = el.value.trim(); });
  return out;
}

/* --- data -------------------------------------------------------------- */

function isServiceView(view = state.view) { return !!(VIEWS[view] && VIEWS[view].kind); }

async function load() {
  try {
    if (state.view === 'flow') {
      state.data.flow = await api('GET', '/api/flow');
      flowObserve(state.data.flow);
      // The platform's clock heads the diagram. A runner that is not up is
      // a note there, not a broken page.
      try {
        state.clock = await api('GET', '/api/runner/clock');
      } catch (err) {
        state.clock = { error: err.message };
      }
    }
    if (isServiceView()) {
      state.data.overview = await api('GET', '/api/overview');
      await loadActivity();
    }
    if (state.view === 'banks') state.data.banks = await api('GET', '/api/banks');
    if (state.view === 'merchants') state.data.merchants = await api('GET', '/api/merchants');
    if (state.view === 'config') state.data.config = await api('GET', '/api/config');
    // The services badge is wanted on every view, so it is refreshed even
    // when the Services view is not the one on screen.
    // The badges are wanted on every view, so the overview is refreshed even
    // when the view on screen is not a service list.
    if (!isServiceView()) state.data.overview = await api('GET', '/api/overview');
    state.error = null;
  } catch (err) {
    state.error = err.message;
  }
  renderBadges();
  renderView();
}

/* Who Banking Circle will call, and about what.
   This is the half of the vendor that is invisible until it is wrong: a
   subscription is a URL the bank POSTs to, and "nobody subscribed" and
   "subscribed, pointing at the wrong host" both show up downstream as
   silence. The card names the endpoint, the events behind it, and offers a
   probe, so the two can be told apart in one look. */
/* The simulated clock.
   Settlement only runs on a business day, so "what happens on a Sunday" is a
   real test and so is "…and then on Monday, with the same money". Every
   supervised process follows one shared offset, which is what makes this two
   buttons rather than a restart of four processes with a different env. */
function clockPanel(s) {
  if (s.id !== 'local-runner') return '';
  const c = state.clock;
  if (!c) return '';
  if (c.error) return `<div class="note bad">Clock unavailable — ${esc(c.error)}</div>`;

  const shifted = Math.abs(c.offset_hours) >= 0.05;
  const when = (c.now || '').replace('T', ' ').slice(0, 16);
  // What you are typing survives the five-second re-render; until you type,
  // the fields show the clock as it stands.
  const pick = state.clockPick || { date: (c.now || '').slice(0, 10), time: (c.now || '').slice(11, 16) };
  return `<div class="note${c.business_day ? '' : ' warn'}">
      Simulated clock <span class="mono">${esc(when)} UTC</span> —
      <span class="mono">${esc(zoneTime(c.now, 'Europe/Paris'))}</span> in Paris,
      <span class="mono">${esc(zoneTime(c.now, 'Europe/Stockholm'))}</span> in Stockholm,
      <span class="mono">${esc(zoneTime(c.now, 'Europe/London'))}</span> in London.
      ${c.business_day
        ? 'A business day, so settlement will run.'
        : '<b>Not a business day</b>: the balance check will skip and settlement will not leave the safeguarding account.'}
      ${shifted ? ` Shifted ${esc(String(c.offset_hours))}h from the real clock${c.pinned ? ', pinned' : ''}.` : ''}
      Every vendor in the lab follows this clock.
    </div>
    <div class="form">
      <button class="ghost small" data-action="clock-real">Now</button>
      <button class="ghost small" data-action="clock-advance" data-spec="10m">+10 min</button>
      <button class="ghost small" data-action="clock-advance" data-spec="30m">+30 min</button>
      <button class="ghost small" data-action="clock-advance" data-spec="1h">+1 hour</button>
      <button class="ghost small" data-action="clock-advance" data-spec="1d">+1 day</button>
      <button class="ghost small" data-action="clock-advance" data-spec="-1d">−1 day</button>
      <button class="ghost small" data-action="clock-auto">Nearest business day</button>
      <button class="ghost small" data-action="clock-sunday">Move to Sunday</button>
    </div>
    <div class="form">
      <div class="field"><label>Date (UTC)</label>
        <input type="date" data-clock-pick="date" value="${esc(pick.date)}" aria-label="Date (UTC)"></div>
      <div class="field"><label>Time (UTC)</label>
        <input type="time" step="60" data-clock-pick="time" value="${esc(pick.time)}" aria-label="Time (UTC)"></div>
      <button class="ghost small" data-action="clock-set">Set clock</button>
      <span class="hint" data-clock-hint>${esc(pickHint(pick))}</span>
    </div>`;
}

/* HH:MM, weekday, in a timezone — for saying what a UTC instant is where
   the platform's calendars live. */
function zoneTime(iso, timeZone) {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime())) return '—';
  return new Intl.DateTimeFormat('en-GB', { timeZone, weekday: 'short', hour: '2-digit', minute: '2-digit' }).format(d);
}

/* The picked UTC date and time as an instant, or null while incomplete. */
function pickedInstant(pick) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(pick.date || '') || !/^\d{2}:\d{2}$/.test(pick.time || '')) return null;
  return `${pick.date}T${pick.time}:00Z`;
}

function pickHint(pick) {
  const at = pickedInstant(pick);
  return at ? `= ${zoneTime(at, 'Europe/Paris')} Paris, ${zoneTime(at, 'Europe/Stockholm')} Stockholm` : 'pick a date and a time';
}

/* The next occurrence of a weekday at 11:00 UTC — late enough that every
   calendar the platform checks is on the same date. */
function nextWeekday(day) {
  const d = new Date();
  d.setUTCHours(11, 0, 0, 0);
  while (d.getUTCDay() !== day) d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString();
}

async function moveClock(body) {
  const r = await api('POST', '/api/runner/clock', body);
  const a = r.applied || r;
  toast(
    a.business_day ? 'Clock moved — a business day' : 'Clock moved — not a business day',
    `${a.now}\n${a.stockholm} in Stockholm, ${a.london} in London.\n` +
      (a.business_day ? 'Settlement will run.' : 'The balance check will skip and settlement will not leave the bank.'),
    a.business_day ? 'good' : ''
  );
}

function subscriptionPanel(s) {
  if (s.id !== 'banking-circle') return '';
  const d = state.subs;
  if (!d) return '';
  if (d.error) return `<div class="note bad">Subscriptions unavailable — ${esc(d.error)}</div>`;

  const subs = d.subscriptions || [];
  if (!subs.length) {
    return `<div class="note warn">Nothing is subscribed, so every notification this
      vendor produces goes nowhere. A service that subscribes on boot — as
      <span class="mono">apps/banking-circle</span> does — will have pointed its
      <span class="mono">BC_*</span> configuration at another host.</div>`;
  }

  const rows = subs.map((sub) => {
    const events = (sub.events || []).map((e) =>
      `<span class="pill${e.active ? '' : ' warn'}">${esc(e.type)}${e.targets ? ` ·&nbsp;${e.targets}` : ''}</span>`
    ).join('') || '<span class="pill warn">no events</span>';
    // Two stalls, two words, because they are undone differently: retained
    // is what the bank kept when it gave up on a dead endpoint, queued is
    // what you stopped on purpose.
    const queue = []
      .concat(sub.pending ? [`<span class="pill warn">${sub.pending} retained</span>`] : [])
      .concat(sub.queued ? [`<span class="pill warn">${sub.queued} queued</span>`] : [])
      .join('') || '—';
    // The label is the next action, never the current state — see
    // design-system.md, "Toggle — a button, not a switch".
    const toggle = sub.paused
      ? `<button class="ghost small" data-action="bc-resume" data-id="${esc(sub.id)}">Release${sub.queued ? ` ${sub.queued}` : ''}</button>`
      : `<button class="ghost small" data-action="bc-pause" data-id="${esc(sub.id)}">Pause</button>`;
    return `<tr>
      <td class="mono">${esc(sub.endpoint)}</td>
      <td><div class="pill-row">${events}</div></td>
      <td><div class="pill-row">
        <span class="pill ${sub.status === 'active' ? 'ok' : 'warn'}">${esc(sub.status)}</span>${
        sub.paused ? '<span class="pill warn">paused</span>' : ''}</div></td>
      <td><div class="pill-row num">${queue}</div></td>
      <td class="actions">${toggle}</td>
      <td class="actions"><button class="ghost small" data-action="bc-test" data-id="${esc(sub.id)}">Send test</button></td>
    </tr>`;
  }).join('');

  const paused = subs.filter((sub) => sub.paused);
  const held = paused.reduce((n, sub) => n + (sub.queued || 0), 0);
  // Said here as well as in the log, because this is the surface that can
  // explain it: the log below will go quiet, and a quiet log looks exactly
  // like a broken endpoint.
  const note = paused.length
    ? `<div class="note warn"><b>Delivery is paused</b> on ${paused.length}
        ${paused.length === 1 ? 'subscription' : 'subscriptions'}.
        ${held ? `${held} notification${held === 1 ? '' : 's'} waiting.` : 'Nothing is waiting yet.'}
        What queues shows up amber in <b>Notifications sent</b> below, and goes out in order when you release it.
        Nothing else stops: payments still process and notifications are still produced.</div>`
    : '';

  // The second note explains the pause only while nothing is paused. Once
  // something is, the amber note above says more and better, and two
  // stacked paragraphs saying the same thing is how a card stops being read.
  return `${note}<div class="note">Banking Circle POSTs to these, encrypted per subscription, in
      batches of up to ${esc(String(subs[0].max_per_message || 5))}.${paused.length ? ''
      : ` <b>Pause</b> holds one subscription's deliveries so you can watch a catch-up happen on purpose.`}</div>
    <div class="table-scroll"><table>
      <thead><tr>
        <th>Endpoint</th><th>Events</th><th>Status</th><th class="num">Queue</th>
        <th class="actions">Delivery</th><th class="actions"></th>
      </tr></thead>
      <tbody>${rows}</tbody>
    </table></div>`;
}

/* The payouts Banking Circle holds, with the two things the recipient's side
   can do to one after it has been processed: send it back (a return) or
   have the scheme undo it (a reversal). A nested card like the activity
   logs, so it can sit closed while you read something else. */
const PAYOUT_STATE = {
  OutgoingPaymentBooked: ['booked', ''],
  OutgoingPaymentProcessed: ['processed', 'ok'],
  OutgoingPaymentRejected: ['rejected', 'bad'],
  MissingFunding: ['missing funding', 'warn'],
  Reversed: ['reversed', 'warn'],
};

function payoutsPanel(s) {
  if (s.id !== 'banking-circle') return '';
  const d = state.payouts;
  if (!d) return '';
  if (d.error) return `<div class="note bad">Payouts unavailable — ${esc(d.error)}</div>`;

  const id = 'banking-circle:payouts';
  const open = isOpen(id);
  const payouts = d.payouts || [];
  const returned = payouts.filter((p) => p.returned_by).length;
  const reversed = payouts.filter((p) => p.state === 'Reversed').length;
  const rejected = payouts.filter((p) => p.state === 'OutgoingPaymentRejected').length;
  // Counts are data, so they carry status colour; the total never does.
  const pills = [`<span class="pill">${d.total} total</span>`]
    .concat(returned ? [`<span class="pill warn">${returned} returned</span>`] : [])
    .concat(reversed ? [`<span class="pill warn">${reversed} reversed</span>`] : [])
    .concat(rejected ? [`<span class="pill bad">${rejected} rejected</span>`] : [])
    .join(' ');
  const last = payouts[0];
  const desc = last
    ? `${esc(last.id)} ${esc(last.amount)} ${esc(last.currency)} ${esc((PAYOUT_STATE[last.state] || [last.state])[0])} — ${ago(last.created_at)} ago`
    : 'Nothing yet.';

  let body = '';
  if (open) {
    const rows = payouts.map((p) => {
      const [label, tone] = PAYOUT_STATE[p.state] || [p.state, ''];
      const statePills = `<span class="pill ${tone}">${esc(label)}</span>` +
        (p.returned_by ? `<span class="pill warn">returned</span>` : '');
      const act = (action, text, allowed) => allowed
        ? `<button class="danger small" data-action="${action}" data-id="${esc(p.id)}"
             data-amount="${esc(p.amount)}" data-currency="${esc(p.currency)}">${text}</button>`
        : '';
      return `<tr>
        <td class="mono">${esc(p.id)}</td>
        <td class="mono">${esc(p.reference || '—')}</td>
        <td class="mono">${esc(p.external_ref || '—')}</td>
        <td class="mono num">${esc(p.amount)} ${esc(p.currency)}</td>
        <td><div class="pill-row">${statePills}</div></td>
        <td class="actions">${act('bc-return', 'Return', p.can_return)}</td>
        <td class="actions">${act('bc-reverse', 'Reverse', p.can_reverse)}</td>
      </tr>`;
    }).join('');
    body = `<div class="card-body">
      <div class="note"><b>Return</b> is the beneficiary's bank sending a processed payout
        back: the payout stays processed, and the money arrives as a new incoming payment
        flagged <span class="mono">return</span>. <b>Reverse</b> is the scheme undoing it:
        the payout becomes reversed, with a second booking. Only a processed payout that
        has not come back can be either, so only those rows have buttons.</div>
      ${payouts.length ? `<div class="table-scroll"><table>
        <thead><tr>
          <th>Payment</th><th>Reference</th><th>External ref</th><th class="num">Amount</th>
          <th>State</th><th class="actions"></th><th class="actions"></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table></div>
      ${d.total > payouts.length ? `<div class="log-foot">Showing the newest ${payouts.length} of ${d.total}.</div>` : ''}`
      : `<div class="empty">No payouts yet. A settlement run sends them here through B4B —
          start one from the local runner card.</div>`}
    </div>`;
  }

  return `<div class="card log ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="${esc(id)}" role="button" tabindex="0" aria-expanded="${open}">
      <span class="chev">▸</span>
      <div class="card-title">
        <span class="name">Payouts ${pills}</span>
        <span class="desc">${desc}</span>
      </div>
    </div>
    ${body}
  </div>`;
}

/* A service's recent history is fetched only while its card is open. Every
   vendor polling its own log every five seconds would be a lot of traffic
   to show nobody, and the panel is the thing that says you are watching. */
async function loadActivity() {
  const ov = state.data.overview;
  if (!ov) return;
  const watched = ov.services.filter(
    (s) => s.activity_path && s.kind === VIEWS[state.view].kind && isOpen(s.id)
  );
  for (const id of Object.keys(state.activity)) {
    if (!watched.some((s) => s.id === id)) delete state.activity[id];
  }
  // Banking Circle's card carries one more thing: who is subscribed to its
  // notifications. It is fetched on the same condition — the card is open —
  // for the same reason.
  if (watched.some((s) => s.id === 'local-runner')) {
    try {
      state.clock = await api('GET', '/api/runner/clock');
    } catch (err) {
      state.clock = { error: err.message };
    }
  }
  if (watched.some((s) => s.id === 'banking-circle')) {
    try {
      state.subs = await api('GET', '/api/banking-circle/subscriptions');
    } catch (err) {
      state.subs = { error: err.message };
    }
    try {
      state.payouts = await api('GET', '/api/banking-circle/payouts');
    } catch (err) {
      state.payouts = { error: err.message };
    }
  }
  await Promise.all(watched.map(async (s) => {
    try {
      state.activity[s.id] = await api('GET', `/api/services/${encodeURIComponent(s.id)}/activity?limit=100`);
    } catch (err) {
      // Kept against the service rather than thrown: one vendor being down
      // must not blank the whole view, and "this panel could not be read,
      // here is why" is a better answer than an empty list that reads as
      // "nothing has happened".
      state.activity[s.id] = { error: err.message };
    }
  }));
}

/* One nested card per log the service keeps. Collapsed, the header is the
   headline — how many calls, and what the last one was. Opened, it is the
   last hundred. */
function activityPanels(s) {
  if (!s.activity_path) return '';
  const got = state.activity[s.id];
  if (!got) return `<div class="note">Reading recent activity…</div>`;
  if (got.error) return `<div class="note bad">Activity unavailable — ${esc(got.error)}</div>`;
  return (got.logs || []).map((log) => logPanel(s, log)).join('');
}

function logPanel(s, log) {
  const id = `${s.id}:${log.name}`;
  const open = isOpen(id);
  const events = log.events || [];
  const failed = events.filter((e) => e.status === 'bad').length;
  const refused = events.filter((e) => e.status === 'warn').length;

  // Counts are data, so they carry status colour; the total never does.
  const pills = [`<span class="pill">${log.total} total</span>`]
    .concat(refused ? [`<span class="pill warn">${refused} refused</span>`] : [])
    .concat(failed ? [`<span class="pill bad">${failed} failed</span>`] : [])
    .join(' ');

  const last = log.last;
  const desc = last
    ? `${esc(last.summary)} — ${ago(last.at)} ago`
    : 'Nothing yet.';

  const body = open ? `<div class="card-body">
      ${log.note ? `<div class="note">${esc(log.note)}</div>` : ''}
      ${events.length ? `<div class="log-rows">${events.map(logRow).join('')}</div>
        ${log.total > events.length ? `<div class="log-foot">Showing the last ${events.length} of ${log.total}. Older calls have scrolled out of the service's buffer.</div>` : ''}`
      : `<div class="empty">${esc(emptyLogHint(s.id, log.name))}</div>`}
    </div>` : '';

  return `<div class="card log ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="${esc(id)}" role="button" tabindex="0" aria-expanded="${open}">
      <span class="chev">▸</span>
      <div class="card-title">
        <span class="name">${esc(log.title)} ${pills}</span>
        <span class="desc">${desc}</span>
      </div>
    </div>
    ${body}
  </div>`;
}

function logRow(e) {
  const detail = e.detail || {};
  const keys = Object.keys(detail).filter((k) => k !== 'status').sort();
  return `<div class="log-row ${esc(e.status || 'ok')}">
    <span class="log-time">${esc(clock(e.at))}</span>
    <span class="log-op">${esc(e.op || '')}</span>
    <span class="log-main">
      <span class="log-summary">${esc(e.summary || '')}</span>
      ${keys.length ? `<span class="log-detail">${keys.map((k) => `${esc(k)}=${esc(detail[k])}`).join('  ')}</span>` : ''}
    </span>
    <span class="log-peer">${esc(e.peer || '')}</span>
  </div>`;
}

/* An empty state says what would put something in it. "No events" tells an
   operator nothing they did not already know from looking. */
const EMPTY_HINTS = {
  'b4b:payments': 'Nothing yet. A payout from the platform lands here the moment it is asked for — accepted or refused.',
  'b4b:callbacks': 'Nothing yet. Callbacks appear once a payment moves; a refused one is shown in red, which is usually the thing you are looking for.',
  'banking-circle:payments': 'Nothing yet. B4B posts here once a payout clears its gates, and funding the safeguarding account shows up here too.',
  'banking-circle:notifications': 'Nothing yet. Notification batches appear here as they are posted to a subscription endpoint.',
  'banking-circle:reports': 'Nothing yet. The platform\'s reconciliation sweep reads the intraday report here about an hour after a settlement — move the platform clock forward an hour and trigger it to see one.',
  'worldline:sftp': 'Nothing yet. This fills when the platform connects and collects a settlement file — the pull, not the file being cut.',
};

function emptyLogHint(serviceID, logName) {
  return EMPTY_HINTS[`${serviceID}:${logName}`] || 'Nothing yet.';
}

function clock(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toTimeString().slice(0, 8);
}

function renderBadges() {
  const run = state.data.flow && state.data.flow.run;
  const el = $('#badge-flow');
  if (el) {
    const stages = (run && run.stages) || [];
    const done = stages.filter((s) => s.status === 'COMPLETED').length;
    el.textContent = stages.length ? `${done}/${stages.length}` : '';
  }
  const ov = state.data.overview;
  if (ov) {
    // Per section, because the whole-lab number stopped being answerable
    // from one screen the moment the list was split. Only services with a
    // health endpoint are counted: a CA script that cannot be probed is not
    // a service that is down.
    for (const [view, meta] of Object.entries(VIEWS)) {
      if (!meta.kind) continue;
      const group = ov.services.filter((s) => s.kind === meta.kind && s.health_path);
      const up = group.filter((s) => (s.status || {}).state === 'up').length;
      const el = $(`#badge-${view}`);
      if (el) el.textContent = group.length ? `${up}/${group.length}` : '';
    }
  }
  const pods = state.data.pods;
  // "2/4" — running out of declared. A pod that is not running still has a
  // readable recipe, so this is a count, not a health verdict.
  if (pods && pods.enabled) $('#badge-pods').textContent = `${pods.live}/${pods.total}`;
  const banks = state.data.banks;
  if (banks) $('#badge-banks').textContent = String(banks.banks.length);
  const m = state.data.merchants;
  if (m) $('#badge-merchants').textContent = String(m.merchants.length);
}


/* --- system in test ------------------------------------------------------

   A sequence diagram of one settlement run, drawn as SVG by hand.

   Not Mermaid, and not reluctantly: the repo's first non-negotiable is that
   these pages run from `git clone` with no network, which rules out a CDN
   and makes a ~1MB vendored bundle a poor trade for one diagram. The
   deciding reason is the animation, though — the whole point here is that an
   individual arrow lights as its own traffic arrives, and a diagram library
   that re-renders from a text description on every change cannot do that
   without throwing away the DOM it would need to animate.

   State lives in two places on purpose. The server says how many messages
   each hop has carried; the browser remembers what that number was a second
   ago. "Something just happened" is a difference between two observations,
   and only one end of this is in a position to notice it. */

const FLOW_HOLD_MS = 1200;   // a hop that takes 3ms is still worth a look
const FLOW_LIVE_MS = 1000;   // poll while a run is in flight
const FLOW_IDLE_MS = 5000;   // and at the console's usual rate when not

const flow = {
  counts: {},      // step id -> count at the last poll
  lit: {},         // step id -> when it last moved
  runID: null,
  baseline: {},    // step id -> count when this run started
  boxBaseline: {}, // participant id -> stat values when this run started
  timer: null,
};

function flowRunID(data) {
  const run = data && data.run;
  if (!run) return null;
  return run.id || run.root || null;
}

/* Diff first, render second. Every visual state below is derived from these
   two numbers and a clock, so the drawing code stays a pure function of
   state and can be reasoned about on its own. */
function flowObserve(data) {
  const id = flowRunID(data);
  if (id !== flow.runID) {
    // A new run rebases "this run" counters without losing the totals: the
    // vendors' rings are older than any one run and saying otherwise would
    // be the view lying about what it knows.
    flow.runID = id;
    flow.baseline = {};
    flow.boxBaseline = {};
    for (const s of data.steps || []) flow.baseline[s.id] = s.count;
    for (const p of data.participants || []) {
      flow.boxBaseline[p.id] = (p.stats || []).map((st) => Number(st.value) || 0);
    }
  }
  let moved = false;
  for (const s of data.steps || []) {
    const was = flow.counts[s.id];
    if (was !== undefined && s.count > was) {
      flow.lit[s.id] = Date.now();
      moved = true;
    }
    flow.counts[s.id] = s.count;
  }
  // Re-render once when the hold expires, or an arrow that lit on the last
  // poll of a run would stay green until something else happened.
  if (moved) setTimeout(() => { if (state.view === 'flow') renderView(); }, FLOW_HOLD_MS + 60);
}

function flowIsLit(id) {
  return flow.lit[id] && Date.now() - flow.lit[id] < FLOW_HOLD_MS;
}

/* The colour rules, in one place:
     idle      nothing has ever come this way          dark grey
     active    something arrived in the last second    green
     busy      a batch is in flight                    bright neutral
     partial   some of a batch was refused             amber
     failed    something broke                         red
     done      finished, and it was fine               dark green
   A box is green while its own traffic is moving and light grey once it has
   been through — the participants tell you where you are, the arrows tell
   you what happened. */
function flowStepClass(s) {
  const lit = flowIsLit(s.id);
  if (s.count === 0) return s.failed > 0 ? 'is-failed' : 'is-idle';
  if (lit) {
    // A batch in flight is neither good nor bad news yet, so it gets the
    // brightest neutral rather than a verdict it has not earned.
    if (s.failed > 0) return 'is-failed is-lit';
    return s.multi ? 'is-busy is-lit' : 'is-active is-lit';
  }
  if (s.failed > 0) {
    // Four of five report stages landing and one failing on the mail hop is
    // a partial success, and painting it the same red as "nothing arrived"
    // would make the two indistinguishable at a glance.
    return s.multi && s.failed < s.count ? 'is-partial' : 'is-failed';
  }
  if (s.multi && s.refused > 0) return 'is-partial';
  return 'is-done-ok';
}

/* A box reports on the participant, not on its traffic. One report stage
   failing on the mail hop does not make Banking Circle unwell, and a red
   box that means "something that touched this went wrong" is a box you
   stop believing. Red here is the service itself being unreachable. */
function flowBoxClass(p, steps) {
  if (p.error || p.status === 'down') return 'is-failed';
  const mine = steps.filter((s) => s.from === p.id || s.to === p.id);
  if (mine.some((s) => flowIsLit(s.id))) return 'is-active';
  if (mine.some((s) => s.count > 0)) return 'is-done';
  return 'is-idle';
}

function flowDelta(id, count) {
  const base = flow.baseline[id];
  if (base === undefined || count - base <= 0) return '';
  return `+${count - base}`;
}

/* --- the drawing ------------------------------------------------------- */

/* The canvas is measured in CSS pixels and rendered at 1:1 (see app.css):
   an SVG stretched to the viewport magnifies its own text, which is how a
   diagram ends up shouting over the prose around it. */
const FLOW_W = 1000;
const FLOW_PAD = 96;         // half a box, so the outer lifelines sit inside
const FLOW_BOX_W = 178;
const FLOW_SUT_W = 196;      // the system under test's box: wider, still clear of its neighbours
const FLOW_SUT = 'platform'; // the participant being tested; the rest are the world it talks to
const FLOW_BOX_H = 78;
const FLOW_TOP = 10;
const FLOW_ROW = 52;
const FLOW_FIRST_ROW = 124;

/* The platform's clock, above the diagram: in UTC and where its calendars
   live, shifted or not, business day or not. */
function flowClock() {
  const c = state.clock;
  if (!c) return '';
  if (c.error) {
    return `<div class="note">Platform clock unavailable — ${esc(c.error)}. The vendors keep the
      last time they were given, or the real clock if they never had one.</div>`;
  }
  const shifted = Math.abs(c.offset_hours) >= 0.05;
  const when = (c.now || '').replace('T', ' ').slice(0, 16);
  return `<div class="note${c.business_day ? '' : ' warn'}">
      Platform clock <span class="mono">${esc(when)} UTC</span> —
      <span class="mono">${esc(zoneTime(c.now, 'Europe/Paris'))}</span> Paris,
      <span class="mono">${esc(zoneTime(c.now, 'Europe/Stockholm'))}</span> Stockholm,
      <span class="mono">${esc(zoneTime(c.now, 'Europe/London'))}</span> London.
      ${shifted ? `Shifted ${esc(String(c.offset_hours))}h from the real clock.` : 'On the real clock.'}
      ${c.business_day ? 'A business day.' : '<b>Not a business day</b>: settlement will not leave the bank.'}
      <a href="#platform">Move it</a>.
    </div>`;
}

function flowColumns(participants) {
  const span = FLOW_W - FLOW_PAD * 2;
  const step = participants.length > 1 ? span / (participants.length - 1) : 0;
  const at = {};
  participants.forEach((p, i) => { at[p.id] = FLOW_PAD + i * step; });
  return at;
}

function flowBox(p, x, klass, deltas) {
  const sut = p.id === FLOW_SUT;
  const width = sut ? FLOW_SUT_W : FLOW_BOX_W;
  const left = x - width / 2;
  const stats = (p.stats || []).map((st, i) => {
    const d = deltas[i] ? ` <tspan class="flow-delta">${esc(deltas[i])}</tspan>` : '';
    return `<text class="flow-stat" x="${left + 10}" y="${FLOW_TOP + 48 + i * 13}">${esc(st.label)}: <tspan class="flow-stat-v">${esc(st.value)}</tspan>${d}</text>`;
  }).join('');
  // The catalogue's full name is the one to keep in the tooltip; the box
  // gets what fits in it, because a title clipped by the viewBox reads as a
  // rendering bug rather than as a long name.
  const short = String(p.label || '').replace(/\s*\(.*\)\s*$/, '');
  return `<g class="flow-box ${klass}${sut ? ' is-sut' : ''}">
    <title>${esc(p.label)}${p.error ? ' — ' + esc(p.error) : ''}</title>
    <rect x="${left}" y="${FLOW_TOP}" width="${width}" height="${FLOW_BOX_H}" rx="9"></rect>
    <text class="flow-title" x="${left + 10}" y="${FLOW_TOP + 19}">${esc(short)}</text>
    <text class="flow-role" x="${left + 10}" y="${FLOW_TOP + 33}">${esc(p.role || '')}</text>
    <circle class="flow-health ${esc(p.status || 'unknown')}" cx="${left + width - 12}" cy="${FLOW_TOP + 15}" r="3.5"></circle>
    ${stats}
  </g>`;
}

/* The tooltip carries what the line cannot: why the hop exists, and the
   vendor's own words for the last thing that came through it. */
function flowTip(s) {
  const parts = [s.note, s.last_summary].filter(Boolean);
  return parts.length ? `<title>${esc(parts.join(' — '))}</title>` : '';
}

function flowArrow(s, at, y) {
  const klass = flowStepClass(s);
  const delta = flowDelta(s.id, s.count);
  // "256" from a 256-event window is not a count, it is the window. Say so.
  const n = s.capped ? `${s.count}+` : `${s.count}`;
  const badge = s.count
    ? `${n}${delta ? ' (' + delta + ')' : ''}${s.failed ? ' · ' + s.failed + ' failed' : ''}`
    : '';

  if (s.from === s.to) {
    // A self-call: a small loop off the lifeline, because a hop that never
    // leaves the platform is still a step in the sequence.
    const x = at[s.from];
    const w = 48;
    return `<g class="flow-step ${klass}" data-step="${esc(s.id)}">
      ${flowTip(s)}
      <path class="flow-line" d="M ${x} ${y - 8} h ${w} v 16 h ${-w}"></path>
      <polygon class="flow-head" points="${x},${y + 8} ${x + 8},${y + 4.5} ${x + 8},${y + 11.5}"></polygon>
      <text class="flow-label at-start" x="${x + w + 10}" y="${y - 1}">${esc(s.label)}</text>
      ${badge ? `<text class="flow-count at-start" x="${x + w + 10}" y="${y + 13}">${esc(badge)}</text>` : ''}
    </g>`;
  }

  const x1 = at[s.from];
  const x2 = at[s.to];
  const dir = x2 > x1 ? 1 : -1;
  const tipX = x2 - 8 * dir;
  const mid = (x1 + x2) / 2;
  return `<g class="flow-step ${klass}" data-step="${esc(s.id)}">
    ${flowTip(s)}
    <line class="flow-line" x1="${x1}" y1="${y}" x2="${tipX}" y2="${y}"></line>
    <polygon class="flow-head" points="${x2},${y} ${tipX},${y - 5} ${tipX},${y + 5}"></polygon>
    <text class="flow-label" x="${mid}" y="${y - 7}">${esc(s.label)}</text>
    ${badge ? `<text class="flow-count" x="${mid}" y="${y + 14}">${esc(badge)}</text>` : ''}
  </g>`;
}

function renderFlow() {
  const d = state.data.flow;
  if (!d) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;

  const parts = d.participants || [];
  const steps = d.steps || [];
  const at = flowColumns(parts);
  const height = FLOW_FIRST_ROW + steps.length * FLOW_ROW + 20;

  const boxes = parts.map((p) => {
    const base = flow.boxBaseline[p.id] || [];
    const deltas = (p.stats || []).map((st, i) => {
      const now = Number(st.value) || 0;
      const was = base[i];
      return was !== undefined && now - was > 0 ? `+${now - was}` : '';
    });
    return flowBox(p, at[p.id], flowBoxClass(p, steps), deltas);
  }).join('');

  const lifelines = parts.map((p) =>
    `<line class="flow-life${p.id === FLOW_SUT ? ' is-sut' : ''}" x1="${at[p.id]}" y1="${FLOW_TOP + FLOW_BOX_H}" x2="${at[p.id]}" y2="${height - 12}"></line>`
  ).join('');

  const arrows = steps.map((s, i) => flowArrow(s, at, FLOW_FIRST_ROW + i * FLOW_ROW)).join('');

  const run = d.run;
  const live = run && !run.finished_at;
  const banner = run
    ? `<div class="note${live ? '' : ' flow-note-done'}">${live ? 'Running now' : 'Last run'} —
        <span class="mono">${esc(run.id || run.root || '')}</span>${run.error ? ' — ' + esc(run.error) : ''}</div>`
    : `<div class="note">No run yet. Start one from
        <a href="#platform">Platform → Local runner → Run settlement</a>, and this diagram lights up as it goes.</div>`;

  const unreachable = Object.entries(d.errors || {})
    .map(([id, msg]) => `<div class="note bad">${esc(id)} could not be read — ${esc(msg)}</div>`).join('');

  return `${flowClock()}${banner}${unreachable}
    <div class="flow-wrap">
      <svg class="flow" viewBox="0 0 ${FLOW_W} ${height}" role="img"
           aria-label="Sequence diagram of one settlement run">
        ${lifelines}${boxes}${arrows}
      </svg>
    </div>
    ${flowLegend()}
    ${flowReport(d)}`;
}

function flowLegend() {
  const keys = [
    ['is-idle', 'not yet'],
    ['is-active', 'happening now'],
    ['is-busy', 'batch in flight'],
    ['is-done-ok', 'done'],
    ['is-partial', 'partly refused'],
    ['is-failed', 'failed'],
  ];
  return `<div class="flow-legend">${keys.map(([k, label]) =>
    `<span class="flow-key ${k}"><i></i>${esc(label)}</span>`).join('')}</div>`;
}

function flowReport(d) {
  const open = isOpen('report');
  const stats = d.report || [];
  const body = open ? `<div class="card-body">
      <dl class="kv">${stats.map((s) =>
        `<dt>${esc(s.label)}</dt><dd>${esc(s.value)}</dd>`).join('')}</dl>
      <div class="note">Fees are not here: they are computed inside the platform's own
        workers and never leave them, so this would have to invent a number. The rest is
        read from the services themselves.</div>
    </div>` : '';
  const headline = stats.slice(0, 3).map((s) => `${s.label} ${s.value}`).join(' · ');
  return `<div class="card ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="report" role="button" tabindex="0" aria-expanded="${open}">
      <span class="chev">▸</span>
      <div class="card-title">
        <span class="name">Run report</span>
        <span class="desc">${esc(headline || 'Nothing to report yet.')}</span>
      </div>
    </div>
    ${body}
  </div>`;
}

/* --- services ---------------------------------------------------------- */

/* Some cards earn the top of their list. The local runner is the thing
   being tested and the thing with the buttons; below it the stand-ins are
   reference material. */
const FIRST = { platform: 'local-runner' };

function renderServices() {
  const ov = state.data.overview;
  if (!ov) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;
  const kind = VIEWS[state.view].kind;
  const group = ov.services.filter((s) => s.kind === kind);

  // Three tiles for this section, one for the lab. Splitting the list took
  // away the screen that answered "is everything up?", and that answer is
  // worth keeping somewhere you always are.
  const state_ = (s) => (s.status || {}).state;
  const probed = group.filter((s) => s.health_path);
  const labUp = ov.counts.up || 0;
  const labProbed = ov.services.filter((s) => s.health_path).length;
  let html = `<div class="stats">
    <div class="stat up"><b>${probed.filter((s) => state_(s) === 'up').length}</b><span>up</span></div>
    <div class="stat down"><b>${probed.filter((s) => state_(s) === 'down').length}</b><span>down</span></div>
    <div class="stat"><b>${probed.filter((s) => state_(s) !== 'up' && state_(s) !== 'down').length}</b><span>not reporting</span></div>
    <div class="stat accent"><b>${labUp}/${labProbed}</b><span>whole lab</span></div>
  </div>`;

  if (!group.length) return html + `<div class="empty">Nothing in this section yet.</div>`;

  const first = FIRST[state.view];
  const ordered = first
    ? [...group.filter((s) => s.id === first), ...group.filter((s) => s.id !== first)]
    : group;
  return html + ordered.map(serviceCard).join('');
}

function serviceCard(s) {
  const st = s.status || {};
  const open = isOpen(s.id);
  const latency = st.latency_ms != null && st.state === 'up' ? `${st.latency_ms}ms` : '';
  const stateLabel = st.state === 'not-probed' ? 'no health endpoint' : st.state;

  let body = '';
  if (open) {
    body = `<div class="card-body">
      <div class="note">${esc(s.summary)}</div>
      <dl class="kv">
        <dt>Reached at</dt><dd>${esc(s.base_url)}${s.health_path ? esc(s.health_path) : ''}</dd>
        <dt>Ports</dt><dd>${esc((s.ports || []).join('  '))}</dd>
        <dt>Transport</dt><dd>${esc(s.transport)}</dd>
        <dt>Auth</dt><dd>${esc(s.auth)}</dd>
        <dt>State</dt><dd>${esc(stateLabel)}${st.detail ? ' — ' + esc(st.detail) : ''}${st.since ? ` (for ${ago(st.since)})` : ''}</dd>
        <dt>Swap for the real thing</dt><dd style="font-family:var(--sans)">${esc(s.swap_for)}</dd>
        ${s.docs ? `<dt>Design doc</dt><dd>${esc(s.docs)}</dd>` : ''}
      </dl>
      ${clockPanel(s)}
      ${subscriptionPanel(s)}
      ${payoutsPanel(s)}
      ${activityPanels(s)}
      ${(s.endpoints || []).length ? `<div class="table-scroll"><table>
        <thead><tr><th>Method</th><th>Path</th><th>What it does</th></tr></thead>
        <tbody>${s.endpoints.map((e) => `<tr>
          <td class="mono">${esc(e.method)}</td>
          <td class="mono">${esc(e.path)}</td>
          <td>${esc(e.note || '')}</td></tr>`).join('')}</tbody></table></div>` : ''}
      ${serviceNote(s)}
      <div class="form">
        <button class="ghost small" data-action="probe" data-id="${esc(s.id)}">Probe now</button>
        ${s.browse_url ? `<a class="ghost small" href="${esc(s.browse_url)}" target="_blank" rel="noreferrer">Open ${esc(s.browse_url)}</a>` : ''}
        ${serviceExtras(s)}
      </div>
    </div>`;
  }

  return `<div class="card ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="${esc(s.id)}">
      <span class="chev">▸</span>
      <i class="dot ${esc(st.state || 'unknown')}"></i>
      <div class="card-title">
        <span class="name">${esc(s.name)} <span class="pill ${esc(s.kind)}">${esc(s.kind)}</span></span>
        <span class="desc">${esc(s.summary)}</span>
      </div>
      <div class="card-meta">${spark(st.history)}<span>${esc(latency)}</span></div>
    </div>
    ${body}
  </div>`;
}

/* The two actions that drive the whole money flow, offered where the
   service that performs them is. Each is one call to that service's own
   endpoint -- nothing here is reachable only from this UI. */
/* A sentence where a button would be wrong. Prose belongs above the action
   row, not inside it: .form is a flex row of fields and a note dropped in
   between two buttons gets squeezed to nothing. */
function serviceNote(s) {
  if (s.id === 'local-runner' && !(s.status && s.status.state === 'up')) {
    return `<div class="note warn">Not running, so there is nothing to drive. Start it with
      <span class="mono">pnpm nx up infinite-local-runner</span> — one command brings up the
      orchestrator, the workers and gateway together, and this card then runs settlements
      against them.</div>`;
  }
  return '';
}

function serviceExtras(s) {
  if (s.id === 'local-runner') {
    // Offered only when the runner is actually up. A run button on a
    // process that is not listening would fail in a way that reads as the
    // lab being broken rather than the platform not being started.
    if (s.status && s.status.state === 'up') {
      return `<button class="btn small" data-action="runner-settle">Run settlement</button>
              <button class="ghost small" data-action="runner-fund-sga">Fund safeguarding accounts</button>`;
    }
    return '';
  }
  if (s.id === 'worldline') {
    return `<button class="btn small" data-action="cycle" data-slot="morning">Run morning cycle (ER)</button>
            <button class="ghost small" data-action="cycle" data-slot="afternoon">Afternoon confirmation (AR)</button>`;
  }
  if (s.id === 'settlement') {
    return `<button class="btn small" data-action="pull">Pull from Worldline now</button>`;
  }
  return '';
}

/* --- pods -------------------------------------------------------------- */

/* A pod is a bundle of kernels behind one address. Collapsed, a card is the
   one-line claim; expanded, it is the wiring — which kernel listens to what,
   what each is actually configured with, and which routes exist.

   The schematic is drawn from the *running* pod where one answers, and from
   its recipe otherwise. Those are different facts and the card says which,
   because a diagram of what somebody intended is not a diagram of what is
   happening. */

// Boot phases, in order. Grouping the graph by phase is what makes it
// readable: bring-up order is also, roughly, dependency order.
const PHASES = [
  ['port', 'Ports', 'capabilities everything else is handed'],
  ['store', 'Stores', 'books of record'],
  ['transform', 'Transforms', 'codecs and file boundaries'],
  ['gate', 'Gates', 'decisions — they may block, they may not book'],
  ['orchestrate', 'Orchestrators', 'case and window drivers'],
  ['ingress', 'Ingress', 'the HTTP edge, up last so nothing is reachable early'],
  ['worker', 'Workers', 'senders and pollers'],
];

// The four-way "one truth" taxonomy each kernel declares.
const TRUTH_LABEL = {
  'decide': 'decides',
  'hold-state': 'holds state',
  'transform': 'transforms',
  'move-bytes': 'moves bytes',
};

function renderBanks() {
  const d = state.data.banks;
  if (!d) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;
  return d.banks.map(bankCard).join('');
}

function bankCard(b) {
  const open = isOpen(b.id);
  const total = b.accounts.length;
  let body = '';
  if (open) {
    body = `<div class="card-body">
      <div class="note">${esc(b.summary)}</div>
      ${b.error ? `<div class="note bad">${esc(b.error)}</div>` : ''}
      ${total ? `<div class="table-scroll"><table>
        <thead><tr><th>Account</th><th>${esc(b.number_label)}</th><th>Holder</th><th>Ccy</th><th class="num">Balance</th></tr></thead>
        <tbody>${b.accounts.map((a) => `<tr>
          <td class="mono">${esc(a.id)}</td>
          <td class="mono">${esc(a.number)}</td>
          <td>${esc(a.holder)}</td>
          <td class="mono">${esc(a.currency)}</td>
          <td class="num mono">${esc(a.balance)}</td></tr>`).join('')}</tbody></table></div>`
        : (b.error ? '' : '<div class="empty">No accounts yet.</div>')}
      ${b.can_open_account ? `
        <div class="form" id="open-${esc(b.id)}">
          <div class="field wide"><label>Holder</label><input data-field="holder" placeholder="Acme Ltd"></div>
          <div class="field"><label>Currency</label><input data-field="currency" value="EUR"></div>
          <div class="field"><label>Opening balance</label><input data-field="opening_balance" placeholder="0.00"></div>
          <button class="btn small" data-action="open-account" data-id="${esc(b.id)}">Open account</button>
        </div>`
        : `<div class="note warn">${esc(b.note || 'This bank has no account-opening endpoint.')}</div>`}
    </div>`;
  }
  return `<div class="card ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="${esc(b.id)}">
      <span class="chev">▸</span>
      <i class="dot ${b.error ? 'down' : 'up'}"></i>
      <div class="card-title">
        <span class="name">${esc(b.name)} <span class="pill ${esc(b.kind)}">${esc(b.kind)}</span></span>
        <span class="desc">${esc(b.summary)}</span>
      </div>
      <div class="card-meta"><span>${total} account${total === 1 ? '' : 's'}</span></div>
    </div>
    ${body}
  </div>`;
}

/* --- merchants --------------------------------------------------------- */

function renderMerchants() {
  const d = state.data.merchants;
  if (!d) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;

  let html = '';
  if (!d.provisioning_ready) {
    html += `<div class="note warn">Payout-rail provisioning is unavailable: ${esc(d.provisioning_error || 'B4B signing key not readable')}.
      Merchants can still be created; they just cannot be registered as beneficiaries until the key is there.</div>`;
  }

  const partners = d.partners || [];
  html += `<div class="section-title">Hierarchy <small>${d.distributors.length} distributor(s), ${partners.length} partner(s), ${d.merchants.length} merchant(s)</small></div>`;
  html += `<div class="card"><div class="card-body" style="border-top:0;padding-top:14px">
    <div class="table-scroll"><table>
      <thead><tr><th>Partner</th><th>Distributor</th><th>Country</th><th class="num">Merchants</th></tr></thead>
      <tbody>${partners.map((p) => {
        const dist = d.distributors.find((x) => x.id === p.distributor_id);
        const n = d.merchants.filter((m) => m.partner_id === p.id).length;
        return `<tr><td>${esc(p.name)} <span class="mono">${esc(p.id)}</span></td>
          <td>${esc(dist ? dist.name : p.distributor_id)}</td>
          <td class="mono">${esc(p.country)}</td>
          <td class="num">${n}</td></tr>`;
      }).join('')}</tbody></table></div>
    <div class="form">
      <div class="field wide"><label>New partner</label><input data-field="name" placeholder="Southwind Partner Services"></div>
      <div class="field"><label>Country</label>${countrySelect(d.countries, '')}</div>
      <button class="ghost small" data-action="add-partner">Add partner</button>
    </div>
  </div></div>`;

  html += `<div class="section-title">Merchants</div>`;
  html += d.merchants.length
    ? d.merchants.map((m) => merchantCard(m, d)).join('')
    : `<div class="empty">No merchants yet. Create one — it is what every outlet, MID and payout in this lab hangs off.</div>`;
  return html;
}

function countrySelect(countries, selected, field = 'country') {
  return `<select data-field="${esc(field)}">
    <option value="">auto</option>
    ${(countries || []).map((c) => `<option value="${esc(c.code)}" ${c.code === selected ? 'selected' : ''}>${esc(c.code)} — ${esc(c.name)} (${esc(c.currency)})</option>`).join('')}
  </select>`;
}

function merchantCard(m, d) {
  const open = isOpen(m.id);
  const provisioned = m.outlets.filter((o) => o.beneficiary_id).length;
  const blocked = m.outlets.some((o) => o.sanctions_status && o.sanctions_status !== 'pass');
  const statusPill = m.status === 'active' ? 'ok' : (m.status === 'suspended' ? 'bad' : '');

  let body = '';
  if (open) {
    body = `<div class="card-body">
      <dl class="kv">
        <dt>Legal name</dt><dd style="font-family:var(--sans)">${esc(m.legal_name)}</dd>
        <dt>Address</dt><dd style="font-family:var(--sans)">${esc([m.address.line1, m.address.city, m.address.post_code, m.address.country].filter(Boolean).join(', '))}</dd>
        <dt>Settles in</dt><dd>${esc(m.currency)}</dd>
        <dt>MCC</dt><dd>${esc(m.mcc)}</dd>
        <dt>Contact</dt><dd>${esc(m.email)}</dd>
        <dt>Partner</dt><dd>${esc(m.partner_id || '—')}</dd>
      </dl>

      <div class="table-scroll"><table>
        <thead><tr><th>Outlet</th><th>MID</th><th>Terminal</th><th>Paid into</th><th>Beneficiary</th><th>Sanctions</th></tr></thead>
        <tbody>${m.outlets.map((o) => `<tr>
          <td>${esc(o.name)}<div class="mono" style="color:var(--text-faint)">${esc(o.address.city)}, ${esc(o.address.country)}</div></td>
          <td class="mono">${esc(o.mid)}</td>
          <td class="mono">${esc(o.terminal_id)}</td>
          <td class="mono">${esc(o.account_number)}<div style="color:var(--text-faint)">${esc(o.financial_institution)}</div></td>
          <td class="mono">${o.beneficiary_id ? esc(o.beneficiary_id) : '<span style="color:var(--text-faint)">not provisioned</span>'}</td>
          <td>${o.beneficiary_id ? sanctionsControl(o) : '—'}</td>
        </tr>`).join('')}</tbody></table></div>

      <div class="form">
        <button class="ghost small" data-action="add-outlet" data-id="${esc(m.id)}">Add outlet</button>
        <button class="btn small" data-action="provision" data-id="${esc(m.id)}">Register outlets at B4B</button>
        <button class="ghost small" data-action="seed-trading" data-id="${esc(m.id)}" data-count="5" data-amount="2500">Seed 5 card payments / outlet</button>
        <button class="ghost small" data-action="set-status" data-id="${esc(m.id)}" data-status="${m.status === 'suspended' ? 'active' : 'suspended'}">${m.status === 'suspended' ? 'Reinstate' : 'Suspend'}</button>
        <button class="danger small" data-action="delete-merchant" data-id="${esc(m.id)}">Delete</button>
      </div>
      <p class="hint">Seeded card payments are dated yesterday, because Worldline settles T+1 — run the morning cycle on the Services view to have them appear in a settlement file.
        B4B keeps beneficiaries in memory, so after it restarts an outlet shown here as registered is one B4B has forgotten; re-registering is safe and corrects the record rather than creating a second payee.</p>
    </div>`;
  }

  return `<div class="card ${open ? 'is-open' : ''}">
    <div class="card-head" data-card="${esc(m.id)}">
      <span class="chev">▸</span>
      <div class="card-title">
        <span class="name">${esc(m.trading_name || m.legal_name)}
          <span class="pill ${statusPill}">${esc(m.status)}</span>
          ${blocked ? '<span class="pill bad">sanctions</span>' : ''}
        </span>
        <span class="desc">${esc(m.id)} · ${esc(m.country)} · ${esc(m.currency)} · ${m.outlets.length} outlet${m.outlets.length === 1 ? '' : 's'}, ${provisioned} on the payout rail</span>
      </div>
      <div class="card-meta"><span>${esc(m.mcc)}</span></div>
    </div>
    ${body}
  </div>`;
}

function sanctionsControl(o) {
  const opts = ['pass', 'review', 'fail'].map((s) =>
    `<option value="${s}" ${o.sanctions_status === s ? 'selected' : ''}>${s}</option>`).join('');
  return `<select data-action-change="sanctions" data-mid="${esc(o.mid)}">${opts}</select>`;
}

/* --- configuration ------------------------------------------------------ */

function renderConfig() {
  const d = state.data.config;
  if (!d) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;
  let html = '';

  const bc = d.banking_circle;
  html += `<div class="section-title">Banking Circle notification delivery <small>${esc(d.banking_circle_source || d.banking_circle_path || '')}</small></div>`;
  if (d.banking_circle_error) {
    html += `<div class="note bad">${esc(d.banking_circle_error)}</div>`;
  } else if (bc) {
    const scale = bc.time_scale || 1;
    html += `<div class="card"><div class="card-body" style="border-top:0;padding-top:14px">
      ${d.banking_circle_note ? `<div class="note warn">${esc(d.banking_circle_note)}</div>` : ''}
      <div class="note">Time scale <b>${esc(scale)}×</b> — the documented schedule really ends 48 hours after the first failure; here it plays out in about ${esc(fmtSeconds(totalSeconds(bc) / scale))}. Deactivation after ${esc(bc.deactivate_after_retries)} failed retries.</div>
      <div class="table-scroll"><table>
        <thead><tr><th class="num">Retry</th><th>Documented wait</th><th>Wait here</th><th>Email</th></tr></thead>
        <tbody>${(bc.retry_schedule || []).map((s, i) => `<tr>
          <td class="num mono">${i + 1}</td>
          <td class="mono">${esc(fmtSeconds(parseDur(s.after)))}</td>
          <td class="mono">${esc(fmtSeconds(parseDur(s.after) / scale))}</td>
          <td>${s.email ? `<span class="pill ${s.email === 'deactivation' ? 'bad' : 'warn'}">${esc(s.email)}</span>` : ''}</td>
        </tr>`).join('')}</tbody></table></div>
      <p class="hint">Read-only here: banking-circle loads <span class="mono">${esc(d.banking_circle_path || 'config/banking-circle.json')}</span> at start-up. Edit it on the host and restart that service —
        <span class="mono">$EDITOR config/banking-circle.json &amp;&amp; docker compose restart banking-circle</span>.
        <span class="mono">BC_TIME_SCALE</span> in compose.yml overrides the file's <span class="mono">time_scale</span>; the value above is what the service reported it is running.</p>
    </div></div>`;
  }

  html += `<div class="section-title">Worldline file-exchange channel <small>live, and editable</small></div>`;
  if (d.worldline_channel_error) {
    html += `<div class="note bad">${esc(d.worldline_channel_error)}</div>`;
  } else if (d.worldline_channel) {
    html += `<div class="card"><div class="card-body" style="border-top:0;padding-top:14px" id="wl-channel">
      <textarea data-field="json">${esc(JSON.stringify(d.worldline_channel, null, 2))}</textarea>
      <div class="form">
        <button class="btn small" data-action="save-channel">Save to Worldline</button>
      </div>
      <p class="hint">PUT /config on the worldline service. This is the SFT file-exchange channel configuration, not the settlement cycle — the cycle's timings are environment variables, listed below.</p>
    </div></div>`;
  }

  for (const g of d.groups || []) {
    html += `<div class="section-title">${esc(g.title)}${g.note ? ` <small>${esc(g.note)}</small>` : ''}</div>`;
    html += `<div class="card"><div class="card-body" style="border-top:0;padding-top:14px"><div class="table-scroll"><table>
      <thead><tr><th>Variable</th><th>Service</th><th>${g.settings.some((s) => s.live !== undefined && s.live !== '') ? 'Live value' : 'In compose.yml'}</th><th>What it does</th></tr></thead>
      <tbody>${g.settings.map((s) => `<tr>
        <td class="mono">${esc(s.key)}</td>
        <td class="mono">${esc(s.service)}</td>
        <td class="mono">${esc(s.live || s.configured || '—')}</td>
        <td>${esc(s.purpose || '')}</td></tr>`).join('')}</tbody></table></div></div></div>`;
  }
  return html;
}

/* Go renders a duration as "15s", "1m0s", "2h0m0s" -- a sum of components,
   not a single number and unit. Parsing only the first one would read 2h0m0s
   as two hours and 1m0s as one minute by luck, and 90m as 90 minutes but
   1h30m0s as one hour. Sum them all. */
function parseDur(s) {
  const units = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let total = 0, m;
  while ((m = re.exec(String(s || '')))) total += parseFloat(m[1]) * units[m[2]];
  return total;
}
function totalSeconds(bc) {
  return (bc.retry_schedule || []).reduce((acc, s) => acc + parseDur(s.after), 0);
}
function fmtSeconds(s) {
  if (s < 1) return `${+(s * 1000).toFixed(s < 0.01 ? 1 : 0)}ms`;
  if (s < 60) return `${+s.toFixed(2)}s`;
  if (s < 3600) return `${+(s / 60).toFixed(1)}m`;
  return `${+(s / 3600).toFixed(1)}h`;
}

/* --- actions ------------------------------------------------------------ */

const ACTIONS = {
  probe: async (d) => { await api('POST', `/api/services/${d.id}/probe`); },

  cycle: async (d) => {
    const r = await api('POST', `/api/actions/worldline-cycle?slot=${encodeURIComponent(d.slot)}`);
    const files = (r.files || []).map((f) => f.filename).join('\n');
    toast(`Cycle run (${d.slot})`, files || 'No file cut — nothing had traded in the settled window.', 'good');
  },

  pull: async () => {
    const r = await api('POST', '/api/actions/settlement-pull');
    toast('Pulled from Worldline', r, 'good');
  },

  /* The runner answers 202 and keeps going: a settlement run takes the best
     part of a minute, and a button that sat spinning through it would hide
     the thing worth watching, which is the stages arriving in the panels. */
  'runner-settle': async () => {
    const r = await api('POST', '/api/actions/runner-settle');
    toast('Settlement run started', `${r.run}\nWatch the panels below — this takes about a minute.`, 'good');
  },

  /* The probe is the vendor's own clienttest, and the answer is whatever the
     endpoint did with it — reported from Banking Circle's notification log
     rather than from the 200 that only means "queued". */
  /* Each of these is one POST the runner owns the meaning of. The toast
     reports the day it landed on, because "it worked" is not the answer —
     "you are now on Sunday, and settlement will skip" is. */
  'clock-sunday': async () => {
    const next = nextWeekday(0); // Sunday
    await moveClock({ at: next, reason: 'non-business-day scenario' });
  },
  'clock-advance': async (d) => moveClock({ advance: d.spec }),
  'clock-auto': async () => moveClock({ mode: 'auto-business-day' }),
  'clock-real': async () => { state.clockPick = null; await moveClock({ mode: 'real' }); },
  'clock-set': async () => {
    const at = pickedInstant(state.clockPick || {
      date: $('[data-clock-pick="date"]').value, time: $('[data-clock-pick="time"]').value,
    });
    if (!at) throw new Error('Pick a date and a time (UTC) first.');
    state.clockPick = null;
    await moveClock({ at, reason: `set to ${at}` });
  },

  'bc-test': async (d) => {
    const r = await api('POST', `/api/banking-circle/subscriptions/${encodeURIComponent(d.id)}/test`);
    toast(r.delivered ? 'Endpoint took it' : 'Nothing took it', r.result, r.delivered ? 'good' : 'bad');
  },

  /* Pause and release are two routes, not one with a flag, so each button is
     one call somebody could make with curl — and so a mistyped URL is a 404
     rather than a pause nobody asked for. */
  'bc-pause': async (d) => {
    const r = await api('POST', `/api/banking-circle/subscriptions/${encodeURIComponent(d.id)}/pause`);
    toast('Delivery paused', `${r.endpoint}\nNotifications will queue in the log below until you release them.` +
      (r.queued ? `\n${r.queued} already waiting.` : ''));
  },

  'bc-resume': async (d) => {
    const r = await api('POST', `/api/banking-circle/subscriptions/${encodeURIComponent(d.id)}/resume`);
    toast('Delivery resumed',
      r.released ? `${r.released} notification(s) released to ${r.endpoint}, oldest first.`
                 : `${r.endpoint}\nNothing was waiting.`,
      'good');
  },

  /* Return and reverse are two routes for the same reason pause and release
     are, and both are irreversible, so the confirm says what cannot be
     undone rather than asking whether you are sure. */
  'bc-return': async (d) => {
    if (!confirm(`Return ${d.id}? It stays processed, and ${d.amount} ${d.currency} comes back to the safeguarding account as a new incoming payment flagged return. A payout comes back once — there is no undo.`)) return;
    const r = await api('POST', `/api/banking-circle/payouts/${encodeURIComponent(d.id)}/return`);
    toast('Payout returned', `${r.id} brings ${r.amount} ${r.currency} back, flagged return.\n${d.id} stays processed.`, 'good');
  },

  'bc-reverse': async (d) => {
    if (!confirm(`Reverse ${d.id}? It becomes reversed, and ${d.amount} ${d.currency} is booked back to the safeguarding account. There is no undo.`)) return;
    const r = await api('POST', `/api/banking-circle/payouts/${encodeURIComponent(d.id)}/reverse`);
    toast('Payout reversed', `${r.id} is now ${r.state}.\nThe reversal is booked back to the safeguarding account.`, 'good');
  },

  'runner-fund-sga': async () => {
    const r = await api('POST', '/api/actions/runner-fund-sga');
    toast('Safeguarding accounts funded', r.output || r, 'good');
  },

  'open-account': async (d, btn) => {
    const scope = btn.closest('.form');
    const f = fieldsIn(scope);
    if (!f.holder) throw new Error('holder is required');
    const a = await api('POST', `/api/banks/${d.id}/accounts`, {
      holder: f.holder, currency: f.currency || 'EUR', opening_balance: f.opening_balance || '',
    });
    toast('Account opened', `${a.id}  ${a.number}`, 'good');
  },

  'add-partner': async (_d, btn) => {
    const f = fieldsIn(btn.closest('.form'));
    if (!f.name) throw new Error('name is required');
    await api('POST', '/api/partners', { name: f.name, country: f.country || '' });
    toast('Partner added', f.name, 'good');
  },

  'create-merchant': async (_d, btn) => {
    const f = fieldsIn(btn.closest('.modal'));
    const body = {
      legal_name: f.legal_name,
      trading_name: f.trading_name,
      partner_id: f.partner_id || '',
      country: f.country || '',
      currency: f.currency || '',
      mcc: f.mcc || '',
      email: f.email || '',
      outlets: parseInt(f.outlets || '1', 10) || 1,
    };
    if (f.line1) {
      body.address = { line1: f.line1, city: f.city, post_code: f.post_code, country: f.country || '' };
    }
    const m = await api('POST', '/api/merchants', body);
    closeModal();
    state.open.add(`merchants:${m.id}`);
    toast('Merchant created', `${m.legal_name}\n${m.outlets.map((o) => o.mid).join('\n')}`, 'good');
  },

  'add-outlet': async (d) => {
    const r = await api('POST', `/api/merchants/${d.id}/outlets`, { name: '' });
    toast('Outlet opened', `${r.outlet.name}\nMID ${r.outlet.mid}`, 'good');
  },

  provision: async (d) => {
    const r = await api('POST', `/api/merchants/${d.id}/provision`);
    const bad = r.outlets.filter((o) => o.error);
    if (bad.length) {
      toast('Provisioned with failures', bad.map((o) => `${o.mid}: ${o.error}`).join('\n'), 'bad');
    } else {
      toast('Registered at B4B', r.outlets.map((o) => `${o.mid} → ${o.beneficiary_id} (${o.sanctions_status})`).join('\n'), 'good');
    }
  },

  'seed-trading': async (d) => {
    const r = await api('POST', `/api/merchants/${d.id}/trading`, {
      count: parseInt(d.count, 10), amount_cents: parseInt(d.amount, 10),
    });
    toast('Worldline acquired the trading', `${r.accepted} transactions dated ${r.date}\n${r.note}`, 'good');
  },

  'set-status': async (d) => {
    await api('POST', `/api/merchants/${d.id}/status`, { status: d.status });
    toast('Status changed', `${d.id} → ${d.status}`, 'good');
  },

  'delete-merchant': async (d) => {
    if (!confirm('Delete this merchant? Anything already registered at B4B stays there — the rail has no delete.')) return;
    await api('DELETE', `/api/merchants/${d.id}`);
    state.open.delete(`merchants:${d.id}`);
    toast('Merchant deleted', d.id);
  },

  'save-channel': async (_d, btn) => {
    const ta = btn.closest('.card-body').querySelector('[data-field="json"]');
    let parsed;
    try { parsed = JSON.parse(ta.value); } catch (err) { throw new Error('not valid JSON: ' + err.message); }
    await api('PUT', '/api/config/worldline-channel', parsed);
    toast('Channel configuration saved', '', 'good');
  },

  'open-create-merchant': async () => { openMerchantModal(); },
  refresh: async () => { },
};

function bindChanges(root) {
  // The clock picker keeps what is being typed in state, so a re-render
  // does not throw it away, and says live what the time is in Paris.
  root.querySelectorAll('[data-clock-pick]').forEach((input) => {
    input.addEventListener('input', () => {
      const scope = input.closest('.form');
      state.clockPick = {
        date: scope.querySelector('[data-clock-pick="date"]').value,
        time: scope.querySelector('[data-clock-pick="time"]').value,
      };
      const hint = scope.querySelector('[data-clock-hint]');
      if (hint) hint.textContent = pickHint(state.clockPick);
    });
    input.addEventListener('click', (ev) => ev.stopPropagation());
  });
  root.querySelectorAll('[data-action-change="sanctions"]').forEach((sel) => {
    sel.addEventListener('change', async (ev) => {
      ev.stopPropagation();
      try {
        await api('POST', `/api/outlets/${sel.dataset.mid}/sanctions`, { sanctions_status: sel.value });
        toast('Sanctions status moved', `${sel.dataset.mid} → ${sel.value}`, sel.value === 'pass' ? 'good' : 'bad');
      } catch (err) {
        toast('Failed', err.message, 'bad');
      }
      await load();
    });
    sel.addEventListener('click', (ev) => ev.stopPropagation());
  });
}

/* --- modal --------------------------------------------------------------- */

function openMerchantModal() {
  const d = state.data.merchants || { countries: [], partners: [] };
  $('#modal').innerHTML = `
    <div class="modal-head">
      <h2>New merchant</h2>
      <p>Anything left blank is generated — obviously fake, and derived from the merchant's own id so it never changes under you.</p>
    </div>
    <div class="modal-body">
      <div class="form">
        <div class="field wide"><label>Legal name</label><input data-field="legal_name" placeholder="Quiet Coffee Ltd" autofocus></div>
        <div class="field wide"><label>Trading name</label><input data-field="trading_name" placeholder="Quiet Coffee"></div>
      </div>
      <div class="form">
        <div class="field"><label>Country</label>${countrySelect(d.countries, '')}</div>
        <div class="field"><label>Currency</label><input data-field="currency" placeholder="auto"></div>
        <div class="field"><label>MCC</label><input data-field="mcc" placeholder="auto"></div>
        <div class="field"><label>Outlets</label><input data-field="outlets" type="number" min="0" max="20" value="1"></div>
      </div>
      <div class="form">
        <div class="field wide"><label>Partner</label>
          <select data-field="partner_id">
            <option value="">none</option>
            ${(d.partners || []).map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('')}
          </select>
        </div>
        <div class="field wide"><label>Contact email</label><input data-field="email" placeholder="auto (.test)"></div>
      </div>
      <div class="form">
        <div class="field wide"><label>Address line 1</label><input data-field="line1" placeholder="auto"></div>
        <div class="field"><label>City</label><input data-field="city" placeholder="auto"></div>
        <div class="field"><label>Post code</label><input data-field="post_code" placeholder="auto"></div>
      </div>
      <p class="hint">Each outlet gets its own MID, terminal id and payout account. Register them at B4B afterwards to put them on the payout rail.</p>
    </div>
    <div class="modal-foot">
      <button class="ghost" id="modal-cancel">Cancel</button>
      <button class="btn" data-action="create-merchant">Create merchant</button>
    </div>`;
  $('#modal-backdrop').hidden = false;
  $('#modal-cancel').addEventListener('click', closeModal);
  bindActions($('#modal'));
  $('#modal').querySelector('[data-field="legal_name"]').focus();
}

function closeModal() { $('#modal-backdrop').hidden = true; $('#modal').innerHTML = ''; }

/* --- shell ---------------------------------------------------------------- */

function renderView() {
  const meta = VIEWS[state.view];
  $('#view-title').textContent = meta.title;
  $('#view-sub').textContent = meta.sub;

  $('#topbar-actions').innerHTML = state.view === 'merchants'
    ? `<button class="ghost small" data-action="refresh">Refresh</button>
       <button class="btn" data-action="open-create-merchant">+ Create merchant</button>`
    : `<button class="ghost small" data-action="refresh">Refresh</button>`;

  const html =
    state.view === 'flow' ? renderFlow() :
    isServiceView() ? renderServices() :
    state.view === 'banks' ? renderBanks() :
    state.view === 'merchants' ? renderMerchants() :
    renderConfig();

  const content = $('#content');
  // Keep the scroll position across a re-render. The poll rebuilds the
  // view every few seconds, and a page that jumps back to the top while
  // you are reading the retry table is a page you close.
  const scroll = content.scrollTop;
  content.innerHTML = (state.error ? `<div class="note bad">${esc(state.error)}</div>` : '') + html;
  content.scrollTop = scroll;
  bindCards(content);
  bindActions(content);
  bindActions($('#topbar-actions'));
  bindChanges(content);
}

function setView(view) {
  state.view = view;
  document.querySelectorAll('.nav-item').forEach((b) => b.classList.toggle('is-active', b.dataset.view === view));
  location.hash = view;
  renderView();
  load();
}

function isEditing() {
  const el = document.activeElement;
  return !!el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName);
}

function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  try { localStorage.setItem('console-theme', theme); } catch (_) { /* private window */ }
}

function init() {
  try {
    const saved = localStorage.getItem('console-theme');
    if (saved) document.documentElement.dataset.theme = saved;
  } catch (_) { /* private window: the default stands */ }

  $('#nav').addEventListener('click', (ev) => {
    const item = ev.target.closest('.nav-item');
    if (item) setView(item.dataset.view);
  });
  $('#theme-toggle').addEventListener('click', () => {
    setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
  });
  $('#modal-backdrop').addEventListener('click', (ev) => {
    if (ev.target === $('#modal-backdrop')) closeModal();
  });
  document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeModal(); });

  const hash = location.hash.replace('#', '');
  // #services was the old single list; a bookmark to it should still land
  // somewhere sensible rather than on an empty view.
  const landing = hash === 'services' ? 'vendors' : hash;
  setView(VIEWS[landing] ? landing : 'flow');

  // Poll, so a service coming up or going down shows without a reload.
  // Paused while a modal is open or a field has focus: re-rendering
  // replaces the DOM, and doing that under a half-typed form throws the
  // operator's input away.
  // The base rate is the console's usual five seconds. The diagram runs at
  // one, but only while a run is actually in flight: a settlement takes
  // forty seconds and an arrow that lights up four seconds after its
  // traffic arrived is not showing you a sequence, it is showing you a
  // summary.
  let sinceLoad = 0;
  state.timer = setInterval(() => {
    if (!$('#modal-backdrop').hidden) return;
    if (document.hidden) return;
    if (isEditing()) return;
    sinceLoad += 250;
    const run = state.data.flow && state.data.flow.run;
    const live = state.view === 'flow' && run && !run.finished_at;
    if (sinceLoad < (live ? FLOW_LIVE_MS : FLOW_IDLE_MS)) return;
    sinceLoad = 0;
    load();
  }, 250);
}

init();
