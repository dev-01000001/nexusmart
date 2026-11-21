"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Button,
  IconArrowUpRight,
  IconCheck,
  IconCopy,
  IconGlobe,
  IconRefresh,
} from "@/components";
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
import common from "@/features/seller/SellerCommon.module.css";
import type { GwSite } from "./siteEditorTypes";
import { getSiteStatusMeta } from "./siteEditorUtils";
import { buildPublicSiteUrl } from "./siteSummary";
import styles from "./SellerSiteDomains.module.css";

export interface SellerSiteDomainsSectionProps {
  site: GwSite | null;
  initialDomains: SiteDomainDTO[];
  rootDomain: string;
  requestHost: string;
  requestProto: string;
  loadError?: string | null;
  onDomainsChange?: (domains: SiteDomainDTO[]) => void;
  onSwitchToTheme?: () => void;
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

function getToneClass(tone: "pos" | "warn" | "neg" | "neutral"): string {
  switch (tone) {
    case "pos":
      return common.pillPos;
    case "warn":
      return common.pillWarn;
    case "neg":
      return common.pillNeg;
    default:
      return "";
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
    <>
      <p className={styles.dnsHint}>
        Đăng nhập vào trang quản trị nơi bạn đã mua tên miền, mở mục quản lý DNS và thêm đầy đủ các
        bản ghi bên dưới. Sau khi lưu, quay lại đây bấm Kiểm tra lại.
      </p>

      <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
        <table className={common.dataTable}>
          <thead>
            <tr>
              <th>Loại</th>
              <th>Tên bản ghi</th>
              <th>Giá trị</th>
            </tr>
          </thead>
          <tbody>
            {records.map((rec, idx) => {
              const nameKey = `dns-name-${idx}`;
              const valueKey = `dns-val-${idx}`;
              const nameCopied = copiedKey === nameKey;
              const valueCopied = copiedKey === valueKey;
              return (
                <tr key={`${rec.type}-${rec.name}-${idx}`}>
                  <td>
                    <span className={styles.dnsTypePill}>{rec.type}</span>
                  </td>
                  <td>
                    <div className={styles.dnsValueCell}>
                      <code className={styles.dnsCode}>{rec.name}</code>
                      <button
                        type="button"
                        className={`${styles.copyBtn} ${nameCopied ? styles.copyBtnDone : ""}`}
                        onClick={() => void handleCopy(nameKey, rec.name)}
                        aria-label={`Sao chép tên bản ghi ${rec.type}`}
                      >
                        {nameCopied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                        <span>{nameCopied ? "Đã chép" : "Sao chép"}</span>
                      </button>
                    </div>
                  </td>
                  <td>
                    <div className={styles.dnsValueCell}>
                      <code className={styles.dnsCode}>{rec.value}</code>
                      <button
                        type="button"
                        className={`${styles.copyBtn} ${valueCopied ? styles.copyBtnDone : ""}`}
                        onClick={() => void handleCopy(valueKey, rec.value)}
                        aria-label={`Sao chép giá trị bản ghi ${rec.type}`}
                      >
                        {valueCopied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                        <span>{valueCopied ? "Đã chép" : "Sao chép"}</span>
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div className={styles.dnsMobileList}>
        {records.map((rec, idx) => {
          const nameKey = `dns-name-${idx}`;
          const valueKey = `dns-val-${idx}`;
          const nameCopied = copiedKey === nameKey;
          const valueCopied = copiedKey === valueKey;
          return (
            <div key={`mobile-${rec.type}-${rec.name}-${idx}`} className={styles.dnsMobileCard}>
              <div>
                <span className={styles.dnsTypePill}>{rec.type}</span>
              </div>
              <div className={styles.dnsMobileField}>
                <span className={styles.dnsMobileLabel}>Tên bản ghi</span>
                <div className={styles.dnsMobileValueRow}>
                  <code className={styles.dnsCode}>{rec.name}</code>
                  <button
                    type="button"
                    className={`${styles.copyBtn} ${nameCopied ? styles.copyBtnDone : ""}`}
                    onClick={() => void handleCopy(nameKey, rec.name)}
                    aria-label={`Sao chép tên bản ghi ${rec.type}`}
                  >
                    {nameCopied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                    <span>{nameCopied ? "Đã chép" : "Sao chép"}</span>
                  </button>
                </div>
              </div>
              <div className={styles.dnsMobileField}>
                <span className={styles.dnsMobileLabel}>Giá trị</span>
                <div className={styles.dnsMobileValueRow}>
                  <code className={styles.dnsCode}>{rec.value}</code>
                  <button
                    type="button"
                    className={`${styles.copyBtn} ${valueCopied ? styles.copyBtnDone : ""}`}
                    onClick={() => void handleCopy(valueKey, rec.value)}
                    aria-label={`Sao chép giá trị bản ghi ${rec.type}`}
                  >
                    {valueCopied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                    <span>{valueCopied ? "Đã chép" : "Sao chép"}</span>
                  </button>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </>
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
    <div className={styles.stack}>
      {/* Section A: Địa chỉ mặc định */}
      <section className={common.card} aria-label="Địa chỉ mặc định của cửa hàng">
        <div className={common.cardHeader}>
          <div>
            <h2 className={common.cardTitle}>Địa chỉ mặc định</h2>
            <p className={common.cardMeta}>
              Địa chỉ cửa hàng được NexusMart cấp sẵn theo tên miền phụ bạn đã chọn.
            </p>
          </div>
          <span className={`${common.pill} ${getToneClass(siteStatusMeta.tone)}`}>
            {siteStatusMeta.label}
          </span>
        </div>

        {site && defaultHost ? (
          <div className={styles.addressRow}>
            <div className={styles.addressMeta}>
              <div className={styles.hostIdentity}>
                <IconGlobe size={16} className={styles.hostIcon} />
                <span className={styles.hostName}>{defaultHost}</span>
              </div>
              <div className={styles.badgeRow}>
                {isCustomPrimary ? (
                  <span className={common.channelTag}>Tự động chuyển sang tên miền riêng</span>
                ) : (
                  <span className={`${common.channelTag} ${common.channelTagStorefront}`}>
                    Địa chỉ chính
                  </span>
                )}
              </div>
            </div>

            <div className={styles.actionGroup}>
              <button
                type="button"
                className={`${styles.copyBtn} ${copiedKey === "default-host" ? styles.copyBtnDone : ""}`}
                onClick={() => void handleCopy("default-host", defaultHost)}
                aria-label="Sao chép địa chỉ mặc định"
              >
                {copiedKey === "default-host" ? <IconCheck size={13} /> : <IconCopy size={13} />}
                <span>{copiedKey === "default-host" ? "Đã chép" : "Sao chép"}</span>
              </button>

              {defaultPublicUrl && (
                <a
                  href={defaultPublicUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.openLink}
                >
                  <span>Mở cửa hàng</span>
                  <IconArrowUpRight size={13} />
                </a>
              )}
            </div>
          </div>
        ) : (
          <p className={styles.introText}>
            Cửa hàng chưa có địa chỉ mặc định. Lưu cấu hình ở mục Giao diện để kích hoạt địa chỉ
            dưới *.{rootDomain}.
          </p>
        )}
      </section>

      {/* Section B: Tên miền riêng */}
      <section className={common.card} aria-label="Quản lý tên miền riêng">
        <div className={common.cardHeader}>
          <div>
            <h2 className={common.cardTitle}>Tên miền riêng</h2>
            <p className={common.cardMeta}>
              Gắn tên miền thương hiệu của riêng bạn để khách truy cập trực tiếp vào cửa hàng.
            </p>
          </div>
        </div>

        {domainsError && (
          <div className={styles.inlineErrorBar} role="alert">
            <span>{domainsError}</span>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={reloading}
              onClick={() => void handleReloadDomains()}
            >
              {reloading ? "Đang tải..." : "Thử tải lại"}
            </Button>
          </div>
        )}

        {!site ? (
          <div className={styles.setupPrompt}>
            <p className={styles.setupPromptText}>
              Bạn cần tạo và lưu cấu hình cửa hàng riêng trước khi gắn tên miền riêng.
            </p>
            {onSwitchToTheme ? (
              <Button variant="primary" size="sm" shape="app" onClick={onSwitchToTheme}>
                Thiết lập giao diện cửa hàng
              </Button>
            ) : (
              <Button variant="primary" size="sm" shape="app" href="/seller/site">
                Thiết lập giao diện cửa hàng
              </Button>
            )}
          </div>
        ) : !customDomain ? (
          <div>
            <p className={styles.introText}>
              Khách hàng có thể truy cập trực tiếp bằng tên miền của riêng bạn. Khi xác minh hoàn
              tất, mọi lượt truy cập vào địa chỉ mặc định sẽ tự động được chuyển sang tên miền
              riêng.
            </p>

            <form className={styles.addForm} onSubmit={(e) => void handleAddDomain(e)} noValidate>
              <div className={common.field}>
                <label htmlFor="custom-domain-input" className={common.fieldLabel}>
                  Nhập tên miền riêng
                </label>
                <div className={styles.addInputRow}>
                  <input
                    id="custom-domain-input"
                    type="text"
                    className={common.textInput}
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
                  />
                  <Button
                    type="submit"
                    variant="primary"
                    size="sm"
                    shape="app"
                    className={styles.addSubmitBtn}
                    disabled={adding || isSuspended}
                  >
                    {adding ? "Đang thêm..." : "Thêm tên miền"}
                  </Button>
                </div>

                {visibleInputError ? (
                  <span id="custom-domain-feedback" className={common.fieldError} role="alert">
                    {visibleInputError}
                  </span>
                ) : (
                  <span id="custom-domain-feedback" className={common.fieldHint}>
                    {isSuspended
                      ? "Site riêng đang tạm khóa nên tạm thời không thể thêm tên miền mới."
                      : "Chỉ nhập tên miền (ví dụ: shop.thuonghieu.vn), không kèm https:// hoặc dấu gạch chéo."}
                  </span>
                )}
              </div>
            </form>
          </div>
        ) : (
          <div>
            <div className={styles.customHeader}>
              <div className={styles.customIdentity}>
                <div className={styles.hostIdentity}>
                  <IconGlobe size={18} className={styles.hostIcon} />
                  <span className={styles.customHostTitle}>{customDomain.host}</span>
                </div>
                <div className={styles.badgeRow}>
                  {customStatusMeta && (
                    <span className={`${common.pill} ${getToneClass(customStatusMeta.tone)}`}>
                      {customStatusMeta.label}
                    </span>
                  )}
                  {customDomain.is_primary && (
                    <span className={`${common.channelTag} ${common.channelTagStorefront}`}>
                      Địa chỉ chính
                    </span>
                  )}
                </div>
              </div>

              <div className={styles.actionGroup}>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={checking || removing || customDomain.status === "REMOVING"}
                  onClick={() => void handleCheckDomain()}
                >
                  <IconRefresh size={14} />
                  <span>{checking ? "Đang kiểm tra..." : "Kiểm tra lại"}</span>
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  className={styles.removeTriggerBtn}
                  disabled={checking || removing}
                  onClick={() => {
                    setActionError(null);
                    setConfirmRemoveOpen((prev) => !prev);
                  }}
                >
                  Gỡ tên miền
                </Button>
              </div>
            </div>

            {confirmRemoveOpen && (
              <div
                className={styles.confirmStrip}
                role="region"
                aria-label="Xác nhận gỡ tên miền riêng"
              >
                <p className={styles.confirmText}>
                  Gỡ tên miền <span className={styles.confirmHost}>{customDomain.host}</span>? Cửa
                  hàng sẽ quay lại dùng địa chỉ mặc định{" "}
                  <span className={styles.confirmHost}>{defaultHost}</span>.
                </p>
                <div className={styles.actionGroup}>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={removing}
                    onClick={() => setConfirmRemoveOpen(false)}
                  >
                    Hủy
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    shape="app"
                    disabled={removing}
                    onClick={() => void handleConfirmRemove()}
                  >
                    {removing ? "Đang gỡ..." : "Xác nhận gỡ"}
                  </Button>
                </div>
              </div>
            )}

            {actionError && (
              <div className={styles.statusBannerNeg} role="alert">
                {actionError}
              </div>
            )}

            {customDomain.status === "ACTIVE" && (
              <div className={styles.statusBannerPos} role="status">
                <span>
                  Tên miền riêng đã hoạt động và trở thành địa chỉ chính của cửa hàng. Khách vào địa
                  chỉ mặc định sẽ tự động được chuyển sang tên miền này.
                </span>
                <div className={styles.statusBannerActions}>
                  {customPublicUrl && (
                    <a
                      href={customPublicUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={styles.statusBannerLink}
                    >
                      <span>Mở cửa hàng bằng tên miền riêng</span>
                      <IconArrowUpRight size={13} />
                    </a>
                  )}
                  {customDomain.verified_at && (
                    <span className={styles.statusBannerMeta}>
                      Xác minh lúc {formatDateTimeVi(customDomain.verified_at)}
                    </span>
                  )}
                </div>
              </div>
            )}

            {customErrorGuidance && (
              <div className={styles.statusBannerNeg} role="alert">
                {customErrorGuidance}
              </div>
            )}

            {customDomain.status === "REMOVING" && (
              <div className={styles.statusBannerNeutral} role="status">
                Hệ thống đang hoàn tất quá trình gỡ bỏ tên miền này. Bạn vui lòng tải lại sau ít
                phút.
              </div>
            )}

            {customDomain.verification.length > 0 &&
              (customDomain.status === "ACTIVE" ? (
                <details className={styles.dnsDetails}>
                  <summary className={styles.dnsSummary}>
                    Xem lại bản ghi DNS đã khai báo ({customDomain.verification.length})
                  </summary>
                  <div className={styles.dnsDetailsBody}>
                    {renderDnsRecords(customDomain.verification)}
                  </div>
                </details>
              ) : (
                <div className={styles.dnsSection}>
                  <h3 className={styles.dnsHeading}>Bảng bản ghi DNS cần khai báo</h3>
                  {renderDnsRecords(customDomain.verification)}
                </div>
              ))}

            <p className={styles.footnote}>
              Mỗi cửa hàng gắn tối đa 1 tên miền riêng. Để đổi sang tên miền khác, hãy gỡ tên miền
              hiện tại trước.
            </p>
          </div>
        )}
      </section>
    </div>
  );
}
