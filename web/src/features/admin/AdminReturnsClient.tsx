"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Flag,
  Info,
  RefreshCw,
  Search,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  Money,
  PageHeader,
  StatusBadge,
  Timeline,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type {
  GatewayReturnRequest,
  PaginatedResponse,
  ReturnAdminDecision,
  ReturnFeeBearer,
} from "@/lib/gateway-types";
import { cn } from "@/lib/utils";
import {
  buildShippingLabelHref,
  getTimelineEventTitle,
  isAddressUpdatedTimelineNote,
  localizeTimelineNote,
  parseSafeEvidenceUrl,
} from "@/features/account/orderMeta";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { getAdminReturnStatusMeta } from "./types";
import {
  canAdminResolveReturn,
  getReturnDecisionLabelVi,
  getReturnFeeBearerLabelVi,
  validateReturnResolveInput,
} from "./AdminReturnsHelpers";
import {
  AdminDetailField,
  AdminDetailGrid,
  AdminKbdHint,
  AdminPaginationBar,
  hasOpenRadixDialog,
  isEditableTarget,
} from "./ops/AdminOpsPrimitives";
import { adminReturnStatusTone } from "./ops/adminTones";

type ReturnFilterTab =
  | ""
  | "ESCALATED"
  | "EVIDENCE_REQUESTED"
  | "PENDING"
  | "AWAITING_SHIPMENT"
  | "RECEIVED"
  | "REFUNDING"
  | "REFUNDED"
  | "CLOSED";

const PAGE_SIZE = 20;

const DECISION_OPTIONS: Array<{
  value: ReturnAdminDecision;
  title: string;
  hint: string;
}> = [
  {
    value: "REFUND",
    title: "Hoàn tiền ngay",
    hint: "Tạo lệnh hoàn tiền trực tiếp, không yêu cầu gửi trả hàng.",
  },
  {
    value: "RETURN_AND_REFUND",
    title: "Trả hàng và hoàn tiền",
    hint: "Tạo vận đơn hoàn về gian hàng, hoàn tiền sau khi nhận hàng.",
  },
  {
    value: "REQUEST_EVIDENCE",
    title: "Yêu cầu bổ sung bằng chứng",
    hint: "Gia hạn 3 ngày để người mua gửi thêm chứng từ xác minh.",
  },
  {
    value: "REJECT",
    title: "Từ chối khiếu nại",
    hint: "Đóng khiếu nại và chuyển đơn về trạng thái Hoàn tất.",
  },
];

const FEE_BEARER_OPTIONS: Array<{ value: ReturnFeeBearer; label: string }> = [
  { value: "SELLER", label: "Gian hàng chịu phí hoàn hàng" },
  { value: "BUYER", label: "Người mua chịu phí hoàn hàng" },
  { value: "PLATFORM", label: "Sàn hỗ trợ phí hoàn hàng" },
];

export interface AdminReturnsClientProps {
  initialItems: GatewayReturnRequest[];
  initialTotal: number;
  initialEscalatedCount: number;
  initialEvidenceCount: number;
  loadError: string | null;
}

