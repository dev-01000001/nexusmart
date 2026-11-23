# ADR-0006: Trợ lý AI — nhà cung cấp LLM, embedding tiếng Việt, RAG và guardrail

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-23

## Bối cảnh

- **Hiện trạng trong repo** (`docs/audit/plt.md` mục 0, `UC-AI01..AI14`, `PLT-D1`):
  - Chưa có `services/ai-assistant`, `proto/nexusmart/ai/`, `pgvector`, bộ eval hay guardrail nào.
  - Web chỉ có `web/src/features/commerce/AiAssistantPanel.tsx` mô phỏng streaming bằng `setInterval` trên mảng `PRODUCTS` tĩnh và lộ `INITIAL_ORDERS` mẫu cho mọi buyer đăng nhập (`PLT-D1`); `seller/chat` chỉ có 3 câu mẫu cứng.
- **Use case & tiêu chí đo trích từ `docs/spec.md`**:
  - **Ưu tiên Cao (bắt buộc)**:
    - `UC-AI01`: tư vấn mua hàng bằng hội thoại (RAG trên `pgvector`, trả product card + link, giữ ngữ cảnh; không có SP phù hợp thì nói rõ, không bịa; ngoài phạm vi từ chối lịch sự; LLM lỗi → fallback tìm kiếm từ khoá).
    - `UC-AI02`: tìm kiếm ngữ nghĩa hybrid (phối hợp `ADR-0004`).
    - `UC-AI13`: thu phản hồi hữu ích/không hữu ích, lưu hội thoại (ẩn PII), chạy **eval set ~100 câu** sau mỗi lần đổi prompt/model đo độ chính xác truy xuất, tỷ lệ trả lời có căn cứ (groundedness), tỷ lệ bịa SP (hallucination); giảm chỉ số thì chặn phát hành.
    - `UC-AI14`: guardrail đầu vào (độ dài, nội dung cấm, **bộ test prompt injection**), **hạn mức token theo người dùng** (vượt trả `429`) và **ngân sách token theo ngày** (hết ngân sách chuyển sang model rẻ hơn hoặc tắt tính năng phụ), giới hạn tool theo role, lọc đầu ra (không lộ system prompt/dữ liệu người khác), log token/chi phí/độ trễ.
  - **Ưu tiên Trung bình**: `UC-AI03` (tra cứu đơn bằng tool `getOrderStatus`), `UC-AI04` (thêm giỏ `addToCart` có bước xác nhận UI; **cấm AI tự checkout/thanh toán**), `UC-AI05` (job nền tóm tắt đánh giá khi ≥ 10 review, lưu cache), `UC-AI10` (gợi ý 1–3 câu trả lời chat cho seller), `UC-AI11` (RAG chính sách/FAQ kèm link gốc), `UC-AI12` (tóm tắt & chuyển tiếp ticket CSKH).
  - **Ưu tiên Thấp (chỉ đặc tả, chưa bắt buộc cài)**: `UC-AI06` (sinh mô tả), `UC-AI07` (gợi ý cá nhân hoá), `UC-AI08` (tìm bằng ảnh), `UC-AI09` (hỏi đáp số liệu seller).
  - **Tiêu chí kỹ thuật cứng** (`docs/spec.md:32,174`, `AGENTS.md`): viết bằng **Go** (`pgvector-go`, HTTP LLM API, SSE — không cần Python); **tool chạy bằng JWT của chính người dùng**, không dùng quyền riêng; streaming **SSE với token đầu tiên (TTFT) < 2 giây**.
  - **Ngoài phạm vi**: fine-tune model, tự host LLM, trợ lý giọng nói, AI tự thanh toán (`docs/spec.md:32`).
- **Tái sử dụng liên phân hệ**: `services/aiops` (`ADR-0007`, Rust) cũng cần gọi LLM API cho phân tích nguyên nhân gốc (`UC-OPS03`), nên việc chọn nhà cung cấp có ảnh hưởng kép.

## Ứng viên

### Phần A — Nhà cung cấp LLM qua API

