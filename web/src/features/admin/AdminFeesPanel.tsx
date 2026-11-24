"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayCategory,
  GatewayCategoryFeeSchedule,
  GatewayPaymentFeeConfigItem,
  GatewayPaymentFeeConfigResponse,
  GatewayPaymentMethod,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { formatBpsPercent, formatDateTimeVi, formatInt } from "@/lib/format";
import { StatusPill } from "./StatusPill";
import { useAdminCapability } from "./AdminUserContext";
import shared from "./adminShared.module.css";

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function isEffectiveFuture(iso?: string): boolean {
  if (!iso) return false;
  const ms = Date.parse(iso);
  return Number.isFinite(ms) && ms > Date.now();
}

export function AdminFeesPanel() {
  const { showToast } = useMockStore();
  const canReadCatFees = useAdminCapability("fees.category.read");
  const canWriteCatFees = useAdminCapability("fees.category.write");
  const canReadPayFees = useAdminCapability("fees.payment.read");
  const canWritePayFees = useAdminCapability("fees.payment.write");
  const canReadCategories = useAdminCapability("categories.read");

  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [catSchedules, setCatSchedules] = useState<GatewayCategoryFeeSchedule[]>([]);
  const [paymentFees, setPaymentFees] = useState<GatewayPaymentFeeConfigItem[]>([]);
  const [activePaymentFeeIds, setActivePaymentFeeIds] = useState<Set<string>>(() => new Set());
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Filter & create category fee schedule
  const [catFilterId, setCatFilterId] = useState("");
  const [catId, setCatId] = useState("");
  const [catBpsStr, setCatBpsStr] = useState("500");
  const [catEffectiveFrom, setCatEffectiveFrom] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 24 * 3600 * 1000))
  );
  const [catNote, setCatNote] = useState("");
  const [catSaving, setCatSaving] = useState(false);
  const [catFormError, setCatFormError] = useState<string | null>(null);

  // Create payment fee schedule
  const [payMethod, setPayMethod] = useState<GatewayPaymentMethod>("VIETQR");
  const [payBpsStr, setPayBpsStr] = useState("100");
  const [payEffectiveFrom, setPayEffectiveFrom] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 24 * 3600 * 1000))
  );
  const [payNote, setPayNote] = useState("");
  const [paySaving, setPaySaving] = useState(false);
  const [payFormError, setPayFormError] = useState<string | null>(null);

  const loadFees = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const catQs = new URLSearchParams({ page: "1", page_size: "100" });
      if (catFilterId) catQs.set("category_id", catFilterId);

      const [catsRes, catFeesRes, payFeesRes] = await Promise.all([
        canReadCategories
          ? api<{ categories: GatewayCategory[] }>("admin/categories").catch(() => ({
              categories: [] as GatewayCategory[],
            }))
          : Promise.resolve({ categories: [] as GatewayCategory[] }),
        canReadCatFees
          ? api<PaginatedResponse<GatewayCategoryFeeSchedule>>(
              `admin/fees/categories?${catQs.toString()}`
            )
          : Promise.resolve({
              items: [] as GatewayCategoryFeeSchedule[],
              page: 1,
              page_size: 100,
              total: 0,
            }),
        canReadPayFees
          ? api<GatewayPaymentFeeConfigResponse>("admin/fees/payment?include_history=true")
          : Promise.resolve({ active_configs: [], history: [] } as GatewayPaymentFeeConfigResponse),
      ]);

      const catList = catsRes.categories ?? [];
      setCategories(catList);
      if (catList.length > 0) {
        setCatId((prev) => prev || catList[0].id);
      }
      setCatSchedules(catFeesRes.items ?? []);
      const activeList = payFeesRes.active_configs ?? [];
      const historyList = payFeesRes.history ?? [];
      const activeIds = new Set(activeList.map((c) => c.id));
      const mergedMap = new Map<string, GatewayPaymentFeeConfigItem>();
      for (const item of [...activeList, ...historyList]) {
        if (item?.id && !mergedMap.has(item.id)) {
          mergedMap.set(item.id, item);
        }
      }
      const mergedList = Array.from(mergedMap.values()).sort((a, b) =>
        (b.effective_from || "").localeCompare(a.effective_from || "")
      );
      setActivePaymentFeeIds(activeIds);
      setPaymentFees(mergedList);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải biểu phí sàn và phí thanh toán lúc này."));
    } finally {
      setLoading(false);
    }
  }, [catFilterId, canReadCategories, canReadCatFees, canReadPayFees]);

  useEffect(() => {
    void loadFees();
  }, [loadFees]);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) {
      map.set(c.id, c.name);
    }
    return map;
  }, [categories]);

  const handleSaveCategoryFee = async (e: React.FormEvent) => {
    e.preventDefault();
    const bps = Math.trunc(Number(catBpsStr));
    if (!catId) {
      setCatFormError("Vui lòng chọn danh mục cần đặt biểu phí.");
      return;
    }
    if (!Number.isFinite(bps) || bps < 0 || bps > 5000) {
      setCatFormError("Phí hoa hồng hợp lệ từ 0 đến 5000 bps (0% – 50%).");
      return;
    }
    const effDate = new Date(catEffectiveFrom);
    if (Number.isNaN(effDate.getTime())) {
      setCatFormError("Vui lòng chọn thời điểm bắt đầu hiệu lực hợp lệ.");
      return;
    }
    setCatFormError(null);
    setCatSaving(true);
    try {
      await api<{ schedule: GatewayCategoryFeeSchedule }>(
        `admin/fees/categories/${encodeURIComponent(catId)}`,
        {
          method: "POST",
          json: {
            commission_rate_bps: bps,
            effective_from: effDate.toISOString(),
            note: catNote.trim(),
          },
        }
      );
      setCatNote("");
      await loadFees();
      showToast(
        `Đã lên lịch phí hoa hồng ${formatBpsPercent(bps)} cho danh mục ${categoryNameById.get(catId) ?? catId}.`
      );
    } catch (err) {
      setCatFormError(getErrorMessage(err, "Không thể lưu lịch phí danh mục."));
    } finally {
      setCatSaving(false);
    }
  };

  const handleSavePaymentFee = async (e: React.FormEvent) => {
    e.preventDefault();
    const bps = Math.trunc(Number(payBpsStr));
    if (!Number.isFinite(bps) || bps < 0 || bps > 2000) {
      setPayFormError("Phí phương thức thanh toán hợp lệ từ 0 đến 2000 bps (0% – 20%).");
      return;
    }
    const effDate = new Date(payEffectiveFrom);
    if (Number.isNaN(effDate.getTime())) {
      setPayFormError("Vui lòng chọn thời điểm bắt đầu hiệu lực hợp lệ.");
      return;
    }
    setPayFormError(null);
    setPaySaving(true);
    try {
      await api<{ config: GatewayPaymentFeeConfigItem }>("admin/fees/payment", {
        method: "POST",
        json: {
          payment_method: payMethod,
          fee_bps: bps,
          effective_from: effDate.toISOString(),
          note: payNote.trim(),
        },
      });
      setPayNote("");
      await loadFees();
      showToast(`Đã lên lịch phí thanh toán ${payMethod}: ${formatBpsPercent(bps)}.`);
    } catch (err) {
      setPayFormError(getErrorMessage(err, "Không thể lưu lịch phí thanh toán."));
    } finally {
      setPaySaving(false);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải lịch biểu phí hoa hồng danh mục và phí thanh toán..."
          rows={5}
        />
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải biểu phí sàn"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadFees()}
        />
      </div>
    );
  }

  const parsedCatBps = Math.trunc(Number(catBpsStr) || 0);
  const parsedPayBps = Math.trunc(Number(payBpsStr) || 0);

  return (
    <div className={shared.sectionStack}>
      {/* Category commission fee schedules */}
      <section className={shared.panel} aria-labelledby="cat-fee-heading">
        <div className={shared.panelHeader}>
          <div>
            <h2 id="cat-fee-heading" className={shared.panelTitle}>
              Biểu phí hoa hồng theo danh mục ({formatInt(catSchedules.length)})
            </h2>
            <span className={shared.panelMeta}>
              Đơn vị bps (100 bps = 1%) · Chỉ áp dụng cho đơn tạo mới từ thời điểm hiệu lực
            </span>
          </div>
          <div className={shared.detailButtons}>
            <select
              className={shared.toolbarSelect}
              value={catFilterId}
              onChange={(e) => setCatFilterId(e.target.value)}
              aria-label="Lọc lịch phí theo danh mục"
            >
              <option value="">Tất cả danh mục</option>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <Button variant="outline" size="sm" shape="app" onClick={() => void loadFees()}>
              Làm mới
            </Button>
          </div>
        </div>

        {canWriteCatFees && (
          <form onSubmit={(e) => void handleSaveCategoryFee(e)} className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <label htmlFor="fee-cat-id" className={shared.fieldLabel}>
                  Danh mục áp dụng
                </label>
                <select
                  id="fee-cat-id"
                  className={shared.toolbarSelect}
                  style={{ height: 38 }}
                  value={catId}
                  onChange={(e) => setCatId(e.target.value)}
                >
                  {categories.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name} (hiện tại {formatBpsPercent(c.commission_rate_bps ?? 0)})
                    </option>
                  ))}
                </select>
              </div>
              <div className={shared.detailField}>
                <label htmlFor="fee-cat-bps" className={shared.fieldLabel}>
                  Mức hoa hồng (bps — {formatBpsPercent(parsedCatBps)})
                </label>
                <input
                  id="fee-cat-bps"
                  type="number"
                  min={0}
                  max={5000}
                  step={1}
                  className={shared.textInput}
                  value={catBpsStr}
                  onChange={(e) => setCatBpsStr(e.target.value)}
                  required
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="fee-cat-eff" className={shared.fieldLabel}>
                  Bắt đầu hiệu lực
                </label>
                <input
                  id="fee-cat-eff"
                  type="datetime-local"
                  className={shared.textInput}
                  value={catEffectiveFrom}
                  onChange={(e) => setCatEffectiveFrom(e.target.value)}
                  required
                />
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <label htmlFor="fee-cat-note" className={shared.fieldLabel}>
                  Ghi chú điều chỉnh (lưu vào nhật ký kiểm toán)
                </label>
                <input
                  id="fee-cat-note"
                  type="text"
                  className={shared.textInput}
                  value={catNote}
                  onChange={(e) => setCatNote(e.target.value)}
                  placeholder="VD: Điều chỉnh biểu phí ngành hàng Quý 4..."
                />
              </div>
            </div>

            {catFormError && (
              <p className={shared.validationError} role="alert">
                {catFormError}
              </p>
            )}

            <div className={shared.detailActionBar}>
              <span className={shared.savedStamp}>
                Hệ thống không ghi đè đơn hàng cũ; lịch mới có hiệu lực đúng mốc thời gian chọn.
              </span>
              <Button type="submit" variant="primary" size="sm" shape="app" disabled={catSaving}>
                {catSaving ? "Đang lưu..." : "Lên lịch phí danh mục"}
              </Button>
            </div>
          </form>
        )}

        {catSchedules.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có lịch biểu phí danh mục nào được ghi nhận.</p>
        ) : (
          <>
            <div className={shared.tableWrap} style={{ marginTop: 14 }}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Danh mục</th>
                    <th className={shared.colNum}>Mức hoa hồng</th>
                    <th>Hiệu lực từ</th>
                    <th>Trạng thái</th>
                    <th>Ghi chú</th>
                  </tr>
                </thead>
                <tbody>
                  {catSchedules.map((sch) => {
                    const upcoming = isEffectiveFuture(sch.effective_from);
                    return (
                      <tr key={sch.id}>
                        <td>
                          <div className={shared.cellPrimary}>
                            {sch.category_name ||
                              categoryNameById.get(sch.category_id) ||
                              sch.category_id}
                          </div>
                          {sch.created_at && (
                            <div className={shared.cellSub}>
                              Tạo lúc {formatDateTimeVi(sch.created_at)}
                            </div>
                          )}
                        </td>
                        <td className={shared.colNum}>
                          <strong>{formatBpsPercent(sch.commission_rate_bps)}</strong>
                          <div className={shared.cellSub}>
                            {formatInt(sch.commission_rate_bps)} bps
                          </div>
                        </td>
                        <td>{formatDateTimeVi(sch.effective_from)}</td>
                        <td>
                          <StatusPill
                            label={upcoming ? "Chờ hiệu lực" : "Đang / đã hiệu lực"}
                            tone={upcoming ? "warning" : "positive"}
                          />
                        </td>
                        <td>{sch.note || "—"}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {catSchedules.map((sch) => {
                const upcoming = isEffectiveFuture(sch.effective_from);
                return (
                  <div key={sch.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {sch.category_name ||
                          categoryNameById.get(sch.category_id) ||
                          sch.category_id}
                      </span>
                      <span className={shared.mobileMetric}>
                        {formatBpsPercent(sch.commission_rate_bps)}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        Từ {formatDateTimeVi(sch.effective_from)}
                      </span>
                      <StatusPill
                        label={upcoming ? "Chờ hiệu lực" : "Hiệu lực"}
                        tone={upcoming ? "warning" : "positive"}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </section>

      {/* Payment method fees (COD / VIETQR / ZALOPAY) */}
      <section className={shared.panel} aria-labelledby="pay-fee-heading">
        <div className={shared.panelHeader}>
          <div>
            <h2 id="pay-fee-heading" className={shared.panelTitle}>
              Phí thanh toán theo phương thức (COD / VietQR / ZaloPay) (
              {formatInt(paymentFees.length)})
            </h2>
            <span className={shared.panelMeta}>
              Tính theo tổng thanh toán của đơn con và hạch toán vào sổ cái ghi kép
            </span>
          </div>
        </div>

        {canWritePayFees && (
          <form onSubmit={(e) => void handleSavePaymentFee(e)} className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <label htmlFor="fee-pay-method" className={shared.fieldLabel}>
                  Phương thức thanh toán
                </label>
                <select
                  id="fee-pay-method"
                  className={shared.toolbarSelect}
                  style={{ height: 38 }}
                  value={payMethod}
                  onChange={(e) => setPayMethod(e.target.value as GatewayPaymentMethod)}
                >
                  <option value="VIETQR">Chuyển khoản VietQR</option>
                  <option value="ZALOPAY">Ví điện tử ZaloPay</option>
                  <option value="COD">Thu hộ khi nhận hàng (COD)</option>
                </select>
              </div>
              <div className={shared.detailField}>
                <label htmlFor="fee-pay-bps" className={shared.fieldLabel}>
                  Mức phí (bps — {formatBpsPercent(parsedPayBps)})
                </label>
                <input
                  id="fee-pay-bps"
                  type="number"
                  min={0}
                  max={2000}
                  step={1}
                  className={shared.textInput}
                  value={payBpsStr}
                  onChange={(e) => setPayBpsStr(e.target.value)}
                  required
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="fee-pay-eff" className={shared.fieldLabel}>
                  Bắt đầu hiệu lực
                </label>
                <input
                  id="fee-pay-eff"
                  type="datetime-local"
                  className={shared.textInput}
                  value={payEffectiveFrom}
                  onChange={(e) => setPayEffectiveFrom(e.target.value)}
                  required
                />
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <label htmlFor="fee-pay-note" className={shared.fieldLabel}>
                  Ghi chú thay đổi phí thanh toán
                </label>
                <input
                  id="fee-pay-note"
                  type="text"
                  className={shared.textInput}
                  value={payNote}
                  onChange={(e) => setPayNote(e.target.value)}
                  placeholder="VD: Biểu phí xử lý giao dịch ngân hàng..."
                />
              </div>
            </div>

            {payFormError && (
              <p className={shared.validationError} role="alert">
                {payFormError}
              </p>
            )}

            <div className={shared.detailActionBar}>
              <span className={shared.savedStamp}>
                Lịch sử thay đổi được lưu bất biến phục vụ kiểm toán tài chính.
              </span>
              <Button type="submit" variant="primary" size="sm" shape="app" disabled={paySaving}>
                {paySaving ? "Đang lưu..." : "Lên lịch phí thanh toán"}
              </Button>
            </div>
          </form>
        )}

        {paymentFees.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có cấu hình lịch phí thanh toán nào.</p>
        ) : (
          <>
            <div className={shared.tableWrap} style={{ marginTop: 14 }}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Phương thức</th>
                    <th className={shared.colNum}>Mức phí</th>
                    <th>Hiệu lực từ</th>
                    <th>Trạng thái</th>
                    <th>Ghi chú</th>
                  </tr>
                </thead>
                <tbody>
                  {paymentFees.map((cfg) => {
                    const upcoming = isEffectiveFuture(cfg.effective_from);
                    const isActive = activePaymentFeeIds.has(cfg.id);
                    const statusLabel = isActive
                      ? "Đang áp dụng"
                      : upcoming
                        ? "Chờ hiệu lực"
                        : "Lịch sử";
                    const statusTone = isActive ? "positive" : upcoming ? "warning" : "neutral";
                    return (
                      <tr key={cfg.id}>
                        <td>
                          <div className={shared.cellPrimary}>{cfg.payment_method}</div>
                          {cfg.created_at && (
                            <div className={shared.cellSub}>
                              Tạo lúc {formatDateTimeVi(cfg.created_at)}
                            </div>
                          )}
                        </td>
                        <td className={shared.colNum}>
                          <strong>{formatBpsPercent(cfg.fee_bps)}</strong>
                          <div className={shared.cellSub}>{formatInt(cfg.fee_bps)} bps</div>
                        </td>
                        <td>{formatDateTimeVi(cfg.effective_from)}</td>
                        <td>
                          <StatusPill label={statusLabel} tone={statusTone} />
                        </td>
                        <td>{cfg.note || "—"}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {paymentFees.map((cfg) => {
                const upcoming = isEffectiveFuture(cfg.effective_from);
                const isActive = activePaymentFeeIds.has(cfg.id);
                const statusLabel = isActive
                  ? "Đang áp dụng"
                  : upcoming
                    ? "Chờ hiệu lực"
                    : "Lịch sử";
                const statusTone = isActive ? "positive" : upcoming ? "warning" : "neutral";
                return (
                  <div key={cfg.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{cfg.payment_method}</span>
                      <span className={shared.mobileMetric}>{formatBpsPercent(cfg.fee_bps)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        Từ {formatDateTimeVi(cfg.effective_from)}
                      </span>
                      <StatusPill label={statusLabel} tone={statusTone} />
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </section>
    </div>
  );
}
