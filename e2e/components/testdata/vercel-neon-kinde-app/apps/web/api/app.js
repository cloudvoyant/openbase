const crypto = require("node:crypto");

function parseCookies(header) {
  const out = {};
  for (const part of String(header || "").split(";")) {
    const idx = part.indexOf("=");
    if (idx > 0) {
      out[part.slice(0, idx).trim()] = decodeURIComponent(part.slice(idx + 1).trim());
    }
  }
  return out;
}

function signSession(payloadObj) {
  const secret = process.env.KINDE_CLIENT_SECRET || "openbase-fallback";
  const b64 = Buffer.from(JSON.stringify(payloadObj)).toString("base64url");
  const sig = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  return `${b64}.${sig}`;
}

function verifySession(rawCookie) {
  if (!rawCookie || !rawCookie.includes(".")) return null;
  const [b64, sig] = rawCookie.split(".", 2);
  const secret = process.env.KINDE_CLIENT_SECRET || "openbase-fallback";
  const expected = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  if (sig !== expected) return null;
  try {
    return JSON.parse(Buffer.from(b64, "base64url").toString("utf8"));
  } catch {
    return null;
  }
}

async function neonQuery(sql, params = []) {
  const connStr = process.env.DIRECT_URL || process.env.DATABASE_URL || "";
  if (!connStr) throw new Error("DATABASE_URL is not configured");
  const parsed = new URL(connStr);
  const resp = await fetch(`https://${parsed.hostname}/sql`, {
    method: "POST",
    headers: {
      "Neon-Connection-String": connStr,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ query: sql, params }),
  });
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(`Neon SQL HTTP ${resp.status}: ${JSON.stringify(data)}`);
  }
  return data.rows || [];
}

async function ensureNotesSchema() {
  await neonQuery(
    "CREATE TABLE IF NOT EXISTS app_notes (id SERIAL PRIMARY KEY, body TEXT NOT NULL, created_env TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())",
  );
}

async function readBody(req) {
  const chunks = [];
  for await (const c of req) chunks.push(Buffer.from(c));
  return Buffer.concat(chunks).toString("utf8");
}

function resolveOrigin(req) {
  const host = req.headers["x-forwarded-host"] || req.headers.host || "";
  if (host && (host.endsWith(".vercel.app") || host.startsWith("localhost:"))) {
    const proto = host.startsWith("localhost:") ? "http" : "https";
    return `${proto}://${host}`;
  }
  return (process.env.KINDE_SITE_URL || "http://localhost:5173").replace(/\/+$/, "");
}

async function handleLogin(req, res) {
  const origin = resolveOrigin(req);
  const redirectUri = `${origin}/api/auth/kinde_callback`;
  const authUrl = new URL(`${process.env.KINDE_ISSUER_URL}/oauth2/auth`);
  authUrl.searchParams.set("client_id", process.env.KINDE_CLIENT_ID || "");
  authUrl.searchParams.set("response_type", "code");
  authUrl.searchParams.set("scope", "openid profile email");
  authUrl.searchParams.set("redirect_uri", redirectUri);
  authUrl.searchParams.set("state", crypto.randomBytes(12).toString("hex"));
  res.statusCode = 302;
  res.setHeader("Location", authUrl.toString());
  res.end();
}

async function handleCallback(req, res, urlObj) {
  const code = urlObj.searchParams.get("code") || "";
  if (!code) {
    res.statusCode = 400;
    res.end("Missing OAuth2 code");
    return;
  }
  const origin = resolveOrigin(req);
  const tokenResp = await fetch(`${process.env.KINDE_ISSUER_URL}/oauth2/token`, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Accept: "application/json",
    },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      client_id: process.env.KINDE_CLIENT_ID || "",
      client_secret: process.env.KINDE_CLIENT_SECRET || "",
      code,
      redirect_uri: `${origin}/api/auth/kinde_callback`,
    }),
  });
  const tokenData = await tokenResp.json();
  if (!tokenResp.ok || !tokenData.access_token) {
    res.statusCode = 401;
    res.end(`Token exchange failed: ${JSON.stringify(tokenData)}`);
    return;
  }
  const profileResp = await fetch(`${process.env.KINDE_ISSUER_URL}/oauth2/v2/user_profile`, {
    headers: {
      Authorization: `Bearer ${tokenData.access_token}`,
      Accept: "application/json",
    },
  });
  const profile = profileResp.ok ? await profileResp.json() : {};
  const cookieVal = signSession({
    sub: profile.id || "kinde-user",
    email: profile.preferred_email || profile.email || "authenticated@kinde",
    name: [profile.given_name, profile.family_name].filter(Boolean).join(" ") || "Kinde User",
  });
  res.statusCode = 302;
  res.setHeader("Set-Cookie", `ob_session=${cookieVal}; Path=/; HttpOnly; SameSite=Lax; Secure`);
  res.setHeader("Location", "/");
  res.end();
}