function ReturnsWorkspace({
  initialItems,
  initialTotal,
  initialEscalatedCount,
  initialEvidenceCount,
  loadError: initialLoadError,
  initialQuery,
  initialSelected,
}: AdminReturnsClientProps & {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canResolve = roleHasCapability(user.role, "returns.resolve");
  const canOpenRefunds = roleHasCapability(user.role, "refunds.read");

  const [items, setItems] = useState<GatewayReturnRequest[]>(initialItems);
  const [total, setTotal] = useState(initialTotal);
  const [escalatedCount, setEscalatedCount] = useState(initialEscalatedCount);
  const [evidenceCount, setEvidenceCount] = useState(initialEvidenceCount);
  const [statusTab, setStatusTab] = useState<ReturnFilterTab>("");
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(initialLoadError);

  const [query, setQuery] = useState(initialQuery);
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [detailById, setDetailById] = useState<Record<string, GatewayReturnRequest>>({});
  const [loadingDetailId, setLoadingDetailId] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(
    Boolean(initialSelected && initialItems.some((r) => r.id === initialSelected))
  );

  const [decision, setDecision] = useState<ReturnAdminDecision>("REFUND");
  const [feeBearer, setFeeBearer] = useState<ReturnFeeBearer>("SELLER");
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const listAbortRef = useRef<AbortController | null>(null);
  const detailAbortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      listAbortRef.current?.abort();
      detailAbortRef.current?.abort();
    };
  }, []);

  const loadList = useCallback(async (nextStatus: ReturnFilterTab, nextPage: number) => {
    listAbortRef.current?.abort();
    const controller = new AbortController();
    listAbortRef.current = controller;

    setLoading(true);
    setLoadError(null);
    try {
      const qs = new URLSearchParams({
        page: String(nextPage),
        page_size: String(PAGE_SIZE),
      });
      if (nextStatus) qs.set("status", nextStatus);
      const res = await api<PaginatedResponse<GatewayReturnRequest>>(
        `/api/admin/returns?${qs.toString()}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted) return;
      const list = Array.isArray(res.items) ? res.items : [];
      setItems(list);
      setTotal(res.total ?? list.length);
      if (nextStatus === "ESCALATED") setEscalatedCount(res.total ?? list.length);
      if (nextStatus === "EVIDENCE_REQUESTED") setEvidenceCount(res.total ?? list.length);
      setSelectedId((prev) => (prev && list.some((r) => r.id === prev) ? prev : null));
    } catch (err) {
      if (controller.signal.aborted) return;
      setLoadError(getErrorMessage(err, "Không thể tải danh sách yêu cầu trả hàng lúc này."));
    } finally {
      if (!controller.signal.aborted) {
        setLoading(false);
      }
    }
  }, []);

  const fetchDetail = useCallback(async (returnId: string) => {
    detailAbortRef.current?.abort();
    const controller = new AbortController();
    detailAbortRef.current = controller;

    setLoadingDetailId(returnId);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/api/admin/returns/${encodeURIComponent(returnId)}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted || !res?.return_request) return;
      setDetailById((prev) => ({ ...prev, [returnId]: res.return_request }));
      setItems((prev) =>
        prev.map((it) => (it.id === returnId ? { ...it, ...res.return_request } : it))
      );
    } catch {
      // Keep list-level snapshot if detail refresh fails.
    } finally {
      if (!controller.signal.aborted) {
        setLoadingDetailId((cur) => (cur === returnId ? null : cur));
      }
    }
  }, []);

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (r) =>
        r.id.toLowerCase().includes(q) ||
        (r.order_code ?? "").toLowerCase().includes(q) ||
        r.order_id.toLowerCase().includes(q) ||
        r.buyer_id.toLowerCase().includes(q) ||
        r.shop_id.toLowerCase().includes(q) ||
        (r.reason ?? "").toLowerCase().includes(q) ||
        (r.dispute_reason ?? "").toLowerCase().includes(q) ||
        (r.return_tracking_code ?? "").toLowerCase().includes(q)
    );
  }, [items, query]);

  const activeIndex = useMemo(() => {
    if (!selectedId) return -1;
    return filteredItems.findIndex((r) => r.id === selectedId);
  }, [filteredItems, selectedId]);

  const activeListItem = useMemo(() => {
    if (!selectedId) return null;
    return (
      filteredItems.find((r) => r.id === selectedId) ??
      items.find((r) => r.id === selectedId) ??
      null
    );
  }, [filteredItems, items, selectedId]);

  const activeReturn = activeListItem ? (detailById[activeListItem.id] ?? activeListItem) : null;

  useEffect(() => {
    if (!drawerOpen || !activeListItem) {
      if (!activeListItem && drawerOpen) setDrawerOpen(false);
      return;
    }
    if (!detailById[activeListItem.id]) {
      void fetchDetail(activeListItem.id);
    }
  }, [activeListItem, detailById, drawerOpen, fetchDetail]);

  useEffect(() => {
    setFormError(null);
    setNote("");
    setDecision("REFUND");
    setFeeBearer("SELLER");
  }, [activeReturn?.id]);

  const openReturnSheet = useCallback((item: GatewayReturnRequest) => {
    setSelectedId(item.id);
    setDrawerOpen(true);
  }, []);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (filteredItems.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filteredItems.length - 1, cur + delta));
      const nextItem = filteredItems[nextIdx];
      if (!nextItem) return;
      setSelectedId(nextItem.id);
    },
    [filteredItems, activeIndex]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        isEditableTarget(e.target) ||
        hasOpenRadixDialog()
      ) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
        setDrawerOpen(true);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
        setDrawerOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [stepSelection]);

  const handleTabChange = (nextTab: ReturnFilterTab) => {
    setStatusTab(nextTab);
    setPage(1);
    void loadList(nextTab, 1);
  };

  const handlePageChange = (nextPage: number) => {
    setPage(nextPage);
    void loadList(statusTab, nextPage);
  };

  const handleResolveSubmit = async (e: React.FormEvent, target: GatewayReturnRequest) => {
    e.preventDefault();
    if (submitting || !canResolve) return;

    const validated = validateReturnResolveInput(
      {
        decision,
        note,
        return_fee_bearer:
          decision === "REFUND" || decision === "RETURN_AND_REFUND" ? feeBearer : "",
      },
      { goodsReceived: Boolean(target.goods_received) }
    );
    if (!validated.ok) {
      setFormError(validated.message);
      return;
    }

    setFormError(null);
    setSubmitting(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/api/admin/returns/${encodeURIComponent(target.id)}/resolve`,
        {
          method: "POST",
          body: validated.value,
        }
      );
      const updated = res.return_request;
      setDetailById((prev) => ({ ...prev, [target.id]: updated }));
      setItems((prev) => prev.map((it) => (it.id === target.id ? updated : it)));
      setNote("");
      showToast(
        `Đã phân xử đơn ${target.order_code || target.order_id.slice(0, 8)}: ${getReturnDecisionLabelVi(validated.value.decision)}.`
      );
      router.refresh();
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể gửi quyết định phân xử lúc này.");
      setFormError(msg);
      showToast(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const renderEvidenceLinks = (label: string, urls: string[] | undefined) => {
    const safeLinks = (urls ?? [])
      .map((raw) => parseSafeEvidenceUrl(raw))
      .filter((item): item is NonNullable<typeof item> => item !== null);
    return (
      <div className="space-y-1.5">
        <div className="text-xs font-medium text-muted-foreground">
          {label} ({formatInt(safeLinks.length)})
        </div>
        {safeLinks.length === 0 ? (
          <p className="text-xs text-muted-foreground">Chưa đính kèm bằng chứng.</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {safeLinks.map((link, idx) => (
              <li key={`${link.href}-${idx}`}>
                <a
                  href={link.href}
                  target="_blank"
                  rel="noopener noreferrer nofollow"
                  className="inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-2.5 py-1 text-xs font-medium text-brand hover:bg-accent"
                >
                  <span>Bằng chứng #{idx + 1}</span>
                  <span className="text-muted-foreground">({link.hostname})</span>
                  <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  };

  const renderInspector = (ret: GatewayReturnRequest) => {
    const resolvable = canAdminResolveReturn(ret.status);
    const labelHref = buildShippingLabelHref(ret.return_tracking_code, ret.order_id);
    const isRefundingStage =
      ret.status.toUpperCase() === "REFUNDING" || ret.status.toUpperCase() === "REFUNDED";
    const noteId = `ret-note-drawer-${ret.id}`;
    const feeId = `ret-fee-drawer-${ret.id}`;

    const timelineItems = (ret.events ?? []).map((ev) => {
      const evMeta = getAdminReturnStatusMeta(ev.to_status);
      const title = isAddressUpdatedTimelineNote(ev.note)
        ? getTimelineEventTitle({
            from_status: ev.from_status,
            to_status: ev.to_status,
            note: ev.note,
          })
        : evMeta.label;
      return {
        id: ev.id,
        at: ev.created_at ? formatShortTimeDateVi(ev.created_at) : EMPTY_DASH,
        title,
        description: ev.note ? localizeTimelineNote(ev.note) : undefined,
      };
    });

    return (
      <div key={ret.id} className="space-y-5 px-4 pb-6">
        <AdminDetailGrid>
          <AdminDetailField label="Số tiền hoàn dự kiến" mono>
            {ret.refund_amount_vnd > 0 ? (
              <Money vnd={ret.refund_amount_vnd} size="sm" />
            ) : (
              EMPTY_DASH
            )}
          </AdminDetailField>
          <AdminDetailField label="Phí hoàn hàng">
            {ret.return_fee_vnd > 0 ? `${formatVnd(ret.return_fee_vnd)} · ` : ""}
            {getReturnFeeBearerLabelVi(ret.return_fee_bearer)}
          </AdminDetailField>
          <AdminDetailField label="Người mua" mono>
            {ret.buyer_id.slice(0, 12)}
          </AdminDetailField>
          <AdminDetailField label="Gian hàng" mono>
            {ret.shop_id.slice(0, 12)}
          </AdminDetailField>
          <AdminDetailField label="Tình trạng hàng trả">
            {ret.goods_received ? "Gian hàng đã nhận hàng trả" : "Chưa nhận hàng trả"}
          </AdminDetailField>
          <AdminDetailField label="Hạn xử lý" mono>
            {ret.deadline_at ? formatDateTimeVi(ret.deadline_at) : EMPTY_DASH}
          </AdminDetailField>
          {ret.return_tracking_code && (
            <AdminDetailField label="Vận đơn hoàn hàng" mono wide>
              <span>{ret.return_tracking_code}</span>
              {labelHref && (
                <>
                  {" · "}
                  <a
                    href={labelHref}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-brand hover:underline"
                    title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
                  >
                    In nhãn vận đơn (Ctrl+P)
                  </a>
                </>
              )}
            </AdminDetailField>
          )}
        </AdminDetailGrid>

        {/* Statements */}
        <div className="space-y-2.5">
          <div className="rounded-lg border border-border bg-muted/30 p-3">
            <div className="text-xs font-medium text-muted-foreground">
              Lý do người mua yêu cầu trả hàng
            </div>
            <p className="mt-1 text-[13px] text-foreground [overflow-wrap:anywhere]">
              {ret.reason || "Chưa có mô tả chi tiết."}
            </p>
          </div>
          {ret.seller_response && (
            <div className="rounded-lg border border-border bg-muted/30 p-3">
              <div className="text-xs font-medium text-muted-foreground">Phản hồi từ gian hàng</div>
              <p className="mt-1 text-[13px] text-foreground [overflow-wrap:anywhere]">
                {ret.seller_response}
              </p>
            </div>
          )}
          {ret.dispute_reason && (
            <div className="rounded-lg border border-border bg-badge-critical-bg/30 p-3">
              <div className="text-xs font-medium text-badge-critical-ink">
                Nội dung khiếu nại lên sàn
              </div>
              <p className="mt-1 text-[13px] text-foreground [overflow-wrap:anywhere]">
                {ret.dispute_reason}
              </p>
            </div>
          )}
          {ret.admin_note && (
            <div className="rounded-lg border border-border bg-muted/30 p-3">
              <div className="text-xs font-medium text-muted-foreground">Ghi chú quản trị viên</div>
              <p className="mt-1 text-[13px] text-foreground [overflow-wrap:anywhere]">
                {ret.admin_note}
              </p>
            </div>
          )}
        </div>

        {/* Evidence */}
        <div className="space-y-3">
          {renderEvidenceLinks("Bằng chứng người mua", ret.evidence_urls)}
          {renderEvidenceLinks("Bằng chứng gian hàng", ret.seller_evidence_urls)}
        </div>

        {isRefundingStage && (
          <Alert>
            <Info className="size-4 text-brand" aria-hidden="true" />
            <AlertDescription className="space-y-1.5">
              <p>
                Các lệnh hoàn tiền giá trị lớn tự động chuyển sang quy trình duyệt 2 người
                (maker–checker) trước khi tất toán.
              </p>
              {canOpenRefunds && (
                <Link
                  href="/admin/doi-soat?tab=refunds"
                  className="inline-block font-medium text-brand hover:underline"
                >
                  Theo dõi &amp; duyệt lệnh hoàn tiền tại Đối soát →
                </Link>
              )}
            </AlertDescription>
          </Alert>
        )}

        {loadingDetailId === ret.id && (
          <p className="text-xs text-muted-foreground">Đang tải chi tiết lịch sử xử lý...</p>
        )}

        {timelineItems.length > 0 && (
          <>
            <Separator />
            <div className="space-y-2.5">
              <h3 className="text-sm font-semibold text-foreground">Diễn biến xử lý</h3>
              <Timeline items={timelineItems} />
            </div>
          </>
        )}

        {resolvable && canResolve && (
          <>
            <Separator />
            <form
              className="space-y-4 rounded-lg border border-border bg-card p-4"
              onSubmit={(e) => void handleResolveSubmit(e, ret)}
              noValidate
            >
              <h3 className="text-sm font-semibold text-foreground">Quyết định phân xử</h3>

              <RadioGroup
                value={decision}
                onValueChange={(val) => {
                  setDecision(val as ReturnAdminDecision);
                  setFormError(null);
                }}
                aria-label="Chọn quyết định phân xử trả hàng"
                className="space-y-2"
              >
                {DECISION_OPTIONS.map((opt) => {
                  const disabled = opt.value === "RETURN_AND_REFUND" && Boolean(ret.goods_received);
                  const checked = decision === opt.value;
                  const optId = `ret-decision-${ret.id}-${opt.value}`;
                  return (
                    <Label
                      key={opt.value}
                      htmlFor={optId}
                      className={cn(
                        "flex cursor-pointer items-start gap-3 rounded-lg border border-border p-3 font-normal transition-colors",
                        checked && "border-brand bg-brand/5",
                        disabled && "cursor-not-allowed opacity-55"
                      )}
                    >
                      <RadioGroupItem
                        id={optId}
                        value={opt.value}
                        disabled={disabled || submitting}
                        className="mt-0.5 shrink-0"
                      />
                      <span className="min-w-0 flex-1 space-y-0.5">
                        <span className="block text-[13px] font-medium text-foreground">
                          {opt.title}
                        </span>
                        <span className="block text-xs text-muted-foreground">
                          {disabled
                            ? "Gian hàng đã nhận hàng trả nên không tạo lại vận đơn."
                            : opt.hint}
                        </span>
                      </span>
                    </Label>
                  );
                })}
              </RadioGroup>

              {(decision === "REFUND" || decision === "RETURN_AND_REFUND") && (
                <Field>
                  <FieldLabel htmlFor={feeId}>Bên chịu phí hoàn hàng</FieldLabel>
                  <Select
                    value={feeBearer}
                    disabled={submitting}
                    onValueChange={(val) => setFeeBearer(val as ReturnFeeBearer)}
                  >
                    <SelectTrigger id={feeId} className="w-full bg-card">
                      <SelectValue>
                        {FEE_BEARER_OPTIONS.find((opt) => opt.value === feeBearer)?.label}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {FEE_BEARER_OPTIONS.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value}>
                          {opt.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FieldDescription>
                    Lệnh hoàn tiền vượt ngưỡng tự động chuyển duyệt 2 người tại màn hình Đối soát.
                  </FieldDescription>
                </Field>
              )}

              <Field>
                <FieldLabel htmlFor={noteId}>
                  {decision === "REQUEST_EVIDENCE"
                    ? "Nội dung yêu cầu bổ sung bằng chứng (bắt buộc)"
                    : decision === "REJECT"
                      ? "Lý do từ chối khiếu nại (bắt buộc)"
                      : "Ghi chú phân xử (lưu nhật ký kiểm toán)"}
                </FieldLabel>
                <Textarea
                  id={noteId}
                  rows={3}
                  value={note}
                  disabled={submitting}
                  onChange={(e) => {
                    setNote(e.target.value);
                    setFormError(null);
                  }}
                  placeholder={
                    decision === "REQUEST_EVIDENCE"
                      ? "Nêu rõ ảnh/video cần người mua bổ sung trong 3 ngày..."
                      : "Ghi rõ căn cứ bằng chứng và hướng xử lý..."
                  }
                />
              </Field>

              {formError && <FieldError>{formError}</FieldError>}

              <div className="flex items-center justify-end gap-2">
                <Button type="submit" size="sm" disabled={submitting}>
                  {submitting ? "Đang gửi..." : "Xác nhận phân xử"}
                </Button>
              </div>
            </form>
          </>
        )}

        {resolvable && !canResolve && (
          <p className="text-xs text-muted-foreground">
            Tài khoản của bạn chỉ có quyền xem hồ sơ khiếu nại.
          </p>
        )}
      </div>
    );
  };

  const tabs = [
    { key: "", label: "Tất cả", count: statusTab === "" ? total : undefined },
    { key: "ESCALATED", label: "Cần phân xử", count: escalatedCount },
    { key: "EVIDENCE_REQUESTED", label: "Chờ bằng chứng", count: evidenceCount },
    { key: "PENDING", label: "Chờ shop" },
    { key: "AWAITING_SHIPMENT", label: "Chờ gửi trả" },
    { key: "RECEIVED", label: "Shop đã nhận" },
    { key: "REFUNDING", label: "Đang hoàn tiền" },
    { key: "REFUNDED", label: "Đã hoàn tiền" },
    { key: "CLOSED", label: "Đã đóng" },
  ];

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="space-y-5">
      <PageHeader
        title="Khiếu nại & trả hàng"
        subtitle="Xem chứng từ người mua và gian hàng, phân xử hoàn tiền hoặc trả hàng."
        primaryAction={
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={loading}
            onClick={() => void loadList(statusTab, page)}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} aria-hidden="true" />
            <span>{loading ? "Đang tải..." : "Làm mới"}</span>
          </Button>
        }
      />

      <MetricStrip
        items={[
          { label: "Hồ sơ trong bộ lọc", value: formatInt(total) },
          {
            label: "Cần phân xử",
            value: (
              <span className={escalatedCount > 0 ? "text-destructive" : undefined}>
                {formatInt(escalatedCount)}
              </span>
            ),
          },
          { label: "Chờ bằng chứng", value: formatInt(evidenceCount) },
        ]}
      />

      <IndexTable
        tabs={tabs}
        activeTab={statusTab}
        onTabChange={(next) => handleTabChange(next as ReturnFilterTab)}
        toolbar={
          <div className="flex items-center gap-3">
            <AdminKbdHint label="chuyển hồ sơ" />
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatInt(total)} hồ sơ
            </span>
          </div>
        }
        footer={
          !loadError && !loading && filteredItems.length > 0 ? (
            <AdminPaginationBar
              page={page}
              totalPages={totalPages}
              totalItems={total}
              itemLabel="hồ sơ"
              disabled={loading}
              onPageChange={handlePageChange}
            />
          ) : undefined
        }
      >
        <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
          <div className="relative min-w-0 flex-1 sm:max-w-md">
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm mã đơn, mã yêu cầu, vận đơn, lý do..."
              aria-label="Tìm kiếm hồ sơ khiếu nại và trả hàng"
              className="h-9 bg-card pl-8"
            />
          </div>
        </div>

        {loadError ? (
          <div className="p-4">
            <ErrorState
              title="Không thể tải danh sách khiếu nại"
              description={loadError}
              onRetry={() => void loadList(statusTab, page)}
              className="shadow-none"
            />
          </div>
        ) : loading ? (
          <div className="space-y-2.5 p-5" role="status" aria-live="polite">
            <p className="text-xs text-muted-foreground">Đang tải danh sách khiếu nại...</p>
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full rounded-md" />
            ))}
          </div>
        ) : filteredItems.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Flag}
              title="Không có yêu cầu trả hàng hoặc khiếu nại phù hợp"
              description="Thử chuyển sang trạng thái khác hoặc xóa từ khóa tìm kiếm."
              className="shadow-none"
            />
          </div>
        ) : (
          <>
            <div className="hidden md:block">
              <Table aria-label="Danh sách khiếu nại trả hàng">
                <TableHeader>
                  <TableRow>
                    <TableHead>Đơn hàng</TableHead>
                    <TableHead>Trạng thái</TableHead>
                    <TableHead className="hidden xl:table-cell">Lý do / Khiếu nại</TableHead>
                    <TableHead className="hidden text-right lg:table-cell">Hoàn dự kiến</TableHead>
                    <TableHead className="hidden lg:table-cell">Tạo lúc</TableHead>
                    <TableHead className="text-right">Thao tác</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredItems.map((item) => {
                    const meta = getAdminReturnStatusMeta(item.status);
                    const isSelected = drawerOpen && activeReturn?.id === item.id;
                    return (
                      <TableRow
                        key={item.id}
                        data-state={isSelected ? "selected" : undefined}
                        className="cursor-pointer"
                        onClick={() => openReturnSheet(item)}
                      >
                        <TableCell>
                          <div className="font-medium text-foreground">
                            Đơn {item.order_code || item.order_id.slice(0, 8)}
                          </div>
                          <div className="text-xs text-muted-foreground tabular-nums">
                            {item.id.slice(0, 12)}
                            {item.refund_amount_vnd > 0 && (
                              <span className="lg:hidden">
                                {" · "}
                                {formatVnd(item.refund_amount_vnd)}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <StatusBadge tone={adminReturnStatusTone(item.status)}>
                            {meta.label}
                          </StatusBadge>
                        </TableCell>
                        <TableCell className="hidden max-w-[280px] whitespace-normal xl:table-cell">
                          <span className="line-clamp-2 text-[13px] text-foreground">
                            {item.dispute_reason || item.reason || "Chưa có lý do"}
                          </span>
                        </TableCell>
                        <TableCell className="hidden text-right lg:table-cell">
                          {item.refund_amount_vnd > 0 ? (
                            <Money vnd={item.refund_amount_vnd} />
                          ) : (
                            <span className="text-xs text-muted-foreground">{EMPTY_DASH}</span>
                          )}
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground tabular-nums lg:table-cell">
                          {item.created_at ? formatShortTimeDateVi(item.created_at) : EMPTY_DASH}
                        </TableCell>
                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() => openReturnSheet(item)}
                          >
                            {canAdminResolveReturn(item.status) && canResolve
                              ? "Phân xử"
                              : "Xem chi tiết"}
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>

            <div className="md:hidden">
              {filteredItems.map((item) => {
                const meta = getAdminReturnStatusMeta(item.status);
                return (
                  <IndexRowCard
                    key={item.id}
                    title={`Đơn ${item.order_code || item.order_id.slice(0, 8)}`}
                    badge={
                      <StatusBadge tone={adminReturnStatusTone(item.status)}>
                        {meta.label}
                      </StatusBadge>
                    }
                    meta={
                      <>
                        <span className="tabular-nums">
                          {item.refund_amount_vnd > 0
                            ? formatVnd(item.refund_amount_vnd)
                            : EMPTY_DASH}
                        </span>
                        {item.created_at && (
                          <>
                            <span>·</span>
                            <span className="tabular-nums">
                              {formatShortTimeDateVi(item.created_at)}
                            </span>
                          </>
                        )}
                        <span className="line-clamp-1 basis-full">
                          {item.dispute_reason || item.reason || "Chưa có lý do"}
                        </span>
                      </>
                    }
                    onClick={() => openReturnSheet(item)}
                  />
                );
              })}
            </div>
          </>
        )}
      </IndexTable>

      <Sheet open={drawerOpen && Boolean(activeReturn)} onOpenChange={setDrawerOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {activeReturn ? (
            <>
              <SheetHeader className="border-b border-border pr-12">
                <div className="flex flex-wrap items-center gap-2">
                  <SheetTitle>
                    Đơn {activeReturn.order_code || activeReturn.order_id.slice(0, 8)}
                  </SheetTitle>
                  <StatusBadge tone={adminReturnStatusTone(activeReturn.status)}>
                    {getAdminReturnStatusMeta(activeReturn.status).label}
                  </StatusBadge>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
                  <SheetDescription className="tabular-nums">
                    Mã yêu cầu {activeReturn.id.slice(0, 12)}
                    {activeReturn.created_at
                      ? ` · Tạo lúc ${formatDateTimeVi(activeReturn.created_at)}`
                      : ""}
                  </SheetDescription>
                  {activeIndex >= 0 && (
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex <= 0}
                        onClick={() => stepSelection(-1)}
                        aria-label="Hồ sơ trước (K)"
                      >
                        <ChevronLeft className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex >= filteredItems.length - 1}
                        onClick={() => stepSelection(1)}
                        aria-label="Hồ sơ tiếp theo (J)"
                      >
                        <ChevronRight className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  )}
                </div>
              </SheetHeader>
              {renderInspector(activeReturn)}
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}

export function AdminReturnsClient(props: AdminReturnsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại & trả hàng" />}>
      <AdminStateGate
        title="Khiếu nại & trả hàng"
        emptyTitle="Chưa có yêu cầu trả hàng hoặc khiếu nại"
        emptyMessage="Hồ sơ tranh chấp từ người mua sẽ xuất hiện tại đây."
        errorTitle="Không thể tải danh sách khiếu nại"
        errorMessage="Dịch vụ đơn hàng tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <ReturnsWorkspace
            {...props}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
