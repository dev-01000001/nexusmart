"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ExternalLink, Globe, RefreshCw, Search } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  PageHeader,
  StatusBadge,
  siteStatusTone,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, ApiError } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import {
  formatDomainErrorForSeller,
  getDomainStatusMeta,
  type SiteDomainDTO,
} from "@/lib/site/domains";
import { cn } from "@/lib/utils";
import { EMPTY_DASH } from "@/features/admin/adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import {
  AdminConfirmActionDialog,
  AdminDetailField,
  AdminDetailGrid,
  AdminPaginationBar,
} from "@/features/admin/ops/AdminOpsPrimitives";
import { adminDomainStatusTone } from "@/features/admin/ops/adminTones";
import type { AdminSitesListResponse, GwSite } from "@/features/seller/site/siteEditorTypes";
import { getSiteStatusMeta } from "@/features/seller/site/siteEditorUtils";
import { buildPublicSiteUrl } from "@/features/seller/site/siteSummary";

type SiteFilterTab = "all" | "PUBLISHED" | "DRAFT" | "SUSPENDED";

const SITES_PAGE_SIZE = 15;

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
  const { user, showToast } = useAdminContext();
  const canSuspendSite = roleHasCapability(user.role, "sites.suspend");
  const canWriteDomains = roleHasCapability(user.role, "sites.domains.write");

  const [sites, setSites] = useState<GwSite[]>(initialSites);
  const [activeTab, setActiveTab] = useState<SiteFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [page, setPage] = useState(1);
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

  const totalPages = Math.max(1, Math.ceil(filtered.length / SITES_PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pagedSites = useMemo(() => {
    const start = (safePage - 1) * SITES_PAGE_SIZE;
    return filtered.slice(start, start + SITES_PAGE_SIZE);
  }, [filtered, safePage]);

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
    if (!canWriteDomains || domain.kind !== "CUSTOM") return;
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
    if (!canSuspendSite) return;
    const reason = (reasonDrafts[site.id] ?? "").trim();
    if (!reason) {
      setReasonErrorId(site.id);
      return;
    }
    setReasonErrorId(null);
    setConfirmSuspendSite(site);
  };

  const handleToggleSuspend = async (site: GwSite, suspend: boolean) => {
    if (!canSuspendSite) return;
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
      <div className="space-y-5">
        <PageHeader title="Site gian hàng" />
        <ErrorState
          title="Không thể tải danh sách site gian hàng"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  const tabs = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PUBLISHED", label: "Đang hoạt động", count: counts.PUBLISHED },
    { key: "DRAFT", label: "Bản nháp", count: counts.DRAFT },
    { key: "SUSPENDED", label: "Tạm khóa", count: counts.SUSPENDED },
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
      <div className="space-y-5 px-4 pb-6">
        <AdminDetailGrid>
          <AdminDetailField label="Gian hàng">{shopName}</AdminDetailField>
          <AdminDetailField label="Tên miền phụ">{site.subdomain || EMPTY_DASH}</AdminDetailField>
          <AdminDetailField label="Địa chỉ đầy đủ" wide>
            {isPublished ? (
              <a
                href={buildPublicSiteUrl(fullHost, requestHost, requestProto) || "#"}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 font-medium text-brand hover:underline"
              >
                <span>{fullHost}</span>
                <ExternalLink className="size-3.5 shrink-0" aria-hidden="true" />
              </a>
            ) : (
              <span>{fullHost}</span>
            )}
          </AdminDetailField>
          <AdminDetailField label="Phiên bản xuất bản" mono>
            {versionText}
          </AdminDetailField>
          <AdminDetailField label="Xuất bản gần nhất" mono>
            {site.published_at ? formatDateTimeVi(site.published_at) : EMPTY_DASH}
          </AdminDetailField>
          <AdminDetailField label="Cập nhật gần nhất" mono wide>
            {site.updated_at ? formatDateTimeVi(site.updated_at) : EMPTY_DASH}
          </AdminDetailField>
          {site.suspended_reason && (
            <AdminDetailField label="Lý do tạm khóa hiện tại" wide>
              <span className="font-medium text-destructive">{site.suspended_reason}</span>
            </AdminDetailField>
          )}
        </AdminDetailGrid>

        <Separator />

        <div className="space-y-3" aria-label="Tên miền của site">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="inline-flex items-center gap-1.5 text-sm font-semibold text-foreground">
              <Globe className="size-4 text-brand" aria-hidden="true" />
              <span>Tên miền của site</span>
            </h3>
            <Button
              type="button"
              variant="outline"
              size="xs"
              disabled={domainsLoading}
              onClick={() => void loadShopDomains(site.shop_id, true)}
            >
              <RefreshCw
                className={cn("size-3.5", domainsLoading && "animate-spin")}
                aria-hidden="true"
              />
              <span>{domainsLoading ? "Đang tải..." : "Tải lại"}</span>
            </Button>
          </div>

          {domainsLoading && !domainState?.loaded ? (
            <p className="text-xs text-muted-foreground">Đang tải danh sách tên miền...</p>
          ) : domainState?.error ? (
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-badge-critical-bg/40 p-3 text-xs text-destructive"
              role="alert"
            >
              <span>{domainState.error}</span>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => void loadShopDomains(site.shop_id, true)}
              >
                Thử lại
              </Button>
            </div>
          ) : !domainState || domainState.domains.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              Chưa có bản ghi tên miền nào cho site này.
            </p>
          ) : (
            <div className="space-y-2">
              {domainState.domains.map((domain) => {
                const domainMeta = getDomainStatusMeta(domain.status);
                const isCustom = domain.kind === "CUSTOM";
                const errorText =
                  domain.status !== "ACTIVE" &&
                  (domain.status === "FAILED" || Boolean(domain.last_error))
                    ? formatDomainErrorForSeller(domain.last_error || "verify")
                    : "";

                return (
                  <div
                    key={domain.id}
                    className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div className="min-w-0 flex-1 space-y-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-[13px] font-medium text-foreground [overflow-wrap:anywhere]">
                          {domain.host}
                        </span>
                        {domain.is_primary && <Badge variant="info">Địa chỉ chính</Badge>}
                      </div>
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
                        <span>
                          {domain.kind === "SUBDOMAIN" ? "Tên miền mặc định" : "Tên miền riêng"}
                        </span>
                        <span>·</span>
                        <span className="tabular-nums">
                          Xác minh:{" "}
                          {domain.verified_at ? formatDateTimeVi(domain.verified_at) : EMPTY_DASH}
                        </span>
                      </div>
                      {errorText && (
                        <p className="text-xs font-medium text-destructive">{errorText}</p>
                      )}
                    </div>

                    <div className="flex shrink-0 flex-wrap items-center gap-2">
                      <StatusBadge tone={adminDomainStatusTone(domain.status)}>
                        {domainMeta.label}
                      </StatusBadge>
                      {isCustom && canWriteDomains && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="xs"
                          disabled={Boolean(removingDomainKey)}
                          className="text-destructive hover:bg-badge-critical-bg/60 hover:text-destructive"
                          onClick={() => setConfirmRemoveDomain({ site, domain })}
                        >
                          Gỡ tên miền
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {canSuspendSite && (
          <>
            <Separator />
            <div className="space-y-3 rounded-lg border border-border bg-card p-4">
              <Field>
                <FieldLabel htmlFor={`site-reason-${site.id}`}>
                  {isSuspended
                    ? "Ghi chú mở khóa site (tùy chọn)"
                    : "Lý do tạm khóa site (bắt buộc)"}
                </FieldLabel>
                <Input
                  id={`site-reason-${site.id}`}
                  type="text"
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
                  <FieldError>Vui lòng nhập lý do cụ thể trước khi tạm khóa site.</FieldError>
                )}
              </Field>

              <div className="flex flex-wrap items-center gap-2">
                {isSuspended ? (
                  <Button
                    type="button"
                    size="sm"
                    disabled={isBusy}
                    onClick={() => void handleToggleSuspend(site, false)}
                  >
                    {isBusy ? "Đang mở khóa..." : "Mở khóa"}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    disabled={isBusy}
                    onClick={() => handleRequestSuspend(site)}
                  >
                    Tạm khóa
                  </Button>
                )}
              </div>
            </div>
          </>
        )}
      </div>
    );
  };

  return (
    <div className="space-y-5">
      <PageHeader
        title="Site gian hàng"
        subtitle={`Quản lý tên miền phụ (*.${rootDomain}), tên miền riêng và trạng thái xuất bản.`}
        primaryAction={
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={refreshing}
            onClick={() => void handleRefresh()}
          >
            <RefreshCw className={cn("size-4", refreshing && "animate-spin")} aria-hidden="true" />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
        }
      />

      <MetricStrip
        className="md:grid-flow-row md:grid-cols-2 md:divide-x-0 lg:grid-flow-col lg:auto-cols-fr lg:divide-x"
        items={[
          { label: "Tổng site", value: formatInt(counts.all) },
          { label: "Đang hoạt động", value: formatInt(counts.PUBLISHED) },
          { label: "Bản nháp", value: formatInt(counts.DRAFT) },
          { label: "Tạm khóa", value: formatInt(counts.SUSPENDED) },
        ]}
      />

      <IndexTable
        tabs={tabs}
        activeTab={activeTab}
        onTabChange={(next) => {
          setActiveTab(next as SiteFilterTab);
          setPage(1);
        }}
        toolbar={
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatInt(filtered.length)} site · *.{rootDomain}
          </span>
        }
        footer={
          filtered.length > 0 ? (
            <AdminPaginationBar
              page={safePage}
              totalPages={totalPages}
              totalItems={filtered.length}
              itemLabel="site"
              onPageChange={setPage}
            />
          ) : undefined
        }
      >
        <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
          <div className="relative min-w-0 flex-1 sm:max-w-sm">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage(1);
              }}
              placeholder="Tìm tên gian hàng, tên miền phụ..."
              aria-label="Tìm kiếm site gian hàng"
              className="h-9 bg-card pl-8"
            />
          </div>
        </div>

        {filtered.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Globe}
              title="Không tìm thấy site gian hàng phù hợp"
              description="Thử chọn bộ lọc trạng thái khác hoặc thay đổi từ khóa tìm kiếm."
              className="shadow-none"
            />
          </div>
        ) : (
          <>
            <div className="hidden md:block">
              <Table aria-label="Danh sách site gian hàng">
                <TableHeader>
                  <TableRow>
                    <TableHead>Gian hàng</TableHead>
                    <TableHead className="hidden lg:table-cell">
                      Tên miền phụ &amp; Địa chỉ
                    </TableHead>
                    <TableHead>Trạng thái</TableHead>
                    <TableHead className="hidden text-right lg:table-cell">Phiên bản</TableHead>
                    <TableHead className="hidden xl:table-cell">Cập nhật</TableHead>
                    <TableHead className="text-right">Thao tác</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pagedSites.map((site) => {
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
                      <TableRow
                        key={site.id}
                        data-state={isSelected ? "selected" : undefined}
                        className="cursor-pointer"
                        onClick={() => openSiteDrawer(site)}
                      >
                        <TableCell className="max-w-[240px] whitespace-normal">
                          <div className="font-medium text-foreground">{shopName}</div>
                          <div className="truncate text-xs text-muted-foreground lg:hidden">
                            {fullHost}
                          </div>
                          <div className="hidden text-xs text-muted-foreground tabular-nums lg:block">
                            {site.shop_id.slice(0, 12)}
                          </div>
                        </TableCell>
                        <TableCell className="hidden max-w-[260px] whitespace-normal lg:table-cell">
                          <div className="font-medium text-foreground">{site.subdomain}</div>
                          <div className="truncate text-xs text-muted-foreground">{fullHost}</div>
                        </TableCell>
                        <TableCell>
                          <StatusBadge tone={siteStatusTone(site.status)}>
                            {statusMeta.label}
                          </StatusBadge>
                        </TableCell>
                        <TableCell className="hidden text-right tabular-nums lg:table-cell">
                          {site.published_version_id ? (
                            <Badge variant="secondary" className="rounded-md font-mono">
                              {versionLabel}
                            </Badge>
                          ) : (
                            <span className="text-xs text-muted-foreground">{versionLabel}</span>
                          )}
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground tabular-nums xl:table-cell">
                          {site.updated_at ? formatDateTimeVi(site.updated_at) : EMPTY_DASH}
                        </TableCell>
                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() => openSiteDrawer(site)}
                          >
                            {canSuspendSite
                              ? isSuspended
                                ? "Mở khóa"
                                : "Quản lý / Khóa"
                              : "Xem chi tiết"}
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>

            <div className="md:hidden">
              {pagedSites.map((site) => {
                const statusMeta = getSiteStatusMeta(site.status);
                const shopName = resolveShopName(site);
                const fullHost = site.primary_host || `${site.subdomain}.${rootDomain}`;
                return (
                  <IndexRowCard
                    key={site.id}
                    title={shopName}
                    badge={
                      <StatusBadge tone={siteStatusTone(site.status)}>
                        {statusMeta.label}
                      </StatusBadge>
                    }
                    meta={<span>{fullHost}</span>}
                    onClick={() => openSiteDrawer(site)}
                  />
                );
              })}
            </div>
          </>
        )}
      </IndexTable>

      <Sheet open={drawerOpen && Boolean(activeSite)} onOpenChange={setDrawerOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {activeSite ? (
            <>
              <SheetHeader className="border-b border-border pr-12">
                <div className="flex flex-wrap items-center gap-2">
                  <SheetTitle>{resolveShopName(activeSite)}</SheetTitle>
                  <StatusBadge tone={siteStatusTone(activeSite.status)}>
                    {getSiteStatusMeta(activeSite.status).label}
                  </StatusBadge>
                </div>
                <SheetDescription>
                  {activeSite.primary_host || `${activeSite.subdomain}.${rootDomain}`}
                </SheetDescription>
              </SheetHeader>
              {renderDrawerBody(activeSite)}
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      <AdminConfirmActionDialog
        open={canWriteDomains && Boolean(confirmRemoveDomain)}
        title="Xác nhận gỡ tên miền riêng"
        description={
          confirmRemoveDomain
            ? `Gỡ tên miền "${confirmRemoveDomain.domain.host}" khỏi ${resolveShopName(confirmRemoveDomain.site)}? Lưu lượng sẽ quay về tên miền phụ mặc định.`
            : ""
        }
        confirmLabel="Xác nhận gỡ"
        confirmVariant="destructive"
        busy={Boolean(removingDomainKey)}
        onCancel={() => setConfirmRemoveDomain(null)}
        onConfirm={() => {
          if (confirmRemoveDomain) {
            void handleRemoveAdminDomain(confirmRemoveDomain.site, confirmRemoveDomain.domain);
          }
        }}
      />

      <AdminConfirmActionDialog
        open={canSuspendSite && Boolean(confirmSuspendSite)}
        title="Xác nhận tạm khóa site"
        description={
          confirmSuspendSite
            ? `Tạm khóa site "${resolveSiteHost(confirmSuspendSite)}" của ${resolveShopName(confirmSuspendSite)}? Khách mua sẽ không thể truy cập cho tới khi mở khóa lại.`
            : ""
        }
        confirmLabel="Xác nhận tạm khóa"
        confirmVariant="destructive"
        busy={Boolean(confirmSuspendSite && busyId === confirmSuspendSite.id)}
        onCancel={() => setConfirmSuspendSite(null)}
        onConfirm={() => {
          if (confirmSuspendSite) {
            void handleToggleSuspend(confirmSuspendSite, true);
          }
        }}
      >
        {confirmSuspendSite && (
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
            Lý do: {reasonDrafts[confirmSuspendSite.id]}
          </div>
        )}
      </AdminConfirmActionDialog>
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
