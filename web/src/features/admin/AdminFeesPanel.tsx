"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Percent, RefreshCw } from "lucide-react";
import { EmptyState, ErrorState, IndexRowCard, IndexTable, StatusBadge } from "@/components/app";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, getErrorMessage } from "@/lib/api";
import { formatBpsPercent, formatDateTimeVi, formatInt, formatVnIsoDate } from "@/lib/format";
import type {
  GatewayCategory,
  GatewayCategoryFeeSchedule,
  GatewayPaymentFeeConfigItem,
  GatewayPaymentFeeConfigResponse,
  GatewayPaymentMethod,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { EMPTY_DASH, localVnInputToIso } from "./adminMeta";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { FilterSelect, TableLoadingSkeleton } from "./config/adminUiHelpers";

function defaultTomorrowVnLocal(): string {
  return `${formatVnIsoDate(Date.now() + 24 * 3600 * 1000)}T00:00`;
}

function isEffectiveFuture(iso?: string): boolean {
  if (!iso) return false;
  const ms = Date.parse(iso);
  return Number.isFinite(ms) && ms > Date.now();
}

export function AdminFeesPanel() {
  const { showToast } = useAdminContext();
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
  const [catEffectiveFrom, setCatEffectiveFrom] = useState(() => defaultTomorrowVnLocal());
  const [catNote, setCatNote] = useState("");
  const [catSaving, setCatSaving] = useState(false);
  const [catFormError, setCatFormError] = useState<string | null>(null);

  // Create payment fee schedule
  const [payMethod, setPayMethod] = useState<GatewayPaymentMethod>("VIETQR");
  const [payBpsStr, setPayBpsStr] = useState("100");
  const [payEffectiveFrom, setPayEffectiveFrom] = useState(() => defaultTomorrowVnLocal());
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
          ? api<{ categories: GatewayCategory[] }>("/api/admin/categories").catch(() => ({
              categories: [] as GatewayCategory[],
            }))
          : Promise.resolve({ categories: [] as GatewayCategory[] }),
        canReadCatFees
          ? api<PaginatedResponse<GatewayCategoryFeeSchedule>>(
              `/api/admin/fees/categories?${catQs.toString()}`
            )
          : Promise.resolve({
              items: [] as GatewayCategoryFeeSchedule[],
              page: 1,
              page_size: 100,
              total: 0,
            }),
        canReadPayFees
          ? api<GatewayPaymentFeeConfigResponse>("/api/admin/fees/payment?include_history=true")
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
    const effIso = localVnInputToIso(catEffectiveFrom);
    if (!effIso) {
      setCatFormError("Vui lòng chọn thời điểm bắt đầu hiệu lực hợp lệ.");
      return;
    }
    setCatFormError(null);
    setCatSaving(true);
    try {
      await api<{ schedule: GatewayCategoryFeeSchedule }>(
        `/api/admin/fees/categories/${encodeURIComponent(catId)}`,
        {
          method: "POST",
          json: {
            commission_rate_bps: bps,
            effective_from: effIso,
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
    const effIso = localVnInputToIso(payEffectiveFrom);
    if (!effIso) {
      setPayFormError("Vui lòng chọn thời điểm bắt đầu hiệu lực hợp lệ.");
      return;
    }
    setPayFormError(null);
    setPaySaving(true);
    try {
      await api<{ config: GatewayPaymentFeeConfigItem }>("/api/admin/fees/payment", {
        method: "POST",
        json: {
          payment_method: payMethod,
          fee_bps: bps,
          effective_from: effIso,
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
    return <TableLoadingSkeleton rows={5} label="Đang tải biểu phí..." />;
  }

  if (errorMsg) {
    return (
      <ErrorState
        title="Không thể tải biểu phí sàn"
        description={errorMsg}
        onRetry={() => void loadFees()}
      />
    );
  }

  const parsedCatBps = Math.trunc(Number(catBpsStr) || 0);
  const parsedPayBps = Math.trunc(Number(payBpsStr) || 0);

  return (
    <div className="space-y-6">
      {/* Category commission fee schedules */}
      {canReadCatFees ? (
        <section className="space-y-4" aria-labelledby="cat-fee-heading">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 id="cat-fee-heading" className="text-sm font-semibold text-foreground">
                Biểu phí hoa hồng danh mục ({formatInt(catSchedules.length)})
              </h2>
              <p className="text-[13px] text-muted-foreground">
                100 bps = 1% — lên lịch điều chỉnh theo ngày hiệu lực giờ Việt Nam.
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="w-48">
                <FilterSelect
                  value={catFilterId}
                  onValueChange={setCatFilterId}
                  ariaLabel="Lọc lịch phí theo danh mục"
                  options={[
                    { value: "", label: "Tất cả danh mục" },
                    ...categories.map((c) => ({ value: c.id, label: c.name })),
                  ]}
                />
              </div>
              <Button type="button" variant="outline" size="sm" onClick={() => void loadFees()}>
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Làm mới</span>
              </Button>
            </div>
          </div>

          {canWriteCatFees ? (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] md:items-start">
              <div className="space-y-1">
                <h3 className="text-sm font-semibold text-foreground">Lên lịch phí danh mục</h3>
                <p className="text-[13px] text-muted-foreground">
                  Áp dụng tự động cho đơn hàng mới kể từ mốc hiệu lực đã đặt.
                </p>
              </div>
              <Card>
                <form onSubmit={(e) => void handleSaveCategoryFee(e)} noValidate>
                  <CardContent className="pt-4">
                    <FieldGroup className="gap-4">
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <Field className="gap-1.5 sm:col-span-2">
                          <FieldLabel htmlFor="fee-cat-id">Danh mục áp dụng</FieldLabel>
                          <Select value={catId} onValueChange={setCatId}>
                            <SelectTrigger id="fee-cat-id" className="w-full bg-card">
                              <SelectValue placeholder="Chọn danh mục..." />
                            </SelectTrigger>
                            <SelectContent>
                              {categories.map((c) => (
                                <SelectItem key={c.id} value={c.id}>
                                  {c.name} (hiện tại {formatBpsPercent(c.commission_rate_bps ?? 0)})
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor="fee-cat-bps">
                            Mức hoa hồng (bps — {formatBpsPercent(parsedCatBps)})
                          </FieldLabel>
                          <Input
                            id="fee-cat-bps"
                            type="number"
                            min={0}
                            max={5000}
                            step={1}
                            value={catBpsStr}
                            onChange={(e) => setCatBpsStr(e.target.value)}
                            required
                            className="bg-card tabular-nums"
                          />
                        </Field>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor="fee-cat-eff">Bắt đầu hiệu lực (giờ VN)</FieldLabel>
                          <Input
                            id="fee-cat-eff"
                            type="datetime-local"
                            value={catEffectiveFrom}
                            onChange={(e) => setCatEffectiveFrom(e.target.value)}
                            required
                            className="bg-card tabular-nums"
                          />
                        </Field>

                        <Field className="gap-1.5 sm:col-span-2">
                          <FieldLabel htmlFor="fee-cat-note">Ghi chú điều chỉnh</FieldLabel>
                          <Input
                            id="fee-cat-note"
                            type="text"
                            value={catNote}
                            onChange={(e) => setCatNote(e.target.value)}
                            placeholder="VD: Điều chỉnh biểu phí ngành hàng Quý 4..."
                            className="bg-card"
                          />
                        </Field>
                      </div>
                      <FieldError>{catFormError}</FieldError>
                    </FieldGroup>
                  </CardContent>
                  <CardFooter className="justify-end border-t border-border pt-3.5">
                    <Button type="submit" size="sm" disabled={catSaving}>
                      {catSaving ? "Đang lưu..." : "Lên lịch phí danh mục"}
                    </Button>
                  </CardFooter>
                </form>
              </Card>
            </div>
          ) : null}

          {catSchedules.length === 0 ? (
            <EmptyState
              icon={Percent}
              title="Chưa có lịch biểu phí danh mục nào"
              description="Các lịch biểu phí hoa hồng theo danh mục sẽ hiển thị tại đây."
            />
          ) : (
            <IndexTable>
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Danh mục</TableHead>
                      <TableHead className="text-right">Mức hoa hồng</TableHead>
                      <TableHead>Hiệu lực từ</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead>Ghi chú</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {catSchedules.map((sch) => {
                      const upcoming = isEffectiveFuture(sch.effective_from);
                      return (
                        <TableRow key={sch.id}>
                          <TableCell className="whitespace-normal">
                            <div className="font-medium text-foreground">
                              {sch.category_name ||
                                categoryNameById.get(sch.category_id) ||
                                sch.category_id}
                            </div>
                            {sch.created_at ? (
                              <div className="text-xs text-muted-foreground tabular-nums">
                                Tạo lúc {formatDateTimeVi(sch.created_at)}
                              </div>
                            ) : null}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            <div className="font-semibold text-foreground">
                              {formatBpsPercent(sch.commission_rate_bps)}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              {formatInt(sch.commission_rate_bps)} bps
                            </div>
                          </TableCell>
                          <TableCell className="tabular-nums">
                            {formatDateTimeVi(sch.effective_from)}
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={upcoming ? "attention" : "success"}>
                              {upcoming ? "Chờ hiệu lực" : "Đang / đã hiệu lực"}
                            </StatusBadge>
                          </TableCell>
                          <TableCell className="max-w-[260px] whitespace-normal text-muted-foreground">
                            {sch.note || EMPTY_DASH}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              <div className="md:hidden">
                {catSchedules.map((sch) => {
                  const upcoming = isEffectiveFuture(sch.effective_from);
                  return (
                    <IndexRowCard
                      key={sch.id}
                      title={
                        sch.category_name ||
                        categoryNameById.get(sch.category_id) ||
                        sch.category_id
                      }
                      badge={
                        <StatusBadge tone={upcoming ? "attention" : "success"}>
                          {upcoming ? "Chờ hiệu lực" : "Hiệu lực"}
                        </StatusBadge>
                      }
                      meta={
                        <>
                          <span className="font-semibold text-foreground tabular-nums">
                            {formatBpsPercent(sch.commission_rate_bps)}
                          </span>
                          <span>·</span>
                          <span className="tabular-nums">
                            Từ {formatDateTimeVi(sch.effective_from)}
                          </span>
                        </>
                      }
                    />
                  );
                })}
              </div>
            </IndexTable>
          )}
        </section>
      ) : null}

      {canReadCatFees && canReadPayFees ? <Separator /> : null}

      {/* Payment method fees (COD / VIETQR / ZALOPAY) */}
      {canReadPayFees ? (
        <section className="space-y-4" aria-labelledby="pay-fee-heading">
          <div>
            <h2 id="pay-fee-heading" className="text-sm font-semibold text-foreground">
              Phí thanh toán ({formatInt(paymentFees.length)})
            </h2>
            <p className="text-[13px] text-muted-foreground">
              Biểu phí xử lý giao dịch áp dụng cho VietQR, ZaloPay và COD.
            </p>
          </div>

          {canWritePayFees ? (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] md:items-start">
              <div className="space-y-1">
                <h3 className="text-sm font-semibold text-foreground">Lên lịch phí thanh toán</h3>
                <p className="text-[13px] text-muted-foreground">
                  Mức phí hợp lệ từ 0 đến 2000 bps (0% – 20%).
                </p>
              </div>
              <Card>
                <form onSubmit={(e) => void handleSavePaymentFee(e)} noValidate>
                  <CardContent className="pt-4">
                    <FieldGroup className="gap-4">
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <Field className="gap-1.5 sm:col-span-2">
                          <FieldLabel htmlFor="fee-pay-method">Phương thức thanh toán</FieldLabel>
                          <Select
                            value={payMethod}
                            onValueChange={(v) => setPayMethod(v as GatewayPaymentMethod)}
                          >
                            <SelectTrigger id="fee-pay-method" className="w-full bg-card">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="VIETQR">Chuyển khoản VietQR</SelectItem>
                              <SelectItem value="ZALOPAY">Ví điện tử ZaloPay</SelectItem>
                              <SelectItem value="COD">Thu hộ khi nhận hàng (COD)</SelectItem>
                            </SelectContent>
                          </Select>
                        </Field>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor="fee-pay-bps">
                            Mức phí (bps — {formatBpsPercent(parsedPayBps)})
                          </FieldLabel>
                          <Input
                            id="fee-pay-bps"
                            type="number"
                            min={0}
                            max={2000}
                            step={1}
                            value={payBpsStr}
                            onChange={(e) => setPayBpsStr(e.target.value)}
                            required
                            className="bg-card tabular-nums"
                          />
                        </Field>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor="fee-pay-eff">Bắt đầu hiệu lực (giờ VN)</FieldLabel>
                          <Input
                            id="fee-pay-eff"
                            type="datetime-local"
                            value={payEffectiveFrom}
                            onChange={(e) => setPayEffectiveFrom(e.target.value)}
                            required
                            className="bg-card tabular-nums"
                          />
                        </Field>

                        <Field className="gap-1.5 sm:col-span-2">
                          <FieldLabel htmlFor="fee-pay-note">
                            Ghi chú thay đổi phí thanh toán
                          </FieldLabel>
                          <Input
                            id="fee-pay-note"
                            type="text"
                            value={payNote}
                            onChange={(e) => setPayNote(e.target.value)}
                            placeholder="VD: Biểu phí xử lý giao dịch ngân hàng..."
                            className="bg-card"
                          />
                        </Field>
                      </div>
                      <FieldError>{payFormError}</FieldError>
                    </FieldGroup>
                  </CardContent>
                  <CardFooter className="justify-end border-t border-border pt-3.5">
                    <Button type="submit" size="sm" disabled={paySaving}>
                      {paySaving ? "Đang lưu..." : "Lên lịch phí thanh toán"}
                    </Button>
                  </CardFooter>
                </form>
              </Card>
            </div>
          ) : null}

          {paymentFees.length === 0 ? (
            <EmptyState
              icon={Percent}
              title="Chưa có lịch phí thanh toán nào"
              description="Các biểu phí thanh toán sẽ hiển thị tại đây sau khi được cấu hình."
            />
          ) : (
            <IndexTable>
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Phương thức</TableHead>
                      <TableHead className="text-right">Mức phí</TableHead>
                      <TableHead>Hiệu lực từ</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead>Ghi chú</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {paymentFees.map((cfg) => {
                      const upcoming = isEffectiveFuture(cfg.effective_from);
                      const isActive = activePaymentFeeIds.has(cfg.id);
                      const statusLabel = isActive
                        ? "Đang áp dụng"
                        : upcoming
                          ? "Chờ hiệu lực"
                          : "Lịch sử";
                      const statusTone = isActive ? "success" : upcoming ? "attention" : "neutral";
                      return (
                        <TableRow key={cfg.id}>
                          <TableCell className="whitespace-normal">
                            <div className="font-medium text-foreground">{cfg.payment_method}</div>
                            {cfg.created_at ? (
                              <div className="text-xs text-muted-foreground tabular-nums">
                                Tạo lúc {formatDateTimeVi(cfg.created_at)}
                              </div>
                            ) : null}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            <div className="font-semibold text-foreground">
                              {formatBpsPercent(cfg.fee_bps)}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              {formatInt(cfg.fee_bps)} bps
                            </div>
                          </TableCell>
                          <TableCell className="tabular-nums">
                            {formatDateTimeVi(cfg.effective_from)}
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={statusTone}>{statusLabel}</StatusBadge>
                          </TableCell>
                          <TableCell className="max-w-[260px] whitespace-normal text-muted-foreground">
                            {cfg.note || EMPTY_DASH}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              <div className="md:hidden">
                {paymentFees.map((cfg) => {
                  const upcoming = isEffectiveFuture(cfg.effective_from);
                  const isActive = activePaymentFeeIds.has(cfg.id);
                  const statusLabel = isActive
                    ? "Đang áp dụng"
                    : upcoming
                      ? "Chờ hiệu lực"
                      : "Lịch sử";
                  const statusTone = isActive ? "success" : upcoming ? "attention" : "neutral";
                  return (
                    <IndexRowCard
                      key={cfg.id}
                      title={cfg.payment_method}
                      badge={<StatusBadge tone={statusTone}>{statusLabel}</StatusBadge>}
                      meta={
                        <>
                          <span className="font-semibold text-foreground tabular-nums">
                            {formatBpsPercent(cfg.fee_bps)}
                          </span>
                          <span>·</span>
                          <span className="tabular-nums">
                            Từ {formatDateTimeVi(cfg.effective_from)}
                          </span>
                        </>
                      }
                    />
                  );
                })}
              </div>
            </IndexTable>
          )}
        </section>
      ) : null}
    </div>
  );
}
