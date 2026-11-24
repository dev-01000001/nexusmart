"use client";

import React, { useCallback, useEffect, useState } from "react";
import type {
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
  GatewayPaymentMethodsResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { GATEWAY_PAYMENT_METHOD_LABELS, formatDateTimeVi, formatVnd } from "@/lib/format";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { StatusPill } from "./StatusPill";
import { useAdminCapability } from "./AdminUserContext";
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
  const { showToast } = useMockStore();
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
      const res = await api<GatewayPaymentMethodsResponse>("admin/payment-methods");
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
        [cfg.method]: "Thời hạn giữ đơn (phút) phải từ 0 đến 1440.",
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
        `admin/payment-methods/${encodeURIComponent(cfg.method)}`,
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
            "Cấu hình đã được người khác cập nhật trước đó (VERSION_CONFLICT). Vui lòng tải lại trước khi lưu.",
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
              Cấu hình phương thức thanh toán (COD / VietQR / ZaloPay)
            </h2>
            <span className={shared.panelMeta}>
              Đang bật {enabledCount}/{methods.length} phương thức · Áp dụng trực tiếp cho trang
              thanh toán &amp; đổi phương thức
            </span>
          </div>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadMethods()}>
            Làm mới
          </Button>
        </div>

        {!canWrite && (
          <p className={shared.savedStamp} style={{ marginBottom: 12 }}>
            Tài khoản hiện có quyền xem cấu hình phương thức thanh toán (chỉ Quản trị viên / Vận
            hành được phép chỉnh sửa).
          </p>
        )}

        {conflictMethod && (
          <div
            className={shared.detailWell}
            role="alert"
            style={{ marginBottom: 16, borderLeft: "3px solid var(--negative)" }}
          >
            <p className={shared.validationError}>
              Phát hiện xung đột phiên bản (VERSION_CONFLICT) khi lưu phương thức{" "}
              <strong>{conflictMethod}</strong>. Dữ liệu trên máy chủ đã thay đổi từ một phiên khác.
            </p>
            <div>
              <Button variant="primary" size="sm" shape="app" onClick={() => void loadMethods()}>
                Tải lại cấu hình mới nhất
              </Button>
            </div>
          </div>
        )}

        <div className={shared.sectionStack} style={{ marginTop: 8 }}>
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
                    <h3 className={shared.detailHeading}>
                      {getMethodTitle(cfg.method)} ({cfg.method})
                    </h3>
                    <p className={shared.cellSub}>
                      Phiên bản v{cfg.version}
                      {cfg.updated_at ? ` · Cập nhật ${formatDateTimeVi(cfg.updated_at)}` : ""}
                      {cfg.credential_preview ? ` · Chứng thực: ${cfg.credential_preview}` : ""}
                    </p>
                  </div>
                  <div className={shared.detailHeaderMeta}>
                    {typeof cfg.credentials_configured === "boolean" && !isCod && (
                      <StatusPill
                        label={cfg.credentials_configured ? "Đã cấu hình khoá" : "Chế độ mô phỏng"}
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
                    <span className={shared.fieldLabel}>Trạng thái hiển thị cho người mua</span>
                    <div style={{ display: "flex", alignItems: "center", gap: 10, minHeight: 38 }}>
                      <button
                        type="button"
                        role="switch"
                        aria-checked={draft.enabled}
                        aria-label={`Bật hoặc tắt ${cfg.method}`}
                        disabled={!canWrite || isSaving}
                        className={`${shared.toggleBtn} ${draft.enabled ? shared.toggleBtnOn : ""}`}
                        onClick={() => updateDraft(cfg.method, { enabled: !draft.enabled })}
                      >
                        <span className={shared.toggleKnob} />
                      </button>
                      <span className={shared.detailValPlain}>
                        {draft.enabled ? "Đang cho phép đặt đơn" : "Tạm tắt (chặn đơn mới)"}
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
                      Hiện tại:{" "}
                      {Number(draft.minOrderStr) > 0
                        ? formatVnd(Math.trunc(Number(draft.minOrderStr)))
                        : "0₫ (không giới hạn dưới)"}
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
                      Hiện tại:{" "}
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
                    <span className={shared.cellSub}>
                      {isCod
                        ? "COD xác nhận ngay, không cần chờ thanh toán"
                        : "Áp dụng cho giao dịch mới"}
                    </span>
                  </div>

                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <label htmlFor={`pm-note-${cfg.method}`} className={shared.fieldLabel}>
                      Ghi chú thay đổi (ghi nhận vào nhật ký kiểm toán)
                    </label>
                    <input
                      id={`pm-note-${cfg.method}`}
                      type="text"
                      disabled={!canWrite || isSaving}
                      className={shared.textInput}
                      value={draft.note}
                      onChange={(e) => updateDraft(cfg.method, { note: e.target.value })}
                      placeholder="VD: Hạ hạn mức COD xuống 2.000.000₫ trong đợt khuyến mãi..."
                    />
                  </div>
                </div>

                {errText && (
                  <p className={shared.validationError} role="alert">
                    {errText}
                  </p>
                )}

                {canWrite && (
                  <div className={shared.detailActionBar}>
                    <span className={shared.savedStamp}>
                      Tắt phương thức chỉ chặn đơn mới; giao dịch đang chờ vẫn nhận callback bình
                      thường.
                    </span>
                    <Button
                      type="submit"
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={isSaving}
                    >
                      {isSaving ? "Đang lưu..." : `Lưu cấu hình ${cfg.method}`}
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
