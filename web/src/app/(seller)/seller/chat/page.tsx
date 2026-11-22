"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ORDER_STATUS_META, SHOPS, getSellerWorkspace, getShopById } from "@/data";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate, useSellerContext } from "@/features/seller/SellerContext";
import {
  getShopChatThreadsWithSession,
  parseUiState,
  sendExtraSellerChatMessage,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

const QUICK_REPLIES = [
  {
    label: "Tư vấn chọn size",
    text: "Chào anh/chị, vui lòng gửi chiều cao và cân nặng để xưởng tư vấn đúng size theo bảng đo thực tế ạ.",
  },
  {
    label: "Xác nhận giao trong ngày",
    text: "Đơn hàng của anh/chị đã đủ tồn kho SKU và sẽ được xưởng đóng gói bàn giao vận chuyển trong chuyến hôm nay ạ.",
  },
  {
    label: "Chính sách đổi size 45 ngày",
    text: "Gian hàng hỗ trợ đổi size 2 chiều tận nơi miễn phí trong 45 ngày với sản phẩm còn nguyên tem mác và chưa qua giặt tẩy ạ.",
  },
];

// Stable pale identity tints >= 60deg apart on the colour wheel (app-screens.md)
const AVATAR_TINTS = ["#e3edf0", "#e6efe8", "#efeae1", "#e9e6ef"];

function getInitials(fullName: string): string {
  const parts = fullName.trim().split(/\s+/);
  if (parts.length <= 1) return fullName.slice(0, 2).toUpperCase();
  return `${parts[0][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

function SellerChatContent() {
  return (
    <SellerShopGate pageTitle="Tin nhắn khách hàng">{() => <SellerChatInner />}</SellerShopGate>
  );
}

function SellerChatInner() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const { chatThreads, sendChatMessage, showToast } = useMockStore();
  const { shop, mockShopKey } = useSellerContext();
  const session = useSellerSession();
  const activeShop = getShopById(mockShopKey) ?? SHOPS[0];
  const displayShopName = shop?.name ?? activeShop.name;

  const [query, setQuery] = useState("");
  const [selectedThreadId, setSelectedThreadId] = useState<string>("");
  const [mobileView, setMobileView] = useState<"list" | "thread">("list");
  const [draft, setDraft] = useState("");

  const threads = useMemo(
    () => getShopChatThreadsWithSession(activeShop.id, chatThreads, session),
    [activeShop.id, chatThreads, session]
  );

  const shopOrders = useMemo(() => getSellerWorkspace(activeShop.id).orders, [activeShop.id]);

  const filteredThreads = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return threads;
    return threads.filter(
      (t) =>
        t.buyerName.toLowerCase().includes(q) ||
        (t.productRef?.name.toLowerCase().includes(q) ?? false) ||
        t.messages.some((m) => m.text.toLowerCase().includes(q))
    );
  }, [threads, query]);

  const activeThread = useMemo(() => {
    return (
      filteredThreads.find((t) => t.id === selectedThreadId) ??
      threads.find((t) => t.id === selectedThreadId) ??
      filteredThreads[0] ??
      threads[0]
    );
  }, [filteredThreads, threads, selectedThreadId]);

  const matchedOrder = useMemo(() => {
    if (!activeThread) return undefined;
    return shopOrders.find((o) => o.buyerName === activeThread.buyerName);
  }, [activeThread, shopOrders]);

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = draft.trim();
    if (!clean || !activeThread) return;
    if (activeThread.source === "store") {
      sendChatMessage(activeThread.id, "seller", clean);
    } else {
      sendExtraSellerChatMessage(activeThread.id, clean);
    }
    setDraft("");
    showToast(`Đã gửi phản hồi đến ${activeThread.buyerName}.`);
  };

  const handleSelectThread = (id: string) => {
    setSelectedThreadId(id);
    setMobileView("thread");
  };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tin nhắn khách hàng</h1>
          <p className={common.finding}>
            {displayShopName} · {formatInt(threads.length)} hội thoại đang theo dõi · Phản hồi trung
            bình {activeShop.responseTime.toLowerCase()}
          </p>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang đồng bộ hộp thư khách hàng gian hàng..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể kết nối hộp thư trò chuyện"
          message="Kênh tin nhắn trực tiếp tạm gián đoạn. Tin nhắn mới sẽ đồng bộ ngay khi kết nối lại."
          lastUpdated="02:18 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/chat"
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có tin nhắn khách hàng nào"
          message="Khi người mua nhắn hỏi về chất liệu, kích cỡ hoặc tình trạng đơn hàng, hội thoại sẽ hiển thị tại đây."
          actionLabel="Xem đơn hàng gian hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && (
        <section className={styles.inboxShell} aria-label="Hộp thư người bán">
          {/* Left column: Thread list */}
          <div
            className={`${styles.threadPane} ${mobileView === "thread" ? styles.paneHiddenMobile : ""}`}
          >
            <div className={styles.threadPaneHead}>
              <div className={common.searchWrap} style={{ width: "100%" }}>
                <span className={common.searchIcon}>
                  <IconSearch size={14} />
                </span>
                <input
                  type="search"
                  className={common.searchInput}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm khách hàng hoặc sản phẩm..."
                  aria-label="Tìm hội thoại khách hàng"
                />
              </div>
            </div>

            <div className={styles.threadList} role="listbox" aria-label="Danh sách hội thoại">
              {filteredThreads.length === 0 ? (
                <div style={{ padding: 16 }}>
                  <p className={common.cardMeta}>Không tìm thấy hội thoại phù hợp.</p>
                </div>
              ) : (
                filteredThreads.map((thread, idx) => {
                  const isSelected = activeThread?.id === thread.id;
                  const lastMsg = thread.messages[thread.messages.length - 1];
                  return (
                    <button
                      key={thread.id}
                      type="button"
                      role="option"
                      aria-selected={isSelected}
                      className={`${styles.threadItem} ${isSelected ? styles.threadItemSelected : ""}`}
                      onClick={() => handleSelectThread(thread.id)}
                    >
                      <span
                        className={styles.avatar}
                        style={{ backgroundColor: AVATAR_TINTS[idx % AVATAR_TINTS.length] }}
                        aria-hidden="true"
                      >
                        {getInitials(thread.buyerName)}
                      </span>
                      <span className={styles.threadBody}>
                        <span className={styles.threadTop}>
                          <span className={styles.threadName}>{thread.buyerName}</span>
                          {lastMsg && (
                            <span className={styles.threadTime}>
                              {formatDateTimeVi(lastMsg.timestamp).split(" · ")[0]}
                            </span>
                          )}
                        </span>
                        <span className={styles.threadSnippet}>
                          {lastMsg?.text ?? "Chưa có tin nhắn"}
                        </span>
                        <span className={styles.threadMetaRow}>
                          <span className={styles.threadProdTag}>
                            {thread.productRef?.name ?? activeShop.shortName}
                          </span>
                          {thread.unreadBySeller > 0 && (
                            <span className={styles.unreadCount}>{thread.unreadBySeller}</span>
                          )}
                        </span>
                      </span>
                    </button>
                  );
                })
              )}
            </div>
          </div>

          {/* Right column: Active conversation & order context */}
          <div
            className={`${styles.chatPane} ${mobileView === "list" ? styles.paneHiddenMobile : ""}`}
          >
            {activeThread ? (
              <>
                <div className={styles.chatHeader}>
                  <div className={styles.chatHeaderLeft}>
                    <button
                      type="button"
                      className={styles.mobileBackBtn}
                      onClick={() => setMobileView("list")}
                    >
                      Danh sách
                    </button>
                    <div>
                      <h2 className={styles.chatBuyerName}>{activeThread.buyerName}</h2>
                      <p className={styles.chatBuyerSub}>
                        {activeThread.source === "store"
                          ? "Đồng bộ trực tiếp với khung chat người mua"
                          : `Khách hàng gian hàng ${activeShop.shortName}`}
                      </p>
                    </div>
                  </div>
                  {matchedOrder && (
                    <span className={common.pill}>
                      {ORDER_STATUS_META[matchedOrder.status].label}
                    </span>
                  )}
                </div>

                {/* Product & customer order context */}
                {(activeThread.productRef || matchedOrder) && (
                  <div className={styles.contextStrip}>
                    {activeThread.productRef && (
                      <div className={styles.contextProduct}>
                        <img
                          src={activeThread.productRef.image}
                          alt={activeThread.productRef.name}
                          className={styles.contextThumb}
                          width={30}
                          height={40}
                        />
                        <div>
                          <div className={styles.contextProdName}>
                            {activeThread.productRef.name}
                          </div>
                          <div className={styles.contextProdPrice}>
                            {formatVnd(activeThread.productRef.price)}
                          </div>
                        </div>
                      </div>
                    )}
                    {matchedOrder && (
                      <div className={styles.contextOrder}>
                        <span>
                          Đơn <strong>{matchedOrder.orderCode}</strong> (
                          {formatVnd(matchedOrder.subtotal)})
                        </span>
                        <Link
                          href={`/seller/don-hang/${matchedOrder.id}`}
                          className={styles.contextOrderLink}
                        >
                          Mở đơn
                        </Link>
                      </div>
                    )}
                  </div>
                )}

                <div className={styles.messages} aria-live="polite">
                  {activeThread.messages.map((msg) => {
                    const isSeller = msg.sender === "seller";
                    return (
                      <div
                        key={msg.id}
                        className={`${styles.msgGroup} ${isSeller ? styles.msgSeller : styles.msgBuyer}`}
                      >
                        <div
                          className={`${styles.bubble} ${isSeller ? styles.bubbleSeller : styles.bubbleBuyer}`}
                        >
                          {msg.text}
                        </div>
                        <span className={styles.msgMeta}>
                          {msg.senderName} · {formatDateTimeVi(msg.timestamp)}
                        </span>
                      </div>
                    );
                  })}
                </div>

                <form className={styles.composerArea} onSubmit={handleSend}>
                  <div className={styles.quickReplies} aria-label="Mẫu trả lời nhanh">
                    {QUICK_REPLIES.map((tpl) => (
                      <button
                        key={tpl.label}
                        type="button"
                        className={styles.quickReplyBtn}
                        onClick={() => setDraft(tpl.text)}
                      >
                        {tpl.label}
                      </button>
                    ))}
                  </div>
                  <div className={styles.composerRow}>
                    <input
                      type="text"
                      className={styles.composerInput}
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      placeholder={`Nhắn phản hồi cho ${activeThread.buyerName}...`}
                      aria-label="Nhập nội dung phản hồi"
                    />
                    <Button type="submit" variant="ink" size="sm" shape="app">
                      Gửi phản hồi
                    </Button>
                  </div>
                </form>
              </>
            ) : (
              <div style={{ padding: 24 }}>
                <p className={common.cardMeta}>Chọn một hội thoại bên trái để xem và phản hồi.</p>
              </div>
            )}
          </div>
        </section>
      )}
    </div>
  );
}

export default function SellerChatPage() {
  return (
    <SellerSuspense>
      <SellerChatContent />
    </SellerSuspense>
  );
}
