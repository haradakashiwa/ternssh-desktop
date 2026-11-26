// The web app of ternssh desktop: the window's tabs, servers, quick
// commands and terminals, over one WebSocket. The terminals are the
// window's own sessions: this page shows what they show, at their size,
// and types into them; it never resizes them, which the window does.
"use strict";

const T = {
  en: {
    search: "Search", refresh: "Refresh", loading: "Loading…", noItems: "Nothing here yet",
    noTerminals: "No terminals open", noTerminalsHint: "Choose a server or terminal on the left: it opens here and in the app.",
    close: "Close", reconnect: "Reconnect", offline: "Disconnected from the app, reconnecting…",
    online: "Connected to the app", shownInApp: "shown in the app", notReady: "The terminal is not connected",
    sent: "Sent: ",
    keysCtrl: "Ctrl: applies to the next key. Tap twice to keep it on.", keysAlt: "Alt: applies to the next key. Tap twice to keep it on.",
  },
  zh: {
    search: "搜索", refresh: "刷新", loading: "正在加载…", noItems: "这里还没有内容",
    noTerminals: "没有打开的终端", noTerminalsHint: "在左侧选择服务器或终端：它会在这里和应用中同时打开。",
    close: "关闭", reconnect: "重新连接", offline: "与应用的连接已断开，正在重连…",
    online: "已连接到应用", shownInApp: "应用中正在显示", notReady: "终端未连接",
    sent: "已发送：",
    keysCtrl: "Ctrl：作用于下一个按键，连按两次保持按下", keysAlt: "Alt：作用于下一个按键，连按两次保持按下",
  },
};
let lang = navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en";
const t = (k) => T[lang][k];

const $ = (id) => document.getElementById(id);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
};

// The window's icons (gui/style.go), drawn the same way: 16×16 strokes in
// the text's color.
const ICONS = {
  close: '<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>',
  refresh: '<path d="M13 8a5 5 0 1 1-1.5-3.6"/><path d="M13 2.5v3h-3"/>',
  server: '<rect x="2.5" y="2.5" width="11" height="4.5" rx="1.2"/><rect x="2.5" y="9" width="11" height="4.5" rx="1.2"/><path d="M5 4.75h.01M5 11.25h.01"/>',
  folder: '<path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h2.6l1.4 1.5h5A1.5 1.5 0 0 1 14 6v5.5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 11.5z"/>',
  laptop: '<rect x="3" y="3.5" width="10" height="7" rx="1"/><path d="M1.5 12.5h13"/>',
  terminal: '<rect x="1.5" y="2.5" width="13" height="11" rx="2"/><path d="M4.5 6.5l2 1.5-2 1.5M8 10h3"/>',
  search: '<circle cx="7" cy="7" r="4.5"/><path d="M10.5 10.5L14 14"/>',
  chevron: '<path d="M6 4l4 4-4 4"/>',
  zap: '<path d="M9 1.5L3.5 9h4l-1 5.5L12.5 7h-4z"/>',
  menu: '<path d="M2.5 4h11M2.5 8h11M2.5 12h11"/>',
};
const svgNS = "http://www.w3.org/2000/svg";
function icon(name, size = 16, cls = "") {
  const s = document.createElementNS(svgNS, "svg");
  s.setAttribute("viewBox", "0 0 16 16");
  s.setAttribute("width", size);
  s.setAttribute("height", size);
  s.setAttribute("fill", "none");
  s.setAttribute("stroke", "currentColor");
  s.setAttribute("stroke-width", "1.3");
  s.setAttribute("stroke-linecap", "round");
  s.setAttribute("stroke-linejoin", "round");
  s.setAttribute("aria-hidden", "true");
  s.setAttribute("class", "icon " + cls);
  s.innerHTML = ICONS[name]; // constants of this file, not data
  return s;
}

