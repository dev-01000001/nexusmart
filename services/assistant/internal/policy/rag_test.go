package policy_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexusmart/core/services/assistant/internal/policy"
)

func TestNormalizeVietnameseAndHashEmbed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"Đổi trả 7 ngày — Hoàn tiền QR", "doi tra 7 ngay — hoan tien qr"},
		{"Phí hoa hồng sàn 5% (500 bps)", "phi hoa hong san 5% (500 bps)"},
		{"COD tối đa 5.000.000 VNĐ", "cod toi da 5.000.000 vnd"},
		{"  Khoá   rút tiền   24 giờ  ", "khoa rut tien 24 gio"},
		// NFD decomposed combining acute/dot accents + đ.
		{"Đo\u0302\u0309i tra\u0309 ha\u0300ng", "doi tra hang"},
	}

	for _, tc := range cases {
		got := policy.NormalizeVietnamese(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeVietnamese(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	v1 := policy.HashEmbed256("chính sách đổi trả 7 ngày")
	v2 := policy.HashEmbed256("chinh sach doi tra 7 ngay")
	if len(v1) != 256 || len(v2) != 256 {
		t.Fatalf("expected 256-dim vectors, got %d and %d", len(v1), len(v2))
	}

	var normSq float64
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("accented and accent-folded embeddings diverged at dim %d: %f vs %f", i, v1[i], v2[i])
		}
		normSq += float64(v1[i]) * float64(v1[i])
	}
	if math.Abs(math.Sqrt(normSq)-1.0) > 1e-4 {
		t.Fatalf("expected unit-normalized vector, got L2 norm %f", math.Sqrt(normSq))
	}
}

func TestRAGSearchAllSixEmbeddedPolicies(t *testing.T) {
	t.Parallel()

	rag, err := policy.NewRAG("", "mock")
	if err != nil {
		t.Fatalf("NewRAG failed: %v", err)
	}

	queries := []struct {
		name           string
		query          string
		wantDocSlug    string
		wantTextSubstr string
	}{
		{
			name:           "returns accented",
			query:          "Chính sách đổi trả trong bao nhiêu ngày và bằng chứng ảnh video?",
			wantDocSlug:    "returns",
			wantTextSubstr: "7 ngày",
		},
		{
			name:           "returns no accents",
			query:          "nguoi ban khong phan hoi trong 3 ngay thi sao",
			wantDocSlug:    "returns",
			wantTextSubstr: "3 ngày",
		},
		{
			name:           "shipping carriers and address",
			query:          "so dia chi 34 tinh thanh va hang van chuyen GHN GHTK",
			wantDocSlug:    "shipping",
			wantTextSubstr: "GHN",
		},
		{
			name:           "payment cod limit and bom hang",
			query:          "han muc COD toi da 5 trieu va bom hang qua 3 lan",
			wantDocSlug:    "payment",
			wantTextSubstr: "5.000.000",
		},
		{
			name:           "payment vietqr timeout",
			query:          "thanh toan VietQR payOS SePay het han 15 phut",
			wantDocSlug:    "payment",
			wantTextSubstr: "15 phút",
		},
		{
			name:           "seller commission and bank lock",
			query:          "phi hoa hong san 5% 500 bps va doi tai khoan ngan hang khoa rut 24 gio",
			wantDocSlug:    "seller-fees",
			wantTextSubstr: "5%",
		},
		{
			name:           "promotions voucher stacking and flash sale",
			query:          "thu tu ap dung voucher gian hang voucher san freeship xu thuong",
			wantDocSlug:    "promotions",
			wantTextSubstr: "Voucher gian hàng",
		},
		{
			name:           "privacy argon2id jwt and 30 day delete",
			query:          "xoa tai khoan cho 30 ngay va access token 15 phut argon2id",
			wantDocSlug:    "privacy-security",
			wantTextSubstr: "30 ngày",
		},
	}

	ctx := context.Background()
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			chunks, searchErr := rag.Search(ctx, tc.query, 4)
			if searchErr != nil {
				t.Fatalf("Search(%q) error: %v", tc.query, searchErr)
			}
			if len(chunks) == 0 {
				t.Fatalf("Search(%q) returned 0 chunks", tc.query)
			}
			top := chunks[0]
			if top.DocSlug != tc.wantDocSlug {
				t.Errorf("Search(%q) top DocSlug = %q (citation %s), want %q", tc.query, top.DocSlug, top.Citation, tc.wantDocSlug)
			}
			wantPrefix := "/policies/" + tc.wantDocSlug + "#"
			if !strings.HasPrefix(top.Citation, wantPrefix) {
				t.Errorf("Citation = %q, want prefix %q", top.Citation, wantPrefix)
			}
			foundSubstr := false
			for _, c := range chunks {
				if strings.Contains(c.Content, tc.wantTextSubstr) || strings.Contains(c.Section, tc.wantTextSubstr) {
					foundSubstr = true
					break
				}
			}
			if !foundSubstr {
				t.Errorf("Search(%q) chunks did not contain %q", tc.query, tc.wantTextSubstr)
			}
		})
	}
}

func TestRAGReloadInstantReindex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	docPath := filepath.Join(dir, "custom.md")
	initial := "# Chính sách Bảo hành\n\n## Thời gian bảo hành\nSản phẩm được bảo hành 12 tháng chính hãng.\n"
	if err := os.WriteFile(docPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial policy: %v", err)
	}

	rag, err := policy.NewRAG(dir, "mock")
	if err != nil {
		t.Fatalf("NewRAG(dir) failed: %v", err)
	}

	chunks, err := rag.Search(context.Background(), "bao hanh bao nhieu thang", 2)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("initial search failed: chunks=%d err=%v", len(chunks), err)
	}
	if !strings.Contains(chunks[0].Content, "12 tháng") {
		t.Fatalf("expected 12 tháng in initial chunk, got %q", chunks[0].Content)
	}

	updated := "# Chính sách Bảo hành\n\n## Thời gian bảo hành\nSản phẩm được bảo hành 24 tháng đổi mới tận nơi.\n"
	if err := os.WriteFile(docPath, []byte(updated), 0o600); err != nil {
		t.Fatalf("update policy: %v", err)
	}
	if err := rag.Reload(context.Background()); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	chunks, err = rag.Search(context.Background(), "bao hanh 24 thang", 2)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("post-reload search failed: chunks=%d err=%v", len(chunks), err)
	}
	if !strings.Contains(chunks[0].Content, "24 tháng") {
		t.Fatalf("expected 24 tháng after reload, got %q", chunks[0].Content)
	}
	if chunks[0].Citation != "/policies/custom#thoi-gian-bao-hanh" {
		t.Fatalf("unexpected citation: %q", chunks[0].Citation)
	}
}

func TestRAGOffTopicReturnsEmpty(t *testing.T) {
	t.Parallel()

	rag, err := policy.NewRAG("", "mock")
	if err != nil {
		t.Fatalf("NewRAG failed: %v", err)
	}
	chunks, err := rag.Search(context.Background(), "dự báo thời tiết ngày mai tại Đà Nẵng", 4)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 policy chunks for off-topic weather query, got %d (top=%s)", len(chunks), chunks[0].Citation)
	}
}
