"use client";

import React, { Suspense, useCallback, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconArrowUpRight, IconRefresh, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH } from "@/features/admin/adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import type { AdminSitesListResponse, GwSite } from "@/features/seller/site/siteEditorTypes";
import { getSiteStatusMeta } from "@/features/seller/site/siteEditorUtils";
import styles from "./AdminSitesClient.module.css";

type SiteFilterTab = "all" | "PUBLISHED" | "DRAFT" | "SUSPENDED";

export interface AdminSitesClientProps {
  initialSites: GwSite[];
  rootDomain: string;
  shopNameById: Record<string, string>;
  loadError: string | null;
}

function buildPublicSiteHref(host: string): string {
  if (!host) return "#";
  if (typeof window === "undefined") return `http://${host}`;
  const proto = window.location.protocol || "http:";
  const port = window.location.port ? `:${window.location.port}` : "";
  return `${proto}//${host}${port}`;
}

function AdminSitesContent({
  initialSites,
  rootDomain,
  shopNameById,
  loadError,
  initialQuery,
  initialSelected,
}: AdminSitesClientProps & {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [sites, setSites] = useState<GwSite[]>(initialSites);
  const [activeTab, setActiveTab] = useState<SiteFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [selectedId, setSelectedId] = useState<string | null>(
    initialSelected ?? initialSites[0]?.id ?? null
  );
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const resolveShopName = useCallback(
    (site: GwSite) =>
      site.shop_name || shopNameById[site.shop_id] || `Gian hàng ${site.shop_id.slice(0, 8)}`,
    [shopNameById]
  );

  const counts = useMemo(() => {
    const map: Record<SiteFilterTab, number> = {
      all: sites.length,
      PUBLISHED: 0,
      DRAFT: 0,
      SUSPENDED: 0,
    };
    for (const s of sites) {
      const st = s.status.toUpperCase() as SiteFilterTab;
      if (st in map) map[st] += 1;
    }
    return map;
  }, [sites]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return sites.filter((s) => {
      if (activeTab !== "all" && s.status.toUpperCase() !== activeTab) return false;
      if (!q) return true;
      const shopName = resolveShopName(s).toLowerCase();
      return (
        shopName.includes(q) ||
        s.subdomain.toLowerCase().includes(q) ||
        s.primary_host.toLowerCase().includes(q) ||
        s.shop_id.toLowerCase().includes(q)
      );
    });
  }, [sites, activeTab, query, resolveShopName]);

  const activeDesktopSite = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((s) => s.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const handleRefresh = async () => {
    setRefreshing(true);
    try {
      const params = new URLSearchParams();
      params.set("page_size", "100");
      const res = await api<AdminSitesListResponse>(`/api/admin/sites?${params.toString()}`);
      const next = (res.items ?? []).map((item) => ({
        ...item,
        shop_name: item.shop_name || shopNameById[item.shop_id] || "",
      }));
      setSites(next);
      showToast("Đã đồng bộ danh sách site gian hàng mới nhất.");
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể tải lại danh sách site.");
    } finally {
      setRefreshing(false);
    }
  };

  const handleToggleSuspend = async (site: GwSite, suspend: boolean) => {
    const reason = (reasonDrafts[site.id] ?? "").trim();
    if (suspend && !reason) {
      setReasonErrorId(site.id);
      return;
    }
    setReasonErrorId(null);
    setBusyId(site.id);
    try {
      const res = await api<{ site: GwSite | null }>(
        `/api/admin/sites/${encodeURIComponent(site.id)}/suspend`,
        {
          method: "POST",
          body: { suspend, reason },
        }
      );
      if (res.site) {
        const updated: GwSite = {
          ...res.site,
          shop_name: res.site.shop_name || resolveShopName(site),
        };
        setSites((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      }
      setReasonDrafts((prev) => ({ ...prev, [site.id]: "" }));
      const shopLabel = resolveShopName(site);
      showToast(
        suspend
          ? `Đã tạm khóa site "${site.primary_host}" của ${shopLabel}.`
          : `Đã mở khóa lại site "${site.primary_host}" của ${shopLabel}.`
      );
      router.refresh();
    } catch (err) {
      showToast(
        err instanceof ApiError
          ? err.message
          : "Không thể cập nhật trạng thái site gian hàng lúc này."
      );
    } finally {
      setBusyId(null);
    }
  };

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách site gian hàng"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const renderDetailPanel = (site: GwSite, showHeading = false) => {
    const statusMeta = getSiteStatusMeta(site.status);
    const isSuspended = site.status.toUpperCase() === "SUSPENDED";
    const isPublished = site.status.toUpperCase() === "PUBLISHED";
    const draftReason = reasonDrafts[site.id] ?? "";
    const showReasonError = reasonErrorId === site.id;
    const isBusy = busyId === site.id;
    const shopName = resolveShopName(site);
    const fullHost = site.primary_host || `${site.subdomain}.${rootDomain}`;
    const versionText = site.published_version_number
      ? `v${site.published_version_number}`
      : site.published_version_id
        ? "Đã xuất bản"
        : "Chưa xuất bản";

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>Chi tiết site: {shopName}</h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={statusMeta.label} tone={statusMeta.adminTone} />
            </div>
          </div>
        )}

        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Gian hàng</span>
            <span className={shared.detailVal}>{shopName}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Tên miền phụ</span>
            <span className={shared.detailVal}>{site.subdomain || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Địa chỉ đầy đủ</span>
            {isPublished ? (
              <a
                href={buildPublicSiteHref(fullHost)}
                target="_blank"
                rel="noopener noreferrer"
                className={styles.hostLink}
              >
                <span>{fullHost}</span>
                <IconArrowUpRight size={13} />
              </a>
            ) : (
              <span className={styles.hostPlain}>{fullHost}</span>
            )}
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Phiên bản xuất bản</span>
            <span className={`${shared.detailValPlain} num`}>{versionText}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Xuất bản gần nhất</span>
            <span className={`${shared.detailValPlain} num`}>
              {site.published_at ? formatDateTimeVi(site.published_at) : EMPTY_DASH}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Cập nhật gần nhất</span>
            <span className={`${shared.detailValPlain} num`}>
              {site.updated_at ? formatDateTimeVi(site.updated_at) : EMPTY_DASH}
            </span>
          </div>
          {site.suspended_reason && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Lý do tạm khóa hiện tại</span>
              <span className={styles.reasonText}>{site.suspended_reason}</span>
            </div>
          )}
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`site-reason-${site.id}`} className={shared.fieldLabel}>
              {isSuspended ? "Ghi chú mở khóa site (tùy chọn)" : "Lý do tạm khóa site"}
            </label>
            <input
              id={`site-reason-${site.id}`}
              type="text"
              className={shared.textInput}
              value={draftReason}
              onChange={(e) => {
                setReasonDrafts((prev) => ({ ...prev, [site.id]: e.target.value }));
                if (showReasonError) setReasonErrorId(null);
              }}
              placeholder={
                isSuspended
                  ? "Nhập ghi chú mở khóa lại cho nhật ký quản trị..."
                  : "Nhập lý do cụ thể khi tạm khóa site gian hàng (bắt buộc)..."
              }
            />
            {showReasonError && (
              <span className={styles.validationError}>
                Vui lòng nhập lý do cụ thể trước khi tạm khóa site.
              </span>
            )}
          </div>

          <div className={shared.detailButtons}>
            {isSuspended ? (
              <Button
                variant="primary"
                size="sm"
                shape="app"
                disabled={isBusy}
                onClick={() => void handleToggleSuspend(site, false)}
              >
                {isBusy ? "Đang xử lý..." : "Mở khóa"}
              </Button>
            ) : (
              <Button
                variant="danger"
                size="sm"
                shape="app"
                disabled={isBusy}
                onClick={() => void handleToggleSuspend(site, true)}
              >
                {isBusy ? "Đang xử lý..." : "Tạm khóa"}
              </Button>
            )}
          </div>
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: SiteFilterTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "PUBLISHED", label: "Đang hoạt động" },
    { id: "DRAFT", label: "Bản nháp" },
    { id: "SUSPENDED", label: "Tạm khóa" },
  ];

  const subtitle = `${formatInt(counts.PUBLISHED)} site đang hoạt động công khai, ${formatInt(counts.DRAFT)} bản nháp và ${formatInt(counts.SUSPENDED)} site đang tạm khóa trên tên miền *.${rootDomain}.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Site gian hàng</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
        <div className={shared.titleActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={refreshing}
            onClick={() => void handleRefresh()}
          >
            <IconRefresh size={14} />
            <span>{refreshing ? "Đang đồng bộ..." : "Làm mới"}</span>
          </Button>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái site gian hàng">
          {tabs.map((tab) => {
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.id)}
              >
                <span>{tab.label}</span>
                <span className={shared.tabCount}>{counts[tab.id]}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className={shared.toolbar}>
        <div className={shared.toolbarLeft}>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              type="search"
              className={shared.toolbarSearch}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm tên gian hàng, tên miền phụ..."
              aria-label="Tìm kiếm site gian hàng"
            />
          </div>
        </div>
        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} site</span>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>
          Không tìm thấy site gian hàng nào khớp với điều kiện lọc hiện tại.
        </p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Gian hàng</th>
                  <th>Tên miền phụ & Địa chỉ</th>
                  <th>Trạng thái</th>
                  <th className={shared.colNum}>Phiên bản</th>
                  <th>Cập nhật</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((site) => {
                  const statusMeta = getSiteStatusMeta(site.status);
                  const isSelected = activeDesktopSite?.id === site.id;
                  const shopName = resolveShopName(site);
                  const fullHost = site.primary_host || `${site.subdomain}.${rootDomain}`;
                  const isSuspended = site.status.toUpperCase() === "SUSPENDED";
                  const versionLabel = site.published_version_number
                    ? `v${site.published_version_number}`
                    : site.published_version_id
                      ? "Đã xuất bản"
                      : "Chưa có";

                  return (
                    <tr
                      key={site.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(site.id);
                      }}
                    >
                      <td>
                        <div className={shared.cellPrimary}>{shopName}</div>
                        <div className={shared.cellSub}>{site.shop_id.slice(0, 12)}</div>
                      </td>
                      <td>
                        <div className={shared.cellPrimary}>{site.subdomain}</div>
                        <div className={shared.cellSub}>{fullHost}</div>
                      </td>
                      <td>
                        <StatusPill label={statusMeta.label} tone={statusMeta.adminTone} />
                      </td>
                      <td className={shared.colNum}>
                        <span
                          className={
                            site.published_version_id ? styles.versionTag : styles.versionMuted
                          }
                        >
                          {versionLabel}
                        </span>
                      </td>
                      <td>{site.updated_at ? formatDateTimeVi(site.updated_at) : EMPTY_DASH}</td>
                      <td className={shared.colAction}>
                        <div className={styles.inlineActionRow}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              setReasonErrorId(null);
                              setSelectedId(site.id);
                            }}
                          >
                            {isSelected ? "Đang xem" : isSuspended ? "Mở khóa" : "Quản lý / Khóa"}
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeDesktopSite && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Chi tiết và kiểm soát trạng thái site gian hàng"
            >
              {renderDetailPanel(activeDesktopSite, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((site) => {
              const statusMeta = getSiteStatusMeta(site.status);
              const isSelected = mobileOpenId === site.id;
              const shopName = resolveShopName(site);
              const fullHost = site.primary_host || `${site.subdomain}.${rootDomain}`;
              return (
                <div key={site.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => {
                      setReasonErrorId(null);
                      setMobileOpenId((prev) => (prev === site.id ? null : site.id));
                    }}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{shopName}</span>
                      <span className={styles.mobilePillRow}>
                        <StatusPill label={statusMeta.label} tone={statusMeta.adminTone} />
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>{fullHost}</span>
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderDetailPanel(site)}</div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

export function AdminSitesClient(props: AdminSitesClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Site gian hàng" />}>
      <AdminStateGate
        title="Site gian hàng"
        subtitle="Theo dõi danh sách tên miền phụ và tạm khóa các site vi phạm chính sách sàn."
        emptyTitle="Chưa có site gian hàng nào"
        emptyMessage="Khi các gian hàng tạo và lưu cấu hình site riêng, danh sách sẽ hiển thị tại đây."
        errorTitle="Không thể tải danh sách site gian hàng"
        errorMessage="Không thể kết nối tới dịch vụ quản lý site lúc này. Vui lòng thử tải lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <AdminSitesContent
            initialSites={props.initialSites}
            rootDomain={props.rootDomain}
            shopNameById={props.shopNameById}
            loadError={props.loadError}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
