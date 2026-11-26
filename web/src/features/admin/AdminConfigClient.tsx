"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Info, Search, Ticket } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  PageHeader,
  StatusBadge,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { GatewayCategory, GatewayVoucher } from "@/lib/gateway-types";
import { EMPTY_DASH } from "./adminMeta";
import { AdminApprovalsPanel } from "./AdminApprovalsPanel";
import { AdminCarriersPanel } from "./AdminCarriersPanel";
import { AdminCategoriesPanel } from "./AdminCategoriesPanel";
import { AdminFeesPanel } from "./AdminFeesPanel";
import { AdminPaymentMethodsPanel } from "./AdminPaymentMethodsPanel";
import { AdminSearchReindexBlock } from "./AdminSearchReindexBlock";
import { useAdminContext } from "./AdminShell";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { AdminUsersPanel } from "./AdminUsersPanel";
import { getVoucherScopeMeta, type AdminVoucherScope } from "./adminComplaintsTypes";
import {
  DetailGrid,
  DetailItem,
  FilterSelect,
  pillToneToStatusTone,
} from "./config/adminUiHelpers";

type ConfigTab =
  | "thanh_toan"
  | "duyet_hai_nguoi"
  | "phi_san"
  | "van_chuyen"
  | "danh_muc"
  | "nguoi_dung"
  | "voucher_san";

export interface AdminConfigClientProps {
  initialCategories: GatewayCategory[];
  categoriesError: string | null;
  initialVouchers: GatewayVoucher[];
  vouchersError: string | null;
}

