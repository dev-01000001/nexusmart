# Swarm A – Search service
Hợp đồng: `m-all-contract.md`. Spec hàng "Tìm kiếm".
- `services/search` (Go): Meilisearch cho từ khoá (có dấu và không dấu, typo, lọc, sắp xếp, facet, gợi ý), pgvector cho vector, hybrid bằng Reciprocal Rank Fusion. gRPC `Search`, `Suggest`, `SimilarProducts` (cho assistant) + REST qua gateway.
- Product service phát event `product.*` qua outbox sẵn có; search consume, cập nhật index dưới 5 giây (đo và ghi số).
- `tools/catalog-gen`: sinh 20.000 sản phẩm mẫu tiếng Việt cho các gian hàng seed, nạp qua API product (không ghi thẳng DB của service khác).
- Test: unit, integration với Meilisearch + Postgres tạm; benchmark p95 truy vấn < 200 ms trên 20.000 sản phẩm (ghi số thật).
