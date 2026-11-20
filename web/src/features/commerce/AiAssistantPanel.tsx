"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { AI_SUGGESTED_PROMPTS, ORDER_STATUS_META, PRODUCTS, SHOPS, getShopById } from "@/data";
import type { Order, Product } from "@/data";
import { api } from "@/lib/api";
import { BODY_SHAPE_LABELS, recommendSize, type BodyProfile } from "@/lib/sizing";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/Button";
import { IconClose, IconShield } from "@/components/Icons";
import styles from "./OverlayPanel.module.css";

interface AiTurn {
  id: string;
  role: "user" | "assistant";
  text: string;
  products?: Product[];
  orderRef?: Order;
  helpHref?: string;
}

interface QuickActionChip {
  id: string;
  label: string;
  prompt: string;
}

const SESSION_QUICK_CHIPS: QuickActionChip[] = [
  {
    id: "chip-order-lookup",
    label: "Tra cứu mã đơn",
    prompt: "Tra cứu mã đơn hàng gần nhất trong tài khoản của tôi",
  },
  {
    id: "chip-body-size",
    label: "Tư vấn size theo hồ sơ số đo",
    prompt: "Tư vấn size áo và quần theo hồ sơ số đo hiện tại của tôi",
  },
  {
    id: "chip-return-45",
    label: "Chính sách đổi trả 45 ngày",
    prompt: "Chính sách đổi trả 45 ngày và đổi size 2 chiều của NexusMart như thế nào?",
  },
];

function isOrderQuestion(prompt: string): boolean {
  const lower = prompt.toLowerCase();
  return (
    /NMP?-\d{6}-\d{2,4}/i.test(prompt) ||
    lower.includes("đơn") ||
    lower.includes("vận chuyển") ||
    lower.includes("trạng thái") ||
    lower.includes("tra cứu")
  );
}

