"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { SHOPS, getShopById } from "@/data";
import type { ChatMessage } from "@/data";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/Button";
import { IconClose, IconPhone } from "@/components/Icons";
import styles from "./OverlayPanel.module.css";

const QUICK_REPLIES = [
  "Tư vấn size theo chiều cao cân nặng",
  "Phí giao về tỉnh và thời gian nhận hàng?",
  "Hỗ trợ đổi size 2 chiều trong 45 ngày",
];

function buildVietnameseSellerReply(shopName: string, buyerText: string): string {
  const lower = buyerText.toLowerCase();
  if (
    lower.includes("size") ||
    lower.includes("cao") ||
    lower.includes("kg") ||
    lower.includes("nặng")
  ) {
    return `${shopName} chào bạn. Với phom Regular fit, khoảng 1m66-1m72 (60-67 kg) mặc size M, còn 1m72-1m77 (68-75 kg) mặc size L vừa gọn vai. Nếu mặc chưa vừa, shop hỗ trợ bưu tá đổi size 2 chiều miễn phí tận nơi trong 45 ngày ạ.`;
  }
  if (
    lower.includes("ship") ||
    lower.includes("giao") ||
    lower.includes("tỉnh") ||
    lower.includes("ngày")
  ) {
    return `Đơn từ ${shopName} xuất kho trong ngày khi xác nhận trước 16:00. Tuyến nội vùng 1-2 ngày (22.000₫-24.000₫), liên tỉnh 2-4 ngày, miễn phí ship gian hàng từ 199.000₫ hoặc áp mã FREESHIP25 từ 249.000₫ toàn sàn ạ.`;
  }
  if (
    lower.includes("đơn") ||
    lower.includes("mã") ||
    lower.includes("hủy") ||
    lower.includes("đổi")
  ) {
    return `${shopName} đã ghi nhận thông tin đơn hàng của bạn. Bạn có thể mở mục Đơn hàng để theo dõi vận đơn, yêu cầu đổi size 2 chiều miễn phí trong 45 ngày hoặc xuất hóa đơn VAT điện tử ạ.`;
  }
  if (lower.includes("tồn") || lower.includes("màu") || lower.includes("còn")) {
    return `Các size phổ biến từ M đến XL tại ${shopName} hiện đều còn sẵn tại kho. Khi bạn bấm Đặt hàng, sản phẩm đúng màu và size sẽ được giữ riêng cho đơn của bạn ngay ạ.`;
  }
  return `Cảm ơn bạn đã nhắn tin cho ${shopName}. Nhân viên tư vấn đã nhận tin nhắn và sẽ phản hồi chi tiết hơn trong ít phút tới. Nếu cần gọi gấp, bạn có thể liên hệ Hotline 1900 6845 (8:30 - 22:00).`;
}

