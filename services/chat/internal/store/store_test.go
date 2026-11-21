package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestMemStoreConversationMessagesAndCursors(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()

	// Idempotent CreateOrGetConversation.
	c1, created, err := st.CreateOrGetConversation(ctx, "buyer-1", "shop-1", "shop1.nexusmart.localhost")
	if err != nil || !created {
		t.Fatalf("expected created conversation, got created=%v err=%v", created, err)
	}
	c2, created2, err := st.CreateOrGetConversation(ctx, "buyer-1", "shop-1", "shop1.nexusmart.localhost")
	if err != nil || created2 || c2.ID != c1.ID {
		t.Fatalf("expected idempotent conversation %s, got id=%s created=%v err=%v", c1.ID, c2.ID, created2, err)
	}

	// Send 5 messages + duplicate client_msg_id retry.
	var msgs []Message
	for i := 1; i <= 5; i++ {
		role := RoleBuyer
		sender := "buyer-1"
		if i%2 == 0 {
			role = RoleSeller
			sender = "seller-1"
		}
		m, wasCreated, err := st.CreateMessage(ctx, CreateMessageInput{
			ConversationID: c1.ID,
			SenderID:       sender,
			SenderRole:     role,
			Kind:           KindText,
			Body:           "msg-" + strconv.Itoa(i),
			ClientMsgID:    "cid-" + strconv.Itoa(i),
		})
		if err != nil || !wasCreated {
			t.Fatalf("create message %d failed: created=%v err=%v", i, wasCreated, err)
		}
		msgs = append(msgs, m)
	}

	dup, wasCreated, err := st.CreateMessage(ctx, CreateMessageInput{
		ConversationID: c1.ID,
		SenderID:       "buyer-1",
		SenderRole:     RoleBuyer,
		Kind:           KindText,
		Body:           "msg-1",
		ClientMsgID:    "cid-1",
	})
	if err != nil || wasCreated || dup.ID != msgs[0].ID {
		t.Fatalf("expected idempotent retry returning %s, got id=%s created=%v err=%v", msgs[0].ID, dup.ID, wasCreated, err)
	}

	// Paginate backwards using numeric seq and UUID cursors.
	page1, err := st.ListMessages(ctx, ListMessagesInput{ConversationID: c1.ID, Limit: 2})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1.Messages) != 2 || page1.Messages[0].Seq != msgs[3].Seq || page1.Messages[1].Seq != msgs[4].Seq || !page1.HasMore {
		t.Fatalf("unexpected page1: %+v", page1)
	}

	page2, err := st.ListMessages(ctx, ListMessagesInput{ConversationID: c1.ID, Before: page1.NextBefore, Limit: 2})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2.Messages) != 2 || page2.Messages[0].Seq != msgs[1].Seq || page2.Messages[1].Seq != msgs[2].Seq || !page2.HasMore {
		t.Fatalf("unexpected page2: %+v", page2)
	}

	// Use UUID cursor for page3.
	page3, err := st.ListMessages(ctx, ListMessagesInput{ConversationID: c1.ID, Before: page2.Messages[0].ID, Limit: 2})
	if err != nil {
		t.Fatalf("list page3: %v", err)
	}
	if len(page3.Messages) != 1 || page3.Messages[0].Seq != msgs[0].Seq || page3.HasMore {
		t.Fatalf("unexpected page3: %+v", page3)
	}

	// Sync newer messages using after cursor (both numeric seq and UUID).
	syncRes, err := st.ListMessages(ctx, ListMessagesInput{ConversationID: c1.ID, After: msgs[1].ID, Limit: 10})
	if err != nil {
		t.Fatalf("sync after uuid: %v", err)
	}
	if len(syncRes.Messages) != 3 || syncRes.Messages[0].Seq != msgs[2].Seq || syncRes.Messages[2].Seq != msgs[4].Seq {
		t.Fatalf("unexpected sync result: %+v", syncRes)
	}

	// Unread counts & monotonic MarkRead.
	// Buyer has 2 seller msgs (seq 2, 4); Seller has 3 buyer msgs (seq 1, 3, 5).
	buyerConvs, err := st.ListConversations(ctx, ListConversationsFilter{UserID: "buyer-1", Scope: ScopeBuyer})
	if err != nil || buyerConvs.Total != 1 || buyerConvs.TotalUnread != 2 {
		t.Fatalf("unexpected buyer list: %+v err=%v", buyerConvs, err)
	}
	sellerConvs, err := st.ListConversations(ctx, ListConversationsFilter{UserID: "seller-1", CallerShopID: "shop-1", Scope: ScopeSeller, UnrepliedOnly: true})
	if err != nil || sellerConvs.Total != 1 || sellerConvs.TotalUnread != 3 {
		t.Fatalf("unexpected seller unreplied list: %+v err=%v", sellerConvs, err)
	}

	// Mark read up to msg[1] (seq 2) for buyer -> 1 remaining unread (seq 4).
	mr1, err := st.MarkRead(ctx, c1.ID, RoleBuyer, 0, msgs[1].ID)
	if err != nil || mr1.LastReadSeq != msgs[1].Seq || mr1.UnreadCount != 1 {
		t.Fatalf("unexpected mr1: %+v err=%v", mr1, err)
	}
	// Mark read to default latest -> 0 remaining unread.
	mr2, err := st.MarkRead(ctx, c1.ID, RoleBuyer, 0, "")
	if err != nil || mr2.LastReadSeq != msgs[4].Seq || mr2.UnreadCount != 0 {
		t.Fatalf("unexpected mr2: %+v err=%v", mr2, err)
	}
	// Out-of-order smaller seq must NOT move cursor backward.
	mr3, err := st.MarkRead(ctx, c1.ID, RoleBuyer, msgs[0].Seq, "")
	if err != nil || mr3.LastReadSeq != msgs[4].Seq || mr3.UnreadCount != 0 {
		t.Fatalf("expected monotonic cursor %d, got %+v err=%v", msgs[4].Seq, mr3, err)
	}

	// Atomic cooldown claim.
	t0 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	ok, err := st.ClaimAutoReplySlot(ctx, c1.ID, t0, 30*time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim should succeed: ok=%v err=%v", ok, err)
	}
	ok2, err := st.ClaimAutoReplySlot(ctx, c1.ID, t0.Add(10*time.Minute), 30*time.Minute)
	if err != nil || ok2 {
		t.Fatalf("second claim within cooldown should fail: ok=%v err=%v", ok2, err)
	}
	ok3, err := st.ClaimAutoReplySlot(ctx, c1.ID, t0.Add(30*time.Minute), 30*time.Minute)
	if err != nil || !ok3 {
		t.Fatalf("claim after cooldown should succeed: ok=%v err=%v", ok3, err)
	}

	// Multi-conversation missed messages only returns conversations accessible to caller.
	cOther, _, _ := st.CreateOrGetConversation(ctx, "buyer-other", "shop-other", "")
	_, _, _ = st.CreateMessage(ctx, CreateMessageInput{
		ConversationID: cOther.ID,
		SenderID:       "buyer-other",
		SenderRole:     RoleBuyer,
		Kind:           KindText,
		Body:           "secret other convo",
	})
	missed, err := st.ListMissedMessages(ctx, "buyer-1", "", strconv.FormatInt(msgs[2].Seq, 10), 20)
	if err != nil {
		t.Fatalf("list missed: %v", err)
	}
	if len(missed.Messages) != 2 || missed.Messages[0].Seq != msgs[3].Seq || missed.Messages[1].Seq != msgs[4].Seq {
		t.Fatalf("unexpected missed messages (must not leak other conv): %+v", missed)
	}

	// Mutually exclusive cursors rejected.
	if _, err := st.ListMessages(ctx, ListMessagesInput{ConversationID: c1.ID, Before: "5", After: "1"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for before+after, got %v", err)
	}
}
