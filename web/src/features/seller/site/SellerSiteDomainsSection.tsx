"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  Check,
  CheckCircle2,
  ChevronDown,
  Copy,
  ExternalLink,
  Globe,
  RefreshCw,
} from "lucide-react";
import { StatusBadge, siteStatusTone, type StatusTone } from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import {
  formatDomainErrorForSeller,
  getDomainStatusMeta,
  validateCustomHostname,
  type DomainVerificationRecord,
  type SiteDomainDTO,
} from "@/lib/site/domains";
import { useSellerContext } from "@/features/seller/SellerShell";
import type { GwSite } from "./siteEditorTypes";
import { getSiteStatusMeta } from "./siteEditorUtils";
import { buildPublicSiteUrl } from "./siteSummary";

export interface SubdomainEditorBinding {
  value: string;
  cleanSub: string;
  error: string | null;
  onChange: (nextValue: string) => void;
  dirty?: boolean;
  saving?: boolean;
  onSaveDraft?: () => void;
}

export interface SellerSiteDomainsSectionProps {
  site: GwSite | null;
  initialDomains: SiteDomainDTO[];
  rootDomain: string;
  requestHost: string;
  requestProto: string;
  loadError?: string | null;
  onDomainsChange?: (domains: SiteDomainDTO[]) => void;
  onSwitchToTheme?: () => void;
  subdomainEditor?: SubdomainEditorBinding;
}

async function copyTextToClipboard(text: string): Promise<boolean> {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fallback below when clipboard permission is unavailable
    }
  }
  if (typeof document === "undefined") return false;
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  document.body.appendChild(textarea);
  textarea.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }
  document.body.removeChild(textarea);
  return copied;
}

function domainStatusToBadgeTone(status: string | null | undefined): StatusTone {
  switch ((status ?? "").trim().toUpperCase()) {
    case "ACTIVE":
      return "success";
    case "PENDING":
      return "attention";
    case "FAILED":
      return "critical";
    case "REMOVING":
    default:
      return "neutral";
  }
}

