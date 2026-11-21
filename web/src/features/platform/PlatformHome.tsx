import React from "react";
import Image from "next/image";
import Link from "next/link";
import { Button } from "@/components/Button";
import { IconArrowUpRight } from "@/components/Icons";
import { FABRIC_MEDIA, MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import type { DirectoryResult } from "./directory.server";
import { AmbientLoomVideo, PlatformMotion } from "./PlatformMotion";
import { SiteDirectorySection } from "./SiteDirectorySection";
import { SubdomainPreview } from "./SubdomainPreview";
import styles from "./PlatformHome.module.css";

export interface PlatformHomeProps {
  directory: DirectoryResult;
  rootDomain: string;
}

const SELLER_CAPABILITIES = [
  {
    title: "Giao diện mang dấu ấn thương hiệu",
    detail:
      "Chọn bảng màu, phông chữ, biểu trưng và sắp xếp từng khối nội dung theo câu chuyện của gian hàng. Xem trước bản nháp trước khi xuất bản và hoàn tác về phiên bản cũ bất cứ lúc nào.",
  },
  {
    title: "Cửa hàng trọn vẹn trên địa chỉ riêng",
    detail:
      "Mỗi gian hàng có trang chủ, danh sách sản phẩm, tìm kiếm nội bộ, khung Flash sale, mã giảm giá riêng của shop, giỏ hàng và trang theo dõi đơn cho khách hàng của mình.",
  },
  {
    title: "Thanh toán COD, VietQR và giao nhận toàn quốc",
    detail:
      "Tích hợp sẵn thanh toán khi nhận hàng và chuyển khoản VietQR xác nhận tự động. Tự tính cước giao vận cho 34 tỉnh thành theo địa chỉ hai cấp và trọng lượng đơn.",
  },
  {
    title: "Quản trị tồn kho và sổ cái đối soát minh bạch",
    detail:
      "Theo dõi tồn kho chính xác theo từng màu và cỡ, xác nhận và bàn giao vận chuyển ngay tại Kênh người bán. Doanh thu và tiền chờ đối soát được ghi nhận nguyên VND.",
  },
];

const LAUNCH_STAGES = [
  {
    stage: "Tạo tài khoản người bán",
    detail:
      "Đăng ký bằng email và xác thực nhanh qua mã 6 chữ số. Tài khoản của bạn là chìa khóa vào Kênh người bán trên trang gốc NexusMart.",
  },
  {
    stage: "Hoàn thiện hồ sơ và danh mục",
    detail:
      "Gửi thông tin gian hàng để quản trị viên duyệt hoạt động, sau đó đăng sản phẩm, khai báo biến thể màu sắc, kích cỡ và số lượng tồn kho.",
  },
  {
    stage: "Thiết lập tên miền phụ và giao diện",
    detail:
      "Chọn tên miền phụ riêng, thiết lập màu nền, màu nhấn, ảnh bìa và các khối trang chủ trong trình chỉnh sửa trực quan có bản xem trước.",
  },
  {
    stage: "Xuất bản và đón đơn hàng đầu tiên",
    detail:
      "Bấm xuất bản để cửa hàng lên sóng ngay trên tên miền phụ của bạn và tự động góp mặt trong danh bạ gian hàng của NexusMart.",
  },
];

const PLATFORM_FAQS = [
  {
    question: "NexusMart khác gì với sàn thương mại điện tử truyền thống?",
    answer:
      "NexusMart không gộp hàng hóa của nhiều gian hàng vào chung một kệ hay một giỏ hàng. Mỗi người bán sở hữu một trang cửa hàng độc lập trên tên miền phụ riêng, tự làm chủ thương hiệu, nội dung và khách hàng của mình.",
  },
  {
    question: "Khách mua hàng tạo giỏ hàng và xem đơn ở đâu?",
    answer:
      "Khách hàng chọn cửa hàng mong muốn từ danh bạ gian hàng để truy cập thẳng địa chỉ riêng của cửa hàng đó. Việc đăng nhập, giỏ hàng, thanh toán và tra cứu đơn đều diễn ra ngay trên trang của gian hàng.",
  },
  {
    question: "Một tài khoản có mua được ở nhiều cửa hàng khác nhau không?",
    answer:
      "Có. Bạn có thể dùng cùng một địa chỉ email đã đăng ký, nhưng phiên đăng nhập và đơn hàng được cô lập riêng theo từng tên miền cửa hàng để bảo vệ quyền riêng tư và tránh lẫn đơn giữa các shop.",
  },
  {
    question: "Người bán có thể thay đổi giao diện sau khi đã xuất bản không?",
    answer:
      "Hoàn toàn được. Bạn có thể chỉnh sửa bản nháp, xem trước toàn bộ trang chủ rồi xuất bản phiên bản mới. Hệ thống lưu lại lịch sử các lần xuất bản để bạn khôi phục nhanh khi cần.",
  },
  {
    question: "Cửa hàng hỗ trợ những phương thức thanh toán nào?",
    answer:
      "Mỗi cửa hàng xuất bản trên NexusMart đều hỗ trợ sẵn thanh toán tiền mặt khi nhận hàng (COD) và chuyển khoản quét mã VietQR có đối soát trạng thái tự động.",
  },
  {
    question: "Khi nào gian hàng của tôi xuất hiện trong danh bạ trên trang chủ?",
    answer:
      "Ngay khi gian hàng đã được duyệt hoạt động và bạn bấm Xuất bản trang cửa hàng trong Kênh người bán. Nếu trang ở trạng thái nháp hoặc đang tạm khóa, cửa hàng sẽ không hiển thị công khai.",
  },
];

export function PlatformHome({ directory, rootDomain }: PlatformHomeProps) {
  return (
    <div className={styles.page}>
      <PlatformMotion depKey={directory.ok ? `${directory.page}-${directory.total}` : "err"} />

      {/* Section 1: Editorial Platform Hero */}
      <section className={styles.heroBand} aria-label="Giới thiệu nền tảng NexusMart">
        <div className={styles.heroGrid}>
          <div className={styles.heroCopy}>
            <h1 className={`${styles.heroTitle} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Cửa hàng riêng
                </span>
              </span>
              <span className="line">
                <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                  cho mỗi thương hiệu
                </span>
              </span>
            </h1>

            <p className={styles.heroLead} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
              NexusMart là nền tảng giúp thương hiệu và xưởng sản xuất Việt Nam tự mở trang bán hàng
              độc lập trên tên miền phụ riêng, trọn vẹn từ thiết kế giao diện, giỏ hàng, thanh toán
              COD và VietQR đến đối soát đơn hàng.
            </p>

            <div
              className={styles.heroActions}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <Button variant="primary" size="lg" href="/dang-ky?role=seller">
                Mở gian hàng
              </Button>
              <Button variant="secondary" size="lg" href="#danh-ba-gian-hang">
                Xem danh bạ gian hàng
              </Button>
            </div>

            <dl
              className={styles.heroFacts}
              data-rise=""
              style={{ "--i": 4 } as React.CSSProperties}
            >
              <div className={styles.factItem}>
                <dt className={styles.factKey}>Địa chỉ cửa hàng</dt>
                <dd className={styles.factVal}>Tên miền phụ độc lập từng gian hàng</dd>
              </div>
              <div className={styles.factItem}>
                <dt className={styles.factKey}>Thanh toán &amp; vận chuyển</dt>
                <dd className={styles.factVal}>COD, VietQR và giao nhận 34 tỉnh thành</dd>
              </div>
              <div className={styles.factItem}>
                <dt className={styles.factKey}>Cô lập dữ liệu</dt>
                <dd className={styles.factVal}>Phiên, giỏ hàng và đơn hàng tách riêng mỗi site</dd>
              </div>
            </dl>
          </div>

          <div className={styles.heroVisuals}>
            <figure
              className={styles.heroMainFigure}
              data-rise=""
              style={{ "--i": 1 } as React.CSSProperties}
            >
              <div className={styles.heroMainMedia}>
                <Image
                  src={WORKSHOP_MEDIA.src}
                  alt={WORKSHOP_MEDIA.alt}
                  fill
                  priority
                  sizes="(max-width: 1024px) 100vw, 48vw"
                  className={styles.coverPhoto}
                />
              </div>
              <figcaption className={styles.figureCaption}>
                Từ xưởng sản xuất trực tiếp tới trang cửa hàng mang bản sắc riêng, không qua trung
                gian chung kệ.
              </figcaption>
            </figure>

            <div className={styles.heroSubPair}>
              <figure
                className={styles.heroSubFigure}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={styles.heroSubMedia}>
                  <AmbientLoomVideo
                    src={WORKSHOP_MEDIA.loomVideo.src}
                    poster={WORKSHOP_MEDIA.loomVideo.poster}
                    alt={WORKSHOP_MEDIA.loomVideo.alt}
                    width={WORKSHOP_MEDIA.loomVideo.width}
                    height={WORKSHOP_MEDIA.loomVideo.height}
                    className={styles.loomVideo}
                  />
                </div>
                <figcaption className={styles.figureCaption}>
                  Vận hành ổn định từ sản xuất tới đóng gói
                </figcaption>
              </figure>

              <figure
                className={styles.heroSubFigure}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <div className={styles.heroSubMedia}>
                  <Image
                    src={FABRIC_MEDIA.src}
                    alt={FABRIC_MEDIA.alt}
                    fill
                    priority
                    sizes="(max-width: 1024px) 50vw, 24vw"
                    className={styles.coverPhoto}
                  />
                </div>
                <figcaption className={styles.figureCaption}>
                  Trình bày rõ từng chi tiết sản phẩm trên site riêng
                </figcaption>
              </figure>
            </div>
          </div>
        </div>
      </section>

      {/* Section 2: Real PUBLISHED Site Directory */}
      <SiteDirectorySection directory={directory} />

      {/* Section 3: Deep-Slate Seller Platform Band + Interactive Subdomain Preview */}
      <section
        id="danh-cho-nguoi-ban"
        className={styles.sellerBand}
        aria-labelledby="seller-platform-heading"
      >
        <div className={styles.sellerInner}>
          <div className={styles.sellerTop}>
            <div className={styles.sellerIntro} data-reveal="">
              <h2 id="seller-platform-heading" className={styles.sellerHeading}>
                Xây cửa hàng của riêng bạn, giữ trọn khách hàng của bạn
              </h2>
              <p className={styles.sellerLead}>
                Thay vì chia sẻ không gian với hàng chục đối thủ trên cùng một trang tìm kiếm,
                NexusMart trao cho mỗi người bán một trang web bán hàng độc lập cùng bộ công cụ quản
                trị tập trung trong Kênh người bán.
              </p>
              <div className={styles.sellerLinks}>
                <Link href="/dang-nhap" className={styles.sellerInlineLink}>
                  <span>Đã có tài khoản? Vào Kênh người bán</span>
                  <IconArrowUpRight size={15} />
                </Link>
              </div>
            </div>

            <SubdomainPreview rootDomain={rootDomain} />
          </div>

          <div className={styles.capabilityGrid}>
            {SELLER_CAPABILITIES.map((item) => (
              <article key={item.title} className={styles.capabilityBlock} data-reveal="">
                <h3 className={styles.capabilityTitle}>{item.title}</h3>
                <p className={styles.capabilityDetail}>{item.detail}</p>
              </article>
            ))}
          </div>
        </div>
      </section>

      {/* Section 4: How It Works (Chronological Launch Flow + Editorial Showcase) */}
      <section
        id="cach-van-hanh"
        className={styles.flowBand}
        aria-labelledby="platform-flow-heading"
      >
        <div className={styles.flowGrid}>
          <div className={styles.flowCopy}>
            <div className={styles.sectionHeader} data-reveal="">
              <h2 id="platform-flow-heading" className={styles.sectionHeading}>
                Từ hồ sơ gian hàng đến trang bán hàng riêng
              </h2>
              <p className={styles.sectionLead}>
                Mọi bước khởi tạo, quản lý sản phẩm, chỉnh sửa giao diện và đối soát doanh thu đều
                nằm gọn trong Kênh người bán.
              </p>
            </div>

            <ol className={styles.stageList}>
              {LAUNCH_STAGES.map((item) => (
                <li key={item.stage} className={styles.stageRow} data-reveal="">
                  <h3 className={styles.stageTitle}>{item.stage}</h3>
                  <p className={styles.stageDetail}>{item.detail}</p>
                </li>
              ))}
            </ol>
          </div>

          <div className={styles.flowVisuals}>
            <div className={styles.editorialPair} data-reveal="">
              <figure className={styles.editorialFigure}>
                <div className={styles.editorialMedia}>
                  <Image
                    src={MEDIA_BANNERS.everyday.src}
                    alt="Cửa hàng trực tuyến với phong cách tối giản sáng rõ"
                    fill
                    sizes="(max-width: 1024px) 100vw, 24vw"
                    className={styles.coverPhoto}
                  />
                </div>
                <figcaption className={styles.figureCaption}>
                  Tùy biến màu nền, phông chữ và banner đầu trang theo từng thương hiệu
                </figcaption>
              </figure>

              <figure className={styles.editorialFigure}>
                <div className={styles.editorialMedia}>
                  <Image
                    src={MEDIA_BANNERS.office.src}
                    alt="Giao diện trưng bày bộ sưu tập riêng biệt của gian hàng"
                    fill
                    sizes="(max-width: 1024px) 100vw, 24vw"
                    className={styles.coverPhoto}
                  />
                </div>
                <figcaption className={styles.figureCaption}>
                  Sắp xếp sản phẩm nổi bật, mã ưu đãi và chính sách bán hàng riêng
                </figcaption>
              </figure>
            </div>

            <div className={styles.readyPanel} data-reveal="">
              <h3 className={styles.readyTitle}>Sẵn sàng ngay khi cửa hàng được duyệt</h3>
              <p className={styles.readyText}>
                Bạn không cần thuê máy chủ, tự tích hợp cổng thanh toán hay xây lại luồng đặt hàng.
                Chỉ cần tập trung vào chất lượng sản phẩm, hình ảnh và trải nghiệm chăm sóc khách
                hàng trên tên miền của mình.
              </p>
              <div className={styles.readyActions}>
                <Button variant="primary" size="md" href="/dang-ky?role=seller">
                  Mở gian hàng
                </Button>
                <Button variant="secondary" size="md" href="/ve-nexusmart">
                  Tìm hiểu về NexusMart
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Section 5: Two Audiences, Clear Separation */}
      <section className={styles.audienceBand} aria-labelledby="platform-audience-heading">
        <div className={styles.audienceInner}>
          <div className={styles.sectionHeader} data-reveal="">
            <h2 id="platform-audience-heading" className={styles.sectionHeading}>
              Rõ ràng cho khách ghé thăm, độc lập cho người bán
            </h2>
          </div>

          <div className={styles.audienceSplit}>
            <article className={styles.audienceCol} data-reveal="">
              <h3 className={styles.audienceTitle}>Dành cho khách mua hàng</h3>
              <ul className={styles.bulletList}>
                <li className={styles.bulletItem}>
                  Truy cập trực tiếp trang cửa hàng chính thức từ danh bạ gian hàng trên NexusMart.
                </li>
                <li className={styles.bulletItem}>
                  Giỏ hàng, mã ưu đãi gian hàng, địa chỉ giao nhận và lịch sử đơn được giữ riêng cho
                  từng cửa hàng bạn mua.
                </li>
                <li className={styles.bulletItem}>
                  Thanh toán minh bạch qua COD khi nhận hàng hoặc quét mã VietQR xác nhận tức thì.
                </li>
              </ul>
              <div className={styles.audienceAction}>
                <Button variant="secondary" size="md" href="#danh-ba-gian-hang">
                  Chọn cửa hàng trong danh bạ
                </Button>
              </div>
            </article>

            <article className={styles.audienceHighlight} data-reveal="">
              <h3 className={styles.audienceTitle}>Nguyên tắc vận hành của NexusMart</h3>
              <ul className={styles.bulletList}>
                <li className={styles.bulletItem}>
                  Không ghép sản phẩm hay đẩy gợi ý của gian hàng khác lên trang cửa hàng của bạn.
                </li>
                <li className={styles.bulletItem}>
                  Mỗi tên miền phụ giữ phiên đăng nhập riêng và bảo vệ thao tác ghi đúng theo nguồn
                  truy cập.
                </li>
                <li className={styles.bulletItem}>
                  Mọi giao dịch thanh toán và đối soát đều hạch toán bằng số nguyên VND trên sổ cái
                  ghi kép.
                </li>
              </ul>
              <div className={styles.audienceAction}>
                <Button variant="ink" size="md" href="/tro-giup">
                  Xem trung tâm trợ giúp
                </Button>
              </div>
            </article>
          </div>
        </div>
      </section>

      {/* Section 6: Platform FAQ + Closing CTA */}
      <section className={styles.faqBand} aria-labelledby="platform-faq-heading">
        <div className={styles.faqInner}>
          <div className={styles.sectionHeader} data-reveal="">
            <h2 id="platform-faq-heading" className={styles.sectionHeading}>
              Câu hỏi thường gặp về nền tảng
            </h2>
            <p className={styles.sectionLead}>
              Những thông tin cốt lõi về cách vận hành cửa hàng độc lập trên NexusMart.
            </p>
          </div>

          <div className={styles.faqGrid}>
            {PLATFORM_FAQS.map((item) => (
              <article key={item.question} className={styles.faqItem} data-reveal="">
                <h3 className={styles.faqQuestion}>{item.question}</h3>
                <p className={styles.faqAnswer}>{item.answer}</p>
              </article>
            ))}
          </div>

          <div className={styles.closingBand} data-reveal="">
            <div className={styles.closingCopy}>
              <h2 className={styles.closingHeading}>Sẵn sàng mở cửa hàng riêng trên NexusMart</h2>
              <p className={styles.closingText}>
                Khởi tạo tài khoản người bán để chuẩn bị hồ sơ gian hàng, thiết kế giao diện và xuất
                bản trang bán hàng trên địa chỉ của riêng bạn.
              </p>
            </div>
            <div className={styles.closingActions}>
              <Button variant="primary" size="lg" href="/dang-ky?role=seller">
                Mở gian hàng
              </Button>
              <Button variant="secondary" size="lg" href="/dang-nhap">
                Đăng nhập
              </Button>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
