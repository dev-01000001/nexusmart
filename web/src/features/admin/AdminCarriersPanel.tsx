"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Truck } from "lucide-react";
import { EmptyState, ErrorState, StatusBadge } from "@/components/app";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, getErrorMessage } from "@/lib/api";
import { formatCarrierLabel, formatDateTimeVi, formatVnd } from "@/lib/format";
import type {
  GatewayAdminTestCarrierResponse,
  GatewayCarrierConfig,
  GatewayFallbackRate,
  GatewayShippingRegion,
} from "@/lib/gateway-types";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { SHIPPING_REGION_LABELS } from "./types";
import { TableLoadingSkeleton } from "./config/adminUiHelpers";

const REGIONS: GatewayShippingRegion[] = ["SAME_PROVINCE", "SAME_REGION", "CROSS_REGION"];

interface CarrierDraft {
  enabled: boolean;
  api_token: string;
  fallback_rates: GatewayFallbackRate[];
}

function normalizeFallbackRates(rates?: GatewayFallbackRate[]): GatewayFallbackRate[] {
  const byRegion = new Map<string, GatewayFallbackRate>();
  for (const r of rates ?? []) {
    byRegion.set(String(r.region).toUpperCase(), r);
  }
  return REGIONS.map((region) => {
    const existing = byRegion.get(region);
    if (existing) {
      return {
        region,
        base_fee_vnd: Math.max(0, Math.trunc(existing.base_fee_vnd || 0)),
        base_weight_grams: Math.max(1, Math.trunc(existing.base_weight_grams || 1000)),
        extra_fee_vnd_per_500g: Math.max(0, Math.trunc(existing.extra_fee_vnd_per_500g || 0)),
      };
    }
    return {
      region,
      base_fee_vnd: 0,
      base_weight_grams: 1000,
      extra_fee_vnd_per_500g: 0,
    };
  });
}

function buildDraft(carrier: GatewayCarrierConfig): CarrierDraft {
  return {
    enabled: Boolean(carrier.enabled),
    api_token: "",
    fallback_rates: normalizeFallbackRates(carrier.fallback_rates),
  };
}

