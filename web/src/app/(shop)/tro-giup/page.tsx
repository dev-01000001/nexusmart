"use client";

import React, { useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { Button } from "@/components/Button";
import { StateView } from "@/components/StateView";
import { FABRIC_MEDIA, MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { PlatformMotion } from "@/features/platform/PlatformMotion";
import styles from "./page.module.css";

type HelpAudience = "all" | "seller" | "buyer";
type HelpTopic = "mo-gian-hang" | "giao-dien" | "thanh-toan" | "mua-hang" | "bao-mat";

interface PlatformFaq {
  id: string;
  audience: "seller" | "buyer";
  topic: HelpTopic;
  question: string;
  answer: string;
}

const TOPIC_LABELS: Record<HelpTopic, string> = {
  "mo-gian-hang": "Mở gian hàng & tên miền",
  "giao-dien": "Giao diện & xuất bản",
  "thanh-toan": "Thanh toán & đối soát",
  "mua-hang": "Mua hàng trên trang shop",
  "bao-mat": "Bảo mật & tài khoản",
};

const PLATFORM_FAQS: readonly PlatformFaq[] = [
  {
    id: "seller-open",
    audience: "seller",
    topic: "mo-gian-hang",
    question: "Làm thế nào để mở cửa hàng riêng trên NexusMart?",
    answer:
      "Bạn bắt đầu bằng việc đăng ký và xác thực tài khoản qua email trên trang gốc NexusMart. Sau khi hồ sơ gian hàng được duyệt, bạn truy cập Kênh người bán để chọn tên miền phụ, đăng sản phẩm và xuất bản cửa hàng.",
  },
  {
    id: "seller-subdomain",
    audience: "seller",
    topic: "mo-gian-hang",
    question: "Quy tắc đặt tên miền phụ cho gian hàng là gì?",
    answer:
      "Tên miền phụ dùng chữ cái thường không dấu, chữ số và dấu gạch nối, không trùng với các tên hệ thống giữ riêng. Khi cửa hàng được xuất bản, địa chỉ đó sẽ hoạt động ngay và xuất hiện trong danh bạ gian hàng.",
  },
  {
    id: "seller-theme",
    audience: "seller",
    topic: "giao-dien",
    question: "Tôi có thể xem trước giao diện trước khi công khai không?",
    answer:
      "Có. Trong mục Quản lý giao diện của Kênh người bán, mọi thay đổi về màu sắc, phông chữ, biểu trưng và các khối trang chủ đều lưu thành bản nháp. Bạn có thể xem trước toàn bộ trang trước khi bấm Xuất bản.",
  },
  {
    id: "seller-rollback",
    audience: "seller",
    topic: "giao-dien",
    question: "Nếu muốn quay lại giao diện cũ sau khi xuất bản thì làm thế nào?",
    answer:
      "NexusMart lưu lại lịch sử các phiên bản đã xuất bản của từng cửa hàng. Bạn có thể chọn một phiên bản trước đó trong Kênh người bán và khôi phục ngay mà không mất dữ liệu sản phẩm hay đơn hàng.",
  },
  {
    id: "seller-settlement",
    audience: "seller",
    topic: "thanh-toan",
    question: "Doanh thu COD và VietQR được đối soát như thế nào?",
    answer:
      "Các đơn hàng phát sinh trên trang cửa hàng của bạn được ghi nhận tập trung trong Kênh người bán bằng số nguyên VND. Trạng thái thanh toán VietQR tự động cập nhật khi giao dịch hoàn tất và hạch toán vào sổ cái đối soát.",
  },
  {
    id: "seller-suspend",
    audience: "seller",
    topic: "bao-mat",
    question: "Điều gì xảy ra nếu cửa hàng đang ở trạng thái nháp hoặc bị tạm khóa?",
    answer:
      "Khi trang cửa hàng chưa xuất bản hoặc đang bị quản trị viên tạm khóa, khách ghé thăm truy cập tên miền phụ sẽ không xem được trang hay đặt hàng, và cửa hàng cũng tự động ẩn khỏi danh bạ công khai.",
  },
  {
    id: "buyer-where",
    audience: "buyer",
    topic: "mua-hang",
    question: "Tôi muốn tìm sản phẩm và đặt hàng thì vào đâu?",
    answer:
      "Trang gốc NexusMart không bán hàng trực tiếp. Bạn hãy chọn thương hiệu trong Danh bạ gian hàng để mở trang cửa hàng riêng của họ, nơi có đầy đủ danh mục, tìm kiếm, mã ưu đãi gian hàng, giỏ hàng và thanh toán.",
  },
  {
    id: "buyer-orders",
    audience: "buyer",
    topic: "mua-hang",
    question: "Vì sao đơn hàng đặt ở cửa hàng A không hiện tại cửa hàng B?",
    answer:
      "Mỗi cửa hàng trên NexusMart hoạt động như một trang độc lập. Giỏ hàng, lịch sử đơn hàng và tra cứu đơn chỉ hiển thị các giao dịch phát sinh tại đúng cửa hàng bạn đang truy cập.",
  },
  {
    id: "buyer-payment",
    audience: "buyer",
    topic: "thanh-toan",
    question: "Các cửa hàng trên NexusMart hỗ trợ hình thức thanh toán nào?",
    answer:
      "Người mua có thể chọn thanh toán tiền mặt khi nhận hàng (COD) hoặc chuyển khoản quét mã VietQR ngay trên bước thanh toán của từng cửa hàng, với trạng thái xác nhận cập nhật tự động.",
  },
  {
    id: "buyer-account",
    audience: "buyer",
    topic: "bao-mat",
    question: "Vì sao tôi cần đăng nhập lại khi chuyển sang một cửa hàng khác?",
    answer:
      "Để giữ an toàn cho phiên đăng nhập và tách biệt dữ liệu giữa các cửa hàng, trình duyệt chỉ lưu phiên riêng cho từng tên miền phụ. Bạn vẫn dùng được cùng một email và mật khẩu đã đăng ký.",
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
  const [audience, setAudience] = useState<HelpAudience>("all");
  const [topic, setTopic] = useState<"all" | HelpTopic>("all");

  const filteredFaqs = useMemo(() => {
    const q = foldVi(query);
    return PLATFORM_FAQS.filter((item) => {
      if (audience !== "all" && item.audience !== audience) return false;
      if (topic !== "all" && item.topic !== topic) return false;
      if (!q) return true;
      const hay = foldVi(`${item.question} ${item.answer} ${TOPIC_LABELS[item.topic]}`);
      return hay.includes(q);
    });
  }, [query, audience, topic]);

  const sellerFaqs = useMemo(
    () => filteredFaqs.filter((item) => item.audience === "seller"),
    [filteredFaqs]
  );
  const buyerFaqs = useMemo(
    () => filteredFaqs.filter((item) => item.audience === "buyer"),
    [filteredFaqs]
  );

  return (
    <div className={styles.page}>
      <PlatformMotion depKey={`${audience}-${topic}`} />

      <section className={styles.pageHeader}>
        <div className={styles.headerLeadGroup}>
          <h1 className={`${styles.pageTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Trung tâm trợ giúp
              </span>
            </span>
          </h1>
          <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Hướng dẫn mở gian hàng, xuất bản tên miền phụ, đối soát thanh toán và cách mua hàng trên
            từng cửa hàng độc lập thuộc nền tảng NexusMart.
          </p>
        </div>

        <div
          className={styles.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <Button variant="secondary" size="md" href="/#danh-ba-gian-hang">
            Danh bạ gian hàng
          </Button>
          <Button variant="primary" size="md" href="/dang-ky?role=seller">
            Mở gian hàng
          </Button>
        </div>
      </section>

      <section className={styles.section} aria-label="Hướng dẫn nhanh theo vai trò">
        <div className={styles.commitmentSplit}>
          <div
            className={styles.commitmentPanel}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.commitmentContent}>
              <h2 className={styles.commitmentTitle}>Hỗ trợ đúng nơi bạn cần làm việc</h2>
              <ul className={styles.commitmentList}>
                <li>
                  <strong>Người bán:</strong> đăng nhập trên trang gốc NexusMart để vào Kênh người
                  bán, chỉnh giao diện tên miền phụ, quản lý sản phẩm, đơn hàng và đối soát.
                </li>
                <li>
                  <strong>Khách mua hàng:</strong> truy cập trực tiếp địa chỉ của cửa hàng bạn đã
                  đặt mua để xem giỏ hàng, thanh toán VietQR, theo dõi hoặc hủy đơn.
                </li>
                <li>
                  <strong>Liên hệ đội ngũ nền tảng:</strong> gửi thư về hotro@nexusmart.vn khi cần
                  hỗ trợ duyệt hồ sơ gian hàng hoặc xử lý sự cố tài khoản.
                </li>
              </ul>
            </div>

            <div className={styles.commitmentBadges}>
              <span className={styles.commitmentChip}>Email: hotro@nexusmart.vn</span>
              <span className={styles.commitmentChip}>Tiếp nhận: 8:30 - 22:00 hằng ngày</span>
            </div>
          </div>

          <div
            className={styles.photoQuad}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.photoFrame}>
              <Image
                src={WORKSHOP_MEDIA.src}
                alt={WORKSHOP_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1024px) 50vw, 22vw"
                className={styles.atmospherePhoto}
              />
            </div>
            <div className={styles.photoFrame}>
              <Image
                src={FABRIC_MEDIA.src}
                alt={FABRIC_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1024px) 50vw, 22vw"
                className={styles.atmospherePhoto}
              />
            </div>
            <div className={styles.photoFrame}>
              <Image
                src={MEDIA_BANNERS.everyday.src}
                alt="Không gian vận hành cửa hàng trực tuyến"
                fill
                priority
                sizes="(max-width: 1024px) 50vw, 22vw"
                className={styles.atmospherePhoto}
              />
            </div>
            <div className={styles.photoFrame}>
              <Image
                src={MEDIA_BANNERS.office.src}
                alt="Trang cửa hàng mang nhận diện thương hiệu riêng"
                fill
                priority
                sizes="(max-width: 1024px) 50vw, 22vw"
                className={styles.atmospherePhoto}
              />
            </div>
          </div>
        </div>
      </section>

      <section className={styles.sectionGap} aria-labelledby="help-faq-heading">
        <div className={styles.sectionHead} data-reveal="">
          <h2 id="help-faq-heading" className={styles.sectionTitle}>
            Câu hỏi thường gặp về nền tảng
          </h2>
          <span className={`${styles.sectionNote} num`}>
            {filteredFaqs.length} / {PLATFORM_FAQS.length} câu hỏi
          </span>
        </div>

        <div className={styles.faqControlBar} data-reveal="">
          <div className={styles.faqSearchRow}>
            <input
              type="search"
              className={styles.input}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm nhanh: tên miền phụ, xuất bản, VietQR, đối soát, tách đơn..."
              aria-label="Tìm kiếm câu hỏi thường gặp"
            />
            <div className={styles.modeSwitch} role="group" aria-label="Lọc theo đối tượng">
              {(
                [
                  { id: "all", label: "Tất cả" },
                  { id: "seller", label: "Người bán" },
                  { id: "buyer", label: "Khách mua hàng" },
                ] as const
              ).map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={`${styles.modeBtn} ${audience === item.id ? styles.modeBtnActive : ""}`}
                  onClick={() => setAudience(item.id)}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </div>

          <div className={styles.topicBar} role="group" aria-label="Lọc theo chủ đề">
            <button
              type="button"
              className={`${styles.topicChip} ${topic === "all" ? styles.topicChipActive : ""}`}
              onClick={() => setTopic("all")}
            >
              Tất cả chủ đề
            </button>
            {(Object.keys(TOPIC_LABELS) as HelpTopic[]).map((key) => (
              <button
                key={key}
                type="button"
                className={`${styles.topicChip} ${topic === key ? styles.topicChipActive : ""}`}
                onClick={() => setTopic(key)}
              >
                {TOPIC_LABELS[key]}
              </button>
            ))}
          </div>
        </div>

        {filteredFaqs.length === 0 ? (
          <StateView
            kind="empty"
            title="Không tìm thấy câu hỏi khớp từ khóa"
            message="Bạn hãy xóa bộ lọc tìm kiếm hoặc liên hệ trực tiếp qua email hotro@nexusmart.vn."
            actionLabel="Xem toàn bộ câu hỏi"
            onAction={() => {
              setQuery("");
              setAudience("all");
              setTopic("all");
            }}
          />
        ) : (
          <div className={styles.faqTwoCol}>
            {(audience === "all" || audience === "seller") && (
              <div className={styles.faqScopeCol}>
                <div className={styles.scopeHead}>
                  <h3 className={styles.scopeTitle}>Dành cho người bán</h3>
                  <span className={`${styles.countBadge} num`}>{sellerFaqs.length} mục</span>
                </div>
                {sellerFaqs.length === 0 ? (
                  <p className={styles.faqMeta}>Không có câu hỏi cho người bán trong chủ đề này.</p>
                ) : (
                  <div className={styles.faqReadingList}>
                    {sellerFaqs.map((item) => (
                      <article key={item.id} className={styles.faqEntry} data-reveal="">
                        <span className={styles.faqMeta}>{TOPIC_LABELS[item.topic]}</span>
                        <p className={styles.faqQ}>{item.question}</p>
                        <p className={styles.faqA}>{item.answer}</p>
                      </article>
                    ))}
                  </div>
                )}
              </div>
            )}

            {(audience === "all" || audience === "buyer") && (
              <div className={styles.faqScopeCol}>
                <div className={styles.scopeHead}>
                  <h3 className={styles.scopeTitle}>Dành cho khách mua hàng</h3>
                  <span className={`${styles.countBadgePos} num`}>{buyerFaqs.length} mục</span>
                </div>
                {buyerFaqs.length === 0 ? (
                  <p className={styles.faqMeta}>
                    Không có câu hỏi cho khách mua hàng trong chủ đề này.
                  </p>
                ) : (
                  <div className={styles.faqReadingList}>
                    {buyerFaqs.map((item) => (
                      <article key={item.id} className={styles.faqEntry} data-reveal="">
                        <span className={styles.faqMeta}>{TOPIC_LABELS[item.topic]}</span>
                        <p className={styles.faqQ}>{item.question}</p>
                        <p className={styles.faqA}>{item.answer}</p>
                      </article>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </section>

      <section className={styles.sectionGap} aria-labelledby="help-policies-heading">
        <div className={styles.sectionHead} data-reveal="">
          <h2 id="help-policies-heading" className={styles.sectionTitle}>
            Nguyên tắc thanh toán, đối soát và bảo mật
          </h2>
        </div>

        <div className={styles.policyGrid}>
          <article id="thanh-toan-doi-soat" className={styles.policyCard} data-reveal="">
            <h3 className={styles.panelTitle}>Thanh toán và đối soát minh bạch</h3>
            <p className={styles.faqA}>
              Mỗi cửa hàng xuất bản trên NexusMart hỗ trợ thanh toán tiền mặt khi nhận hàng (COD) và
              chuyển khoản quét mã VietQR. Giao dịch được ghi nhận nguyên VND trên sổ cái ghi kép,
              chống lặp đơn và kiểm tra tính toàn vẹn của mọi thông báo xác nhận thanh toán.
            </p>
            <Link href="/dang-nhap" className={styles.inlineLink}>
              Vào Kênh người bán để xem số dư và đối soát
            </Link>
          </article>

          <article id="bao-mat" className={styles.policyCard} data-reveal="">
            <h3 className={styles.panelTitle}>Bảo mật và tách biệt dữ liệu theo tên miền</h3>
            <p className={styles.faqA}>
              Phiên đăng nhập được gắn riêng theo từng tên miền phụ và không chia sẻ chéo giữa các
              cửa hàng. Mọi yêu cầu tạo giỏ hàng, đặt hàng hay xem chi tiết đơn đều được máy chủ xác
              thực theo đúng cửa hàng đang phục vụ và quyền sở hữu của người dùng.
            </p>
            <Link href="/ve-nexusmart" className={styles.inlineLink}>
              Đọc thêm về kiến trúc nền tảng NexusMart
            </Link>
          </article>
        </div>
      </section>
    </div>
  );
}