// What the page shows, which the window does not: the tab and terminal
// chosen here, and the groups folded.
const view = {
  tab: localStorage.getItem("tab") || "home",
  session: JSON.parse(localStorage.getItem("sessions") || "{}"), // by tab
  folded: new Set(JSON.parse(localStorage.getItem("folded") || "[]")),
  query: "",
};
let state = null;
let ws = null;
let watching = ""; // the session whose frames come
let size = { cols: 80, rows: 24 };
const asked = new Set(); // tabs asked to load

function save() {
  localStorage.setItem("tab", view.tab);
  localStorage.setItem("sessions", JSON.stringify(view.session));
  localStorage.setItem("folded", JSON.stringify([...view.folded]));
}

// --- The terminal

const dark = matchMedia("(prefers-color-scheme: dark)");
const themes = {
  dark: { background: "#121416", foreground: "#e8eaec", cursor: "#e8eaec", selectionBackground: "#2fb98555" },
  light: { background: "#fbfbfc", foreground: "#1d2125", cursor: "#1d2125", selectionBackground: "#00815433" },
};
const term = new Terminal({
  fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, "Cascadia Mono", monospace',
  fontSize: 13,
  scrollback: 10000,
  cursorBlink: true,
  allowProposedApi: true,
  theme: dark.matches ? themes.dark : themes.light,
});
dark.addEventListener("change", () => { term.options.theme = dark.matches ? themes.dark : themes.light; });
term.open($("term"));