export function AdminCarriersPanel() {
  const { showToast } = useAdminContext();
  const canWrite = useAdminCapability("carriers.write");

  const [carriers, setCarriers] = useState<GatewayCarrierConfig[]>([]);
  const [drafts, setDrafts] = useState<Record<string, CarrierDraft>>({});
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [savingCode, setSavingCode] = useState<string | null>(null);
  const [testingCode, setTestingCode] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, GatewayAdminTestCarrierResponse>>(
    {}
  );
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  const loadCarriers = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const res = await api<{ carriers: GatewayCarrierConfig[] }>("/api/admin/carriers");
      const list = res.carriers ?? [];
      setCarriers(list);
      const nextDrafts: Record<string, CarrierDraft> = {};
      for (const c of list) {
        nextDrafts[c.carrier_code] = buildDraft(c);
      }
      setDrafts(nextDrafts);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách hãng vận chuyển lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadCarriers();
  }, [loadCarriers]);

  const updateDraft = (code: string, updater: (prev: CarrierDraft) => CarrierDraft) => {
    setDrafts((prev) => {
      const base =
        prev[code] ??
        buildDraft(
          carriers.find((c) => c.carrier_code === code) ?? {
            carrier_code: code,
            name: code,
            enabled: false,
            token_configured: false,
            fallback_rates: [],
          }
        );
      return { ...prev, [code]: updater(base) };
    });
  };

  const updateRateField = (
    code: string,
    region: GatewayShippingRegion,
    field: "base_fee_vnd" | "base_weight_grams" | "extra_fee_vnd_per_500g",
    rawValue: string
  ) => {
    const parsed = Math.max(0, Math.trunc(Number(rawValue) || 0));
    updateDraft(code, (prev) => ({
      ...prev,
      fallback_rates: prev.fallback_rates.map((r) =>
        r.region === region ? { ...r, [field]: parsed } : r
      ),
    }));
  };

  const handleSaveCarrier = async (e: React.FormEvent, carrier: GatewayCarrierConfig) => {
    e.preventDefault();
    if (!canWrite) return;
    const code = carrier.carrier_code;
    if (savingCode) return;
    const draft = drafts[code];
    if (!draft) return;

    for (const rate of draft.fallback_rates) {
      if (rate.base_fee_vnd < 0 || rate.base_weight_grams <= 0 || rate.extra_fee_vnd_per_500g < 0) {
        setRowErrors((prev) => ({
          ...prev,
          [code]: "Biểu phí dự phòng cần có khối lượng cơ bản lớn hơn 0g và mức phí không âm.",
        }));
        return;
      }
    }

    setRowErrors((prev) => ({ ...prev, [code]: "" }));
    setSavingCode(code);
    try {
      const payload: {
        enabled: boolean;
        api_token?: string;
        fallback_rates: GatewayFallbackRate[];
      } = {
        enabled: draft.enabled,
        fallback_rates: draft.fallback_rates,
      };
      const cleanToken = draft.api_token.trim();
      if (cleanToken) {
        payload.api_token = cleanToken;
      }

      const res = await api<{ carrier: GatewayCarrierConfig }>(
        `/api/admin/carriers/${encodeURIComponent(code)}`,
        {
          method: "PUT",
          json: payload,
        }
      );
      if (res?.carrier) {
        setCarriers((prev) => prev.map((c) => (c.carrier_code === code ? res.carrier : c)));
        setDrafts((prev) => ({ ...prev, [code]: buildDraft(res.carrier) }));
      }
      showToast(`Đã lưu cấu hình ${formatCarrierLabel(code, carrier.name)}.`);
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [code]: getErrorMessage(err, "Không thể lưu cấu hình hãng vận chuyển lúc này."),
      }));
    } finally {
      setSavingCode(null);
    }
  };

  const handleTestCarrier = async (carrier: GatewayCarrierConfig) => {
    if (!canWrite) return;
    const code = carrier.carrier_code;
    if (testingCode) return;
    setTestingCode(code);
    setRowErrors((prev) => ({ ...prev, [code]: "" }));
    try {
      const res = await api<GatewayAdminTestCarrierResponse>(
        `/api/admin/carriers/${encodeURIComponent(code)}/test`,
        { method: "POST" }
      );
      setTestResults((prev) => ({ ...prev, [code]: res }));
      const displayName = formatCarrierLabel(code, carrier.name);
      showToast(
        res.ok ? `Kết nối ${displayName} thành công.` : `Kiểm tra ${displayName} chưa thành công.`
      );
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể kiểm tra kết nối hãng vận chuyển lúc này.");
      setTestResults((prev) => ({ ...prev, [code]: { ok: false, message: msg } }));
    } finally {
      setTestingCode(null);
    }
  };

  if (loading) {
    return <TableLoadingSkeleton rows={4} label="Đang tải cấu hình hãng vận chuyển..." />;
  }

  if (errorMsg) {
    return (
      <ErrorState
        title="Không thể tải danh sách hãng vận chuyển"
        description={errorMsg}
        onRetry={() => void loadCarriers()}
      />
    );
  }

  if (carriers.length === 0) {
    return (
      <EmptyState
        icon={Truck}
        title="Chưa có hãng vận chuyển nào"
        description="Hệ thống vận chuyển chưa trả về đối tác nào."
        action={
          <Button type="button" variant="outline" size="sm" onClick={() => void loadCarriers()}>
            Làm mới
          </Button>
        }
      />
    );
  }

  return (
    <div className="space-y-6">
      {carriers.map((carrier, idx) => {
        const code = carrier.carrier_code;
        const displayName = formatCarrierLabel(code, carrier.name);
        const draft = drafts[code] ?? buildDraft(carrier);
        const testRes = testResults[code];
        const rowErr = rowErrors[code];
        const isSaving = savingCode === code;
        const isTesting = testingCode === code;

        return (
          <React.Fragment key={code}>
            {idx > 0 ? <Separator /> : null}
            <section
              aria-labelledby={`carrier-heading-${code}`}
              className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] md:items-start"
            >
              <div className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <h2
                    id={`carrier-heading-${code}`}
                    className="text-sm font-semibold text-foreground"
                  >
                    {displayName}
                  </h2>
                  <StatusBadge tone={draft.enabled ? "success" : "neutral"}>
                    {draft.enabled ? "Đang bật" : "Đang tắt"}
                  </StatusBadge>
                </div>
                <p className="text-xs leading-[18px] text-muted-foreground">
                  Mã: {code} · Khóa API:{" "}
                  {carrier.token_configured ? "Đã cấu hình" : "Chưa cấu hình"}
                </p>
                <p className="text-xs leading-[18px] text-muted-foreground tabular-nums">
                  Nội tỉnh dự phòng: {formatVnd(draft.fallback_rates[0]?.base_fee_vnd ?? 0)}
                  {carrier.updated_at ? ` · ${formatDateTimeVi(carrier.updated_at)}` : ""}
                </p>
              </div>

              <Card>
                <form method="post" onSubmit={(e) => void handleSaveCarrier(e, carrier)} noValidate>
                  <CardContent className="space-y-4 pt-4">
                    <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/35 px-3.5 py-2.5">
                      <span className="text-sm font-medium text-foreground">Bật {displayName}</span>
                      <Switch
                        checked={draft.enabled}
                        aria-label={`Bật hoặc tắt ${displayName}`}
                        disabled={!canWrite || isSaving}
                        onCheckedChange={(checked) =>
                          updateDraft(code, (prev) => ({ ...prev, enabled: checked }))
                        }
                      />
                    </div>

                    <Field className="gap-1.5">
                      <FieldLabel htmlFor={`carrier-token-${code}`}>
                        API token đối tác (chỉ ghi mới)
                      </FieldLabel>
                      <Input
                        id={`carrier-token-${code}`}
                        type="password"
                        autoComplete="new-password"
                        value={draft.api_token}
                        onChange={(e) =>
                          updateDraft(code, (prev) => ({ ...prev, api_token: e.target.value }))
                        }
                        placeholder={
                          carrier.token_configured
                            ? "Đã cấu hình — để trống để giữ nguyên"
                            : "Nhập API token đối tác..."
                        }
                        disabled={!canWrite || isSaving}
                        className="bg-card"
                      />
                    </Field>

                    {/* Desktop fallback rates table */}
                    <div className="hidden overflow-hidden rounded-lg border border-border md:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>Tuyến vận chuyển</TableHead>
                            <TableHead className="text-right">Cước cơ bản (₫)</TableHead>
                            <TableHead className="text-right">Mốc KL (g)</TableHead>
                            <TableHead className="text-right">Phụ phí / 500g (₫)</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {draft.fallback_rates.map((rate) => {
                            const regionKey = rate.region as GatewayShippingRegion;
                            const regionLabel = SHIPPING_REGION_LABELS[rate.region] ?? rate.region;
                            return (
                              <TableRow key={rate.region}>
                                <TableCell className="font-medium text-foreground">
                                  {regionLabel}
                                </TableCell>
                                <TableCell className="text-right">
                                  <Input
                                    type="number"
                                    min={0}
                                    step={1}
                                    value={rate.base_fee_vnd}
                                    onChange={(e) =>
                                      updateRateField(
                                        code,
                                        regionKey,
                                        "base_fee_vnd",
                                        e.target.value
                                      )
                                    }
                                    aria-label={`Cước cơ bản ${regionLabel} của ${displayName}`}
                                    disabled={!canWrite || isSaving}
                                    className="ml-auto h-8 w-28 bg-card text-right tabular-nums"
                                  />
                                </TableCell>
                                <TableCell className="text-right">
                                  <Input
                                    type="number"
                                    min={1}
                                    step={1}
                                    value={rate.base_weight_grams}
                                    onChange={(e) =>
                                      updateRateField(
                                        code,
                                        regionKey,
                                        "base_weight_grams",
                                        e.target.value
                                      )
                                    }
                                    aria-label={`Mốc khối lượng ${regionLabel} của ${displayName}`}
                                    disabled={!canWrite || isSaving}
                                    className="ml-auto h-8 w-24 bg-card text-right tabular-nums"
                                  />
                                </TableCell>
                                <TableCell className="text-right">
                                  <Input
                                    type="number"
                                    min={0}
                                    step={1}
                                    value={rate.extra_fee_vnd_per_500g}
                                    onChange={(e) =>
                                      updateRateField(
                                        code,
                                        regionKey,
                                        "extra_fee_vnd_per_500g",
                                        e.target.value
                                      )
                                    }
                                    aria-label={`Phụ phí mỗi 500g ${regionLabel} của ${displayName}`}
                                    disabled={!canWrite || isSaving}
                                    className="ml-auto h-8 w-28 bg-card text-right tabular-nums"
                                  />
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>

                    {/* Mobile fallback rates stacked cards */}
                    <div className="space-y-3 md:hidden">
                      {draft.fallback_rates.map((rate) => {
                        const regionKey = rate.region as GatewayShippingRegion;
                        const regionLabel = SHIPPING_REGION_LABELS[rate.region] ?? rate.region;
                        const baseId = `carrier-rate-${code}-${rate.region}-base`;
                        const weightId = `carrier-rate-${code}-${rate.region}-weight`;
                        const extraId = `carrier-rate-${code}-${rate.region}-extra`;
                        return (
                          <div
                            key={rate.region}
                            className="space-y-2.5 rounded-lg border border-border bg-muted/35 p-3"
                          >
                            <div className="text-[13px] font-semibold text-foreground">
                              {regionLabel}
                            </div>
                            <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
                              <Field className="gap-1">
                                <FieldLabel htmlFor={baseId} className="text-xs">
                                  Cước cơ bản (₫)
                                </FieldLabel>
                                <Input
                                  id={baseId}
                                  type="number"
                                  min={0}
                                  step={1}
                                  value={rate.base_fee_vnd}
                                  onChange={(e) =>
                                    updateRateField(code, regionKey, "base_fee_vnd", e.target.value)
                                  }
                                  disabled={!canWrite || isSaving}
                                  className="bg-card tabular-nums"
                                />
                              </Field>
                              <Field className="gap-1">
                                <FieldLabel htmlFor={weightId} className="text-xs">
                                  Mốc KL (g)
                                </FieldLabel>
                                <Input
                                  id={weightId}
                                  type="number"
                                  min={1}
                                  step={1}
                                  value={rate.base_weight_grams}
                                  onChange={(e) =>
                                    updateRateField(
                                      code,
                                      regionKey,
                                      "base_weight_grams",
                                      e.target.value
                                    )
                                  }
                                  disabled={!canWrite || isSaving}
                                  className="bg-card tabular-nums"
                                />
                              </Field>
                              <Field className="gap-1">
                                <FieldLabel htmlFor={extraId} className="text-xs">
                                  Phụ phí / 500g (₫)
                                </FieldLabel>
                                <Input
                                  id={extraId}
                                  type="number"
                                  min={0}
                                  step={1}
                                  value={rate.extra_fee_vnd_per_500g}
                                  onChange={(e) =>
                                    updateRateField(
                                      code,
                                      regionKey,
                                      "extra_fee_vnd_per_500g",
                                      e.target.value
                                    )
                                  }
                                  disabled={!canWrite || isSaving}
                                  className="bg-card tabular-nums"
                                />
                              </Field>
                            </div>
                          </div>
                        );
                      })}
                    </div>

                    <FieldError>{rowErr}</FieldError>

                    {testRes ? (
                      <div className="flex flex-wrap items-center gap-2" role="status">
                        <StatusBadge tone={testRes.ok ? "success" : "critical"}>
                          {testRes.ok ? "Kết nối ổn định" : "Kiểm tra thất bại"}
                        </StatusBadge>
                        {testRes.message ? (
                          <span className="text-xs text-muted-foreground">{testRes.message}</span>
                        ) : null}
                      </div>
                    ) : null}
                  </CardContent>

                  {canWrite ? (
                    <CardFooter className="flex-wrap justify-end gap-2 border-t border-border pt-3.5">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={isTesting || isSaving}
                        onClick={() => void handleTestCarrier(carrier)}
                      >
                        {isTesting ? "Đang kiểm tra..." : "Kiểm tra kết nối"}
                      </Button>
                      <Button type="submit" size="sm" disabled={isSaving || isTesting}>
                        {isSaving ? "Đang lưu..." : "Lưu cấu hình hãng"}
                      </Button>
                    </CardFooter>
                  ) : null}
                </form>
              </Card>
            </section>
          </React.Fragment>
        );
      })}
    </div>
  );
}
