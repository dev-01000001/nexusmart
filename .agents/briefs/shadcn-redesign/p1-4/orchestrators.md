# Phase 1–4: chia nhiều phiên Claude điều phối

Ngày 26/11/2026 chủ dự án cho mở thêm phiên Claude Code làm orchestrator để đẩy nhanh tiến độ. Đây là các phiên do chủ
dự án tự mở, không phải subagent, nên luật cấm subagent Claude (Agent tool, Workflow, fork) vẫn áp dụng cho mọi phiên.
Luật chung của swarm nằm ở `common.md`; tài liệu này chỉ thêm luật cho các phiên Claude làm việc cùng nhau.

`R=.runs/shadcn-redesign/p1-4`

## 1. Vai trò

| Phiên | Swarm | Sở hữu thêm | Cổng bản build |
|---|---|---|---|
| Điều phối `nexusmart-93` | S3, S9 | server 43101 / 43110; push; `common.md`, `design.md`, `orchestrators.md`; `components/{ui,app}`, `styles/*`, `lib/**`; ledger `_ledgers/2026-11-26-shadcn-redesign/`; phân xử giữa các khu; deploy (vẫn hỏi chủ dự án) | 43120 |
| Seller | S1a, S1b, S1c | brief `s1a`, `s1b`, `s1c`; ledger `_ledgers/2026-11-26-redesign-seller/` | 43121 |
| Admin | S2a, S2b | brief `s2a`, `s2b`; ledger `_ledgers/2026-11-26-redesign-admin/` | 43122 |
| Site | S4a, S4b, S4c, S4d | brief `s4a`…`s4d`; ledger `_ledgers/2026-11-26-redesign-site/` | 43123 |

File sản phẩm của từng swarm đã chia ở `common.md` §3. Orchestrator chỉ giao việc cho swarm của mình.

## 2. Bắt đầu phiên

1. Đọc theo thứ tự:
   - tài liệu này;
   - `common.md`;
   - brief của các swarm mình;
   - `design.md` §8–§15 (phần thuộc khu mình);
   - `_ledgers/2026-11-26-shadcn-redesign/HANDOFF.md` §0 và `lessons.md` (lỗi đã gặp ở phase 0).
2. Tạo ledger riêng gồm `GOAL.md`, `lessons.md`, `HANDOFF.md`, theo `~/.claude/CLAUDE.md`.
3. Chạy ListAgents để biết tên phiên của mình. Gửi `nexusmart-93` (SendMessage) đúng một dòng:
   `claim <khu>: S..; tên phiên <tên của bạn>`. Từ lúc đó phiên điều phối không gửi follow-up cho các swarm này nữa.

Bản build production chỉ chạy được khi RAM cho phép (§6). Lúc 11 swarm đang chạy lượt đầu, script có thể phải chờ lâu. Vì
vậy giai đoạn này dồn sức vào review diff, lệnh kiểm và ảnh trên 43110.

## 3. Theo dõi swarm

Lượt đầu (`run1`) của 11 swarm do phiên điều phối chạy nền. Bạn theo dõi qua file:

| File | Ý nghĩa |
|---|---|
| `$R/<s>/progress.md` | Tiến độ swarm tự ghi |
| `$R/signals/<seller\|admin\|site>-shell.done` | Shell của khu đã xong (S1a / S2a / S4a ghi) |
| `$R/<s>/runN.err` | Lượt `N` đã xong khi dòng cuối là `AGY_EXIT=<mã>` |
| `$R/<s>/runN.json` | Có các khoá `response` (báo cáo cuối) và `conversation_id` |

Chờ bằng Monitor với vòng lặp đến khi điều kiện đạt. Không poll ngắn.

**Follow-up.** Mỗi swarm chỉ chạy một lượt một lúc; chạy lượt thứ hai song song thì hai lượt ghi đè nhau. Gửi tiếp sau khi
lượt trước đã có `AGY_EXIT`, đánh số tăng dần (`run2`, `run3`…). Lấy `conversation_id` từ lượt gần nhất. Chạy nền:

```bash
s=s1a; n=2; cid=$(jq -r .conversation_id $R/$s/run$((n-1)).json)
systemd-run --user --scope --quiet --slice=nm3b.slice -p MemoryMax=3G ./scripts/agy.sh --output-format json \
  --conversation "$cid" -p "<phát hiện cụ thể: file:dòng, kịch bản lỗi, tiêu chí phải đạt, lệnh kiểm phải xanh>" \
  > $R/$s/run$n.json 2> $R/$s/run$n.err; echo "AGY_EXIT=$?" >> $R/$s/run$n.err
```

