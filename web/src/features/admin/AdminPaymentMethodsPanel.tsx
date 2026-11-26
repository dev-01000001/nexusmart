"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { AlertCircle, Info, RefreshCw } from "lucide-react";
import { ErrorState, SaveBar, StatusBadge } from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { GATEWAY_PAYMENT_METHOD_LABELS, formatDateTimeVi, formatVnd } from "@/lib/format";
import type {
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
  GatewayPaymentMethodsResponse,
} from "@/lib/gateway-types";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { TableLoadingSkeleton } from "./config/adminUiHelpers";

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

function buildDraftFromConfig(cfg: GatewayPaymentMethodConfig): MethodDraft {
  return {
    enabled: Boolean(cfg.enabled),
    minOrderStr: String(Math.max(0, Math.trunc(cfg.min_order_vnd || 0))),
    maxOrderStr: String(Math.max(0, Math.trunc(cfg.max_order_vnd || 0))),
    expiryMinutesStr: String(Math.max(0, Math.trunc(cfg.expiry_minutes || 0))),
    note: "",
  };
}

function buildDraftMap(list: GatewayPaymentMethodConfig[]): Record<string, MethodDraft> {
  const map: Record<string, MethodDraft> = {};
  for (const cfg of list) {
    map[cfg.method] = buildDraftFromConfig(cfg);
  }
  return map;
}

function isMethodDirty(cfg: GatewayPaymentMethodConfig, draft?: MethodDraft): boolean {
  if (!draft) return false;
  const base = buildDraftFromConfig(cfg);
  return (
    draft.enabled !== base.enabled ||
    draft.minOrderStr !== base.minOrderStr ||
    draft.maxOrderStr !== base.maxOrderStr ||
    draft.expiryMinutesStr !== base.expiryMinutesStr ||
    draft.note.trim().length > 0
  );
}

