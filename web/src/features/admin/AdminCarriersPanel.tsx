"use client";

import React, { useCallback, useEffect, useState } from "react";
import type {
  GatewayAdminTestCarrierResponse,
  GatewayCarrierConfig,
  GatewayFallbackRate,
  GatewayShippingRegion,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { formatDateTimeVi, formatVnd } from "@/lib/format";
import { SHIPPING_REGION_LABELS } from "./types";
import { StatusPill } from "./StatusPill";
import { ToggleSwitch } from "./ToggleSwitch";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

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
        base_weight_grams: Math.max(100, Math.trunc(existing.base_weight_grams || 1000)),
        extra_fee_vnd_per_500g: Math.max(0, Math.trunc(existing.extra_fee_vnd_per_500g || 0)),
      };
    }
    return {
      region,
      base_fee_vnd: region === "SAME_PROVINCE" ? 18000 : region === "SAME_REGION" ? 25000 : 32000,
      base_weight_grams: 1000,
      extra_fee_vnd_per_500g: 5000,
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
  const { showToast } = useMockStore();
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
      const res = await api<{ carriers: GatewayCarrierConfig[] }>("admin/carriers");
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
        `admin/carriers/${encodeURIComponent(code)}`,
        {
          method: "PUT",
          json: payload,
        }
      );
      if (res?.carrier) {
        setCarriers((prev) => prev.map((c) => (c.carrier_code === code ? res.carrier : c)));
        setDrafts((prev) => ({ ...prev, [code]: buildDraft(res.carrier) }));
      }
      showToast(`Đã lưu cấu hình hãng vận chuyển ${carrier.name || code}.`);
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
    const code = carrier.carrier_code;
    if (testingCode) return;
    setTestingCode(code);
    setRowErrors((prev) => ({ ...prev, [code]: "" }));
    try {
      const res = await api<GatewayAdminTestCarrierResponse>(
        `admin/carriers/${encodeURIComponent(code)}/test`,
        { method: "POST" }
      );
      setTestResults((prev) => ({ ...prev, [code]: res }));
      showToast(
        res.ok
          ? `Kết nối ${carrier.name || code} thành công.`
          : `Kiểm tra ${carrier.name || code} chưa thành công.`
      );
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể kiểm tra kết nối hãng vận chuyển lúc này.");
      setTestResults((prev) => ({ ...prev, [code]: { ok: false, message: msg } }));
    } finally {
      setTestingCode(null);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải cấu hình hãng vận chuyển..."
          rows={4}
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
          title="Không thể tải danh sách hãng vận chuyển"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadCarriers()}
        />
      </div>
    );
  }

  if (carriers.length === 0) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có hãng vận chuyển nào được cấu hình"
          message="Hệ thống vận chuyển chưa trả về đối tác nào. Vui lòng thử làm mới sau."
          actionLabel="Làm mới"
          onAction={() => void loadCarriers()}
        />
      </div>
    );
  }

  return (
    <div className={styles.sectionList}>
      {carriers.map((carrier) => {
        const code = carrier.carrier_code;
        const draft = drafts[code] ?? buildDraft(carrier);
        const testRes = testResults[code];
        const rowErr = rowErrors[code];
        const isSaving = savingCode === code;
        const isTesting = testingCode === code;

        return (
          <section
            key={code}
            className={styles.sectionRow}
            aria-labelledby={`carrier-heading-${code}`}
          >
            <div className={styles.labelCol}>
              <div className={styles.switchTitleLine}>
                <h2 id={`carrier-heading-${code}`} className={styles.sectionTitle}>
                  {carrier.name || code.toUpperCase()}
                </h2>
                <StatusPill
                  label={draft.enabled ? "Đang bật" : "Đang tắt"}
                  tone={draft.enabled ? "positive" : "neutral"}
                />
              </div>
              <p className={styles.sectionDesc}>
                Cấu hình trạng thái bật/tắt, khóa API đối tác (lưu mã hóa, chỉ ghi mới) và bảng cước
                dự phòng khi API hãng tạm gián đoạn.
              </p>

              <dl className={styles.summaryList}>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Mã đối tác</dt>
                  <dd className={styles.summaryVal}>{code}</dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Khóa kết nối API</dt>
                  <dd className={styles.summaryVal}>
                    {carrier.token_configured ? "Đã cấu hình" : "Chưa cấu hình"}
                  </dd>
                </div>
                <div className={styles.summaryPair}>
                  <dt className={styles.summaryKey}>Cước nội tỉnh dự phòng</dt>
                  <dd className={styles.summaryVal}>
                    {formatVnd(draft.fallback_rates[0]?.base_fee_vnd ?? 0)}
                  </dd>
                </div>
                {carrier.updated_at && (
                  <div className={styles.summaryPair}>
                    <dt className={styles.summaryKey}>Cập nhật gần nhất</dt>
                    <dd className={styles.summaryVal}>{formatDateTimeVi(carrier.updated_at)}</dd>
                  </div>
                )}
              </dl>
            </div>

            <form
              className={styles.settingsPanel}
              onSubmit={(e) => void handleSaveCarrier(e, carrier)}
            >
              <div className={styles.switchRow}>
                <div className={styles.switchCopy}>
                  <label htmlFor={`carrier-enable-${code}`} className={styles.switchTitle}>
                    Cho phép gian hàng và người mua sử dụng {carrier.name || code}
                  </label>
                  <p className={styles.switchDesc}>
                    Khi tắt, hãng này sẽ không còn xuất hiện trong danh sách chọn vận chuyển khi đặt
                    hàng.
                  </p>
                </div>
                <ToggleSwitch
                  id={`carrier-enable-${code}`}
                  checked={draft.enabled}
                  label={`Bật hoặc tắt ${carrier.name || code}`}
                  onChange={(next) => updateDraft(code, (prev) => ({ ...prev, enabled: next }))}
                />
              </div>

              <div className={styles.fieldGroup}>
                <label htmlFor={`carrier-token-${code}`} className={styles.fieldLabel}>
                  API token đối tác (chỉ ghi mới)
                </label>
                <input
                  id={`carrier-token-${code}`}
                  type="password"
                  autoComplete="new-password"
                  className={styles.settingInput}
                  value={draft.api_token}
                  onChange={(e) =>
                    updateDraft(code, (prev) => ({ ...prev, api_token: e.target.value }))
                  }
                  placeholder={
                    carrier.token_configured
                      ? "Đã cấu hình — để trống để giữ nguyên token hiện tại"
                      : "Nhập API token đối tác vận chuyển..."
                  }
                  disabled={isSaving}
                />
                <span className={styles.fieldHint}>
                  Token được mã hóa khi lưu trữ và không bao giờ hiển thị lại trên giao diện.
                </span>
              </div>

              <div className={styles.commissionTableWrap}>
                <table className={styles.commissionTable}>
                  <thead>
                    <tr>
                      <th>Tuyến vận chuyển</th>
                      <th className={shared.colNum}>Cước cơ bản (₫)</th>
                      <th className={shared.colNum}>Mốc KL (g)</th>
                      <th className={shared.colNum}>Phụ phí / 500g (₫)</th>
                    </tr>
                  </thead>
                  <tbody>
                    {draft.fallback_rates.map((rate) => {
                      const regionKey = rate.region as GatewayShippingRegion;
                      return (
                        <tr key={rate.region}>
                          <td>{SHIPPING_REGION_LABELS[rate.region] ?? rate.region}</td>
                          <td className={shared.colNum}>
                            <input
                              type="number"
                              min={0}
                              step={1000}
                              className={styles.compactInput}
                              value={rate.base_fee_vnd}
                              onChange={(e) =>
                                updateRateField(code, regionKey, "base_fee_vnd", e.target.value)
                              }
                              aria-label={`Cước cơ bản ${SHIPPING_REGION_LABELS[rate.region] ?? rate.region} của ${carrier.name}`}
                              disabled={isSaving}
                            />
                          </td>
                          <td className={shared.colNum}>
                            <input
                              type="number"
                              min={100}
                              step={100}
                              className={styles.compactInput}
                              value={rate.base_weight_grams}
                              onChange={(e) =>
                                updateRateField(
                                  code,
                                  regionKey,
                                  "base_weight_grams",
                                  e.target.value
                                )
                              }
                              aria-label={`Mốc khối lượng ${SHIPPING_REGION_LABELS[rate.region] ?? rate.region} của ${carrier.name}`}
                              disabled={isSaving}
                            />
                          </td>
                          <td className={shared.colNum}>
                            <input
                              type="number"
                              min={0}
                              step={500}
                              className={styles.compactInput}
                              value={rate.extra_fee_vnd_per_500g}
                              onChange={(e) =>
                                updateRateField(
                                  code,
                                  regionKey,
                                  "extra_fee_vnd_per_500g",
                                  e.target.value
                                )
                              }
                              aria-label={`Phụ phí mỗi 500g ${SHIPPING_REGION_LABELS[rate.region] ?? rate.region} của ${carrier.name}`}
                              disabled={isSaving}
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={styles.commissionMobileList}>
                {draft.fallback_rates.map((rate) => {
                  const regionKey = rate.region as GatewayShippingRegion;
                  const regionLabel = SHIPPING_REGION_LABELS[rate.region] ?? rate.region;
                  return (
                    <div key={rate.region} className={styles.commissionMobileCard}>
                      <span className={styles.fieldLabel}>{regionLabel}</span>
                      <div className={styles.commissionMobileInputs}>
                        <div className={styles.fieldGroup}>
                          <label className={styles.fieldHint}>Cước cơ bản (₫)</label>
                          <input
                            type="number"
                            min={0}
                            step={1000}
                            className={`${styles.settingInput} ${styles.settingInputNum}`}
                            value={rate.base_fee_vnd}
                            onChange={(e) =>
                              updateRateField(code, regionKey, "base_fee_vnd", e.target.value)
                            }
                            disabled={isSaving}
                          />
                        </div>
                        <div className={styles.fieldGroup}>
                          <label className={styles.fieldHint}>Mốc KL (g)</label>
                          <input
                            type="number"
                            min={100}
                            step={100}
                            className={`${styles.settingInput} ${styles.settingInputNum}`}
                            value={rate.base_weight_grams}
                            onChange={(e) =>
                              updateRateField(code, regionKey, "base_weight_grams", e.target.value)
                            }
                            disabled={isSaving}
                          />
                        </div>
                        <div className={styles.fieldGroup}>
                          <label className={styles.fieldHint}>Phụ phí / 500g (₫)</label>
                          <input
                            type="number"
                            min={0}
                            step={500}
                            className={`${styles.settingInput} ${styles.settingInputNum}`}
                            value={rate.extra_fee_vnd_per_500g}
                            onChange={(e) =>
                              updateRateField(
                                code,
                                regionKey,
                                "extra_fee_vnd_per_500g",
                                e.target.value
                              )
                            }
                            disabled={isSaving}
                          />
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>

              {rowErr && (
                <p className={shared.validationError} role="alert">
                  {rowErr}
                </p>
              )}

              {testRes && (
                <div
                  style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}
                  role="status"
                >
                  <StatusPill
                    label={testRes.ok ? "Kết nối ổn định" : "Kiểm tra thất bại"}
                    tone={testRes.ok ? "positive" : "negative"}
                  />
                  <span className={styles.fieldHint}>{testRes.message}</span>
                </div>
              )}

              <div className={styles.panelFooter}>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={isTesting || isSaving}
                  onClick={() => void handleTestCarrier(carrier)}
                >
                  {isTesting ? "Đang kiểm tra..." : "Kiểm tra kết nối"}
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  size="sm"
                  shape="app"
                  disabled={isSaving || isTesting}
                >
                  {isSaving ? "Đang lưu..." : "Lưu cấu hình hãng"}
                </Button>
              </div>
            </form>
          </section>
        );
      })}
    </div>
  );
}
