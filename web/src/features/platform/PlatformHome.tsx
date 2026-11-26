import React from "react";
import Image from "next/image";
import Link from "next/link";
import { IconCheck, IconSpark, IconTruck } from "@/components/Icons";
import type { DirectoryResult } from "./directory.server";
import { SiteDirectorySection } from "./SiteDirectorySection";
import { StorefrontStageMini } from "./StorefrontStageMini";
import { SubdomainPreview } from "./SubdomainPreview";
import { VietQrSvg } from "./VietQrSvg";
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

const MOCK_ORDERS = [
  {
    code: "#DH-2408",
    detail: "2 sản phẩm · Hà Nội",
    total: "548.000₫",
    status: "Đã thanh toán",
    tone: "positive" as const,
  },
  {
    code: "#DH-2407",
    detail: "1 sản phẩm · Đà Nẵng",
    total: "299.000₫",
    status: "Đang giao",
    tone: "accent" as const,
  },
  {
    code: "#DH-2406",
    detail: "3 sản phẩm · TP.HCM",
    total: "720.000₫",
    status: "Hoàn tất",
    tone: "positive" as const,
  },
] as const;

const SCALE_CARDS = [
  {
    id: "workshop",
    title: "Xưởng may",
    description: "Bán thẳng sản phẩm của xưởng tới người mua.",
    image: "/banners/vietnam-workshop.jpg",
    alt: "Xưởng may Việt Nam kiểm tra chất liệu vải trên bàn cắt",
  },
  {
    id: "fashion-brand",
    title: "Thương hiệu thời trang",
    description: "Giữ nhận diện thương hiệu trên từng trang.",
    image: "/banners/campaign-everyday.jpg",
    alt: "Trang phục thường ngày của thương hiệu thời trang Việt",
  },
  {
    id: "starter-shop",
    title: "Shop mới bắt đầu",
    description: "Bắt đầu với vài sản phẩm, mở rộng khi đơn tăng.",
    image: "/banners/campaign-green-fiber.jpg",
    alt: "Cuộn vải tự nhiên và mẫu sợi chuẩn bị cho bộ sưu tập đầu tiên",
  },
] as const;

const LAUNCH_STEPS = [
  { num: "01", title: "Chọn giao diện riêng" },
  { num: "02", title: "Đăng sản phẩm lên kệ" },
  { num: "03", title: "Nhận đơn, quét VietQR" },
] as const;

