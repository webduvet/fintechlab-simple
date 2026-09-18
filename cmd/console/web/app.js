/* fintech sim lab console.
   Vanilla, no framework, no bundler: the whole point of the lab is that it
   runs offline from one `make up`, and a UI that needs a package registry
   would be the first thing to break. State is small enough to re-render
   the active view wholesale on every change. */

const state = {
  view: 'services',
  open: new Set(),      // "view:id" of expanded cards, so a poll does not collapse them
  data: {},
  timer: null,
};

const VIEWS = {
  services: {
    title: 'Standalone',
    sub: 'Process catalogue: deep protocol standalones (cmd/*). Prefer Pods for the composable mocked-vendor model — each vendor twin is a recipe of kernels.',
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

async function load() {
  try {
    if (state.view === 'services') state.data.overview = await api('GET', '/api/overview');
    if (state.view === 'banks') state.data.banks = await api('GET', '/api/banks');
    if (state.view === 'merchants') state.data.merchants = await api('GET', '/api/merchants');
    if (state.view === 'config') state.data.config = await api('GET', '/api/config');
    // The services badge is wanted on every view, so it is refreshed even
    // when the Services view is not the one on screen.
    if (state.view !== 'services') state.data.overview = await api('GET', '/api/overview');
    state.error = null;
  } catch (err) {
    state.error = err.message;
  }
  renderBadges();
  renderView();
}

function renderBadges() {
  const ov = state.data.overview;
  if (ov) {
    const up = ov.counts.up || 0;
    const total = ov.services.filter((s) => s.health_path).length;
    $('#badge-services').textContent = `${up}/${total}`;
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

/* --- services ---------------------------------------------------------- */

const KIND_LABEL = {
  vendor: ['Vendor simulations', 'the deliverable — point these at a real host and your code should not notice'],
  platform: ['Platform scaffolding', 'a stand-in for your own stack, kept so a hop can be proved connected'],
  supporting: ['Supporting', 'PKI and stubs'],
};

function renderServices() {
  const ov = state.data.overview;
  if (!ov) return `<div class="empty">${esc(state.error || 'Loading…')}</div>`;

  const c = ov.counts;
  let html = `<div class="stats">
    <div class="stat up"><b>${c.up || 0}</b><span>up</span></div>
    <div class="stat down"><b>${c.down || 0}</b><span>down</span></div>
    <div class="stat"><b>${(c.unknown || 0) + (c['not-probed'] || 0)}</b><span>not reporting</span></div>
    <div class="stat accent"><b>${ov.services.filter((s) => s.kind === 'vendor').length}</b><span>vendor sims</span></div>
  </div>`;

  for (const kind of ['vendor', 'platform', 'supporting']) {
    const group = ov.services.filter((s) => s.kind === kind);
    if (!group.length) continue;
    const [title, note] = KIND_LABEL[kind];
    html += `<div class="section-title">${esc(title)} <small>${esc(note)}</small></div>`;
    html += group.map(serviceCard).join('');
  }
  return html;
}

function serviceCard(s) {
  const st = s.status || {};
  const open = isOpen(s.id);
  const latency = st.latency_ms != null && st.state === 'up' ? `${st.latency_ms}ms` : '';
  const stateLabel = st.state === 'not-probed' ? 'no health endpoint' : st.state;

  let body = '';
  if (open) {
    body = `<div class="card-body">
      <div class="note">${esc(s.summary)}${s.pod_recipe ? `<div class="card-meta" style="margin-top:0.5rem"><span class="pill">pod twin</span> <a href="#" data-view-pods="${esc(s.pod_recipe)}">recipes/${esc(s.pod_recipe)}</a> — composable kernel bundle</div>` : ''}</div>
      <dl class="kv">
        <dt>Reached at</dt><dd>${esc(s.base_url)}${s.health_path ? esc(s.health_path) : ''}</dd>
        <dt>Ports</dt><dd>${esc((s.ports || []).join('  '))}</dd>
        <dt>Transport</dt><dd>${esc(s.transport)}</dd>
        <dt>Auth</dt><dd>${esc(s.auth)}</dd>
        <dt>State</dt><dd>${esc(stateLabel)}${st.detail ? ' — ' + esc(st.detail) : ''}${st.since ? ` (for ${ago(st.since)})` : ''}</dd>
        <dt>Swap for the real thing</dt><dd style="font-family:var(--sans)">${esc(s.swap_for)}</dd>
        ${s.docs ? `<dt>Design doc</dt><dd>${esc(s.docs)}</dd>` : ''}
      </dl>
      ${(s.endpoints || []).length ? `<div class="table-scroll"><table>
        <thead><tr><th>Method</th><th>Path</th><th>What it does</th></tr></thead>
        <tbody>${s.endpoints.map((e) => `<tr>
          <td class="mono">${esc(e.method)}</td>
          <td class="mono">${esc(e.path)}</td>
          <td>${esc(e.note || '')}</td></tr>`).join('')}</tbody></table></div>` : ''}
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
        <span class="desc">${esc(s.summary)}${s.pod_recipe ? `<div class="card-meta" style="margin-top:0.5rem"><span class="pill">pod twin</span> <a href="#" data-view-pods="${esc(s.pod_recipe)}">recipes/${esc(s.pod_recipe)}</a> — composable kernel bundle</div>` : ''}</span>
      </div>
      <div class="card-meta">${spark(st.history)}<span>${esc(latency)}</span></div>
    </div>
    ${body}
  </div>`;
}

/* The two actions that drive the whole money flow, offered where the
   service that performs them is. Each is one call to that service's own
   endpoint -- nothing here is reachable only from this UI. */
function serviceExtras(s) {
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
    state.view === 'services' ? renderServices() :
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
  setView(VIEWS[hash] ? hash : 'services');

  // Poll, so a service coming up or going down shows without a reload.
  // Paused while a modal is open or a field has focus: re-rendering
  // replaces the DOM, and doing that under a half-typed form throws the
  // operator's input away.
  state.timer = setInterval(() => {
    if (!$('#modal-backdrop').hidden) return;
    if (document.hidden) return;
    if (isEditing()) return;
    load();
  }, 5000);
}

init();
