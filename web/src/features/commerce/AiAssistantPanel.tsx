"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import {
  AI_SUGGESTED_PROMPTS,
  INITIAL_ORDERS,
  ORDER_STATUS_META,
  PRODUCTS,
  SHOPS,
  getShopById,
} from "@/data";
import type { Order, Product } from "@/data";
import { BODY_SHAPE_LABELS, recommendSize, type BodyProfile } from "@/lib/sizing";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
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
    id: "chip-return-7",
    label: "Chính sách đổi trả 7 ngày",
    prompt: "Chính sách trả hàng và hoàn tiền trong 7 ngày của NexusMart như thế nào?",
  },
];

function buildAiAnswer(
  prompt: string,
  orders: Order[],
  isAuthenticated: boolean,
  bodyProfile: BodyProfile
): { text: string; products: Product[]; orderRef?: Order; helpHref?: string } {
  const lower = prompt.toLowerCase();

  // Check explicit order code typed by user (e.g. NM-261120-109)
  const codeMatch = prompt.toUpperCase().match(/NMP?-\d{6}-\d{2,3}/);
  if (codeMatch) {
    if (!isAuthenticated) {
      return {
        text: "Bạn chưa đăng nhập tài khoản. Hãy đăng nhập để trợ lý tra đơn của bạn, hoặc dùng trang Tra cứu đơn hàng bằng Mã đơn và Số điện thoại.",
        products: [],
        helpHref: "/tra-cuu-don-hang",
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
      text: `Không tìm thấy mã ${codeMatch[0]} trong lịch sử đơn thuộc tài khoản của bạn. Nếu đặt không cần tài khoản, bạn có thể tra tại trang Tra cứu đơn hàng.`,
      products: [],
      helpHref: "/tra-cuu-don-hang",
    };
  }

  // Size recommendation from saved bodyProfile across all 4 shops
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

  // 7-day return & refund policy
  if (
    lower.includes("7 ngày") ||
    lower.includes("45 ngày") ||
    lower.includes("đổi trả") ||
    lower.includes("hoàn tiền")
  ) {
    return {
      text: "NexusMart hỗ trợ yêu cầu trả hàng và hoàn tiền trong vòng 7 ngày kể từ khi nhận hàng. Bạn có thể mở trang chi tiết đơn hàng để gửi yêu cầu và theo dõi tiến độ xử lý.",
      products: [],
      helpHref: "/tro-giup",
    };
  }

  // Exact match to suggested prompts first for crisp deterministic replies + real products
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
    if (suggested.id === "ai-order") {
      if (!isAuthenticated) {
        return {
          text: "Bạn chưa đăng nhập tài khoản. Hãy đăng nhập để trợ lý kiểm tra đơn hàng của bạn hoặc mở trang Tra cứu đơn hàng.",
          products: [],
          helpHref: "/tra-cuu-don-hang",
        };
      }
      const pendingPay = orders.find((o) => o.paymentStatus === "cho_thanh_toan");
      const shipping =
        orders.find((o) => o.status === "dang_giao") ??
        pendingPay ??
        orders.find((o) => o.status === "dang_chuan_bi") ??
        orders[0];
      if (!shipping) {
        return {
          text: "Tài khoản của bạn hiện chưa có đơn hàng nào.",
          products: [],
        };
      }
      const meta = ORDER_STATUS_META[shipping.status];
      return {
        text: `Đơn hàng ${shipping.orderCode} từ ${shipping.shopName} (tổng thanh toán ${formatVnd(shipping.total)}) hiện ở trạng thái ${meta.label}. Cập nhật gần nhất lúc ${formatDateTimeVi(shipping.updatedAt)}: ${shipping.timeline[shipping.timeline.length - 1]?.note ?? meta.description}`,
        products: [],
        orderRef: shipping,
      };
    }
  }

  // Order lookup via user session
  if (
    lower.includes("đơn") ||
    lower.includes("vận chuyển") ||
    lower.includes("trạng thái") ||
    lower.includes("tra cứu")
  ) {
    if (!isAuthenticated) {
      return {
        text: "Để bảo vệ riêng tư, trợ lý chỉ xem đơn thuộc tài khoản của bạn. Vui lòng đăng nhập tại trang Tài khoản hoặc dùng trang Tra cứu đơn hàng bằng Mã đơn và Số điện thoại.",
        products: [],
        helpHref: "/tra-cuu-don-hang",
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
        text: "Tài khoản của bạn hiện chưa phát sinh đơn hàng nào trên NexusMart.",
        products: [],
      };
    }
    const meta = ORDER_STATUS_META[targetOrder.status];
    const pendingNote =
      targetOrder.paymentStatus === "cho_thanh_toan"
        ? " Lưu ý: đơn này đang chờ thanh toán lại."
        : "";
    return {
      text: `Tài khoản của bạn hiện có ${orders.length} đơn hàng. Đơn cần chú ý nhất là ${targetOrder.orderCode} (${targetOrder.shopName}), trạng thái ${meta.label}, tổng ${formatVnd(targetOrder.total)}.${pendingNote} Bạn cũng có thể nhập trực tiếp mã đơn (ví dụ NM-261120-104) để kiểm tra cụ thể.`,
      products: [],
      orderRef: targetOrder,
    };
  }

  // Height / weight size parser
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

  // Fabric comparison
  if (
    lower.includes("cotton") ||
    lower.includes("dry") ||
    lower.includes("vải") ||
    lower.includes("gsm")
  ) {
    return {
      text: "Cotton Compact 220gsm (95% cotton chải kỹ, 5% spandex) đứng phom và mềm da cho mặc hằng ngày, còn Dry-Fit 145gsm (micro-mesh) thoát mồ hôi nhanh và khô trong khoảng 45 phút cho chạy bộ, tập gym.",
      products: PRODUCTS.filter((p) => ["prod-at-trang", "prod-at-dryfit"].includes(p.id)),
    };
  }

  // Budget / combo query
  if (
    lower.includes("combo") ||
    lower.includes("600") ||
    lower.includes("đi làm") ||
    lower.includes("rẻ")
  ) {
    return {
      text: "Gợi ý bộ đi làm gọn gàng dưới 600.000₫: Áo Polo Pique Cotton Kem nhạt (289.000₫) phối Quần Short Linen pha Cotton Xám đá (279.000₫), tổng 568.000₫ chưa tính mã giảm DANANG30 và NEXUS30.",
      products: PRODUCTS.filter((p) => ["prod-polo-kem", "prod-short-linen"].includes(p.id)),
    };
  }

  // Keyword match across real PRODUCTS
  const matched = PRODUCTS.filter((p) => {
    const hay = `${p.name} ${p.categoryName} ${p.fabric} ${p.description}`.toLowerCase();
    return lower
      .split(/\s+/)
      .filter((w) => w.length >= 2)
      .some((w) => hay.includes(w));
  }).slice(0, 2);

  if (matched.length > 0) {
    return {
      text: `Mình tìm thấy ${matched.length} sản phẩm phù hợp nhất với yêu cầu "${prompt}" trên NexusMart. Bạn có thể mở trang sản phẩm để xem chi tiết thông số vải và chọn size:`,
      products: matched,
    };
  }

  return {
    text: "Mình có thể tra cứu đơn hàng trong tài khoản của bạn, tư vấn size theo số đo cho cả 4 xưởng, giải thích chính sách đổi trả 7 ngày hoặc so sánh chất liệu vải.",
    products: PRODUCTS.filter((p) => ["prod-at-xamda", "prod-polo-xanhda"].includes(p.id)),
  };
}

