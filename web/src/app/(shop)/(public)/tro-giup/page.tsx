"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Mail, Search, SearchX, X } from "lucide-react";
import { EmptyState } from "@/components/app";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { UiScope } from "@/components/ui/ui-scope";

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
    <UiScope className="w-full bg-background text-foreground">
      <div className="mx-auto flex w-full max-w-[720px] flex-col gap-6 px-4 pt-24 pb-16 sm:px-6 md:pt-28 md:pb-24">
        <section className="space-y-5" aria-labelledby="help-heading">
          <div className="space-y-1.5">
            <h1
              id="help-heading"
              className="text-2xl leading-8 font-semibold tracking-tight text-foreground sm:text-3xl sm:leading-9"
            >
              Trợ giúp
            </h1>
            <p className="text-sm leading-5 text-muted-foreground">
              Câu hỏi thường gặp về mở gian hàng, giao diện, thanh toán COD / VietQR và bảo mật.
            </p>
          </div>

          <div className="relative">
            <Search
              className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <label htmlFor="help-search-input" className="sr-only">
              Tìm câu hỏi trợ giúp
            </label>
            <Input
              id="help-search-input"
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm câu hỏi: tên miền phụ, VietQR, xuất bản, tách đơn..."
              className="h-11 bg-card pr-11 pl-10 text-sm shadow-card [&::-webkit-search-cancel-button]:appearance-none"
            />
            {query ? (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => setQuery("")}
                aria-label="Xóa từ khóa tìm kiếm"
                className="absolute top-1/2 right-0.5 size-10 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            ) : null}
          </div>

          <div className="flex flex-wrap gap-2" role="group" aria-label="Nhóm câu hỏi">
            {TABS.map((tab) => {
              const selected = activeTab === tab.id;
              return (
                <Button
                  key={tab.id}
                  type="button"
                  variant={selected ? "default" : "secondary"}
                  size="sm"
                  aria-pressed={selected}
                  className="min-h-10 rounded-full px-4"
                  onClick={() => setActiveTab(tab.id)}
                >
                  {tab.label}
                </Button>
              );
            })}
          </div>
        </section>

        <section aria-label="Danh sách câu hỏi">
          {filtered.length === 0 ? (
            <EmptyState
              icon={SearchX}
              title="Không tìm thấy câu hỏi khớp từ khóa."
              description="Thử tìm với từ khóa khác hoặc xem lại toàn bộ danh sách câu hỏi."
              action={
                <Button
                  type="button"
                  onClick={() => {
                    setQuery("");
                    setActiveTab("all");
                  }}
                >
                  Xem tất cả
                </Button>
              }
            />
          ) : (
            <div className="rounded-xl bg-card px-5 py-1 shadow-card sm:px-6">
              <Accordion
                type="single"
                collapsible
                value={openId}
                onValueChange={setOpenId}
                className="w-full"
              >
                {filtered.map((item) => (
                  <AccordionItem
                    key={item.id}
                    value={item.id}
                    id={item.anchor}
                    className="scroll-mt-24 border-border"
                  >
                    <AccordionTrigger className="min-h-12 py-4 text-left text-[15px] leading-6 font-semibold text-foreground hover:text-brand hover:no-underline">
                      {item.question}
                    </AccordionTrigger>
                    <AccordionContent className="pb-4 text-sm leading-6 text-muted-foreground">
                      {item.answer}
                    </AccordionContent>
                  </AccordionItem>
                ))}
              </Accordion>
            </div>
          )}
        </section>

        <section
          className="flex flex-wrap items-center justify-between gap-4 rounded-xl bg-card p-5 shadow-card sm:p-6"
          aria-label="Liên hệ hỗ trợ"
        >
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <a
              href="mailto:hotro@nexusmart.vn"
              className="inline-flex min-h-10 items-center gap-2 text-sm font-semibold text-foreground transition-colors hover:text-brand hover:underline"
            >
              <Mail className="size-4 text-brand" aria-hidden="true" />
              hotro@nexusmart.vn
            </a>
            <span className="text-xs text-muted-foreground">8:30 đến 22:00 hằng ngày</span>
          </div>
          <Button size="lg" asChild>
            <Link href="/dang-ky?role=seller">Mở gian hàng</Link>
          </Button>
        </section>
      </div>
    </UiScope>
  );
}