function handleLogout(req, res) {
  const origin = resolveOrigin(req);
  const logoutUrl = new URL(`${process.env.KINDE_ISSUER_URL}/logout`);
  logoutUrl.searchParams.set("redirect", `${origin}/`);
  res.statusCode = 302;
  res.setHeader("Set-Cookie", "ob_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure");
  res.setHeader("Location", logoutUrl.toString());
  res.end();
}

async function handleNotes(req, res) {
  await ensureNotesSchema();
  const env = process.env.OPENBASE_ENVIRONMENT || "unknown";
  if (req.method === "POST") {
    const raw = await readBody(req);
    const ct = String(req.headers["content-type"] || "");
    let noteText = "";
    if (ct.includes("application/json")) {
      noteText = String((JSON.parse(raw || "{}") || {}).body || "").trim();
    } else {
      noteText = String(new URLSearchParams(raw).get("body") || "").trim();
    }
    if (!noteText) {
      res.statusCode = 400;
      res.end(JSON.stringify({ ok: false, error: "body is required" }));
      return;
    }
    const inserted = await neonQuery(
      "INSERT INTO app_notes (body, created_env) VALUES ($1, $2) RETURNING id, body, created_env",
      [noteText, env],
    );
    if (!ct.includes("application/json")) {
      res.statusCode = 303;
      res.setHeader("Location", "/");
      res.end();
      return;
    }
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ ok: true, note: inserted[0] }));
    return;
  }
  const notes = await neonQuery("SELECT id, body, created_env FROM app_notes ORDER BY id ASC");
  res.setHeader("Content-Type", "application/json");
  res.end(JSON.stringify({ ok: true, environment: env, notes }));
}

async function handleStorage(req, res, urlObj) {
  const key = (urlObj.searchParams.get("key") || "greeting.txt").replace(/^\/+/, "");
  const baseUrl = (process.env.BLOB_BASE_URL || "").replace(/\/+$/, "");
  const primary = process.env.BLOB_PREFIX || "";
  const fallback = process.env.BLOB_FALLBACK_PREFIX || "";
  const rwToken = process.env.BLOB_READ_WRITE_TOKEN || "";

  if (req.method === "POST") {
    const body = await readBody(req);
    const putResp = await fetch(`https://blob.vercel-storage.com/${primary}${key}`, {
      method: "PUT",
      headers: {
        Authorization: `Bearer ${rwToken}`,
        "x-api-version": "7",
        "x-add-random-suffix": "0",
        "x-allow-overwrite": "1",
        "x-content-type": "text/plain",
      },
      body,
    });
    const putJson = await putResp.json();
    res.statusCode = putResp.ok ? 200 : 500;
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ ok: putResp.ok, prefix: primary, key, blob: putJson }));
    return;
  }

  const fetchKey = async (pfx) => {
    const headers = rwToken ? { Authorization: `Bearer ${rwToken}` } : {};
    return fetch(`${baseUrl}/${pfx}${key}`, { headers });
  };
  let r = await fetchKey(primary);
  if (r.ok) {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ ok: true, sourcePrefix: primary, key, content: await r.text() }));
    return;
  }
  if (r.status === 404 && fallback) {
    r = await fetchKey(fallback);
    if (r.ok) {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ ok: true, sourcePrefix: fallback, key, content: await r.text() }));
      return;
    }
  }
  res.statusCode = 404;
  res.setHeader("Content-Type", "application/json");
  res.end(JSON.stringify({ ok: false, error: "blob not found", primary, fallback }));
}