Giá chuẩn tính theo **1 triệu token (MTok)**, tra cứu ngày `2026-11-23`:

1. **OpenAI API**
   - **Model tiêu biểu & giá**:
     - Chính (tốc độ + chi phí cân bằng): **GPT-5 Mini** (`$0.25` input / `$0.025` cached / `$2.00` output).
     - Dự phòng khi chạm trần ngân sách ngày (`UC-AI14`): **GPT-5 Nano** (`$0.05` input / `$0.005` cached / `$0.40` output) hoặc **GPT-4o Mini** (`$0.15` / `$0.60`).
     - Cao cấp (tùy chọn cho AIOps RCA): **GPT-5** (`$1.25` / `$10.00`) hoặc **GPT-4.1** (`$2.00` / `$8.00`).
   - **SDK Go / Rust**: Go chính thức `github.com/openai/openai-go` (**Apache-2.0**); Rust gọi REST/SSE trực tiếp bằng `reqwest` (đã có trong `services/payment`) hoặc `async-openai`.
   - **Kỹ thuật**: Function calling + Structured Outputs strict JSON rất ổn định; SSE TTFT ~0.5–1.2s trên dòng Mini/Nano; Prompt Caching giảm 90% input; hỗ trợ Batch API (-50%) cho `UC-AI05`/eval; dùng chung 1 API key với `text-embedding-3-small`.

2. **Google Gemini API (Google AI Studio / Vertex AI)**
   - **Model tiêu biểu & giá**:
     - Chính/rẻ: **Gemini 3.5 / 3.1 Flash-Lite** (`~$0.25–0.30` input / `$1.50–2.50` output); **Gemini 3.8 Flash** (`$0.75` input / `$3.75` output — giá mở đầu, có lộ trình lên `$1.50`/`$7.50`).
     - Cao cấp: **Gemini 3.1 Pro / 4 Carbon** (`$2.00–4.00` input / `$10.00–20.00` output).
   - **SDK Go / Rust**: Go chính thức `google.golang.org/genai` (**Apache-2.0**); Rust gọi REST/SSE bằng `reqwest`.
   - **Kỹ thuật**: Tiếng Việt rất tự nhiên, TTFT thấp (< 1s trên dòng Flash/Flash-Lite), có free tier Google AI Studio để dev thử nghiệm (nhưng free tier có thể dùng dữ liệu huấn luyện → sản xuất/demo có PII phải dùng Paid Tier), dùng chung 1 API key với `gemini-embedding-001`.

3. **Anthropic Claude API**
   - **Model tiêu biểu & giá**:
     - Chính/nhanh: **Claude Haiku 5.5** (từ `$0.10` input / `$0.50` output).
     - Cao cấp (mạnh cho RAG khắt khe & AIOps RCA): **Claude Sonnet 5.5** (`$2.00` input / `$0.10` cached / `$10.00` output); **Claude Opus 5.5** (`$4.00` / `$20.00`).
   - **SDK Go / Rust**: Go chính thức `github.com/anthropics/anthropic-sdk-go/v2` (**MIT**); Rust gọi Messages API SSE trực tiếp bằng `reqwest`.
   - **Kỹ thuật**: Tuân thủ ràng buộc grounded RAG ("không bịa sản phẩm") và kháng prompt injection rất tốt; Batch API giảm 50%; **điểm trừ**: không có embedding model chính chủ (phải ghép thêm nhà cung cấp thứ 2 như Voyage AI/OpenAI → 2 API key, 2 tài khoản thanh toán).

#### Bảng so sánh — Nhà cung cấp LLM API

