"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Button, StateView } from "@/components";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { GATEWAY_PAYMENT_METHOD_LABELS, formatDateTimeVi, formatVnd } from "@/lib/format";
import type {
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
  GatewayPaymentMethodsResponse,
} from "@/lib/gateway-types";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { StatusPill } from "./StatusPill";
import shared from "./adminShared.module.css";

interface MethodDraft {
  enabled: boolean;
  minOrderStr: string;
  maxOrderStr: string;
  expiryMinutesStr: string;
  note: string;
}

function getMethodTitle(method: string): string {
  const key = method.trim().toUpperCase() as GatewayPaymentMethod;
  return GATEWAY_PAYMENT_METHOD_LABELS[key] ?? method;
}

function buildDraftMap(list: GatewayPaymentMethodConfig[]): Record<string, MethodDraft> {
  const map: Record<string, MethodDraft> = {};
  for (const cfg of list) {
    map[cfg.method] = {
      enabled: Boolean(cfg.enabled),
      minOrderStr: String(Math.max(0, Math.trunc(cfg.min_order_vnd || 0))),
      maxOrderStr: String(Math.max(0, Math.trunc(cfg.max_order_vnd || 0))),
      expiryMinutesStr: String(Math.max(0, Math.trunc(cfg.expiry_minutes || 0))),
      note: "",
    };
  }
  return map;
}