async function handleHealth(req, res, session) {
  await ensureNotesSchema();
  const notes = await neonQuery("SELECT id, body, created_env FROM app_notes ORDER BY id ASC");
  const secretSha256 = crypto
    .createHash("sha256")
    .update(process.env.KINDE_CLIENT_SECRET || "")
    .digest("hex");
  res.setHeader("Content-Type", "application/json");
  res.end(
    JSON.stringify({
      ok: true,
      runtime: "vercel",
      environment: process.env.OPENBASE_ENVIRONMENT || "unknown",
      auth: {
        issuer: process.env.KINDE_ISSUER_URL || "",
        clientId: process.env.KINDE_CLIENT_ID || "",
        redirectUrl: process.env.KINDE_REDIRECT_URL || "",
        secretSha256,
        authenticated: Boolean(session),
        user: session,
      },
      db: {
        connected: true,
        pooled: Boolean((process.env.DATABASE_URL || "").includes("-pooler")),
        noteCount: notes.length,
        notes,
      },
      storage: {
        kind: "vercel-blob",
        baseUrl: process.env.BLOB_BASE_URL || "",
        prefix: process.env.BLOB_PREFIX || "",
        fallbackPrefix: process.env.BLOB_FALLBACK_PREFIX || "",
      },
    }),
  );
}

async function handleDashboard(req, res, session) {
  await ensureNotesSchema();
  const notes = await neonQuery("SELECT id, body, created_env FROM app_notes ORDER BY id ASC");
  const env = process.env.OPENBASE_ENVIRONMENT || "unknown";
  const authBlock = session
    ? `<p>Signed in as <strong>${session.email}</strong> (${session.name}) &mdash; <a href="/api/auth/logout">Sign out</a></p>`
    : `<p>Not signed in &mdash; <a href="/api/auth/login"><strong>Sign in with Kinde OIDC</strong></a></p>`;
  const items = notes
    .map((n) => `<li><code>[${n.created_env}]</code> ${String(n.body).replace(/</g, "&lt;")}</li>`)
    .join("");

  res.setHeader("Content-Type", "text/html; charset=utf-8");
  res.end(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>OpenBase Vercel + Neon + Kinde (${env})</title>
<style>body{font-family:system-ui,sans-serif;max-width:760px;margin:2rem auto;padding:0 1rem;line-height:1.5}code{background:#f3f4f6;padding:2px 6px;border-radius:4px}.card{border:1px solid #e5e7eb;border-radius:8px;padding:1rem;margin-bottom:1rem}</style>
</head>
<body>
  <h1>OpenBase Neokit-Style Vercel App</h1>
  <div class="card"><strong>Environment:</strong> <code>${env}</code><br><strong>Kinde Issuer:</strong> <code>${process.env.KINDE_ISSUER_URL || ""}</code>${authBlock}</div>
  <div class="card"><h2>Neon PostgreSQL Branch Notes</h2><ul>${items || "<li>No notes yet</li>"}</ul>
    <form method="POST" action="/api/notes"><input name="body" placeholder="Write a note to this Neon branch..." required style="width:70%;padding:6px"> <button type="submit" style="padding:6px 12px">Add Note</button></form>
  </div>
  <div class="card"><h2>Vercel Blob Overlay</h2><p>Primary prefix: <code>${process.env.BLOB_PREFIX || ""}</code> | Fallback: <code>${process.env.BLOB_FALLBACK_PREFIX || "(none)"}</code> | <a href="/api/health">JSON /api/health</a></p></div>
</body></html>`);
}

module.exports = async (req, res) => {
  try {
    const urlObj = new URL(req.url || "/", "https://openbase.local");
    const route = urlObj.searchParams.get("route") || "dashboard";
    const session = verifySession(parseCookies(req.headers.cookie).ob_session);
    if (route === "login") return await handleLogin(req, res);
    if (route === "callback") return await handleCallback(req, res, urlObj);
    if (route === "logout") return handleLogout(req, res);
    if (route === "notes") return await handleNotes(req, res);
    if (route === "storage") return await handleStorage(req, res, urlObj);
    if (route === "health") return await handleHealth(req, res, session);
    return await handleDashboard(req, res, session);
  } catch (err) {
    res.statusCode = 500;
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ ok: false, error: String(err && err.message ? err.message : err) }));
  }
};
