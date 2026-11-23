import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";

test("P1-3: listAllAdminProducts queries /api/v1/admin/products and returns non-APPROVED products across pages", async () => {
  const allProducts = [
    { id: "p-1", shop_id: "s-1", name: "Ao Thun", slug: "ao-thun", status: "APPROVED" },
    { id: "p-2", shop_id: "s-1", name: "Ao Polo", slug: "ao-polo", status: "PENDING" },
    { id: "p-3", shop_id: "s-1", name: "Quan Short", slug: "quan-short", status: "HIDDEN" },
    { id: "p-4", shop_id: "s-1", name: "Quan Jogger", slug: "quan-jogger", status: "REJECTED" },
  ];
  const requestedPaths = [];

  const server = http.createServer((req, res) => {
    const parsed = new URL(req.url, "http://127.0.0.1");
    requestedPaths.push(parsed.pathname);

    if (parsed.pathname === "/api/v1/products") {
      // CAT-B2: public endpoint strips auth and returns APPROVED/OUT_OF_STOCK only.
      const pub = allProducts.filter((p) => p.status === "APPROVED");
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ items: pub, page: 1, page_size: 100, total: pub.length }));
      return;
    }

    if (parsed.pathname === "/api/v1/admin/products") {
      if (req.headers.authorization !== "Bearer admin-jwt-test") {
        res.writeHead(401, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: { code: "UNAUTHENTICATED" } }));
        return;
      }
      const page = Number(parsed.searchParams.get("page") || "1");
      // Return 2 items per page to exercise pagination against gateway response shape.
      const slice = allProducts.slice((page - 1) * 2, page * 2);
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          items: slice,
          page,
          page_size: Number(parsed.searchParams.get("page_size") || "100"),
          total: allProducts.length,
        })
      );
      return;
    }

    res.writeHead(404);
    res.end();
  });

  await new Promise((resolve, reject) => {
    server.on("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  try {
    const { port } = server.address();
    process.env.SEED_GATEWAY_URL = `http://127.0.0.1:${port}`;
    const { listAllAdminProducts } = await import(`./seed.mjs?t=${Date.now()}`);
    const products = await listAllAdminProducts("admin-jwt-test");

    assert.ok(
      requestedPaths.every((p) => p === "/api/v1/admin/products"),
      `expected only /api/v1/admin/products calls, got: ${requestedPaths.join(", ")}`
    );
    assert.equal(products.length, 4, `expected all 4 products including non-APPROVED, got ${products.length}`);
    assert.deepEqual(
      products.map((p) => p.status),
      ["APPROVED", "PENDING", "HIDDEN", "REJECTED"]
    );
  } finally {
    delete process.env.SEED_GATEWAY_URL;
    await new Promise((resolve) => server.close(resolve));
  }
});