export function AdminPaymentMethodsPanel() {
  const { showToast } = useAdminContext();
  const canWrite = useAdminCapability("payment_methods.write");

  const [methods, setMethods] = useState<GatewayPaymentMethodConfig[]>([]);
  const [drafts, setDrafts] = useState<Record<string, MethodDraft>>({});
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savingMethod, setSavingMethod] = useState<string | null>(null);
  const [conflictMethod, setConflictMethod] = useState<string | null>(null);
  const [methodErrors, setMethodErrors] = useState<Record<string, string>>({});

  const loadMethods = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    setConflictMethod(null);
    try {
      const res = await api<GatewayPaymentMethodsResponse>("/api/admin/payment-methods");
      const list = Array.isArray(res?.methods) ? res.methods : [];
      setMethods(list);
      setDrafts(buildDraftMap(list));
      setMethodErrors({});
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải cấu hình phương thức thanh toán lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadMethods();
  }, [loadMethods]);

  const updateDraft = (method: string, patch: Partial<MethodDraft>) => {
    setDrafts((prev) => ({
      ...prev,
      [method]: {
        ...(prev[method] ?? {
          enabled: true,
          minOrderStr: "0",
          maxOrderStr: "0",
          expiryMinutesStr: "15",
          note: "",
        }),
        ...patch,
      },
    }));
  };

  const handleSave = async (cfg: GatewayPaymentMethodConfig) => {
    if (!canWrite || savingMethod) return;
    const draft = drafts[cfg.method];
    if (!draft) return;

    const minVnd = Math.trunc(Number(draft.minOrderStr));
    const maxVnd = Math.trunc(Number(draft.maxOrderStr));
    const expiryMinutes = Math.trunc(Number(draft.expiryMinutesStr));

    if (!Number.isFinite(minVnd) || minVnd < 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị đơn tối thiểu phải là số nguyên VND không âm.",
      }));
      return;
    }
    if (!Number.isFinite(maxVnd) || maxVnd < 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị đơn tối đa phải là số nguyên VND không âm (0 = không giới hạn).",
      }));
      return;
    }
    if (maxVnd > 0 && minVnd > maxVnd) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị tối thiểu không được lớn hơn giá trị tối đa.",
      }));
      return;
    }
    if (!Number.isFinite(expiryMinutes) || expiryMinutes < 0 || expiryMinutes > 1440) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Thời hạn giữ đơn phải từ 0 đến 1440 phút.",
      }));
      return;
    }

    const enabledOthers = methods.filter((m) => m.method !== cfg.method && m.enabled).length;
    if (!draft.enabled && enabledOthers === 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Phải giữ ít nhất một phương thức thanh toán đang bật.",
      }));
      return;
    }

    setSavingMethod(cfg.method);
    setConflictMethod(null);
    setMethodErrors((prev) => {
      const next = { ...prev };
      delete next[cfg.method];
      return next;
    });

    try {
      const res = await api<{ method: GatewayPaymentMethodConfig }>(
        `/api/admin/payment-methods/${encodeURIComponent(cfg.method)}`,
        {
          method: "PUT",
          json: {
            enabled: draft.enabled,
            min_order_vnd: minVnd,
            max_order_vnd: maxVnd,
            expiry_minutes: expiryMinutes,
            note: draft.note.trim(),
            expected_version: cfg.version,
          },
        }
      );
      if (res?.method) {
        setMethods((prev) => prev.map((m) => (m.method === cfg.method ? res.method : m)));
        setDrafts((prev) => ({
          ...prev,
          [cfg.method]: {
            enabled: Boolean(res.method.enabled),
            minOrderStr: String(Math.max(0, Math.trunc(res.method.min_order_vnd || 0))),
            maxOrderStr: String(Math.max(0, Math.trunc(res.method.max_order_vnd || 0))),
            expiryMinutesStr: String(Math.max(0, Math.trunc(res.method.expiry_minutes || 0))),
            note: "",
          },
        }));
      } else {
        await loadMethods();
      }
      showToast(`Đã cập nhật cấu hình ${getMethodTitle(cfg.method)}.`);
    } catch (err) {
      if (
        err instanceof ApiError &&
        (err.status === 409 ||
          err.code === "VERSION_CONFLICT" ||
          err.code === "FAILED_PRECONDITION")
      ) {
        setConflictMethod(cfg.method);
        setMethodErrors((prev) => ({
          ...prev,
          [cfg.method]:
            err.message ||
            "Cấu hình đã được người khác cập nhật trước đó. Vui lòng tải lại dữ liệu mới nhất trước khi lưu.",
        }));
      } else {
        setMethodErrors((prev) => ({
          ...prev,
          [cfg.method]: getErrorMessage(err, "Không thể lưu cấu hình phương thức thanh toán."),
        }));
      }
    } finally {
      setSavingMethod(null);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải cấu hình cổng thanh toán..."
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
          title="Không thể tải cấu hình cổng thanh toán"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadMethods()}
        />
      </div>
    );
  }

  const enabledCount = methods.filter((m) => m.enabled).length;

  return (
    <div className={shared.sectionStack}>
      <section className={shared.panel} aria-labelledby="pm-config-heading">
        <div className={shared.panelHeader}>
          <div>
            <h2 id="pm-config-heading" className={shared.panelTitle}>
              Cổng thanh toán ({enabledCount}/{methods.length} đang bật)
            </h2>
          </div>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadMethods()}>
            Làm mới
          </Button>
        </div>

        {!canWrite && (
          <p className={shared.savedStamp} style={{ marginBottom: 12 }}>
            Chỉ có quyền xem cấu hình phương thức thanh toán.
          </p>
        )}

        {conflictMethod && (
          <div className={shared.detailWell} role="alert" style={{ marginBottom: 16 }}>
            <p className={shared.validationError}>
              Cấu hình phương thức <strong>{getMethodTitle(conflictMethod)}</strong> đã thay đổi từ
              phiên khác. Vui lòng tải lại dữ liệu mới nhất trước khi lưu.
            </p>
            <div>
              <Button variant="primary" size="sm" shape="app" onClick={() => void loadMethods()}>
                Tải lại cấu hình mới nhất
              </Button>
            </div>
          </div>
        )}

        <div className={shared.sectionStack}>
          {methods.map((cfg) => {
            const draft = drafts[cfg.method] ?? {
              enabled: cfg.enabled,
              minOrderStr: String(cfg.min_order_vnd ?? 0),
              maxOrderStr: String(cfg.max_order_vnd ?? 0),
              expiryMinutesStr: String(cfg.expiry_minutes ?? 0),
              note: "",
            };
            const isSaving = savingMethod === cfg.method;
            const errText = methodErrors[cfg.method];
            const isCod = cfg.method.toUpperCase() === "COD";

            return (
              <form
                key={cfg.method}
                className={shared.detailWell}
                noValidate
                onSubmit={(e) => {
                  e.preventDefault();
                  void handleSave(cfg);
                }}
              >
                <div className={shared.detailHeader}>
                  <div>
                    <h3 className={shared.detailHeading}>{getMethodTitle(cfg.method)}</h3>
                    <p className={shared.cellSub}>
                      Phiên bản v{cfg.version}
                      {cfg.updated_at ? ` · Cập nhật ${formatDateTimeVi(cfg.updated_at)}` : ""}
                      {cfg.credential_preview ? ` · Chứng thực: ${cfg.credential_preview}` : ""}
                    </p>
                  </div>
                  <div className={shared.detailHeaderMeta}>
                    {typeof cfg.credentials_configured === "boolean" && !isCod && (
                      <StatusPill
                        label={cfg.credentials_configured ? "Đã cấu hình khóa" : "Chế độ mô phỏng"}
                        tone={cfg.credentials_configured ? "positive" : "neutral"}
                      />
                    )}
                    <StatusPill
                      label={draft.enabled ? "Đang bật" : "Đang tắt"}
                      tone={draft.enabled ? "positive" : "warning"}
                    />
                  </div>
                </div>

                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <span className={shared.fieldLabel}>Trạng thái</span>
                    <div style={{ display: "flex", alignItems: "center", gap: 10, minHeight: 38 }}>
                      <button
                        type="button"
                        role="switch"
                        aria-checked={draft.enabled}
                        aria-label={`Bật hoặc tắt ${getMethodTitle(cfg.method)}`}
                        disabled={!canWrite || isSaving}
                        className={`${shared.toggleBtn} ${draft.enabled ? shared.toggleBtnOn : ""}`}
                        onClick={() => updateDraft(cfg.method, { enabled: !draft.enabled })}
                      >
                        <span className={shared.toggleKnob} />
                      </button>
                      <span className={shared.detailValPlain}>
                        {draft.enabled ? "Cho phép đặt đơn" : "Tạm tắt"}
                      </span>
                    </div>
                  </div>

                  <div className={shared.detailField}>
                    <label htmlFor={`pm-min-${cfg.method}`} className={shared.fieldLabel}>
                      Đơn tối thiểu (VND)
                    </label>
                    <input
                      id={`pm-min-${cfg.method}`}
                      type="number"
                      min={0}
                      step={1}
                      disabled={!canWrite || isSaving}
                      className={shared.textInput}
                      value={draft.minOrderStr}
                      onChange={(e) => updateDraft(cfg.method, { minOrderStr: e.target.value })}
                    />
                    <span className={shared.cellSub}>
                      {Number(draft.minOrderStr) > 0
                        ? formatVnd(Math.trunc(Number(draft.minOrderStr)))
                        : "Không giới hạn dưới"}
                    </span>
                  </div>

                  <div className={shared.detailField}>
                    <label htmlFor={`pm-max-${cfg.method}`} className={shared.fieldLabel}>
                      Đơn tối đa (VND, 0 = không giới hạn)
                    </label>
                    <input
                      id={`pm-max-${cfg.method}`}
                      type="number"
                      min={0}
                      step={1}
                      disabled={!canWrite || isSaving}
                      className={shared.textInput}
                      value={draft.maxOrderStr}
                      onChange={(e) => updateDraft(cfg.method, { maxOrderStr: e.target.value })}
                    />
                    <span className={shared.cellSub}>
                      {Number(draft.maxOrderStr) > 0
                        ? formatVnd(Math.trunc(Number(draft.maxOrderStr)))
                        : "Không giới hạn trên"}
                    </span>
                  </div>

                  <div className={shared.detailField}>
                    <label htmlFor={`pm-exp-${cfg.method}`} className={shared.fieldLabel}>
                      Hạn giữ đơn chờ thanh toán (phút)
                    </label>
                    <input
                      id={`pm-exp-${cfg.method}`}
                      type="number"
                      min={0}
                      max={1440}
                      step={1}
                      disabled={!canWrite || isSaving || isCod}
                      className={shared.textInput}
                      value={draft.expiryMinutesStr}
                      onChange={(e) =>
                        updateDraft(cfg.method, { expiryMinutesStr: e.target.value })
                      }
                    />
                  </div>

                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <label htmlFor={`pm-note-${cfg.method}`} className={shared.fieldLabel}>
                      Ghi chú thay đổi
                    </label>
                    <input
                      id={`pm-note-${cfg.method}`}
                      type="text"
                      disabled={!canWrite || isSaving}
                      className={shared.textInput}
                      value={draft.note}
                      onChange={(e) => updateDraft(cfg.method, { note: e.target.value })}
                      placeholder="Nhập lý do điều chỉnh cấu hình..."
                    />
                  </div>
                </div>

                {errText && (
                  <p className={shared.validationError} role="alert">
                    {errText}
                  </p>
                )}

                {canWrite && (
                  <div className={shared.detailButtons}>
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      loading={isSaving}
                    >
                      Lưu cấu hình {cfg.method}
                    </Button>
                  </div>
                )}
              </form>
            );
          })}
        </div>
      </section>
    </div>
  );
}
