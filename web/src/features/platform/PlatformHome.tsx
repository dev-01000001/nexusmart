import React from "react";
import type { DirectoryResult } from "./directory.server";
import { HeroLiveDoor } from "./HeroLiveDoor";
import { PlatformClosingCta } from "./PlatformClosingCta";
import { PlatformLoomSection } from "./PlatformLoomSection";
import { PlatformPinnedStory } from "./PlatformPinnedStory";
import { SiteDirectorySection } from "./SiteDirectorySection";
import styles from "./PlatformHome.module.css";

export interface PlatformHomeProps {
  directory: DirectoryResult;
  rootDomain: string;
}

const PLATFORM_FAQS = [
  {
    id: "diff",
    question: "NexusMart khác gì sàn thương mại điện tử?",
    answer:
      "Mỗi thương hiệu có địa chỉ riêng, giao diện riêng và tệp khách riêng, không chung kệ hay bị gợi ý sang đối thủ.",
  },
  {
    id: "buyer",
    question: "Khách mua hàng và xem đơn ở đâu?",
    answer:
      "Khách vào thẳng địa chỉ từng cửa hàng từ danh bạ. Giỏ hàng, thanh toán và tra cứu đơn tách riêng theo từng shop.",
  },
  {
    id: "theme",
    question: "Có đổi giao diện sau khi xuất bản không?",
    answer:
      "Có. Bạn chỉnh bản nháp, xem trước rồi xuất bản mới, và khôi phục bản cũ bất cứ lúc nào.",
  },
  {
    id: "payment",
    question: "Cửa hàng hỗ trợ thanh toán nào?",
    answer:
      "Mỗi cửa hàng tích hợp sẵn thanh toán khi nhận hàng (COD) và chuyển khoản VietQR xác nhận trạng thái tự động.",
  },
  {
    id: "directory",
    question: "Khi nào cửa hàng hiện trong danh bạ?",
    answer:
      "Ngay khi hồ sơ được duyệt và bạn bấm Xuất bản trong Kênh người bán. Bản nháp hoặc shop tạm khóa sẽ tự ẩn.",
  },
] as const;

export function PlatformHome({ directory, rootDomain }: PlatformHomeProps) {
  return (
    <div className={styles.page}>
      {/* 1. Hero = The Door + Live Storefront */}
      <HeroLiveDoor rootDomain={rootDomain} />

      {/* 2. Real Published Directory = Row of Expanding Doors */}
      <SiteDirectorySection directory={directory} />

      {/* 3. Pinned Story (3 steps, live UI fragment) */}
      <PlatformPinnedStory />

      {/* 4. Jacquard Loom Film expanding via CSS view() clip-path */}
      <PlatformLoomSection />

      {/* 5. FAQ: sticky left heading + 5 color-filled <details name> tiles */}
      <section className={styles.faqBand} aria-labelledby="platform-faq-heading">
        <div className={styles.faqGrid}>
          <div className={styles.faqLeadCol}>
            <h2
              id="platform-faq-heading"
              className={`${styles.faqHeading} type-wide`}
              data-reveal=""
            >
              Câu hỏi thường gặp
            </h2>
          </div>

          <div className={styles.faqList} data-reveal="rows">
            {PLATFORM_FAQS.map((item) => (
              <details key={item.id} name="platform-faq" className={styles.faqTile}>
                <summary className={styles.faqSummary}>
                  <span className={`${styles.faqQuestion} type-normal`}>{item.question}</span>
                  <span className={styles.faqPlus} aria-hidden="true" />
                </summary>
                <div className={styles.faqAnswerWrap}>
                  <p className={styles.faqAnswer}>{item.answer}</p>
                </div>
              </details>
            ))}
          </div>
        </div>
      </section>

      {/* 6. Fill-in-the-blank CTA (followed by PlatformFooter in layout) */}
      <PlatformClosingCta rootDomain={rootDomain} />
    </div>
  );
}
