"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { IconClose, IconSearch } from "@/components/Icons";
import { StateView } from "@/components/StateView";
import styles from "./page.module.css";

type HelpTopic = "all" | "mo-gian-hang" | "giao-dien" | "thanh-toan" | "mua-hang" | "bao-mat";

interface HelpFaq {
  id: string;
  anchor?: string;
  topic: Exclude<HelpTopic, "all">;
  question: string;
  answer: string;
}

const TABS: ReadonlyArray<{ id: HelpTopic; label: string }> = [
  { id: "all", label: "Tất cả" },
  { id: "mo-gian-hang", label: "Mở gian hàng" },
  { id: "giao-dien", label: "Giao diện" },
  { id: "thanh-toan", label: "Thanh toán" },
  { id: "mua-hang", label: "Mua hàng" },
  { id: "bao-mat", label: "Bảo mật" },
];

const HELP_FAQS: readonly HelpFaq[] = [
  {
    id: "seller-open",
    topic: "mo-gian-hang",
    question: "Làm thế nào để mở cửa hàng riêng trên NexusMart?",
    answer:
      "Đăng ký và xác thực email qua mã 6 chữ số trên trang gốc. Khi hồ sơ gian hàng được duyệt, bạn vào Kênh người bán để chọn tên miền phụ, đăng sản phẩm và xuất bản.",
  },
  {
    id: "seller-subdomain",
    topic: "mo-gian-hang",
    question: "Quy tắc đặt tên miền phụ cho gian hàng là gì?",
    answer:
      "Dùng chữ cái thường không dấu, chữ số và dấu gạch nối, tối thiểu 3 ký tự và không trùng tên hệ thống giữ riêng.",
  },
  {
    id: "seller-theme",
    topic: "giao-dien",
    question: "Có thể xem trước giao diện trước khi xuất bản không?",
    answer:
      "Có. Mọi thay đổi về màu sắc, ảnh bìa và các khối trang chủ đều lưu thành bản nháp để bạn xem trước toàn bộ trang trước khi công khai.",
  },
  {
    id: "seller-rollback",
    topic: "giao-dien",
    question: "Làm sao quay lại giao diện cũ sau khi đã xuất bản?",
    answer:
      "Hệ thống lưu lịch sử các phiên bản đã xuất bản của từng cửa hàng. Bạn có thể chọn bản trước đó trong Kênh người bán để khôi phục ngay.",
  },
  {
    id: "seller-settlement",
    anchor: "thanh-toan-doi-soat",
    topic: "thanh-toan",
    question: "Doanh thu COD và VietQR được đối soát như thế nào?",
    answer:
      "Mọi đơn hàng ghi nhận tập trung trong Kênh người bán bằng số nguyên VND trên sổ cái ghi kép. Giao dịch VietQR tự động cập nhật trạng thái xác nhận.",
  },
  {
    id: "buyer-payment",
    topic: "thanh-toan",
    question: "Các cửa hàng hỗ trợ những hình thức thanh toán nào?",
    answer:
      "Mỗi cửa hàng xuất bản trên NexusMart hỗ trợ thanh toán tiền mặt khi nhận hàng (COD) và chuyển khoản quét mã VietQR.",
  },
  {
    id: "buyer-where",
    topic: "mua-hang",
    question: "Tôi muốn tìm sản phẩm và đặt mua thì vào đâu?",
    answer:
      "Chọn thương hiệu trong Danh bạ gian hàng trên trang chủ để mở thẳng địa chỉ cửa hàng đó. Mọi danh mục, giỏ hàng và thanh toán nằm trên trang của shop.",
  },
  {
    id: "buyer-orders",
    topic: "mua-hang",
    question: "Vì sao đơn hàng ở cửa hàng A không hiện ở cửa hàng B?",
    answer:
      "Mỗi cửa hàng vận hành độc lập trên tên miền riêng. Giỏ hàng và lịch sử đơn chỉ hiển thị giao dịch thuộc đúng cửa hàng bạn đang truy cập.",
  },
  {
    id: "buyer-account",
    anchor: "bao-mat",
    topic: "bao-mat",
    question: "Vì sao cần đăng nhập riêng khi chuyển sang cửa hàng khác?",
    answer:
      "Phiên đăng nhập được gắn cô lập theo từng tên miền phụ để bảo vệ quyền riêng tư và chống lẫn phiên. Bạn vẫn dùng cùng một email và mật khẩu.",
  },
  {
    id: "seller-suspend",
    topic: "bao-mat",
    question: "Cửa hàng ở trạng thái nháp hoặc tạm khóa sẽ hiển thị ra sao?",
    answer:
      "Khi chưa xuất bản hoặc bị tạm khóa, tên miền phụ sẽ không phục vụ mua hàng và cửa hàng tự động ẩn khỏi danh bạ công khai.",
  },
];