export function AiAssistantPanel() {
  const { isAuthenticated } = useSession();
  const { aiOpen, closeAi, bodyProfile } = useMockStore();
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

  const runPrompt = (promptText: string) => {
    const clean = promptText.trim();
    if (!clean) return;
    stopStreaming();

    const answer = buildAiAnswer(clean, INITIAL_ORDERS, isAuthenticated, bodyProfile);
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
    runPrompt(question);
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
              Tra cứu đơn, tư vấn size 4 xưởng theo hồ sơ số đo và đổi trả 7 ngày
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
            <strong>Bản xem trước, dữ liệu mẫu:</strong> Trợ lý AI đang ở chế độ minh họa giao diện.
            Để xem đơn hàng thật, vui lòng mở mục Đơn hàng của tôi.
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
                  onClick={() => runPrompt(chip.prompt)}
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
                  onClick={() => runPrompt(item.prompt)}
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
                      Đơn mẫu <strong className="num">{turn.orderRef.orderCode}</strong> ·{" "}
                      <span className="num">{formatVnd(turn.orderRef.total)}</span>
                    </span>
                    <Link href="/don-hang" className={styles.orderLinkAction} onClick={closeAi}>
                      Mở đơn hàng của tôi
                    </Link>
                  </div>
                )}

                {turn.helpHref && (
                  <div className={styles.orderLinkBox}>
                    <span>Đi nhanh tới mục liên quan</span>
                    <Link href={turn.helpHref} className={styles.orderLinkAction} onClick={closeAi}>
                      {turn.helpHref === "/tro-giup"
                        ? "Trung tâm trợ giúp"
                        : turn.helpHref === "/tra-cuu-don-hang"
                          ? "Tra cứu đơn hàng"
                          : turn.helpHref === "/hoi-vien"
                            ? "Hội viên Nexus"
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
            placeholder="Nhập mã đơn (NM-...), chiều cao cân nặng..."
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