export function BuyerChatPanel() {
  const { chatOpen, closeChat, chatShopId } = useStore();
  const { chatThreads, sendChatMessage, markChatReadByBuyer } = useMockStore();
  const { user } = useSession();
  const buyerName = user?.fullName?.trim() || "Khách hàng NexusMart";

  const [selectedShopId, setSelectedShopId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [typingShopId, setTypingShopId] = useState<string | null>(null);
  const [extraMessages, setExtraMessages] = useState<Record<string, ChatMessage[]>>({});
  const [simulatedError, setSimulatedError] = useState(false);

  const replyTimerRef = useRef<number | null>(null);
  const bodyRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (chatOpen && chatShopId) {
      setSelectedShopId(chatShopId);
      markChatReadByBuyer(chatShopId);
    }
  }, [chatOpen, chatShopId, markChatReadByBuyer]);

  useEffect(() => {
    if (!chatOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeChat();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [chatOpen, closeChat]);

  useEffect(() => {
    return () => {
      if (replyTimerRef.current !== null) {
        window.clearTimeout(replyTimerRef.current);
      }
    };
  }, []);

  const effectiveShopId = selectedShopId ?? chatShopId ?? chatThreads[0]?.shopId ?? SHOPS[0].id;
  const activeThread = useMemo(
    () => chatThreads.find((t) => t.shopId === effectiveShopId),
    [chatThreads, effectiveShopId]
  );
  const activeShop = useMemo(() => getShopById(effectiveShopId) ?? SHOPS[0], [effectiveShopId]);

  const visibleMessages: ChatMessage[] = useMemo(() => {
    if (activeThread) {
      return activeThread.messages;
    }
    const localList = extraMessages[activeShop.id] ?? [];
    return [
      {
        id: `welcome-${activeShop.id}`,
        sender: "seller",
        senderName: activeShop.name,
        text: `Xin chào ${buyerName}, ${activeShop.name} (${activeShop.city}) sẵn sàng hỗ trợ chọn size, chất liệu vải và kiểm tra đơn hàng cho bạn.`,
        timestamp: "2026-11-20T02:00:00+07:00",
      },
      ...localList,
    ];
  }, [activeThread, activeShop, extraMessages, buyerName]);

  useEffect(() => {
    if (!chatOpen || !bodyRef.current) return;
    bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
  }, [chatOpen, visibleMessages.length, typingShopId]);

  if (!chatOpen) return null;

  const dispatchMessage = (text: string) => {
    const clean = text.trim();
    if (!clean) return;
    setSimulatedError(false);

    const shopId = activeShop.id;
    const shopName = activeShop.name;

    if (activeThread) {
      sendChatMessage(activeThread.id, "buyer", clean, buyerName);
    } else {
      const msg: ChatMessage = {
        id: `local-buyer-${Date.now()}`,
        sender: "buyer",
        senderName: buyerName,
        text: clean,
        timestamp: new Date().toISOString(),
      };
      setExtraMessages((prev) => ({
        ...prev,
        [shopId]: [...(prev[shopId] ?? []), msg],
      }));
    }

    setDraft("");
    setTypingShopId(shopId);

    if (replyTimerRef.current !== null) {
      window.clearTimeout(replyTimerRef.current);
    }

    const threadIdToReply = activeThread?.id ?? null;
    replyTimerRef.current = window.setTimeout(() => {
      const replyText = buildVietnameseSellerReply(shopName, clean);
      if (threadIdToReply) {
        sendChatMessage(threadIdToReply, "seller", replyText);
      } else {
        const replyMsg: ChatMessage = {
          id: `local-seller-${Date.now()}`,
          sender: "seller",
          senderName: shopName,
          text: replyText,
          timestamp: new Date().toISOString(),
        };
        setExtraMessages((prev) => ({
          ...prev,
          [shopId]: [...(prev[shopId] ?? []), replyMsg],
        }));
      }
      setTypingShopId((current) => (current === shopId ? null : current));
    }, 850);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    dispatchMessage(draft);
  };

  return (
    <div
      className={styles.backdrop}
      role="dialog"
      aria-modal="true"
      aria-label="Hộp thoại nhắn tin với gian hàng"
      onClick={(e) => {
        if (e.target === e.currentTarget) closeChat();
      }}
    >
      <div className={styles.panel}>
        <div className={styles.header}>
          <div className={styles.headerText}>
            <p className={styles.title}>{activeShop.name}</p>
            <p className={styles.subtitle}>
              Phản hồi {activeShop.responseTime.toLowerCase()} · {activeShop.returnPolicy}
            </p>
          </div>
          <div className={styles.headerControls}>
            <Link
              href={`/gian-hang/${activeShop.slug}`}
              className={styles.iconBtn}
              onClick={closeChat}
            >
              Xem shop
            </Link>
            <button
              type="button"
              className={styles.iconBtn}
              onClick={closeChat}
              aria-label="Đóng khung nhắn tin"
            >
              <IconClose size={18} />
            </button>
          </div>
        </div>

        <div className={styles.noticeBar}>
          <IconPhone size={15} className={styles.noticeIcon} />
          <span>
            Cần hỗ trợ khẩn cấp hoặc khiếu nại? Hotline <strong className="num">1900 6845</strong>{" "}
            (8:30 - 22:00) ·{" "}
            <Link href="/tro-giup" className={styles.orderLinkAction} onClick={closeChat}>
              Mở Trung tâm trợ giúp
            </Link>
          </span>
        </div>

        <div className={styles.threadBar} role="tablist" aria-label="Chọn gian hàng trò chuyện">
          {SHOPS.map((shop) => {
            const thread = chatThreads.find((t) => t.shopId === shop.id);
            const isActive = shop.id === activeShop.id;
            return (
              <button
                key={shop.id}
                type="button"
                role="tab"
                aria-selected={isActive}
                className={`${styles.threadTab} ${isActive ? styles.threadTabActive : ""}`}
                onClick={() => {
                  setSelectedShopId(shop.id);
                  markChatReadByBuyer(shop.id);
                }}
              >
                <span>{shop.shortName}</span>
                {thread && thread.unreadByBuyer > 0 && !isActive && (
                  <span className={`${styles.unreadCount} num`}>{thread.unreadByBuyer}</span>
                )}
              </button>
            );
          })}
        </div>

        {activeThread?.productRef && (
          <div className={styles.productRefCard}>
            <img
              src={activeThread.productRef.image}
              alt={activeThread.productRef.name}
              width={896}
              height={1200}
              className={styles.productRefImg}
            />
            <div className={styles.productRefInfo}>
              <Link
                href={`/san-pham/${activeThread.productRef.slug}`}
                className={styles.productRefTitle}
                onClick={closeChat}
              >
                {activeThread.productRef.name}
              </Link>
              <span className={`${styles.productRefPrice} num`}>
                {formatVnd(activeThread.productRef.price)}
              </span>
            </div>
          </div>
        )}

        <div className={styles.body} ref={bodyRef}>
          {simulatedError ? (
            <div className={styles.emptyNote} role="alert">
              <p>Không thể đồng bộ tin nhắn mới lúc này. Vui lòng thử lại.</p>
              <div>
                <Button variant="secondary" size="sm" onClick={() => setSimulatedError(false)}>
                  Kết nối lại
                </Button>
              </div>
            </div>
          ) : (
            visibleMessages.map((msg) => (
              <div
                key={msg.id}
                className={`${styles.msgGroup} ${
                  msg.sender === "buyer" ? styles.msgGroupBuyer : styles.msgGroupSeller
                }`}
              >
                <span className={styles.msgMeta}>
                  {msg.senderName} · {formatDateTimeVi(msg.timestamp)}
                </span>
                <div
                  className={`${styles.msgBubble} ${
                    msg.sender === "buyer" ? styles.msgBuyer : styles.msgSeller
                  }`}
                >
                  {msg.text}
                </div>
              </div>
            ))
          )}

          {typingShopId === activeShop.id && (
            <div className={styles.streamingStatus} role="status" aria-live="polite">
              <span>{activeShop.name} đang soạn phản hồi...</span>
            </div>
          )}
        </div>

        <div className={styles.quickChips}>
          {QUICK_REPLIES.map((chip) => (
            <button
              key={chip}
              type="button"
              className={styles.quickChip}
              onClick={() => dispatchMessage(chip)}
            >
              {chip}
            </button>
          ))}
        </div>

        <form className={styles.footer} onSubmit={handleSubmit}>
          <input
            type="text"
            className={styles.input}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={`Nhắn cho ${activeShop.shortName}...`}
            aria-label="Nội dung tin nhắn gửi gian hàng"
          />
          <Button type="submit" variant="primary" size="md" disabled={!draft.trim()}>
            Gửi
          </Button>
        </form>
      </div>
    </div>
  );
}
