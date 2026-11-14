// Console single-page app. No build step and no dependencies.

const API = '/console/api';
const state = { user: null, providers: [], version: '', overview: null, users: null };

// ---------- language: English text is the key, other languages map from it ----------
const TR = {
  'Address books': 'Adres defterleri', 'Devices': 'Cihazlar', 'Users': 'Kullanıcılar', 'Sessions': 'Oturumlar', 'Logs': 'Kayıtlar',
  'Settings': 'Ayarlar', 'Sign in': 'Giriş yap', 'Sign out': 'Çıkış yap', 'Username': 'Kullanıcı adı', 'Password': 'Parola', 'or': 'veya',
  'Continue with {0}': '{0} ile devam et', 'Your server': 'Sunucun', 'ID server': 'ID sunucusu', 'Key': 'Anahtar', 'Copy': 'Kopyala',
  'Copied': 'Kopyalandı', 'Copy client configuration': 'İstemci yapılandırmasını kopyala', 'Not set yet': 'Henüz ayarlanmadı',
  'My address book': 'Adres defterim', 'Tags': 'Etiketler', 'New address book': 'Yeni adres defteri', 'Add device': 'Cihaz ekle',
  'Search': 'Ara', 'Tiles': 'Kutular', 'List': 'Liste', 'Connect': 'Bağlan', 'Copy ID': 'ID\'yi kopyala', 'Edit': 'Düzenle',
  'Remove': 'Kaldır', 'Rename': 'Yeniden adlandır', 'Share': 'Paylaş', 'Delete': 'Sil', 'Cancel': 'Vazgeç', 'Save': 'Kaydet',
  'Name': 'Ad', 'Note': 'Not', 'Alias': 'Takma ad', 'ID': 'ID', 'Platform': 'Platform', 'Unknown': 'Bilinmiyor',
  'On another server': 'Başka bir sunucuda', 'Server address': 'Sunucu adresi', 'Server key': 'Sunucu anahtarı',
  'Leave both empty for devices on this server.': 'Bu sunucudaki cihazlar için ikisini de boş bırak.',
  'No devices here yet': 'Burada henüz cihaz yok', 'Add one with its RustDesk ID, or add it from the RustDesk app.': 'RustDesk ID\'siyle ekle ya da RustDesk uygulamasından ekle.',
  'Nothing matches the filter': 'Filtreye uyan kayıt yok', 'Read only': 'Salt okunur', 'Shared by {0}': '{0} paylaştı', 'Shared': 'Paylaşılan',
  'New tag': 'Yeni etiket', 'Edit tag': 'Etiketi düzenle', 'Color': 'Renk', 'Delete tag': 'Etiketi sil',
  'Remove {0} from this address book?': '{0} bu adres defterinden kaldırılsın mı?', 'Delete the address book {0} and its {1} entries?': '{0} adres defteri ve içindeki {1} kayıt silinsin mi?',
  'Share {0}': '{0} defterini paylaş', 'No access': 'Erişim yok', 'Can view': 'Görebilir', 'Can edit': 'Düzenleyebilir', 'Full control': 'Tam yetki',
  'There is nobody else to share with yet.': 'Henüz paylaşılacak başka kullanıcı yok.',
  'Pin a server on the entries': 'Kayıtlara sunucu sabitle',
  'Entries that are a bare ID get the server address and key added, so the RustDesk app connects through that server even while you are signed in.': 'Yalnız ID olan kayıtlara sunucu adresi ve anahtarı eklenir; böylece RustDesk uygulaması hesabın açıkken de o sunucu üzerinden bağlanır.',
  'Pin on {0} entries': '{0} kayda sabitle', 'All entries already carry a server.': 'Tüm kayıtlarda zaten sunucu var.',
  'Online': 'Çevrimiçi', 'Offline': 'Çevrimdışı', 'Last seen {0}': 'Son görülme {0}', 'never': 'hiç', 'just now': 'az önce',
  '{0} min ago': '{0} dk önce', '{0} h ago': '{0} sa önce', '{0} d ago': '{0} gün önce',
  'No device has reported to this server yet': 'Bu sunucuya henüz cihaz bildirim yapmadı',
  'Devices appear here once RustDesk on them uses this server as API server.': 'Üzerindeki RustDesk bu sunucuyu API sunucusu olarak kullanınca cihazlar burada görünür.',
  'Add to address book': 'Adres defterine ekle', 'Forget': 'Unut', 'Forget {0}? It comes back when it reports again.': '{0} unutulsun mu? Yeniden bildirim yapınca geri gelir.',
  'Owner': 'Sahibi', 'Address book': 'Adres defteri', 'Add': 'Ekle', 'Added': 'Eklendi',
  'New user': 'Yeni kullanıcı', 'Administrator': 'Yönetici', 'Disabled': 'Devre dışı', 'E-mail': 'E-posta', 'Display name': 'Görünen ad',
  'Edit user': 'Kullanıcıyı düzenle', 'Set password': 'Parola belirle', 'New password': 'Yeni parola', 'At least 8 characters.': 'En az 8 karakter.',
  'Delete the user {0}? Their address books are deleted too.': '{0} kullanıcısı silinsin mi? Adres defterleri de silinir.',
  'Leave empty for an account that signs in with a provider only.': 'Yalnız sağlayıcıyla giriş yapacak hesap için boş bırak.',
  'No password': 'Parolası yok', 'Signed-in apps and browsers': 'Giriş yapılmış uygulamalar ve tarayıcılar', 'Everyone': 'Herkes', 'Mine': 'Benimkiler',
  'Web console': 'Web konsolu', 'RustDesk app': 'RustDesk uygulaması', 'This browser': 'Bu tarayıcı', 'Sign out this session': 'Bu oturumu kapat', 'Last used {0}': 'Son kullanım {0}',
  'Connections': 'Bağlantılar', 'File transfers': 'Dosya aktarımları', 'Device': 'Cihaz', 'From': 'Bağlanan', 'Started': 'Başlangıç', 'Duration': 'Süre',
  'Path': 'Yol', 'Time': 'Zaman', 'ongoing': 'sürüyor', 'No connections recorded yet': 'Henüz bağlantı kaydı yok', 'No file transfers recorded yet': 'Henüz dosya aktarımı kaydı yok',
  'Older': 'Daha eski', 'Newer': 'Daha yeni', 'Server': 'Sunucu', 'Relay server': 'Aktarma sunucusu', 'Public key': 'Açık anahtar',
  'Public address of this console': 'Bu konsolun dış adresi', 'Shown to users and used to build the client configuration.': 'Kullanıcılara gösterilir ve istemci yapılandırmasını oluşturmak için kullanılır.',
  'Client configuration': 'İstemci yapılandırması', 'Paste this into RustDesk under Settings, Network, ID/Relay server, Import.': 'Bunu RustDesk\'te Ayarlar, Ağ, ID/Aktarma sunucusu, İçe aktar bölümüne yapıştır.',
  'Sign-in providers': 'Giriş sağlayıcıları', 'Add provider': 'Sağlayıcı ekle', 'Issuer': 'Yayıncı (issuer)', 'Client ID': 'İstemci kimliği', 'Client secret': 'İstemci gizli anahtarı',
  'Leave empty to keep the stored secret.': 'Kayıtlı gizli anahtarı korumak için boş bırak.', 'Create accounts for new people who sign in': 'Giriş yapan yeni kişiler için hesap aç',
  'Redirect address to register at the provider': 'Sağlayıcıya kaydedilecek yönlendirme adresi', 'No provider configured': 'Sağlayıcı ayarlanmadı',
  'Delete the provider {0}? People linked through it lose that sign-in.': '{0} sağlayıcısı silinsin mi? Onunla bağlı kişiler o girişi kaybeder.',
  'Your password': 'Parolan', 'Current password': 'Mevcut parola', 'Change password': 'Parolayı değiştir', 'Password changed': 'Parola değişti', 'Saved': 'Kaydedildi',
  'Language': 'Dil', 'devices': 'cihaz', 'online': 'çevrimiçi', 'users': 'kullanıcı', 'address book entries': 'adres defteri kaydı', 'Overview': 'Genel durum',
  'All address books': 'Tüm adres defterleri',
  'API tokens': 'API token\'ları', 'New API token': 'Yeni API token\'ı', 'For scripts and for rustdesk --assign. The token is shown once.': 'Betikler ve rustdesk --assign için. Token yalnız bir kez gösterilir.',
  'Copy the token now. It cannot be shown again.': 'Token\'ı şimdi kopyala. Bir daha gösterilemez.', 'Done': 'Tamam', 'No API token yet': 'Henüz API token\'ı yok', 'Revoke': 'İptal et',
  'Sign-ins': 'Girişler', 'Alarms': 'Alarmlar', 'Result': 'Sonuç', 'Signed in': 'Giriş yapıldı', 'Refused': 'Reddedildi', 'Method': 'Yöntem', 'Event': 'Olay', 'Details': 'Ayrıntı',
  'No sign-ins recorded yet': 'Henüz giriş kaydı yok', 'No alarms. Devices report blocked addresses and repeated failed attempts here.': 'Alarm yok. Cihazlar engellenen adresleri ve yinelenen başarısız denemeleri buraya bildirir.',
  'Blocked by the address whitelist': 'Adres beyaz listesi engelledi', 'More than 30 failed attempts': '30\'dan fazla başarısız deneme', 'Six failed attempts within a minute': 'Bir dakikada altı başarısız deneme',
  'Repeated attempts from one IPv6 range': 'Tek IPv6 aralığından yinelenen denemeler', 'Terminal sign-in attempts throttled': 'Terminal giriş denemeleri yavaşlatıldı', 'Too many terminal sign-ins at once': 'Aynı anda çok fazla terminal girişi',
  'Session went beyond its permission': 'Oturum iznini aştı', 'Blocked by the ID whitelist': 'ID beyaz listesi engelledi', 'Security event {0}': 'Güvenlik olayı {0}',
  'Set owner': 'Sahibini belirle', 'Nobody': 'Kimse', 'Set group': 'Grubu belirle', 'Group': 'Grup', 'Devices with the same group name are listed together in the RustDesk app.': 'Aynı grup adındaki cihazlar RustDesk uygulamasında birlikte listelenir.',
  'End session {0}': '{0} numaralı oturumu sonlandır', 'The device closes the session within a few seconds.': 'Cihaz oturumu birkaç saniye içinde kapatır.', '{0} active': '{0} etkin',
  'Devices and logs': 'Cihazlar ve kayıtlar', 'Let installations enrol themselves': 'Kurulumlar kendini kaydedebilsin',
  'A custom RustDesk client can name its owner, group and address book (preset options). Leave off unless you deploy such clients.': 'Özel bir RustDesk istemcisi sahibini, grubunu ve adres defterini bildirebilir (preset seçenekleri). Böyle istemciler dağıtmıyorsan kapalı bırak.',
  'Keep logs for (days)': 'Kayıtları sakla (gün)', 'Empty keeps them forever.': 'Boş bırakılırsa süresiz saklanır.',
  'Options sent to every device': 'Tüm cihazlara gönderilen ayarlar', 'One option per line as name=value. An empty value removes the option on the devices.': 'Her satıra bir ayar: ad=değer. Boş değer, ayarı cihazlarda kaldırır.',
  'Default password': 'Varsayılan parola', 'Offered by the app for entries of this book that have no password of their own. Stored as plain text.': 'Bu defterde kendi parolası olmayan kayıtlar için uygulamanın kullandığı parola. Düz metin saklanır.', 'Version {0}': 'Sürüm {0}', 'More': 'Diğer', 'Sign-in': 'Giriş', 'Role': 'Rol', 'User': 'Kullanıcı',
};
let lang = localStorage.getItem('rdl-lang') || ((navigator.language || 'en').toLowerCase().startsWith('tr') ? 'tr' : 'en');
function t(s, ...a) {
  const out = (lang === 'tr' && TR[s]) || s;
  return out.replace(/\{(\d)\}/g, (_, i) => a[i] ?? '');
}