// Replies to a program's questions (where the cursor is, what the
// terminal is, its colors, focus) come from the window's terminal: this
// one's would reach the program twice.
const reply = /^(\x1b\[[?>]?[\d;]*(\$y|[cRnt])|\x1b\[\?[\d;]*u|\x1b\[[IO]|\x1b\][\d;]+;rgb:[^\x07\x1b]*(\x07|\x1b\\)|\x1bP[\s\S]*\x1b\\)$/;
term.onData((d) => {
  if (!watching || reply.test(d)) return;
  send({ type: "input", id: watching, text: modify(d) });
});
term.onBinary((d) => {
  if (!watching) return;
  send({ type: "input", id: watching, data: btoa(d) });
});

// fit sets the font size for the terminal's columns to fit the page's
// width, between 9 and 14 pixels; narrower pages scroll.
let charRatio = 0;
function fit() {
  if (!charRatio) {
    const c = document.createElement("canvas").getContext("2d");
    c.font = `100px ${term.options.fontFamily}`;
    charRatio = c.measureText("W").width / 100 || 0.6;
  }
  const w = $("screen").clientWidth - 22;
  const fs = Math.max(9, Math.min(14, Math.floor((w / size.cols / charRatio) * 10) / 10));
  if (term.options.fontSize !== fs) term.options.fontSize = fs;
}
addEventListener("resize", fit);

// The key bar: the keys a phone's keyboard lacks. Ctrl and Alt apply to
// the next key, from the bar or the keyboard; tapped twice, they stay on
// until tapped again.
const mods = { ctrl: 0, alt: 0 }; // 0 off, 1 the next key, 2 locked
const KEYS = [
  { label: "Esc", seq: "\x1b" },
  { label: "Tab", seq: "\t" },
  { label: "Ctrl", mod: "ctrl" },
  { label: "Alt", mod: "alt" },
  { sep: true },
  { label: "↑", csi: "A" }, { label: "↓", csi: "B" }, { label: "←", csi: "D" }, { label: "→", csi: "C" },
  { label: "Home", csi: "H" }, { label: "End", csi: "F" },
  { label: "PgUp", tilde: 5 }, { label: "PgDn", tilde: 6 },
  { sep: true },
  { label: "^C", seq: "\x03" }, { label: "^D", seq: "\x04" }, { label: "^Z", seq: "\x1a" }, { label: "^L", seq: "\x0c" }, { label: "^R", seq: "\x12" },
  { sep: true },
  ...["~", "/", "|", "-", "_", "*", "&", "$", "'", '"', "`", "\\", "[", "]", "{", "}", "<", ">"].map((c) => ({ label: c, seq: c })),
];

// modify applies Ctrl and Alt to typed text, as a terminal does to a key:
// Ctrl turns a letter or one of @[\]^_ into its control character.
function modify(d) {
  if (!mods.ctrl && !mods.alt) return d;
  if (mods.ctrl && d.length === 1) {
    const c = d.toUpperCase().charCodeAt(0);
    if (c >= 64 && c <= 95) d = String.fromCharCode(c & 0x1f);
    else if (d === " " || d === "2") d = "\x00";
    else if (d === "?" || d === "8") d = "\x7f";
  }
  if (mods.alt) d = "\x1b" + d;
  used();
  return d;
}

// keySeq is what a key of the bar sends, with Ctrl and Alt, as xterm's
// keys are: ESC [ 1 ; m A, where m is 1 + 2 for Alt + 4 for Ctrl.
function keySeq(k) {
  const m = 1 + (mods.alt ? 2 : 0) + (mods.ctrl ? 4 : 0);
  let seq;
  if (k.csi) {
    if (m > 1) seq = `\x1b[1;${m}${k.csi}`;
    else seq = (term.modes.applicationCursorKeysMode ? "\x1bO" : "\x1b[") + k.csi;
  } else if (k.tilde) {
    seq = m > 1 ? `\x1b[${k.tilde};${m}~` : `\x1b[${k.tilde}~`;
  } else {
    return modify(k.seq);
  }
  used();
  return seq;
}

// used turns off a modifier that was for one key.
function used() {
  for (const k in mods) if (mods[k] === 1) mods[k] = 0;
  paintKeys();
}

function paintKeys() {
  for (const b of $("keys").querySelectorAll("[data-mod]")) {
    const v = mods[b.dataset.mod];
    b.classList.toggle("on", v > 0);
    b.classList.toggle("lock", v === 2);
    b.title = t(b.dataset.mod === "ctrl" ? "keysCtrl" : "keysAlt");
  }
}

{
  const bar = $("keys");
  let repeat = 0;
  for (const k of KEYS) {
    if (k.sep) {
      bar.append(el("span", "sep"));
      continue;
    }
    const b = el("button", "key", k.label);
    b.tabIndex = -1;
    if (k.mod) b.dataset.mod = k.mod;
    const press = () => {
      if (k.mod) {
        mods[k.mod] = (mods[k.mod] + 1) % 3;
        return paintKeys();
      }
      if (watching) send({ type: "input", id: watching, text: keySeq(k) });
    };
    // Pressing keeps the focus in the terminal, and the phone's keyboard
    // up; held, the arrows and the like repeat.
    b.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      press();
      if (!k.csi && !k.tilde) return;
      clearTimeout(repeat);
      const again = () => { press(); repeat = setTimeout(again, 60); };
      repeat = setTimeout(again, 450);
    });
    for (const ev of ["pointerup", "pointerleave", "pointercancel"]) b.addEventListener(ev, () => clearTimeout(repeat));
    bar.append(b);
  }
}

