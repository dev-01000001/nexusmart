"use client";

import React, { useCallback, useState } from "react";
import Link from "next/link";
import { ArrowRight, Globe } from "lucide-react";
import { NexusMark } from "@/components/Icons";
import { AuthSplit } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { RegisterForm } from "@/features/account/AuthForms";
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
    caption:
      "Cửa hàng mang tên bạn, tồn kho đồng bộ theo từng SKU, thanh toán COD và VietQR minh bạch.",
    loginSubtitle: "Vào Kênh người bán hoặc Cổng quản trị trên nền tảng NexusMart.",
    registerSubtitle: "Mở cửa hàng mang tên riêng miễn phí, tồn kho cập nhật theo thời gian thực.",
  },
  buyer: {
    label: "Khách mua",
    imageSrc: MEDIA_BANNERS.everyday.src,
    caption: "Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng theo từng thương hiệu bạn chọn mua.",
    loginSubtitle: "Khách mua hàng chọn cửa hàng trong danh bạ để mua sắm và theo dõi đơn.",
    registerSubtitle:
      "Tạo tài khoản NexusMart để theo dõi đơn hàng tại các cửa hàng bạn yêu thích.",
  },
} as const;

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

  const subtitleNode = (
    <div className="space-y-2.5">
      {resolvedSubtitle ? <p>{resolvedSubtitle}</p> : null}
      {suggestedShopName && role === "seller" ? (
        <div className="flex flex-wrap items-center gap-2" role="status">
          <Badge variant="info" className="max-w-full gap-1.5 py-1 text-xs">
            <Globe className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="truncate">
              {suggestedShopName}
              {suggestedSlug && rootDomain ? ` · ${suggestedSlug}.${rootDomain}` : ""}
            </span>
          </Badge>
        </div>
      ) : null}
    </div>
  );

  return (
    <AuthSplit
      variant="standalone"
      brand={{
        name: "NexusMart",
        href: "/",
        logo: <NexusMark size={20} accentColor="var(--ui-brand)" />,
      }}
      title={displayTitle}
      subtitle={subtitleNode}
      aside={{
        imageSrc: stage.imageSrc,
        caption: stage.caption,
      }}
      footer={
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Link href="/#danh-ba-gian-hang" className="hover:text-foreground hover:underline">
            Danh bạ gian hàng
          </Link>
          <div className="flex items-center gap-3">
            <Link href="/tro-giup" className="hover:text-foreground hover:underline">
              Trợ giúp
            </Link>
            <Link href="/ve-nexusmart" className="hover:text-foreground hover:underline">
              Về NexusMart
            </Link>
          </div>
        </div>
      }
    >
      <div className="space-y-5">
        {showRoleSwitch ? (
          <div className="space-y-2">
            <span className="block text-xs font-medium text-muted-foreground">Vai trò sử dụng</span>
            <ToggleGroup
              type="single"
              variant="outline"
              value={role}
              onValueChange={(value) => {
                if (value === "seller" || value === "buyer") {
                  handleSelectRole(value);
                }
              }}
              aria-label="Chọn vai trò"
              className="grid w-full grid-cols-2"
            >
              <ToggleGroupItem
                value="seller"
                className="h-10 w-full font-medium data-[state=on]:border-primary data-[state=on]:bg-primary data-[state=on]:text-primary-foreground"
              >
                {ROLE_STAGE.seller.label}
              </ToggleGroupItem>
              <ToggleGroupItem
                value="buyer"
                className="h-10 w-full font-medium data-[state=on]:border-primary data-[state=on]:bg-primary data-[state=on]:text-primary-foreground"
              >
                {ROLE_STAGE.buyer.label}
              </ToggleGroupItem>
            </ToggleGroup>

            {role === "buyer" ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/60 px-3.5 py-2.5 text-xs text-muted-foreground">
                <span>Mỗi cửa hàng giữ giỏ hàng và đơn hàng riêng.</span>
                <Link
                  href="/#danh-ba-gian-hang"
                  className="inline-flex items-center gap-1 font-medium text-brand hover:underline"
                >
                  Vào danh bạ gian hàng
                  <ArrowRight className="size-3.5" aria-hidden="true" />
                </Link>
              </div>
            ) : null}
          </div>
        ) : null}

        {children}
      </div>
    </AuthSplit>
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
        buyerHref="/#danh-ba-gian-hang"
        loginHref="/dang-nhap"
        afterRegisterHref={afterRegisterHref}
        onStepChange={handleStepChange}
      />
    </PlatformAuthFrame>
  );
}