export function AdminPaymentMethodsPanel() {
  const { showToast } = useAdminContext();
  const canWrite = useAdminCapability("payment_methods.write");

  const [methods, setMethods] = useState<GatewayPaymentMethodConfig[]>([]);
  const [drafts, setDrafts] = useState<Record<string, MethodDraft>>({});
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savingMethod, setSavingMethod] = useState<string | null>(null);
  const [savingAll, setSavingAll] = useState(false);
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
    setMethodErrors((prev) => {
      if (!prev[method]) return prev;
      const next = { ...prev };
      delete next[method];
      return next;
    });
  };

  const handleSave = async (cfg: GatewayPaymentMethodConfig): Promise<boolean> => {
    if (!canWrite || savingMethod) return false;
    const draft = drafts[cfg.method];
    if (!draft) return false;

    const minVnd = Math.trunc(Number(draft.minOrderStr));
    const maxVnd = Math.trunc(Number(draft.maxOrderStr));
    const expiryMinutes = Math.trunc(Number(draft.expiryMinutesStr));

    if (!Number.isFinite(minVnd) || minVnd < 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị đơn tối thiểu phải là số nguyên VND không âm.",
      }));
      return false;
    }
    if (!Number.isFinite(maxVnd) || maxVnd < 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị đơn tối đa phải là số nguyên VND không âm (0 = không giới hạn).",
      }));
      return false;
    }
    if (maxVnd > 0 && minVnd > maxVnd) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Giá trị tối thiểu không được lớn hơn giá trị tối đa.",
      }));
      return false;
    }
    if (!Number.isFinite(expiryMinutes) || expiryMinutes < 0 || expiryMinutes > 1440) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Thời hạn giữ đơn phải từ 0 đến 1440 phút.",
      }));
      return false;
    }

    const enabledOthers = methods.filter((m) => m.method !== cfg.method && m.enabled).length;
    if (!draft.enabled && enabledOthers === 0) {
      setMethodErrors((prev) => ({
        ...prev,
        [cfg.method]: "Phải giữ ít nhất một phương thức thanh toán đang bật.",
      }));
      return false;
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
          [cfg.method]: buildDraftFromConfig(res.method),
        }));
      } else {
        await loadMethods();
      }
      showToast(`Đã cập nhật cấu hình ${getMethodTitle(cfg.method)}.`);
      return true;
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
      return false;
    } finally {
      setSavingMethod(null);
    }
  };

  const dirtyMethods = useMemo(
    () => methods.filter((cfg) => isMethodDirty(cfg, drafts[cfg.method])),
    [methods, drafts]
  );

  const handleSaveAllDirty = async () => {
    if (!canWrite || savingAll || dirtyMethods.length === 0) return;
    setSavingAll(true);
    try {
      for (const cfg of dirtyMethods) {
        const ok = await handleSave(cfg);
        if (!ok) break;
      }
    } finally {
      setSavingAll(false);
    }
  };

  const handleDiscardAll = () => {
    setDrafts(buildDraftMap(methods));
    setMethodErrors({});
    setConflictMethod(null);
  };

  if (loading) {
    return <TableLoadingSkeleton rows={4} label="Đang tải cấu hình cổng thanh toán..." />;
  }

  if (errorMsg) {
    return (
      <ErrorState
        title="Không thể tải cấu hình cổng thanh toán"
        description={errorMsg}
        onRetry={() => void loadMethods()}
      />
    );
  }

  const enabledCount = methods.filter((m) => m.enabled).length;

  return (
    <div className="max-w-[800px] space-y-6">
      {canWrite ? (
        <SaveBar
          visible={dirtyMethods.length > 0}
          saving={savingAll || Boolean(savingMethod)}
          message={`Có ${dirtyMethods.length} cổng thanh toán chưa lưu thay đổi`}
          onDiscard={handleDiscardAll}
          onSave={() => void handleSaveAllDirty()}
        />
      ) : null}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="pm-config-heading" className="text-sm font-semibold text-foreground">
            Cổng thanh toán ({enabledCount}/{methods.length} đang bật)
          </h2>
          <p className="text-[13px] text-muted-foreground">
            Điều chỉnh trạng thái bật/tắt, hạn mức đơn hàng và thời hạn giữ đơn cho từng cổng.
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void loadMethods()}>
          <RefreshCw className="size-3.5" aria-hidden="true" />
          <span>Làm mới</span>
        </Button>
      </div>

      {!canWrite ? (
        <Alert>
          <Info className="size-4" aria-hidden="true" />
          <AlertDescription>Chỉ có quyền xem cấu hình phương thức thanh toán.</AlertDescription>
        </Alert>
      ) : null}

      {conflictMethod ? (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription className="flex w-full flex-wrap items-center justify-between gap-2">
            <span>
              Cấu hình phương thức <strong>{getMethodTitle(conflictMethod)}</strong> đã thay đổi từ
              phiên khác. Vui lòng tải lại dữ liệu mới nhất trước khi lưu.
            </span>
            <Button type="button" size="xs" onClick={() => void loadMethods()}>
              Tải lại cấu hình mới nhất
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="space-y-6">
        {methods.map((cfg, idx) => {
          const draft = drafts[cfg.method] ?? buildDraftFromConfig(cfg);
          const isSaving = savingMethod === cfg.method;
          const errText = methodErrors[cfg.method];
          const isCod = cfg.method.toUpperCase() === "COD";

          return (
            <React.Fragment key={cfg.method}>
              {idx > 0 ? <Separator /> : null}
              <section
                aria-labelledby={`pm-heading-${cfg.method}`}
                className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] md:items-start"
              >
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3
                      id={`pm-heading-${cfg.method}`}
                      className="text-sm font-semibold text-foreground"
                    >
                      {getMethodTitle(cfg.method)}
                    </h3>
                    <StatusBadge tone={draft.enabled ? "success" : "warning"}>
                      {draft.enabled ? "Đang bật" : "Đang tắt"}
                    </StatusBadge>
                  </div>

                  {typeof cfg.credentials_configured === "boolean" && !isCod ? (
                    <div>
                      <StatusBadge tone={cfg.credentials_configured ? "success" : "neutral"}>
                        {cfg.credentials_configured ? "Đã cấu hình khóa" : "Chế độ mô phỏng"}
                      </StatusBadge>
                    </div>
                  ) : null}

                  <p className="text-xs leading-[18px] text-muted-foreground tabular-nums">
                    Phiên bản v{cfg.version}
                    {cfg.updated_at ? ` · Cập nhật ${formatDateTimeVi(cfg.updated_at)}` : ""}
                    {cfg.credential_preview ? ` · Chứng thực: ${cfg.credential_preview}` : ""}
                  </p>
                </div>

                <Card>
                  <form
                    noValidate
                    onSubmit={(e) => {
                      e.preventDefault();
                      void handleSave(cfg);
                    }}
                  >
                    <CardContent className="space-y-4 pt-4">
                      <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted/35 px-3.5 py-2.5">
                        <div className="min-w-0">
                          <div className="text-sm font-medium text-foreground">Trạng thái</div>
                          <div className="text-xs text-muted-foreground">
                            {draft.enabled ? "Cho phép đặt đơn" : "Tạm tắt"}
                          </div>
                        </div>
                        <Switch
                          checked={draft.enabled}
                          disabled={!canWrite || isSaving || savingAll}
                          aria-label={`Bật hoặc tắt ${getMethodTitle(cfg.method)}`}
                          onCheckedChange={(checked) =>
                            updateDraft(cfg.method, { enabled: checked })
                          }
                        />
                      </div>

                      <FieldGroup className="gap-4">
                        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                          <Field className="gap-1.5">
                            <FieldLabel htmlFor={`pm-min-${cfg.method}`}>
                              Đơn tối thiểu (VND)
                            </FieldLabel>
                            <Input
                              id={`pm-min-${cfg.method}`}
                              type="number"
                              min={0}
                              step={1}
                              disabled={!canWrite || isSaving || savingAll}
                              value={draft.minOrderStr}
                              onChange={(e) =>
                                updateDraft(cfg.method, { minOrderStr: e.target.value })
                              }
                              className="bg-card tabular-nums"
                            />
                            <FieldDescription className="text-xs tabular-nums">
                              {Number(draft.minOrderStr) > 0
                                ? formatVnd(Math.trunc(Number(draft.minOrderStr)))
                                : "Không giới hạn dưới"}
                            </FieldDescription>
                          </Field>

                          <Field className="gap-1.5">
                            <FieldLabel htmlFor={`pm-max-${cfg.method}`}>
                              Đơn tối đa (VND, 0 = không giới hạn)
                            </FieldLabel>
                            <Input
                              id={`pm-max-${cfg.method}`}
                              type="number"
                              min={0}
                              step={1}
                              disabled={!canWrite || isSaving || savingAll}
                              value={draft.maxOrderStr}
                              onChange={(e) =>
                                updateDraft(cfg.method, { maxOrderStr: e.target.value })
                              }
                              className="bg-card tabular-nums"
                            />
                            <FieldDescription className="text-xs tabular-nums">
                              {Number(draft.maxOrderStr) > 0
                                ? formatVnd(Math.trunc(Number(draft.maxOrderStr)))
                                : "Không giới hạn trên"}
                            </FieldDescription>
                          </Field>
                        </div>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor={`pm-exp-${cfg.method}`}>
                            Hạn giữ đơn chờ thanh toán (phút)
                          </FieldLabel>
                          <Input
                            id={`pm-exp-${cfg.method}`}
                            type="number"
                            min={0}
                            max={1440}
                            step={1}
                            disabled={!canWrite || isSaving || savingAll || isCod}
                            value={draft.expiryMinutesStr}
                            onChange={(e) =>
                              updateDraft(cfg.method, { expiryMinutesStr: e.target.value })
                            }
                            className="max-w-44 bg-card tabular-nums"
                          />
                        </Field>

                        <Field className="gap-1.5">
                          <FieldLabel htmlFor={`pm-note-${cfg.method}`}>
                            Ghi chú thay đổi
                          </FieldLabel>
                          <Input
                            id={`pm-note-${cfg.method}`}
                            type="text"
                            disabled={!canWrite || isSaving || savingAll}
                            value={draft.note}
                            onChange={(e) => updateDraft(cfg.method, { note: e.target.value })}
                            placeholder="Nhập lý do điều chỉnh cấu hình..."
                            className="bg-card"
                          />
                        </Field>

                        <FieldError>{errText}</FieldError>
                      </FieldGroup>
                    </CardContent>

                    {canWrite ? (
                      <CardFooter className="justify-end border-t border-border pt-3.5">
                        <Button type="submit" size="sm" disabled={isSaving || savingAll}>
                          {isSaving ? "Đang lưu..." : `Lưu cấu hình ${cfg.method}`}
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
    </div>
  );
}
