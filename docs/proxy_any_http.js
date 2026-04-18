const decoder = new TextDecoder();
const encoder = new TextEncoder();

function readAllStdin() {
  const chunks = [];
  let total = 0;
  while (true) {
    const buf = new Uint8Array(4096);
    const n = Javy.IO.readSync(0, buf);
    if (!n || n <= 0) break;
    chunks.push(buf.subarray(0, n));
    total += n;
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

function writeJSON(obj) {
  Javy.IO.writeSync(1, encoder.encode(JSON.stringify(obj)));
}

function normalizeHeaders(input) {
  const out = {};
  if (!input || typeof input !== "object") return out;
  for (const [k, v] of Object.entries(input)) {
    if (!k) continue;
    const lk = k.toLowerCase();
    // strip hop-by-hop/request-specific headers
    if (
      lk === "host" ||
      lk === "connection" ||
      lk === "content-length" ||
      lk === "transfer-encoding"
    ) {
      continue;
    }
    out[k] = String(v);
  }
  return out;
}

function joinURL(base, path, rawQuery) {
  const b = String(base || "").replace(/\/+$/, "");
  const p = String(path || "/");
  const fullPath = p.startsWith("/") ? p : "/" + p;
  const q = rawQuery ? "?" + rawQuery : "";
  return b + fullPath + q;
}

function parseQueryString(rawQuery) {
  const map = {};
  const query = String(rawQuery || "").replace(/^\?/, "");
  if (!query) return map;
  for (const pair of query.split("&")) {
    if (!pair) continue;
    const idx = pair.indexOf("=");
    const rawKey = idx >= 0 ? pair.slice(0, idx) : pair;
    const rawVal = idx >= 0 ? pair.slice(idx + 1) : "";
    const key = decodeURIComponent(rawKey.replace(/\+/g, " "));
    const val = decodeURIComponent(rawVal.replace(/\+/g, " "));
    map[key] = val;
  }
  return map;
}

function buildQueryString(params, omitKeys) {
  const omit = new Set((omitKeys || []).map((k) => String(k).toLowerCase()));
  const out = [];
  for (const [k, v] of Object.entries(params || {})) {
    if (!k) continue;
    if (omit.has(String(k).toLowerCase())) continue;
    out.push(encodeURIComponent(k) + "=" + encodeURIComponent(String(v)));
  }
  return out.join("&");
}

function isAllowedUpstreamBase(value) {
  try {
    const u = new URL(String(value || ""));
    return u.protocol === "http:" || u.protocol === "https:";
  } catch (_e) {
    return false;
  }
}

async function main() {
  const raw = readAllStdin();
  let ev = {};
  if (raw.length > 0) {
    try {
      ev = JSON.parse(decoder.decode(raw));
    } catch (_e) {
      ev = {};
    }
  }

  const method = String(ev.method || "GET").toUpperCase();
  const path = String(ev.path || "/");
  const rawQuery = String(ev.raw_query || "");
  const query = parseQueryString(rawQuery);
  const incomingHeaders = normalizeHeaders(ev.headers);

  // Configure upstream target.
  // Priority: GET query param -> header -> default.
  // Example: /fn/proxy/path?upstreamBase=https%3A%2F%2Fapi.example.com
  const queryUpstreamBase = method === "GET" ? (query.upstreamBase || query.upstream_base || "") : "";
  const headerUpstreamBase = incomingHeaders["X-Upstream-Base"] || incomingHeaders["x-upstream-base"] || "";
  const upstreamBase = queryUpstreamBase || headerUpstreamBase || "https://httpbin.org";

  if (!isAllowedUpstreamBase(upstreamBase)) {
    writeJSON({
      continue: true,
      output: {
        status: 400,
        headers: { "content-type": "application/json" },
        body: {
          error: "invalid_upstream_base",
          message: "upstreamBase must be a valid http/https URL"
        }
      }
    });
    return;
  }

  if (typeof fetch !== "function") {
    writeJSON({
      continue: true,
      output: {
        status: 501,
        headers: { "content-type": "application/json" },
        body: {
          error: "fetch_unavailable",
          message: "This Javy runtime does not expose fetch/networking."
        }
      }
    });
    return;
  }

  const forwardedQuery = buildQueryString(query, ["upstreamBase", "upstream_base"]);
  const targetURL = joinURL(upstreamBase, path, forwardedQuery);

  // Prevent leaking control header upstream.
  delete incomingHeaders["X-Upstream-Base"];
  delete incomingHeaders["x-upstream-base"];

  const init = {
    method,
    headers: incomingHeaders
  };

  // attach body only for methods that usually carry one
  if (method !== "GET" && method !== "HEAD" && typeof ev.body === "string") {
    init.body = ev.body;
  }

  try {
    const upstreamResp = await fetch(targetURL, init);
    const bodyText = await upstreamResp.text();

    // You can pass through more headers if you want.
    const outHeaders = {
      "content-type": upstreamResp.headers.get("content-type") || "text/plain; charset=utf-8"
    };

    writeJSON({
      continue: true,
      output: {
        status: upstreamResp.status,
        headers: outHeaders,
        body: bodyText
      }
    });
  } catch (err) {
    writeJSON({
      continue: true,
      output: {
        status: 502,
        headers: { "content-type": "application/json" },
        body: {
          error: "upstream_fetch_failed",
          message: String(err && err.message ? err.message : err)
        }
      }
    });
  }
}

main().catch((err) => {
  writeJSON({
    continue: true,
    output: {
      status: 500,
      headers: { "content-type": "application/json" },
      body: {
        error: "proxy_runtime_error",
        message: String(err && err.message ? err.message : err)
      }
    }
  });
});