// ---------- small DOM helpers ----------
function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (k === 'checked' || k === 'disabled' || k === 'selected' || k === 'open') el[k] = !!v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  el.append(...kids.flat(Infinity).filter(k => k != null && k !== false));
  return el;
}
const ICONS = {
  books: '<path d="M5 4h11a3 3 0 0 1 3 3v13H8a3 3 0 0 1-3-3z"/><path d="M5 17a3 3 0 0 1 3-3h11"/><path d="M9 8h6"/>',
  devices: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14.5a6.5 6.5 0 0 1 3.5 5.5"/>',
  sessions: '<rect x="4" y="10" width="16" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>',
  logs: '<path d="M6 3h9l4 4v14H6z"/><path d="M9 12h7M9 16h7M9 8h3"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>',
  more: '<circle cx="12" cy="5" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="12" cy="19" r="1.2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  out: '<path d="M10 4H5v16h5M15 8l4 4-4 4M19 12H9"/>',
  tiles: '<rect x="4" y="4" width="7" height="7" rx="1.5"/><rect x="13" y="4" width="7" height="7" rx="1.5"/><rect x="4" y="13" width="7" height="7" rx="1.5"/><rect x="13" y="13" width="7" height="7" rx="1.5"/>',
  list: '<path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01"/>',
  // platform glyphs (own drawings, not vendor logos)
  windows: '<path d="M4 5h7v6H4zM13 5h7v6h-7zM4 13h7v6H4zM13 13h7v6h-7z"/>',
  mac: '<rect x="4" y="5" width="16" height="11" rx="1.5"/><path d="M2 19h20"/><path d="M10 19l.5-3M14 19l-.5-3"/>',
  linux: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M12 15h5"/>',
  android: '<rect x="7" y="2.5" width="10" height="19" rx="2.5"/><path d="M11 18.5h2"/>',
  unknown: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
};
function icon(name) {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('aria-hidden', 'true');
  s.innerHTML = ICONS[name] || ICONS.unknown;
  return s;
}
function platformIcon(p) {
  p = (p || '').toLowerCase();
  return icon(p.includes('win') ? 'windows' : p.includes('mac') || p.includes('ios') ? 'mac' : p.includes('linux') ? 'linux' : p.includes('android') ? 'android' : 'unknown');
}
function hash(s) { let x = 7; for (const c of String(s)) x = (x * 31 + c.codePointAt(0)) >>> 0; return x; }
const peerColor = (id, platform) => `hsl(${hash(id + '|' + (platform || '')) % 360} 52% 44%)`;
const PALETTE = [0xFFE5484D, 0xFFF76B15, 0xFFFFB224, 0xFF46A758, 0xFF12A594, 0xFF00A2C7, 0xFF0071FF, 0xFF5B5BD6, 0xFF8E4EC6, 0xFFD6409F, 0xFF8D8D8D, 0xFF5F6D7E];
const tagColor = tg => '#' + (((tg.color ? tg.color : PALETTE[hash(tg.name) % PALETTE.length]) >>> 0) & 0xFFFFFF).toString(16).padStart(6, '0');

