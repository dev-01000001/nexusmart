// Run: node --test web/src/components/app/mediaUploadUtils.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MEDIA_MAX_BYTES,
  MEDIA_MAX_PRODUCT_IMAGES,
  appendUniqueMediaUrl,
  buildGatewayMediaPath,
  buildMediaPublicUrl,
  formatMediaBytes,
  isValidMediaKey,
  mapMediaUploadError,
  normalizeMediaContentType,
  reorderMediaUrls,
  setPrimaryMediaUrl,
  validateContentLengthHeader,
  validateMediaFileClient,
} from "./mediaUploadUtils.ts";
import {
  classifyHost,
  isBlockedSitePath,
  isMediaPath,
  resolveCanonicalHostRedirect,
} from "../../lib/site/host.ts";

const VALID_SHOP = "11111111-2222-4333-8444-555555555555";
const VALID_HASH = "a".repeat(64);

test("normalizeMediaContentType accepts only JPEG, PNG, and WebP", () => {
  assert.equal(normalizeMediaContentType("image/jpeg"), "image/jpeg");
  assert.equal(normalizeMediaContentType("IMAGE/PNG; charset=binary"), "image/png");
  assert.equal(normalizeMediaContentType("image/webp"), "image/webp");
  assert.equal(normalizeMediaContentType("image/svg+xml"), null);
  assert.equal(normalizeMediaContentType("image/gif"), null);
  assert.equal(normalizeMediaContentType("text/html"), null);
  assert.equal(normalizeMediaContentType(""), null);
});

test("validateMediaFileClient checks size, MIME, empty files, and extension fallback", () => {
  assert.deepEqual(validateMediaFileClient({ name: "a.jpg", type: "image/jpeg", size: 1024 }), {
    ok: true,
    contentType: "image/jpeg",
  });
  assert.deepEqual(
    validateMediaFileClient({ name: "exact-limit.png", type: "image/png", size: MEDIA_MAX_BYTES }),
    { ok: true, contentType: "image/png" }
  );
  // Empty browser MIME falls back safely to .webp/.png/.jpg
  assert.deepEqual(validateMediaFileClient({ name: "photo.WEBP", type: "", size: 2048 }), {
    ok: true,
    contentType: "image/webp",
  });

  // Oversized (> 5 MiB)
  const over = validateMediaFileClient({
    name: "big.jpg",
    type: "image/jpeg",
    size: MEDIA_MAX_BYTES + 1,
  });
  assert.equal(over.ok, false);
  if (!over.ok) {
    assert.equal(over.code, "TOO_LARGE");
    assert.match(over.message, /5 MB/);
  }

  // Empty file
  const empty = validateMediaFileClient({ name: "empty.png", type: "image/png", size: 0 });
  assert.equal(empty.ok, false);
  if (!empty.ok) assert.equal(empty.code, "EMPTY");

  // SVG / GIF / HTML / unknown extension rejected
  for (const bad of [
    { name: "x.svg", type: "image/svg+xml", size: 100 },
    { name: "x.svg", type: "", size: 100 },
    { name: "x.gif", type: "image/gif", size: 100 },
    { name: "x.html", type: "text/html", size: 100 },
    { name: "x.jpg", type: "image/svg+xml", size: 100 },
  ]) {
    const res = validateMediaFileClient(bad);
    assert.equal(res.ok, false);
    if (!res.ok) assert.equal(res.code, "UNSUPPORTED_TYPE");
  }
});

