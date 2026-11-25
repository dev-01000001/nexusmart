"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import type {
  GatewayAdminApprovalsResponse,
  GatewayRefund,
  GatewayShop,
  GatewayShopSuspensionApproval,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { filterUnresolvedActorIds } from "@/lib/a8-types";
import { canDecideTwoPersonApproval, roleHasCapability } from "@/lib/admin-rbac";
import { localizeRefundReason } from "@/features/account/orderMeta";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import {
  type ApprovalActorLookupEntry,
  fetchShopOwnerById,
  formatApprovalActorDisplay,
} from "./types";
import { StatusPill } from "./StatusPill";
import { useAdminUser } from "./AdminUserContext";
import type { PillTone } from "./adminMeta";
import shared from "./adminShared.module.css";

type ApprovalTypeFilter = "" | "shop" | "refund";
type ApprovalStatusFilter = "PENDING" | "APPROVED" | "REJECTED";

function getShopApprovalStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "PENDING":
      return { label: "Chờ duyệt 2 người", tone: "warning" };
    case "APPROVED":
      return { label: "Đã phê duyệt", tone: "positive" };
    case "REJECTED":
      return { label: "Đã từ chối", tone: "negative" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

function getRefundApprovalStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "PENDING_APPROVAL":
      return { label: "Chờ duyệt 2 người", tone: "warning" };
    case "PENDING":
      return { label: "Đã duyệt (chờ chuyển tiền)", tone: "positive" };
    case "DONE":
      return { label: "Đã hoàn tất", tone: "positive" };
    case "REJECTED":
      return { label: "Đã từ chối", tone: "negative" };
    case "MANUAL_REVIEW":
      return { label: "Cần xử lý tay", tone: "warning" };
    case "FAILED":
      return { label: "Thất bại", tone: "negative" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

export function AdminApprovalsPanel() {
  const { showToast } = useMockStore();
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
      const res = await api<GatewayAdminApprovalsResponse>(`admin/approvals?${qs.toString()}`);
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

  // Resolve maker/checker UUIDs to display names via existing GET /admin/users?query=<id>
  // (permitted for all 6 internal staff roles via users.read). Falls back to 8-char short ID.
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
            api<PaginatedResponse<GatewayUser>>(path)
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
          // Leave id out of fetchedUserIdsRef so transient failures retry on refresh/re-run.
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
        `admin/approvals/shop/${encodeURIComponent(item.id)}/decide`,
        {
          method: "POST",
          json: { decision, note },
        }
      );
      showToast(
        decision === "APPROVE"
          ? "Đã phê duyệt tạm khoá gian hàng (quy tắc 2 người)."
          : "Đã từ chối yêu cầu khoá gian hàng."
      );
      await loadApprovals();
    } catch (err) {
      setActionErrorById((prev) => ({
        ...prev,
        [item.id]: getErrorMessage(err, "Không thể xử lý phê duyệt yêu cầu khoá gian hàng."),
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
        `admin/approvals/refund/${encodeURIComponent(rf.refund_id)}/decide`,
        {
          method: "POST",
          json: { approve, note },
        }
      );
      showToast(
        approve
          ? "Đã phê duyệt lệnh hoàn tiền lớn (quy tắc 2 người)."
          : "Đã từ chối lệnh hoàn tiền lớn."
      );
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

  return (
    <div className={shared.sectionStack}>
      <div className={shared.toolbar} style={{ marginTop: 0 }}>
        <div className={shared.toolbarLeft}>
          <select
            className={shared.toolbarSelect}
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as ApprovalStatusFilter)}
            aria-label="Lọc theo trạng thái phê duyệt"
          >
            <option value="PENDING">Đang chờ duyệt</option>
            <option value="APPROVED">Đã phê duyệt</option>
            <option value="REJECTED">Đã từ chối</option>
          </select>
          <select
            className={shared.toolbarSelect}
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value as ApprovalTypeFilter)}
            aria-label="Lọc theo loại yêu cầu"
          >
            <option value="">Tất cả loại yêu cầu</option>
            {canReadShopApprovals && <option value="shop">Khoá gian hàng</option>}
            {canReadRefunds && <option value="refund">Hoàn tiền giá trị lớn</option>}
          </select>
        </div>
        <div className={shared.toolbarRight}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={loading}
            onClick={() => void loadApprovals()}
          >
            {loading ? "Đang tải..." : "Làm mới"}
          </Button>
        </div>
      </div>

      {loading ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải hàng đợi chờ phê duyệt 2 người..."
            rows={4}
          />
        </div>
      ) : errorMsg ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải hàng đợi duyệt 2 người"
            message={errorMsg}
            actionLabel="Thử lại"
            onAction={() => void loadApprovals()}
          />
        </div>
      ) : (
        <>
          {showShopSection && (
            <section className={shared.panel} aria-labelledby="approvals-shop-heading">
              <div className={shared.panelHeader}>
                <div>
                  <h2 id="approvals-shop-heading" className={shared.panelTitle}>
                    Yêu cầu tạm khoá gian hàng ({formatInt(shopApprovals.length)})
                  </h2>
                  <span className={shared.panelMeta}>
                    Quy tắc 2 người: người lập yêu cầu không thể tự phê duyệt
                  </span>
                </div>
              </div>

              {shopApprovals.length === 0 ? (
                <p className={shared.emptyRow}>Không có yêu cầu khoá gian hàng nào khớp bộ lọc.</p>
              ) : (
                <div className={shared.sectionStack} style={{ marginTop: 8 }}>
                  {shopApprovals.map((item) => {
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
                      <div key={item.id} className={shared.detailWell}>
                        <div className={shared.detailHeader}>
                          <div>
                            <h3 className={shared.detailHeading}>
                              Gian hàng: {item.shop_name || item.shop_id}
                            </h3>
                            <p className={shared.cellSub}>
                              Mã yêu cầu: <span title={item.id}>#{item.id.slice(0, 8)}</span> ·
                              Người lập:{" "}
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
                          <StatusPill label={st.label} tone={st.tone} />
                        </div>

                        <div className={shared.detailGrid}>
                          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                            <span className={shared.detailKey}>Lý do đề xuất tạm khoá</span>
                            <span className={shared.detailValPlain}>{item.reason || "—"}</span>
                          </div>
                          {item.decided_by && (
                            <div className={shared.detailField}>
                              <span className={shared.detailKey}>Người kết luận</span>
                              <span className={shared.detailVal} title={item.decided_by}>
                                {formatApprovalActorDisplay(item.decided_by, undefined, userById)}
                                {item.decided_at ? ` · ${formatDateTimeVi(item.decided_at)}` : ""}
                              </span>
                            </div>
                          )}
                          {item.decided_note && (
                            <div className={shared.detailField}>
                              <span className={shared.detailKey}>Ghi chú kết luận</span>
                              <span className={shared.detailValPlain}>{item.decided_note}</span>
                            </div>
                          )}
                        </div>

                        {isPending && isSelfMaker && (
                          <p className={shared.savedStamp}>
                            Người lập yêu cầu không thể tự phê duyệt (quy tắc duyệt 2 người). Vui
                            lòng chờ nhân sự khác có thẩm quyền xác nhận.
                          </p>
                        )}

                        {rowErr && (
                          <p className={shared.validationError} role="alert">
                            {rowErr}
                          </p>
                        )}

                        {canDecide && (
                          <div className={shared.detailActionBar}>
                            <div className={shared.detailNoteGroup}>
                              <label
                                htmlFor={`shop-app-note-${item.id}`}
                                className={shared.fieldLabel}
                              >
                                Ghi chú phê duyệt / từ chối
                              </label>
                              <input
                                id={`shop-app-note-${item.id}`}
                                type="text"
                                className={shared.textInput}
                                value={noteById[item.id] ?? ""}
                                onChange={(e) =>
                                  setNoteById((prev) => ({ ...prev, [item.id]: e.target.value }))
                                }
                                placeholder="Nhập ghi chú kết luận (bắt buộc khi từ chối)..."
                              />
                            </div>
                            <div className={shared.detailButtons}>
                              <Button
                                variant="danger"
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleDecideShop(item, "APPROVE")}
                              >
                                {isBusy ? "Đang xử lý..." : "Duyệt khoá gian hàng"}
                              </Button>
                              <Button
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleDecideShop(item, "REJECT")}
                              >
                                Từ chối yêu cầu
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {showRefundSection && (
            <section className={shared.panel} aria-labelledby="approvals-refund-heading">
              <div className={shared.panelHeader}>
                <div>
                  <h2 id="approvals-refund-heading" className={shared.panelTitle}>
                    Yêu cầu hoàn tiền giá trị lớn ({formatInt(refundApprovals.length)})
                  </h2>
                  <span className={shared.panelMeta}>
                    Áp dụng duyệt 2 người cho lệnh hoàn đạt ngưỡng hoặc tổng cộng dồn 24 giờ
                  </span>
                </div>
              </div>

              {statusFilter === "APPROVED" ? (
                <p className={shared.emptyRow}>
                  Lệnh hoàn tiền lớn sau khi được phê duyệt sẽ tự động chuyển sang vòng đời tất toán
                  (chờ chuyển khoản / đã tất toán). Vui lòng theo dõi chi tiết tại mục{" "}
                  <Link href="/admin/doi-soat" className={shared.panelLink}>
                    Hoàn tiền &amp; Tất toán
                  </Link>
                  .
                </p>
              ) : refundApprovals.length === 0 ? (
                <p className={shared.emptyRow}>Không có yêu cầu hoàn tiền lớn nào khớp bộ lọc.</p>
              ) : (
                <div className={shared.sectionStack} style={{ marginTop: 8 }}>
                  {refundApprovals.map((rf) => {
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
                      <div key={rf.refund_id} className={shared.detailWell}>
                        <div className={shared.detailHeader}>
                          <div>
                            <h3 className={shared.detailHeading}>
                              Hoàn tiền {formatVnd(rf.amount_vnd)} · Đơn {rf.order_id}
                            </h3>
                            <p className={shared.cellSub}>
                              Mã hoàn: <span title={rf.refund_id}>#{rf.refund_id.slice(0, 8)}</span>{" "}
                              · Giao dịch:{" "}
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
                          <StatusPill label={st.label} tone={st.tone} />
                        </div>

                        <div className={shared.detailGrid}>
                          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                            <span className={shared.detailKey}>Lý do hoàn tiền</span>
                            <span className={shared.detailValPlain}>
                              {localizeRefundReason(rf.reason) || "—"}
                            </span>
                          </div>
                          {rf.approved_by && (
                            <div className={shared.detailField}>
                              <span className={shared.detailKey}>Người kết luận</span>
                              <span className={shared.detailVal} title={rf.approved_by}>
                                {formatApprovalActorDisplay(rf.approved_by, undefined, userById)}
                                {rf.approved_at ? ` · ${formatDateTimeVi(rf.approved_at)}` : ""}
                              </span>
                            </div>
                          )}
                          {rf.approval_note && (
                            <div className={shared.detailField}>
                              <span className={shared.detailKey}>Ghi chú phê duyệt</span>
                              <span className={shared.detailValPlain}>{rf.approval_note}</span>
                            </div>
                          )}
                        </div>

                        {isPending && isSelfMaker && (
                          <p className={shared.savedStamp}>
                            Người lập lệnh hoàn tiền không thể tự phê duyệt (quy tắc duyệt 2 người).
                            Vui lòng nhờ kế toán hoặc quản trị viên khác duyệt.
                          </p>
                        )}

                        {rowErr && (
                          <p className={shared.validationError} role="alert">
                            {rowErr}
                          </p>
                        )}

                        {canDecide && (
                          <div className={shared.detailActionBar}>
                            <div className={shared.detailNoteGroup}>
                              <label
                                htmlFor={`refund-app-note-${rf.refund_id}`}
                                className={shared.fieldLabel}
                              >
                                Ghi chú phê duyệt / từ chối
                              </label>
                              <input
                                id={`refund-app-note-${rf.refund_id}`}
                                type="text"
                                className={shared.textInput}
                                value={noteById[rf.refund_id] ?? ""}
                                onChange={(e) =>
                                  setNoteById((prev) => ({
                                    ...prev,
                                    [rf.refund_id]: e.target.value,
                                  }))
                                }
                                placeholder="Nhập ghi chú kết luận (bắt buộc khi từ chối)..."
                              />
                            </div>
                            <div className={shared.detailButtons}>
                              <Button
                                variant="primary"
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleDecideRefund(rf, true)}
                              >
                                {isBusy ? "Đang xử lý..." : "Phê duyệt hoàn tiền"}
                              </Button>
                              <Button
                                variant="outline"
                                size="sm"
                                shape="app"
                                disabled={isBusy}
                                onClick={() => void handleDecideRefund(rf, false)}
                              >
                                Từ chối hoàn tiền
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {!showShopSection && !showRefundSection && (
            <section className={shared.panel}>
              <p className={shared.emptyRow}>
                Vai trò hiện tại không có danh mục phê duyệt nào cần theo dõi.
              </p>
            </section>
          )}
        </>
      )}
    </div>
  );
}