function buildAiAnswer(
  prompt: string,
  orders: Order[],
  isAuthenticated: boolean,
  bodyProfile: BodyProfile
): { text: string; products: Product[]; orderRef?: Order; helpHref?: string } {
  const lower = prompt.toLowerCase();

  const codeMatch = prompt.toUpperCase().match(/NMP?-[A-Z0-9-]+/);
  if (codeMatch) {
    if (!isAuthenticated) {
      return {
        text: "Bạn chưa đăng nhập tài khoản. Hãy đăng nhập để trợ lý tra cứu đơn hàng của riêng bạn.",
        products: [],
        helpHref: "/dang-nhap",
      };
    }
    const found = orders.find(
      (o) =>
        o.orderCode.toUpperCase() === codeMatch[0] ||
        o.parentOrderCode?.toUpperCase() === codeMatch[0]
    );
    if (found) {
      const meta = ORDER_STATUS_META[found.status];
      const payNote =
        found.paymentStatus === "cho_thanh_toan"
          ? "Đơn đang chờ hoàn tất thanh toán (vẫn đang giữ sản phẩm cho bạn)."
          : found.isPaid
            ? "Đã thanh toán."
            : "Thanh toán khi nhận hàng (hỗ trợ đồng kiểm).";
      return {
        text: `Đơn ${found.orderCode} từ ${found.shopName} (${formatVnd(found.total)}): trạng thái ${meta.label}. ${payNote} Cập nhật gần nhất lúc ${formatDateTimeVi(found.updatedAt)}.`,
        products: [],
        orderRef: found,
      };
    }
    return {
      text: `Không tìm thấy mã ${codeMatch[0]} trong lịch sử đơn thuộc tài khoản của bạn.`,
      products: [],
      helpHref: "/don-hang",
    };
  }

  if (lower.includes("hồ sơ số đo") || lower.includes("số đo hiện tại")) {
    const shapeInfo = BODY_SHAPE_LABELS[bodyProfile.shape];
    const shopSummaries = SHOPS.map((shop) => {
      const topRec = recommendSize({
        heightCm: bodyProfile.heightCm,
        weightKg: bodyProfile.weightKg,
        shape: bodyProfile.shape,
        preferredFit: bodyProfile.preferredFit,
        garmentGroup: "top",
        shopId: shop.id,
      });
      const bottomRec = recommendSize({
        heightCm: bodyProfile.heightCm,
        weightKg: bodyProfile.weightKg,
        shape: bodyProfile.shape,
        preferredFit: bodyProfile.preferredFit,
        garmentGroup: "bottom",
        shopId: shop.id,
      });
      return `${shop.shortName}: Áo ${topRec.size}, Quần ${bottomRec.size}`;
    }).join(" · ");

    return {
      text: `Dựa trên hồ sơ số đo của bạn (${bodyProfile.heightCm} cm, ${bodyProfile.weightKg} kg, dáng ${shapeInfo.label}): ${shopSummaries}. Bạn có thể cập nhật số đo tại mục Hồ sơ số đo trong trang Tài khoản.`,
      products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-polo-detkim"].includes(p.id)),
      helpHref: "/tai-khoan",
    };
  }

  if (lower.includes("45 ngày") || lower.includes("đổi trả") || lower.includes("hoàn tiền")) {
    return {
      text: "NexusMart áp dụng đổi trả miễn phí trong 45 ngày (tối đa 3 lần/đơn): bưu tá đổi size 2 chiều tận nơi, hoàn chuyển khoản định kỳ vào Thứ 2 & Thứ 5 hằng tuần. Hàng Outlet, săn deal sốc và pack niêm phong không áp dụng đổi trả.",
      products: [],
      helpHref: "/tro-giup",
    };
  }

  const suggested = AI_SUGGESTED_PROMPTS.find((s) => s.prompt === prompt);
  if (suggested) {
    if (suggested.id === "ai-size") {
      return {
        text: suggested.response,
        products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-short-daily"].includes(p.id)),
      };
    }
    if (suggested.id === "ai-combo") {
      return {
        text: suggested.response,
        products: PRODUCTS.filter((p) => ["prod-polo-kem", "prod-short-linen"].includes(p.id)),
      };
    }
    if (suggested.id === "ai-fabric") {
      return {
        text: suggested.response,
        products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-at-dryfit"].includes(p.id)),
      };
    }
    if (suggested.id === "ai-return") {
      return {
        text: suggested.response,
        products: [],
        helpHref: "/tro-giup",
      };
    }
    if (suggested.id === "ai-membership") {
      return {
        text: suggested.response,
        products: [],
        helpHref: "/hoi-vien",
      };
    }
  }

  if (isOrderQuestion(prompt)) {
    if (!isAuthenticated) {
      return {
        text: "Để bảo vệ riêng tư, trợ lý chỉ xem đơn thuộc tài khoản của bạn. Vui lòng đăng nhập để kiểm tra đơn hàng.",
        products: [],
        helpHref: "/dang-nhap",
      };
    }
    const pendingPay = orders.find((o) => o.paymentStatus === "cho_thanh_toan");
    const targetOrder =
      pendingPay ??
      orders.find((o) => o.status === "dang_giao") ??
      orders.find((o) => o.status === "cho_xac_nhan") ??
      orders[0];
    if (!targetOrder) {
      return {
        text: "Tài khoản của bạn hiện chưa có đơn hàng nào trên NexusMart.",
        products: [],
        helpHref: "/danh-muc",
      };
    }
    const meta = ORDER_STATUS_META[targetOrder.status];
    const pendingNote =
      targetOrder.paymentStatus === "cho_thanh_toan"
        ? " Lưu ý: đơn này đang chờ hoàn tất thanh toán VietQR."
        : "";
    return {
      text: `Tài khoản của bạn hiện có ${orders.length} đơn hàng. Đơn gần nhất là ${targetOrder.orderCode} (${targetOrder.shopName}), trạng thái ${meta.label}, tổng ${formatVnd(targetOrder.total)}.${pendingNote}`,
      products: [],
      orderRef: targetOrder,
    };
  }

  const kgMatch = lower.match(/(\d{2,3})\s*(kg|kí|cân)/);
  if (lower.includes("size") || lower.includes("cỡ") || kgMatch) {
    const kg = kgMatch ? Number(kgMatch[1]) : bodyProfile.weightKg;
    const rec = recommendSize({
      heightCm: bodyProfile.heightCm,
      weightKg: kg,
      shape: bodyProfile.shape,
      preferredFit: bodyProfile.preferredFit,
      garmentGroup: "top",
      shopId: "shop-bach-dang",
    });
    return {
      text: `${rec.reason} Nếu chọn phom ôm gọn tại Dệt Kim Hà Nội, bạn có thể kiểm tra đối chiếu 4 xưởng trong mục Hồ sơ số đo.`,
      products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-at-oversize"].includes(p.id)),
    };
  }

  if (
    lower.includes("cotton") ||
    lower.includes("dry") ||
    lower.includes("vải") ||
    lower.includes("gsm")
  ) {
    return {
      text: "Cotton Compact 220gsm (95% cotton chải kỹ, 5% spandex) đứng phom và mềm da cho mặc hằng ngày, còn Dry-Fit 145gsm (micro-mesh) thoát mồ hôi nhanh cho chạy bộ và tập gym.",
      products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-at-dryfit"].includes(p.id)),
    };
  }

  if (
    lower.includes("combo") ||
    lower.includes("600") ||
    lower.includes("đi làm") ||
    lower.includes("rẻ")
  ) {
    return {
      text: "Gợi ý bộ đi làm gọn gàng dưới 600.000₫: Áo Polo Pique Cotton Kem nhạt phối Quần Short Linen pha Cotton Xám đá.",
      products: PRODUCTS.filter((p) => ["prod-polo-kem", "prod-short-linen"].includes(p.id)),
    };
  }

  const matched = PRODUCTS.filter((p) => {
    const hay = `${p.name} ${p.categoryName} ${p.fabric} ${p.description}`.toLowerCase();
    return lower
      .split(/\s+/)
      .filter((w) => w.length >= 2)
      .some((w) => hay.includes(w));
  }).slice(0, 2);

  if (matched.length > 0) {
    return {
      text: `Mình tìm thấy ${matched.length} sản phẩm phù hợp với yêu cầu "${prompt}" trên NexusMart. Bạn có thể mở xem chi tiết chất liệu và chọn size:`,
      products: matched,
    };
  }

  return {
    text: "Mình có thể tra cứu đơn hàng trong tài khoản của bạn, tư vấn size theo số đo cho cả 4 xưởng, giải thích chính sách đổi trả 45 ngày hoặc so sánh chất liệu vải.",
    products: PRODUCTS.filter((p) => ["prod-at-xamda", "prod-polo-xanhda"].includes(p.id)),
  };
}