function ConfigWorkspace({
  initialCategories,
  categoriesError,
  initialVouchers,
  vouchersError,
  initialQuery,
}: AdminConfigClientProps & { initialQuery: string }) {
  const router = useRouter();
  const { user } = useAdminContext();
  const role = user.role;

  const canReadPaymentMethods = roleHasCapability(role, "payment_methods.read");
  const canReadApprovals = roleHasCapability(role, "approvals.read");
  const canReadFees =
    roleHasCapability(role, "fees.category.read") || roleHasCapability(role, "fees.payment.read");
  const canReadCarriers = roleHasCapability(role, "carriers.read");
  const canReadCategories = roleHasCapability(role, "categories.read");
  const canReadUsers = roleHasCapability(role, "users.read");
  const canReadVouchers = roleHasCapability(role, "vouchers.read");
  const canReindexSearch = roleHasCapability(role, "search.reindex");

  const tabs = useMemo<Array<{ id: ConfigTab; label: string; count?: number }>>(() => {
    const list: Array<{ id: ConfigTab; label: string; count?: number }> = [];
    if (canReadPaymentMethods) list.push({ id: "thanh_toan", label: "Cổng thanh toán" });
    if (canReadApprovals) list.push({ id: "duyet_hai_nguoi", label: "Phê duyệt 2 người" });
    if (canReadFees) list.push({ id: "phi_san", label: "Biểu phí" });
    if (canReadCarriers) list.push({ id: "van_chuyen", label: "Hãng vận chuyển" });
    if (canReadCategories) {
      list.push({ id: "danh_muc", label: "Danh mục", count: initialCategories.length });
    }
    if (canReadUsers) list.push({ id: "nguoi_dung", label: "Người dùng" });
    if (canReadVouchers) {
      list.push({ id: "voucher_san", label: "Voucher sàn & Xu", count: initialVouchers.length });
    }
    return list;
  }, [
    canReadPaymentMethods,
    canReadApprovals,
    canReadFees,
    canReadCarriers,
    canReadCategories,
    canReadUsers,
    canReadVouchers,
    initialCategories.length,
    initialVouchers.length,
  ]);

  const [selectedTab, setSelectedTab] = useState<ConfigTab>(tabs[0]?.id ?? "nguoi_dung");
  const activeTab: ConfigTab = tabs.some((t) => t.id === selectedTab)
    ? selectedTab
    : (tabs[0]?.id ?? "nguoi_dung");

  // Read-only platform voucher history filter & Sheet
  const [voucherScopeFilter, setVoucherScopeFilter] = useState<"all" | AdminVoucherScope>("all");
  const [voucherQuery, setVoucherQuery] = useState("");
  const [drawerVoucher, setDrawerVoucher] = useState<GatewayVoucher | null>(null);

  const filteredVouchers = useMemo(() => {
    const q = voucherQuery.trim().toLowerCase();
    return initialVouchers.filter((v) => {
      if (voucherScopeFilter !== "all" && v.scope.toUpperCase() !== voucherScopeFilter) {
        return false;
      }
      if (!q) return true;
      return v.code.toLowerCase().includes(q) || v.name.toLowerCase().includes(q);
    });
  }, [initialVouchers, voucherScopeFilter, voucherQuery]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Cấu hình sàn"
        subtitle="Thiết lập cổng thanh toán, phê duyệt 2 người, biểu phí, vận chuyển, danh mục và nhân sự."
      />

      {tabs.length > 0 ? (
        <Tabs
          value={activeTab}
          onValueChange={(v) => setSelectedTab(v as ConfigTab)}
          className="space-y-5"
        >
          <div className="max-w-full overflow-x-auto pb-0.5">
            <TabsList aria-label="Nhóm cấu hình nền tảng">
              {tabs.map((t) => (
                <TabsTrigger key={t.id} value={t.id}>
                  <span>{t.label}</span>
                  {typeof t.count === "number" ? (
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {formatInt(t.count)}
                    </span>
                  ) : null}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          {canReadPaymentMethods ? (
            <TabsContent value="thanh_toan">
              <AdminPaymentMethodsPanel />
            </TabsContent>
          ) : null}

          {canReadApprovals ? (
            <TabsContent value="duyet_hai_nguoi">
              <AdminApprovalsPanel />
            </TabsContent>
          ) : null}

          {canReadFees ? (
            <TabsContent value="phi_san">
              <AdminFeesPanel />
            </TabsContent>
          ) : null}

          {canReadCarriers ? (
            <TabsContent value="van_chuyen">
              <AdminCarriersPanel />
            </TabsContent>
          ) : null}

          {canReadCategories ? (
            <TabsContent value="danh_muc">
              <AdminCategoriesPanel
                initialCategories={initialCategories}
                initialError={categoriesError}
              />
            </TabsContent>
          ) : null}

          {canReadUsers ? (
            <TabsContent value="nguoi_dung">
              <AdminUsersPanel initialQuery={initialQuery} />
            </TabsContent>
          ) : null}

          {/* Read-only history for Platform Vouchers & Coins (zero create/credit actions) */}
          {canReadVouchers ? (
            <TabsContent value="voucher_san">
              <section className="space-y-4" aria-label="Lịch sử voucher sàn và Xu">
                <Alert role="note">
                  <Info className="size-4" aria-hidden="true" />
                  <AlertDescription>
                    Chế độ chỉ đọc lịch sử. Voucher sàn và chương trình cấp Xu đã ngừng phát hành
                    mới.
                  </AlertDescription>
                </Alert>

                {vouchersError ? (
                  <ErrorState
                    title="Không thể tải lịch sử voucher sàn"
                    description={vouchersError}
                    onRetry={() => router.refresh()}
                  />
                ) : (
                  <IndexTable
                    toolbar={
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {formatInt(filteredVouchers.length)} voucher
                      </span>
                    }
                  >
                    <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
                      <div className="relative min-w-0 flex-1 sm:max-w-xs">
                        <Search
                          className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground"
                          aria-hidden="true"
                        />
                        <Input
                          type="search"
                          value={voucherQuery}
                          onChange={(e) => setVoucherQuery(e.target.value)}
                          placeholder="Tìm mã hoặc tên voucher..."
                          aria-label="Tìm lịch sử voucher sàn"
                          className="h-9 bg-card pl-8"
                        />
                      </div>
                      <div className="w-full sm:w-44">
                        <FilterSelect
                          value={voucherScopeFilter}
                          onValueChange={(v) =>
                            setVoucherScopeFilter(v as "all" | AdminVoucherScope)
                          }
                          ariaLabel="Lọc phạm vi voucher"
                          options={[
                            { value: "all", label: "Tất cả phạm vi" },
                            { value: "PLATFORM", label: "Voucher sàn" },
                            { value: "FREESHIP", label: "Freeship" },
                          ]}
                        />
                      </div>
                    </div>

                    {filteredVouchers.length === 0 ? (
                      <div className="p-4">
                        <EmptyState
                          icon={Ticket}
                          title="Chưa có lịch sử voucher sàn phù hợp"
                          className="shadow-none"
                        />
                      </div>
                    ) : (
                      <>
                        <div className="hidden md:block">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead>Mã &amp; chương trình</TableHead>
                                <TableHead>Phạm vi</TableHead>
                                <TableHead className="text-right">
                                  Mức giảm &amp; đơn tối thiểu
                                </TableHead>
                                <TableHead>Lượt sử dụng</TableHead>
                                <TableHead>Hiệu lực</TableHead>
                                <TableHead>Trạng thái</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {filteredVouchers.map((v) => {
                                const scopeMeta = getVoucherScopeMeta(v.scope);
                                const pct =
                                  v.quota > 0
                                    ? Math.min(100, Math.round((v.used_count / v.quota) * 100))
                                    : 0;
                                const discountSummary =
                                  v.discount_type.toUpperCase() === "PERCENT"
                                    ? `Giảm ${formatInt(v.discount_percent)}% (tối đa ${formatVnd(v.max_discount_vnd)})`
                                    : `Giảm ${formatVnd(v.value_vnd)}`;

                                return (
                                  <TableRow
                                    key={v.id}
                                    data-state={drawerVoucher?.id === v.id ? "selected" : undefined}
                                    className="cursor-pointer"
                                    onClick={() => setDrawerVoucher(v)}
                                  >
                                    <TableCell className="whitespace-normal">
                                      <button
                                        type="button"
                                        className="flex flex-col items-start text-left focus-visible:underline focus-visible:outline-none"
                                        onClick={(e) => {
                                          e.stopPropagation();
                                          setDrawerVoucher(v);
                                        }}
                                      >
                                        <span className="font-semibold text-foreground">
                                          {v.code}
                                        </span>
                                        <span className="text-xs text-muted-foreground">
                                          {v.name}
                                        </span>
                                      </button>
                                    </TableCell>
                                    <TableCell>
                                      <StatusBadge tone={pillToneToStatusTone(scopeMeta.tone)}>
                                        {scopeMeta.label}
                                      </StatusBadge>
                                    </TableCell>
                                    <TableCell className="text-right whitespace-normal tabular-nums">
                                      <div className="font-medium text-foreground">
                                        {discountSummary}
                                      </div>
                                      <div className="text-xs text-muted-foreground">
                                        Đơn từ {formatVnd(v.min_order_vnd)}
                                      </div>
                                    </TableCell>
                                    <TableCell>
                                      <div className="min-w-32 space-y-1.5">
                                        <div className="text-xs text-muted-foreground tabular-nums">
                                          {formatInt(v.used_count)} / {formatInt(v.quota)} lượt
                                        </div>
                                        <Progress value={pct} aria-label={`Đã dùng ${pct}%`} />
                                      </div>
                                    </TableCell>
                                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                                      <div>
                                        {v.starts_at
                                          ? `Từ ${formatDateTimeVi(v.starts_at)}`
                                          : EMPTY_DASH}
                                      </div>
                                      <div>
                                        {v.ends_at
                                          ? `Đến ${formatDateTimeVi(v.ends_at)}`
                                          : EMPTY_DASH}
                                      </div>
                                    </TableCell>
                                    <TableCell>
                                      <StatusBadge tone={v.is_active ? "success" : "neutral"}>
                                        {v.is_active ? "Đang hoạt động" : "Tạm ngưng"}
                                      </StatusBadge>
                                    </TableCell>
                                  </TableRow>
                                );
                              })}
                            </TableBody>
                          </Table>
                        </div>

                        <div className="md:hidden">
                          {filteredVouchers.map((v) => {
                            const scopeMeta = getVoucherScopeMeta(v.scope);
                            const discountSummary =
                              v.discount_type.toUpperCase() === "PERCENT"
                                ? `-${formatInt(v.discount_percent)}%`
                                : `-${formatVnd(v.value_vnd)}`;
                            return (
                              <IndexRowCard
                                key={v.id}
                                title={`${v.code}: ${v.name}`}
                                badge={
                                  <StatusBadge tone={pillToneToStatusTone(scopeMeta.tone)}>
                                    {scopeMeta.label}
                                  </StatusBadge>
                                }
                                meta={
                                  <>
                                    <span className="font-semibold text-foreground tabular-nums">
                                      {discountSummary}
                                    </span>
                                    <span>·</span>
                                    <span className="tabular-nums">
                                      {formatInt(v.used_count)}/{formatInt(v.quota)} lượt
                                    </span>
                                    <span>·</span>
                                    <span className="tabular-nums">
                                      đơn từ {formatVnd(v.min_order_vnd)}
                                    </span>
                                  </>
                                }
                                onClick={() => setDrawerVoucher(v)}
                              />
                            );
                          })}
                        </div>
                      </>
                    )}
                  </IndexTable>
                )}
              </section>
            </TabsContent>
          ) : null}
        </Tabs>
      ) : null}

      {canReindexSearch ? (
        <>
          <Separator />
          <AdminSearchReindexBlock />
        </>
      ) : null}

      <Sheet open={Boolean(drawerVoucher)} onOpenChange={(open) => !open && setDrawerVoucher(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-lg">
          {drawerVoucher ? (
            <>
              <SheetHeader className="border-b border-border pb-4">
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle>{drawerVoucher.code}</SheetTitle>
                  <StatusBadge tone={drawerVoucher.is_active ? "success" : "neutral"}>
                    {drawerVoucher.is_active ? "Đang hoạt động" : "Tạm ngưng"}
                  </StatusBadge>
                </div>
                {drawerVoucher.name ? (
                  <SheetDescription>{drawerVoucher.name}</SheetDescription>
                ) : null}
              </SheetHeader>

              <div className="px-4 pb-6">
                <DetailGrid>
                  <DetailItem label="Phạm vi">
                    {getVoucherScopeMeta(drawerVoucher.scope).label}
                  </DetailItem>
                  <DetailItem label="Mức giảm" numeric>
                    {drawerVoucher.discount_type.toUpperCase() === "PERCENT"
                      ? `${formatInt(drawerVoucher.discount_percent)}% (tối đa ${formatVnd(drawerVoucher.max_discount_vnd)})`
                      : formatVnd(drawerVoucher.value_vnd)}
                  </DetailItem>
                  <DetailItem label="Đơn tối thiểu" numeric>
                    {formatVnd(drawerVoucher.min_order_vnd)}
                  </DetailItem>
                  <DetailItem label="Lượt đã dùng / tổng" numeric>
                    {formatInt(drawerVoucher.used_count)} / {formatInt(drawerVoucher.quota)}
                  </DetailItem>
                  <DetailItem label="Giới hạn mỗi tài khoản" numeric>
                    {formatInt(drawerVoucher.per_user_limit)} lượt
                  </DetailItem>
                  <DetailItem label="Ngân sách tối đa" numeric>
                    {formatVnd(drawerVoucher.budget_vnd)}
                  </DetailItem>
                  <DetailItem label="Bắt đầu" numeric>
                    {drawerVoucher.starts_at
                      ? formatDateTimeVi(drawerVoucher.starts_at)
                      : EMPTY_DASH}
                  </DetailItem>
                  <DetailItem label="Kết thúc" numeric>
                    {drawerVoucher.ends_at ? formatDateTimeVi(drawerVoucher.ends_at) : EMPTY_DASH}
                  </DetailItem>
                </DetailGrid>
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}

export function AdminConfigClient(props: AdminConfigClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Cấu hình sàn" />}>
      <AdminStateGate
        title="Cấu hình sàn"
        emptyTitle="Chưa có dữ liệu cấu hình"
        emptyMessage="Không có mục cấu hình khả dụng cho vai trò hiện tại."
        errorTitle="Không thể tải cấu hình nền tảng"
        errorMessage="Dịch vụ cấu hình tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery }) => <ConfigWorkspace {...props} initialQuery={initialQuery} />}
      </AdminStateGate>
    </Suspense>
  );
}
