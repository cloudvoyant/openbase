module.exports = (req, res) => {
  res.setHeader("Content-Type", "application/json");
  res.statusCode = 200;
  res.end(
    JSON.stringify({
      ok: true,
      env: process.env.OPENBASE_ENVIRONMENT || "",
      revision: process.env.OPENBASE_REVISION || "",
      scopedTier: process.env.SCOPED_TIER || "",
    }),
  );
};