function toast(msg) {
  document.querySelector('.toast')?.remove();
  const el = h('div', { class: 'toast', role: 'status' }, msg);
  document.body.append(el);
  setTimeout(() => el.remove(), 2600);
}
async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast(t('Copied')); } catch { prompt(t('Copy'), text); }
}
function ago(ts) {
  if (!ts) return t('never');
  const s = Math.max(0, Date.now() / 1000 - ts);
  if (s < 90) return t('just now');
  if (s < 3600) return t('{0} min ago', Math.round(s / 60));
  if (s < 86400) return t('{0} h ago', Math.round(s / 3600));
  return t('{0} d ago', Math.round(s / 86400));
}
const when = ts => ts ? new Date(ts * 1000).toLocaleString(lang, { dateStyle: 'short', timeStyle: 'short' }) : '';
function span(sec) {
  if (sec < 60) return sec + ' s';
  if (sec < 3600) return Math.round(sec / 60) + ' min';
  return (sec / 3600).toFixed(1) + ' h';
}

// RustDesk IDs may carry their own server: 123456789@host?key=abc
function splitId(full) {
  const m = /^([^@]+)@([^?]+)(?:\?key=(.*))?$/.exec(full || '');
  return m ? { id: m[1], server: m[2], key: m[3] || '' } : { id: full || '', server: '', key: '' };
}
const joinId = p => p.server ? `${p.id}@${p.server}${p.key ? '?key=' + p.key : ''}` : p.id;
const fmtId = id => /^\d+$/.test(id) ? id.replace(/\B(?=(\d{3})+(?!\d))/g, ' ') : id;

