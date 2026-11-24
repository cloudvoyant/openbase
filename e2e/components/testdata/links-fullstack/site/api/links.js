async function exchangeVercelOIDCForSTS(oidcToken, wifProvider) {
  const resp = await fetch("https://sts.googleapis.com/v1/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      audience: wifProvider,
      grantType: "urn:ietf:params:oauth:grant-type:token-exchange",
      requestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
      scope: "https://www.googleapis.com/auth/cloud-platform",
      subjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
      subjectToken: oidcToken,
    }),
  });
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(`STS exchange failed (${resp.status}): ${JSON.stringify(data)}`);
  }
  return data.access_token;
}

async function generateGoogleIDToken(stsToken, saEmail, audience) {
  const url = `https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${encodeURIComponent(saEmail)}:generateIdToken`;
  const resp = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${stsToken}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ audience, includeEmail: true }),
  });
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(`generateIdToken failed (${resp.status}): ${JSON.stringify(data)}`);
  }
  return data.token;
}

async function generateGoogleAccessToken(stsToken, saEmail) {
  const url = `https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${encodeURIComponent(saEmail)}:generateAccessToken`;
  const resp = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${stsToken}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      scope: ["https://www.googleapis.com/auth/devstorage.read_write"],
    }),
  });
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(`generateAccessToken failed (${resp.status}): ${JSON.stringify(data)}`);
  }
  return data.accessToken;
}

async function readGCSOverlay(accessToken, primaryBucket, fallbackBucket, key) {
  const fetchObj = async (bkt) => {
    const u = `https://storage.googleapis.com/storage/v1/b/${encodeURIComponent(bkt)}/o/${encodeURIComponent(key)}?alt=media`;
    return fetch(u, { headers: { Authorization: `Bearer ${accessToken}` } });
  };
  let r = await fetchObj(primaryBucket);
  if (r.ok) {
    return { sourceBucket: primaryBucket, payload: await r.text() };
  }
  if (r.status === 404 && fallbackBucket) {
    r = await fetchObj(fallbackBucket);
    if (r.ok) {
      return { sourceBucket: fallbackBucket, payload: await r.text() };
    }
  }
  throw new Error(`GCS overlay read failed (${r.status})`);
}

module.exports = async (req, res) => {
  try {
    const oidcToken = req.headers["x-vercel-oidc-token"] || process.env.VERCEL_OIDC_TOKEN || "";
    if (!oidcToken) {
      throw new Error("missing x-vercel-oidc-token header");
    }
    const stsToken = await exchangeVercelOIDCForSTS(
      oidcToken,
      process.env.GCP_WORKLOAD_IDENTITY_PROVIDER,
    );
    const saEmail = process.env.GCP_SERVICE_ACCOUNT_EMAIL;
    const idToken = await generateGoogleIDToken(stsToken, saEmail, process.env.ORDERS_URL);
    const crResp = await fetch(`${process.env.ORDERS_URL}/health`, {
      headers: { Authorization: `Bearer ${idToken}` },
    });
    const crBody = await crResp.json();
    if (!crResp.ok) {
      throw new Error(`Cloud Run /health failed (${crResp.status}): ${JSON.stringify(crBody)}`);
    }

    const gcsAccessToken = await generateGoogleAccessToken(stsToken, saEmail);
    const overlay = await readGCSOverlay(
      gcsAccessToken,
      process.env.STORAGE_BUCKET,
      process.env.STORAGE_FALLBACK_BUCKET || "",
      "seed.txt",
    );
    const sharedAsset = await readGCSOverlay(
      gcsAccessToken,
      process.env.SHARED_ASSETS_BUCKET,
      "",
      "brand.txt",
    );

    res.setHeader("Content-Type", "application/json");
    res.statusCode = 200;
    res.end(
      JSON.stringify({
        ok: true,
        cloudRunStatus: crResp.status,
        cloudRun: crBody,
        gcsOverlay: overlay,
        sharedAsset,
      }),
    );
  } catch (err) {
    res.setHeader("Content-Type", "application/json");
    res.statusCode = 500;
    res.end(JSON.stringify({ ok: false, error: String(err && err.message ? err.message : err) }));
  }
};
