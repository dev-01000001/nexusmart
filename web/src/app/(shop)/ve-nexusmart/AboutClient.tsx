"use client";

import React, { useState } from "react";
import Image from "next/image";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/Button";
import { IconArrowUpRight } from "@/components/Icons";
import { StateView } from "@/components/StateView";
import { FABRIC_MEDIA, WORKSHOP_MEDIA } from "@/data/media";
import type { DirectoryResult } from "@/features/platform/directory.server";
import { AmbientLoomVideo, PlatformMotion } from "@/features/platform/PlatformMotion";
import styles from "./page.module.css";

export interface AboutClientProps {
  directory: DirectoryResult;
}

type AboutTab = "model" | "capabilities" | "stores" | "onboarding";

const ABOUT_TABS: ReadonlyArray<{ id: AboutTab; label: string }> = [
  { id: "model", label: "Triết lý nền tảng" },
  { id: "capabilities", label: "Hạ tầng tích hợp" },
  { id: "stores", label: "Cửa hàng đang mở bán" },
  { id: "onboarding", label: "Mở gian hàng" },
];

const MODEL_PILLARS = [
  {
    title: "Không đặt thương hiệu vào cuộc đua giá chung kệ",
    detail:
      "Thay vì gom hàng hóa của mọi người bán vào một trang sàn duy nhất, NexusMart cấp cho mỗi gian hàng một tên miền phụ riêng. Thương hiệu tự quyết định cách kể câu chuyện, bảng màu, danh mục và trải nghiệm mua sắm.",
  },
  {
    title: "Dữ liệu, giỏ hàng và đơn hàng tách bạch theo từng cửa hàng",
    detail:
      "Khi khách hàng truy cập một cửa hàng trên NexusMart, phiên đăng nhập, giỏ hàng, mã ưu đãi và lịch sử đơn thuộc trọn vẹn về cửa hàng đó. Không có tình trạng lẫn đơn hay gợi ý sang đối thủ.",
  },
  {
    title: "Kế thừa chuẩn vận hành bài bản cho hàng Việt",
    detail:
      "Từ khâu khai báo tồn kho theo màu sắc, kích cỡ, tính cước giao vận cho 34 tỉnh thành đến đối soát dòng tiền nguyên VND, toàn bộ hạ tầng thương mại cốt lõi đã sẵn sàng để người bán tập trung vào sản phẩm.",
  },
];

const INFRA_BLOCKS = [
  {
    title: "Trình dựng giao diện có lịch sử phiên bản",
    detail:
      "Chỉnh sửa màu nền, màu chữ, màu nhấn, phông chữ, biểu trưng và thứ tự từng khối trang chủ. Xem trước bản nháp trước khi xuất bản và quay lại phiên bản trước chỉ bằng một thao tác.",
  },
  {
    title: "Luồng đặt hàng khép kín trên tên miền phụ",
    detail:
      "Tìm kiếm sản phẩm trong shop, xem tồn kho thực tế theo phiên bản, áp mã giảm giá riêng của gian hàng, thanh toán COD hoặc chuyển khoản VietQR xác nhận trạng thái tự động.",
  },
  {
    title: "Sổ cái đối soát nguyên VND",
    detail:
      "Mọi khoản thu từ đơn hàng, hoàn tiền và số dư khả dụng của người bán đều được hạch toán rõ ràng bằng số nguyên VND, giúp đối chiếu nhanh giữa Kênh người bán và Cổng quản trị.",
  },
  {
    title: "Kiểm soát xuất bản và an toàn truy cập",
    detail:
      "Quản trị viên duyệt hồ sơ gian hàng trước khi mở bán, bảo vệ thao tác theo đúng tên miền đang truy cập và có thể tạm dừng hiển thị ngay lập tức nếu phát hiện vi phạm.",
  },
];

const LAUNCH_STEPS = [
  {
    title: "Đăng ký và xác thực email",
    detail:
      "Khởi tạo tài khoản trên trang gốc NexusMart và hoàn tất mã xác thực 6 chữ số gửi về hộp thư của bạn.",
  },
  {
    title: "Gửi hồ sơ gian hàng để duyệt",
    detail:
      "Hoàn thiện thông tin pháp lý, địa chỉ kho gửi hàng và danh mục sản phẩm dự kiến để quản trị viên kích hoạt quyền người bán.",
  },
  {
    title: "Thiết lập giao diện trên tên miền phụ",
    detail:
      "Đăng sản phẩm cùng tồn kho thực tế, tạo mã ưu đãi riêng của shop, tùy biến giao diện và kiểm tra kỹ trên bản xem trước.",
  },
  {
    title: "Xuất bản cửa hàng công khai",
    detail:
      "Sau khi bấm Xuất bản, địa chỉ cửa hàng đi vào hoạt động ngay và xuất hiện trong danh bạ gian hàng trên trang chủ NexusMart.",
  },
];

