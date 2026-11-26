"use client";

import React from "react";
import { useRouter } from "next/navigation";
import { IconAlert, IconRefresh } from "@/components/Icons";
import styles from "./SiteDirectorySection.module.css";

export function DirectoryErrorState() {
  const router = useRouter();
  return (
    <div className={styles.errorBanner} role="alert">
      <div className={styles.errorCopy}>
        <IconAlert size={18} className={styles.errorIcon} />
        <p className={styles.errorTitle}>Chưa thể tải danh bạ lúc này.</p>
      </div>
      <button type="button" className={styles.errorBtn} onClick={() => router.refresh()}>
        <IconRefresh size={14} />
        <span>Tải lại</span>
      </button>
    </div>
  );
}