// ---------- API ----------
async function api(method, path, body) {
  const res = await fetch(API + path, {
    method, credentials: 'same-origin',
    headers: { 'X-RDL': '1', ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await res.json(); } catch { /* empty body */ }
  if (res.status === 401 && state.user) { state.user = null; render(); }
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// ---------- menus and dialogs ----------
function menu(anchor, items) {
  document.querySelector('.menu')?.remove();
  const el = h('div', { class: 'menu', role: 'menu' }, items.filter(Boolean).map(it => it === '-' ? h('hr') :
    h('button', { type: 'button', role: 'menuitem', class: it.danger && 'danger', onclick: () => { el.remove(); it.run(); } }, it.label)));
  document.body.append(el);
  const r = anchor.getBoundingClientRect();
  el.style.top = Math.max(8, Math.min(r.bottom + 4, innerHeight - el.offsetHeight - 8)) + 'px';
  el.style.left = Math.max(8, Math.min(r.right - el.offsetWidth, innerWidth - el.offsetWidth - 8)) + 'px';
  const close = e => { if (!el.contains(e.target)) { el.remove(); removeEventListener('pointerdown', close, true); } };
  setTimeout(() => addEventListener('pointerdown', close, true));
  el.querySelector('button')?.focus();
  el.addEventListener('keydown', e => { if (e.key === 'Escape') { el.remove(); anchor.focus(); } });
}
// dialog(title, body nodes, onSave(formData) -> may throw, options); resolves true when saved.
function dialog(title, body, onSave, { save = t('Save'), danger = false } = {}) {
  return new Promise(resolve => {
    let done = false;
    // Resolve directly instead of waiting for the dialog's "close" event, which
    // some embedded browsers deliver late or not at all.
    const finish = ok => { if (done) return; done = true; if (d.open) d.close(); d.remove(); resolve(ok); };
    const err = h('div', { class: 'err', role: 'alert' });
    const submit = h('button', { class: 'btn ' + (danger ? 'danger' : 'primary'), type: 'submit' }, save);
    const form = h('form', { method: 'dialog', onsubmit: async e => {
      e.preventDefault();
      submit.disabled = true;
      try { await onSave(new FormData(form), form); finish(true); } catch (ex) { err.textContent = ex.message; submit.disabled = false; }
    } }, h('h2', null, title), h('div', { class: 'body' }, body, err),
    h('div', { class: 'actions' }, h('button', { class: 'btn', type: 'button', onclick: () => finish(false) }, t('Cancel')), submit));
    const d = h('dialog', null, form);
    d.addEventListener('cancel', e => { e.preventDefault(); finish(false); });
    d.addEventListener('close', () => finish(false));
    document.body.append(d);
    d.showModal();
  });
}
const confirmBox = (text, label) => dialog(text, [], async () => {}, { save: label, danger: true });
const field = (label, input, hint) => h('label', { class: 'field' }, h('span', null, label), input, hint && h('small', null, hint));
const text = (name, value = '', extra = {}) => h('input', { type: 'text', name, value, autocomplete: 'off', ...extra });

// ---------- shell ----------
const NAV = [
  { id: 'books', label: 'Address books' }, { id: 'devices', label: 'Devices' }, { id: 'users', label: 'Users', admin: true },
  { id: 'sessions', label: 'Sessions' }, { id: 'logs', label: 'Logs' }, { id: 'settings', label: 'Settings' },
];
const route = () => { const [view, arg] = location.hash.replace(/^#\/?/, '').split('/'); return { view: view || 'books', arg: arg && decodeURIComponent(arg) }; };
const go = (view, arg) => { location.hash = '#/' + view + (arg ? '/' + encodeURIComponent(arg) : ''); };

async function render() {
  const app = document.getElementById('app');
  document.documentElement.lang = lang;
  if (!state.user) { app.replaceChildren(loginView()); return; }
  const { view } = route();
  const items = NAV.filter(n => !n.admin || state.user.is_admin);
  const link = n => h('a', { href: '#/' + n.id, class: view === n.id && 'on', 'aria-current': view === n.id && 'page' }, icon(n.id), h('span', null, t(n.label)));
  const main = h('main', null);
  app.replaceChildren(h('div', { class: 'shell' },
    h('aside', { class: 'side' },
      h('div', { class: 'brand' }, h('img', { src: '/icon.svg', alt: '' }), 'RustDesk API Lite'),
      serverCard(),
      h('nav', { class: 'nav', 'aria-label': 'Main' }, items.map(link)),
      h('div', { class: 'me' }, h('div', { class: 'avatar' }, (state.user.username[0] || '?').toUpperCase()),
        h('div', { class: 'grow' }, h('div', { class: 'trunc' }, state.user.display_name || state.user.username), h('div', { class: 'sub muted trunc' }, state.user.is_admin ? t('Administrator') : state.user.email)),
        h('button', { class: 'icon-btn', title: t('Sign out'), 'aria-label': t('Sign out'), onclick: signOut }, icon('out')))),
    main,
    h('nav', { class: 'tabbar', 'aria-label': 'Main' }, items.filter(n => n.id !== 'sessions').map(link))));
  const views = { books: booksView, devices: devicesView, users: usersView, sessions: sessionsView, logs: logsView, settings: settingsView };
  try { await (views[view] || booksView)(main); } catch (ex) { main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'empty' }, h('b', null, ex.message)))); }
}
function serverCard() {
  const c = state.overview?.client;
  if (!c) return null;
  return h('section', { class: 'server' }, h('h2', null, t('Your server')),
    h('div', { class: 'kv' }, h('span', null, t('ID server')), h('b', null, c.id_server || t('Not set yet'))),
    c.key && h('div', { class: 'kv' }, h('span', null, t('Key')), h('b', { class: 'mono' }, c.key)),
    c.encoded && h('button', { class: 'btn small', onclick: () => copy(c.encoded) }, t('Copy client configuration')));
}
async function signOut() { try { await api('POST', '/logout'); } catch { /* already gone */ } state.user = null; render(); }
async function afterLogin(user) {
  state.user = user;
  try { state.overview = await api('GET', '/overview'); } catch { state.overview = null; }
  render();
}
function loginView() {
  const err = h('div', { class: 'err', role: 'alert' });
  const form = h('form', { onsubmit: async e => {
    e.preventDefault();
    const f = new FormData(form);
    try { afterLogin((await api('POST', '/login', { username: f.get('username'), password: f.get('password') })).user); } catch (ex) { err.textContent = ex.message; }
  } },
    h('div', { class: 'brand' }, h('img', { src: '/icon.svg', alt: '' }), 'RustDesk API Lite'),
    field(t('Username'), h('input', { type: 'text', name: 'username', autocomplete: 'username', required: true, autofocus: true })),
    field(t('Password'), h('input', { type: 'password', name: 'password', autocomplete: 'current-password', required: true })),
    err, h('button', { class: 'btn primary', type: 'submit' }, t('Sign in')),
    state.providers.length ? h('div', { class: 'or' }, t('or')) : null,
    state.providers.map(p => h('a', { class: 'btn', style: null, href: '/oidc/login?provider=' + encodeURIComponent(p), role: 'button' }, t('Continue with {0}', p[0].toUpperCase() + p.slice(1)))));
  for (const a of form.querySelectorAll('a.btn')) { a.style.display = 'block'; a.style.textAlign = 'center'; a.style.marginTop = '8px'; }
  return h('div', { class: 'login' }, form);
}

// ---------- address books ----------
const ab = { guid: null, tags: new Set(), q: '', mode: localStorage.getItem('rdl-view') || 'tiles' };
async function booksView(main) {
  const all = state.user.is_admin && localStorage.getItem('rdl-allbooks') === '1';
  const [{ address_books: books }, { devices }] = await Promise.all([api('GET', '/address-books' + (all ? '?all=1' : '')), api('GET', '/devices')]);
  const { arg } = route();
  const cur = books.find(b => b.guid === arg) || books.find(b => b.guid === ab.guid) || books[0];
  if (cur.guid !== ab.guid) { ab.guid = cur.guid; ab.tags = new Set(); ab.q = ''; }
  const data = await api('GET', '/address-books/' + cur.guid);
  const book = data.book, canEdit = book.rule >= 2;
  const online = new Map(devices.map(d => [d.id, d.online]));
  const bookName = b => b.personal ? (b.owner_id === state.user.id ? t('My address book') : b.owner) : b.name;
  const reload = () => render();

  const tiles = h('div', { class: 'tiles' + (ab.mode === 'list' ? ' as-list' : '') });
  const paint = () => {
    const q = ab.q.trim().toLowerCase();
    const shown = data.peers.filter(p => {
      const tags = p.tags || [];
      if (ab.tags.size && ![...ab.tags].every(x => tags.includes(x))) return false;
      return !q || [p.id, p.alias, p.username, p.hostname, p.note, ...tags].some(v => String(v || '').toLowerCase().includes(q));
    });
    tiles.replaceChildren(...shown.map(peerTile));
    empty.classList.toggle('hide', shown.length > 0);
    empty.replaceChildren(...(data.peers.length
      ? [h('b', null, t('Nothing matches the filter'))]
      : [h('b', null, t('No devices here yet')), t('Add one with its RustDesk ID, or add it from the RustDesk app.')]));
  };
  const empty = h('div', { class: 'empty' });
  const peerTile = p => {
    const s = splitId(p.id), tags = (p.tags || []).map(n => data.tags.find(x => x.name === n) || { name: n, color: 0 });
    // Entries pinned to this very server are still ours: their state is known.
    const own = (state.overview?.client.id_server || '').replace(/:\d+$/, '');
    const st = s.server && s.server.replace(/:\d+$/, '') !== own ? 'unknown' : online.get(s.id) ? 'on' : online.has(s.id) ? '' : 'unknown';
    const art = h('div', { class: 'peer-art' }, platformIcon(p.platform),
      h('div', { class: 'host trunc' }, [p.username, p.hostname].filter(Boolean).join('@') || (s.server || '')),
      h('div', { class: 'tags' }, tags.slice(0, 6).map(tg => { const i = h('i', { title: tg.name }); i.style.background = tagColor(tg); return i; })));
    art.style.background = peerColor(s.id, p.platform);
    const more = h('button', { class: 'icon-btn', 'aria-label': t('More'), onclick: () => menu(more, [
      { label: t('Connect'), run: () => { location.href = 'rustdesk://' + p.id; } },
      { label: t('Copy ID'), run: () => copy(p.id) },
      canEdit && '-', canEdit && { label: t('Edit'), run: () => editPeer(p) },
      canEdit && { label: t('Remove'), danger: true, run: async () => {
        if (await confirmBox(t('Remove {0} from this address book?', p.alias || fmtId(s.id)), t('Remove'))) { await api('POST', `/address-books/${book.guid}/peers/delete`, { ids: [p.id] }); reload(); }
      } }]) }, icon('more'));
    return h('article', { class: 'peer' }, art, h('div', { class: 'peer-foot' },
      h('span', { class: 'dot ' + st, title: st === 'on' ? t('Online') : st === '' ? t('Offline') : '' }),
      h('div', { class: 'grow' }, h('div', { class: 'name trunc' }, p.alias || fmtId(s.id)), h('div', { class: 'sub trunc' }, p.alias ? fmtId(s.id) + (s.server ? ' @ ' + s.server : '') : (s.server || p.note || ''))),
      more));
  };

  async function editPeer(p) {
    const s = splitId(p?.id), last = JSON.parse(localStorage.getItem('rdl-last-server') || '{}');
    const tagBoxes = data.tags.length ? field(t('Tags'), h('div', { class: 'tagpick' }, data.tags.map(tg => {
      const dot = h('i', { class: 'tagdot' }); dot.style.background = tagColor(tg);
      return h('label', null, h('input', { type: 'checkbox', name: 'tag', value: tg.name, checked: (p?.tags || []).includes(tg.name) }), dot, tg.name);
    }))) : null;
    await dialog(p ? t('Edit') : t('Add device'), [
      field(t('ID'), text('id', s.id, { required: true, inputmode: 'numeric', autofocus: !p })),
      field(t('Alias'), text('alias', p?.alias || '')),
      tagBoxes,
      field(t('Platform'), h('select', { name: 'platform' }, ['', 'Windows', 'Mac OS', 'Linux', 'Android'].map(v => h('option', { value: v, selected: (p?.platform || '') === v }, v || t('Unknown'))))),
      field(t('Note'), h('textarea', { name: 'note' }, p?.note || '')),
      h('details', { open: !!s.server }, h('summary', { class: 'muted' }, t('On another server')), h('div', null,
        h('p', { class: 'muted' }, t('Leave both empty for devices on this server.')),
        field(t('Server address'), text('server', p ? s.server : '', { placeholder: last.server || 'rustdesk.example.com' })),
        field(t('Server key'), text('key', p ? s.key : '', { placeholder: last.key || '' })))),
    ], async f => {
      const ns = { id: f.get('id').replace(/\s+/g, ''), server: f.get('server').trim(), key: f.get('key').trim() };
      if (ns.server) localStorage.setItem('rdl-last-server', JSON.stringify({ server: ns.server, key: ns.key }));
      const next = { ...(p || {}), id: joinId(ns), alias: f.get('alias').trim(), platform: f.get('platform'), note: f.get('note').trim(), tags: data.tags.length ? f.getAll('tag') : (p?.tags || []) };
      await api('PUT', `/address-books/${book.guid}/peers`, next);
      if (p && p.id !== next.id) await api('POST', `/address-books/${book.guid}/peers/delete`, { ids: [p.id] });
    }) && reload();
  }
  async function editTag(tg) {
    const cur = tg ? (tg.color || PALETTE[hash(tg.name) % PALETTE.length]) : PALETTE[6];
    const body = [field(t('Name'), text('name', tg?.name || '', { required: true, autofocus: true })),
      field(t('Color'), h('div', { class: 'swatches' }, PALETTE.map(c => { const i = h('i'); i.style.background = tagColor({ color: c });
        return h('label', null, h('input', { type: 'radio', name: 'color', value: c, checked: (c >>> 0) === (cur >>> 0) }), i); }))),
      tg && h('button', { class: 'btn danger small', type: 'button', onclick: async e => {
        await api('POST', `/address-books/${book.guid}/tags/delete`, { names: [tg.name] }); ab.tags.delete(tg.name); e.target.closest('dialog').querySelector('.actions .btn').click(); reload();
      } }, t('Delete tag'))];
    await dialog(tg ? t('Edit tag') : t('New tag'), body, async f => {
      const name = f.get('name').trim();
      await api('PUT', `/address-books/${book.guid}/tags`, { name: tg ? tg.name : name, rename: tg ? name : '', color: Number(f.get('color') || cur) });
      if (tg && ab.tags.delete(tg.name)) ab.tags.add(name);
    }) && reload();
  }
  async function shareBook() {
    const { users } = await api('GET', '/users');
    const others = users.filter(u => u.id !== book.owner_id && !u.disabled);
    const rules = [[0, 'No access'], [1, 'Can view'], [2, 'Can edit'], [3, 'Full control']];
    await dialog(t('Share {0}', book.name), others.length ? others.map(u => field(u.username, h('select', { name: 'u' + u.id },
      rules.map(([v, l]) => h('option', { value: v, selected: (data.shares.find(s => s.user_id === u.id)?.rule || 0) === v }, t(l))))))
      : h('p', { class: 'muted' }, t('There is nobody else to share with yet.')),
    async f => api('PUT', `/address-books/${book.guid}/shares`, { shares: others.map(u => ({ user_id: u.id, rule: Number(f.get('u' + u.id)) })).filter(s => s.rule) })) && reload();
  }
  async function pinServer() {
    const bare = data.peers.filter(p => !splitId(p.id).server), c = state.overview?.client || {};
    await dialog(t('Pin a server on the entries'), [
      h('p', { class: 'muted' }, t('Entries that are a bare ID get the server address and key added, so the RustDesk app connects through that server even while you are signed in.')), h('br'),
      field(t('Server address'), text('server', (c.id_server || '').replace(/:21116$/, ''), { required: true })),
      field(t('Server key'), text('key', c.key || '')),
      !bare.length && h('p', { class: 'muted' }, t('All entries already carry a server.')),
    ], async f => {
      const server = f.get('server').trim(), key = f.get('key').trim();
      for (const p of bare) await api('PUT', `/address-books/${book.guid}/peers`, { ...p, id: joinId({ id: p.id, server, key }) });
      if (bare.length) await api('POST', `/address-books/${book.guid}/peers/delete`, { ids: bare.map(p => p.id) });
    }, { save: t('Pin on {0} entries', bare.length) }) && reload();
  }
  const newBook = async () => {
    let guid;
    await dialog(t('New address book'), [field(t('Name'), text('name', '', { required: true, autofocus: true })), field(t('Note'), text('note'))],
      async f => { guid = (await api('POST', '/address-books', { name: f.get('name'), note: f.get('note') })).guid; }) && go('books', guid);
  };
  const bookMenu = btn => menu(btn, [
    book.rule === 3 && !book.personal && { label: t('Rename'), run: async () => { await dialog(t('Rename'), [field(t('Name'), text('name', book.name, { required: true })), field(t('Note'), text('note', book.note)),
      field(t('Default password'), text('default_password', book.default_password), t('Offered by the app for entries of this book that have no password of their own. Stored as plain text.'))],
      f => api('PATCH', '/address-books/' + book.guid, { name: f.get('name'), note: f.get('note'), default_password: f.get('default_password') })) && reload(); } },
    book.rule === 3 && !book.personal && { label: t('Share'), run: shareBook },
    canEdit && { label: t('Pin a server on the entries'), run: pinServer },
    canEdit && { label: t('New tag'), run: () => editTag() },
    book.rule === 3 && !book.personal && '-',
    book.rule === 3 && !book.personal && { label: t('Delete'), danger: true, run: async () => {
      if (await confirmBox(t('Delete the address book {0} and its {1} entries?', book.name, data.peers.length), t('Delete'))) { await api('DELETE', '/address-books/' + book.guid); ab.guid = null; go('books'); render(); }
    } }]);

  const more = h('button', { class: 'icon-btn', 'aria-label': t('More'), onclick: () => bookMenu(more) }, icon('more'));
  const modeBtn = h('button', { class: 'icon-btn', title: ab.mode === 'list' ? t('Tiles') : t('List'), 'aria-label': ab.mode === 'list' ? t('Tiles') : t('List'), onclick: () => {
    ab.mode = ab.mode === 'list' ? 'tiles' : 'list'; localStorage.setItem('rdl-view', ab.mode); reload();
  } }, icon(ab.mode === 'list' ? 'tiles' : 'list'));
  const tagBtn = tg => {
    const dot = h('i', { class: 'tagdot' }); dot.style.background = tagColor(tg);
    const b = h('button', { class: 'pick' + (ab.tags.has(tg.name) ? ' on' : ''), 'aria-pressed': ab.tags.has(tg.name), onclick: () => {
      ab.tags.has(tg.name) ? ab.tags.delete(tg.name) : ab.tags.add(tg.name); b.classList.toggle('on'); paint();
    }, oncontextmenu: e => { if (canEdit) { e.preventDefault(); editTag(tg); } }, ondblclick: () => canEdit && editTag(tg) }, dot, h('span', { class: 'trunc' }, tg.name));
    return b;
  };
  // Tags used on entries but never given a colour still need to be filterable.
  const known = new Set(data.tags.map(x => x.name));
  for (const p of data.peers) for (const n of p.tags || []) if (!known.has(n)) { known.add(n); data.tags.push({ name: n, color: 0 }); }

  main.replaceChildren(h('div', { class: 'ab' },
    h('div', { class: 'ab-side' },
      h('div', { class: 'group' }, h('h2', null, t('Address books'), h('button', { class: 'icon-btn', title: t('New address book'), 'aria-label': t('New address book'), onclick: newBook }, icon('plus'))),
        books.map(b => h('button', { class: 'pick' + (b.guid === book.guid ? ' on' : ''), onclick: () => go('books', b.guid) }, h('span', { class: 'trunc' }, bookName(b)), h('small', null, b.peer_count))),
        state.user.is_admin && h('label', { class: 'check muted', style: null }, h('input', { type: 'checkbox', checked: all, onchange: e => { localStorage.setItem('rdl-allbooks', e.target.checked ? '1' : '0'); reload(); } }), t('All address books'))),
      h('div', { class: 'group' }, h('h2', null, t('Tags'), canEdit && h('button', { class: 'icon-btn', title: t('New tag'), 'aria-label': t('New tag'), onclick: () => editTag() }, icon('plus'))),
        data.tags.map(tagBtn))),
    h('div', { class: 'ab-main' },
      h('div', { class: 'ab-select' }, h('select', { 'aria-label': t('Address book'), onchange: e => go('books', e.target.value) }, books.map(b => h('option', { value: b.guid, selected: b.guid === book.guid }, `${bookName(b)} (${b.peer_count})`))),
        h('button', { class: 'btn', onclick: newBook, 'aria-label': t('New address book') }, '+')),
      h('div', { class: 'toolbar' }, h('h1', { class: 'trunc' }, bookName(book)),
        book.owner_id !== state.user.id && h('span', { class: 'chip blue' }, t('Shared by {0}', book.owner)),
        !canEdit && h('span', { class: 'chip' }, t('Read only')),
        h('input', { class: 'search', type: 'search', placeholder: t('Search'), 'aria-label': t('Search'), value: ab.q, oninput: e => { ab.q = e.target.value; paint(); } }),
        modeBtn, canEdit && h('button', { class: 'btn primary', onclick: () => editPeer() }, t('Add device')), more),
      tiles, empty)));
  for (const el of main.querySelectorAll('.ab-side .check')) { el.style.margin = '8px'; el.style.fontSize = '12.5px'; }
  paint();
}

// ---------- devices ----------
async function devicesView(main) {
  const { devices } = await api('GET', '/devices');
  const addToBook = async d => {
    const { address_books: books } = await api('GET', '/address-books');
    const mine = books.filter(b => b.rule >= 2);
    await dialog(t('Add to address book'), [field(t('Address book'), h('select', { name: 'guid' }, mine.map(b => h('option', { value: b.guid }, b.personal ? t('My address book') : b.name)))),
      field(t('Alias'), text('alias', d.hostname))],
    f => api('PUT', `/address-books/${f.get('guid')}/peers`, { id: d.id, alias: f.get('alias').trim(), hostname: d.hostname, username: d.username, platform: platformOf(d.os) }), { save: t('Add') }) && toast(t('Added'));
  };
  const row = d => {
    const more = h('button', { class: 'icon-btn', 'aria-label': t('More'), onclick: () => menu(more, [
      { label: t('Connect'), run: () => { location.href = 'rustdesk://' + d.id; } },
      { label: t('Copy ID'), run: () => copy(d.id) },
      { label: t('Add to address book'), run: () => addToBook(d) },
      state.user.is_admin && { label: t('Set owner'), run: async () => {
        const { users } = await api('GET', '/users');
        await dialog(t('Set owner'), [field(t('Owner'), h('select', { name: 'owner' }, h('option', { value: 0 }, t('Nobody')), users.map(u => h('option', { value: u.id, selected: u.id === d.user_id }, u.username))))],
          f => api('PATCH', '/devices/' + encodeURIComponent(d.id), { owner_id: Number(f.get('owner')) })) && render(); } },
      state.user.is_admin && { label: t('Set group'), run: async () => { await dialog(t('Set group'), [field(t('Group'), text('group', d.group), t('Devices with the same group name are listed together in the RustDesk app.'))],
        f => api('PATCH', '/devices/' + encodeURIComponent(d.id), { group: f.get('group') })) && render(); } },
      state.user.is_admin && { label: t('Note'), run: async () => { await dialog(t('Note'), [field(t('Note'), h('textarea', { name: 'note' }, d.note))], f => api('PATCH', '/devices/' + encodeURIComponent(d.id), { note: f.get('note') })) && render(); } },
      ...(state.user.is_admin ? d.conns.map(cid => ({ label: t('End session {0}', cid), danger: true, run: async () => {
        await api('POST', '/devices/' + encodeURIComponent(d.id) + '/disconnect', { conn_id: cid }); toast(t('The device closes the session within a few seconds.')); } })) : []),
      state.user.is_admin && '-',
      state.user.is_admin && { label: t('Forget'), danger: true, run: async () => { if (await confirmBox(t('Forget {0}? It comes back when it reports again.', d.hostname || fmtId(d.id)), t('Forget'))) { await api('DELETE', '/devices/' + encodeURIComponent(d.id)); render(); } } },
    ]) }, icon('more'));
    const art = h('div', { class: 'peer-art' }, platformIcon(d.os)); art.style.cssText = `background:${peerColor(d.id, platformOf(d.os))};width:40px;height:40px;border-radius:6px;padding:0;flex:none`;
    art.querySelector('svg').style.cssText = 'width:20px;height:20px';
    return h('div', null, art,
      h('div', { class: 'grow' }, h('div', { class: 'row' }, h('span', { class: 'dot ' + (d.online ? 'on' : '') }), h('b', { class: 'trunc' }, d.hostname || fmtId(d.id)), d.user && h('span', { class: 'chip blue' }, d.user),
          d.group && h('span', { class: 'chip' }, d.group), d.conns.length > 0 && h('span', { class: 'chip blue' }, t('{0} active', d.conns.length))),
        h('div', { class: 'sub trunc' }, [fmtId(d.id), d.username, d.os, d.version && 'v' + d.version].filter(Boolean).join(', ')),
        d.note && h('div', { class: 'sub trunc' }, d.note)),
      h('div', { class: 'sub wide-only' }, d.online ? t('Online') : t('Last seen {0}', ago(d.last_seen))), more);
  };
  const on = devices.filter(d => d.online).length;
  main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'head' }, h('h1', null, t('Devices')), h('span', { class: 'muted' }, `${devices.length} ${t('devices')}, ${on} ${t('online')}`)),
    h('div', { class: 'panel list' }, devices.length ? devices.map(row) : h('div', { class: 'empty', style: null }, h('span', null, h('b', null, t('No device has reported to this server yet')), t('Devices appear here once RustDesk on them uses this server as API server.'))))));
  for (const el of main.querySelectorAll('.list > .empty')) el.style.display = 'block';
}
const platformOf = os => { const s = (os || '').toLowerCase(); return s.includes('win') ? 'Windows' : s.includes('mac') ? 'Mac OS' : s.includes('android') ? 'Android' : s.includes('linux') || s.includes('ubuntu') || s.includes('debian') ? 'Linux' : ''; };

