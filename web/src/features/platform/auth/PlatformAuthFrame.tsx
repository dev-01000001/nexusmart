"use client";

import React, { useCallback, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { ArrowRight, Globe } from "lucide-react";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { RegisterForm } from "@/features/account/AuthForms";
import { PLATFORM_CONTAINER } from "../platformStyles";
import { resolveAfterRegisterHref, suggestSubdomain } from "../subdomain";

export type AuthRoleStage = "seller" | "buyer";

export interface PlatformAuthFrameProps {
  mode: "login" | "register" | "recovery";
  title: string;
  subtitle?: string;
  initialRole?: AuthRoleStage;
  suggestedShopName?: string;
  rootDomain?: string;
  showRoleSwitch?: boolean;
  onRoleChange?: (role: AuthRoleStage) => void;
  children: React.ReactNode;
}

const ROLE_STAGE = {
  seller: {
    label: "Người bán",
    imageSrc: WORKSHOP_MEDIA.src,
    imageAlt: WORKSHOP_MEDIA.alt,
    caption:
      "Cửa hàng mang tên bạn, tồn kho đồng bộ theo từng SKU, thanh toán COD và VietQR minh bạch.",
    loginSubtitle: "Vào Kênh người bán hoặc Cổng quản trị trên nền tảng NexusMart.",
    registerSubtitle: "Mở cửa hàng mang tên riêng miễn phí, tồn kho cập nhật theo thời gian thực.",
  },
  buyer: {
    label: "Khách mua",
    imageSrc: MEDIA_BANNERS.everyday.src,
    imageAlt: MEDIA_BANNERS.everyday.alt,
    caption: "Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng theo từng thương hiệu bạn chọn mua.",
    loginSubtitle: "Khách mua hàng chọn cửa hàng trong danh bạ để mua sắm và theo dõi đơn.",
    registerSubtitle:
      "Tạo tài khoản NexusMart để theo dõi đơn hàng tại các cửa hàng bạn yêu thích.",
  },
} as const;

/**
 * Scoped `--ui-*` dark variables for shared `AuthForms` inside root-host auth pages.
 * Combined with `data-site-theme-mode="dark"` (which activates `@custom-variant dark` and dark
 * semantic tokens in `ui.css`), this adapts shared shadcn controls to the homepage dark palette
 * without touching `AuthForms.tsx` or storefront auth pages.
 */
const DARK_AUTH_FORM_VARS = {
  "--ui-background": "#02090a",
  "--ui-foreground": "#ffffff",
  "--ui-card": "#0a181a",
  "--ui-card-foreground": "#ffffff",
  "--ui-popover": "#0a181a",
  "--ui-popover-foreground": "#ffffff",
  "--ui-primary": "#ffffff",
  "--ui-primary-foreground": "#000000",
  "--ui-secondary": "#12292c",
  "--ui-secondary-foreground": "#ffffff",
  "--ui-muted": "#0e2225",
  "--ui-muted-foreground": "#9dabad",
  "--ui-accent": "#173438",
  "--ui-accent-foreground": "#ffffff",
  "--ui-brand": "#66ccd7",
  "--ui-brand-foreground": "#061012",
  "--ui-border": "rgba(255, 255, 255, 0.14)",
  "--ui-input": "#5a7276",
  "--ui-ring": "#66ccd7",
  "--ui-shadow-card": "none",
  "--ui-shadow-button": "none",
} as React.CSSProperties;

const DARK_AUTH_FORM_SLOT_CLS = [
  "text-foreground",
  "[&_[data-slot=button]:not([data-size=icon])]:rounded-full",
  "[&_[data-slot=button][data-size=lg]]:min-h-[var(--home-btn-mid-h)]",
  "[&_[data-slot=button][data-size=lg]]:font-[550]",
  "[&_[data-slot=button][data-variant=secondary]]:border",
  "[&_[data-slot=button][data-variant=secondary]]:border-[var(--home-border-strong)]",
  "[&_[data-slot=input]]:rounded-xl",
  "[&_[data-slot=input]]:border-[var(--ui-input)]",
  "[&_[data-slot=input]]:text-[var(--home-ink)]",
  "[&_[data-slot=input]]:placeholder:text-[var(--home-input-placeholder)]",
  "[&_[data-slot=field-label]]:text-[var(--home-ink)]",
].join(" ");

export function PlatformAuthFrame({
  mode,
  title,
  subtitle,
  initialRole = "seller",
  suggestedShopName = "",
  rootDomain = "",
  showRoleSwitch = mode !== "recovery",
  onRoleChange,
  children,
}: PlatformAuthFrameProps) {
  const [role, setRole] = useState<AuthRoleStage>(initialRole);

  const handleSelectRole = (nextRole: AuthRoleStage) => {
    setRole(nextRole);
    onRoleChange?.(nextRole);
  };

  const stage = ROLE_STAGE[role];
  const suggestedSlug = suggestedShopName ? suggestSubdomain(suggestedShopName) : "";
  const displayTitle =
    mode === "register" ? (role === "seller" ? "Mở gian hàng" : "Tạo tài khoản") : title;
  const resolvedSubtitle =
    subtitle ??
    (mode === "login"
      ? stage.loginSubtitle
      : mode === "register"
        ? stage.registerSubtitle
        : undefined);

  return (
    <div className="relative w-full overflow-x-clip bg-[var(--home-band)] pt-[calc(var(--home-header-h)+clamp(28px,4vw,52px))] pb-[clamp(56px,7vw,96px)] text-[var(--home-ink)] [background:radial-gradient(ellipse_56%_48%_at_84%_12%,var(--home-glow-soft),transparent_62%),radial-gradient(ellipse_48%_40%_at_14%_86%,rgba(102,204,215,0.1),transparent_64%),var(--home-band)]">
      <div className={PLATFORM_CONTAINER}>
        <div className="grid grid-cols-1 items-stretch gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.02fr)] lg:gap-10 xl:gap-14">
          {/* Left column: Dark editorial form card */}
          <div className="flex flex-col justify-between rounded-[24px] border border-[var(--home-border)] bg-[var(--home-card-dark)] p-6 shadow-[var(--home-shadow-card)] sm:p-8 lg:p-10">
            <div className="mx-auto w-full max-w-[440px]">
              <h1 className="font-[family-name:var(--home-font)] text-[clamp(2rem,1.4rem+1.8vw,2.75rem)] leading-[1.1] font-[330] tracking-[-0.025em] text-[var(--home-ink)] [text-wrap:balance]">
                {displayTitle}
              </h1>

              {(resolvedSubtitle || (suggestedShopName && role === "seller")) && (
                <div className="mt-3 space-y-3 text-[length:var(--home-fs-body)] leading-[1.5] text-[var(--home-ink-2)]">
                  {resolvedSubtitle ? <p>{resolvedSubtitle}</p> : null}
                  {suggestedShopName && role === "seller" ? (
                    <div className="flex flex-wrap items-center gap-2" role="status">
                      <span className="inline-flex max-w-full items-center gap-2 rounded-full border border-[rgba(102,204,215,0.34)] bg-[rgba(102,204,215,0.12)] px-3.5 py-1.5 text-xs font-medium text-[var(--home-accent)]">
                        <Globe className="size-3.5 shrink-0" aria-hidden="true" />
                        <span className="truncate">
                          {suggestedShopName}
                          {suggestedSlug && rootDomain ? ` · ${suggestedSlug}.${rootDomain}` : ""}
                        </span>
                      </span>
                    </div>
                  ) : null}
                </div>
              )}

              <div className="mt-7 space-y-6">
                {showRoleSwitch ? (
                  <div className="space-y-2.5">
                    <span className="block text-xs font-medium text-[var(--home-ink-2)]">
                      Vai trò sử dụng
                    </span>
                    <ToggleGroup
                      type="single"
                      value={role}
                      onValueChange={(value) => {
                        if (value === "seller" || value === "buyer") {
                          handleSelectRole(value);
                        }
                      }}
                      aria-label="Chọn vai trò"
                      className="grid w-full grid-cols-2 gap-1 rounded-full border border-[var(--home-border)] bg-[var(--home-glass)] p-1"
                    >
                      <ToggleGroupItem
                        value="seller"
                        className="h-10 w-full rounded-full! border-0 bg-transparent text-sm font-medium text-[var(--home-ink-2)] shadow-none transition-colors hover:bg-transparent hover:text-[var(--home-ink)] data-[state=on]:bg-[var(--home-ink)] data-[state=on]:font-[550] data-[state=on]:text-[var(--home-bg)] motion-reduce:transition-none"
                      >
                        {ROLE_STAGE.seller.label}
                      </ToggleGroupItem>
                      <ToggleGroupItem
                        value="buyer"
                        className="h-10 w-full rounded-full! border-0 bg-transparent text-sm font-medium text-[var(--home-ink-2)] shadow-none transition-colors hover:bg-transparent hover:text-[var(--home-ink)] data-[state=on]:bg-[var(--home-ink)] data-[state=on]:font-[550] data-[state=on]:text-[var(--home-bg)] motion-reduce:transition-none"
                      >
                        {ROLE_STAGE.buyer.label}
                      </ToggleGroupItem>
                    </ToggleGroup>

                    {role === "buyer" ? (
                      <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-[var(--home-border)] bg-[var(--home-glass)] px-4 py-3 text-xs text-[var(--home-ink-2)]">
                        <span>Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng.</span>
                        <Link
                          href="/cua-hang"
                          className="inline-flex items-center gap-1 font-medium text-[var(--home-accent)] hover:underline"
                        >
                          Vào danh bạ gian hàng
                          <ArrowRight className="size-3.5" aria-hidden="true" />
                        </Link>
                      </div>
                    ) : null}
                  </div>
                ) : null}

                <div
                  data-site-theme-mode="dark"
                  style={DARK_AUTH_FORM_VARS}
                  className={DARK_AUTH_FORM_SLOT_CLS}
                >
                  {children}
                </div>
              </div>
            </div>

            <div className="mx-auto mt-8 flex w-full max-w-[440px] flex-wrap items-center justify-between gap-2 border-t border-[var(--home-border)] pt-5 text-xs text-[var(--home-ink-3)]">
              <Link
                href="/cua-hang"
                className="transition-colors hover:text-[var(--home-ink)] hover:underline motion-reduce:transition-none"
              >
                Danh bạ gian hàng
              </Link>
              <div className="flex items-center gap-4">
                <Link
                  href="/tro-giup"
                  className="transition-colors hover:text-[var(--home-ink)] hover:underline motion-reduce:transition-none"
                >
                  Trợ giúp
                </Link>
                <Link
                  href="/ve-nexusmart"
                  className="transition-colors hover:text-[var(--home-ink)] hover:underline motion-reduce:transition-none"
                >
                  Về NexusMart
                </Link>
              </div>
            </div>
          </div>

          {/* Right column: Editorial photography stage switching by role */}
          <div className="relative hidden min-h-[560px] flex-col justify-end overflow-hidden rounded-[24px] border border-[var(--home-border)] bg-[var(--home-card)] shadow-[var(--home-shadow-card)] lg:flex">
            <Image
              src={stage.imageSrc}
              alt={stage.imageAlt}
              fill
              priority
              sizes="(min-width: 1024px) 48vw, 100vw"
              className="object-cover object-center"
            />
            <div
              className="pointer-events-none absolute inset-0 [background:linear-gradient(180deg,rgba(2,9,10,0.1)_0%,rgba(2,9,10,0.28)_48%,rgba(2,9,10,0.92)_100%)]"
              aria-hidden="true"
            />
            <div className="relative z-10 p-8 xl:p-10">
              <p className="max-w-[36ch] border-l-2 border-[var(--home-accent)] pl-5 text-[length:var(--home-fs-lead)] leading-[1.48] font-normal text-[var(--home-ink)]">
                {stage.caption}
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export interface PlatformRegisterFrameProps {
  initialRole?: AuthRoleStage;
  suggestedShopName?: string;
  rootDomain?: string;
}

export function PlatformRegisterFrame({
  initialRole = "seller",
  suggestedShopName = "",
  rootDomain = "",
}: PlatformRegisterFrameProps) {
  const [selectedRole, setSelectedRole] = useState<AuthRoleStage>(initialRole);
  const [step, setStep] = useState<"register" | "otp">("register");

  const handleStepChange = useCallback((nextStep: "register" | "otp") => {
    setStep(nextStep);
  }, []);

  const afterRegisterHref = resolveAfterRegisterHref(selectedRole);

  return (
    <PlatformAuthFrame
      mode="register"
      title={selectedRole === "seller" ? "Mở gian hàng" : "Tạo tài khoản"}
      subtitle={step === "otp" ? "Nhập mã xác thực 6 chữ số đã gửi về email của bạn." : undefined}
      initialRole={initialRole}
      suggestedShopName={suggestedShopName}
      rootDomain={rootDomain}
      showRoleSwitch={step === "register"}
      onRoleChange={setSelectedRole}
    >
      <RegisterForm
        variant="platform"
        buyerHref="/cua-hang"
        loginHref="/dang-nhap"
        afterRegisterHref={afterRegisterHref}
        onStepChange={handleStepChange}
      />
    </PlatformAuthFrame>
  );
}