| Tiêu chí | OpenAI (GPT-5 Mini / Nano) | Google Gemini (Flash-Lite / 3.8 Flash) | Anthropic Claude (Haiku 5.5 / Sonnet 5.5) |
| --- | --- | --- | --- |
| Độ khớp tiêu chí (TTFT < 2s, tool calling, tiếng Việt, SSE) | **Rất tốt** (tool calling strict schema ổn định nhất, TTFT < 1.2s) | **Rất tốt** (tiếng Việt mượt, TTFT < 1s; tool calling tốt) | **Rất tốt** (chống bịa & prompt injection xuất sắc; Haiku 5.5 TTFT < 1s) |
| Độ phức tạp vận hành | **Thấp** (1 API key chung LLM + Embedding) | **Thấp** (1 API key chung LLM + Embedding) | Trung bình (cần thêm nhà cung cấp Embedding riêng) |
| Chi phí (LLM chính + fallback) | **Rất thấp** ($0.25/$2.00; fallback $0.05/$0.40) | Thấp ($0.25–0.75 / $1.50–3.75; có free tier dev) | **Rất thấp ở Haiku** ($0.10/$0.50); trung bình ở Sonnet ($2/$10) |
| Giấy phép (SDK / Dịch vụ) | SDK **Apache-2.0**; API thương mại (không dùng API data để train) | SDK **Apache-2.0**; API thương mại (lưu ý tắt free tier khi có dữ liệu thật) | SDK **MIT**; API thương mại (không dùng API data để train) |
| Mức độ trưởng thành | Rất cao (chuẩn công nghiệp, Go SDK chín muồi) | Cao (`go-genai` mới thay `generative-ai-go`) | Rất cao (`anthropic-sdk-go/v2` ổn định) |

---

### Phần B — Mô hình Embedding cho tiếng Việt (lưu trên `pgvector v0.8.7`)

> **Lưu ý kỹ thuật `pgvector`**: chỉ mục HNSW của kiểu `vector` giới hạn **≤ 2.000 chiều** (còn `halfvec` cho tới 4.000 chiều). Chọn kích thước **1.024 hoặc 1.536 chiều** giúp dùng thẳng `vector` + HNSW tốn ít RAM và truy vấn < 30 ms trên 50.000 sản phẩm.

| Mô hình Embedding | Nhà cung cấp | Giá ($/1M tok) | Số chiều khuyến nghị | Chất lượng tiếng Việt | Độ phức tạp tích hợp |
| --- | --- | --- | --- | --- | --- |
| **`text-embedding-3-small`** | OpenAI | **$0.02** (Batch `$0.01`) | `1536` (hỗ trợ MRL cắt `768`/`1024`) | Khá–tốt (tăng mạnh khi kết hợp hybrid Meilisearch ở `ADR-0004`) | **Thấp nhất** nếu chọn OpenAI |
| **`voyage-4` / `voyage-4-lite`** | Voyage AI | `$0.06` / `$0.02` | `1024` (context 32k) | **Rất tốt** (dẫn đầu benchmark retrieval đa ngôn ngữ) | Trung bình (thêm 1 API key riêng) |
| **`gemini-embedding-001`** | Google | `$0.15` | `768` hoặc `1536` (MRL, tối đa 3072) | Tốt (hỗ trợ tốt tiếng Việt; có nhánh multimodal ở v2 `$0.20`) | **Thấp nhất** nếu chọn Gemini |
| **`Embed v4`** | Cohere | `$0.12` | `1536` (context 128k) | Rất tốt (mạnh tài liệu chính sách dài) | Trung bình (thêm 1 API key riêng) |
| `BGE-M3` *(tham khảo)* | Self-host (MIT) | ~$0 API (tốn GPU) | `1024` (dense+sparse) | Rất tốt tiếng Việt | **Loại** vì spec cấm tự host model (`docs/spec.md:32`) |

---

### Phần C — Chi phí ước tính cho đồ án & demo (2 tháng phát triển + eval + demo)

Giả định trung bình 1 lượt tư vấn RAG tốn `~2.500` input token (system prompt + top-5 SP/chính sách + lịch sử 4 lượt) và `~500` output token; 1 sản phẩm ~`200` token để tạo embedding:

