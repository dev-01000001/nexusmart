# Swarm H – QA tích hợp, đánh giá định lượng, báo cáo, demo (tháng 6)
Hợp đồng: `m-all-contract.md`. Bạn là swarm DUY NHẤT rebuild stack và chạy `make e2e`, `make seed`, `make e2e-web`. Khi dùng stack, tạo `.runs/stack.lock` (nội dung: "H"), xóa khi xong; E và F chờ file này.
Vòng lặp: mỗi 20–30 phút kiểm tra swarm nào đã có code build xanh (đọc `docs/reports/*.md`), rebuild service đó, viết e2e mới (`tests/e2e/16_*`+, `tests/e2e-web/` file mới) cho tính năng của nó, chạy chuỗi `make e2e && make seed && make e2e-web`, ghi lỗi vào `docs/reports/issues.md` cho swarm đích. Lặp đến khi mọi swarm xong.
Seed: thêm dữ liệu cho chat, trả hàng, template, catalog 20.000 sản phẩm (dùng `tools/catalog-gen` của A).
Cuối cùng:
- `docs/EVAL_REPORT.md`: bảng mọi tiêu chí đo của spec (cột "Yêu cầu kỹ thuật và tiêu chí đo") với số đo thật, nguồn số (lệnh, file log), đạt/chưa đạt.
- `docs/FINAL_REPORT.md`: kiến trúc 11 service + AIOps, quyết định, kết quả, giới hạn trung thực.
- `docs/DEMO.md`: kịch bản demo 15 phút từng bước, lệnh khởi động, tài khoản demo.
Gate cuối: `make lint`, `make test`, chuỗi `make e2e && make seed && make e2e-web && make e2e-web` EXIT 0, CI chỉ `workflow_dispatch`, chỉ 28080 và 3300 public. Dán đầu ra thật.