// ---------- users ----------
async function usersView(main) {
  const { users } = await api('GET', '/users');
  const edit = async u => {
    await dialog(u ? t('Edit user') : t('New user'), [
      !u && field(t('Username'), text('username', '', { required: true, autofocus: true })),
      field(t('E-mail'), h('input', { type: 'email', name: 'email', value: u?.email || '' })),
      field(t('Display name'), text('display_name', u?.display_name || '')),
      !u && field(t('Password'), h('input', { type: 'password', name: 'password', autocomplete: 'new-password', minlength: 8 }), t('Leave empty for an account that signs in with a provider only.')),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', name: 'is_admin', checked: u?.is_admin }), t('Administrator')),
      u && h('label', { class: 'check' }, h('input', { type: 'checkbox', name: 'disabled', checked: u.disabled }), t('Disabled')),
    ], f => {
      const body = { email: f.get('email'), display_name: f.get('display_name'), is_admin: f.has('is_admin') };
      return u ? api('PATCH', '/users/' + u.id, { ...body, disabled: f.has('disabled') }) : api('POST', '/users', { ...body, username: f.get('username'), password: f.get('password') });
    }) && render();
  };
  const setPw = u => dialog(t('Set password'), [field(t('New password'), h('input', { type: 'password', name: 'password', autocomplete: 'new-password', minlength: 8, required: true }), t('At least 8 characters.'))],
    f => api('POST', `/users/${u.id}/password`, { password: f.get('password') })).then(ok => ok && toast(t('Password changed')));
  const row = u => {
    const more = h('button', { class: 'icon-btn', 'aria-label': t('More'), onclick: () => menu(more, [
      { label: t('Edit'), run: () => edit(u) }, { label: t('Set password'), run: () => setPw(u) },
      u.id !== state.user.id && '-', u.id !== state.user.id && { label: t('Delete'), danger: true, run: async () => {
        if (await confirmBox(t('Delete the user {0}? Their address books are deleted too.', u.username), t('Delete'))) { try { await api('DELETE', '/users/' + u.id); } catch (ex) { toast(ex.message); } render(); }
      } }]) }, icon('more'));
    return h('div', null, h('div', { class: 'avatar' }, u.username[0].toUpperCase()),
      h('div', { class: 'grow' }, h('div', { class: 'row' }, h('b', { class: 'trunc' }, u.username), u.is_admin && h('span', { class: 'chip blue' }, t('Administrator')), u.disabled && h('span', { class: 'chip' }, t('Disabled'))),
        h('div', { class: 'sub trunc' }, [u.display_name, u.email].filter(Boolean).join(', '))),
      h('div', { class: 'row wide-only' }, u.providers.map(p => h('span', { class: 'chip' }, p)), !u.has_password && h('span', { class: 'chip' }, t('No password'))), more);
  };
  main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'head' }, h('h1', null, t('Users')), h('button', { class: 'btn primary', onclick: () => edit() }, t('New user'))),
    h('div', { class: 'panel list' }, users.map(row))));
}