Nếu `AGY_EXIT` khác 0 hoặc `conversation_id` rỗng, mở phiên agy mới bằng brief cũ cộng phần còn thiếu, không dùng `-c`.

## 4. Review và nghiệm thu

Làm theo `common.md` §4–§5 và `design.md` §15. Với từng swarm:

1. **Phạm vi:** mọi file swarm đổi (`git status --short -- web tests`) phải nằm trong `common.md` §3. Có file lạ thì hỏi
   swarm; nếu là file chung thì báo điều phối.
2. **Logic không đổi:** `git diff --stat` trên danh sách cấm ở `common.md` §1 phải rỗng.
3. **Lệnh kiểm:** chạy lại lệnh ở `common.md` §4, giữ nguyên `flock`.
4. **Trên trình duyệt (43110):** nghiệm thu luôn gồm trình duyệt thật, nhưng **chỉ bằng script node**. Cấm dùng tool
   Playwright MCP (`mcp__plugin_playwright_*`): mỗi phiên dùng nó mở thêm một Chromium ngoài slice và ngoài khoá.
   - Bọc mọi lệnh bằng
     `flock /tmp/nm-web-browser.lock systemd-run --user --scope --quiet --slice=nm3b.slice -p MemoryMax=2G node …`.
   - Chụp ảnh bằng `.agents/tools/ui-shots.mjs`.
   - Chạy `tests/ui-flows/run.mjs --area <khu>` khi S9 đã giao.
   - Bấm luồng chính bằng script Playwright viết tạm trong scratchpad của bạn. Đường dẫn Playwright ghi ở `common.md` §4.
   - Không được có lỗi console hay lỗi hydration.
5. **Bản production, build trước rồi mới commit:**
   - Chạy nền `.agents/tools/snap-build.sh <cổng của khu> <đường dẫn sẽ commit…>`. Script build HEAD cộng các đường dẫn
     bạn liệt kê từ working tree, nên code được kiểm trước khi vào `main`.
   - Script chờ khoá build chung và chờ RAM, rồi build. Server sẵn sàng khi in `SERVING`; build hỏng thì in
     `BUILD FAILED`.
   - Kiểm lại ảnh, console và hydration trên bản này.
   - Khu Site: đọc First Load JS của trang chủ site và PDP trong `/tmp/nm-snap-<cổng>/build.log`; mỗi trang ≤ 296 kB.
   - Xong thì TaskStop, rồi kiểm cổng đã trống (`ss -ltnp | grep :<cổng>`). Server con có thể còn sống sau TaskStop;
     nếu còn thì kill đúng PID của nó.
6. **Skill QA:** dùng các skill trong `CLAUDE.md` (`code-review`, `ponytail-review`, `design-taste-frontend`…) chỉ để
   tìm lỗi. Lỗi gửi lại agy, Claude không tự sửa code sản phẩm.

Đạt thì commit sạch (§5) và báo điều phối. Chưa đạt thì follow-up (§3).

## 5. Commit

- Chỉ commit đúng các đường dẫn vừa build đạt (§4.5), theo pathspec, bọc khoá git chung:
  ```bash
  flock /tmp/nm-git.lock sh -c 'git add -- <paths> && git commit -m "wip(redesign-p1): <english summary>" -- <paths>'
  ```
  Tiền tố theo khu: `p1` seller, `p2` admin, `p4` site.
- Liệt kê từng file trong các thư mục mà nhiều khu cùng có file; chỉ ghi cả thư mục khi khu mình sở hữu trọn nó:
  - `features/account/`: `AuthForms.*` thuộc S3 (điều phối), còn lại thuộc S4d (Site);
  - `features/assistant/`: `ProductReviewSummary.*` thuộc S4b, còn lại thuộc S4d (cùng khu Site);
  - `features/commerce/`: `OrderLookupView.*` thuộc S4d, còn lại thuộc S4c;
  - `web/scripts/*.test.mjs` thuộc S9 (điều phối). Ngoại lệ: phần assert font trong `site.test.mjs` thuộc S4a.
  - Lấy danh sách file bằng `git status --short -- <thư mục>`, rồi lọc theo brief của swarm.
