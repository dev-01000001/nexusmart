"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Button,
  IconArrowUpRight,
  IconGlobe,
  IconRefresh,
  IconSearch,
  StateView,
} from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import {
  formatDomainErrorForSeller,
  getDomainStatusMeta,
  type SiteDomainDTO,
} from "@/lib/site/domains";
import { EMPTY_DASH } from "@/features/admin/adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { AdminConfirmDialog, AdminDrawer, SlidingTabBar } from "@/features/admin/AdminUi";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import type { AdminSitesListResponse, GwSite } from "@/features/seller/site/siteEditorTypes";
import { getSiteStatusMeta } from "@/features/seller/site/siteEditorUtils";
import { buildPublicSiteUrl } from "@/features/seller/site/siteSummary";
import styles from "./AdminSitesClient.module.css";

type SiteFilterTab = "all" | "PUBLISHED" | "DRAFT" | "SUSPENDED";

interface ShopDomainsCacheEntry {
  domains: SiteDomainDTO[];
  loading: boolean;
  error: string | null;
  loaded: boolean;
}

export interface AdminSitesClientProps {
  initialSites: GwSite[];
  rootDomain: string;
  requestHost: string;
  requestProto: string;
  shopNameById: Record<string, string>;
  loadError: string | null;
}

function AdminSitesContent({
  initialSites,
  rootDomain,
  requestHost,
  requestProto,
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
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [drawerOpen, setDrawerOpen] = useState(Boolean(initialSelected));
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [domainsByShopId, setDomainsByShopId] = useState<Record<string, ShopDomainsCacheEntry>>({});
  const [confirmRemoveDomain, setConfirmRemoveDomain] = useState<{
    site: GwSite;
    domain: SiteDomainDTO;
  } | null>(null);
  const [confirmSuspendSite, setConfirmSuspendSite] = useState<GwSite | null>(null);
  const [removingDomainKey, setRemovingDomainKey] = useState<string | null>(null);
  const fetchedShopIdsRef = useRef<Set<string>>(new Set());
  const inflightShopIdsRef = useRef<Set<string>>(new Set());

  const resolveShopName = useCallback(
    (site: GwSite) =>
      site.shop_name || shopNameById[site.shop_id] || `Gian hàng ${site.shop_id.slice(0, 8)}`,
    [shopNameById]
  );

  const resolveSiteHost = useCallback(
    (site: GwSite) =>
      site.primary_host || (site.subdomain ? `${site.subdomain}.${rootDomain}` : site.id),
    [rootDomain]
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

  const activeSite = useMemo(() => {
    if (!selectedId) return null;
    return sites.find((s) => s.id === selectedId) ?? null;
  }, [sites, selectedId]);

  const loadShopDomains = useCallback(async (shopId: string, force = false) => {
    if (!shopId) return;
    if (
      !force &&
      (fetchedShopIdsRef.current.has(shopId) || inflightShopIdsRef.current.has(shopId))
    ) {
      return;
    }
    inflightShopIdsRef.current.add(shopId);
    setDomainsByShopId((prev) => ({
      ...prev,
      [shopId]: {
        domains: prev[shopId]?.domains ?? [],
        loading: true,
        error: null,
        loaded: prev[shopId]?.loaded ?? false,
      },
    }));
    try {
      const res = await api<{ domains?: SiteDomainDTO[] }>(
        `/api/admin/sites/${encodeURIComponent(shopId)}/domains`
      );
      fetchedShopIdsRef.current.add(shopId);
      setDomainsByShopId((prev) => ({
        ...prev,
        [shopId]: {
          domains: res.domains ?? [],
          loading: false,
          error: null,
          loaded: true,
        },
      }));
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? err.message
          : "Không thể tải danh sách tên miền của site lúc này.";
      setDomainsByShopId((prev) => ({
        ...prev,
        [shopId]: {
          domains: prev[shopId]?.domains ?? [],
          loading: false,
          error: msg,
          loaded: prev[shopId]?.loaded ?? false,
        },
      }));
    } finally {
      inflightShopIdsRef.current.delete(shopId);
    }
  }, []);

  useEffect(() => {
    if (drawerOpen && activeSite?.shop_id) {
      void loadShopDomains(activeSite.shop_id);
    }
  }, [drawerOpen, activeSite?.shop_id, loadShopDomains]);

  const openSiteDrawer = (site: GwSite) => {
    setReasonErrorId(null);
    setSelectedId(site.id);
    setDrawerOpen(true);
  };

  const handleRemoveAdminDomain = async (site: GwSite, domain: SiteDomainDTO) => {
    if (domain.kind !== "CUSTOM") return;
    const domainKey = `${site.shop_id}:${domain.id}`;
    setRemovingDomainKey(domainKey);
    try {
      await api<void>(
        `/api/admin/sites/${encodeURIComponent(site.shop_id)}/domains/${encodeURIComponent(domain.id)}`,
        { method: "DELETE" }
      );
      setDomainsByShopId((prev) => {
        const entry = prev[site.shop_id];
        if (!entry) return prev;
        const remaining = entry.domains
          .filter((item) => item.id !== domain.id)
          .map((item) => (item.kind === "SUBDOMAIN" ? { ...item, is_primary: true } : item));
        return {
          ...prev,
          [site.shop_id]: {
            ...entry,
            domains: remaining,
          },
        };
      });
      const fallbackHost = site.subdomain ? `${site.subdomain}.${rootDomain}` : site.primary_host;
      setSites((prev) =>
        prev.map((item) => (item.id === site.id ? { ...item, primary_host: fallbackHost } : item))
      );
      setConfirmRemoveDomain(null);
      showToast(`Đã gỡ tên miền riêng "${domain.host}" khỏi ${resolveShopName(site)}.`);
      router.refresh();
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể gỡ tên miền riêng lúc này.");
    } finally {
      setRemovingDomainKey(null);
    }
  };

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
      fetchedShopIdsRef.current.clear();
      if (drawerOpen && activeSite?.shop_id) {
        void loadShopDomains(activeSite.shop_id, true);
      }
      showToast("Đã đồng bộ danh sách site gian hàng.");
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể tải lại danh sách site.");
    } finally {
      setRefreshing(false);
    }
  };

  const handleRequestSuspend = (site: GwSite) => {
    const reason = (reasonDrafts[site.id] ?? "").trim();
    if (!reason) {
      setReasonErrorId(site.id);
      return;
    }
    setReasonErrorId(null);
    setConfirmSuspendSite(site);
  };

  const handleToggleSuspend = async (site: GwSite, suspend: boolean) => {
    const reason = (reasonDrafts[site.id] ?? "").trim();
    if (suspend && !reason) {
      setReasonErrorId(site.id);
      setConfirmSuspendSite(null);
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
      setConfirmSuspendSite(null);
      const shopLabel = resolveShopName(site);
      const siteHost = resolveSiteHost(site);
      showToast(
        suspend
          ? `Đã tạm khóa site "${siteHost}" của ${shopLabel}.`
          : `Đã mở khóa site "${siteHost}" của ${shopLabel}.`
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
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const tabs: Array<{ id: SiteFilterTab; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "PUBLISHED", label: "Đang hoạt động", count: counts.PUBLISHED },
    { id: "DRAFT", label: "Bản nháp", count: counts.DRAFT },
    { id: "SUSPENDED", label: "Tạm khóa", count: counts.SUSPENDED },
  ];

  const renderDrawerBody = (site: GwSite) => {
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
    const domainState = domainsByShopId[site.shop_id];
    const domainsLoading = !domainState || domainState.loading;

    return (
      <>
        <div className={shared.detailWell}>
          <div className={shared.detailGrid}>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Gian hàng</span>
              <span className={shared.detailVal}>{shopName}</span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Tên miền phụ</span>
              <span className={shared.detailVal}>{site.subdomain || EMPTY_DASH}</span>
            </div>
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Địa chỉ đầy đủ</span>
              {isPublished ? (
                <a
                  href={buildPublicSiteUrl(fullHost, requestHost, requestProto) || "#"}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.hostLink}
                >
                  <span>{fullHost}</span>
                  <IconArrowUpRight size={14} />
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
        </div>

        <div className={styles.domainsSection} aria-label="Tên miền của site">
          <div className={styles.domainsHeader}>
            <h3 className={styles.domainsTitle}>
              <IconGlobe size={15} />
              <span>Tên miền của site</span>
            </h3>
            <button
              type="button"
              className={styles.domainsReloadBtn}
              disabled={domainsLoading}
              onClick={() => void loadShopDomains(site.shop_id, true)}
            >
              <IconRefresh size={13} />
              <span>{domainsLoading ? "Đang tải..." : "Tải lại"}</span>
            </button>
          </div>

          {domainsLoading && !domainState?.loaded ? (
            <p className={styles.domainsStateText}>Đang tải danh sách tên miền...</p>
          ) : domainState?.error ? (
            <div className={styles.domainsErrorRow} role="alert">
              <span>{domainState.error}</span>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => void loadShopDomains(site.shop_id, true)}
              >
                Thử lại
              </Button>
            </div>
          ) : !domainState || domainState.domains.length === 0 ? (
            <p className={styles.domainsStateText}>Chưa có bản ghi tên miền nào cho site này.</p>
          ) : (
            <div className={styles.domainsList}>
              {domainState.domains.map((domain) => {
                const domainMeta = getDomainStatusMeta(domain.status);
                const isCustom = domain.kind === "CUSTOM";
                const errorText =
                  domain.status !== "ACTIVE" &&
                  (domain.status === "FAILED" || Boolean(domain.last_error))
                    ? formatDomainErrorForSeller(domain.last_error || "verify")
                    : "";

                return (
                  <div key={domain.id} className={styles.domainItem}>
                    <div className={styles.domainRowMain}>
                      <div className={styles.domainIdentity}>
                        <div className={styles.domainHostRow}>
                          <span className={styles.domainHost}>{domain.host}</span>
                          {domain.is_primary && (
                            <span
                              className={`${shared.channelBadge} ${shared.channelBadgeStorefront}`}
                            >
                              Địa chỉ chính
                            </span>
                          )}
                        </div>
                        <div className={styles.domainMetaRow}>
                          <span>
                            {domain.kind === "SUBDOMAIN" ? "Tên miền mặc định" : "Tên miền riêng"}
                          </span>
                          <span className={styles.domainMetaItem}>
                            Xác minh:{" "}
                            {domain.verified_at ? formatDateTimeVi(domain.verified_at) : EMPTY_DASH}
                          </span>
                        </div>
                        {errorText && <span className={styles.domainErrorNote}>{errorText}</span>}
                      </div>

                      <div className={styles.domainRight}>
                        <StatusPill label={domainMeta.label} tone={domainMeta.adminTone} />
                        {isCustom && (
                          <button
                            type="button"
                            className={`${shared.rowActionBtn} ${shared.rowActionBtnDanger}`}
                            disabled={Boolean(removingDomainKey)}
                            onClick={() => setConfirmRemoveDomain({ site, domain })}
                          >
                            Gỡ tên miền
                          </button>
                        )}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`site-reason-${site.id}`} className={shared.fieldLabel}>
              {isSuspended ? "Ghi chú mở khóa site (tùy chọn)" : "Lý do tạm khóa site (bắt buộc)"}
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
                  ? "Nhập ghi chú mở khóa cho nhật ký quản trị..."
                  : "Nhập lý do cụ thể trước khi tạm khóa..."
              }
            />
            {showReasonError && (
              <span className={shared.validationError}>
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
                loading={isBusy}
                onClick={() => void handleToggleSuspend(site, false)}
              >
                Mở khóa
              </Button>
            ) : (
              <Button
                variant="danger"
                size="sm"
                shape="app"
                disabled={isBusy}
                onClick={() => handleRequestSuspend(site)}
              >
                Tạm khóa
              </Button>
            )}
          </div>
        </div>
      </>
    );
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Site gian hàng</h1>
        </div>
        <div className={shared.titleActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            loading={refreshing}
            onClick={() => void handleRefresh()}
          >
            <IconRefresh size={14} />
            <span>Làm mới</span>
          </Button>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Lọc trạng thái site gian hàng"
        rightSlot={<span className={shared.resultCount}>*.{rootDomain}</span>}
      />

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
        <p className={shared.emptyRow}>Không tìm thấy site gian hàng phù hợp.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Gian hàng</th>
                  <th>Tên miền phụ &amp; Địa chỉ</th>
                  <th>Trạng thái</th>
                  <th className={shared.colNum}>Phiên bản</th>
                  <th>Cập nhật</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((site) => {
                  const statusMeta = getSiteStatusMeta(site.status);
                  const isSelected = drawerOpen && activeSite?.id === site.id;
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
                      onClick={() => openSiteDrawer(site)}
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
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            openSiteDrawer(site);
                          }}
                        >
                          {isSuspended ? "Mở khóa" : "Quản lý / Khóa"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filtered.map((site) => {
              const statusMeta = getSiteStatusMeta(site.status);
              const shopName = resolveShopName(site);
              const fullHost = site.primary_host || `${site.subdomain}.${rootDomain}`;
              return (
                <div key={site.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => openSiteDrawer(site)}
                  >
                    <span className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{shopName}</span>
                      <StatusPill label={statusMeta.label} tone={statusMeta.adminTone} />
                    </span>
                    <span className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>{fullHost}</span>
                    </span>
                  </button>
                </div>
              );
            })}
          </div>
        </>
      )}

      {activeSite && (
        <AdminDrawer
          open={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          title={resolveShopName(activeSite)}
          subtitle={activeSite.primary_host || `${activeSite.subdomain}.${rootDomain}`}
          headerBadge={
            <StatusPill
              label={getSiteStatusMeta(activeSite.status).label}
              tone={getSiteStatusMeta(activeSite.status).adminTone}
            />
          }
          width="lg"
        >
          {renderDrawerBody(activeSite)}
        </AdminDrawer>
      )}

      <AdminConfirmDialog
        open={Boolean(confirmRemoveDomain)}
        title="Xác nhận gỡ tên miền riêng"
        description={
          confirmRemoveDomain
            ? `Gỡ tên miền "${confirmRemoveDomain.domain.host}" khỏi ${resolveShopName(confirmRemoveDomain.site)}? Lưu lượng sẽ quay về tên miền phụ mặc định.`
            : ""
        }
        confirmLabel="Xác nhận gỡ"
        busy={Boolean(removingDomainKey)}
        onCancel={() => setConfirmRemoveDomain(null)}
        onConfirm={() => {
          if (confirmRemoveDomain) {
            void handleRemoveAdminDomain(confirmRemoveDomain.site, confirmRemoveDomain.domain);
          }
        }}
      />

      <AdminConfirmDialog
        open={Boolean(confirmSuspendSite)}
        title="Xác nhận tạm khóa site"
        description={
          confirmSuspendSite
            ? `Tạm khóa site "${resolveSiteHost(confirmSuspendSite)}" của ${resolveShopName(confirmSuspendSite)}? Khách mua sẽ không thể truy cập cho tới khi mở khóa lại.`
            : ""
        }
        confirmLabel="Xác nhận tạm khóa"
        busy={Boolean(confirmSuspendSite && busyId === confirmSuspendSite.id)}
        onCancel={() => setConfirmSuspendSite(null)}
        onConfirm={() => {
          if (confirmSuspendSite) {
            void handleToggleSuspend(confirmSuspendSite, true);
          }
        }}
      >
        {confirmSuspendSite && (
          <div className={styles.confirmReasonBox}>
            Lý do: {reasonDrafts[confirmSuspendSite.id]}
          </div>
        )}
      </AdminConfirmDialog>
    </div>
  );
}

export function AdminSitesClient(props: AdminSitesClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Site gian hàng" />}>
      <AdminStateGate
        title="Site gian hàng"
        emptyTitle="Chưa có site gian hàng nào"
        emptyMessage="Khi các gian hàng tạo site riêng, danh sách sẽ hiển thị tại đây."
        errorTitle="Không thể tải danh sách site"
        errorMessage="Dịch vụ quản lý site tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <AdminSitesContent
            initialSites={props.initialSites}
            rootDomain={props.rootDomain}
            requestHost={props.requestHost}
            requestProto={props.requestProto}
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