// ---------- sessions ----------
async function sessionsView(main) {
  const all = state.user.is_admin && localStorage.getItem('rdl-allsessions') === '1';
  const { sessions } = await api('GET', '/sessions' + (all ? '?all=1' : ''));
  const row = s => h('div', null, h('div', { class: 'grow' },
    h('div', { class: 'row' }, h('b', { class: 'trunc' }, s.kind === 'api' ? s.name : s.kind === 'console' ? t('Web console') : (s.device_name || t('RustDesk app'))), all && h('span', { class: 'chip blue' }, s.username)),
    h('div', { class: 'sub trunc' }, [s.kind === 'client' ? fmtId(s.device_id) : '', s.kind === 'client' ? s.device_os : '', s.ip, t('Last used {0}', ago(s.last_used_at))].filter(Boolean).join(', '))),
  h('button', { class: 'btn small', onclick: async () => { await api('DELETE', '/sessions/' + s.id); render(); } }, s.kind === 'api' ? t('Revoke') : t('Sign out')));
  const newToken = async () => {
    let token;
    if (!await dialog(t('New API token'), [h('p', { class: 'muted' }, t('For scripts and for rustdesk --assign. The token is shown once.')), h('br'), field(t('Name'), text('name', '', { required: true, autofocus: true }))],
      async f => { token = (await api('POST', '/tokens', { name: f.get('name') })).token; })) return;
    await dialog(t('New API token'), [h('p', { class: 'muted' }, t('Copy the token now. It cannot be shown again.')), h('br'),
      h('div', { class: 'copybox' }, h('span', { class: 'mono grow', style: null }, token), h('button', { class: 'btn small', type: 'button', onclick: () => copy(token) }, t('Copy')))], async () => {}, { save: t('Done') });
    render();
  };
  const apps = sessions.filter(s => s.kind !== 'api'), tokens = sessions.filter(s => s.kind === 'api');
  main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'head' }, h('h1', null, t('Signed-in apps and browsers')),
    state.user.is_admin && h('select', { 'aria-label': t('Sessions'), onchange: e => { localStorage.setItem('rdl-allsessions', e.target.value); render(); } },
      h('option', { value: '0', selected: !all }, t('Mine')), h('option', { value: '1', selected: all }, t('Everyone')))),
  h('div', { class: 'panel list' }, apps.map(row)),
  h('section', { class: 'panel' }, h('header', null, h('h2', null, t('API tokens')), h('button', { class: 'btn small', onclick: newToken }, t('New API token'))),
    h('div', { class: 'list' }, tokens.length ? tokens.map(row) : h('div', { class: 'muted' }, t('No API token yet'))))));
  for (const el of main.querySelectorAll('.head select')) el.style.width = 'auto';
  for (const el of main.querySelectorAll('.copybox .mono')) el.style.wordBreak = 'break-all';
}