| Hạng mục | Quy mô ước tính | Phương án OpenAI (`gpt-5-mini` + `3-small`) | Phương án Gemini (`3.5-flash-lite` + `gemini-emb-001`) | Phương án Claude (`haiku-5.5` + `voyage-4`) |
| --- | --- | --- | --- | --- |
| Index embedding ban đầu | 50.000 SP (~10M tok) + 20% re-index (~2M tok) | **$0.24** | **$1.80** | **$0.72** (`voyage-4`) |
| Chạy eval set 100 câu | 20 vòng chỉnh prompt/model (5M in + 1M out) | **$3.25** (~$2.00 có cache) | **$2.75** | **$1.00** (Haiku) / ~$12 (Sonnet) |
| Demo & test tương tác | 3.000 lượt chat (7.5M in + 1.5M out) | **$4.88** (~$3.20 có cache) | **$4.13** | **$1.50** (Haiku) |
| Tóm tắt đánh giá (`UC-AI05`) + gợi ý chat (`UC-AI10`) | 1.000 lượt (2M in + 0.4M out) | **$1.30** | **$1.10** | **$0.40** |
| AIOps RCA (`UC-OPS03`) | 100 sự cố (0.5M in + 0.15M out, dùng model mạnh) | **$2.13** (`gpt-5`) | **$2.50** (`3.1-pro`) | **$2.50** (`sonnet-5.5`) |
| **Tổng cộng dự kiến** | **Trọn gói 2 tháng + bảo vệ đồ án** | **~$10 – $12 USD** | **~$12 – $13 USD** | **~$6 – $17 USD** |

*(Đặt trần chi tiêu ngày `$1–2/ngày` ở ứng dụng + Monthly Hard Cap `$20/tháng` trên dashboard nhà cung cấp đảm bảo không phát sinh hoá đơn bất ngờ.)*

---

### Phần D — Cơ chế giữ khoá API ngoài repo & kiểm soát an toàn

1. **Local dev**: khai báo `LLM_API_KEY`, `EMBEDDING_API_KEY` trong `.env` (đã nằm trong `.gitignore`); `.env.example` chỉ chứa giá trị rỗng và mặc định `AI_LLM_MODE=mock` để CI / `make e2e` chạy không cần key thật (giống `VIETQR_MODE=mock`, `GHN_MODE=mock`).
2. **Staging / Production trên K8s (`ADR-0008`)**: tiêm qua **Kubernetes Secret** (quản lý bằng **SOPS `v3.13.3` + `age`** khi tự host, hoặc **External Secrets Operator `v2.15.0`** kéo từ Secret Manager khi lên cloud); tuyệt đối không ghi key vào `values.yaml`, Dockerfile hay CLI args.
3. **Tách biệt & giới hạn thiệt hại**: tách 2 API key riêng (`staging` và `production`), giới hạn spend limit từng key trên portal nhà cung cấp, xoay vòng định kỳ; mọi log JSON trong `pkg/common/logger` và Rust `aiops` tự động che `*_API_KEY` / `Authorization`.

## Quyết định

- **Đề xuất**:
  1. **Kiến trúc provider-agnostic**: xây `services/ai-assistant` bằng **Go** với 2 interface `LLMClient` (hỗ trợ SSE streaming + tool calling) và `Embedder` (tương tự pattern `Carrier` ở `services/shipping`), hỗ trợ **mode `mock`** cho CI/test và **tự động hạ cấp sang model rẻ hơn** khi chạm ngân sách ngày (`UC-AI14`).
  2. **Bộ model mặc định đề xuất**: **OpenAI `gpt-5-mini`** (model chính) + **`gpt-5-nano`** (fallback ngân sách) + **`text-embedding-3-small`** (`1536` dims) trên **`pgvector v0.8.7`** (`pgvector/pgvector:pg16`).
     - *Lý do*: chỉ cần **1 API key / 1 billing**, chi phí trọn gói đồ án thấp (~$10–12), function calling (`UC-AI03/AI04`) với strict schema ổn định nhất, TTFT < 1.2 giây, và khi kết hợp hybrid keyword từ Meilisearch (`ADR-0004`), chất lượng tìm kiếm tiếng Việt của `text-embedding-3-small` đạt mức rất tốt.
     - *Phương án thay thế ngang hàng*: nếu chủ dự án ưu tiên **Google Gemini** (`gemini-3.5-flash-lite` + `gemini-embedding-001`) hoặc **Claude Haiku 5.5 / Sonnet 5.5 + `voyage-4`**, kiến trúc interface cho phép chọn/đổi chỉ bằng biến môi trường `AI_PROVIDER`.
  3. **Bảo mật & Guardrail bắt buộc**:
     - **Zero-privilege tool execution**: mọi tool (`getOrderStatus`, `addToCart`) gọi gRPC sang `order`/`cart`/`product` bằng **đúng JWT Bearer của người dùng** (cấm dùng `SERVICE_JWT_SECRET`); `addToCart` chỉ tạo đề xuất có mã xác nhận để người dùng bấm đồng ý trên UI trước khi thực thi.
     - **Kiểm soát đầu vào/đầu ra & chi phí**: chặn input quá dài (> 1.500 ký tự), lọc pattern prompt injection, dùng system prompt cô lập dữ liệu truy xuất trong thẻ `<context>` (không tin dữ liệu mô tả sản phẩm từ seller); Redis token bucket `ai:quota:user:<uid>` (ví dụ 20.000 tok/ngày/user → `429` + `Retry-After`) và `ai:budget:day:<YYYY-MM-DD>`; output filter che system prompt/JWT/PII.
