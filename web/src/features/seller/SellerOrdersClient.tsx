"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconCheck, IconRefresh, IconSearch, StateView } from "@/components";
import { api, apiBlob, getErrorMessage } from "@/lib/api";
import {
  GATEWAY_ORDER_STATUS_LABELS,
  formatCarrierLabel,
  formatDateTimeVi,
  formatInt,
  formatVnd,
} from "@/lib/format";
import {
  BATCH_POLL_MAX_OFF_PAGE_PER_TICK,
  BATCH_POLL_MAX_TICKS,
  MAX_BATCH_SHIP_ORDERS,
  SELLER_ORDERS_PAGE_SIZE,
  aggregateSellerOrderStatusCounts,
  buildSellerOrdersQuery,
  collectBatchTrackingCodes,
  resolveClampedPageOnEmpty,
  selectOffPageOrdersToPoll,
  toggleBatchSelection,
  translateBatchShipReasonCode,
  type BatchShipOrdersResponse,
  type PickingListResponse,
  type SellerOrderFilterKey,
  type SellerOrderStatusCount,
  type TrackedBatchOrderState,
} from "@/lib/a8-types";
import {
  getSalesChannelMeta,
  normalizeSalesChannel,
  parseSalesChannelFilter,
  type SalesChannelFilter,
} from "@/lib/salesChannel";
import { buildShippingLabelHref } from "@/features/account/orderMeta";
import {
  CARRIER_OPTIONS,
  buildCarrierSelectOptions,
  canSellerConfirmOrder,
  canSellerShipOrder,
  parseUiState,
  resolveOrderShippingDefaults,
  translateSellerShipError,
} from "./types";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { SellerOrderQuickDrawer } from "./SellerOrderQuickDrawer";
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/don-hang/page.module.css";

interface SelectedOrderMeta {
  order_code: string;
  tracking_code: string;
  status: string;
  shipment_job_status?: string;
}

export interface SellerOrdersClientProps {
  initialOrders: GwSellerOrder[];
  initialTotal: number;
  initialStatusCounts: SellerOrderStatusCount[];
  productImages: Record<string, string>;
  loadError: string | null;
}

function buildPaginationWindow(current: number, totalPages: number): number[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>([1, totalPages, current]);
  for (let delta = -1; delta <= 1; delta += 1) {
    const p = current + delta;
    if (p > 1 && p < totalPages) pages.add(p);
  }
  return Array.from(pages).sort((a, b) => a - b);
}

function mapStatusParamToFilter(rawStatus: string | null): SellerOrderFilterKey {
  if (rawStatus === "CONFIRMED" || rawStatus === "cho_xac_nhan") return "CONFIRMED";
  if (rawStatus === "PREPARING" || rawStatus === "dang_chuan_bi") return "PREPARING";
  if (rawStatus === "SHIPPING" || rawStatus === "dang_giao") return "SHIPPING";
  if (rawStatus === "DONE" || rawStatus === "DELIVERED" || rawStatus === "COMPLETED") return "DONE";
  if (rawStatus === "OTHER" || rawStatus === "CANCELLED" || rawStatus === "tra_huy") return "OTHER";
  return "all";
}

const CHANNEL_FILTER_OPTIONS: ReadonlyArray<{ value: SalesChannelFilter; label: string }> = [
  { value: "", label: "Mọi kênh bán" },
  { value: "MARKETPLACE", label: "Sàn NexusMart" },
  { value: "STOREFRONT", label: "Site riêng" },
];