// ---------- logs ----------
const logs = { tab: 'connections', offset: 0 };
const ALARMS = { 0: 'Blocked by the address whitelist', 1: 'More than 30 failed attempts', 2: 'Six failed attempts within a minute', 6: 'Repeated attempts from one IPv6 range',
  7: 'Terminal sign-in attempts throttled', 8: 'Too many terminal sign-ins at once', 9: 'Session went beyond its permission', 10: 'Blocked by the ID whitelist' };
async function logsView(main) {
  const size = 50, admin = state.user.is_admin;
  if (!admin) logs.tab = 'logins';
  const { rows, total } = await api('GET', `/logs/${logs.tab}?limit=${size}&offset=${logs.offset}`);
  const tab = (id, label) => h('button', { class: 'btn small' + (logs.tab === id ? ' primary' : ''), onclick: () => { logs.tab = id; logs.offset = 0; render(); } }, t(label));
  const two = (a, b) => h('td', null, h('div', null, a), b && h('div', { class: 'sub muted' }, b));
  const info = raw => { try { const o = JSON.parse(raw); return [o.name, o.id && fmtId(o.id), o.ip].filter(Boolean).join(', '); } catch { return raw; } };
  const defs = {
    connections: { empty: 'No connections recorded yet', cols: ['Device', 'From', 'IP', 'Started', 'Duration', 'Note'], row: r => [
      two(r.device || fmtId(r.device_id), r.device && fmtId(r.device_id)), two(r.peer_name || fmtId(r.peer_id) || '?', r.peer_name && fmtId(r.peer_id)), h('td', { class: 'mono' }, r.ip), h('td', null, when(r.started_at)),
      h('td', null, r.ended_at ? span(r.ended_at - r.started_at) : h('span', { class: 'muted' }, Date.now() / 1000 - r.started_at < 86400 ? t('ongoing') : '')), h('td', null, r.note)] },
    files: { empty: 'No file transfers recorded yet', cols: ['Device', 'From', 'Path', 'Time'], row: r => [h('td', null, fmtId(r.device_id)), h('td', null, fmtId(r.peer_id)), h('td', { class: 'mono' }, r.path), h('td', null, when(r.at))] },
    logins: { empty: 'No sign-ins recorded yet', cols: ['User', 'Result', 'Method', 'Device', 'IP', 'Time'], row: r => [h('td', null, r.username), h('td', null, h('span', { class: 'chip' + (r.ok ? ' blue' : '') }, r.ok ? t('Signed in') : t('Refused'))),
      h('td', null, r.method), two(r.kind === 'console' ? t('Web console') : (r.device_name || t('RustDesk app')), r.device_id && fmtId(r.device_id)), h('td', { class: 'mono' }, r.ip), h('td', null, when(r.at))] },
    alarms: { empty: 'No alarms. Devices report blocked addresses and repeated failed attempts here.', cols: ['Device', 'Event', 'Details', 'Time'], row: r => [two(r.device || fmtId(r.device_id), r.device && fmtId(r.device_id)),
      h('td', null, ALARMS[r.type] ? t(ALARMS[r.type]) : t('Security event {0}', r.type)), h('td', null, info(r.info)), h('td', null, when(r.at))] },
  };
  const d = defs[logs.tab];
  main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'head' }, h('h1', null, t('Logs')),
    admin && [tab('connections', 'Connections'), tab('files', 'File transfers'), tab('logins', 'Sign-ins'), tab('alarms', 'Alarms')]),
  h('div', { class: 'panel' }, rows.length ? h('div', { class: 'scroll-x' }, h('table', null, h('thead', null, h('tr', null, d.cols.map(x => h('th', null, t(x))))), h('tbody', null, rows.map(r => h('tr', null, d.row(r))))))
    : h('div', { class: 'empty' }, h('b', null, t(d.empty)))),
  total > size && h('div', { class: 'row pager' }, h('button', { class: 'btn small', disabled: logs.offset === 0, onclick: () => { logs.offset = Math.max(0, logs.offset - size); render(); } }, t('Newer')),
    h('span', { class: 'muted' }, `${logs.offset + 1}–${Math.min(total, logs.offset + size)} / ${total}`),
    h('button', { class: 'btn small', disabled: logs.offset + size >= total, onclick: () => { logs.offset += size; render(); } }, t('Older')))));
}