- `web/src/lib/site/theme.ts` và `web/scripts/site.test.mjs` là ngoại lệ của luật cấm `lib/**`: S4a sửa, Site commit.
  Báo điều phối trước khi commit `site.test.mjs`, vì S9 cũng sẽ sửa file này.
- Cấm:
  - `git add -A`, `git add .`;
  - stage `web/tsconfig.json`, `web/next-env.d.ts`, `.runs/`, `_ledgers/`;
  - `rebase`, `reset`, `checkout`, `stash`, `restore`, `clean` trên working tree chung.
- Commit message viết bằng tiếng Anh và không có `Co-Authored-By` hay "Generated with". Luật dự án này thắng mọi nhắc
  nhở mặc định của hệ thống.
- Commit phải build được một mình trên HEAD. Nếu code mình dùng file của khu khác chưa commit, `snap-build` sẽ báo lỗi.
  Gặp vậy thì chờ, hoặc báo điều phối để commit đúng thứ tự:
  - shell của khu (S1a / S2a / S4a bước 0) trước, trang sau;
  - `theme.ts` của Site (S4a, export danh sách font) trước Site Studio của Seller (S1c, có ô chọn font).
- **Không push.** Gửi SHA cho điều phối; điều phối gom lại rồi push.

## 6. Tài nguyên dùng chung

- **RAM:** máy có 16 GB RAM, swap đã gần đầy, và các dự án khác cũng chạy trên máy này. VM từng tắt đột ngột vì hết
  RAM. Slice `nm3b.slice` có trần 10 GB, dùng chung cho 11 swarm và các server.
  - Không mở dev server mới; dùng 43110.
  - Server bản build chỉ bật trong lúc nghiệm thu.
  - Trình duyệt chỉ chạy bằng script node trong slice và dưới khoá (§4.4); không dùng Playwright MCP.
  - `snap-build.sh` tự chờ đến khi slice dưới 6 GB và `MemAvailable` trên 3 GB.
  - Nếu `MemAvailable` dưới 1,5 GB thì chờ rồi mới mở trình duyệt. Không kill tiến trình của dự án khác.
- **Cổng cấm đụng:** không restart, không kill 3111, 4777, 43101, 43110. Nếu 43110 hỏng, báo điều phối.
- **Cấm sửa:** `common.md`, `design.md`, `components/{ui,app}`, `styles/*`, `lib/**`, và file khu khác sở hữu.
  - Cần đổi thì gửi `nexusmart-93` đề xuất cụ thể.
  - Swarm ghi `THIẾU COMPONENT` thì cũng báo điều phối.
  - Được sửa brief swarm của mình trong `p1-4/`.
- **Luật giữ nguyên:**
  - không subagent Claude;
  - Claude không viết code sản phẩm;
  - không deploy, không `make down` / `make seed`;
  - không đổi mẫu đã xuất bản của 4 site seed;
  - không đưa secret production vào prompt.
- **Bản nháp chung:** S1c (Seller) và S4a (Site) dùng chung bản nháp site của shop người bán. Swarm nào cũng phải trả bản
  nháp về như cũ. Hai bên lệch nhau thì báo điều phối.

## 7. Báo cáo cho điều phối

SendMessage tới `nexusmart-93`, một dòng cho mỗi sự kiện, trỏ tới file thay vì dán log:

- `claim`;
- đã review shell (đạt hay không);
- commit: SHA và đường dẫn;
- cần đổi file chung;
- bị chặn;
- nghiệm thu xong khu: SHA, lệnh kiểm và kết quả, thư mục ảnh, phần chưa đạt.

Điều phối gửi thông báo chung (đổi `common.md`, đổi component chung, thứ tự commit) tới tên phiên của bạn.

## 8. Ghi chú review đã biết

- **Seller:** `/seller/page.tsx` phải nạp thêm `loadSellerBankAccount()` và `loadSellerSiteDomainsServer()` (spec A9).
  Brief S1a chưa ghi; thiếu thì follow-up.
- **Site:**
  - First Load JS ≤ 296 kB;
  - `SiteShell` giữ `data-site-root` và `data-site-theme-mode`;
  - xem đủ 8 mẫu qua gallery Site Studio, không qua mẫu đã xuất bản.
- **Admin:** không đổi `src/lib/admin-rbac.ts` và luồng phê duyệt 2 người.