export function PlatformHome({ directory, rootDomain }: PlatformHomeProps) {
  return (
    <div className={styles.page}>
      {/* 1. Hero */}
      <section className={styles.hero} aria-labelledby="platform-hero-heading">
        <div className={styles.heroMedia}>
          <Image
            src="/banners/campaign-office.jpg"
            alt="Chủ thương hiệu Việt trao đổi kế hoạch vận hành cửa hàng trực tuyến"
            fill
            priority
            sizes="100vw"
            className={styles.heroBgImg}
          />
          <div className={styles.heroOverlay} aria-hidden="true" />
        </div>

        <div className={styles.heroInner}>
          <div className={styles.heroContent}>
            <h1 id="platform-hero-heading" className={`${styles.heroTitle} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  Cửa hàng
                </span>
              </span>
              <span className="line">
                <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                  mang tên bạn
                </span>
              </span>
            </h1>

            <p
              className={styles.heroSubtitle}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              Gõ tên thương hiệu, có ngay cửa hàng riêng trên địa chỉ của bạn.
              <br />
              Giao diện riêng, khách riêng, thanh toán COD và VietQR.
            </p>

            <div
              className={styles.heroActions}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <Link href="/dang-ky?role=seller" className={styles.btnWhite}>
                Mở gian hàng
              </Link>
              <a href="#danh-ba-gian-hang" className={styles.btnOutline}>
                Xem các cửa hàng
              </a>
            </div>
          </div>
        </div>
      </section>

      {/* 2. Danh bạ gian hàng (overlapping rounded dark band) */}
      <SiteDirectorySection directory={directory} />

      {/* 3. Khối thẻ lớn Trợ lý AI */}
      <section className={styles.sectionBand} aria-labelledby="platform-ai-heading">
        <div className={styles.container}>
          <div className={styles.aiCard} data-reveal="">
            <div className={styles.aiCopyCol}>
              <h2 id="platform-ai-heading" className={styles.sectionHeading}>
                Trợ lý AI trả lời khách thay bạn
              </h2>
              <p className={styles.aiDescription}>
                Khách hỏi size, chất liệu, tình trạng đơn ngay trên cửa hàng. Trợ lý chỉ xem được
                những gì khách đó được phép xem.
              </p>
            </div>

            <div
              className={styles.aiPreviewCol}
              role="img"
              aria-label="Minh họa hội thoại trợ lý AI trả lời câu hỏi về size và chất liệu áo polo trên cửa hàng"
            >
              <div className={styles.chatBubbleCustomer} aria-hidden="true">
                <p>Áo polo này có size L không?</p>
              </div>

              <div className={styles.aiProductCard} aria-hidden="true">
                <div className={styles.aiProductPhoto}>
                  <Image
                    src="/models/model-polo-xanh-da.jpg"
                    alt=""
                    fill
                    sizes="(max-width: 900px) 45vw, 180px"
                    className={styles.coverTop}
                  />
                </div>
                <div className={styles.aiProductInfo}>
                  <p className={styles.aiProductName}>Áo Polo Pique Xanh Đá</p>
                  <p className={`${styles.aiProductPrice} num`}>299.000₫</p>
                  <p className={styles.aiProductMeta}>Size S · M · L · XL</p>
                </div>
              </div>

              <div className={styles.chatBubbleAssistant} aria-hidden="true">
                <div className={styles.assistantHeader}>
                  <IconSpark size={14} className={styles.assistantIcon} />
                  <span>Trợ lý cửa hàng</span>
                </div>
                <p>
                  Còn size L màu xanh đá, vải cotton pique thoáng mát, phù hợp tầm 65–74 kg. Bạn có
                  thể đặt ngay trên trang sản phẩm.
                </p>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* 4. Một nơi cho cả cửa hàng */}
      <section className={styles.sectionBand} aria-labelledby="platform-all-in-one-heading">
        <div className={styles.container}>
          <h2 id="platform-all-in-one-heading" className={styles.twoToneHeading} data-reveal="">
            <span className={styles.headingPrimary}>Một nơi cho cả cửa hàng.</span>
            <br />
            <span className={styles.headingSecondary}>Không cần ghép nhiều công cụ.</span>
          </h2>

          <div className={styles.threeColGrid} data-reveal="rows">
            {/* Card 1: Mini Storefront */}
            <article className={styles.featureCard}>
              <div className={styles.featureFrame}>
                <StorefrontStageMini rootDomain={rootDomain} />
              </div>
              <div className={styles.featureBody}>
                <h3 className={styles.featureTitle}>Giao diện mang dấu ấn riêng</h3>
                <p className={styles.featureCopy}>
                  8 mẫu giao diện, xem trước rồi mới xuất bản, gắn được tên miền riêng.
                </p>
              </div>
            </article>

            {/* Card 2: Orders in one place */}
            <article className={styles.featureCard}>
              <div className={styles.featureFrame}>
                <div
                  className={styles.ordersMock}
                  role="img"
                  aria-label="Minh họa danh sách đơn hàng với trạng thái thanh toán và giao hàng trong Kênh người bán"
                >
                  <div className={styles.mockHeader} aria-hidden="true">
                    <span className={styles.mockTitle}>Đơn hàng</span>
                    <span className={styles.mockMeta}>In nhãn · Đổi trả</span>
                  </div>

                  <ul className={styles.ordersList} aria-hidden="true">
                    {MOCK_ORDERS.map((order) => (
                      <li key={order.code} className={styles.orderRow}>
                        <div className={styles.orderLeft}>
                          <span className={`${styles.orderCode} num`}>{order.code}</span>
                          <span className={styles.orderDetail}>{order.detail}</span>
                        </div>
                        <div className={styles.orderRight}>
                          <span className={`${styles.orderTotal} num`}>{order.total}</span>
                          <span
                            className={`${styles.orderPill} ${
                              order.tone === "positive"
                                ? styles.orderPillPositive
                                : styles.orderPillAccent
                            }`}
                          >
                            {order.status}
                          </span>
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
              <div className={styles.featureBody}>
                <h3 className={styles.featureTitle}>Đơn hàng ở một chỗ</h3>
                <p className={styles.featureCopy}>
                  Đơn, tồn kho, in nhãn giao hàng và đổi trả trong Kênh người bán.
                </p>
              </div>
            </article>

            {/* Card 3: Shipping 34 provinces */}
            <article className={styles.featureCard}>
              <div className={styles.featureFrame}>
                <div
                  className={styles.shippingMock}
                  role="img"
                  aria-label="Minh họa ô tính phí vận chuyển theo tỉnh và phường trên 34 tỉnh thành"
                >
                  <div className={styles.mockHeader} aria-hidden="true">
                    <span className={styles.mockTitle}>
                      <IconTruck size={15} className={styles.shippingIcon} />
                      <span>Tính phí giao hàng</span>
                    </span>
                    <span className={styles.mockMeta}>34 tỉnh thành</span>
                  </div>

                  <div className={styles.shippingFields} aria-hidden="true">
                    <div className={styles.shippingField}>
                      <span className={styles.shippingFieldLabel}>Tỉnh / Thành phố</span>
                      <span className={styles.shippingFieldVal}>Thành phố Hà Nội</span>
                    </div>
                    <div className={styles.shippingField}>
                      <span className={styles.shippingFieldLabel}>Phường / Xã</span>
                      <span className={styles.shippingFieldVal}>Phường Hoàn Kiếm</span>
                    </div>
                  </div>

                  <div className={styles.shippingResult} aria-hidden="true">
                    <div>
                      <span className={styles.shippingFeeLabel}>Phí vận chuyển</span>
                      <span className={styles.shippingEta}>Dự kiến 1–2 ngày</span>
                    </div>
                    <span className={`${styles.shippingFeeVal} num`}>22.000₫</span>
                  </div>
                </div>
              </div>
              <div className={styles.featureBody}>
                <h3 className={styles.featureTitle}>Giao khắp 34 tỉnh thành</h3>
                <p className={styles.featureCopy}>
                  Tính phí theo tỉnh và phường, theo dõi trạng thái giao hàng.
                </p>
              </div>
            </article>
          </div>
        </div>
      </section>

      {/* 5. Thanh toán */}
      <section className={styles.paymentBand} aria-labelledby="platform-payment-heading">
        <div className={styles.container}>
          <div className={styles.paymentGrid}>
            <div className={styles.paymentCopyCol} data-reveal="">
              <h2 id="platform-payment-heading" className={styles.sectionHeading}>
                Thanh toán quen thuộc với khách Việt
              </h2>

              <div className={styles.paymentMethods}>
                <div className={styles.paymentMethodItem}>
                  <h3 className={styles.paymentMethodTitle}>COD — Thu tiền khi giao hàng</h3>
                  <p className={styles.paymentMethodDesc}>
                    Khách nhận kiện hàng mới thanh toán tiền mặt, quản lý trạng thái thu hộ gọn
                    trong Kênh người bán.
                  </p>
                </div>

                <div className={styles.paymentMethodItem}>
                  <h3 className={styles.paymentMethodTitle}>
                    VietQR — Khách quét mã, đơn tự xác nhận khi tiền về
                  </h3>
                  <p className={styles.paymentMethodDesc}>
                    Mã QR tạo sẵn theo đúng số tiền đơn, hệ thống tự cập nhật trạng thái ngay khi
                    giao dịch hoàn tất.
                  </p>
                </div>
              </div>
            </div>

            <div
              className={styles.checkoutSheet}
              role="img"
              aria-label="Minh họa trang thanh toán tóm tắt đơn hàng với lựa chọn COD và mã VietQR"
              data-reveal="scale"
            >
              <div className={styles.checkoutTop} aria-hidden="true">
                <span className={styles.checkoutTitle}>Tóm tắt đơn hàng</span>
                <span className={`${styles.checkoutCount} num`}>2 sản phẩm</span>
              </div>

              <ul className={styles.checkoutItems} aria-hidden="true">
                <li className={styles.checkoutItem}>
                  <span>Áo Polo Pique Xanh Đá · Size L</span>
                  <span className="num">299.000₫</span>
                </li>
                <li className={styles.checkoutItem}>
                  <span>Quần Short Daily Be · Size 31</span>
                  <span className="num">249.000₫</span>
                </li>
                <li className={styles.checkoutItemMuted}>
                  <span>Phí vận chuyển (Hà Nội)</span>
                  <span className="num">22.000₫</span>
                </li>
              </ul>

              <div className={styles.checkoutTotalRow} aria-hidden="true">
                <span>Tổng thanh toán</span>
                <span className={`${styles.checkoutTotalVal} num`}>570.000₫</span>
              </div>

              <div className={styles.checkoutOptions} aria-hidden="true">
                <div className={styles.checkoutOption}>
                  <span className={styles.radioCircle} />
                  <span>COD · Thu tiền khi giao hàng</span>
                </div>
                <div className={`${styles.checkoutOption} ${styles.checkoutOptionSelected}`}>
                  <span className={styles.radioCircleSelected} />
                  <span>VietQR · Chuyển khoản quét mã</span>
                </div>
              </div>

              <div className={styles.checkoutQrRow} aria-hidden="true">
                <div className={styles.checkoutQrBox}>
                  <VietQrSvg size={108} className={styles.checkoutQrSvg} decorative />
                </div>
                <div className={styles.checkoutQrMeta}>
                  <span className={styles.checkoutConfirmBadge}>
                    <IconCheck size={14} />
                    <span>Tự động xác nhận</span>
                  </span>
                  <p className={styles.checkoutQrHint}>
                    Quét mã bằng ứng dụng ngân hàng để hoàn tất đơn{" "}
                    <span className="num">570.000₫</span>.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* 6. Dành cho mọi quy mô */}
      <section className={styles.sectionBand} aria-labelledby="platform-scale-heading">
        <div className={styles.container}>
          <h2 id="platform-scale-heading" className={styles.sectionHeading} data-reveal="">
            Dành cho mọi quy mô
          </h2>

          <div className={styles.threeColGrid} data-reveal="rows">
            {SCALE_CARDS.map((card) => (
              <article key={card.id} className={styles.scaleCard}>
                <div className={styles.scalePhoto}>
                  <Image
                    src={card.image}
                    alt={card.alt}
                    fill
                    sizes="(max-width: 768px) 100vw, 33vw"
                    className={styles.coverCenter}
                  />
                </div>
                <div className={styles.featureBody}>
                  <h3 className={styles.featureTitle}>{card.title}</h3>
                  <p className={styles.featureCopy}>{card.description}</p>
                </div>
              </article>
            ))}
          </div>

          <div className={styles.scaleCtaWrap}>
            <Link href="/dang-ky?role=seller" className={styles.btnOutline}>
              Mở gian hàng
            </Link>
          </div>
        </div>
      </section>

      {/* 7. FAQ */}
      <section className={styles.sectionBand} aria-labelledby="platform-faq-heading">
        <div className={styles.container}>
          <div className={styles.faqGrid}>
            <div className={styles.faqLeadCol}>
              <h2 id="platform-faq-heading" className={styles.sectionHeading} data-reveal="">
                Câu hỏi thường gặp
              </h2>
            </div>

            <div className={styles.faqList} data-reveal="rows">
              {PLATFORM_FAQS.map((item) => (
                <details key={item.id} name="platform-faq" className={styles.faqTile}>
                  <summary className={styles.faqSummary}>
                    <span className={styles.faqQuestion}>{item.question}</span>
                    <span className={styles.faqPlus} aria-hidden="true" />
                  </summary>
                  <div className={styles.faqAnswerWrap}>
                    <p className={styles.faqAnswer}>{item.answer}</p>
                  </div>
                </details>
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* 8. Lên sóng trong ba bước */}
      <section className={styles.stepsBand} aria-labelledby="platform-steps-heading">
        <div className={styles.container}>
          <h2 id="platform-steps-heading" className={styles.stepsHeading} data-reveal="">
            Lên sóng trong ba bước
          </h2>

          <div className={styles.stepsGrid}>
            <div className={styles.stepsGallery} data-reveal="rows">
              <div className={styles.stepsPhotoPrimary}>
                <Image
                  src="/models/model-ao-thun-trang.jpg"
                  alt="Sản phẩm áo thun cotton trưng bày trên cửa hàng trực tuyến"
                  fill
                  sizes="(max-width: 900px) 46vw, 260px"
                  className={styles.coverTop}
                />
              </div>
              <div className={styles.stepsPhotoSecondary}>
                <Image
                  src="/banners/fabric-closeup.jpg"
                  alt="Bề mặt vải dệt tinh chọn của sản phẩm Việt"
                  fill
                  sizes="(max-width: 900px) 46vw, 260px"
                  className={styles.coverCenter}
                />
              </div>
            </div>

            <div className={styles.stepsRightCol}>
              <ol className={styles.stepsList} data-reveal="rows">
                {LAUNCH_STEPS.map((step) => (
                  <li key={step.num} className={styles.stepItem}>
                    <span className={`${styles.stepNum} num`}>{step.num}</span>
                    <span className={styles.stepTitle}>{step.title}</span>
                  </li>
                ))}
              </ol>

              <div className={styles.subdomainWrap}>
                <SubdomainPreview rootDomain={rootDomain} />
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