- **Điều kiện kích hoạt chuyển đổi**:
  - Đổi Embedding sang **`voyage-4`** nếu kết quả eval `UC-AI13` cho thấy Recall@5 tiếng Việt < 85% dù đã bật hybrid search.
  - Chuyển model chính sang **Claude Sonnet 5.5** hoặc **GPT-5** cho các luồng yêu cầu độ chính xác cao nếu tỷ lệ bịa SP (hallucination) trên eval set > 2%.
- **Phụ thuộc**: `ADR-0004` (`pgvector` & `Search.HybridSearch`), `ADR-0005` (tích hợp `UC-AI10` gợi ý trả lời chat và `UC-AI12` chuyển tiếp CSKH), `ADR-0007` (dùng chung provider/chính sách key cho AIOps RCA), `ADR-0008` (quản lý Secret trên K8s).
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **OpenAI 1-provider + interface trừu tượng (đề xuất)**:
  - *Tích cực*: đơn giản hoá billing và quản trị Secret (1 key), chi phí demo nhỏ, tool calling tin cậy, dễ đo lường token/chi phí trên từng request, có mode `mock` giúp CI không phụ thuộc mạng ngoài.
  - *Tiêu cực*: phụ thuộc mạng/SLA nhà cung cấp API bên ngoài (đã giảm thiểu bằng circuit breaker `gobreaker/v2` + fallback tìm kiếm từ khoá `UC-AI01`); chất lượng thuần vector tiếng Việt của `text-embedding-3-small` kém `voyage-4` một chút nếu tắt nhánh keyword.
- **Dùng 2 nhà cung cấp (Claude + Voyage)**:
  - *Tích cực*: groundedness và retrieval tiếng Việt top đầu.
  - *Tiêu cực*: phải quản lý và nạp tiền 2 tài khoản, 2 Secret, tăng công cấu hình ban đầu.

## Kế hoạch triển khai

