# Agent `report`: báo cáo tổng hợp cho chủ dự án

Viết MỘT file `docs/audit/README.md` (tiếng Việt, gọn, cho chủ dự án đọc; không văn mẫu, không emoji). Chỉ ghi file đó. Không sửa code, không git write.

Nguồn (đọc hết): bảng đối chiếu Shopify/sàn mục 2 trong 8 báo cáo `docs/audit/{usr,cat,ord,pay,prm,shp,plt,web}.md`; ma trận `.agents/briefs/fix/uc-matrix.tsv`; kế hoạch `.agents/briefs/fix/plan.md` (mô hình tiền, nguyên nhân gốc, các đợt); sổ kiểm chứng `.agents/briefs/fix/verify-log.md` (mức đã hạ, nghiệm thu đợt 1, rủi ro còn tồn); `docs/adr/0004..0008`; `docs/spec.md` (tab Phạm vi, Use case). Thay đổi đợt 1 xem bằng `git diff --stat` và các báo cáo vòng sửa trong verify-log.

## Cấu trúc bắt buộc
1. Tóm tắt 5–8 dòng: phạm vi rà, cách rà (stack chạy thật, 8 agent, manager tự kiểm), kết quả trước/sau đợt 1, việc chờ chủ dự án quyết.
2. Bảng theo đúng khung chủ dự án đưa (19 nhóm Shopify + 6 nhóm nghiệp vụ sàn, lấy nguyên tên nhóm như các báo cáo): `| Nhóm | Nghiệp vụ trong nhóm | UC tương ứng hoặc "ngoài phạm vi" (dẫn dòng spec) | Trước đợt 1 | Sau đợt 1 | Phần còn lại thuộc đợt/ADR nào |`. Trạng thái dùng: Đủ / Một phần / Thiếu / Lỗi / Ngoài phạm vi. "Sau đợt 1" chỉ đổi khi verify-log ghi đã sửa và nghiệm thu.
3. Bảng UC theo mức ưu tiên (Cao/Trung bình/Thấp × trạng thái) trước đợt 1, và danh sách UC Cao còn chưa Đủ sau đợt 1 kèm lý do.
4. Lỗi đã sửa ở đợt 1: theo 10 nguyên nhân gốc (R1–R10) và các lỗi do review độc lập tìm thêm, mỗi dòng: mức, mô tả một câu, bằng chứng (test e2e/unit hoặc kiểm DB).
5. "Dữ liệu chuẩn chưa": gom các mục `*-D*` trong 8 báo cáo (ví dụ: 18 sản phẩm seed so với spec 20.000–50.000; suất flash sale seed vượt tồn kho; giá gạch lấy từ dữ liệu tĩnh của web; tính năng mock vẫn có link ở header như Chat, Trợ lý AI, Hội viên; hoa hồng theo shop lệch) và ghi đợt xử lý.
6. Rủi ro còn tồn và hồi quy do đợt 1 (lấy nguyên từ cuối verify-log, nói thẳng: huỷ một đơn con VietQR chưa trả làm đơn con còn lại không thanh toán được là hồi quy do bản sửa P0-3).
7. Việc chờ chủ dự án quyết: commit đợt 1; chạy đợt 2; các câu hỏi mở của ADR 0004–0008.
8. Cách chạy và kiểm: stack `nexusmart-audit` (web http://localhost:43000, gateway http://localhost:48080, Mailpit http://localhost:48025), `.env` gốc trỏ `make up`/`make seed` sang project này; hai stack `nexusmart` và `nexusmart-web` vẫn chạy code trước khi sửa, nên reset thay vì nâng cấp tại chỗ (đơn COD cũ theo mô hình cũ không có migration dữ liệu); lệnh gate.

Câu trả lời cuối: đường dẫn file và 5 dòng tóm tắt.