// Touch scrolling, which xterm.js 6 does not do: a vertical drag on the
// terminal scrolls its scrollback, or, in a full-screen program, sends it
// what the wheel would: mouse wheel reports when it asked for the mouse,
// else arrow keys. A sideways drag still scrolls a page too narrow for the
// terminal. A flick keeps going, slowing down.
{
  const screen = $("screen");
  let last = null, axis = null, speed = 0, fling = 0, rest = 0;
  // scroll scrolls dy pixels, up for negative, where the finger is (x, y).
  const scroll = (x, y, dy) => {
    const box = document.querySelector("#term .xterm-screen").getBoundingClientRect();
    const lineH = box.height / term.rows || 16;
    rest += dy;
    const n = Math.trunc(rest / lineH);
    if (!n) return;
    rest -= n * lineH;
    if (term.buffer.active.type === "normal") return term.scrollLines(n);
    if (!watching) return;
    let seq;
    if (term.modes.mouseTrackingMode !== "none") {
      const col = Math.max(1, Math.min(term.cols, Math.floor((x - box.left) / (box.width / term.cols)) + 1));
      const row = Math.max(1, Math.min(term.rows, Math.floor((y - box.top) / lineH) + 1));
      seq = `\x1b[<${n < 0 ? 64 : 65};${col};${row}M`;
    } else {
      seq = (term.modes.applicationCursorKeysMode ? "\x1bO" : "\x1b[") + (n < 0 ? "A" : "B");
    }
    send({ type: "input", id: watching, text: seq.repeat(Math.abs(n)) });
  };
  screen.addEventListener("touchstart", (e) => {
    cancelAnimationFrame(fling);
    if (e.touches.length !== 1) return (last = null);
    const p = e.touches[0];
    last = { x: p.clientX, y: p.clientY, t: e.timeStamp, x0: p.clientX, y0: p.clientY };
    axis = null;
    speed = rest = 0;
  }, { passive: true });
  screen.addEventListener("touchmove", (e) => {
    if (!last || e.touches.length !== 1) return;
    const p = e.touches[0];
    if (!axis) {
      const dx = Math.abs(p.clientX - last.x0), dy = Math.abs(p.clientY - last.y0);
      if (dx < 6 && dy < 6) return;
      axis = dy >= dx ? "y" : "x";
    }
    if (axis !== "y") return;
    e.preventDefault();
    const dy = last.y - p.clientY, dt = Math.max(1, e.timeStamp - last.t);
    speed = 0.8 * (dy / dt) + 0.2 * speed;
    scroll(p.clientX, p.clientY, dy);
    last = { ...last, x: p.clientX, y: p.clientY, t: e.timeStamp };
  }, { passive: false });
  screen.addEventListener("touchend", () => {
    // Full-screen programs get no flick: it would type keys on and on.
    if (!last || axis !== "y" || Math.abs(speed) < 0.3 || term.buffer.active.type !== "normal") return (last = null);
    const { x, y } = last;
    let v = speed * 16, prev = performance.now();
    last = null;
    const step = (now) => {
      const frames = (now - prev) / 16;
      prev = now;
      v *= Math.pow(0.95, frames);
      if (Math.abs(v) < 0.5) return;
      scroll(x, y, v * frames);
      fling = requestAnimationFrame(step);
    };
    fling = requestAnimationFrame(step);
  });
}