// ---------- settings ----------
async function settingsView(main) {
  const admin = state.user.is_admin;
  const [ov, cfg] = await Promise.all([api('GET', '/overview'), admin ? api('GET', '/settings') : null]);
  state.overview = ov;
  const c = ov.counts, panels = [];
  panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Overview')), h('span', { class: 'muted' }, t('Version {0}', ov.version))),
    h('div', { class: 'pad stats' }, [[c.devices, 'devices'], [c.devices_online, 'online'], [c.users, 'users'], [c.peers, 'address book entries']].map(([n, l]) => h('div', null, h('b', null, n), h('span', null, t(l)))))));
  if (ov.client.encoded) panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Client configuration'))),
    h('div', { class: 'pad' }, h('p', { class: 'muted' }, t('Paste this into RustDesk under Settings, Network, ID/Relay server, Import.')), h('br'),
      h('div', { class: 'copybox' }, h('span', { class: 'mono trunc grow' }, ov.client.encoded), h('button', { class: 'btn small', onclick: () => copy(ov.client.encoded) }, t('Copy'))))));
  if (admin) {
    const form = h('form', { class: 'pad', onsubmit: async e => {
      e.preventDefault();
      const f = new FormData(form);
      try { await api('PUT', '/settings', Object.fromEntries(['id_server', 'relay_server', 'server_key', 'public_url'].map(k => [k, f.get(k)]))); toast(t('Saved')); render(); } catch (ex) { toast(ex.message); }
    } },
      field(t('ID server'), text('id_server', cfg.id_server, { placeholder: 'rustdesk.example.com' })),
      field(t('Relay server'), text('relay_server', cfg.relay_server, { placeholder: 'rustdesk.example.com' })),
      field(t('Public key'), text('server_key', cfg.server_key)),
      field(t('Public address of this console'), h('input', { type: 'url', name: 'public_url', value: cfg.public_url, placeholder: location.origin }), t('Shown to users and used to build the client configuration.')),
      h('button', { class: 'btn primary', type: 'submit' }, t('Save')));
    panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Server'))), form));
    const dev = h('form', { class: 'pad', onsubmit: async e => {
      e.preventDefault();
      const f = new FormData(dev), strategy = {};
      for (const line of f.get('strategy').split('\n')) { const i = line.indexOf('='); if (i > 0) strategy[line.slice(0, i).trim()] = line.slice(i + 1).trim(); }
      try { await api('PUT', '/settings', { allow_presets: f.has('allow_presets'), log_days: f.get('log_days'), strategy }); toast(t('Saved')); } catch (ex) { toast(ex.message); }
    } },
      h('label', { class: 'check' }, h('input', { type: 'checkbox', name: 'allow_presets', checked: cfg.allow_presets }), t('Let installations enrol themselves')),
      h('p', { class: 'muted hint' }, t('A custom RustDesk client can name its owner, group and address book (preset options). Leave off unless you deploy such clients.')),
      field(t('Keep logs for (days)'), text('log_days', cfg.log_days, { inputmode: 'numeric' }), t('Empty keeps them forever.')),
      field(t('Options sent to every device'), h('textarea', { name: 'strategy', class: 'mono', rows: 4, placeholder: 'enable-lan-discovery=N' }, Object.entries(cfg.strategy).map(([k, v]) => k + '=' + v).join('\n')),
        t('One option per line as name=value. An empty value removes the option on the devices.')),
      h('button', { class: 'btn primary', type: 'submit' }, t('Save')));
    panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Devices and logs'))), dev));
    const editProvider = async p => {
      await dialog(p ? p.name : t('Add provider'), [
        !p && field(t('Name'), text('name', 'google', { required: true, pattern: '[a-z0-9_-]+' })),
        field(t('Issuer'), h('input', { type: 'url', name: 'issuer', value: p?.issuer || 'https://accounts.google.com', required: true })),
        field(t('Client ID'), text('client_id', p?.client_id || '', { required: true })),
        field(t('Client secret'), h('input', { type: 'password', name: 'client_secret', autocomplete: 'off', required: !p }), p && t('Leave empty to keep the stored secret.')),
        h('label', { class: 'check' }, h('input', { type: 'checkbox', name: 'auto_register', checked: p?.auto_register }), t('Create accounts for new people who sign in')),
        field(t('Redirect address to register at the provider'), h('div', { class: 'copybox' }, h('span', { class: 'mono trunc grow' }, cfg.oidc_callback), h('button', { class: 'btn small', type: 'button', onclick: () => copy(cfg.oidc_callback) }, t('Copy')))),
      ], f => api('PUT', '/oidc/' + encodeURIComponent(p ? p.name : f.get('name')), { issuer: f.get('issuer'), client_id: f.get('client_id'), client_secret: f.get('client_secret'), auto_register: f.has('auto_register') })) && render();
    };
    panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Sign-in providers')), h('button', { class: 'btn small', onclick: () => editProvider() }, t('Add provider'))),
      h('div', { class: 'list' }, cfg.oidc.length ? cfg.oidc.map(p => h('div', null, h('div', { class: 'grow' }, h('b', null, p.name), h('div', { class: 'sub trunc' }, p.issuer)),
        h('button', { class: 'btn small', onclick: () => editProvider(p) }, t('Edit')),
        h('button', { class: 'btn small danger', onclick: async () => { if (await confirmBox(t('Delete the provider {0}? People linked through it lose that sign-in.', p.name), t('Delete'))) { await api('DELETE', '/oidc/' + encodeURIComponent(p.name)); render(); } } }, t('Delete'))))
        : h('div', { class: 'muted' }, t('No provider configured')))));
  }
  const pw = h('form', { class: 'pad', onsubmit: async e => {
    e.preventDefault();
    const f = new FormData(pw);
    try { await api('POST', '/me/password', { current: f.get('current') || '', new: f.get('new') }); pw.reset(); toast(t('Password changed')); } catch (ex) { toast(ex.message); }
  } },
    state.user.has_password && field(t('Current password'), h('input', { type: 'password', name: 'current', autocomplete: 'current-password', required: true })),
    field(t('New password'), h('input', { type: 'password', name: 'new', autocomplete: 'new-password', minlength: 8, required: true }), t('At least 8 characters.')),
    h('button', { class: 'btn', type: 'submit' }, t('Change password')));
  panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Your password'))), pw));
  const langSel = h('select', { 'aria-label': t('Language'), onchange: e => { lang = e.target.value; localStorage.setItem('rdl-lang', lang); render(); } },
    h('option', { value: 'en', selected: lang === 'en' }, 'English'), h('option', { value: 'tr', selected: lang === 'tr' }, 'Türkçe'));
  langSel.style.width = 'auto';
  panels.push(h('section', { class: 'panel' }, h('header', null, h('h2', null, t('Language')), langSel, h('button', { class: 'btn small', onclick: signOut }, t('Sign out')))));
  main.replaceChildren(h('div', { class: 'page' }, h('div', { class: 'head' }, h('h1', null, t('Settings'))), panels));
}

// ---------- start ----------
addEventListener('hashchange', () => state.user && render());
(async () => {
  try {
    const b = await api('GET', '/bootstrap');
    state.providers = b.providers || [];
    state.version = b.version;
    if (b.user) return afterLogin(b.user);
  } catch { /* show the sign-in form */ }
  render();
})();
