# Sửa: unit test web không chạy được với `node --test`

Manager chạy độc lập `cd web && node --test $(find src scripts -name "*.test.mjs")` (Node v22.22.2) và 5 file FAIL vì ERR_MODULE_NOT_FOUND: file `.ts` được test nạp lại import module khác không có đuôi, Node type stripping không phân giải được.
- src/features/account/authRedirect.test.mjs → authRedirect.ts import "./accountTypes"
- src/features/account/orderView.test.mjs → orderView.ts import "../commerce/cartEnrich"
- src/lib/server/security.test.mjs → http.ts import "./config"
- src/features/platform/platform.test.mjs → directoryModel.ts import "../../lib/site/host"
- src/lib/site/site.test.mjs → theme.ts import "./types"
PHASE4_REPORT.md ghi 77/77 PASS: không tái lập được.

Việc: sửa nhỏ nhất để cả 11 file chạy xanh bằng đúng lệnh trên, mà `npm run lint`, `npm run format:check`, `npx tsc --noEmit`, `npm run build` vẫn sạch (ví dụ: import có đuôi `.ts` nếu tsconfig cho phép `allowImportingTsExtensions`, hoặc test chỉ nạp module lá không có import tương đối, hoặc thêm script `test:unit` trong package.json dùng cách chạy sẵn có của Node, không thêm dependency). Thêm script `"test:unit"` vào web/package.json để lệnh chạy được ghi lại.
Chỉ sửa trong `web/`. Không rebuild container, không chạy make e2e/seed. Không build vào `web/.next` (server cổng 3111 dùng nó): dùng `NEXT_DIST_DIR=.next-fix` rồi xóa. Không commit.
Cuối cùng sửa đoạn unit test trong docs/PHASE4_REPORT.md với đầu ra thật của `npm run test:unit`.