export function AboutClient({ directory }: AboutClientProps) {
  const searchParams = useSearchParams();
  const tabParam = searchParams.get("tab") as AboutTab | null;
  const [activeTab, setActiveTab] = useState<AboutTab>(
    tabParam && ABOUT_TABS.some((t) => t.id === tabParam) ? tabParam : "model"
  );

  return (
    <div className={styles.page}>
      <PlatformMotion depKey={activeTab} />

      <section className={styles.heroGrid} aria-label="Về nền tảng NexusMart">
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Nền tảng cho từng
              </span>
            </span>
            <span className="line">
              <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                cửa hàng độc lập
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            NexusMart cung cấp hạ tầng bán hàng trọn gói để mỗi thương hiệu và xưởng sản xuất tự vận
            hành cửa hàng trực tuyến trên tên miền phụ riêng, giữ trọn nhận diện và dữ liệu đơn hàng
            của mình.
          </p>
          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="/dang-ky?role=seller">
              Mở gian hàng
            </Button>
            <Button variant="secondary" size="lg" href="/#danh-ba-gian-hang">
              Xem danh bạ gian hàng
            </Button>
          </div>
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
                sizes="(max-width: 1080px) 100vw, 50vw"
                className={styles.heroPhotoImg}
              />
            </div>
            <figcaption className={styles.heroPhotoCaption}>
              Đồng hành cùng các xưởng sản xuất và thương hiệu nội địa mở kênh bán hàng trực tiếp
            </figcaption>
          </figure>

          <div className={styles.heroSubPair}>
            <figure
              className={styles.heroSubFigure}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.heroSubMedia}>
                <Image
                  src={FABRIC_MEDIA.src}
                  alt={FABRIC_MEDIA.alt}
                  fill
                  priority
                  sizes="(max-width: 1080px) 50vw, 24vw"
                  className={styles.heroPhotoImg}
                />
              </div>
              <figcaption className={styles.heroPhotoCaption}>
                Không gian trình bày sản phẩm và chất liệu riêng biệt
              </figcaption>
            </figure>

            <figure
              className={styles.heroSubFigure}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.heroSubMedia}>
                <Image
                  src={WORKSHOP_MEDIA.greenFiber.src}
                  alt={WORKSHOP_MEDIA.greenFiber.alt}
                  fill
                  priority
                  sizes="(max-width: 1080px) 50vw, 24vw"
                  className={styles.heroPhotoImg}
                />
              </div>
              <figcaption className={styles.heroPhotoCaption}>
                Chủ động câu chuyện thương hiệu và chính sách hậu mãi
              </figcaption>
            </figure>
          </div>
        </div>
      </section>

      <section className={styles.tabsSection} aria-labelledby="about-workspace-heading">
        <div className={styles.tabsHead} data-reveal="">
          <h2 id="about-workspace-heading" className={styles.sectionTitle}>
            Cách NexusMart tổ chức nền tảng
          </h2>
          <div className={styles.tabsBar} role="tablist" aria-label="Chuyên mục Về NexusMart">
            {ABOUT_TABS.map((tab) => {
              const selected = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${styles.tabBtn} ${selected ? styles.tabBtnActive : ""}`}
                  onClick={() => setActiveTab(tab.id)}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
        </div>

        {activeTab === "model" && (
          <div role="tabpanel" className={styles.storyLeadGrid}>
            <div className={styles.pillarsCol}>
              {MODEL_PILLARS.map((pillar) => (
                <article key={pillar.title} className={styles.pillarEntry} data-reveal="">
                  <h3 className={styles.pillarTitle}>{pillar.title}</h3>
                  <p className={styles.pillarDetail}>{pillar.detail}</p>
                </article>
              ))}
            </div>

            <div className={styles.videoCol}>
              <div className={styles.videoFrame} data-reveal="">
                <AmbientLoomVideo
                  src={WORKSHOP_MEDIA.loomVideo.src}
                  poster={WORKSHOP_MEDIA.loomVideo.poster}
                  alt={WORKSHOP_MEDIA.loomVideo.alt}
                  width={WORKSHOP_MEDIA.loomVideo.width}
                  height={WORKSHOP_MEDIA.loomVideo.height}
                  className={styles.loomVideo}
                />
              </div>
              <p className={styles.videoCaption} data-reveal="">
                Mỗi gian hàng trên NexusMart được vận hành như một cửa hàng chuyên biệt: có địa chỉ
                truy cập riêng, bộ nhận diện riêng và quy trình xử lý đơn minh bạch.
              </p>

              <dl className={styles.standardBand} data-reveal="">
                <div className={styles.specRow}>
                  <dt className={styles.specKey}>Mô hình trang bán hàng</dt>
                  <dd className={styles.specVal}>
                    Tên miền phụ riêng cho từng gian hàng đã xuất bản
                  </dd>
                </div>
                <div className={styles.specRow}>
                  <dt className={styles.specKey}>Thanh toán và giao vận</dt>
                  <dd className={styles.specVal}>
                    COD, chuyển khoản VietQR và tính cước giao 34 tỉnh thành
                  </dd>
                </div>
                <div className={styles.specRow}>
                  <dt className={styles.specKey}>Quản trị và đối soát</dt>
                  <dd className={styles.specVal}>
                    Kênh người bán tập trung và sổ cái ghi kép nguyên VND
                  </dd>
                </div>
              </dl>
            </div>
          </div>
        )}

        {activeTab === "capabilities" && (
          <div role="tabpanel" className={styles.infraGrid}>
            {INFRA_BLOCKS.map((item) => (
              <article key={item.title} className={styles.infraCard} data-reveal="">
                <h3 className={styles.pillarTitle}>{item.title}</h3>
                <p className={styles.pillarDetail}>{item.detail}</p>
              </article>
            ))}
          </div>
        )}

        {activeTab === "stores" && (
          <div role="tabpanel" className={styles.panelStack}>
            {!directory.ok ? (
              <StateView
                kind="error"
                title="Chưa thể tải danh sách cửa hàng lúc này"
                message="Kết nối hệ thống đang tạm gián đoạn. Bạn có thể thử lại sau ít phút."
                actionLabel="Xem danh bạ tại trang chủ"
                actionHref="/#danh-ba-gian-hang"
              />
            ) : directory.sites.length === 0 ? (
              <StateView
                kind="empty"
                title="Chưa có cửa hàng nào xuất bản công khai"
                message="Cửa hàng sẽ xuất hiện tại đây ngay khi hoàn tất xuất bản trong Kênh người bán."
                actionLabel="Mở gian hàng"
                actionHref="/dang-ky?role=seller"
              />
            ) : (
              <div className={styles.storesGrid}>
                {directory.sites.map((site) => (
                  <a key={site.siteId} href={site.origin} className={styles.storeCard}>
                    <div className={styles.storeHead}>
                      <h3 className={styles.storeName}>{site.name}</h3>
                      <span className={styles.storeHost}>{site.host}</span>
                    </div>
                    <p className={styles.pillarDetail}>
                      {site.tagline || "Cửa hàng chính thức vận hành trên nền tảng NexusMart."}
                    </p>
                    <span className={styles.storeLink}>
                      <span>Truy cập cửa hàng</span>
                      <IconArrowUpRight size={15} />
                    </span>
                  </a>
                ))}
              </div>
            )}
          </div>
        )}

        {activeTab === "onboarding" && (
          <div role="tabpanel" className={styles.timelineSurface}>
            {LAUNCH_STEPS.map((step) => (
              <article key={step.title} className={styles.stepRow}>
                <h3 className={styles.pillarTitle}>{step.title}</h3>
                <p className={styles.pillarDetail}>{step.detail}</p>
              </article>
            ))}
          </div>
        )}
      </section>

      <section className={styles.ctaBand} aria-labelledby="about-cta-heading" data-reveal="">
        <div className={styles.ctaText}>
          <h2 id="about-cta-heading" className={styles.ctaHeading}>
            Mở trang bán hàng riêng cùng NexusMart
          </h2>
          <p className={styles.ctaSub}>
            Bắt đầu bằng tài khoản người bán hoặc khám phá các cửa hàng đang hoạt động trên nền
            tảng.
          </p>
        </div>
        <div className={styles.ctaLinks}>
          <Button variant="primary" size="md" href="/dang-ky?role=seller">
            Mở gian hàng
          </Button>
          <Button variant="secondary" size="md" href="/#danh-ba-gian-hang">
            Danh bạ gian hàng
          </Button>
          <Button variant="secondary" size="md" href="/tro-giup">
            Trung tâm trợ giúp
          </Button>
        </div>
      </section>
    </div>
  );
}
