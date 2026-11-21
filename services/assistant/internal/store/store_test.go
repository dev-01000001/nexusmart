package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRedactPII(t *testing.T) {
	input := "Liên hệ nguyen.van.a@example.com hoặc SĐT 0912.345.678 (+84 987 654 321), token Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.abcdef123456 và key sk-ant-api03-abcdefghijk12345. Áo giá 350.000đ mã prod-ao-01."
	out := RedactPII(input)

	for _, leaked := range []string{
		"nguyen.van.a@example.com",
		"0912.345.678",
		"+84 987 654 321",
		"eyJhbGciOiJIUzI1NiJ9",
		"sk-ant-api03-abcdefghijk12345",
	} {
		if strings.Contains(out, leaked) {
			t.Errorf("expected %q to be redacted, got: %s", leaked, out)
		}
	}
	// Prices and product IDs must not be corrupted.
	if !strings.Contains(out, "350.000đ") || !strings.Contains(out, "prod-ao-01") {
		t.Errorf("expected benign price/ID to remain intact, got: %s", out)
	}
}

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	conv, err := st.EnsureConversation(ctx, Conversation{UserID: "user-1", ShopID: "shop-bach-dang"})
	if err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if conv.ID == "" {
		t.Fatal("expected generated conversation ID")
	}

	// Messages must be PII-redacted on save.
	err = st.SaveMessage(ctx, Message{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "Gọi tôi số 0901234567 hoặc mail buyer@test.vn nhé",
	})
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	msgs, err := st.ListMessages(ctx, conv.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if strings.Contains(msgs[0].Content, "0901234567") || strings.Contains(msgs[0].Content, "buyer@test.vn") {
		t.Fatalf("expected PII redacted in stored message, got: %s", msgs[0].Content)
	}

	// Feedback comment PII-redacted.
	if err := st.SaveFeedback(ctx, Feedback{
		ConversationID: conv.ID,
		MessageID:      msgs[0].ID,
		UserID:         "user-1",
		Helpful:        true,
		Comment:        "Tốt lắm, mail tôi test@abc.com",
	}); err != nil {
		t.Fatalf("SaveFeedback: %v", err)
	}
	fbs := st.FeedbackEntries()
	if len(fbs) != 1 || strings.Contains(fbs[0].Comment, "test@abc.com") {
		t.Fatalf("unexpected feedback state: %+v", fbs)
	}

	// Reviews and summary.
	if err := st.UpsertReviews(ctx, []Review{
		{ID: "r1", ProductID: "prod-1", ShopID: "shop-1", Rating: 5, Comment: "Vải mát"},
		{ID: "r2", ProductID: "prod-1", ShopID: "shop-1", Rating: 4, Comment: "Giao nhanh"},
	}); err != nil {
		t.Fatalf("UpsertReviews: %v", err)
	}
	revs, err := st.ListReviewsByProduct(ctx, "prod-1")
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListReviewsByProduct: len=%d err=%v", len(revs), err)
	}

	if _, err := st.GetReviewSummary(ctx, "prod-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := st.UpsertReviewSummary(ctx, ReviewSummary{
		ProductID:     "prod-1",
		ShopID:        "shop-1",
		ReviewCount:   2,
		AverageRating: 4.5,
		Summary:       "Sản phẩm tốt",
		Pros:          []string{"Vải mát"},
		Cons:          []string{},
	}); err != nil {
		t.Fatalf("UpsertReviewSummary: %v", err)
	}
	sum, err := st.GetReviewSummary(ctx, "prod-1")
	if err != nil || sum.Summary != "Sản phẩm tốt" || len(sum.Pros) != 1 {
		t.Fatalf("GetReviewSummary: sum=%+v err=%v", sum, err)
	}

	// Handoff ticket.
	if err := st.SaveHandoffTicket(ctx, HandoffTicket{
		ID:      "t-1",
		UserID:  "user-1",
		ShopID:  "shop-1",
		Reason:  "Hỗ trợ đơn hàng, gọi 0988777666",
		Summary: "Khách cần hỗ trợ đổi size",
	}); err != nil {
		t.Fatalf("SaveHandoffTicket: %v", err)
	}
	ticket, err := st.GetHandoffTicket(ctx, "t-1")
	if err != nil || strings.Contains(ticket.Reason, "0988777666") {
		t.Fatalf("GetHandoffTicket: ticket=%+v err=%v", ticket, err)
	}

	// Idempotent event marking.
	inserted, err := st.MarkEventProcessed(ctx, "evt-1", "review.created")
	if err != nil || !inserted {
		t.Fatalf("expected first event mark inserted=true, got inserted=%v err=%v", inserted, err)
	}
	inserted, err = st.MarkEventProcessed(ctx, "evt-1", "review.created")
	if err != nil || inserted {
		t.Fatalf("expected duplicate event mark inserted=false, got inserted=%v err=%v", inserted, err)
	}
}