export function SellerSiteDomainsSection({
  site,
  initialDomains,
  rootDomain,
  requestHost,
  requestProto,
  loadError = null,
  onDomainsChange,
  onSwitchToTheme,
  subdomainEditor,
}: SellerSiteDomainsSectionProps) {
  const router = useRouter();
  const { showToast } = useSellerContext();

  const [domains, setDomains] = useState<SiteDomainDTO[]>(initialDomains);
  const [domainsError, setDomainsError] = useState<string | null>(loadError);
  const [reloading, setReloading] = useState<boolean>(false);

  const [hostnameInput, setHostnameInput] = useState<string>("");
  const [touched, setTouched] = useState<boolean>(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [adding, setAdding] = useState<boolean>(false);

  const [checking, setChecking] = useState<boolean>(false);
  const [confirmRemoveOpen, setConfirmRemoveOpen] = useState<boolean>(false);
  const [removing, setRemoving] = useState<boolean>(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [dnsOpen, setDnsOpen] = useState<boolean>(false);

  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const copyTimerRef = useRef<number | null>(null);

  useEffect(() => {
    setDomains(initialDomains);
  }, [initialDomains]);

  useEffect(() => {
    setDomainsError(loadError);
  }, [loadError]);

  useEffect(() => {
    return () => {
      if (copyTimerRef.current !== null) {
        window.clearTimeout(copyTimerRef.current);
      }
    };
  }, []);

  const customDomain = useMemo(() => domains.find((d) => d.kind === "CUSTOM") ?? null, [domains]);
  const subdomainDomain = useMemo(
    () => domains.find((d) => d.kind === "SUBDOMAIN") ?? null,
    [domains]
  );

  const defaultHost = site?.subdomain
    ? `${site.subdomain}.${rootDomain}`
    : (subdomainDomain?.host ?? "");

  const isPublished = (site?.status ?? "").toUpperCase() === "PUBLISHED";
  const isSuspended = (site?.status ?? "").toUpperCase() === "SUSPENDED";
  const siteStatusMeta = getSiteStatusMeta(site?.status);

  const isCustomPrimary = Boolean(
    customDomain && customDomain.status === "ACTIVE" && customDomain.is_primary
  );

  const validation = useMemo(
    () => validateCustomHostname(hostnameInput, rootDomain),
    [hostnameInput, rootDomain]
  );

  const inlineValidationError = touched && !validation.ok ? validation.message : null;
  const visibleInputError = submitError || inlineValidationError;

  const updateDomains = useCallback(
    (next: SiteDomainDTO[]) => {
      setDomains(next);
      onDomainsChange?.(next);
    },
    [onDomainsChange]
  );

  const handleCopy = useCallback(
    async (key: string, value: string) => {
      const ok = await copyTextToClipboard(value);
      if (!ok) {
        showToast("Không thể sao chép tự động. Bạn vui lòng bôi đen để sao chép.");
        return;
      }
      if (copyTimerRef.current !== null) {
        window.clearTimeout(copyTimerRef.current);
      }
      setCopiedKey(key);
      showToast("Đã sao chép vào bộ nhớ tạm.");
      copyTimerRef.current = window.setTimeout(() => {
        setCopiedKey((current) => (current === key ? null : current));
        copyTimerRef.current = null;
      }, 2000);
    },
    [showToast]
  );

  const handleReloadDomains = async () => {
    setReloading(true);
    setDomainsError(null);
    try {
      const res = await api<{ domains?: SiteDomainDTO[] }>("/api/seller/site/domains");
      updateDomains(res.domains ?? []);
    } catch (err) {
      setDomainsError(
        err instanceof ApiError
          ? err.message
          : "Không thể tải danh sách tên miền lúc này. Vui lòng thử lại."
      );
    } finally {
      setReloading(false);
    }
  };

  const handleAddDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!site || isSuspended || adding) return;
    setTouched(true);
    setSubmitError(null);
    if (!validation.ok) return;

    setAdding(true);
    try {
      const res = await api<{ domain: SiteDomainDTO }>("/api/seller/site/domains", {
        method: "POST",
        body: { hostname: validation.hostname },
      });
      if (res.domain) {
        const next = [
          ...domains.filter((d) => d.kind !== "CUSTOM" && d.id !== res.domain.id),
          res.domain,
        ];
        updateDomains(next);
      }
      setHostnameInput("");
      setTouched(false);
      setSubmitError(null);
      setConfirmRemoveOpen(false);
      showToast(
        "Đã thêm tên miền riêng. Hãy khai báo các bản ghi DNS bên dưới rồi bấm Kiểm tra lại."
      );
      if (!onDomainsChange) {
        router.refresh();
      }
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể thêm tên miền riêng lúc này. Vui lòng thử lại.";
      setSubmitError(msg);
      showToast(msg);
    } finally {
      setAdding(false);
    }
  };

  const handleCheckDomain = async () => {
    if (!customDomain || checking || removing || customDomain.status === "REMOVING") return;
    setChecking(true);
    setActionError(null);
    try {
      const res = await api<{ domain: SiteDomainDTO }>(
        `/api/seller/site/domains/${encodeURIComponent(customDomain.id)}/check`,
        { method: "POST" }
      );
      const updated = res.domain;
      if (updated) {
        const next = domains.map((item) => {
          if (item.id === updated.id) return updated;
          if (item.kind === "SUBDOMAIN" && updated.is_primary) {
            return { ...item, is_primary: false };
          }
          return item;
        });
        updateDomains(next);

        if (updated.status === "ACTIVE") {
          showToast("Tên miền đã hoạt động và trở thành địa chỉ chính của cửa hàng.");
        } else if (updated.status === "PENDING") {
          showToast("Chưa thấy bản ghi DNS mới. Vui lòng đợi vài phút rồi kiểm tra lại.");
        } else if (updated.status === "FAILED") {
          showToast("Xác minh chưa thành công. Hãy kiểm tra lại bản ghi DNS bên dưới.");
        } else {
          showToast("Đã cập nhật trạng thái tên miền.");
        }
      }
      if (!onDomainsChange) {
        router.refresh();
      }
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể kiểm tra tên miền lúc này. Vui lòng thử lại.";
      setActionError(msg);
      showToast(msg);
    } finally {
      setChecking(false);
    }
  };

  const handleConfirmRemove = async () => {
    if (!customDomain || removing) return;
    setRemoving(true);
    setActionError(null);
    try {
      await api<void>(`/api/seller/site/domains/${encodeURIComponent(customDomain.id)}`, {
        method: "DELETE",
      });
      const next = domains
        .filter((item) => item.id !== customDomain.id)
        .map((item) => (item.kind === "SUBDOMAIN" ? { ...item, is_primary: true } : item));
      updateDomains(next);
      setConfirmRemoveOpen(false);
      showToast("Đã gỡ tên miền riêng. Cửa hàng đã quay lại dùng địa chỉ mặc định.");
      if (!onDomainsChange) {
        router.refresh();
      }
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể gỡ tên miền riêng lúc này. Vui lòng thử lại.";
      setActionError(msg);
      showToast(msg);
    } finally {
      setRemoving(false);
    }
  };

  const renderDnsRecords = (records: DomainVerificationRecord[]) => (
    <div className="space-y-3">
      <p className="text-[13px] leading-[18px] text-muted-foreground">
        Đăng nhập vào trang quản trị nơi bạn đã mua tên miền, mở mục quản lý DNS và thêm đầy đủ các
        bản ghi bên dưới. Sau khi lưu, quay lại đây bấm Kiểm tra lại.
      </p>

      {/* Desktop DNS Table */}
      <div className="hidden overflow-hidden rounded-lg border border-border md:block">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-20">Loại</TableHead>
              <TableHead>Tên bản ghi</TableHead>
              <TableHead>Giá trị</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {records.map((rec, idx) => {
              const nameKey = `dns-name-${idx}`;
              const valueKey = `dns-val-${idx}`;
              const nameCopied = copiedKey === nameKey;
              const valueCopied = copiedKey === valueKey;
              return (
                <TableRow key={`${rec.type}-${rec.name}-${idx}`}>
                  <TableCell className="align-top">
                    <Badge variant="neutral" className="rounded-sm font-mono">
                      {rec.type}
                    </Badge>
                  </TableCell>
                  <TableCell className="align-top whitespace-normal">
                    <div className="flex items-start justify-between gap-2">
                      <code className="min-w-0 font-mono text-xs break-all text-foreground">
                        {rec.name}
                      </code>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        onClick={() => void handleCopy(nameKey, rec.name)}
                        aria-label={`Sao chép tên bản ghi ${rec.type}`}
                        className="shrink-0"
                      >
                        {nameCopied ? (
                          <Check className="size-3 text-badge-success-ink" aria-hidden="true" />
                        ) : (
                          <Copy className="size-3" aria-hidden="true" />
                        )}
                        <span>{nameCopied ? "Đã chép" : "Sao chép"}</span>
                      </Button>
                    </div>
                  </TableCell>
                  <TableCell className="align-top whitespace-normal">
                    <div className="flex items-start justify-between gap-2">
                      <code className="min-w-0 font-mono text-xs break-all text-foreground">
                        {rec.value}
                      </code>
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        onClick={() => void handleCopy(valueKey, rec.value)}
                        aria-label={`Sao chép giá trị bản ghi ${rec.type}`}
                        className="shrink-0"
                      >
                        {valueCopied ? (
                          <Check className="size-3 text-badge-success-ink" aria-hidden="true" />
                        ) : (
                          <Copy className="size-3" aria-hidden="true" />
                        )}
                        <span>{valueCopied ? "Đã chép" : "Sao chép"}</span>
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      {/* Mobile DNS Cards (< 768px) */}
      <div className="space-y-2.5 md:hidden">
        {records.map((rec, idx) => {
          const nameKey = `dns-name-${idx}`;
          const valueKey = `dns-val-${idx}`;
          const nameCopied = copiedKey === nameKey;
          const valueCopied = copiedKey === valueKey;
          return (
            <div
              key={`mobile-${rec.type}-${rec.name}-${idx}`}
              className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3"
            >
              <div>
                <Badge variant="neutral" className="rounded-sm font-mono">
                  {rec.type}
                </Badge>
              </div>
              <div className="space-y-1">
                <span className="text-xs font-medium text-muted-foreground">Tên bản ghi</span>
                <div className="flex items-start justify-between gap-2">
                  <code className="min-w-0 font-mono text-xs break-all text-foreground">
                    {rec.name}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleCopy(nameKey, rec.name)}
                    aria-label={`Sao chép tên bản ghi ${rec.type}`}
                    className="min-h-10 shrink-0"
                  >
                    {nameCopied ? (
                      <Check className="size-3.5 text-badge-success-ink" aria-hidden="true" />
                    ) : (
                      <Copy className="size-3.5" aria-hidden="true" />
                    )}
                    <span>{nameCopied ? "Đã chép" : "Sao chép"}</span>
                  </Button>
                </div>
              </div>
              <div className="space-y-1">
                <span className="text-xs font-medium text-muted-foreground">Giá trị</span>
                <div className="flex items-start justify-between gap-2">
                  <code className="min-w-0 font-mono text-xs break-all text-foreground">
                    {rec.value}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleCopy(valueKey, rec.value)}
                    aria-label={`Sao chép giá trị bản ghi ${rec.type}`}
                    className="min-h-10 shrink-0"
                  >
                    {valueCopied ? (
                      <Check className="size-3.5 text-badge-success-ink" aria-hidden="true" />
                    ) : (
                      <Copy className="size-3.5" aria-hidden="true" />
                    )}
                    <span>{valueCopied ? "Đã chép" : "Sao chép"}</span>
                  </Button>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );

  const defaultPublicUrl =
    isPublished && defaultHost ? buildPublicSiteUrl(defaultHost, requestHost, requestProto) : "";
  const customPublicUrl =
    isPublished && customDomain?.host
      ? buildPublicSiteUrl(customDomain.host, requestHost, requestProto)
      : "";

  const customStatusMeta = customDomain ? getDomainStatusMeta(customDomain.status) : null;
  const customErrorGuidance =
    customDomain &&
    customDomain.status !== "ACTIVE" &&
    (customDomain.status === "FAILED" || Boolean(customDomain.last_error))
      ? formatDomainErrorForSeller(customDomain.last_error || "verify")
      : "";

  return (
    <div className="mx-auto max-w-[800px] space-y-6">
      {/* A5 Section 1: Địa chỉ mặc định & Tên miền phụ */}
      <section
        aria-label="Địa chỉ mặc định của cửa hàng"
        className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
      >
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-foreground">Địa chỉ mặc định</h2>
          <p className="text-[13px] leading-[18px] text-muted-foreground">
            Tên miền phụ miễn phí dưới *.{rootDomain}. Khách có thể truy cập trực tiếp ngay sau khi
            site xuất bản.
          </p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Tên miền phụ của gian hàng</CardTitle>
            <CardAction>
              <StatusBadge tone={siteStatusTone(site?.status)}>{siteStatusMeta.label}</StatusBadge>
            </CardAction>
          </CardHeader>

          <CardContent className="space-y-4">
            {subdomainEditor ? (
              <Field className="gap-1.5">
                <FieldLabel htmlFor="site-subdomain">Tên miền phụ</FieldLabel>
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    id="site-subdomain"
                    type="text"
                    value={subdomainEditor.value}
                    maxLength={40}
                    onChange={(e) => subdomainEditor.onChange(e.target.value.toLowerCase())}
                    placeholder="bach-dang"
                    aria-invalid={Boolean(subdomainEditor.error)}
                    className="min-w-0 flex-1 bg-card"
                  />
                  {subdomainEditor.dirty && subdomainEditor.onSaveDraft ? (
                    <Button
                      type="button"
                      size="sm"
                      disabled={subdomainEditor.saving || Boolean(subdomainEditor.error)}
                      onClick={subdomainEditor.onSaveDraft}
                    >
                      {subdomainEditor.saving ? "Đang lưu..." : "Lưu nháp"}
                    </Button>
                  ) : null}
                </div>
                {subdomainEditor.error ? (
                  <FieldError>{subdomainEditor.error}</FieldError>
                ) : (
                  <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2 text-[13px]">
                    <span className="font-semibold break-all text-foreground">
                      {subdomainEditor.cleanSub}
                      <span className="font-normal text-muted-foreground">.{rootDomain}</span>
                    </span>
                    <StatusBadge tone="success">Hợp lệ</StatusBadge>
                  </div>
                )}
              </Field>
            ) : null}

            {site && defaultHost ? (
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-muted/35 p-3">
                <div className="min-w-0 space-y-1.5">
                  <div className="flex items-center gap-2">
                    <Globe className="size-4 shrink-0 text-brand" aria-hidden="true" />
                    <span className="text-sm font-semibold break-words [overflow-wrap:anywhere] text-foreground">
                      {defaultHost}
                    </span>
                  </div>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {isCustomPrimary ? (
                      <Badge variant="neutral">Tự động chuyển sang tên miền riêng</Badge>
                    ) : (
                      <Badge variant="info">Địa chỉ chính</Badge>
                    )}
                  </div>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleCopy("default-host", defaultHost)}
                    aria-label="Sao chép địa chỉ mặc định"
                    className="min-h-10 md:min-h-8"
                  >
                    {copiedKey === "default-host" ? (
                      <Check className="size-3.5 text-badge-success-ink" aria-hidden="true" />
                    ) : (
                      <Copy className="size-3.5" aria-hidden="true" />
                    )}
                    <span>{copiedKey === "default-host" ? "Đã chép" : "Sao chép"}</span>
                  </Button>

                  {defaultPublicUrl ? (
                    <Button variant="outline" size="sm" asChild className="min-h-10 md:min-h-8">
                      <a href={defaultPublicUrl} target="_blank" rel="noopener noreferrer">
                        <span>Mở cửa hàng</span>
                        <ExternalLink className="size-3.5" aria-hidden="true" />
                      </a>
                    </Button>
                  ) : null}
                </div>
              </div>
            ) : !subdomainEditor ? (
              <p className="text-[13px] text-muted-foreground">
                Cửa hàng chưa có địa chỉ mặc định. Lưu cấu hình trong Site Studio để kích hoạt địa
                chỉ dưới *.{rootDomain}.
              </p>
            ) : null}
          </CardContent>
        </Card>
      </section>

      <Separator />

      {/* A5 Section 2: Tên miền riêng */}
      <section
        aria-label="Quản lý tên miền riêng"
        className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
      >
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-foreground">Tên miền riêng</h2>
          <p className="text-[13px] leading-[18px] text-muted-foreground">
            Kết nối tên miền thương hiệu của riêng bạn qua bản ghi DNS. Khi xác minh hoàn tất, địa
            chỉ mặc định sẽ tự động chuyển hướng về tên miền riêng.
          </p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Kết nối tên miền thương hiệu</CardTitle>
          </CardHeader>

          <CardContent className="space-y-4">
            {domainsError && (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertTitle>Không thể tải danh sách tên miền</AlertTitle>
                <AlertDescription className="w-full space-y-2">
                  <p>{domainsError}</p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={reloading}
                    onClick={() => void handleReloadDomains()}
                  >
                    {reloading ? "Đang tải..." : "Thử tải lại"}
                  </Button>
                </AlertDescription>
              </Alert>
            )}

            {!site ? (
              <div className="space-y-3 rounded-lg border border-border bg-muted/40 p-4">
                <p className="text-[13px] text-muted-foreground">
                  Bạn cần tạo và lưu cấu hình cửa hàng riêng trước khi gắn tên miền riêng.
                </p>
                {onSwitchToTheme ? (
                  <Button type="button" size="sm" onClick={onSwitchToTheme}>
                    Thiết lập giao diện cửa hàng
                  </Button>
                ) : (
                  <Button size="sm" asChild>
                    <Link href="/seller/site">Thiết lập giao diện cửa hàng</Link>
                  </Button>
                )}
              </div>
            ) : !customDomain ? (
              <form className="space-y-3" onSubmit={(e) => void handleAddDomain(e)} noValidate>
                <Field className="gap-1.5">
                  <FieldLabel htmlFor="custom-domain-input">Nhập tên miền riêng</FieldLabel>
                  <div className="flex flex-col gap-2 lg:flex-row">
                    <Input
                      id="custom-domain-input"
                      type="text"
                      value={hostnameInput}
                      onChange={(e) => {
                        setHostnameInput(e.target.value);
                        if (!touched) setTouched(true);
                        if (submitError) setSubmitError(null);
                      }}
                      placeholder="shop.thuonghieu.vn"
                      autoComplete="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      disabled={adding || isSuspended}
                      aria-invalid={Boolean(visibleInputError)}
                      aria-describedby="custom-domain-feedback"
                      className="min-w-0 flex-1 bg-card"
                    />
                    <Button
                      type="submit"
                      size="default"
                      disabled={adding || isSuspended}
                      className="min-h-10 shrink-0 md:min-h-9"
                    >
                      {adding ? "Đang thêm..." : "Thêm tên miền"}
                    </Button>
                  </div>

                  {visibleInputError ? (
                    <FieldError id="custom-domain-feedback">{visibleInputError}</FieldError>
                  ) : (
                    <FieldDescription id="custom-domain-feedback" className="text-xs">
                      {isSuspended
                        ? "Site riêng đang tạm khóa nên tạm thời không thể thêm tên miền mới."
                        : "Chỉ nhập tên miền (ví dụ: shop.thuonghieu.vn), không kèm https:// hoặc dấu gạch chéo."}
                    </FieldDescription>
                  )}
                </Field>
              </form>
            ) : (
              <div className="space-y-4">
                <div className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-border bg-muted/35 p-3.5">
                  <div className="min-w-0 space-y-1.5">
                    <div className="flex items-center gap-2">
                      <Globe className="size-4 shrink-0 text-brand" aria-hidden="true" />
                      <span className="text-sm font-semibold break-words [overflow-wrap:anywhere] text-foreground">
                        {customDomain.host}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {customStatusMeta ? (
                        <StatusBadge tone={domainStatusToBadgeTone(customDomain.status)}>
                          {customStatusMeta.label}
                        </StatusBadge>
                      ) : null}
                      {customDomain.is_primary ? <Badge variant="info">Địa chỉ chính</Badge> : null}
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={checking || removing || customDomain.status === "REMOVING"}
                      onClick={() => void handleCheckDomain()}
                      className="min-h-10 md:min-h-8"
                    >
                      <RefreshCw className="size-3.5" aria-hidden="true" />
                      <span>{checking ? "Đang kiểm tra..." : "Kiểm tra lại"}</span>
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="min-h-10 text-destructive hover:text-destructive md:min-h-8"
                      disabled={checking || removing}
                      onClick={() => {
                        setActionError(null);
                        setConfirmRemoveOpen(true);
                      }}
                    >
                      Gỡ tên miền
                    </Button>
                  </div>
                </div>

                {actionError && (
                  <Alert variant="destructive">
                    <AlertCircle className="size-4" aria-hidden="true" />
                    <AlertDescription>{actionError}</AlertDescription>
                  </Alert>
                )}

                {customDomain.status === "ACTIVE" && (
                  <div
                    role="status"
                    className="space-y-2 rounded-lg border border-badge-success-ink/20 bg-badge-success-bg/55 p-3.5 text-[13px] text-badge-success-ink"
                  >
                    <div className="flex items-start gap-2">
                      <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                      <span>
                        Tên miền riêng đã hoạt động và trở thành địa chỉ chính của cửa hàng. Khách
                        vào địa chỉ mặc định sẽ tự động được chuyển sang tên miền này.
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-3 pl-6">
                      {customPublicUrl ? (
                        <a
                          href={customPublicUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 font-semibold underline underline-offset-2"
                        >
                          <span>Mở cửa hàng bằng tên miền riêng</span>
                          <ExternalLink className="size-3.5" aria-hidden="true" />
                        </a>
                      ) : null}
                      {customDomain.verified_at ? (
                        <span className="text-xs opacity-85">
                          Xác minh lúc {formatDateTimeVi(customDomain.verified_at)}
                        </span>
                      ) : null}
                    </div>
                  </div>
                )}

                {customErrorGuidance && (
                  <Alert variant="destructive">
                    <AlertCircle className="size-4" aria-hidden="true" />
                    <AlertDescription>{customErrorGuidance}</AlertDescription>
                  </Alert>
                )}

                {customDomain.status === "REMOVING" && (
                  <Alert>
                    <AlertDescription>
                      Hệ thống đang hoàn tất quá trình gỡ bỏ tên miền này. Bạn vui lòng tải lại sau
                      ít phút.
                    </AlertDescription>
                  </Alert>
                )}

                {customDomain.verification.length > 0 &&
                  (customDomain.status === "ACTIVE" ? (
                    <Collapsible open={dnsOpen} onOpenChange={setDnsOpen}>
                      <CollapsibleTrigger asChild>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="w-full justify-between border border-border bg-muted/35"
                        >
                          <span>
                            Xem lại bản ghi DNS đã khai báo ({customDomain.verification.length})
                          </span>
                          <ChevronDown
                            className={`size-4 transition-transform ${dnsOpen ? "rotate-180" : ""}`}
                            aria-hidden="true"
                          />
                        </Button>
                      </CollapsibleTrigger>
                      <CollapsibleContent className="pt-3">
                        {renderDnsRecords(customDomain.verification)}
                      </CollapsibleContent>
                    </Collapsible>
                  ) : (
                    <div className="space-y-2.5">
                      <h3 className="text-sm font-semibold text-foreground">
                        Bảng bản ghi DNS cần khai báo
                      </h3>
                      {renderDnsRecords(customDomain.verification)}
                    </div>
                  ))}

                <p className="text-xs text-muted-foreground">
                  Mỗi cửa hàng gắn tối đa 1 tên miền riêng. Để đổi sang tên miền khác, hãy gỡ tên
                  miền hiện tại trước.
                </p>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* Confirm Remove Custom Domain AlertDialog */}
      <AlertDialog
        open={confirmRemoveOpen && customDomain !== null}
        onOpenChange={(open) => {
          if (!open && !removing) setConfirmRemoveOpen(false);
        }}
      >
        <AlertDialogContent>
          {customDomain ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Xác nhận gỡ tên miền riêng?</AlertDialogTitle>
                <AlertDialogDescription>
                  Gỡ tên miền <strong>{customDomain.host}</strong>? Cửa hàng sẽ quay lại dùng địa
                  chỉ mặc định <strong>{defaultHost}</strong>.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={removing}>Hủy</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  disabled={removing}
                  onClick={(e) => {
                    e.preventDefault();
                    void handleConfirmRemove();
                  }}
                >
                  {removing ? "Đang gỡ..." : "Xác nhận gỡ"}
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
