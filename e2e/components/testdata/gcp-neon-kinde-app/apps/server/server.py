import base64
import datetime
import hashlib
import hmac
import html
import http.cookies
import http.server
import json
import os
import secrets
import urllib.error
import urllib.parse
import urllib.request


def gcp_metadata(path):
    req = urllib.request.Request(
        "http://metadata.google.internal/computeMetadata/v1/" + path,
        headers={"Metadata-Flavor": "Google"},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return resp.read().decode("utf-8")


def gcp_access_token():
    return json.loads(gcp_metadata("instance/service-accounts/default/token"))["access_token"]


def sign_gcs_v4(bucket, obj):
    sa_email = gcp_metadata("instance/service-accounts/default/email").strip()
    tok = gcp_access_token()
    now = datetime.datetime.now(datetime.timezone.utc)
    datestamp = now.strftime("%Y%m%d")
    timestamp = now.strftime("%Y%m%dT%H%M%SZ")
    scope = f"{datestamp}/auto/storage/goog4_request"
    cred = f"{sa_email}/{scope}"
    params = [
        ("X-Goog-Algorithm", "GOOG4-RSA-SHA256"),
        ("X-Goog-Credential", urllib.parse.quote(cred, safe="")),
        ("X-Goog-Date", timestamp),
        ("X-Goog-Expires", "300"),
        ("X-Goog-SignedHeaders", "host"),
    ]
    query = "&".join(f"{k}={v}" for k, v in params)
    escaped_obj = "/".join(urllib.parse.quote(p, safe="") for p in obj.split("/"))
    canonical = "\n".join(
        [
            "GET",
            f"/{bucket}/{escaped_obj}",
            query,
            "host:storage.googleapis.com",
            "",
            "host",
            "UNSIGNED-PAYLOAD",
        ]
    )
    sts = "\n".join(
        [
            "GOOG4-RSA-SHA256",
            timestamp,
            scope,
            hashlib.sha256(canonical.encode("utf-8")).hexdigest(),
        ]
    )
    body = json.dumps({"payload": base64.b64encode(sts.encode("utf-8")).decode("ascii")}).encode("utf-8")
    sign_req = urllib.request.Request(
        f"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa_email}:signBlob",
        data=body,
        headers={"Authorization": f"Bearer {tok}", "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(sign_req, timeout=10) as resp:
            sig_b64 = json.loads(resp.read().decode("utf-8"))["signedBlob"]
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"signBlob HTTP {exc.code}: {detail}") from exc
    sig_hex = base64.b64decode(sig_b64).hex()
    return f"https://storage.googleapis.com/{bucket}/{escaped_obj}?{query}&X-Goog-Signature={sig_hex}"


def sign_session(payload_obj):
    secret = (os.environ.get("KINDE_CLIENT_SECRET") or "openbase-fallback").encode("utf-8")
    b64 = base64.urlsafe_b64encode(json.dumps(payload_obj).encode("utf-8")).decode("ascii").rstrip("=")
    sig = base64.urlsafe_b64encode(hmac.new(secret, b64.encode("ascii"), hashlib.sha256).digest()).decode("ascii").rstrip("=")
    return f"{b64}.{sig}"


def verify_session(raw_cookie):
    if not raw_cookie or "." not in raw_cookie:
        return None
    b64, sig = raw_cookie.split(".", 1)
    secret = (os.environ.get("KINDE_CLIENT_SECRET") or "openbase-fallback").encode("utf-8")
    expected = base64.urlsafe_b64encode(hmac.new(secret, b64.encode("ascii"), hashlib.sha256).digest()).decode("ascii").rstrip("=")
    if not hmac.compare_digest(sig, expected):
        return None
    pad = "=" * ((4 - len(b64) % 4) % 4)
    try:
        return json.loads(base64.urlsafe_b64decode((b64 + pad).encode("ascii")).decode("utf-8"))
    except Exception:
        return None


def neon_query(sql, params=None):
    conn_str = os.environ.get("DIRECT_URL") or os.environ.get("DATABASE_URL") or ""
    if not conn_str:
        raise RuntimeError("DATABASE_URL is not configured")
    parsed = urllib.parse.urlparse(conn_str)
    body = json.dumps({"query": sql, "params": params or []}).encode("utf-8")
    req = urllib.request.Request(
        f"https://{parsed.hostname}/sql",
        data=body,
        headers={
            "Neon-Connection-String": conn_str,
            "Content-Type": "application/json",
            "User-Agent": "openbase-gcp-neon-kinde-app/1.0",
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    return data.get("rows") or []


def ensure_notes_schema():
    neon_query(
        "CREATE TABLE IF NOT EXISTS app_notes ("
        "id SERIAL PRIMARY KEY, "
        "body TEXT NOT NULL, "
        "created_env TEXT NOT NULL, "
        "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()"
        ")"
    )


def gcs_put(bucket, key, data_bytes):
    tok = gcp_access_token()
    quoted = urllib.parse.quote(key.lstrip("/"), safe="")
    url = f"https://storage.googleapis.com/upload/storage/v1/b/{bucket}/o?uploadType=media&name={quoted}"
    req = urllib.request.Request(
        url,
        data=data_bytes,
        headers={"Authorization": f"Bearer {tok}", "Content-Type": "text/plain; charset=utf-8"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode("utf-8"))


def gcs_get(bucket, key):
    tok = gcp_access_token()
    quoted = urllib.parse.quote(key.lstrip("/"), safe="")
    url = f"https://storage.googleapis.com/storage/v1/b/{bucket}/o/{quoted}?alt=media"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {tok}"}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return 200, resp.read().decode("utf-8")
    except urllib.error.HTTPError as err:
        return err.code, ""


class AppHandler(http.server.BaseHTTPRequestHandler):
    def resolve_origin(self):
        host = self.headers.get("x-forwarded-host") or self.headers.get("host") or ""
        if host and (host.endswith(".run.app") or host.startswith("localhost:")):
            proto = "http" if host.startswith("localhost:") else "https"
            return f"{proto}://{host}"
        return (os.environ.get("KINDE_SITE_URL") or "http://localhost:5173").rstrip("/")

    def current_session(self):
        raw_hdr = self.headers.get("cookie") or ""
        jar = http.cookies.SimpleCookie()
        try:
            jar.load(raw_hdr)
        except Exception:
            return None
        morsel = jar.get("ob_session")
        return verify_session(morsel.value) if morsel else None

    def send_json(self, code, payload):
        raw = json.dumps(payload).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def send_redirect(self, code, location, cookie_header=None):
        self.send_response(code)
        if cookie_header:
            self.send_header("Set-Cookie", cookie_header)
        self.send_header("Location", location)
        self.end_headers()

    def read_body(self):
        length = int(self.headers.get("Content-Length") or "0")
        return self.rfile.read(length) if length > 0 else b""

    def do_GET(self):
        try:
            parsed = urllib.parse.urlparse(self.path)
            qs = urllib.parse.parse_qs(parsed.query)
            session = self.current_session()
            if parsed.path == "/api/auth/login":
                return self.handle_login()
            if parsed.path == "/api/auth/kinde_callback":
                return self.handle_callback(qs)
            if parsed.path == "/api/auth/logout":
                return self.handle_logout()
            if parsed.path == "/api/notes":
                return self.handle_notes_get()
            if parsed.path == "/api/storage":
                return self.handle_storage_get(qs)
            if parsed.path == "/api/sign":
                key = ((qs.get("key") or ["seed.txt"])[0]).lstrip("/")
                return self.send_json(200, {"ok": True, "signedUrl": sign_gcs_v4(os.environ.get("STORAGE_BUCKET", ""), key)})
            if parsed.path == "/api/health":
                return self.handle_health(session)
            return self.handle_dashboard(session)
        except Exception as exc:
            self.send_json(500, {"ok": False, "error": str(exc)})

    def do_POST(self):
        try:
            parsed = urllib.parse.urlparse(self.path)
            qs = urllib.parse.parse_qs(parsed.query)
            if parsed.path == "/api/notes":
                return self.handle_notes_post()
            if parsed.path == "/api/storage":
                return self.handle_storage_post(qs)
            self.send_json(404, {"ok": False, "error": "not found"})
        except Exception as exc:
            self.send_json(500, {"ok": False, "error": str(exc)})

    def handle_login(self):
        origin = self.resolve_origin()
        params = urllib.parse.urlencode(
            {
                "client_id": os.environ.get("KINDE_CLIENT_ID", ""),
                "response_type": "code",
                "scope": "openid profile email",
                "redirect_uri": f"{origin}/api/auth/kinde_callback",
                "state": secrets.token_hex(12),
            }
        )
        issuer = (os.environ.get("KINDE_ISSUER_URL") or "").rstrip("/")
        self.send_redirect(302, f"{issuer}/oauth2/auth?{params}")

    def handle_callback(self, qs):
        code = (qs.get("code") or [""])[0]
        if not code:
            return self.send_json(400, {"ok": False, "error": "Missing OAuth2 code"})
        origin = self.resolve_origin()
        issuer = (os.environ.get("KINDE_ISSUER_URL") or "").rstrip("/")
        form = urllib.parse.urlencode(
            {
                "grant_type": "authorization_code",
                "client_id": os.environ.get("KINDE_CLIENT_ID", ""),
                "client_secret": os.environ.get("KINDE_CLIENT_SECRET", ""),
                "code": code,
                "redirect_uri": f"{origin}/api/auth/kinde_callback",
            }
        ).encode("utf-8")
        tok_req = urllib.request.Request(
            f"{issuer}/oauth2/token",
            data=form,
            headers={
                "Content-Type": "application/x-www-form-urlencoded",
                "Accept": "application/json",
                "User-Agent": "openbase-gcp-neon-kinde-app/1.0",
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(tok_req, timeout=15) as resp:
                tok_data = json.loads(resp.read().decode("utf-8"))
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            return self.send_json(401, {"ok": False, "error": f"Token exchange failed ({err.code}): {detail}"})
        prof_req = urllib.request.Request(
            f"{issuer}/oauth2/v2/user_profile",
            headers={
                "Authorization": f"Bearer {tok_data['access_token']}",
                "Accept": "application/json",
                "User-Agent": "openbase-gcp-neon-kinde-app/1.0",
            },
        )
        with urllib.request.urlopen(prof_req, timeout=15) as resp:
            prof = json.loads(resp.read().decode("utf-8"))
        cookie_val = sign_session(
            {
                "sub": prof.get("id") or "kinde-user",
                "email": prof.get("preferred_email") or prof.get("email") or "authenticated@kinde",
                "name": " ".join(filter(None, [prof.get("given_name"), prof.get("family_name")])) or "Kinde User",
            }
        )
        self.send_redirect(302, "/", f"ob_session={cookie_val}; Path=/; HttpOnly; SameSite=Lax; Secure")

    def handle_logout(self):
        origin = self.resolve_origin()
        issuer = (os.environ.get("KINDE_ISSUER_URL") or "").rstrip("/")
        q = urllib.parse.urlencode({"redirect": f"{origin}/"})
        self.send_redirect(302, f"{issuer}/logout?{q}", "ob_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax; Secure")

    def handle_notes_get(self):
        ensure_notes_schema()
        notes = neon_query("SELECT id, body, created_env FROM app_notes ORDER BY id ASC")
        self.send_json(200, {"ok": True, "environment": os.environ.get("OPENBASE_ENVIRONMENT", "unknown"), "notes": notes})

    def handle_notes_post(self):
        ensure_notes_schema()
        raw = self.read_body().decode("utf-8")
        ct = (self.headers.get("Content-Type") or "").lower()
        if "application/json" in ct:
            note_text = str((json.loads(raw or "{}") or {}).get("body") or "").strip()
        else:
            note_text = str((urllib.parse.parse_qs(raw).get("body") or [""])[0]).strip()
        if not note_text:
            return self.send_json(400, {"ok": False, "error": "body is required"})
        env = os.environ.get("OPENBASE_ENVIRONMENT", "unknown")
        inserted = neon_query(
            "INSERT INTO app_notes (body, created_env) VALUES ($1, $2) RETURNING id, body, created_env",
            [note_text, env],
        )
        if "application/json" not in ct:
            return self.send_redirect(303, "/")
        self.send_json(200, {"ok": True, "note": inserted[0]})

    def handle_storage_get(self, qs):
        key = ((qs.get("key") or ["greeting.txt"])[0]).lstrip("/")
        primary = os.environ.get("STORAGE_BUCKET", "")
        fallback = os.environ.get("STORAGE_FALLBACK_BUCKET", "")
        code, content = gcs_get(primary, key)
        if code == 200:
            return self.send_json(200, {"ok": True, "sourceBucket": primary, "key": key, "content": content})
        if code == 404 and fallback:
            fcode, fcontent = gcs_get(fallback, key)
            if fcode == 200:
                return self.send_json(200, {"ok": True, "sourceBucket": fallback, "key": key, "content": fcontent})
        self.send_json(404, {"ok": False, "error": "object not found", "bucket": primary, "fallbackBucket": fallback})

    def handle_storage_post(self, qs):
        key = ((qs.get("key") or ["greeting.txt"])[0]).lstrip("/")
        primary = os.environ.get("STORAGE_BUCKET", "")
        meta_obj = gcs_put(primary, key, self.read_body())
        self.send_json(200, {"ok": True, "bucket": primary, "key": key, "name": meta_obj.get("name", key)})

    def handle_health(self, session):
        ensure_notes_schema()
        notes = neon_query("SELECT id, body, created_env FROM app_notes ORDER BY id ASC")
        secret_hash = hashlib.sha256((os.environ.get("KINDE_CLIENT_SECRET") or "").encode("utf-8")).hexdigest()
        self.send_json(
            200,
            {
                "ok": True,
                "runtime": "gcp-cloud-run",
                "environment": os.environ.get("OPENBASE_ENVIRONMENT", "unknown"),
                "auth": {
                    "issuer": os.environ.get("KINDE_ISSUER_URL", ""),
                    "clientId": os.environ.get("KINDE_CLIENT_ID", ""),
                    "redirectUrl": os.environ.get("KINDE_REDIRECT_URL", ""),
                    "secretSha256": secret_hash,
                    "authenticated": bool(session),
                    "user": session,
                },
                "db": {
                    "connected": True,
                    "pooled": "-pooler" in (os.environ.get("DATABASE_URL") or ""),
                    "noteCount": len(notes),
                    "notes": notes,
                },
                "storage": {
                    "kind": "gcp-gcs",
                    "bucket": os.environ.get("STORAGE_BUCKET", ""),
                    "fallbackBucket": os.environ.get("STORAGE_FALLBACK_BUCKET", ""),
                },
            },
        )

    def handle_dashboard(self, session):
        ensure_notes_schema()
        notes = neon_query("SELECT id, body, created_env FROM app_notes ORDER BY id ASC")
        env = html.escape(os.environ.get("OPENBASE_ENVIRONMENT", "unknown"))
        if session:
            auth_html = (
                f"<p>Signed in as <strong>{html.escape(session.get('email', ''))}</strong> "
                f"({html.escape(session.get('name', ''))}) &mdash; <a href='/api/auth/logout'>Sign out</a></p>"
            )
        else:
            auth_html = "<p>Not signed in &mdash; <a href='/api/auth/login'><strong>Sign in with Kinde OIDC</strong></a></p>"
        items = "".join(
            f"<li><code>[{html.escape(str(n.get('created_env', '')))}]</code> {html.escape(str(n.get('body', '')))}</li>"
            for n in notes
        ) or "<li>No notes yet</li>"
        doc = f"""<!doctype html>
<html>
<head><meta charset="utf-8"><title>OpenBase Cloud Run + Neon + Kinde ({env})</title>
<style>body{{font-family:system-ui,sans-serif;max-width:760px;margin:2rem auto;padding:0 1rem;line-height:1.5}}code{{background:#f3f4f6;padding:2px 6px;border-radius:4px}}.card{{border:1px solid #e5e7eb;border-radius:8px;padding:1rem;margin-bottom:1rem}}</style>
</head>
<body>
  <h1>OpenBase Cloud Run + Neon + Kinde App</h1>
  <div class="card"><strong>Environment:</strong> <code>{env}</code><br><strong>Kinde Issuer:</strong> <code>{html.escape(os.environ.get('KINDE_ISSUER_URL', ''))}</code>{auth_html}</div>
  <div class="card"><h2>Neon PostgreSQL Branch Notes</h2><ul>{items}</ul>
    <form method="POST" action="/api/notes"><input name="body" placeholder="Write a note to this Neon branch..." required style="width:70%;padding:6px"> <button type="submit" style="padding:6px 12px">Add Note</button></form>
  </div>
  <div class="card"><h2>GCS Bucket Overlay</h2><p>Primary bucket: <code>{html.escape(os.environ.get('STORAGE_BUCKET', ''))}</code> | Fallback: <code>{html.escape(os.environ.get('STORAGE_FALLBACK_BUCKET') or '(none)')}</code> | <a href="/api/health">JSON /api/health</a></p></div>
</body></html>""".encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(doc)))
        self.end_headers()
        self.wfile.write(doc)

    def log_message(self, fmt, *args):
        return


if __name__ == "__main__":
    port = int(os.environ.get("PORT") or "8080")
    http.server.ThreadingHTTPServer(("0.0.0.0", port), AppHandler).serve_forever()
