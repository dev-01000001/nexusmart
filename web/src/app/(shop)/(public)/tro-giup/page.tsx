"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Mail, Search, SearchX, X } from "lucide-react";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import {
  PLATFORM_BTN_PRIMARY,
  PLATFORM_CONTAINER,
  PLATFORM_LEAD,
  PLATFORM_OVERLAP_BAND,
  PLATFORM_PAGE_H1,
} from "@/features/platform/platformStyles";
import { cn } from "@/lib/utils";

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
  const [openId, setOpenId] = useState<string>("");

  useEffect(() => {
    const openTargetFromHash = () => {
      const hash = window.location.hash.replace(/^#/, "");
      if (!hash) return;
      const matched = HELP_FAQS.find((item) => item.anchor === hash || item.id === hash);
      if (matched) {
        setActiveTab("all");
        setQuery("");
        setOpenId(matched.id);
        window.requestAnimationFrame(() => {
          document.getElementById(hash)?.scrollIntoView({ behavior: "smooth", block: "start" });
        });
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
    <div className="w-full overflow-x-clip bg-[var(--home-bg)] text-[var(--home-ink)]">
      {/* Main FAQ band modeled on Homepage S11 Pine FAQ section */}
      <section
        className="relative pt-[calc(var(--home-header-h)+clamp(44px,5.5vw,84px))] pb-[calc(var(--home-space-section)+var(--home-radius-band))] [background:radial-gradient(ellipse_52%_42%_at_85%_14%,var(--home-glow-soft),transparent_64%),radial-gradient(ellipse_46%_36%_at_12%_76%,rgba(102,204,215,0.12),transparent_66%),var(--home-pine)] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)+var(--home-radius-band-sm))]"
        aria-labelledby="help-heading"
      >
        <div className={PLATFORM_CONTAINER}>
          <div className="grid grid-cols-1 items-start gap-10 min-[961px]:grid-cols-[minmax(0,4.3fr)_minmax(0,7.7fr)] min-[961px]:gap-[clamp(36px,5vw,76px)]">
            {/* Sticky Left Column: Title, lead, pill search & topic filter chips */}
            <div className="flex min-w-0 flex-col gap-6 min-[961px]:sticky min-[961px]:top-[calc(var(--home-header-h)+32px)]">
              <div className="flex flex-col gap-3">
                <h1 id="help-heading" className={PLATFORM_PAGE_H1}>
                  Trợ giúp
                </h1>
                <p className={`${PLATFORM_LEAD} max-w-[34ch]`}>
                  Câu hỏi thường gặp về mở gian hàng, giao diện, thanh toán COD / VietQR và bảo mật.
                </p>
              </div>

              <div className="relative flex min-h-[var(--home-btn-mid-h)] items-center rounded-full border border-[var(--home-border-strong)] bg-[var(--home-glass)] transition-[border-color,background-color] duration-200 focus-within:border-[var(--home-accent)] focus-within:bg-[var(--home-glass-strong)] motion-reduce:transition-none">
                <Search
                  className="pointer-events-none absolute top-1/2 left-4 size-4 -translate-y-1/2 text-[var(--home-ink-2)]"
                  aria-hidden="true"
                />
                <label htmlFor="help-search-input" className="sr-only">
                  Tìm câu hỏi trợ giúp
                </label>
                <input
                  id="help-search-input"
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Tìm câu hỏi: tên miền phụ, VietQR, xuất bản, tách đơn..."
                  className="h-[var(--home-btn-mid-h)] w-full min-w-0 rounded-full border-0 bg-transparent pr-11 pl-11 font-[family-name:var(--home-font)] text-[length:var(--home-fs-body)] text-[var(--home-ink)] placeholder:text-[var(--home-input-placeholder)] [&::-webkit-search-cancel-button]:appearance-none"
                />
                {query ? (
                  <button
                    type="button"
                    onClick={() => setQuery("")}
                    aria-label="Xóa từ khóa tìm kiếm"
                    className="absolute top-1/2 right-1 inline-flex size-10 -translate-y-1/2 items-center justify-center rounded-full text-[var(--home-ink-2)] transition-colors hover:bg-[var(--home-glass-strong)] hover:text-[var(--home-ink)] motion-reduce:transition-none"
                  >
                    <X className="size-4" aria-hidden="true" />
                  </button>
                ) : null}
              </div>

              <div className="flex flex-wrap gap-2" role="group" aria-label="Nhóm câu hỏi">
                {TABS.map((tab) => {
                  const selected = activeTab === tab.id;
                  return (
                    <button
                      key={tab.id}
                      type="button"
                      aria-pressed={selected}
                      onClick={() => setActiveTab(tab.id)}
                      className={cn(
                        "inline-flex min-h-10 items-center justify-center rounded-full px-4 text-[length:var(--home-fs-small)] transition-colors duration-200 motion-reduce:transition-none",
                        selected
                          ? "bg-[var(--home-ink)] font-[550] text-[var(--home-bg)]"
                          : "border border-[var(--home-border)] bg-[var(--home-glass)] font-[450] text-[var(--home-ink-2)] hover:border-[var(--home-border-strong)] hover:text-[var(--home-ink)]"
                      )}
                    >
                      {tab.label}
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Right Column: Hairline Accordion matching Homepage S11 FAQ */}
            <section aria-label="Danh sách câu hỏi" className="min-w-0">
              {filtered.length === 0 ? (
                <div className="flex flex-col items-center gap-4 rounded-[var(--home-radius-tile)] border border-[var(--home-border)] bg-[var(--home-card-dark)] px-6 py-12 text-center">
                  <span className="inline-flex size-12 items-center justify-center rounded-full border border-[var(--home-border)] bg-[var(--home-glass)] text-[var(--home-accent)]">
                    <SearchX className="size-5" aria-hidden="true" />
                  </span>
                  <div className="space-y-1.5">
                    <p className="text-[length:var(--home-fs-h3)] font-[450] text-[var(--home-ink)]">
                      Không tìm thấy câu hỏi khớp từ khóa.
                    </p>
                    <p className="max-w-[42ch] text-[length:var(--home-fs-small)] leading-[1.55] text-[var(--home-ink-2)]">
                      Thử tìm với từ khóa khác hoặc xem lại toàn bộ danh sách câu hỏi.
                    </p>
                  </div>
                  <div className="pt-2">
                    <button
                      type="button"
                      onClick={() => {
                        setQuery("");
                        setActiveTab("all");
                      }}
                      className={PLATFORM_BTN_PRIMARY}
                    >
                      Xem tất cả
                    </button>
                  </div>
                </div>
              ) : (
                <Accordion
                  type="single"
                  collapsible
                  value={openId}
                  onValueChange={setOpenId}
                  className="w-full border-t border-[var(--home-border)]"
                >
                  {filtered.map((item) => (
                    <AccordionItem
                      key={item.id}
                      value={item.id}
                      id={item.anchor}
                      className="scroll-mt-24 border-b border-[var(--home-border)] last:border-b"
                    >
                      <AccordionTrigger className="group/faq flex min-h-[72px] w-full items-center justify-between gap-5 rounded-none py-5 text-left text-[length:var(--home-fs-h3)] leading-[1.35] font-[450] text-[var(--home-ink)] transition-colors hover:text-[var(--home-accent)] hover:no-underline focus-visible:ring-0 motion-reduce:transition-none [&>svg:last-child]:hidden">
                        <span>{item.question}</span>
                        <span
                          className="relative h-4 w-4 flex-none text-[var(--home-accent)] before:absolute before:top-1/2 before:left-0 before:h-[1.5px] before:w-4 before:-translate-y-1/2 before:bg-current before:transition-transform before:duration-[280ms] before:ease-[cubic-bezier(0.16,1,0.3,1)] before:content-[''] after:absolute after:top-1/2 after:left-0 after:h-[1.5px] after:w-4 after:-translate-y-1/2 after:rotate-90 after:bg-current after:transition-transform after:duration-[280ms] after:ease-[cubic-bezier(0.16,1,0.3,1)] after:content-[''] group-data-[state=open]/faq:after:rotate-0 motion-reduce:before:transition-none motion-reduce:after:transition-none"
                          aria-hidden="true"
                        />
                      </AccordionTrigger>
                      <AccordionContent className="max-w-[60ch] pr-8 pb-6 text-[length:var(--home-fs-body)] leading-[1.6] text-[var(--home-ink-2)] [@media(max-width:640px)]:pr-0">
                        {item.answer}
                      </AccordionContent>
                    </AccordionItem>
                  ))}
                </Accordion>
              )}
            </section>
          </div>
        </div>
      </section>

      {/* Closing support band overlapping pine FAQ section */}
      <section
        className={`${PLATFORM_OVERLAP_BAND} z-[2] py-[clamp(48px,6.5vw,84px)] [background:linear-gradient(180deg,rgba(2,8,8,0.45)_0%,transparent_24%,rgba(2,8,8,0.72)_100%),radial-gradient(ellipse_50%_70%_at_86%_80%,rgba(102,204,215,0.18),transparent_64%),var(--home-steps-bg)]`}
        aria-label="Liên hệ hỗ trợ"
      >
        <div className={PLATFORM_CONTAINER}>
          <div className="flex flex-wrap items-center justify-between gap-6 rounded-[var(--home-radius-tile)] border border-[var(--home-border)] bg-[var(--home-card-dark)] p-6 shadow-[var(--home-shadow-card)] sm:p-8">
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
              <a
                href="mailto:hotro@nexusmart.vn"
                className="inline-flex min-h-10 items-center gap-2.5 text-[length:var(--home-fs-lead)] font-medium text-[var(--home-ink)] transition-colors hover:text-[var(--home-accent)] hover:underline motion-reduce:transition-none"
              >
                <Mail className="size-4 shrink-0 text-[var(--home-accent)]" aria-hidden="true" />
                hotro@nexusmart.vn
              </a>
              <span className="text-[length:var(--home-fs-small)] text-[var(--home-ink-2)]">
                8:30 đến 22:00 hằng ngày
              </span>
            </div>

            <Link
              href="/dang-ky?role=seller"
              className={`${PLATFORM_BTN_PRIMARY} [@media(max-width:640px)]:w-full`}
            >
              Mở gian hàng
            </Link>
          </div>
        </div>
      </section>
    </div>
  );
}
