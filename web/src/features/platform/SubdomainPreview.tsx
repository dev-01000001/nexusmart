"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { IconArrowUpRight } from "@/components/Icons";
import { checkSubdomain, suggestSubdomain } from "./subdomain";
import styles from "./SubdomainPreview.module.css";

export interface SubdomainPreviewProps {
  rootDomain: string;
}

const CHECK_COPY: Record<ReturnType<typeof checkSubdomain>, string> = {
  empty: "Nhập tên thương hiệu hoặc xưởng của bạn để xem thử địa chỉ tên miền phụ.",
  short:
    "Tên miền phụ cần ít nhất 3 ký tự chữ thường hoặc số, không bắt đầu hay kết thúc bằng dấu gạch nối.",
  reserved:
    "Tên này thuộc danh sách hệ thống giữ riêng. Hãy thử thêm tên địa phương hoặc dòng sản phẩm.",
  ok: "Định dạng hợp lệ. Bạn có thể đăng ký và chọn tên miền phụ này trong Kênh người bán.",
};

export function SubdomainPreview({ rootDomain }: SubdomainPreviewProps) {
  const [shopName, setShopName] = useState("");

  const { label, status } = useMemo(() => {
    const suggested = suggestSubdomain(shopName);
    return { label: suggested, status: checkSubdomain(suggested) };
  }, [shopName]);

  const displayLabel = label || "ten-thuong-hieu";
  const ctaHref = label
    ? `/dang-ky?role=seller&ten=${encodeURIComponent(shopName.trim())}`
    : "/dang-ky?role=seller";

  return (
    <div className={styles.box} data-reveal="">
      <div className={styles.head}>
        <h3 className={styles.title}>Xem thử địa chỉ cửa hàng của bạn</h3>
        <p className={styles.lead}>
          Mỗi gian hàng hoạt động tại một tên miền phụ tách biệt, dùng bảng màu và bố cục riêng.
        </p>
      </div>

      <div className={styles.field}>
        <label htmlFor="platform-shop-name-input" className={styles.label}>
          Tên thương hiệu hoặc xưởng sản xuất
        </label>
        <input
          id="platform-shop-name-input"
          type="text"
          value={shopName}
          onChange={(e) => setShopName(e.target.value)}
          placeholder="Ví dụ: Xưởng Lụa Hội An"
          maxLength={64}
          className={styles.input}
        />
      </div>

      <div className={styles.previewBar} aria-live="polite">
        <span className={styles.previewScheme}>https://</span>
        <span className={label ? styles.previewSlug : styles.previewPlaceholder}>
          {displayLabel}
        </span>
        <span className={styles.previewDomain}>.{rootDomain}</span>
      </div>

      <p
        className={`${styles.status} ${
          status === "ok"
            ? styles.statusOk
            : status === "reserved" || status === "short"
              ? styles.statusWarn
              : styles.statusMuted
        }`}
      >
        {CHECK_COPY[status]}
      </p>

      <div className={styles.actions}>
        <Link href={ctaHref} className={styles.cta}>
          <span>Mở gian hàng</span>
          <IconArrowUpRight size={15} />
        </Link>
      </div>
    </div>
  );
}