export function SellerOrdersClient({
  initialOrders,
  initialTotal,
  initialStatusCounts,
  productImages,
  loadError,
}: SellerOrdersClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialFilter = mapStatusParamToFilter(searchParams.get("status"));
  const initialChannel: SalesChannelFilter =
    parseSalesChannelFilter(searchParams.get("sales_channel")) ?? "";

  const { showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [total, setTotal] = useState(initialTotal);
  const [statusCounts, setStatusCounts] = useState<SellerOrderStatusCount[]>(initialStatusCounts);
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(loadError);

  // Server-side filters & pagination
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState<SellerOrderFilterKey>(initialFilter);
  const [queryInput, setQueryInput] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [carrierFilter, setCarrierFilter] = useState("all");
  const [paymentFilter, setPaymentFilter] = useState<"all" | "COD" | "VIETQR" | "ZALOPAY">("all");
  const [channelFilter, setChannelFilter] = useState<SalesChannelFilter>(initialChannel);
  const [dueBeforeDate, setDueBeforeDate] = useState("");

  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [confirmBatchBusy, setConfirmBatchBusy] = useState(false);

  // Quick slide-over drawer + inline single-order ship form
  const [drawerOrderId, setDrawerOrderId] = useState<string | null>(null);
  const [shippingOrderId, setShippingOrderId] = useState<string | null>(null);
  const [carrierCode, setCarrierCode] = useState(CARRIER_OPTIONS[0].carrierCode);
  const [serviceCode, setServiceCode] = useState(CARRIER_OPTIONS[0].services[0].serviceCode);

  // Wave 3b-3 batch ship, picking list & merged PDF labels
  const [selectedOrderIds, setSelectedOrderIds] = useState<string[]>([]);
  const [selectedMetaById, setSelectedMetaById] = useState<Record<string, SelectedOrderMeta>>({});
  const [batchCarrierCode, setBatchCarrierCode] = useState("");
  const [batchServiceCode, setBatchServiceCode] = useState("");
  const [batchShipBusy, setBatchShipBusy] = useState(false);
  const [batchShipResult, setBatchShipResult] = useState<BatchShipOrdersResponse | null>(null);
  const [batchTrackedById, setBatchTrackedById] = useState<Record<string, TrackedBatchOrderState>>(
    {}
  );
  const [pollTickVersion, setPollTickVersion] = useState(0);
  const [pickingBusy, setPickingBusy] = useState(false);
  const [pickingList, setPickingList] = useState<PickingListResponse | null>(null);
  const [pdfBusy, setPdfBusy] = useState(false);

  const loadSeqRef = useRef(0);
  const pollCountRef = useRef(0);
  const offPagePollCursorRef = useRef(0);
  const didSkipInitialFetchRef = useRef(false);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(queryInput.trim());
      setPage(1);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [queryInput]);

  const loadOrders = useCallback(
    async (opts?: { silent?: boolean }) => {
      const seq = ++loadSeqRef.current;
      if (!opts?.silent) {
        setLoading(true);
        setErrorMsg(null);
      }
      try {
        const baseQs = buildSellerOrdersQuery({
          page,
          pageSize: SELLER_ORDERS_PAGE_SIZE,
          filter,
          carrierCode: carrierFilter,
          paymentMethod: paymentFilter,
          keyword: debouncedQuery,
          dueBeforeDate,
          includeStatusCounts: true,
        });
        const params = new URLSearchParams(baseQs);
        if (channelFilter) {
          params.set("sales_channel", channelFilter);
        }
        const res = await api<{
          items?: GwSellerOrder[];
          total?: number;
          status_counts?: SellerOrderStatusCount[];
        }>(`/api/seller/orders?${params.toString()}`);

        if (loadSeqRef.current !== seq) return;
        const items = Array.isArray(res?.items) ? res.items : [];
        const resolvedTotal = typeof res?.total === "number" ? res.total : items.length;
        if (Array.isArray(res?.status_counts)) {
          setStatusCounts(res.status_counts);
        }
        const clampedPage = resolveClampedPageOnEmpty(
          page,
          items.length,
          resolvedTotal,
          SELLER_ORDERS_PAGE_SIZE
        );
        if (clampedPage !== null) {
          setTotal(resolvedTotal);
          setPage(clampedPage);
          return;
        }
        setOrders(items);
        setTotal(resolvedTotal);
        setSelectedMetaById((prev) => {
          const next = { ...prev };
          for (const ord of items) {
            if (ord.id in next) {
              next[ord.id] = {
                order_code: ord.order_code,
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status,
              };
            }
          }
          return next;
        });
        setBatchTrackedById((prev) => {
          let changed = false;
          const next = { ...prev };
          for (const ord of items) {
            if (ord.id in next) {
              next[ord.id] = {
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status ?? "",
              };
              changed = true;
            }
          }
          return changed ? next : prev;
        });
      } catch (err) {
        if (loadSeqRef.current !== seq) return;
        if (!opts?.silent) {
          setErrorMsg(getErrorMessage(err, "Không thể tải danh sách đơn hàng lúc này."));
        }
      } finally {
        if (loadSeqRef.current === seq && !opts?.silent) {
          setLoading(false);
        }
      }
    },
    [page, filter, carrierFilter, paymentFilter, channelFilter, debouncedQuery, dueBeforeDate]
  );

  useEffect(() => {
    if (uiState) return;
    // Skip redundant initial mount fetch when SSR already loaded default unfiltered page 1 cleanly
    const isDefaultUnfiltered =
      page === 1 &&
      filter === "all" &&
      carrierFilter === "all" &&
      paymentFilter === "all" &&
      channelFilter === "" &&
      !debouncedQuery &&
      !dueBeforeDate;
    if (!didSkipInitialFetchRef.current && isDefaultUnfiltered && !loadError) {
      didSkipInitialFetchRef.current = true;
      return;
    }
    didSkipInitialFetchRef.current = true;
    void loadOrders();
  }, [
    uiState,
    loadOrders,
    page,
    filter,
    carrierFilter,
    paymentFilter,
    channelFilter,
    debouncedQuery,
    dueBeforeDate,
    loadError,
  ]);

  const ordersById = useMemo(() => {
    const map = new Map<string, GwSellerOrder>();
    for (const o of orders) map.set(o.id, o);
    return map;
  }, [orders]);

  const hasOnPageQueuedOrder = useMemo(
    () =>
      orders.some(
        (o) => !o.tracking_code && (o.shipment_job_status ?? "").trim().toUpperCase() === "QUEUED"
      ),
    [orders]
  );

  const offPagePendingCount = useMemo(() => {
    if (!batchShipResult) return 0;
    return selectOffPageOrdersToPoll(
      batchShipResult.results ?? [],
      ordersById,
      batchTrackedById,
      0,
      BATCH_POLL_MAX_OFF_PAGE_PER_TICK
    ).pendingCount;
  }, [batchShipResult, ordersById, batchTrackedById]);

  const hasQueuedShipmentJob = hasOnPageQueuedOrder || offPagePendingCount > 0;

  const pollBatchProgress = useCallback(async () => {
    const plan = batchShipResult
      ? selectOffPageOrdersToPoll(
          batchShipResult.results ?? [],
          ordersById,
          batchTrackedById,
          offPagePollCursorRef.current,
          BATCH_POLL_MAX_OFF_PAGE_PER_TICK
        )
      : { ids: [], pendingCount: 0, nextCursor: 0 };
    offPagePollCursorRef.current = plan.nextCursor;

    const tasks: Array<Promise<unknown>> = [loadOrders({ silent: true })];
    if (plan.ids.length > 0) {
      tasks.push(
        Promise.allSettled(
          plan.ids.map((id) =>
            api<{ order: GwSellerOrder }>(`/api/seller/orders/${encodeURIComponent(id)}`)
          )
        ).then((results) => {
          const fetched: GwSellerOrder[] = [];
          for (const r of results) {
            if (r.status === "fulfilled" && r.value?.order) {
              fetched.push(r.value.order);
            }
          }
          if (fetched.length === 0) return;
          setBatchTrackedById((prev) => {
            const next = { ...prev };
            for (const ord of fetched) {
              next[ord.id] = {
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status ?? "",
              };
            }
            return next;
          });
          setSelectedMetaById((prev) => {
            let changed = false;
            const next = { ...prev };
            for (const ord of fetched) {
              if (ord.id in next) {
                next[ord.id] = {
                  order_code: ord.order_code,
                  tracking_code: ord.tracking_code ?? "",
                  status: ord.status,
                  shipment_job_status: ord.shipment_job_status,
                };
                changed = true;
              }
            }
            return changed ? next : prev;
          });
        })
      );
    }

    await Promise.all(tasks);
    setPollTickVersion((v) => v + 1);
  }, [batchShipResult, ordersById, batchTrackedById, loadOrders]);

  useEffect(() => {
    if (uiState || !hasQueuedShipmentJob) {
      pollCountRef.current = 0;
      return;
    }
    if (pollCountRef.current >= BATCH_POLL_MAX_TICKS) return;
    const timer = window.setTimeout(() => {
      pollCountRef.current += 1;
      void pollBatchProgress();
    }, 3000);
    return () => window.clearTimeout(timer);
  }, [uiState, hasQueuedShipmentJob, pollBatchProgress, pollTickVersion]);

  const counts = useMemo(
    () => aggregateSellerOrderStatusCounts(statusCounts, total),
    [statusCounts, total]
  );

  const totalPages = Math.max(1, Math.ceil(total / SELLER_ORDERS_PAGE_SIZE));
  const startIndex = total === 0 ? 0 : (page - 1) * SELLER_ORDERS_PAGE_SIZE + 1;
  const endIndex = Math.min(total, (page - 1) * SELLER_ORDERS_PAGE_SIZE + orders.length);

  const pageOrderIds = useMemo(() => orders.map((o) => o.id), [orders]);
  const selectedSet = useMemo(() => new Set(selectedOrderIds), [selectedOrderIds]);
  const allPageSelected =
    pageOrderIds.length > 0 && pageOrderIds.every((id) => selectedSet.has(id));

  const selectedTrackingCodes = useMemo(() => {
    const entries = selectedOrderIds.map((id) => {
      const live = ordersById.get(id);
      const tracked = batchTrackedById[id];
      const cached = selectedMetaById[id];
      return {
        tracking_code: live?.tracking_code || tracked?.tracking_code || cached?.tracking_code || "",
      };
    });
    return collectBatchTrackingCodes(entries, MAX_BATCH_SHIP_ORDERS);
  }, [selectedOrderIds, ordersById, batchTrackedById, selectedMetaById]);

  const handleToggleSelectOrder = (order: GwSellerOrder) => {
    const { next, capped } = toggleBatchSelection(
      selectedOrderIds,
      order.id,
      MAX_BATCH_SHIP_ORDERS
    );
    if (capped) {
      showToast(`Chỉ được chọn tối đa ${MAX_BATCH_SHIP_ORDERS} đơn hàng mỗi lô.`);
      return;
    }
    setSelectedOrderIds(next);
    setSelectedMetaById((prev) => {
      const copy = { ...prev };
      if (next.includes(order.id)) {
        copy[order.id] = {
          order_code: order.order_code,
          tracking_code: order.tracking_code ?? "",
          status: order.status,
          shipment_job_status: order.shipment_job_status,
        };
      } else {
        delete copy[order.id];
      }
      return copy;
    });
  };

  const handleToggleSelectPage = () => {
    if (allPageSelected) {
      const pageSet = new Set(pageOrderIds);
      setSelectedOrderIds((prev) => prev.filter((id) => !pageSet.has(id)));
      setSelectedMetaById((prev) => {
        const copy = { ...prev };
        for (const id of pageOrderIds) delete copy[id];
        return copy;
      });
      return;
    }

    const nextIds = [...selectedOrderIds];
    const nextMeta = { ...selectedMetaById };
    let hitCap = false;
    for (const ord of orders) {
      if (nextIds.includes(ord.id)) continue;
      if (nextIds.length >= MAX_BATCH_SHIP_ORDERS) {
        hitCap = true;
        break;
      }
      nextIds.push(ord.id);
      nextMeta[ord.id] = {
        order_code: ord.order_code,
        tracking_code: ord.tracking_code ?? "",
        status: ord.status,
        shipment_job_status: ord.shipment_job_status,
      };
    }
    setSelectedOrderIds(nextIds);
    setSelectedMetaById(nextMeta);
    if (hitCap) {
      showToast(`Đã đạt giới hạn tối đa ${MAX_BATCH_SHIP_ORDERS} đơn hàng mỗi lô.`);
    }
  };

  const handleClearSelection = () => {
    setSelectedOrderIds([]);
    setSelectedMetaById({});
  };

  const shippingOrder = useMemo(
    () => orders.find((o) => o.id === shippingOrderId) ?? null,
    [orders, shippingOrderId]
  );

  const carrierOptions = useMemo(() => buildCarrierSelectOptions(shippingOrder), [shippingOrder]);
  const activeCarrierOption = useMemo(
    () => carrierOptions.find((c) => c.carrierCode === carrierCode) ?? carrierOptions[0],
    [carrierOptions, carrierCode]
  );
  const activeBatchCarrier = useMemo(
    () => CARRIER_OPTIONS.find((c) => c.carrierCode === batchCarrierCode) ?? null,
    [batchCarrierCode]
  );

  const handleToggleShipDrawer = (order: GwSellerOrder) => {
    if (shippingOrderId === order.id) {
      setShippingOrderId(null);
      return;
    }
    const defaults = resolveOrderShippingDefaults(order);
    setCarrierCode(defaults.carrierCode);
    setServiceCode(defaults.serviceCode);
    setShippingOrderId(order.id);
  };

  const handleCarrierChange = (nextCarrier: string) => {
    setCarrierCode(nextCarrier);
    const opt = carrierOptions.find((c) => c.carrierCode === nextCarrier);
    if (opt && opt.services[0]) {
      setServiceCode(opt.services[0].serviceCode);
    }
  };

  const handleConfirmOrder = async (order: GwSellerOrder) => {
    if (!canSellerConfirmOrder(order.status, order.shipment_job_status) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
        { method: "POST" }
      );
      await loadOrders();
      router.refresh();
      showToast(`Đã xác nhận đơn ${order.order_code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleShipOrder = async (
    order: GwSellerOrder,
    override?: { carrier_code: string; service_code: string }
  ) => {
    if (!canSellerShipOrder(order.status, order.tracking_code) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const res = await api<{
        order: GwSellerOrder;
        tracking_code?: string;
        label_url?: string;
        queued?: boolean;
        shipment_job_id?: string;
      }>(`/api/seller/orders/${encodeURIComponent(order.id)}/ship`, {
        method: "POST",
        body: {
          carrier_code: override?.carrier_code ?? carrierCode,
          service_code: override?.service_code ?? serviceCode,
        },
      });
      setShippingOrderId(null);
      await loadOrders();
      router.refresh();
      if (res.queued) {
        showToast(
          `Đơn ${order.order_code} đang chờ tạo vận đơn với hãng — bạn có thể bấm Thử tạo lại ngay.`
        );
      } else {
        showToast(
          res.tracking_code
            ? `Đã tạo vận đơn ${res.tracking_code} cho đơn ${order.order_code}.`
            : `Đã tạo vận đơn cho đơn ${order.order_code}.`
        );
      }
    } catch (err) {
      showToast(translateSellerShipError(err));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleConfirmPagePending = async () => {
    const pending = orders.filter((o) => canSellerConfirmOrder(o.status, o.shipment_job_status));
    if (pending.length === 0 || confirmBatchBusy) return;
    setConfirmBatchBusy(true);
    let okCount = 0;
    try {
      for (const order of pending) {
        try {
          await api<{ order: GwSellerOrder }>(
            `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
            { method: "POST" }
          );
          okCount += 1;
        } catch {
          // Continue confirming remaining orders
        }
      }
      await loadOrders();
      router.refresh();
      showToast(`Đã xác nhận ${formatInt(okCount)}/${formatInt(pending.length)} đơn chờ.`);
    } finally {
      setConfirmBatchBusy(false);
    }
  };

  const handleBatchShip = async () => {
    if (selectedOrderIds.length === 0 || batchShipBusy) return;
    setBatchShipBusy(true);
    try {
      const res = await api<BatchShipOrdersResponse>("/api/seller/orders/batch-ship", {
        method: "POST",
        body: {
          order_ids: selectedOrderIds.slice(0, MAX_BATCH_SHIP_ORDERS),
          carrier_code: batchCarrierCode,
          service_code: batchServiceCode,
        },
      });
      setBatchShipResult(res);
      setBatchTrackedById(() => {
        const next: Record<string, TrackedBatchOrderState> = {};
        for (const row of res.results ?? []) {
          if (row.outcome === "QUEUED" || row.tracking_code) {
            next[row.order_id] = {
              tracking_code: row.tracking_code ?? "",
              status: "",
              shipment_job_status: row.outcome === "QUEUED" ? "QUEUED" : "",
            };
          }
        }
        return next;
      });
      pollCountRef.current = 0;
      offPagePollCursorRef.current = 0;
      await loadOrders();
      router.refresh();
      showToast(
        `Đã xếp hàng tạo vận đơn cho ${formatInt(res.queued_count)} đơn (${formatInt(res.skipped_count)} bỏ qua).`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo vận đơn hàng loạt lúc này."));
    } finally {
      setBatchShipBusy(false);
    }
  };

  const handleLoadPickingList = async () => {
    if (selectedOrderIds.length === 0 || pickingBusy) return;
    setPickingBusy(true);
    try {
      const res = await api<PickingListResponse>("/api/seller/orders/picking-list", {
        method: "POST",
        body: { order_ids: selectedOrderIds.slice(0, MAX_BATCH_SHIP_ORDERS) },
      });
      setPickingList(res);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo danh sách nhặt hàng lúc này."));
    } finally {
      setPickingBusy(false);
    }
  };

  const handleBatchLabelsPdf = async () => {
    if (pdfBusy) return;
    if (selectedTrackingCodes.length === 0) {
      showToast("Các đơn đã chọn chưa có mã vận đơn.");
      return;
    }
    setPdfBusy(true);
    try {
      const blob = await apiBlob("/api/shipping/labels/batch", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/pdf",
        },
        body: JSON.stringify({ tracking_codes: selectedTrackingCodes }),
      });
      const pdfBlob =
        blob.type === "application/pdf" ? blob : new Blob([blob], { type: "application/pdf" });
      const url = URL.createObjectURL(pdfBlob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `nhan-van-don-gop-${selectedTrackingCodes.length}.pdf`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      showToast(`Đã tải nhãn gộp PDF cho ${formatInt(selectedTrackingCodes.length)} vận đơn.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải nhãn vận đơn gộp PDF lúc này."));
    } finally {
      setPdfBusy(false);
    }
  };

  const hasActiveFilters =
    filter !== "all" ||
    Boolean(queryInput.trim()) ||
    carrierFilter !== "all" ||
    paymentFilter !== "all" ||
    channelFilter !== "" ||
    Boolean(dueBeforeDate);

  const handleResetFilters = () => {
    setFilter("all");
    setQueryInput("");
    setDebouncedQuery("");
    setCarrierFilter("all");
    setPaymentFilter("all");
    setChannelFilter("");
    setDueBeforeDate("");
    setPage(1);
  };

  const countCards: Array<{ key: SellerOrderFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "CONFIRMED", label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED, count: counts.CONFIRMED },
    { key: "PREPARING", label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, count: counts.PREPARING },
    { key: "SHIPPING", label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, count: counts.SHIPPING },
    { key: "DONE", label: "Đã giao / Hoàn tất", count: counts.DONE },
    { key: "OTHER", label: "Chờ TT / Hủy / Trả", count: counts.OTHER },
  ];

  const pageConfirmedCount = orders.filter((o) =>
    canSellerConfirmOrder(o.status, o.shipment_job_status)
  ).length;
  const pageButtons = buildPaginationWindow(page, totalPages);
  const drawerOrder = useMemo(
    () => (drawerOrderId ? (orders.find((o) => o.id === drawerOrderId) ?? null) : null),
    [orders, drawerOrderId]
  );

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Đơn hàng</h1>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={loading}
            onClick={() => void loadOrders()}
          >
            <IconRefresh size={14} />
            <span>Làm mới</span>
          </Button>
          {pageConfirmedCount > 0 && (
            <Button
              variant="ink"
              size="sm"
              shape="app"
              disabled={confirmBatchBusy}
              onClick={() => void handleConfirmPagePending()}
            >
              <IconCheck size={14} />
              <span>
                {confirmBatchBusy
                  ? "Đang xác nhận..."
                  : `Xác nhận chờ (${formatInt(pageConfirmedCount)})`}
              </span>
            </Button>
          )}
        </div>
      </header>

      {(uiState === "loading" || (!uiState && loading && orders.length === 0)) && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải danh sách đơn hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách đơn hàng"
          message={errorMsg ?? "Kết nối đến hệ thống xử lý đơn hàng tạm gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => void loadOrders()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có đơn hàng nào"
          message="Khi người mua đặt hàng từ gian hàng, đơn sẽ xuất hiện tại đây."
          actionLabel="Xem sản phẩm"
          actionHref="/seller/san-pham"
        />
      )}

      {!uiState && !(loading && orders.length === 0) && !errorMsg && (
        <>
          <div className={common.countCards6} role="tablist" aria-label="Lọc theo trạng thái đơn">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                  onClick={() => {
                    setFilter(c.key);
                    setPage(1);
                  }}
                >
                  <span className={common.countCardLabel}>{c.label}</span>
                  <span className={common.countCardValue}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <div className={common.chipScroller} role="tablist" aria-label="Lọc theo trạng thái đơn">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={`m-${c.key}`}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                  onClick={() => {
                    setFilter(c.key);
                    setPage(1);
                  }}
                >
                  <span>{c.label}</span>
                  <span className={common.chipCount}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          {selectedOrderIds.length > 0 && (
            <section className={styles.batchBar} aria-label="Thao tác hàng loạt đơn hàng">
              <div className={styles.batchBarInfo}>
                <strong className="num">
                  Đã chọn {formatInt(selectedOrderIds.length)}/{MAX_BATCH_SHIP_ORDERS} đơn
                </strong>
                <span className={styles.queueMeta}>
                  ({formatInt(selectedTrackingCodes.length)} đã có vận đơn)
                </span>
                <button
                  type="button"
                  className={styles.rowDetailLink}
                  onClick={handleClearSelection}
                >
                  Bỏ chọn
                </button>
              </div>
              <div className={styles.batchBarControls}>
                <select
                  className={common.filterSelect}
                  value={batchCarrierCode}
                  onChange={(e) => {
                    const nextCarrier = e.target.value;
                    setBatchCarrierCode(nextCarrier);
                    const opt = CARRIER_OPTIONS.find((c) => c.carrierCode === nextCarrier);
                    setBatchServiceCode(opt?.services[0]?.serviceCode ?? "");
                  }}
                  aria-label="Hãng vận chuyển hàng loạt"
                >
                  <option value="">Giữ hãng theo từng đơn</option>
                  {CARRIER_OPTIONS.map((c) => (
                    <option key={c.carrierCode} value={c.carrierCode}>
                      {c.carrierLabel}
                    </option>
                  ))}
                </select>
                {activeBatchCarrier && (
                  <select
                    className={common.filterSelect}
                    value={batchServiceCode}
                    onChange={(e) => setBatchServiceCode(e.target.value)}
                    aria-label="Gói dịch vụ vận chuyển hàng loạt"
                  >
                    {activeBatchCarrier.services.map((s) => (
                      <option key={s.serviceCode} value={s.serviceCode}>
                        {s.serviceLabel}
                      </option>
                    ))}
                  </select>
                )}
                <Button
                  variant="ink"
                  size="sm"
                  shape="app"
                  disabled={batchShipBusy}
                  onClick={() => void handleBatchShip()}
                >
                  {batchShipBusy ? "Đang xếp hàng..." : "Tạo vận đơn hàng loạt"}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={pickingBusy}
                  onClick={() => void handleLoadPickingList()}
                >
                  {pickingBusy ? "Đang tổng hợp..." : "In danh sách nhặt hàng"}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={pdfBusy || selectedTrackingCodes.length === 0}
                  onClick={() => void handleBatchLabelsPdf()}
                >
                  {pdfBusy
                    ? "Đang xuất PDF..."
                    : `In nhãn gộp PDF (${formatInt(selectedTrackingCodes.length)})`}
                </Button>
              </div>
            </section>
          )}

          {batchShipResult && (
            <section className={common.card} aria-label="Kết quả tạo vận đơn hàng loạt">
              <div className={common.cardHeader}>
                <div>
                  <h2 className={common.cardTitle}>
                    Kết quả lô vận đơn:{" "}
                    <span className="num">{formatInt(batchShipResult.queued_count)}</span> đã xếp
                    hàng · <span className="num">{formatInt(batchShipResult.skipped_count)}</span>{" "}
                    bỏ qua
                  </h2>
                </div>
                <div className={styles.actionGroup}>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => {
                      pollCountRef.current = 0;
                      void pollBatchProgress();
                    }}
                  >
                    Cập nhật tiến độ
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => {
                      setBatchShipResult(null);
                      setBatchTrackedById({});
                    }}
                  >
                    Đóng
                  </Button>
                </div>
              </div>
              <div className={common.tableWrap}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã đơn</th>
                      <th>Kết quả lô</th>
                      <th>Trạng thái xử lý</th>
                      <th>Chi tiết</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(batchShipResult.results ?? []).map((item) => {
                      const liveOrder = ordersById.get(item.order_id);
                      const trackedOrder = batchTrackedById[item.order_id];
                      const liveTracking =
                        liveOrder?.tracking_code ||
                        trackedOrder?.tracking_code ||
                        item.tracking_code;
                      const liveJobStatus = (
                        liveOrder?.shipment_job_status ||
                        trackedOrder?.shipment_job_status ||
                        ""
                      ).toUpperCase();
                      const isQueuedOutcome = item.outcome === "QUEUED";

                      let progressLabel = "Bỏ qua";
                      let progressTone: "neutral" | "warn" | "pos" | "neg" = "neutral";
                      if (liveTracking) {
                        progressLabel = `Đã có mã: ${liveTracking}`;
                        progressTone = "pos";
                      } else if (liveJobStatus === "FAILED") {
                        progressLabel = "Tạo vận đơn thất bại";
                        progressTone = "neg";
                      } else if (isQueuedOutcome || liveJobStatus === "QUEUED") {
                        progressLabel = "Đang chờ tạo vận đơn";
                        progressTone = "warn";
                      }

                      const detailText = isQueuedOutcome
                        ? liveTracking
                          ? "Đã tạo vận đơn thành công."
                          : liveJobStatus === "FAILED"
                            ? "Tạo vận đơn thất bại. Mở chi tiết đơn để thử lại."
                            : "Đã đưa vào hàng đợi tạo vận đơn với hãng."
                        : translateBatchShipReasonCode(item.reason_code);

                      return (
                        <tr key={item.order_id}>
                          <td>
                            <Link
                              href={`/seller/don-hang/${item.order_id}`}
                              className={common.cellLink}
                            >
                              {item.order_code || item.order_id.slice(0, 8)}
                            </Link>
                          </td>
                          <td>
                            <span className={statusPillClass(isQueuedOutcome ? "warn" : "neg")}>
                              {isQueuedOutcome ? "Đã xếp hàng" : "Bỏ qua"}
                            </span>
                          </td>
                          <td>
                            <span className={statusPillClass(progressTone)}>{progressLabel}</span>
                          </td>
                          <td>{detailText}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          <section className={common.card} aria-label="Bảng đơn hàng gian hàng">
            <div className={common.toolbar} style={{ marginBottom: 14 }}>
              <div className={common.toolbarLeft}>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={queryInput}
                    onChange={(e) => setQueryInput(e.target.value)}
                    placeholder="Tìm mã đơn, tên khách, mã vận đơn..."
                    aria-label="Tìm kiếm đơn hàng"
                  />
                </div>

                <select
                  className={common.filterSelect}
                  value={carrierFilter}
                  onChange={(e) => {
                    setCarrierFilter(e.target.value);
                    setPage(1);
                  }}
                  aria-label="Lọc theo hãng vận chuyển"
                >
                  <option value="all">Mọi hãng vận chuyển</option>
                  {CARRIER_OPTIONS.map((c) => (
                    <option key={c.carrierCode} value={c.carrierCode}>
                      {c.carrierLabel}
                    </option>
                  ))}
                </select>

                <select
                  className={common.filterSelect}
                  value={paymentFilter}
                  onChange={(e) => {
                    setPaymentFilter(e.target.value as "all" | "COD" | "VIETQR" | "ZALOPAY");
                    setPage(1);
                  }}
                  aria-label="Lọc theo phương thức thanh toán"
                >
                  <option value="all">Mọi hình thức thanh toán</option>
                  <option value="COD">Thu hộ COD</option>
                  <option value="VIETQR">VietQR</option>
                  <option value="ZALOPAY">Ví ZaloPay</option>
                </select>

                <select
                  className={common.filterSelect}
                  value={channelFilter}
                  onChange={(e) => {
                    setChannelFilter(e.target.value as SalesChannelFilter);
                    setPage(1);
                  }}
                  aria-label="Lọc theo kênh bán"
                >
                  {CHANNEL_FILTER_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>

                <label className={styles.dateFilterWrap}>
                  <span className={styles.queueMeta}>Hạn giao trước:</span>
                  <input
                    type="date"
                    className={styles.dateFilterInput}
                    value={dueBeforeDate}
                    onChange={(e) => {
                      setDueBeforeDate(e.target.value);
                      setPage(1);
                    }}
                    aria-label="Lọc theo hạn giao trước ngày"
                  />
                </label>

                {hasActiveFilters && (
                  <button
                    type="button"
                    className={styles.rowDetailLink}
                    onClick={handleResetFilters}
                  >
                    Xóa lọc
                  </button>
                )}
              </div>
              <span className={common.toolbarMeta}>
                {loading
                  ? "Đang tải..."
                  : `Hiển thị ${formatInt(startIndex)}–${formatInt(endIndex)} / ${formatInt(total)}`}
              </span>
            </div>

            {orders.length === 0 ? (
              <div className={common.inlineEmpty}>
                {counts.all === 0
                  ? "Gian hàng chưa phát sinh đơn hàng nào."
                  : "Không có đơn hàng nào khớp bộ lọc hiện tại."}
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={`${common.dataTable} ${styles.ordersTable}`}>
                    <thead>
                      <tr>
                        <th className={styles.checkCol}>
                          <input
                            type="checkbox"
                            className={styles.rowCheckbox}
                            checked={allPageSelected}
                            onChange={handleToggleSelectPage}
                            aria-label="Chọn tất cả đơn hàng trên trang này"
                          />
                        </th>
                        <th>Đơn hàng</th>
                        <th>Người nhận</th>
                        <th>Sản phẩm &amp; vận chuyển</th>
                        <th className={common.numCol}>Thực nhận</th>
                        <th>Trạng thái</th>
                        <th className={`${common.numCol} ${styles.actionCol}`}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {orders.map((o) => {
                        const meta = getSellerOrderStatusMeta(
                          o.status,
                          o.shipment_job_status,
                          o.tracking_code
                        );
                        const netVnd = computeSellerNetReceivable(o);
                        const isBusy = busyOrderId === o.id;
                        const canConfirm = canSellerConfirmOrder(o.status, o.shipment_job_status);
                        const jobStatus = (o.shipment_job_status ?? "").trim().toUpperCase();
                        const isQueuedOrFailed =
                          !o.tracking_code && (jobStatus === "QUEUED" || jobStatus === "FAILED");
                        const canShip = canSellerShipOrder(o.status, o.tracking_code);
                        const showShipDrawer = shippingOrderId === o.id;
                        const isSelected = selectedSet.has(o.id);
                        const firstPid = o.items?.[0]?.product_id;
                        const thumb = firstPid ? productImages[firstPid] : undefined;
                        const channel = normalizeSalesChannel(o.sales_channel);
                        const channelBadge = getSalesChannelMeta(channel);
                        const labelHref = buildShippingLabelHref(o.tracking_code);
                        const subText = `${formatPaymentMethodVi(o.payment_method)}${
                          o.carrier_code ? ` · ${formatCarrierLabel(o.carrier_code)}` : ""
                        }${o.tracking_code ? ` · ${o.tracking_code}` : ""}`;

                        return (
                          <React.Fragment key={o.id}>
                            <tr className={isSelected ? common.rowSelected : undefined}>
                              <td className={styles.checkCol}>
                                <input
                                  type="checkbox"
                                  className={styles.rowCheckbox}
                                  checked={isSelected}
                                  onChange={() => handleToggleSelectOrder(o)}
                                  aria-label={`Chọn đơn ${o.order_code}`}
                                />
                              </td>
                              <td>
                                <div className={styles.orderIdentity}>
                                  {thumb && (
                                    <img
                                      src={thumb}
                                      alt=""
                                      className={styles.orderThumb}
                                      width={32}
                                      height={40}
                                    />
                                  )}
                                  <div className={styles.orderMetaStack}>
                                    <button
                                      type="button"
                                      className={`${common.rowTriggerBtn} ${styles.orderCodeBtn}`}
                                      onClick={() => setDrawerOrderId(o.id)}
                                    >
                                      {o.order_code}
                                    </button>
                                    <span
                                      className={`${common.channelTag} ${common.channelTagCompact} ${
                                        channel === "STOREFRONT" ? common.channelTagStorefront : ""
                                      } ${styles.orderChannelInline}`}
                                    >
                                      {channelBadge.shortLabel}
                                    </span>
                                    {o.created_at && (
                                      <span className={`${common.cellSub} ${styles.orderDateSub}`}>
                                        {formatDateTimeVi(o.created_at)}
                                      </span>
                                    )}
                                  </div>
                                </div>
                              </td>
                              <td className={styles.buyerCell}>
                                <span className={styles.buyerName}>
                                  {o.shipping_address?.recipient_name || "Người mua"}
                                </span>
                                <span className={styles.buyerProvince}>
                                  {o.shipping_address?.province_name || "—"}
                                </span>
                              </td>
                              <td className={styles.itemColCell}>
                                <span
                                  className={styles.itemSummaryCell}
                                  title={summarizeOrderItems(o.items)}
                                >
                                  {summarizeOrderItems(o.items)}
                                </span>
                                <span className={styles.itemSubCell} title={subText}>
                                  {subText}
                                </span>
                              </td>
                              <td className={common.numCol}>
                                <strong>{formatVnd(netVnd)}</strong>
                                <span className={common.cellSub}>
                                  Hàng: {formatVnd(o.subtotal_vnd)}
                                </span>
                              </td>
                              <td>
                                <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                              </td>
                              <td className={`${common.numCol} ${styles.actionCol}`}>
                                <div className={styles.actionGroup}>
                                  {canConfirm && (
                                    <button
                                      type="button"
                                      className={styles.rowAdvanceBtn}
                                      disabled={isBusy}
                                      onClick={() => void handleConfirmOrder(o)}
                                    >
                                      {isBusy ? "..." : "Xác nhận"}
                                    </button>
                                  )}
                                  {canShip && (
                                    <button
                                      type="button"
                                      className={styles.rowAdvanceBtn}
                                      disabled={isBusy}
                                      onClick={() => handleToggleShipDrawer(o)}
                                    >
                                      {showShipDrawer
                                        ? "Đóng"
                                        : isQueuedOrFailed
                                          ? "Thử tạo lại ngay"
                                          : "Tạo vận đơn"}
                                    </button>
                                  )}
                                  {labelHref && (
                                    <a
                                      href={labelHref}
                                      target="_blank"
                                      rel="noopener noreferrer"
                                      className={styles.rowDetailLink}
                                      title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
                                    >
                                      In nhãn
                                    </a>
                                  )}
                                  <Link
                                    href={`/seller/don-hang/${o.id}`}
                                    className={styles.rowDetailLink}
                                  >
                                    Chi tiết
                                  </Link>
                                </div>
                              </td>
                            </tr>
                            {showShipDrawer && (
                              <tr>
                                <td colSpan={7} className={styles.shipDrawer}>
                                  <div className={styles.shipFormRow}>
                                    <span className={styles.queueMeta}>
                                      Tạo vận đơn cho đơn <strong>{o.order_code}</strong>
                                    </span>
                                    <div className={styles.shipFormControls}>
                                      <select
                                        className={common.filterSelect}
                                        value={carrierCode}
                                        onChange={(e) => handleCarrierChange(e.target.value)}
                                        aria-label="Chọn đơn vị vận chuyển"
                                      >
                                        {carrierOptions.map((c) => (
                                          <option key={c.carrierCode} value={c.carrierCode}>
                                            {c.carrierLabel}
                                          </option>
                                        ))}
                                      </select>
                                      <select
                                        className={common.filterSelect}
                                        value={serviceCode}
                                        onChange={(e) => setServiceCode(e.target.value)}
                                        aria-label="Chọn gói dịch vụ vận chuyển"
                                      >
                                        {activeCarrierOption.services.map((s) => (
                                          <option key={s.serviceCode} value={s.serviceCode}>
                                            {s.serviceLabel}
                                          </option>
                                        ))}
                                      </select>
                                      <Button
                                        variant="ink"
                                        size="sm"
                                        shape="app"
                                        disabled={isBusy}
                                        onClick={() => void handleShipOrder(o)}
                                      >
                                        {isBusy ? "Đang tạo..." : "Xác nhận tạo vận đơn"}
                                      </Button>
                                    </div>
                                  </div>
                                </td>
                              </tr>
                            )}
                          </React.Fragment>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  <div className={styles.mobileSelectAllRow}>
                    <label className={styles.mobileCheckLabel}>
                      <input
                        type="checkbox"
                        className={styles.rowCheckbox}
                        checked={allPageSelected}
                        onChange={handleToggleSelectPage}
                      />
                      <span>Chọn tất cả trang này ({formatInt(orders.length)})</span>
                    </label>
                  </div>
                  {orders.map((o) => {
                    const meta = getSellerOrderStatusMeta(
                      o.status,
                      o.shipment_job_status,
                      o.tracking_code
                    );
                    const isBusy = busyOrderId === o.id;
                    const canConfirm = canSellerConfirmOrder(o.status, o.shipment_job_status);
                    const jobStatus = (o.shipment_job_status ?? "").trim().toUpperCase();
                    const isQueuedOrFailed =
                      !o.tracking_code && (jobStatus === "QUEUED" || jobStatus === "FAILED");
                    const canShip = canSellerShipOrder(o.status, o.tracking_code);
                    const showShipDrawer = shippingOrderId === o.id;
                    const isSelected = selectedSet.has(o.id);
                    const labelHref = buildShippingLabelHref(o.tracking_code);
                    return (
                      <div key={o.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div className={styles.mobileRowLead}>
                            <input
                              type="checkbox"
                              className={styles.rowCheckbox}
                              checked={isSelected}
                              onChange={() => handleToggleSelectOrder(o)}
                              aria-label={`Chọn đơn ${o.order_code}`}
                            />
                            <div>
                              <Link
                                href={`/seller/don-hang/${o.id}`}
                                className={styles.mobileOrderCode}
                              >
                                {o.order_code}
                              </Link>
                              <span className={common.cellSub}>
                                {o.shipping_address?.recipient_name || "Người mua"} ·{" "}
                                {o.shipping_address?.province_name || "—"}
                              </span>
                            </div>
                          </div>
                          <span className={styles.mobileOrderTotal}>
                            {formatVnd(computeSellerNetReceivable(o))}
                          </span>
                        </div>
                        <p className={styles.mobileOrderSummary}>
                          {summarizeOrderItems(o.items)}
                          {o.tracking_code ? ` · ${o.tracking_code}` : ""}
                        </p>
                        <div className={common.mobileRowMeta}>
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          <div className={styles.actionGroup}>
                            {canConfirm && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                disabled={isBusy}
                                onClick={() => void handleConfirmOrder(o)}
                              >
                                {isBusy ? "..." : "Xác nhận"}
                              </button>
                            )}
                            {canShip && (
                              <button
                                type="button"
                                className={styles.rowAdvanceBtn}
                                disabled={isBusy}
                                onClick={() => handleToggleShipDrawer(o)}
                              >
                                {showShipDrawer
                                  ? "Đóng"
                                  : isQueuedOrFailed
                                    ? "Thử tạo lại ngay"
                                    : "Tạo vận đơn"}
                              </button>
                            )}
                            {labelHref && (
                              <a
                                href={labelHref}
                                target="_blank"
                                rel="noopener noreferrer"
                                className={styles.rowDetailLink}
                              >
                                In nhãn
                              </a>
                            )}
                            <Link
                              href={`/seller/don-hang/${o.id}`}
                              className={styles.rowDetailLink}
                            >
                              Chi tiết
                            </Link>
                          </div>
                        </div>
                        {showShipDrawer && (
                          <div className={styles.shipMobileBox}>
                            <select
                              className={common.filterSelect}
                              value={carrierCode}
                              onChange={(e) => handleCarrierChange(e.target.value)}
                            >
                              {carrierOptions.map((c) => (
                                <option key={c.carrierCode} value={c.carrierCode}>
                                  {c.carrierLabel}
                                </option>
                              ))}
                            </select>
                            <select
                              className={common.filterSelect}
                              value={serviceCode}
                              onChange={(e) => setServiceCode(e.target.value)}
                            >
                              {activeCarrierOption.services.map((s) => (
                                <option key={s.serviceCode} value={s.serviceCode}>
                                  {s.serviceLabel}
                                </option>
                              ))}
                            </select>
                            <Button
                              variant="ink"
                              size="sm"
                              shape="app"
                              disabled={isBusy}
                              onClick={() => void handleShipOrder(o)}
                            >
                              {isBusy ? "Đang tạo..." : "Xác nhận tạo vận đơn"}
                            </Button>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>

                <div className={styles.paginationBar} aria-label="Phân trang đơn hàng">
                  <span className={`${common.cardMeta} num`}>
                    Trang {formatInt(page)} / {formatInt(totalPages)} · Tổng {formatInt(total)} đơn
                  </span>
                  <div className={styles.paginationControls}>
                    <button
                      type="button"
                      className={styles.pageBtn}
                      disabled={page <= 1 || loading}
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                    >
                      Trang trước
                    </button>
                    {pageButtons.map((pNum, idx) => {
                      const prevNum = pageButtons[idx - 1];
                      const showGap = prevNum != null && pNum - prevNum > 1;
                      return (
                        <React.Fragment key={pNum}>
                          {showGap && <span className={styles.pageEllipsis}>…</span>}
                          <button
                            type="button"
                            className={`${styles.pageBtn} ${pNum === page ? styles.pageBtnActive : ""}`}
                            aria-current={pNum === page ? "page" : undefined}
                            disabled={loading}
                            onClick={() => setPage(pNum)}
                          >
                            {formatInt(pNum)}
                          </button>
                        </React.Fragment>
                      );
                    })}
                    <button
                      type="button"
                      className={styles.pageBtn}
                      disabled={page >= totalPages || loading}
                      onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                    >
                      Trang sau
                    </button>
                  </div>
                </div>
              </>
            )}
          </section>
        </>
      )}

      <SellerOrderQuickDrawer
        order={drawerOrder}
        actionBusy={drawerOrder ? busyOrderId === drawerOrder.id : false}
        onClose={() => setDrawerOrderId(null)}
        onAction={(order) => {
          const action = getNextBackendOrderAction(
            order.status,
            order.tracking_code,
            order.shipment_job_status
          );
          if (action?.kind === "confirm") {
            void handleConfirmOrder(order);
          } else if (action?.kind === "ship") {
            const defaults = resolveOrderShippingDefaults(order);
            void handleShipOrder(order, {
              carrier_code: defaults.carrierCode,
              service_code: defaults.serviceCode,
            });
          }
        }}
      />

      <SellerDrawer
        open={Boolean(pickingList)}
        onClose={() => setPickingList(null)}
        title="Danh sách nhặt hàng theo SKU"
        subtitle={
          pickingList
            ? `${formatInt(pickingList.order_count)} đơn hàng · Tổng ${formatInt(pickingList.total_units)} sản phẩm`
            : undefined
        }
        footer={
          <div className={styles.actionGroup}>
            <Button variant="ink" size="sm" shape="app" onClick={() => window.print()}>
              In danh sách
            </Button>
            <Button variant="outline" size="sm" shape="app" onClick={() => setPickingList(null)}>
              Đóng
            </Button>
          </div>
        }
      >
        {pickingList &&
          (pickingList.items.length === 0 ? (
            <div className={common.inlineEmpty}>Không có sản phẩm nào trong các đơn đã chọn.</div>
          ) : (
            <div className={common.tableWrap}>
              <table className={common.dataTable}>
                <thead>
                  <tr>
                    <th>Sản phẩm &amp; biến thể</th>
                    <th className={common.numCol}>Tổng SL</th>
                    <th className={common.numCol}>Số đơn</th>
                    <th>Mã đơn</th>
                  </tr>
                </thead>
                <tbody>
                  {pickingList.items.map((item) => (
                    <tr key={item.sku_id}>
                      <td>
                        <strong>{item.product_name}</strong>
                        <span className={common.cellSub}>{item.sku_name}</span>
                      </td>
                      <td className={common.numCol}>
                        <strong>{formatInt(item.total_quantity)}</strong>
                      </td>
                      <td className={common.numCol}>{formatInt(item.order_count)}</td>
                      <td className={styles.orderCodesCell}>
                        {(item.order_codes ?? []).join(", ") || "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ))}
      </SellerDrawer>
    </div>
  );
}