export function AiAssistantPanel() {
  const { aiOpen, closeAi, bodyProfile } = useStore();
  const { isAuthenticated } = useSession();
  const [turns, setTurns] = useState<AiTurn[]>([]);
  const [question, setQuestion] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);

  const streamTimerRef = useRef<number | null>(null);
  const bodyRef = useRef<HTMLDivElement | null>(null);

  const stopStreaming = useCallback(() => {
    if (streamTimerRef.current !== null) {
      window.clearInterval(streamTimerRef.current);
      streamTimerRef.current = null;
    }
    setIsStreaming(false);
  }, []);

  useEffect(() => {
    return () => {
      if (streamTimerRef.current !== null) {
        window.clearInterval(streamTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    if (!aiOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeAi();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [aiOpen, closeAi]);

  useEffect(() => {
    if (!aiOpen || !bodyRef.current) return;
    bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
  }, [aiOpen, turns, isStreaming]);

  if (!aiOpen) return null;

  const runPrompt = async (promptText: string) => {
    const clean = promptText.trim();
    if (!clean) return;
    stopStreaming();

    // Fetch real buyer orders only under the user's own session cookie when an order question is asked
    let liveOrders: Order[] = [];
    if (isAuthenticated && isOrderQuestion(clean)) {
      try {
        const res = await api<{ items?: Order[] }>("/api/orders?page=1&page_size=20");
        liveOrders = res.items ?? [];
      } catch {
        liveOrders = [];
      }
    }

    const answer = buildAiAnswer(clean, liveOrders, isAuthenticated, bodyProfile);
    const userTurnId = `ai-u-${Date.now()}`;
    const assistantTurnId = `ai-a-${Date.now() + 1}`;

    setTurns((prev) => [
      ...prev,
      { id: userTurnId, role: "user", text: clean },
      { id: assistantTurnId, role: "assistant", text: "", products: [], orderRef: undefined },
    ]);
    setQuestion("");
    setIsStreaming(true);

    const words = answer.text.split(" ");
    let cursor = 0;

    streamTimerRef.current = window.setInterval(() => {
      cursor = Math.min(words.length, cursor + 3);
      const partial = words.slice(0, cursor).join(" ");
      const finished = cursor >= words.length;

      setTurns((prev) =>
        prev.map((t) =>
          t.id === assistantTurnId
            ? {
                ...t,
                text: partial,
                products: finished ? answer.products : [],
                orderRef: finished ? answer.orderRef : undefined,
                helpHref: finished ? answer.helpHref : undefined,
              }
            : t
        )
      );

      if (finished) {
        stopStreaming();
      }
    }, 32);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    void runPrompt(question);
  };

  const handleReset = () => {
    stopStreaming();
    setTurns([]);
  };

  return (
    <div
      className={styles.backdrop}
      role="dialog"
      aria-modal="true"
      aria-label="Trợ lý AI NexusMart"
      onClick={(e) => {
        if (e.target === e.currentTarget) closeAi();
      }}
    >
      <div className={styles.panel}>
        <div className={styles.header}>
          <div className={styles.headerText}>
            <p className={styles.title}>Trợ lý AI NexusMart</p>
            <p className={styles.subtitle}>
              Tra cứu đơn, tư vấn size 4 xưởng theo hồ sơ số đo và đổi trả 45 ngày
            </p>
          </div>
          <div className={styles.headerControls}>
            {turns.length > 0 && (
              <button type="button" className={styles.iconBtn} onClick={handleReset}>
                Làm mới
              </button>
            )}
            <button
              type="button"
              className={styles.iconBtn}
              onClick={closeAi}
              aria-label="Đóng trợ lý AI"
            >
              <IconClose size={18} />
            </button>
          </div>
        </div>

        <div className={styles.noticeBar}>
          <IconShield size={16} className={styles.noticeIcon} />
          <span>
            Hỗ trợ riêng tư theo tài khoản của bạn, chỉ đọc số đo và đơn hàng thuộc tài khoản đang
            đăng nhập.
          </span>
        </div>

        <div className={styles.body} ref={bodyRef}>
          <div className={styles.quickSection}>
            <span className={styles.quickTitle}>Hỏi nhanh</span>
            <div className={styles.quickChips} style={{ padding: 0 }}>
              {SESSION_QUICK_CHIPS.map((chip) => (
                <button
                  key={chip.id}
                  type="button"
                  className={styles.quickChip}
                  onClick={() => void runPrompt(chip.prompt)}
                >
                  {chip.label}
                </button>
              ))}
            </div>
          </div>

          <div className={styles.quickSection}>
            <span className={styles.quickTitle}>Gợi ý câu hỏi khác</span>
            <div className={styles.promptList}>
              {AI_SUGGESTED_PROMPTS.slice(0, 4).map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={styles.promptBtn}
                  onClick={() => void runPrompt(item.prompt)}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </div>

          {turns.map((turn, index) => {
            const isActiveStream =
              isStreaming && index === turns.length - 1 && turn.role === "assistant";
            return (
              <div
                key={turn.id}
                className={`${styles.msgGroup} ${
                  turn.role === "user" ? styles.msgGroupBuyer : styles.msgGroupSeller
                }`}
              >
                <span className={styles.msgMeta}>
                  {turn.role === "user" ? "Bạn" : "Trợ lý NexusMart"}
                </span>
                <div
                  className={`${styles.msgBubble} ${
                    turn.role === "user" ? styles.msgBuyer : styles.msgSeller
                  }`}
                >
                  {turn.text}
                  {isActiveStream && <span className={styles.caret} aria-hidden="true" />}
                </div>

                {turn.orderRef && (
                  <div className={styles.orderLinkBox}>
                    <span>
                      Đơn <strong className="num">{turn.orderRef.orderCode}</strong> ·{" "}
                      <span className="num">{formatVnd(turn.orderRef.total)}</span>
                    </span>
                    <Link
                      href={`/don-hang/${turn.orderRef.id}`}
                      className={styles.orderLinkAction}
                      onClick={closeAi}
                    >
                      Mở chi tiết đơn
                    </Link>
                  </div>
                )}

                {turn.helpHref && (
                  <div className={styles.orderLinkBox}>
                    <span>Đi nhanh tới mục liên quan</span>
                    <Link href={turn.helpHref} className={styles.orderLinkAction} onClick={closeAi}>
                      {turn.helpHref === "/tro-giup"
                        ? "Trung tâm trợ giúp"
                        : turn.helpHref === "/dang-nhap"
                          ? "Đăng nhập tài khoản"
                          : turn.helpHref === "/don-hang"
                            ? "Đơn hàng của tôi"
                            : turn.helpHref === "/hoi-vien"
                              ? "Hội viên Nexus"
                              : turn.helpHref === "/danh-muc"
                                ? "Khám phá sản phẩm"
                                : "Mở Hồ sơ số đo"}
                    </Link>
                  </div>
                )}

                {turn.products && turn.products.length > 0 && (
                  <div className={styles.recList}>
                    {turn.products.map((product) => {
                      const shop = getShopById(product.shopId);
                      return (
                        <div key={product.id} className={styles.recCard}>
                          <img
                            src={product.image}
                            alt={product.name}
                            width={896}
                            height={1200}
                            className={styles.recImg}
                          />
                          <div className={styles.recInfo}>
                            <Link
                              href={`/san-pham/${product.slug}`}
                              className={styles.recTitle}
                              onClick={closeAi}
                            >
                              {product.shortName}
                            </Link>
                            <span className={styles.recSub}>
                              {shop?.shortName} · {product.gsm}gsm ·{" "}
                              <strong className="num">{formatVnd(product.price)}</strong>
                            </span>
                          </div>
                          <Button
                            variant="secondary"
                            size="sm"
                            href={`/san-pham/${product.slug}`}
                            onClick={closeAi}
                          >
                            Xem mẫu
                          </Button>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}

          {isStreaming && (
            <div className={styles.streamingStatus} role="status" aria-live="polite">
              <span>Đang trả lời...</span>
              <Button variant="secondary" size="sm" onClick={stopStreaming}>
                Dừng
              </Button>
            </div>
          )}
        </div>

        <form className={styles.footer} onSubmit={handleSubmit}>
          <input
            type="text"
            className={styles.input}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder="Nhập mã đơn, chiều cao cân nặng..."
            aria-label="Câu hỏi cho trợ lý AI"
          />
          <Button type="submit" variant="primary" size="md" disabled={!question.trim()}>
            Hỏi
          </Button>
        </form>
      </div>
    </div>
  );
}
