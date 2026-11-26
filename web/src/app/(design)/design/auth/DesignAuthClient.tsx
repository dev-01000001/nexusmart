"use client";

import * as React from "react";
import Link from "next/link";
import { Eye, EyeOff, Globe } from "lucide-react";
import { toast } from "sonner";
import { NexusMark } from "@/components/Icons";
import { AuthSplit } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

function calcPasswordStrength(pw: string): { score: number; label: string } {
  if (!pw) return { score: 0, label: "Chưa nhập" };
  let points = 0;
  if (pw.length >= 8) points += 25;
  if (pw.length >= 12) points += 15;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) points += 20;
  if (/\d/.test(pw)) points += 20;
  if (/[^a-zA-Z0-9]/.test(pw)) points += 20;
  if (points >= 80) return { score: 100, label: "Mạnh" };
  if (points >= 55) return { score: 65, label: "Tốt" };
  if (points >= 35) return { score: 40, label: "Trung bình" };
  return { score: 20, label: "Yếu" };
}

export function DesignAuthClient({ variant }: { variant: "standalone" | "embedded" }) {
  const [role, setRole] = React.useState<"seller" | "buyer">("seller");
  const [password, setPassword] = React.useState("NexusMart@2026");
  const [confirmPassword, setConfirmPassword] = React.useState("NexusMart@2026");
  const [showPassword, setShowPassword] = React.useState(false);

  const strength = calcPasswordStrength(password);

  const formContent = (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        toast("Biểu mẫu mẫu — không gửi dữ liệu đi đâu.");
      }}
      className="space-y-4"
    >
      <div className="space-y-2">
        <span className="block text-xs font-medium text-muted-foreground">Loại tài khoản</span>
        <ToggleGroup
          type="single"
          variant="outline"
          value={role}
          onValueChange={(v) => {
            if (v === "seller" || v === "buyer") setRole(v);
          }}
          className="grid w-full grid-cols-2"
        >
          <ToggleGroupItem value="seller" className="w-full">
            Người bán
          </ToggleGroupItem>
          <ToggleGroupItem value="buyer" className="w-full">
            Khách mua
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {role === "seller" ? (
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="info" className="gap-1.5">
            <Globe className="size-3" aria-hidden="true" />
            <span>Gợi ý tên miền phụ: aodep-saigon.nexusmart.vn (mẫu)</span>
          </Badge>
        </div>
      ) : null}

      <FieldGroup className="gap-3.5">
        <Field>
          <FieldLabel htmlFor="auth-fullname">Họ và tên</FieldLabel>
          <Input id="auth-fullname" defaultValue="Nguyễn Văn An (mẫu)" />
        </Field>
        <Field>
          <FieldLabel htmlFor="auth-email">Email</FieldLabel>
          <Input id="auth-email" type="email" defaultValue="an.nguyen@aodep.vn" />
        </Field>
        <Field>
          <FieldLabel htmlFor="auth-phone">Số điện thoại</FieldLabel>
          <Input id="auth-phone" type="tel" defaultValue="0901234567" />
        </Field>
        <Field>
          <FieldLabel htmlFor="auth-pw">Mật khẩu</FieldLabel>
          <div className="relative">
            <Input
              id="auth-pw"
              type={showPassword ? "text" : "password"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="pr-10"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => setShowPassword((v) => !v)}
              aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
              className="absolute top-0.5 right-1"
            >
              {showPassword ? (
                <EyeOff className="size-4" aria-hidden="true" />
              ) : (
                <Eye className="size-4" aria-hidden="true" />
              )}
            </Button>
          </div>
          <div className="mt-1.5 space-y-1">
            <Progress value={strength.score} className="h-1.5" aria-label="Độ mạnh mật khẩu" />
            <div className="text-xs text-muted-foreground">
              Độ mạnh mật khẩu:{" "}
              <span className="font-medium text-foreground">{strength.label}</span>
            </div>
          </div>
        </Field>
        <Field>
          <FieldLabel htmlFor="auth-pw-confirm">Nhập lại mật khẩu</FieldLabel>
          <Input
            id="auth-pw-confirm"
            type={showPassword ? "text" : "password"}
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
          />
        </Field>
      </FieldGroup>

      <Button type="submit" className="w-full">
        Tạo tài khoản (mẫu)
      </Button>
    </form>
  );

  return (
    <div className="flex min-h-svh flex-col">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-card px-4 py-2.5 text-xs">
        <div className="flex items-center gap-2">
          <Link href="/design" className="font-medium text-brand hover:underline">
            ← Về mục lục /design
          </Link>
          <span className="text-muted-foreground">·</span>
          <span className="font-semibold text-foreground">AuthSplit (mẫu)</span>
        </div>
        <div className="flex items-center gap-1.5">
          <Button size="xs" variant={variant === "standalone" ? "default" : "outline"} asChild>
            <Link href="/design/auth?bien-the=standalone">Standalone</Link>
          </Button>
          <Button size="xs" variant={variant === "embedded" ? "default" : "outline"} asChild>
            <Link href="/design/auth?bien-the=embedded">Embedded</Link>
          </Button>
        </div>
      </div>

      <div className={variant === "embedded" ? "flex-1 bg-background p-4 md:p-8" : "flex-1"}>
        <AuthSplit
          variant={variant}
          brand={{
            name: "NexusMart",
            href: "/design",
            logo: <NexusMark size={20} accentColor="var(--ui-brand)" />,
          }}
          title="Tạo tài khoản NexusMart (mẫu)"
          subtitle="Mở cửa hàng mang tên riêng miễn phí, tồn kho cập nhật theo thời gian thực."
          aside={{
            imageSrc: "/banners/vietnam-workshop.jpg",
            caption:
              "Cửa hàng mang tên bạn, tồn kho đồng bộ theo từng SKU, thanh toán COD và VietQR minh bạch.",
          }}
          footer={
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span>
                Đã có tài khoản?{" "}
                <Link href="/design/auth" className="font-medium text-brand hover:underline">
                  Đăng nhập →
                </Link>
              </span>
              <div className="flex items-center gap-3">
                <Link href="/design" className="hover:text-foreground">
                  Trợ giúp
                </Link>
                <Link href="/design" className="hover:text-foreground">
                  Chính sách
                </Link>
              </div>
            </div>
          }
        >
          {formContent}
        </AuthSplit>
      </div>
    </div>
  );
}