function b64(s) {
  const bin = atob(s || "");
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function frame(m) {
  if (m.id !== watching) return;
  if (m.reset) {
    size = { cols: m.cols, rows: m.rows };
    term.reset();
    term.resize(m.cols, m.rows);
    fit();
  }
  if (m.data) term.write(b64(m.data));
}

function watch(id) {
  if (id === watching) return;
  watching = id;
  term.reset();
  send({ type: "watch", id });
  if (id) setTimeout(() => term.focus(), 0);
}

// --- The connection

function connect() {
  ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`);
  ws.onopen = () => {
    $("link").classList.add("on");
    $("link").title = t("online");
    asked.clear();
    if (watching) send({ type: "watch", id: watching });
  };
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.type === "state") {
      state = m.state;
      render();
    } else if (m.type === "term") {
      frame(m);
    }
  };
  ws.onclose = () => {
    $("link").classList.remove("on");
    $("link").title = t("offline");
    // Sign in again first: the session cookie the WebSocket goes in with
    // lapses, or the app restarted. The browser sends the username and
    // password it keeps, and asks for them only when they are wrong.
    setTimeout(() => fetch("/", { method: "HEAD", cache: "no-store" }).catch(() => {}).finally(connect), 1500);
  };
}

function send(m) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(m));
}

function act(op, args) {
  send({ type: "action", action: { op, ...args } });
}

let toastTimer;
function toast(text) {
  const e = $("toast");
  e.textContent = text;
  e.classList.add("on");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => e.classList.remove("on"), 1800);
}

// --- Drawing what the window has

function render() {
  if (state.language) lang = state.language.startsWith("zh") ? "zh" : "en";
  document.documentElement.lang = lang === "zh" ? "zh-Hans" : "en";
  $("search").placeholder = t("search");
  $("refresh").title = t("refresh");

  let tab = state.tabs.find((x) => x.id === view.tab);
  if (!tab) {
    tab = state.tabs[0];
    view.tab = tab.id;
  }
  if ((!tab.loaded || !tab.commandsLoaded) && !tab.loading && !asked.has(tab.id)) {
    asked.add(tab.id);
    act("load", { tab: tab.id });
  }
  renderTabs(tab);
  renderTree(tab);
  const sess = pickSession(tab);
  renderSessions(tab, sess);
  renderCommands(tab, sess);
  renderStatus(sess);
  $("empty").classList.toggle("hidden", !!sess);
  $("screen").classList.toggle("hidden", !sess);
  $("keys").classList.toggle("hidden", !sess);
  paintKeys();
  if (!sess) {
    $("empty").replaceChildren(icon("terminal", 40, "big"), el("b", "", t("noTerminals")), el("span", "", t("noTerminalsHint")));
  }
  watch(sess ? sess.id : "");
  save();
}

function renderTabs(cur) {
  const nav = $("tabs");
  nav.replaceChildren();
  for (const tab of state.tabs) {
    const b = el("button", "tab" + (tab.id === cur.id ? " on" : ""));
    b.setAttribute("role", "tab");
    if (tab.home) b.append(icon("laptop", 14, "muted"));
    b.append(el("span", "", tab.name));
    if (tab.sessions && tab.sessions.length) b.append(el("span", "count", String(tab.sessions.length)));
    b.onclick = () => {
      view.tab = tab.id;
      document.body.classList.remove("side-open");
      render();
    };
    nav.append(b);
  }
}

function matches(item, q) {
  if (!q) return true;
  if ((item.name + " " + (item.detail || "")).toLowerCase().includes(q)) return true;
  return (item.children || []).some((c) => matches(c, q));
}

function renderTree(tab) {
  const tree = $("tree");
  const msg = $("sideMsg");
  msg.className = "side-msg" + (tab.err ? " err" : "");
  msg.textContent = tab.err || (tab.loading ? t("loading") : "");
  $("refresh").style.visibility = tab.home ? "hidden" : "visible";
  const open = new Set((tab.sessions || []).map((s) => s.key));
  const q = view.query.trim().toLowerCase();
  const list = (items) => {
    const box = document.createDocumentFragment();
    for (const it of items || []) {
      if (!matches(it, q)) continue;
      if (it.kind === "group") {
        const g = el("div", "group" + (view.folded.has(tab.id + "/" + it.key) && !q ? " closed" : ""));
        const r = el("div", "row");
        r.append(icon("chevron", 10, "chev"), icon("folder", 14, "muted"));
        const txt = el("div", "txt");
        txt.append(el("div", "name", it.name));
        r.append(txt);
        r.onclick = () => {
          const k = tab.id + "/" + it.key;
          view.folded.has(k) ? view.folded.delete(k) : view.folded.add(k);
          render();
        };
        const kids = el("div", "kids");
        kids.append(list(it.children));
        g.append(r, kids);
        box.append(g);
        continue;
      }
      const r = el("div", "row");
      r.title = it.detail || "";
      r.append(icon(it.kind === "terminal" ? "terminal" : "server", 16, "muted"));
      const txt = el("div", "txt");
      txt.append(el("div", "name", it.name));
      if (it.detail) txt.append(el("div", "detail", it.detail));
      r.append(txt);
      if (open.has(it.key)) r.append(el("span", "open-dot"));
      r.onclick = () => {
        // Its terminal, when one is open; another one otherwise.
        const sess = (tab.sessions || []).find((s) => s.key === it.key);
        document.body.classList.remove("side-open");
        if (sess) {
          view.session[tab.id] = sess.id;
          render();
          return;
        }
        pendingOpen = { tab: tab.id, before: new Set((tab.sessions || []).map((s) => s.id)) };
        act("open", { tab: tab.id, key: it.key });
      };
      box.append(r);
    }
    return box;
  };
  tree.replaceChildren(list(tab.items));
  if (!tree.children.length && !tab.loading && !tab.err) tree.append(el("div", "side-msg", t("noItems")));
}

// pendingOpen is a terminal this page opened, to show once it comes.
let pendingOpen = null;

function pickSession(tab) {
  const list = tab.sessions || [];
  if (pendingOpen && pendingOpen.tab === tab.id) {
    const fresh = list.find((s) => !pendingOpen.before.has(s.id));
    if (fresh) {
      view.session[tab.id] = fresh.id;
      pendingOpen = null;
    }
  }
  let sess = list.find((s) => s.id === view.session[tab.id]);
  if (!sess) sess = list.find((s) => s.shown) || list[list.length - 1];
  if (sess) view.session[tab.id] = sess.id;
  return sess;
}

function renderSessions(tab, cur) {
  const bar = $("sessions");
  bar.replaceChildren();
  for (const s of tab.sessions || []) {
    const b = el("div", "stab" + (cur && s.id === cur.id ? " on" : ""));
    b.setAttribute("role", "tab");
    b.title = [s.subtitle, s.agent, s.shown ? t("shownInApp") : ""].filter(Boolean).join("\n");
    b.append(el("span", "dot " + s.status), el("span", "t", s.title));
    if (s.agent) b.append(el("span", "agent", "✳"));
    const x = el("button", "x");
    x.append(icon("close", 10));
    x.title = t("close");
    x.onclick = (e) => {
      e.stopPropagation();
      act("close", { session: s.id });
    };
    b.append(x);
    b.onclick = () => {
      view.session[tab.id] = s.id;
      render();
    };
    bar.append(b);
  }
}

function renderCommands(tab, sess) {
  const box = $("cmds");
  box.replaceChildren();
  if (!sess) return;
  for (const q of tab.commands || []) {
    if (q.builtin && sess.shell) continue;
    const b = el("button", "chip" + (q.builtin ? " builtin" : ""));
    b.append(icon("zap", 12, "zap"), el("span", "", q.label));
    b.title = q.command;
    b.onclick = () => {
      if (sess.status !== "ready") return toast(t("notReady"));
      act("run", { session: sess.id, command: q.command });
      toast(t("sent") + q.label);
      term.focus();
    };
    box.append(b);
  }
}

function renderStatus(sess) {
  const bar = $("status");
  bar.replaceChildren();
  if (!sess) return;
  bar.append(el("span", "dot " + sess.status));
  bar.append(el("span", "", sess.statusText));
  const info = el("span", "grow", sess.subtitle);
  bar.append(info);
  if (sess.err) bar.append(el("span", "err", sess.err));
  if (sess.status === "closed" || sess.status === "error") {
    const b = el("button", "", t("reconnect"));
    b.onclick = () => {
      // Its terminal is replaced by a new one, which this page then shows.
      const tab = state.tabs.find((x) => x.id === view.tab);
      pendingOpen = { tab: view.tab, before: new Set(tab.sessions.map((s) => s.id)) };
      act("reconnect", { session: sess.id });
    };
    bar.append(b);
  }
}

$("search").addEventListener("input", (e) => {
  view.query = e.target.value;
  if (state) render();
});
$("refresh").onclick = () => act("refresh", { tab: view.tab });
$("menu").onclick = () => document.body.classList.toggle("side-open");

$("refresh").append(icon("refresh", 14));
$("menu").append(icon("menu", 16));
$("searchBox").prepend(icon("search", 13, "muted"));

connect();
