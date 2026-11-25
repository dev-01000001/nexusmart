"use client";

import React, { useCallback, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { RegisterForm } from "@/features/account/AuthForms";
import { suggestSubdomain } from "./subdomain";
import styles from "./PlatformAuthShell.module.css";

export type AuthRoleStage = "seller" | "buyer";

export interface PlatformAuthShellProps {
  mode: "login" | "register";
  title: string;
  initialRole?: AuthRoleStage;
  suggestedShopName?: string;
  rootDomain?: string;
  slideDir?: "none" | "forward" | "back";
  children: React.ReactNode;
}

const ROLE_STAGE = {
  seller: {
    label: "Người bán",
    statement: "Quản trị gian hàng, giao diện và đơn hàng.",
    image: WORKSHOP_MEDIA.src,
    alt: WORKSHOP_MEDIA.alt,
    ctaLabel: null as string | null,
    ctaHref: null as string | null,
  },
  buyer: {
    label: "Khách mua",
    statement: "Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng.",
    image: MEDIA_BANNERS.everyday.src,
    alt: MEDIA_BANNERS.everyday.alt,
    ctaLabel: "Vào danh bạ gian hàng",
    ctaHref: "/#danh-ba-gian-hang",
  },
} as const;

export function PlatformAuthShell({
  mode,
  title,
  initialRole = "seller",
  suggestedShopName = "",
  rootDomain = "",
  slideDir = "none",
  children,
}: PlatformAuthShellProps) {
  const [role, setRole] = useState<AuthRoleStage>(initialRole);

  const stage = ROLE_STAGE[role];
  const suggestedSlug = suggestedShopName ? suggestSubdomain(suggestedShopName) : "";
  const displayTitle =
    mode === "register" ? (role === "seller" ? "Mở gian hàng" : "Tạo tài khoản") : title;

  return (
    <section
      className={styles.split}
      aria-label={mode === "login" ? "Đăng nhập NexusMart" : "Đăng ký NexusMart"}
    >
      <aside className={styles.stageCol} data-role={role} aria-label="Chọn vai trò sử dụng">
        <div className={styles.stageTop}>
          <div className={styles.roleSwitch} role="group" aria-label="Chọn vai trò">
            {(["seller", "buyer"] as const).map((r) => {
              const selected = role === r;
              return (
                <button
                  key={r}
                  type="button"
                  aria-pressed={selected}
                  className={`${styles.roleBtn} ${selected ? styles.roleBtnActive : ""}`}
                  onClick={() => setRole(r)}
                >
                  {ROLE_STAGE[r].label}
                </button>
              );
            })}
          </div>

          {suggestedShopName && role === "seller" && (
            <p className={styles.shopPill} role="status">
              <span>{suggestedShopName}</span>
              {suggestedSlug && rootDomain && (
                <span className={styles.shopSlug}>
                  <span>{suggestedSlug}</span>
                  <span>.{rootDomain}</span>
                </span>
              )}
            </p>
          )}
        </div>

        <figure className={styles.stageMedia}>
          {(["seller", "buyer"] as const).map((r) => (
            <span
              key={r}
              className={`${styles.stageLayer} ${role === r ? styles.stageLayerActive : ""}`}
              aria-hidden={role !== r}
            >
              <Image
                src={ROLE_STAGE[r].image}
                alt={ROLE_STAGE[r].alt}
                fill
                priority
                sizes="(max-width: 960px) 100vw, 50vw"
                className={styles.coverImg}
              />
            </span>
          ))}
        </figure>

        <div className={styles.stageBottom}>
          <p className={`${styles.stageStatement} type-tight`}>{stage.statement}</p>
          {stage.ctaLabel && stage.ctaHref && (
            <Link href={stage.ctaHref} className={styles.stageCta}>
              {stage.ctaLabel}
            </Link>
          )}
        </div>
      </aside>

      <div className={styles.formCol}>
        <div className={styles.formInner}>
          <header className={styles.formHeader}>
            <h1 className={`${styles.title} type-wide lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  {displayTitle}
                </span>
              </span>
            </h1>
          </header>

          <div className={styles.formSlot} data-slide={slideDir}>
            {children}
          </div>
        </div>
      </div>
    </section>
  );
}

export interface PlatformRegisterShellProps {
  initialRole?: AuthRoleStage;
  suggestedShopName?: string;
  rootDomain?: string;
}

export function PlatformRegisterShell({
  initialRole = "seller",
  suggestedShopName = "",
  rootDomain = "",
}: PlatformRegisterShellProps) {
  const [slideDir, setSlideDir] = useState<"none" | "forward" | "back">("none");

  const handleStepChange = useCallback((nextStep: "register" | "otp") => {
    setSlideDir(nextStep === "otp" ? "forward" : "back");
  }, []);

  return (
    <PlatformAuthShell
      mode="register"
      title={initialRole === "seller" ? "Mở gian hàng" : "Tạo tài khoản"}
      initialRole={initialRole}
      suggestedShopName={suggestedShopName}
      rootDomain={rootDomain}
      slideDir={slideDir}
    >
      <RegisterForm
        variant="platform"
        buyerHref="/#danh-ba-gian-hang"
        loginHref="/dang-nhap"
        onStepChange={handleStepChange}
      />
    </PlatformAuthShell>
  );
}
