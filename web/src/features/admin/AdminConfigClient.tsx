"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { IconSearch, StateView } from "@/components";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { GatewayCategory, GatewayVoucher } from "@/lib/gateway-types";
import { EMPTY_DASH } from "./adminMeta";
import { AdminApprovalsPanel } from "./AdminApprovalsPanel";
import { AdminCarriersPanel } from "./AdminCarriersPanel";
import { AdminCategoriesPanel } from "./AdminCategoriesPanel";
import { AdminFeesPanel } from "./AdminFeesPanel";
import { AdminPaymentMethodsPanel } from "./AdminPaymentMethodsPanel";
import { useAdminContext } from "./AdminShell";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { AdminDrawer, SlidingTabBar } from "./AdminUi";
import { AdminUsersPanel } from "./AdminUsersPanel";
import { StatusPill } from "./StatusPill";
import { getVoucherScopeMeta, type AdminVoucherScope } from "./adminComplaintsTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

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

  // Read-only platform voucher history filter & drawer
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
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Cấu hình sàn</h1>
        </div>
      </header>

      {tabs.length > 0 && (
        <SlidingTabBar
          tabs={tabs}
          activeId={activeTab}
          onChange={setSelectedTab}
          ariaLabel="Nhóm cấu hình nền tảng"
        />
      )}

      {activeTab === "thanh_toan" && canReadPaymentMethods && <AdminPaymentMethodsPanel />}
      {activeTab === "duyet_hai_nguoi" && canReadApprovals && <AdminApprovalsPanel />}
      {activeTab === "phi_san" && canReadFees && <AdminFeesPanel />}
      {activeTab === "van_chuyen" && canReadCarriers && <AdminCarriersPanel />}
      {activeTab === "danh_muc" && canReadCategories && (
        <AdminCategoriesPanel
          initialCategories={initialCategories}
          initialError={categoriesError}
        />
      )}
      {activeTab === "nguoi_dung" && canReadUsers && (
        <AdminUsersPanel initialQuery={initialQuery} />
      )}

      {/* Read-only history for Platform Vouchers & Coins (zero create/credit actions) */}
      {activeTab === "voucher_san" && canReadVouchers && (
        <section className={styles.tabBody} aria-label="Lịch sử voucher sàn và Xu">
          <p className={styles.readOnlyNotice} role="note">
            Chế độ chỉ đọc lịch sử. Voucher sàn và chương trình cấp Xu đã ngừng phát hành mới.
          </p>

          {vouchersError ? (
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải lịch sử voucher sàn"
              message={vouchersError}
              actionLabel="Thử lại"
              onAction={() => router.refresh()}
            />
          ) : (
            <>
              <div className={shared.toolbar}>
                <div className={shared.toolbarLeft}>
                  <div className={shared.searchWrap}>
                    <span className={shared.searchIcon}>
                      <IconSearch size={14} />
                    </span>
                    <input
                      type="search"
                      className={shared.toolbarSearch}
                      value={voucherQuery}
                      onChange={(e) => setVoucherQuery(e.target.value)}
                      placeholder="Tìm mã hoặc tên voucher..."
                      aria-label="Tìm lịch sử voucher sàn"
                    />
                  </div>
                  <select
                    className={shared.toolbarSelect}
                    value={voucherScopeFilter}
                    onChange={(e) =>
                      setVoucherScopeFilter(e.target.value as "all" | AdminVoucherScope)
                    }
                    aria-label="Lọc phạm vi voucher"
                  >
                    <option value="all">Tất cả phạm vi</option>
                    <option value="PLATFORM">Voucher sàn</option>
                    <option value="FREESHIP">Freeship</option>
                  </select>
                </div>
                <div className={shared.toolbarRight}>
                  <span className={shared.resultCount}>
                    {formatInt(filteredVouchers.length)} voucher
                  </span>
                </div>
              </div>

              {filteredVouchers.length === 0 ? (
                <p className={shared.emptyRow}>Chưa có lịch sử voucher sàn phù hợp.</p>
              ) : (
                <>
                  <div className={shared.tableWrap}>
                    <table className={shared.dataTable}>
                      <thead>
                        <tr>
                          <th>Mã &amp; chương trình</th>
                          <th>Phạm vi</th>
                          <th className={shared.colNum}>Mức giảm &amp; đơn tối thiểu</th>
                          <th>Lượt sử dụng</th>
                          <th>Hiệu lực</th>
                          <th>Trạng thái</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredVouchers.map((v) => {
                          const scopeMeta = getVoucherScopeMeta(v.scope);
                          const ratio = v.quota > 0 ? Math.min(1, v.used_count / v.quota) : 0;
                          const discountSummary =
                            v.discount_type.toUpperCase() === "PERCENT"
                              ? `Giảm ${formatInt(v.discount_percent)}% (tối đa ${formatVnd(v.max_discount_vnd)})`
                              : `Giảm ${formatVnd(v.value_vnd)}`;

                          return (
                            <tr
                              key={v.id}
                              className={`${shared.rowInteractive} ${drawerVoucher?.id === v.id ? shared.rowSelected : ""}`}
                              onClick={() => setDrawerVoucher(v)}
                            >
                              <td>
                                <button
                                  type="button"
                                  className={shared.rowTriggerBtn}
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setDrawerVoucher(v);
                                  }}
                                >
                                  <span className={shared.cellPrimary}>{v.code}</span>
                                  <span className={shared.cellSub}>{v.name}</span>
                                </button>
                              </td>
                              <td>
                                <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                              </td>
                              <td className={shared.colNum}>
                                <div className={shared.cellPrimary}>{discountSummary}</div>
                                <div className={shared.cellSub}>
                                  Đơn từ {formatVnd(v.min_order_vnd)}
                                </div>
                              </td>
                              <td>
                                <div className={styles.quotaCell}>
                                  <span className={shared.cellSub}>
                                    {formatInt(v.used_count)} / {formatInt(v.quota)} lượt
                                  </span>
                                  <div className={shared.progressTrack} aria-hidden="true">
                                    <div
                                      className={shared.progressFill}
                                      style={{ transform: `scaleX(${ratio.toFixed(4)})` }}
                                    />
                                  </div>
                                </div>
                              </td>
                              <td>
                                <div className={shared.cellSub}>
                                  {v.starts_at ? `Từ ${formatDateTimeVi(v.starts_at)}` : EMPTY_DASH}
                                </div>
                                <div className={shared.cellSub}>
                                  {v.ends_at ? `Đến ${formatDateTimeVi(v.ends_at)}` : EMPTY_DASH}
                                </div>
                              </td>
                              <td>
                                <StatusPill
                                  label={v.is_active ? "Đang hoạt động" : "Tạm ngưng"}
                                  tone={v.is_active ? "positive" : "neutral"}
                                />
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>

                  <div className={shared.mobileList}>
                    {filteredVouchers.map((v) => {
                      const scopeMeta = getVoucherScopeMeta(v.scope);
                      const discountSummary =
                        v.discount_type.toUpperCase() === "PERCENT"
                          ? `-${formatInt(v.discount_percent)}%`
                          : `-${formatVnd(v.value_vnd)}`;
                      return (
                        <article key={v.id} className={shared.mobileItem}>
                          <button
                            type="button"
                            className={shared.mobileItemBtn}
                            onClick={() => setDrawerVoucher(v)}
                          >
                            <span className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {v.code}: {v.name}
                              </span>
                              <span className={shared.mobileMetric}>{discountSummary}</span>
                            </span>
                            <span className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                {formatInt(v.used_count)}/{formatInt(v.quota)} lượt, đơn từ{" "}
                                {formatVnd(v.min_order_vnd)}
                              </span>
                              <StatusPill label={scopeMeta.label} tone={scopeMeta.tone} />
                            </span>
                          </button>
                        </article>
                      );
                    })}
                  </div>
                </>
              )}
            </>
          )}
        </section>
      )}

      <AdminDrawer
        open={Boolean(drawerVoucher)}
        onClose={() => setDrawerVoucher(null)}
        title={drawerVoucher?.code ?? ""}
        subtitle={drawerVoucher?.name}
        headerBadge={
          drawerVoucher ? (
            <StatusPill
              label={drawerVoucher.is_active ? "Đang hoạt động" : "Tạm ngưng"}
              tone={drawerVoucher.is_active ? "positive" : "neutral"}
            />
          ) : undefined
        }
      >
        {drawerVoucher && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Phạm vi</span>
                <span className={shared.detailVal}>
                  {getVoucherScopeMeta(drawerVoucher.scope).label}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mức giảm</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerVoucher.discount_type.toUpperCase() === "PERCENT"
                    ? `${formatInt(drawerVoucher.discount_percent)}% (tối đa ${formatVnd(drawerVoucher.max_discount_vnd)})`
                    : formatVnd(drawerVoucher.value_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Đơn tối thiểu</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerVoucher.min_order_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Lượt đã dùng / tổng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(drawerVoucher.used_count)} / {formatInt(drawerVoucher.quota)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Giới hạn mỗi tài khoản</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatInt(drawerVoucher.per_user_limit)} lượt
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Ngân sách tối đa</span>
                <span className={`${shared.detailValPlain} num`}>
                  {formatVnd(drawerVoucher.budget_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Bắt đầu</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerVoucher.starts_at ? formatDateTimeVi(drawerVoucher.starts_at) : EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Kết thúc</span>
                <span className={`${shared.detailValPlain} num`}>
                  {drawerVoucher.ends_at ? formatDateTimeVi(drawerVoucher.ends_at) : EMPTY_DASH}
                </span>
              </div>
            </div>
          </div>
        )}
      </AdminDrawer>
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
