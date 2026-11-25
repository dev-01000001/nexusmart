#!/usr/bin/env node
// Temporary reverse proxy for inspect.mjs runs that need a logged-in session.
// Listens on LISTEN_PORT, forwards to 127.0.0.1:UPSTREAM_PORT keeping the hostname (port rewritten
// to the upstream port so the app sees its own host), and merges a per-hostname cookie jar loaded
// from JAR (JSON: { "<hostname>": "name=value; name2=value2" }). Set-Cookie from upstream updates
// the jar in memory so refresh rotation keeps working during the run.
//   LISTEN_PORT=3431 UPSTREAM_PORT=3421 JAR=jar.json node authproxy.mjs
import http from 'node:http';
import { readFileSync } from 'node:fs';

const LISTEN = Number(process.env.LISTEN_PORT || 3431);
const UP = Number(process.env.UPSTREAM_PORT || 3421);
const jarRaw = JSON.parse(readFileSync(process.env.JAR || new URL('./jar.json', import.meta.url), 'utf8'));
const jar = new Map(Object.entries(jarRaw).map(([h, c]) => [h, new Map(c.split(/;\s*/).filter(Boolean).map((kv) => { const i = kv.indexOf('='); return [kv.slice(0, i), kv.slice(i + 1)]; }))]));

http.createServer((req, res) => {
  const hostname = String(req.headers.host || '').split(':')[0];
  const headers = { ...req.headers, host: `${hostname}:${UP}` };
  if (headers.origin) headers.origin = headers.origin.replace(`:${LISTEN}`, `:${UP}`);
  if (headers.referer) headers.referer = headers.referer.replace(`:${LISTEN}`, `:${UP}`);
  const mine = jar.get(hostname);
  if (mine) {
    const merged = new Map(String(req.headers.cookie || '').split(/;\s*/).filter(Boolean).map((kv) => { const i = kv.indexOf('='); return [kv.slice(0, i), kv.slice(i + 1)]; }));
    for (const [k, v] of mine) merged.set(k, v);
    headers.cookie = [...merged].map(([k, v]) => `${k}=${v}`).join('; ');
  }
  const up = http.request({ host: '127.0.0.1', port: UP, method: req.method, path: req.url, headers }, (r) => {
    const out = { ...r.headers };
    if (out.location) out.location = out.location.replace(`:${UP}`, `:${LISTEN}`);
    for (const sc of [].concat(r.headers['set-cookie'] || [])) {
      const [kv] = sc.split(';'); const i = kv.indexOf('=');
      if (mine && i > 0) { const v = kv.slice(i + 1); if (v && !/max-age=0/i.test(sc)) mine.set(kv.slice(0, i), v); }
    }
    res.writeHead(r.statusCode, out);
    r.pipe(res);
  });
  up.on('error', (e) => { res.writeHead(502); res.end(String(e)); });
  req.pipe(up);
}).listen(LISTEN, '127.0.0.1', () => console.log(`authproxy :${LISTEN} -> :${UP}`));
