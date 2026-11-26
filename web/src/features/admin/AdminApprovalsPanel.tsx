"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Info, RefreshCw, ShieldCheck } from "lucide-react";
import { EmptyState, ErrorState, StatusBadge, type StatusTone } from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { filterUnresolvedActorIds } from "@/lib/a8-types";
import { canDecideTwoPersonApproval, roleHasCapability } from "@/lib/admin-rbac";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type {
  GatewayAdminApprovalsResponse,
  GatewayRefund,
  GatewayShop,
  GatewayShopSuspensionApproval,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { localizeRefundReason } from "@/features/account/orderMeta";
import { EMPTY_DASH } from "./adminMeta";
import { useAdminContext } from "./AdminShell";
import { useAdminUser } from "./AdminUserContext";
import {
  type ApprovalActorLookupEntry,
  fetchShopOwnerById,
  formatApprovalActorDisplay,
} from "./types";
import {
  DetailGrid,
  DetailItem,
  FilterSelect,
  TableLoadingSkeleton,
  approvalStatusTone,
  refundStatusTone,
} from "./config/adminUiHelpers";

type ApprovalTypeFilter = "" | "shop" | "refund";
type ApprovalStatusFilter = "PENDING" | "APPROVED" | "REJECTED";

function getShopApprovalStatusMeta(status: string): { label: string; tone: StatusTone } {
  const upper = status.toUpperCase();
  switch (upper) {
    case "PENDING":
      return { label: "Chờ duyệt 2 người", tone: approvalStatusTone(upper) };
    case "APPROVED":
      return { label: "Đã phê duyệt", tone: approvalStatusTone(upper) };
    case "REJECTED":
      return { label: "Đã từ chối", tone: approvalStatusTone(upper) };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

function getRefundApprovalStatusMeta(status: string): { label: string; tone: StatusTone } {
  const upper = status.toUpperCase();
  switch (upper) {
    case "PENDING_APPROVAL":
      return { label: "Chờ duyệt 2 người", tone: refundStatusTone(upper) };
    case "PENDING":
      return { label: "Đã duyệt (chờ chuyển tiền)", tone: refundStatusTone(upper) };
    case "DONE":
      return { label: "Đã hoàn tất", tone: refundStatusTone(upper) };
    case "REJECTED":
      return { label: "Đã từ chối", tone: refundStatusTone(upper) };
    case "MANUAL_REVIEW":
      return { label: "Cần xử lý tay", tone: refundStatusTone(upper) };
    case "FAILED":
      return { label: "Thất bại", tone: refundStatusTone(upper) };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

export function AdminApprovalsPanel() {
  const { showToast } = useAdminContext();
  const adminUser = useAdminUser();
  const actorId = adminUser?.id ?? "";
  const actorRole = adminUser?.role ?? "";

  const canReadShopApprovals = roleHasCapability(actorRole, "shops.approvals.read");
  const canReadRefunds = roleHasCapability(actorRole, "refunds.read");
  const canReadUsers = roleHasCapability(actorRole, "users.read");

  const [statusFilter, setStatusFilter] = useState<ApprovalStatusFilter>("PENDING");
  const [typeFilter, setTypeFilter] = useState<ApprovalTypeFilter>("");
  const [shopApprovals, setShopApprovals] = useState<GatewayShopSuspensionApproval[]>([]);
  const [refundApprovals, setRefundApprovals] = useState<GatewayRefund[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [noteById, setNoteById] = useState<Record<string, string>>({});
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionErrorById, setActionErrorById] = useState<Record<string, string>>({});
  const [userById, setUserById] = useState<Record<string, ApprovalActorLookupEntry | null>>({});
  const fetchedUserIdsRef = useRef<Set<string>>(new Set());
  const loadSeqRef = useRef(0);

  const loadApprovals = useCallback(async () => {
    const seq = ++loadSeqRef.current;
    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams({ page: "1", page_size: "50", status: statusFilter });
      if (typeFilter) qs.set("type", typeFilter);
      const res = await api<GatewayAdminApprovalsResponse>(`/api/admin/approvals?${qs.toString()}`);
      if (loadSeqRef.current !== seq) return;
      setShopApprovals(Array.isArray(res?.shop_approvals) ? res.shop_approvals : []);
      setRefundApprovals(Array.isArray(res?.refund_approvals) ? res.refund_approvals : []);
    } catch (err) {
      if (loadSeqRef.current !== seq) return;
      setErrorMsg(getErrorMessage(err, "Không thể tải hàng đợi chờ duyệt 2 người lúc này."));
    } finally {
      if (loadSeqRef.current === seq) {
        setLoading(false);
      }
    }
  }, [statusFilter, typeFilter]);

  useEffect(() => {
    void loadApprovals();
  }, [loadApprovals]);

  // Resolve maker/checker UUIDs via GET /api/admin/users?query=<id>; mark settled only after fetch resolves.
  useEffect(() => {
    if (adminUser?.id) {
      fetchedUserIdsRef.current.add(adminUser.id);
      setUserById((prev) =>
        prev[adminUser.id]
          ? prev
          : {
              ...prev,
              [adminUser.id]: {
                full_name: adminUser.full_name,
                email: adminUser.email,
                role: adminUser.role,
              },
            }
      );
    }

    if (!canReadUsers) return;
    const candidateIds = new Set<string>();
    for (const item of shopApprovals) {
      if (item.requested_by) candidateIds.add(item.requested_by.trim());
      if (item.decided_by) candidateIds.add(item.decided_by.trim());
    }
    for (const rf of refundApprovals) {
      if (rf.requested_by) candidateIds.add(rf.requested_by.trim());
      if (rf.approved_by) candidateIds.add(rf.approved_by.trim());
    }

    const toFetch = filterUnresolvedActorIds(candidateIds, fetchedUserIdsRef.current);
    if (toFetch.length === 0) return;

    let cancelled = false;

    void Promise.all(
      toFetch.map(async (id) => {
        try {
          const found = await fetchShopOwnerById(id, (path) =>
            api<PaginatedResponse<GatewayUser>>(`/api/${path.replace(/^\/+/, "")}`)
          );
          if (cancelled) return;
          fetchedUserIdsRef.current.add(id);
          setUserById((prev) => ({
            ...prev,
            [id]: found
              ? { full_name: found.full_name, email: found.email, role: found.role }
              : null,
          }));
        } catch {
          if (cancelled) return;
          setUserById((prev) => (id in prev ? prev : { ...prev, [id]: null }));
        }
      })
    );

    return () => {
      cancelled = true;
    };
  }, [adminUser, canReadUsers, shopApprovals, refundApprovals]);

  const handleDecideShop = async (
    item: GatewayShopSuspensionApproval,
    decision: "APPROVE" | "REJECT"
  ) => {
    if (busyId) return;
    const note = (noteById[item.id] ?? "").trim();
    if (decision === "REJECT" && note.length < 3) {
      setActionErrorById((prev) => ({
        ...prev,
        [item.id]: "Vui lòng nhập lý do từ chối (tối thiểu 3 ký tự).",
      }));
      return;
    }
    setBusyId(item.id);
    setActionErrorById((prev) => {
      const next = { ...prev };
      delete next[item.id];
      return next;
    });
    try {
      await api<{ approval: GatewayShopSuspensionApproval; shop: GatewayShop }>(
        `/api/admin/approvals/shop/${encodeURIComponent(item.id)}/decide`,
        {
          method: "POST",
          json: { decision, note },
        }
      );
      showToast(
        decision === "APPROVE"
          ? "Đã phê duyệt tạm khóa gian hàng."
          : "Đã từ chối yêu cầu khóa gian hàng."
      );
      await loadApprovals();
    } catch (err) {
      setActionErrorById((prev) => ({
        ...prev,
        [item.id]: getErrorMessage(err, "Không thể xử lý phê duyệt yêu cầu khóa gian hàng."),
      }));
    } finally {
      setBusyId(null);
    }
  };

  const handleDecideRefund = async (rf: GatewayRefund, approve: boolean) => {
    if (busyId) return;
    const note = (noteById[rf.refund_id] ?? "").trim();
    if (!approve && note.length < 3) {
      setActionErrorById((prev) => ({
        ...prev,
        [rf.refund_id]: "Vui lòng nhập lý do từ chối hoàn tiền (tối thiểu 3 ký tự).",
      }));
      return;
    }
    setBusyId(rf.refund_id);
    setActionErrorById((prev) => {
      const next = { ...prev };
      delete next[rf.refund_id];
      return next;
    });
    try {
      await api<{ refund: GatewayRefund }>(
        `/api/admin/approvals/refund/${encodeURIComponent(rf.refund_id)}/decide`,
        {
          method: "POST",
          json: { approve, note },
        }
      );
      showToast(approve ? "Đã phê duyệt lệnh hoàn tiền." : "Đã từ chối lệnh hoàn tiền.");
      await loadApprovals();
    } catch (err) {
      setActionErrorById((prev) => ({
        ...prev,
        [rf.refund_id]: getErrorMessage(err, "Không thể xử lý phê duyệt hoàn tiền."),
      }));
    } finally {
      setBusyId(null);
    }
  };

  const showShopSection = canReadShopApprovals && (typeFilter === "" || typeFilter === "shop");
  const showRefundSection = canReadRefunds && (typeFilter === "" || typeFilter === "refund");

  const typeOptions = [
    { value: "", label: "Tất cả loại yêu cầu" },
    ...(canReadShopApprovals ? [{ value: "shop", label: "Khóa gian hàng" }] : []),
    ...(canReadRefunds ? [{ value: "refund", label: "Hoàn tiền giá trị lớn" }] : []),
  ];

  return (
    <div className="space-y-4">
      {/* Toolbar stays mounted while loading/reloading so keyboard focus is preserved */}
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl bg-card p-3 shadow-card">
        <div className="flex flex-1 flex-wrap items-center gap-2">
          <div className="w-full sm:w-48">
            <FilterSelect
              value={statusFilter}
              onValueChange={(v) => setStatusFilter(v as ApprovalStatusFilter)}
              ariaLabel="Lọc theo trạng thái phê duyệt"
              options={[
                { value: "PENDING", label: "Đang chờ duyệt" },
                { value: "APPROVED", label: "Đã phê duyệt" },
                { value: "REJECTED", label: "Đã từ chối" },
              ]}
            />
          </div>
          <div className="w-full sm:w-52">
            <FilterSelect
              value={typeFilter}
              onValueChange={(v) => setTypeFilter(v as ApprovalTypeFilter)}
              ariaLabel="Lọc theo loại yêu cầu"
              options={typeOptions}
            />
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={loading}
          onClick={() => void loadApprovals()}
        >
          <RefreshCw className="size-3.5" aria-hidden="true" />
          <span>{loading ? "Đang tải..." : "Làm mới"}</span>
        </Button>
      </div>

      {loading ? (
        <TableLoadingSkeleton rows={4} label="Đang tải hàng đợi phê duyệt 2 người..." />
      ) : errorMsg ? (
        <ErrorState
          title="Không thể tải hàng đợi duyệt 2 người"
          description={errorMsg}
          onRetry={() => void loadApprovals()}
        />
      ) : (
        <div className="space-y-4">
          {showShopSection ? (
            <Card aria-labelledby="approvals-shop-heading">
              <CardHeader>
                <CardTitle id="approvals-shop-heading">
                  Yêu cầu tạm khóa gian hàng ({formatInt(shopApprovals.length)})
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {shopApprovals.length === 0 ? (
                  <p className="rounded-lg border border-border bg-muted/35 p-4 text-center text-[13px] text-muted-foreground">
                    Không có yêu cầu khóa gian hàng nào khớp bộ lọc.
                  </p>
                ) : (
                  shopApprovals.map((item) => {
                    const st = getShopApprovalStatusMeta(item.status);
                    const isPending = item.status.toUpperCase() === "PENDING";
                    const isSelfMaker = Boolean(actorId && item.requested_by === actorId);
                    const canDecide =
                      isPending &&
                      canDecideTwoPersonApproval({
                        actorId,
                        actorRole,
                        requestedBy: item.requested_by,
                        decideCapability: "shops.suspend.decide",
                      });
                    const isBusy = busyId === item.id;
                    const rowErr = actionErrorById[item.id];

                    return (
                      <div
                        key={item.id}
                        className="space-y-3 rounded-xl border border-border bg-card p-4"
                      >
                        <div className="flex flex-wrap items-start justify-between gap-2">
                          <div className="min-w-0 space-y-0.5">
                            <h3 className="text-sm font-semibold text-foreground">
                              {item.shop_name || item.shop_id}
                            </h3>
                            <p className="text-xs text-muted-foreground tabular-nums">
                              Mã <span title={item.id}>#{item.id.slice(0, 8)}</span> · Người lập:{" "}
                              <span title={item.requested_by}>
                                {formatApprovalActorDisplay(
                                  item.requested_by,
                                  item.requested_role,
                                  userById
                                )}
                              </span>
                              {item.created_at ? ` · ${formatDateTimeVi(item.created_at)}` : ""}
                            </p>
                          </div>
                          <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                        </div>

                        <DetailGrid>
                          <DetailItem label="Lý do đề xuất tạm khóa" wide>
                            {item.reason || EMPTY_DASH}
                          </DetailItem>
                          {item.decided_by ? (
                            <DetailItem label="Người kết luận">
                              <span title={item.decided_by}>
                                {formatApprovalActorDisplay(item.decided_by, undefined, userById)}
                                {item.decided_at ? ` · ${formatDateTimeVi(item.decided_at)}` : ""}
                              </span>
                            </DetailItem>
                          ) : null}
                          {item.decided_note ? (
                            <DetailItem label="Ghi chú kết luận">{item.decided_note}</DetailItem>
                          ) : null}
                        </DetailGrid>

                        {isPending && isSelfMaker ? (
                          <Alert>
                            <Info className="size-4" aria-hidden="true" />
                            <AlertDescription>
                              Người lập yêu cầu không thể tự phê duyệt.
                            </AlertDescription>
                          </Alert>
                        ) : null}

                        {rowErr ? <FieldError>{rowErr}</FieldError> : null}

                        {canDecide ? (
                          <div className="space-y-3 border-t border-border pt-3">
                            <Field className="gap-1.5">
                              <FieldLabel htmlFor={`shop-app-note-${item.id}`}>
                                Ghi chú kết luận
                              </FieldLabel>
                              <Input
                                id={`shop-app-note-${item.id}`}
                                type="text"
                                value={noteById[item.id] ?? ""}
                                onChange={(e) =>
                                  setNoteById((prev) => ({ ...prev, [item.id]: e.target.value }))
                                }
                                placeholder="Bắt buộc nhập lý do khi từ chối..."
                                className="bg-card"
                              />
                            </Field>
                            <div className="flex flex-wrap items-center gap-2">
                              <Button
                                type="button"
                                variant="destructive"
                                size="sm"
                                disabled={isBusy}
                                onClick={() => void handleDecideShop(item, "APPROVE")}
                              >
                                {isBusy ? "Đang xử lý..." : "Duyệt khóa gian hàng"}
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={isBusy}
                                onClick={() => void handleDecideShop(item, "REJECT")}
                              >
                                Từ chối yêu cầu
                              </Button>
                            </div>
                          </div>
                        ) : null}
                      </div>
                    );
                  })
                )}
              </CardContent>
            </Card>
          ) : null}

          {showRefundSection ? (
            <Card aria-labelledby="approvals-refund-heading">
              <CardHeader>
                <CardTitle id="approvals-refund-heading">
                  Yêu cầu hoàn tiền giá trị lớn ({formatInt(refundApprovals.length)})
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {statusFilter === "APPROVED" ? (
                  <Alert>
                    <Info className="size-4" aria-hidden="true" />
                    <AlertDescription>
                      Lệnh hoàn tiền đã phê duyệt chuyển sang mục{" "}
                      <Link
                        href="/admin/doi-soat"
                        className="font-medium text-brand underline underline-offset-4"
                      >
                        Đối soát &amp; sổ cái
                      </Link>{" "}
                      để tất toán.
                    </AlertDescription>
                  </Alert>
                ) : refundApprovals.length === 0 ? (
                  <p className="rounded-lg border border-border bg-muted/35 p-4 text-center text-[13px] text-muted-foreground">
                    Không có yêu cầu hoàn tiền lớn nào khớp bộ lọc.
                  </p>
                ) : (
                  refundApprovals.map((rf) => {
                    const st = getRefundApprovalStatusMeta(rf.status);
                    const isPending = rf.status.toUpperCase() === "PENDING_APPROVAL";
                    const isSelfMaker = Boolean(actorId && rf.requested_by === actorId);
                    const canDecide =
                      isPending &&
                      canDecideTwoPersonApproval({
                        actorId,
                        actorRole,
                        requestedBy: rf.requested_by,
                        decideCapability: "refunds.approve",
                      });
                    const isBusy = busyId === rf.refund_id;
                    const rowErr = actionErrorById[rf.refund_id];

                    return (
                      <div
                        key={rf.refund_id}
                        className="space-y-3 rounded-xl border border-border bg-card p-4"
                      >
                        <div className="flex flex-wrap items-start justify-between gap-2">
                          <div className="min-w-0 space-y-0.5">
                            <h3 className="text-sm font-semibold text-foreground tabular-nums">
                              Hoàn tiền {formatVnd(rf.amount_vnd)} · Đơn {rf.order_id}
                            </h3>
                            <p className="text-xs text-muted-foreground tabular-nums">
                              Mã hoàn <span title={rf.refund_id}>#{rf.refund_id.slice(0, 8)}</span>{" "}
                              · Giao dịch{" "}
                              <span title={rf.payment_id}>#{rf.payment_id.slice(0, 8)}</span>
                              {rf.requested_by ? (
                                <>
                                  {" "}
                                  · Người lập:{" "}
                                  <span title={rf.requested_by}>
                                    {formatApprovalActorDisplay(
                                      rf.requested_by,
                                      undefined,
                                      userById
                                    )}
                                  </span>
                                </>
                              ) : null}
                              {rf.created_at ? ` · ${formatDateTimeVi(rf.created_at)}` : ""}
                            </p>
                          </div>
                          <StatusBadge tone={st.tone}>{st.label}</StatusBadge>
                        </div>

                        <DetailGrid>
                          <DetailItem label="Lý do hoàn tiền" wide>
                            {localizeRefundReason(rf.reason) || EMPTY_DASH}
                          </DetailItem>
                          {rf.approved_by ? (
                            <DetailItem label="Người kết luận">
                              <span title={rf.approved_by}>
                                {formatApprovalActorDisplay(rf.approved_by, undefined, userById)}
                                {rf.approved_at ? ` · ${formatDateTimeVi(rf.approved_at)}` : ""}
                              </span>
                            </DetailItem>
                          ) : null}
                          {rf.approval_note ? (
                            <DetailItem label="Ghi chú phê duyệt">{rf.approval_note}</DetailItem>
                          ) : null}
                        </DetailGrid>

                        {isPending && isSelfMaker ? (
                          <Alert>
                            <Info className="size-4" aria-hidden="true" />
                            <AlertDescription>
                              Người lập lệnh hoàn tiền không thể tự phê duyệt.
                            </AlertDescription>
                          </Alert>
                        ) : null}

                        {rowErr ? <FieldError>{rowErr}</FieldError> : null}

                        {canDecide ? (
                          <div className="space-y-3 border-t border-border pt-3">
                            <Field className="gap-1.5">
                              <FieldLabel htmlFor={`refund-app-note-${rf.refund_id}`}>
                                Ghi chú kết luận
                              </FieldLabel>
                              <Input
                                id={`refund-app-note-${rf.refund_id}`}
                                type="text"
                                value={noteById[rf.refund_id] ?? ""}
                                onChange={(e) =>
                                  setNoteById((prev) => ({
                                    ...prev,
                                    [rf.refund_id]: e.target.value,
                                  }))
                                }
                                placeholder="Bắt buộc nhập lý do khi từ chối..."
                                className="bg-card"
                              />
                            </Field>
                            <div className="flex flex-wrap items-center gap-2">
                              <Button
                                type="button"
                                size="sm"
                                disabled={isBusy}
                                onClick={() => void handleDecideRefund(rf, true)}
                              >
                                {isBusy ? "Đang xử lý..." : "Phê duyệt hoàn tiền"}
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={isBusy}
                                onClick={() => void handleDecideRefund(rf, false)}
                              >
                                Từ chối hoàn tiền
                              </Button>
                            </div>
                          </div>
                        ) : null}
                      </div>
                    );
                  })
                )}
              </CardContent>
            </Card>
          ) : null}

          {!showShopSection && !showRefundSection ? (
            <EmptyState
              icon={ShieldCheck}
              title="Không có danh mục phê duyệt nào khả dụng"
              description="Vai trò hiện tại không có quyền xem mục phê duyệt 2 người này."
            />
          ) : null}
        </div>
      )}
    </div>
  );
}
