"use strict";
// Web console for the solaris-sparc64 tmux session (served by vmweb).

const $ = (sel) => document.querySelector(sel);

const term = new Terminal({
  cols: 80,
  rows: 24,
  fontFamily: 'ui-monospace, Menlo, "DejaVu Sans Mono", Consolas, monospace',
  fontSize: 15,
  cursorBlink: true,
  scrollback: 5000,
  theme: { background: "#121212" },
});
term.open($("#term"));
term.focus();

function b64bytes(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function post(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "X-VMWeb": "1", "Content-Type": "application/octet-stream" },
    body,
  });
  return r;
}

// Keystrokes: one request in flight at a time, so they arrive in order.
const encoder = new TextEncoder();
let queued = [];
let sending = false;
async function flush() {
  if (sending || queued.length === 0) return;
  sending = true;
  const chunk = new Uint8Array(queued.reduce((n, c) => n + c.length, 0));
  let off = 0;
  for (const c of queued) { chunk.set(c, off); off += c.length; }
  queued = [];
  try {
    const r = await post("api/input", chunk);
    if (!r.ok) toast("Console input failed: " + (await r.text()).trim(), true);
  } catch (e) {
    toast("Console input failed: " + e.message, true);
  }
  sending = false;
  flush();
}
term.onData((d) => { queued.push(encoder.encode(d)); flush(); });
term.onBinary((d) => { queued.push(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)); flush(); });

function toast(text, error = false) {
  if (!text) return;
  const el = document.createElement("div");
  el.className = "toast" + (error ? " error" : "");
  el.textContent = text;
  $("#toasts").append(el);
  setTimeout(() => el.remove(), error ? 7000 : 3500);
}

// Server events: console output, snapshots of the screen, size and VM status.
let vm = null;
let session = false;
let connected = false;

function connect() {
  const es = new EventSource("api/events");
  es.addEventListener("open", () => { connected = true; render(); });
  es.addEventListener("error", () => { connected = false; render(); });
  es.addEventListener("size", (e) => {
    const { cols, rows } = JSON.parse(e.data);
    if (cols !== term.cols || rows !== term.rows) term.resize(cols, rows);
    $("#size-now").textContent = `${cols}×${rows}`;
  });
  es.addEventListener("reset", (e) => {
    term.reset();
    term.write(b64bytes(e.data));
  });
  es.addEventListener("out", (e) => term.write(b64bytes(e.data)));
  es.addEventListener("status", (e) => {
    const s = JSON.parse(e.data);
    session = s.session;
    vm = s.vm;
    render();
  });
}

function vmState() {
  if (!connected) return "offline";
  if (!session) return "stopped";
  if (!vm || vm.status === "stopped") return "restarting";
  return vm.status;
}

function render() {
  const state = vmState();
  const pill = $("#state");
  pill.dataset.state = state;
  pill.textContent = state;
  const up = state === "running" || state === "paused";

  const off = $("#offline");
  off.hidden = connected && session;
  $("#offline-text").textContent = !connected
    ? "Disconnected from vmweb, retrying…"
    : "No VM session (start the container with the \"session\" command).";

  const cd = vm && vm.cd ? vm.cd.replace(/^\/vm\//, "") : "";
  $("#cd-now").textContent = up ? "CD: " + (cd || "empty") : "";

  const sel = $("#iso");
  const isos = (vm && vm.isos) || [];
  const prev = sel.value;
  sel.replaceChildren(...isos.map((f) => {
    const o = document.createElement("option");
    o.value = f;
    o.textContent = (f === (vm && vm.cd) ? "● " : "") + f.replace(/^\/vm\//, "");
    return o;
  }));
  if (isos.length === 0) sel.append(new Option("no .iso files in /vm", ""));
  sel.value = isos.includes(prev) ? prev : (vm && isos.includes(vm.cd) ? vm.cd : isos[0] || "");

  $("#pause").textContent = state === "paused" ? "Resume" : "Pause";

  const list = $("#snaps");
  const snaps = (vm && vm.snapshots) || [];
  list.replaceChildren(...snaps.map((s) => {
    const li = document.createElement("li");
    const label = document.createElement("div");
    label.className = "label";
    const name = document.createElement("div");
    name.className = "name";
    name.textContent = s.name;
    name.title = s.name;
    const date = document.createElement("div");
    date.className = "date";
    date.textContent = new Date(s.time * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
    label.append(name, date);
    const restore = document.createElement("button");
    restore.textContent = "Restore";
    restore.onclick = () => run("snapshot-load", s.name, `Power cycle back to snapshot "${s.name}"? Changes since then are lost.`);
    const del = document.createElement("button");
    del.textContent = "Delete";
    del.onclick = () => run("snapshot-delete", s.name, `Delete snapshot "${s.name}"?`);
    li.append(label, restore, del);
    return li;
  }));
  if (snaps.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "No snapshots";
    list.append(li);
  }

  for (const el of document.querySelectorAll("aside button, aside select, aside input")) {
    el.disabled = !up || busy;
  }
  if (up && !busy) $("#cd-insert").disabled = isos.length === 0;
}

let busy = false;
async function run(action, arg = "", confirmText = "") {
  if (confirmText && !confirm(confirmText)) return;
  busy = true;
  render();
  try {
    const r = await fetch("api/action", {
      method: "POST",
      headers: { "X-VMWeb": "1", "Content-Type": "application/json" },
      body: JSON.stringify({ action, arg }),
    });
    const res = r.ok ? await r.json() : { ok: false, message: (await r.text()).trim() };
    toast(res.message || (res.ok ? "Done" : "Failed"), !res.ok);
  } catch (e) {
    toast(e.message, true);
  } finally {
    busy = false;
    render();
    term.focus();
  }
}

for (const b of document.querySelectorAll("button[data-action]")) {
  b.addEventListener("click", () => run(b.dataset.action, "", b.dataset.confirm || ""));
}
$("#pause").addEventListener("click", () => run(vmState() === "paused" ? "resume" : "pause"));
$("#cd-insert").addEventListener("click", () => { if ($("#iso").value) run("cd", $("#iso").value); });
$("#snap-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const name = $("#snap-name").value.trim();
  if (name) run("snapshot-save", name).then(() => { $("#snap-name").value = ""; });
});

async function resize(cols, rows) {
  const r = await fetch("api/resize", {
    method: "POST",
    headers: { "X-VMWeb": "1", "Content-Type": "application/json" },
    body: JSON.stringify({ cols, rows }),
  });
  if (!r.ok) toast("Resize failed: " + (await r.text()).trim(), true);
  else if (cols !== 80 || rows !== 24) toast(`Console is ${cols}×${rows}: run "stty rows ${rows} columns ${cols}" in Solaris`);
  term.focus();
}
$("#size-80x24").addEventListener("click", () => resize(80, 24));
$("#size-fit").addEventListener("click", () => {
  // Measure the space the terminal could fill, at the current font size.
  const wrap = $("#term-wrap");
  const screen = term.element.querySelector(".xterm-screen");
  const cw = screen.clientWidth / term.cols;
  const ch = screen.clientHeight / term.rows;
  if (!cw || !ch) return;
  // Leave room for the padding and xterm's scrollbar.
  const cols = Math.floor((wrap.clientWidth - 40) / cw);
  const rows = Math.floor((wrap.clientHeight - 28) / ch);
  resize(Math.max(20, cols), Math.max(5, rows));
});
$("#term-wrap").addEventListener("click", () => term.focus());

connect();
render();
