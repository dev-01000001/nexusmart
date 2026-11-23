# Đợt 2b – agent `qa`
Luật: `w2-rules.md`, `w2b-plan.md` (mục 6, 8). Các agent khác đang làm song song.
## Sở hữu
`tests/e2e/*.go`; `tests/e2e-web/01..06`, `helpers.mjs`. KHÔNG đụng `tests/e2e-web/07_promotions.test.mjs` (agent web đang sửa), `tools/seed/**` (agent seed đang sửa), code service.
## Nguyên tắc
Chỉ đổi kỳ vọng khi luật 2b buộc đổi (ghi luật nào); không xoá hay nới assertion khác. Assertion về tiền phí phải GIỮ giá trị cũ, chỉ đổi nguồn đọc. Test đỏ vì lỗi thật: báo `file:dòng`, không sửa test cho xanh.
## Lượt 1: sửa file, CHƯA chạy e2e (seed đang dùng cùng stack, rate limit làm đỏ giả)
1. `06 RealCheckout_AtFlashPrice`: đơn có flash không nhận COD. Đổi sang VIETQR; khẳng định thêm COD với flash bị từ chối, trạng thái chờ thanh toán, hạn thanh toán ≤ 5 phút; phần kiểm còn lại giữ nguyên.
2. `16 InternalErrorMasked`: xoá danh mục còn sản phẩm (kể cả xoá mềm) nay trả 409. Tìm một đường khác tạo lỗi Internal thật, tất định, không phá trạng thái chung. Không có thì đổi thành khẳng định 409 với mã lỗi đúng và body không lộ SQL/tên bảng/chuỗi driver, và chỉ rõ unit test nào của gateway đang giữ phần che lỗi 500 (`file:dòng`; chưa có thì nói chưa có).
3. `13, 19, 20, 27, 28, 29` (và chỗ khác nếu có, tự rà): gateway trả `commission_vnd`, `commission_bps`, `payment_fee_vnd`, `payment_fee_bps` = 0 cho người mua. Chỗ nào đọc phí từ đơn lấy bằng token người mua thì đọc lại bằng token seller sở hữu shop hoặc admin. Mỗi bài thêm không quá một khẳng định rằng người mua thấy 0 (32 đã có, không lặp).
4. `33` các ca xuyên service từng phải bỏ qua/chờ service khác: bỏ điều kiện chờ, để test chạy thật.
5. `gofmt -l`, `go vet ./...`, `go test -run '^$' ./...` trong `tests/e2e` sạch. Dừng và báo cáo; manager sẽ gọi lượt 2 để chạy e2e khi stack rảnh.