| Task | Phạm vi | Tiêu chí nghiệm thu đo được |
| --- | --- | --- |
| `T1` Khung `services/ai-assistant` & `ai_db` (`pgvector`) | Tạo `proto/nexusmart/ai/v1/ai.proto`, `make proto`, cập nhật `docs/contracts.md`; dựng `services/ai-assistant` (`:50061/:8091`), migration `ai_db` (`policy_chunks` với `vector(1536)` HNSW, `conversations`, `messages`, `feedback`, `usage_logs`, `review_summaries`), adapter `LLMClient`/`Embedder` (có `mock` + OpenAI/Gemini/Claude) | `buf lint`, unit test adapter pass; `/healthz`, `/readyz`, `/metrics` hoạt động; đổi provider qua env không sửa code nghiệp vụ |
| `T2` RAG sản phẩm & chính sách + SSE streaming (`UC-AI01`, `UC-AI11`) | Nối `Search.HybridSearch` (`ADR-0004`) và index tài liệu chính sách vào `policy_chunks`; mở `POST /api/v1/ai/chat/stream` qua `gateway` (tắt buffer giống SSE payment); fallback tìm kiếm từ khoá khi LLM timeout | Đo trên stack staging: **TTFT < 2 giây** (p95); câu hỏi không có SP trả lời rõ không có, không sinh `product_id` giả; tắt LLM API tự trả kết quả tìm kiếm từ khoá |
| `T3` Function calling bằng JWT người dùng (`UC-AI03`, `UC-AI04`) | Cài tool `getOrderStatus` (gọi `OrderService` với user JWT) và `addToCart` (trả action cần người dùng bấm xác nhận rồi mới gọi `CartService` với user JWT); chặn hoàn toàn checkout/payment | Test bảo mật: buyer A hỏi đơn buyer B bị `OrderService` từ chối `403`, AI không rò thông tin; không gọi `AddToCart` khi chưa có bước xác nhận |
| `T4` Guardrail, hạn mức token & bộ test Prompt Injection (`UC-AI14`) | Cài bộ lọc input/output, giới hạn token theo user (`429`) & ngân sách ngày (tự hạ model) trên Redis; viết bộ **≥ 25 kịch bản tấn công prompt injection** (tiếng Việt + Anh: lộ system prompt, đọc đơn người khác, ép tạo giảm giá/thanh toán, injection giấu trong mô tả SP) | **100% kịch bản injection bị chặn** trong CI (kể cả trên provider thật trước release); vượt quota trả `429` có `Retry-After`; log đầy đủ token, chi phí ước tính, latency |
| `T5` Eval set 100 câu & tính năng TB (`UC-AI13`, `AI05`, `AI10`, `AI12`) | Xây bộ **100 câu eval chuẩn** (tư vấn SP, chính sách, ngoài phạm vi, không có hàng, tool calling) + CLI runner tính Recall@5, groundedness, tỷ lệ bịa SP; nút hữu ích/không hữu ích trên `AiAssistantPanel.tsx` (gỡ `INITIAL_ORDERS` mock `PLT-D1`); cài `UC-AI05`, `UC-AI10`, `UC-AI12` | Báo cáo định lượng tự động từ 100 câu eval: **tỷ lệ bịa SP < 2%**, **trả lời có căn cứ ≥ 90%**; script eval trả exit code ≠ 0 nếu chỉ số tụt quá ngưỡng |

## Câu hỏi chủ dự án cần trả lời

1. **Chủ dự án đang có sẵn tài khoản/credit API của nhà cung cấp nào** (OpenAI, Google Gemini, Anthropic Claude) và **ngân sách tối đa cho toàn bộ đợt phát triển + demo** là bao nhiêu USD?
2. **Có chốt dùng 1 nhà cung cấp duy nhất** (ví dụ OpenAI cho cả LLM + Embedding để gọn vận hành) hay chấp nhận **ghép 2 nhà cung cấp** (ví dụ Claude cho LLM + Voyage/OpenAI cho Embedding)?
3. **Ngưỡng đạt tối thiểu cho bộ eval 100 câu (`UC-AI13`)** để đưa vào báo cáo đồ án là bao nhiêu (đề xuất: Recall@5 ≥ 85%, Groundedness ≥ 90%, Hallucination SP ≤ 2%)?

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-23
- **Nguồn**:
  - OpenAI API & Go SDK: https://openai.com/api/pricing/, https://github.com/openai/openai-go
  - Google Gemini API & Go SDK: https://ai.google.dev/gemini-api/docs/pricing, https://github.com/googleapis/go-genai
  - Anthropic Claude API & Go SDK: https://www.anthropic.com/pricing, https://github.com/anthropics/anthropic-sdk-go
  - Voyage AI / Cohere Embeddings: https://docs.voyageai.com/, https://cohere.com/pricing
  - pgvector & `pgvector-go`: https://github.com/pgvector/pgvector, https://github.com/pgvector/pgvector-go
  - SOPS & External Secrets: https://github.com/getsops/sops, https://github.com/external-secrets/external-secrets