test("isValidMediaKey, buildMediaPublicUrl, and buildGatewayMediaPath enforce strict shop media keys", () => {
  const validKey = `shops/${VALID_SHOP}/${VALID_HASH}.webp`;
  assert.equal(isValidMediaKey(validKey), true);
  assert.equal(buildMediaPublicUrl(validKey), `/media/${validKey}`);
  assert.equal(
    buildGatewayMediaPath(["shops", VALID_SHOP, `${VALID_HASH}.jpg`]),
    `/media/shops/${VALID_SHOP}/${VALID_HASH}.jpg`
  );

  const badKeys = [
    `shops/${VALID_SHOP}/${VALID_HASH}.svg`,
    `shops/${VALID_SHOP}/${VALID_HASH}.gif`,
    `shops/${VALID_SHOP}/${"A".repeat(64)}.jpg`,
    `shops/${VALID_SHOP}/short.jpg`,
    `shops/not-a-uuid/${VALID_HASH}.jpg`,
    `shops/../${VALID_HASH}.jpg`,
    `other/${VALID_SHOP}/${VALID_HASH}.jpg`,
    `shops/${VALID_SHOP}/${VALID_HASH}.jpg/extra`,
  ];
  for (const k of badKeys) {
    assert.equal(isValidMediaKey(k), false, `expected invalid key: ${k}`);
    assert.equal(buildMediaPublicUrl(k), null, `expected null URL: ${k}`);
  }

  assert.equal(buildGatewayMediaPath(["shops", "..", `${VALID_HASH}.jpg`]), null);
  assert.equal(buildGatewayMediaPath(["shops", VALID_SHOP]), null);
  assert.equal(buildGatewayMediaPath(["shops", VALID_SHOP, `${VALID_HASH}.jpg`, "extra"]), null);
});

test("validateContentLengthHeader enforces exact 5 MiB cap and rejects malformed values", () => {
  assert.deepEqual(validateContentLengthHeader(null), { ok: true, length: null });
  assert.deepEqual(validateContentLengthHeader(""), { ok: true, length: null });
  assert.deepEqual(validateContentLengthHeader(String(MEDIA_MAX_BYTES)), {
    ok: true,
    length: MEDIA_MAX_BYTES,
  });
  assert.deepEqual(validateContentLengthHeader(String(MEDIA_MAX_BYTES + 1)), {
    ok: false,
    status: 413,
  });
  assert.deepEqual(validateContentLengthHeader("-5"), { ok: false, status: 400 });
  assert.deepEqual(validateContentLengthHeader("abc"), { ok: false, status: 400 });
});

test("gallery list helpers handle unique append (cap 9), primary selection, and reorder", () => {
  let list = [];
  for (let i = 1; i <= 12; i += 1) {
    list = appendUniqueMediaUrl(list, `/media/shops/${VALID_SHOP}/${String(i).padStart(64, "0")}.jpg`);
  }
  assert.equal(list.length, MEDIA_MAX_PRODUCT_IMAGES);
  assert.equal(appendUniqueMediaUrl(list, list[0]).length, MEDIA_MAX_PRODUCT_IMAGES);

  const reordered = reorderMediaUrls(["a", "b", "c"], 0, 2);
  assert.deepEqual(reordered, ["b", "c", "a"]);

  const promoted = setPrimaryMediaUrl(["a", "b", "c"], "c");
  assert.deepEqual(promoted, ["c", "a", "b"]);
  assert.deepEqual(setPrimaryMediaUrl(["a", "b"], "a"), ["a", "b"]);
});

test("formatMediaBytes and mapMediaUploadError produce Vietnamese messages", () => {
  assert.equal(formatMediaBytes(512), "512 B");
  assert.equal(formatMediaBytes(25 * 1024), "25 KB");
  assert.equal(formatMediaBytes(2.5 * 1024 * 1024), "2,5 MB");

  assert.match(mapMediaUploadError(413), /5 MB/);
  assert.match(mapMediaUploadError(415), /JPEG, PNG hoặc WebP/);
  assert.match(mapMediaUploadError(400), /6000×6000/);
  assert.match(mapMediaUploadError(429), /hơi nhanh/);
  assert.match(mapMediaUploadError(503), /thử lại sau/);
});

test("host routing allows /media/* on tenant site hosts without blocking or canonical 308 redirect", () => {
  const mediaPath = `/media/shops/${VALID_SHOP}/${VALID_HASH}.png`;
  assert.equal(isMediaPath(mediaPath), true);
  assert.equal(isMediaPath("/media"), true);
  assert.equal(isMediaPath("/media-manifest.json"), false);
  assert.equal(isBlockedSitePath(mediaPath), false);

  const siteHost = classifyHost("gom-su.nexusmart.localhost:3300", "nexusmart.localhost");
  assert.equal(
    resolveCanonicalHostRedirect({
      hostClass: siteHost,
      primaryHost: "gomsu.vn",
      rootDomain: "nexusmart.localhost",
      method: "GET",
      pathname: mediaPath,
      proto: "http",
    }),
    null
  );
});