function foldVi(text: string): string {
  return text
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .trim();
}

export default function PlatformHelpPage() {
  const [query, setQuery] = useState("");
  const [activeTab, setActiveTab] = useState<HelpTopic>("all");

  useEffect(() => {
    const openTargetFromHash = () => {
      const hash = window.location.hash.replace(/^#/, "");
      if (!hash) return;
      const target = document.getElementById(hash);
      const details = target?.closest("details");
      if (details) {
        details.open = true;
      }
    };
    openTargetFromHash();
    window.addEventListener("hashchange", openTargetFromHash);
    return () => window.removeEventListener("hashchange", openTargetFromHash);
  }, []);

  const filtered = useMemo(() => {
    const q = foldVi(query);
    return HELP_FAQS.filter((item) => {
      if (activeTab !== "all" && item.topic !== activeTab) return false;
      if (!q) return true;
      return foldVi(`${item.question} ${item.answer}`).includes(q);
    });
  }, [query, activeTab]);

  return (
    <div className={styles.page}>
      <section className={styles.topBand} aria-label="Tìm kiếm câu hỏi trợ giúp">
        <h1 className={`${styles.title} type-wide lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Trợ giúp
            </span>
          </span>
        </h1>

        <div className={styles.searchWrap} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          <IconSearch size={22} className={styles.searchIcon} />
          <label htmlFor="help-search-input" className="sr-only">
            Tìm câu hỏi trợ giúp
          </label>
          <input
            id="help-search-input"
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Tìm câu hỏi: tên miền phụ, VietQR, xuất bản, tách đơn..."
            className={styles.searchInput}
          />
          {query && (
            <button
              type="button"
              className={styles.clearBtn}
              onClick={() => setQuery("")}
              aria-label="Xóa từ khóa tìm kiếm"
            >
              <IconClose size={18} />
            </button>
          )}
        </div>

        <div className={styles.tabsRow} role="group" aria-label="Nhóm câu hỏi">
          {TABS.map((tab) => {
            const selected = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                aria-pressed={selected}
                className={`${styles.tabBtn} ${selected ? styles.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.id)}
              >
                {tab.label}
              </button>
            );
          })}
        </div>
      </section>

      <section className={styles.listBand} aria-label="Danh sách câu hỏi">
        {filtered.length === 0 ? (
          <StateView
            kind="empty"
            title="Không tìm thấy câu hỏi khớp từ khóa."
            actionLabel="Xem tất cả"
            onAction={() => {
              setQuery("");
              setActiveTab("all");
            }}
          />
        ) : (
          <div className={styles.accordionList} data-reveal="rows">
            {filtered.map((item) => (
              <details key={item.id} name="help-faq" className={styles.tile}>
                <summary className={styles.summary}>
                  <span className={`${styles.question} type-normal`}>{item.question}</span>
                  <span className={styles.plus} aria-hidden="true" />
                </summary>
                <div className={styles.answerWrap}>
                  <p id={item.anchor} className={styles.answer}>
                    {item.answer}
                  </p>
                </div>
              </details>
            ))}
          </div>
        )}
      </section>

      <section className={styles.contactStrip} aria-label="Liên hệ hỗ trợ">
        <div className={styles.contactMeta}>
          <a href="mailto:hotro@nexusmart.vn" className={styles.contactLink}>
            hotro@nexusmart.vn
          </a>
          <span className={styles.contactHours}>8:30 đến 22:00 hằng ngày</span>
        </div>
        <Link href="/dang-ky?role=seller" className={styles.contactCta}>
          Mở gian hàng
        </Link>
      </section>
    </div>
  );
}
