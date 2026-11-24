"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayProduct,
  GatewayShopDiscountProgram,
  GatewayShopDiscountProgramResponse,
  GatewaySKU,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import { Button, StateView } from "@/components";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import {
  DISCOUNT_PROGRAM_STATUS_META,
  getProductCoverImage,
  translateShopDiscountWarning,
} from "./types";
import common from "./SellerCommon.module.css";
import styles from "./PromotionsExtra.module.css";

interface Props {
  shopName: string;
  products: GatewayProduct[];
  showForm: boolean;
  onOpenForm: () => void;
  onCloseForm: () => void;
  onNotify: (msg: string) => void;
  onCountChange?: (count: number) => void;
}

interface DraftItemRow {
  sku_id: string;
  discounted_price_vnd: string;
  per_user_limit: string;
}

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function pillClassForTone(tone: "neutral" | "warn" | "pos" | "neg"): string {
  if (tone === "pos") return `${common.pill} ${common.pillPos}`;
  if (tone === "warn") return `${common.pill} ${common.pillWarn}`;
  if (tone === "neg") return `${common.pill} ${common.pillNeg}`;
  return common.pill;
}

export function PromotionsDiscountProgramsTab({
  shopName,
  products,
  showForm,
  onOpenForm,
  onCloseForm,
  onNotify,
  onCountChange,
}: Props) {
  const [programs, setPrograms] = useState<GatewayShopDiscountProgram[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<"ALL" | "ACTIVE" | "SCHEDULED" | "ENDED">("ALL");
  const [serverWarnings, setServerWarnings] = useState<string[]>([]);

  // Create / Edit state
  const [editingId, setEditingId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [startsAt, setStartsAt] = useState(() => toDatetimeLocalValue(new Date()));
  const [endsAt, setEndsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000))
  );
  const [draftItems, setDraftItems] = useState<DraftItemRow[]>([]);
  const [quickPct, setQuickPct] = useState("15");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Inline end confirmation
  const [confirmEndId, setConfirmEndId] = useState<string | null>(null);
  const [endingId, setEndingId] = useState<string | null>(null);

  const skuLookup = useMemo(() => {
    const map = new Map<string, { product: GatewayProduct; sku: GatewaySKU }>();
    for (const product of products) {
      for (const sku of product.skus ?? []) {
        map.set(sku.id, { product, sku });
      }
    }
    return map;
  }, [products]);

  const selectableSkus = useMemo(
    () =>
      products
        .filter((p) => p.status === "APPROVED" || p.status === "PENDING")
        .flatMap((product) =>
          (product.skus ?? []).filter((s) => s.is_active).map((sku) => ({ product, sku }))
        ),
    [products]
  );

  const loadPrograms = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<PaginatedResponse<GatewayShopDiscountProgram>>(
        "seller/discount-programs?page=1&page_size=50"
      );
      const list = res.items ?? [];
      setPrograms(list);
      onCountChange?.(list.length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách chương trình giảm giá của shop."));
    } finally {
      setLoading(false);
    }
  }, [onCountChange]);

  useEffect(() => {
    void loadPrograms();
  }, [loadPrograms]);

  useEffect(() => {
    if (showForm && !editingId && draftItems.length === 0 && selectableSkus.length > 0) {
      const first = selectableSkus[0].sku;
      const suggested = Math.max(1000, Math.trunc((first.price_vnd * 85) / 100));
      setDraftItems([
        {
          sku_id: first.id,
          discounted_price_vnd: String(suggested),
          per_user_limit: "0",
        },
      ]);
    }
  }, [showForm, editingId, draftItems.length, selectableSkus]);

  const resetForm = useCallback(() => {
    setEditingId(null);
    setName("");
    setStartsAt(toDatetimeLocalValue(new Date()));
    setEndsAt(toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000)));
    setDraftItems([]);
    setFormError(null);
  }, []);

  useEffect(() => {
    if (!showForm) {
      resetForm();
    }
  }, [showForm, resetForm]);

  const handleStartEdit = (program: GatewayShopDiscountProgram) => {
    setEditingId(program.id);
    setName(program.name);
    setStartsAt(
      program.starts_at
        ? toDatetimeLocalValue(new Date(program.starts_at))
        : toDatetimeLocalValue(new Date())
    );
    setEndsAt(
      program.ends_at
        ? toDatetimeLocalValue(new Date(program.ends_at))
        : toDatetimeLocalValue(new Date(Date.now() + 7 * 24 * 3600 * 1000))
    );
    setDraftItems(
      (program.items ?? []).map((it) => ({
        sku_id: it.sku_id,
        discounted_price_vnd: String(it.discounted_price_vnd),
        per_user_limit: String(it.per_user_limit ?? 0),
      }))
    );
    setFormError(null);
    onOpenForm();
  };

  const handleAddDraftRow = () => {
    const usedIds = new Set(draftItems.map((r) => r.sku_id));
    const next = selectableSkus.find((x) => !usedIds.has(x.sku.id)) ?? selectableSkus[0];
    if (!next) return;
    const pct = Math.max(1, Math.min(90, Math.trunc(Number(quickPct) || 15)));
    const discounted = Math.max(1000, Math.trunc((next.sku.price_vnd * (100 - pct)) / 100));
    setDraftItems((prev) => [
      ...prev,
      {
        sku_id: next.sku.id,
        discounted_price_vnd: String(discounted),
        per_user_limit: "0",
      },
    ]);
  };

  const handleApplyQuickPct = () => {
    const pct = Math.max(1, Math.min(90, Math.trunc(Number(quickPct) || 15)));
    setDraftItems((prev) =>
      prev.map((row) => {
        const found = skuLookup.get(row.sku_id);
        if (!found) return row;
        const discounted = Math.max(1000, Math.trunc((found.sku.price_vnd * (100 - pct)) / 100));
        return { ...row, discounted_price_vnd: String(discounted) };
      })
    );
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = name.trim();
    if (cleanName.length < 3) {
      setFormError("Tên chương trình giảm giá cần tối thiểu 3 ký tự.");
      return;
    }
    const startDate = new Date(startsAt);
    const endDate = new Date(endsAt);
    if (
      Number.isNaN(startDate.getTime()) ||
      Number.isNaN(endDate.getTime()) ||
      endDate <= startDate
    ) {
      setFormError("Thời điểm kết thúc phải sau thời điểm bắt đầu.");
      return;
    }
    if (draftItems.length === 0) {
      setFormError("Vui lòng chọn ít nhất 1 biến thể SKU tham gia chương trình.");
      return;
    }

    const seenSkus = new Set<string>();
    const payloadItems: Array<{
      sku_id: string;
      discounted_price_vnd: number;
      per_user_limit: number;
    }> = [];

    for (const row of draftItems) {
      if (!row.sku_id) {
        setFormError("Có dòng chưa chọn biến thể SKU.");
        return;
      }
      if (seenSkus.has(row.sku_id)) {
        setFormError("Mỗi biến thể SKU chỉ được khai báo một lần trong cùng chương trình.");
        return;
      }
      seenSkus.add(row.sku_id);

      const discounted = Math.trunc(Number(row.discounted_price_vnd) || 0);
      const perUser = Math.max(0, Math.trunc(Number(row.per_user_limit) || 0));
      const found = skuLookup.get(row.sku_id);
      if (discounted <= 0) {
        setFormError("Giá sau giảm phải là số nguyên VND lớn hơn 0.");
        return;
      }
      if (found && discounted >= found.sku.price_vnd) {
        setFormError(
          `Giá giảm của ${found.sku.sku_code} phải thấp hơn giá niêm yết (${formatVnd(found.sku.price_vnd)}).`
        );
        return;
      }
      payloadItems.push({
        sku_id: row.sku_id,
        discounted_price_vnd: discounted,
        per_user_limit: perUser,
      });
    }

    setFormError(null);
    setSubmitting(true);
    try {
      const res = editingId
        ? await api<GatewayShopDiscountProgramResponse>(
            `seller/discount-programs/${encodeURIComponent(editingId)}`,
            {
              method: "PUT",
              json: {
                name: cleanName,
                starts_at: startDate.toISOString(),
                ends_at: endDate.toISOString(),
                items: payloadItems,
                replace_items: true,
              },
            }
          )
        : await api<GatewayShopDiscountProgramResponse>("seller/discount-programs", {
            method: "POST",
            json: {
              name: cleanName,
              starts_at: startDate.toISOString(),
              ends_at: endDate.toISOString(),
              items: payloadItems,
            },
          });

      setServerWarnings(res.warnings ?? []);
      resetForm();
      onCloseForm();
      await loadPrograms();
      onNotify(
        editingId
          ? `Đã cập nhật chương trình giảm giá "${cleanName}".`
          : `Đã tạo chương trình giảm giá "${cleanName}" cho ${formatInt(payloadItems.length)} SKU.`
      );
    } catch (err) {
      setFormError(
        formatPromotionErrorMessage(
          err,
          "seller_discount_program",
          "Không thể lưu chương trình giảm giá lúc này."
        )
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleEndProgram = async (program: GatewayShopDiscountProgram) => {
    setEndingId(program.id);
    try {
      await api<{ program: GatewayShopDiscountProgram }>(
        `seller/discount-programs/${encodeURIComponent(program.id)}/end`,
        { method: "POST" }
      );
      setConfirmEndId(null);
      await loadPrograms();
      onNotify(`Đã kết thúc chương trình "${program.name}".`);
    } catch (err) {
      onNotify(
        formatPromotionErrorMessage(
          err,
          "seller_discount_program",
          "Không thể kết thúc chương trình giảm giá."
        )
      );
    } finally {
      setEndingId(null);
    }
  };

  const filteredPrograms = useMemo(() => {
    if (statusFilter === "ALL") return programs;
    if (statusFilter === "ENDED") {
      return programs.filter((p) => p.status === "ENDED" || p.status === "CANCELLED");
    }
    return programs.filter((p) => p.status === statusFilter);
  }, [programs, statusFilter]);

  const activeCount = programs.filter((p) => p.status === "ACTIVE").length;
  const scheduledCount = programs.filter((p) => p.status === "SCHEDULED").length;
  const totalSkuItems = programs
    .filter((p) => p.status === "ACTIVE" || p.status === "SCHEDULED")
    .reduce((sum, p) => sum + (p.items?.length ?? 0), 0);

  if (loading) {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang tải danh sách chương trình giảm giá gian hàng..."
        rows={4}
      />
    );
  }

  if (errorMsg) {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải chương trình giảm giá gian hàng"
        message={errorMsg}
        actionLabel="Thử lại"
        onAction={() => void loadPrograms()}
      />
    );
  }

  return (
    <div className={styles.sectionStack}>
      <section className={common.balanceStrip3} aria-label="Tổng hợp chương trình giảm giá shop">
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Đang áp dụng</span>
          <span
            className={`${common.balanceValue} ${activeCount === 0 ? common.balanceValueZero : ""}`}
          >
            {formatInt(activeCount)}
          </span>
          <span className={common.balanceCaption}>Hiển thị giá gốc gạch thật trên sàn</span>
        </div>
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Đã lên lịch</span>
          <span
            className={`${common.balanceValue} ${scheduledCount === 0 ? common.balanceValueZero : ""}`}
          >
            {formatInt(scheduledCount)}
          </span>
          <span className={common.balanceCaption}>Tự động kích hoạt khi đến giờ bắt đầu</span>
        </div>
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>SKU đang tham gia</span>
          <span
            className={`${common.balanceValue} ${totalSkuItems === 0 ? common.balanceValueZero : ""}`}
          >
            {formatInt(totalSkuItems)}
          </span>
          <span className={common.balanceCaption}>
            Flash sale sẽ tự động ưu tiên nếu trùng khung giờ
          </span>
        </div>
      </section>

      {serverWarnings.length > 0 && (
        <section className={common.card} role="status" aria-label="Cảnh báo giá từ hệ thống">
          <div className={common.cardHeader}>
            <span className={`${common.pill} ${common.pillWarn}`}>
              Cảnh báo đối chiếu giá 30 ngày
            </span>
            <button type="button" className={common.cardLink} onClick={() => setServerWarnings([])}>
              Ẩn thông báo
            </button>
          </div>
          <ul style={{ margin: 0, paddingLeft: 18, color: "var(--ink)", fontSize: 13 }}>
            {serverWarnings.map((w, idx) => (
              <li key={idx} style={{ marginBottom: 4 }}>
                {translateShopDiscountWarning(w, skuLookup)}
              </li>
            ))}
          </ul>
        </section>
      )}

      {showForm && (
        <form
          className={styles.inlineFormCard}
          onSubmit={(e) => void handleSubmit(e)}
          aria-label="Biểu mẫu chương trình giảm giá shop"
        >
          <div className={common.cardHeader}>
            <div>
              <h2 className={common.cardTitle}>
                {editingId
                  ? "Chỉnh sửa chương trình giảm giá gian hàng"
                  : "Tạo chương trình giảm giá gian hàng mới"}
              </h2>
              <span className={common.cardMeta}>
                Mỗi SKU chỉ thuộc 1 chương trình đang chạy tại một thời điểm · Giá giảm trực tiếp
                trên trang sản phẩm và giỏ hàng
              </span>
            </div>
          </div>

          {formError && (
            <p className={common.fieldError} role="alert" style={{ marginBottom: 12 }}>
              {formError}
            </p>
          )}

          {selectableSkus.length === 0 ? (
            <p className={common.cardMeta}>
              Gian hàng chưa có biến thể SKU đang mở bán để đưa vào chương trình giảm giá.
            </p>
          ) : (
            <>
              <div className={styles.formGrid3}>
                <div className={common.field}>
                  <label htmlFor="dp-name" className={common.fieldLabel}>
                    Tên chương trình
                  </label>
                  <input
                    id="dp-name"
                    type="text"
                    className={common.textInput}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Ví dụ: Ưu đãi cuối tuần giảm 15%"
                    required
                  />
                </div>
                <div className={common.field}>
                  <label htmlFor="dp-start" className={common.fieldLabel}>
                    Bắt đầu áp dụng
                  </label>
                  <input
                    id="dp-start"
                    type="datetime-local"
                    className={common.textInput}
                    value={startsAt}
                    onChange={(e) => setStartsAt(e.target.value)}
                    required
                  />
                </div>
                <div className={common.field}>
                  <label htmlFor="dp-end" className={common.fieldLabel}>
                    Kết thúc áp dụng
                  </label>
                  <input
                    id="dp-end"
                    type="datetime-local"
                    className={common.textInput}
                    value={endsAt}
                    onChange={(e) => setEndsAt(e.target.value)}
                    required
                  />
                </div>
              </div>

              <div className={common.cardHeader} style={{ marginTop: 8 }}>
                <span className={common.fieldLabel}>
                  Danh sách SKU áp dụng ({formatInt(draftItems.length)})
                </span>
                <div className={styles.actionPair}>
                  <input
                    type="number"
                    min={1}
                    max={90}
                    className={common.textInput}
                    style={{ width: 78, height: 32 }}
                    value={quickPct}
                    onChange={(e) => setQuickPct(e.target.value)}
                    aria-label="Phần trăm giảm nhanh"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={handleApplyQuickPct}
                  >
                    Tính nhanh -{quickPct}%
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={handleAddDraftRow}
                  >
                    Thêm SKU
                  </Button>
                </div>
              </div>

              <div className={styles.sectionStack} style={{ gap: 10, marginBottom: 14 }}>
                {draftItems.map((row, idx) => {
                  const found = skuLookup.get(row.sku_id);
                  const regPrice = found?.sku.price_vnd ?? 0;
                  const min30d = found?.sku.min_price_30d_vnd ?? 0;
                  const discVal = Math.trunc(Number(row.discounted_price_vnd) || 0);
                  return (
                    <div key={`${row.sku_id}-${idx}`} className={styles.formGrid4}>
                      <div className={common.field}>
                        <label htmlFor={`dp-sku-${idx}`} className={common.fieldLabel}>
                          Biến thể SKU
                        </label>
                        <select
                          id={`dp-sku-${idx}`}
                          className={common.textInput}
                          value={row.sku_id}
                          onChange={(e) => {
                            const nextSkuId = e.target.value;
                            const nextFound = skuLookup.get(nextSkuId);
                            const pct = Math.max(
                              1,
                              Math.min(90, Math.trunc(Number(quickPct) || 15))
                            );
                            const suggested = nextFound
                              ? Math.max(
                                  1000,
                                  Math.trunc((nextFound.sku.price_vnd * (100 - pct)) / 100)
                                )
                              : discVal;
                            setDraftItems((prev) =>
                              prev.map((item, i) =>
                                i === idx
                                  ? {
                                      ...item,
                                      sku_id: nextSkuId,
                                      discounted_price_vnd: String(suggested),
                                    }
                                  : item
                              )
                            );
                          }}
                        >
                          {selectableSkus.map(({ product, sku }) => (
                            <option key={sku.id} value={sku.id}>
                              {product.name} · {sku.sku_code} ({formatVnd(sku.price_vnd)})
                            </option>
                          ))}
                        </select>
                        {found && (
                          <span className={common.fieldHint}>
                            Giá gốc: {formatVnd(regPrice)}
                            {min30d > 0 ? ` · Thấp nhất 30 ngày: ${formatVnd(min30d)}` : ""}
                          </span>
                        )}
                      </div>

                      <div className={common.field}>
                        <label htmlFor={`dp-price-${idx}`} className={common.fieldLabel}>
                          Giá sau giảm (VND)
                        </label>
                        <input
                          id={`dp-price-${idx}`}
                          type="number"
                          step={1}
                          min={1}
                          className={common.textInput}
                          value={row.discounted_price_vnd}
                          onChange={(e) =>
                            setDraftItems((prev) =>
                              prev.map((item, i) =>
                                i === idx ? { ...item, discounted_price_vnd: e.target.value } : item
                              )
                            )
                          }
                          required
                        />
                        {regPrice > 0 && discVal > 0 && discVal < regPrice && (
                          <span className={common.fieldHint}>
                            Giảm {formatDiscountPercent(regPrice, discVal)} (tiết kiệm{" "}
                            {formatVnd(regPrice - discVal)})
                          </span>
                        )}
                      </div>

                      <div className={common.field}>
                        <label htmlFor={`dp-limit-${idx}`} className={common.fieldLabel}>
                          Giới hạn / khách (0 = không giới hạn)
                        </label>
                        <input
                          id={`dp-limit-${idx}`}
                          type="number"
                          min={0}
                          className={common.textInput}
                          value={row.per_user_limit}
                          onChange={(e) =>
                            setDraftItems((prev) =>
                              prev.map((item, i) =>
                                i === idx ? { ...item, per_user_limit: e.target.value } : item
                              )
                            )
                          }
                        />
                      </div>

                      <div className={common.field} style={{ justifyContent: "flex-end" }}>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={draftItems.length <= 1}
                          onClick={() => setDraftItems((prev) => prev.filter((_, i) => i !== idx))}
                        >
                          Bỏ SKU
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>

              <div className={styles.formActions}>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => {
                    resetForm();
                    onCloseForm();
                  }}
                >
                  Hủy
                </Button>
                <Button type="submit" variant="ink" size="sm" shape="app" disabled={submitting}>
                  {submitting
                    ? "Đang lưu..."
                    : editingId
                      ? "Cập nhật chương trình"
                      : "Lưu chương trình giảm giá"}
                </Button>
              </div>
            </>
          )}
        </form>
      )}

      <section className={common.card} aria-labelledby="shop-discount-list-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="shop-discount-list-heading" className={common.cardTitle}>
              Chương trình giảm giá của {shopName}
            </h2>
            <span className={common.cardMeta}>
              Tự động gạch giá gốc thật trên trang sản phẩm, giỏ hàng và thanh toán
            </span>
          </div>
          <div className={common.segmented} role="tablist" aria-label="Lọc trạng thái chương trình">
            {(
              [
                ["ALL", `Tất cả (${formatInt(programs.length)})`],
                ["ACTIVE", "Đang chạy"],
                ["SCHEDULED", "Chờ chạy"],
                ["ENDED", "Đã kết thúc"],
              ] as const
            ).map(([k, lbl]) => (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={statusFilter === k}
                className={`${common.segmentedBtn} ${statusFilter === k ? common.segmentedBtnActive : ""}`}
                onClick={() => setStatusFilter(k)}
              >
                {lbl}
              </button>
            ))}
          </div>
        </div>

        {filteredPrograms.length === 0 ? (
          <div className={common.inlineEmpty}>
            Chưa có chương trình giảm giá nào ở trạng thái đã chọn.
          </div>
        ) : (
          <>
            <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
              <table className={common.dataTable}>
                <thead>
                  <tr>
                    <th>Tên chương trình</th>
                    <th>Thời gian áp dụng</th>
                    <th>SKU & mức giảm</th>
                    <th>Trạng thái</th>
                    <th className={common.numCol}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredPrograms.map((program) => {
                    const meta = DISCOUNT_PROGRAM_STATUS_META[program.status] ?? {
                      label: program.status,
                      tone: "neutral" as const,
                    };
                    const canModify = program.status === "ACTIVE" || program.status === "SCHEDULED";
                    const hasInflationFlag = (program.items ?? []).some(
                      (it) => it.price_inflation_flagged
                    );
                    return (
                      <tr key={program.id}>
                        <td>
                          <strong>{program.name}</strong>
                          <span className={common.cellSub}>
                            {formatInt(program.items?.length ?? 0)} biến thể SKU
                          </span>
                          {hasInflationFlag && (
                            <span
                              className={`${common.pill} ${common.pillWarn}`}
                              style={{ marginTop: 4 }}
                            >
                              Cảnh báo tăng giá gốc 30 ngày
                            </span>
                          )}
                        </td>
                        <td>
                          <span>
                            {program.starts_at ? formatDateTimeVi(program.starts_at) : "—"}
                          </span>
                          <span className={common.cellSub}>
                            đến {program.ends_at ? formatDateTimeVi(program.ends_at) : "—"}
                          </span>
                        </td>
                        <td>
                          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                            {(program.items ?? []).slice(0, 3).map((it) => {
                              const found = skuLookup.get(it.sku_id);
                              return (
                                <div
                                  key={it.id || it.sku_id}
                                  style={{ display: "flex", alignItems: "center", gap: 8 }}
                                >
                                  {found && (
                                    <img
                                      src={
                                        found.sku.image_url || getProductCoverImage(found.product)
                                      }
                                      alt={found.product.name}
                                      width={26}
                                      height={34}
                                      style={{
                                        borderRadius: 4,
                                        objectFit: "cover",
                                        background: "var(--surface)",
                                      }}
                                    />
                                  )}
                                  <div>
                                    <span>
                                      {found ? found.sku.sku_code : it.sku_id}:{" "}
                                      <strong>{formatVnd(it.discounted_price_vnd)}</strong>
                                    </span>
                                    <span className={styles.discountTag}>
                                      {formatDiscountPercent(
                                        it.original_price_vnd,
                                        it.discounted_price_vnd
                                      )}
                                    </span>
                                    <span className={common.cellSub}>
                                      Gốc {formatVnd(it.original_price_vnd)}
                                      {it.per_user_limit > 0
                                        ? ` · Tối đa ${formatInt(it.per_user_limit)}/khách`
                                        : ""}
                                      {it.price_inflation_flagged
                                        ? " · Cảnh báo cao hơn giá thấp nhất 30 ngày"
                                        : ""}
                                    </span>
                                  </div>
                                </div>
                              );
                            })}
                            {(program.items?.length ?? 0) > 3 && (
                              <span className={common.cellSub}>
                                + {formatInt((program.items?.length ?? 0) - 3)} SKU khác
                              </span>
                            )}
                          </div>
                        </td>
                        <td>
                          <span className={pillClassForTone(meta.tone)}>{meta.label}</span>
                        </td>
                        <td className={common.numCol}>
                          {canModify ? (
                            <div
                              className={styles.actionPair}
                              style={{ justifyContent: "flex-end" }}
                            >
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                onClick={() => handleStartEdit(program)}
                              >
                                Sửa
                              </Button>
                              {confirmEndId === program.id ? (
                                <>
                                  <Button
                                    type="button"
                                    variant="danger"
                                    size="sm"
                                    shape="app"
                                    disabled={endingId === program.id}
                                    onClick={() => void handleEndProgram(program)}
                                  >
                                    {endingId === program.id ? "Đang dừng..." : "Xác nhận dừng"}
                                  </Button>
                                  <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    shape="app"
                                    onClick={() => setConfirmEndId(null)}
                                  >
                                    Hủy
                                  </Button>
                                </>
                              ) : (
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  onClick={() => setConfirmEndId(program.id)}
                                >
                                  Kết thúc
                                </Button>
                              )}
                            </div>
                          ) : (
                            <span className={common.cardMeta}>—</span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={common.mobileList}>
              {filteredPrograms.map((program) => {
                const meta = DISCOUNT_PROGRAM_STATUS_META[program.status] ?? {
                  label: program.status,
                  tone: "neutral" as const,
                };
                const canModify = program.status === "ACTIVE" || program.status === "SCHEDULED";
                const hasInflationFlag = (program.items ?? []).some(
                  (it) => it.price_inflation_flagged
                );
                return (
                  <div key={program.id} className={common.mobileRow}>
                    <div className={common.mobileRowTop}>
                      <div>
                        <strong>{program.name}</strong>
                        <span className={common.cellSub}>
                          {formatInt(program.items?.length ?? 0)} SKU ·{" "}
                          {program.starts_at ? formatDateTimeVi(program.starts_at) : "—"}
                        </span>
                      </div>
                      <span className={pillClassForTone(meta.tone)}>{meta.label}</span>
                    </div>
                    {hasInflationFlag && (
                      <span className={`${common.pill} ${common.pillWarn}`}>
                        Cảnh báo tăng giá gốc 30 ngày
                      </span>
                    )}
                    <div className={common.mobileRowMeta}>
                      <span>
                        Hết hạn: {program.ends_at ? formatDateTimeVi(program.ends_at) : "—"}
                      </span>
                      {canModify && (
                        <div className={styles.actionPair}>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            shape="app"
                            onClick={() => handleStartEdit(program)}
                          >
                            Sửa
                          </Button>
                          {confirmEndId === program.id ? (
                            <>
                              <Button
                                type="button"
                                variant="ink"
                                size="sm"
                                shape="app"
                                disabled={endingId === program.id}
                                onClick={() => void handleEndProgram(program)}
                              >
                                {endingId === program.id ? "Đang xử lý..." : "Xác nhận kết thúc"}
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                shape="app"
                                onClick={() => setConfirmEndId(null)}
                              >
                                Hủy
                              </Button>
                            </>
                          ) : (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={endingId === program.id}
                              onClick={() => setConfirmEndId(program.id)}
                            >
                              Kết thúc
                            </Button>
                          )}
                        </div>
                      )}
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
