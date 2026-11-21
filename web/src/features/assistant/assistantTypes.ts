// Shared types, pure normalizers, and input validators for buyer & seller AI assistant features.
// Zero React or Next.js imports so this module runs identically in BFF routes, UI, and Node 22 tests.
import { formatInt, formatPercent, formatVnd } from "../../lib/format.ts";
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";
import type { AssistantSseEvent } from "./sseParser.ts";

// ---- Public component prop contracts (aligned with docs/reports/g-web.md) ----

export interface SiteAssistantProductContext {
  id: string;
  slug?: string;
  name: string;
  priceVnd?: number;
}

export interface SiteAssistantWidgetProps {
  shopId: string;
  brandName: string;
  productContext?: SiteAssistantProductContext;
  /** Called after successful handoff to a live shop chat conversation. */
  onHandoffToChat?: (conversationId: string) => void;
  defaultOpen?: boolean;
  offsetBottomPx?: number;
  className?: string;
}

export interface ProductReviewSummaryProps {
  productId: string;
  productName?: string;
  className?: string;
}

// ---- Conversation & structured tool card models ----

export interface AssistantChatInputMessage {
  role: "user" | "assistant";
  content: string;
}

export interface AssistantProductCard {
  id: string;
  slug: string;
  name: string;
  priceVnd: number;
  originalPriceVnd?: number;
  image?: string;
  href: string;
  stockLabel?: string;
  skuId?: string;
  size?: string;
  color?: string;
}

export interface AssistantPendingCartAction {
  id: string;
  skuId: string;
  productId?: string;
  productName: string;
  variantLabel?: string;
  size?: string;
  priceVnd?: number;
  quantity: number;
  image?: string;
  status: "pending" | "adding" | "added" | "dismissed" | "error";
  statusMessage?: string;
  requiresAuth?: boolean;
}

export interface AssistantOrderCard {
  id?: string;
  orderCode: string;
  status: string;
  statusLabel: string;
  totalVnd?: number;
  trackingCode?: string;
  updatedAt?: string;
  href: string;
}

export interface AssistantCitation {
  title: string;
  section?: string;
  href: string;
}

export interface AssistantMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  streaming?: boolean;
  toolStatus?: string | null;
  products?: AssistantProductCard[];
  pendingCartActions?: AssistantPendingCartAction[];
  orders?: AssistantOrderCard[];
  citations?: AssistantCitation[];
  requiresAuthPrompt?: boolean;
  feedback?: "helpful" | "unhelpful" | null;
  error?: string | null;
}

export interface ExtractedToolCardData {
  products: AssistantProductCard[];
  pendingCartActions: AssistantPendingCartAction[];
  orders: AssistantOrderCard[];
  citations: AssistantCitation[];
  requiresAuth: boolean;
}

export interface ReviewSummaryDTO {
  productId: string;
  available: boolean;
  reviewCount: number;
  minReviewsRequired: number;
  averageRating: number;
  summary: string;
  pros: string[];
  cons: string[];
  aiGenerated: boolean;
  updatedAt?: string;
}

export interface SellerReportKpi {
  label: string;
  value: string;
  caption?: string;
  tone?: "neutral" | "pos" | "neg" | "warn";
}

export interface SellerReportRow {
  label: string;
  subLabel?: string;
  unitsSold?: number;
  revenueVnd?: number;
  extra?: string;
}

export interface SellerReportResult {
  period: SellerReportPeriod;
  question?: string;
  answer: string;
  kpis: SellerReportKpi[];
  rows: SellerReportRow[];
  tableColumns?: string[];
  genericRows?: Array<Record<string, string | number>>;
  generatedAt?: string;
}

export type SellerReportPeriod = "7d" | "30d" | "90d";

export interface DescribeProductResult {
  titleSuggestion?: string;
  description: string;
  highlights: string[];
  aiGenerated: boolean;
}

export interface SuggestReplyResult {
  suggestions: string[];
}

export type ToolResultEventLike = Extract<AssistantSseEvent, { type: "tool_result" }>;

// ---- Internal coercion utilities ----

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function asInt(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.trunc(value);
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return Math.trunc(parsed);
  }
  return undefined;
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function asStringList(value: unknown, maxItems = 20): string[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of value) {
    const clean = asString(item);
    if (!clean || seen.has(clean)) continue;
    seen.add(clean);
    out.push(clean);
    if (out.length >= maxItems) break;
  }
  return out;
}

const ORDER_STATUS_LABELS: Record<string, string> = {
  PENDING_PAYMENT: "Chờ thanh toán",
  PAID: "Đã thanh toán",
  CONFIRMED: "Đã xác nhận",
  PROCESSING: "Đang chuẩn bị hàng",
  PACKING: "Đang đóng gói",
  SHIPPING: "Đang giao hàng",
  SHIPPED: "Đang giao hàng",
  DELIVERED: "Đã giao hàng",
  COMPLETED: "Hoàn thành",
  CANCELLED: "Đã hủy",
  RETURN_REQUESTED: "Đang yêu cầu trả hàng",
  RETURN_APPROVED: "Đã duyệt trả hàng",
  REFUNDED: "Đã hoàn tiền",
};

export function getAssistantOrderStatusLabel(status: string): string {
  const key = status.trim().toUpperCase();
  return ORDER_STATUS_LABELS[key] || status.trim() || "Đang xử lý";
}

export function getToolActivityLabel(toolName: string): string {
  const name = toolName.trim().toLowerCase();
  if (name.includes("cart")) return "Đang chuẩn bị thông tin thêm vào giỏ hàng...";
  if (name.includes("order") || name.includes("track")) {
    return "Đang tra cứu thông tin đơn hàng của bạn...";
  }
  if (name.includes("policy") || name.includes("return") || name.includes("shipping")) {
    return "Đang kiểm tra chính sách của cửa hàng...";
  }
  if (name.includes("review")) return "Đang tổng hợp nhận xét từ khách hàng...";
  return "Đang tìm sản phẩm phù hợp trong cửa hàng...";
}

const TECHNICAL_JARGON_RE = /\b(jwt|sse|rag|pgvector|token|sandbox|grpc|sql|redis|nats)\b/i;

export function sanitizeAssistantError(code?: string, rawMessage?: string): string {
  const normalizedCode = (code ?? "").trim().toUpperCase();
  if (normalizedCode === "RATE_LIMITED" || normalizedCode === "TOO_MANY_REQUESTS") {
    return "Bạn gửi câu hỏi hơi nhanh. Vui lòng chờ một lát rồi thử lại.";
  }
  if (normalizedCode === "UNAUTHENTICATED" || normalizedCode === "UNAUTHORIZED") {
    return "Vui lòng đăng nhập để sử dụng thao tác liên quan đến tài khoản và đơn hàng.";
  }
  if (normalizedCode === "QUOTA_EXCEEDED") {
    return "Lượt hỗ trợ tự động hôm nay đã đạt giới hạn. Bạn có thể nhắn trực tiếp với nhân viên gian hàng.";
  }
  const clean = (rawMessage ?? "").trim();
  if (!clean || TECHNICAL_JARGON_RE.test(clean)) {
    return "Trợ lý tạm thời chưa thể trả lời lúc này. Bạn thử hỏi lại hoặc nhắn trực tiếp với nhân viên gian hàng.";
  }
  return clean;
}

// ---- Tool result card extractor ----

function parseProductCard(raw: unknown): AssistantProductCard | null {
  if (!isRecord(raw)) return null;
  const id = asString(raw.id ?? raw.product_id ?? raw.productId);
  const slug = asString(raw.slug ?? raw.product_slug ?? raw.productSlug) || id;
  const name = asString(raw.name ?? raw.product_name ?? raw.productName ?? raw.title);
  if (!name || !slug) return null;

  const priceVnd = Math.max(
    0,
    asInt(raw.price_vnd ?? raw.priceVnd ?? raw.min_price_vnd ?? raw.minPriceVnd ?? raw.price) ?? 0
  );
  const orig = asInt(raw.original_price_vnd ?? raw.originalPriceVnd);
  const rawImg =
    asString(raw.image ?? raw.image_url ?? raw.imageUrl ?? raw.thumbnail) ||
    (Array.isArray(raw.images) ? asString(raw.images[0]) : "");
  const safeImg = sanitizeSafeUrl(rawImg) || undefined;

  const rawHref = asString(raw.href ?? raw.url);
  const safeExplicitHref = rawHref.startsWith("/san-pham/") ? sanitizeSafeUrl(rawHref) : "";
  const href = safeExplicitHref || `/san-pham/${encodeURIComponent(slug)}`;

  const stockCount = asInt(raw.available_stock ?? raw.availableStock ?? raw.stock);
  let stockLabel: string | undefined;
  if (typeof stockCount === "number") {
    stockLabel = stockCount > 0 ? "Còn hàng" : "Tạm hết hàng";
  } else if (raw.in_stock === false || raw.inStock === false) {
    stockLabel = "Tạm hết hàng";
  }

  return {
    id: id || slug,
    slug,
    name,
    priceVnd,
    originalPriceVnd: orig && orig > priceVnd ? orig : undefined,
    image: safeImg,
    href,
    stockLabel,
    skuId: asString(raw.sku_id ?? raw.skuId ?? raw.default_sku_id) || undefined,
    size: asString(raw.size) || undefined,
    color: asString(raw.color ?? raw.color_name) || undefined,
  };
}

function parsePendingCartAction(
  raw: unknown,
  requiresAuth: boolean,
  fallbackId: string
): AssistantPendingCartAction | null {
  if (!isRecord(raw)) return null;
  const skuId = asString(raw.sku_id ?? raw.skuId);
  if (!skuId) return null;
  const productName =
    asString(raw.product_name ?? raw.productName ?? raw.name ?? raw.title) || "Sản phẩm đã chọn";
  const quantity = Math.max(1, Math.min(20, asInt(raw.quantity ?? raw.qty) ?? 1));
  const priceVnd = asInt(raw.price_vnd ?? raw.priceVnd ?? raw.unit_price_vnd);
  const size = asString(raw.size) || undefined;
  const color = asString(raw.color ?? raw.color_name);
  const variantLabel =
    asString(raw.variant_label ?? raw.variantLabel ?? raw.sku_name ?? raw.skuName) ||
    [color, size].filter(Boolean).join(" / ") ||
    undefined;
  const safeImage = sanitizeSafeUrl(asString(raw.image ?? raw.image_url ?? raw.imageUrl));

  return {
    id: fallbackId,
    skuId,
    productId: asString(raw.product_id ?? raw.productId) || undefined,
    productName,
    variantLabel,
    size,
    priceVnd: typeof priceVnd === "number" && priceVnd > 0 ? priceVnd : undefined,
    quantity,
    image: safeImage || undefined,
    status: "pending",
    requiresAuth,
  };
}

function parseOrderCard(raw: unknown): AssistantOrderCard | null {
  if (!isRecord(raw)) return null;
  const id = asString(raw.id ?? raw.order_id ?? raw.orderId);
  const orderCode = asString(raw.order_code ?? raw.orderCode ?? raw.code) || id;
  if (!orderCode) return null;
  const status = asString(raw.status) || "PROCESSING";
  const totalVnd = asInt(raw.total_vnd ?? raw.totalVnd ?? raw.grand_total_vnd ?? raw.subtotal_vnd);
  const trackingCode = asString(raw.tracking_code ?? raw.trackingCode) || undefined;
  const updatedAt = asString(raw.updated_at ?? raw.updatedAt ?? raw.created_at) || undefined;
  const href = id ? `/don-hang/${encodeURIComponent(id)}` : "/tai-khoan/don-hang";
  return {
    id: id || undefined,
    orderCode,
    status,
    statusLabel: getAssistantOrderStatusLabel(status),
    totalVnd: typeof totalVnd === "number" && totalVnd >= 0 ? totalVnd : undefined,
    trackingCode,
    updatedAt,
    href,
  };
}

function parseCitation(raw: unknown): AssistantCitation | null {
  if (!isRecord(raw)) return null;
  const title = asString(
    raw.doc_title ?? raw.docTitle ?? raw.title ?? raw.policy_title ?? raw.section
  );
  const section = asString(raw.section ?? raw.heading);
  const rawHref = asString(raw.citation ?? raw.href ?? raw.url ?? raw.link);
  let href = sanitizeSafeUrl(rawHref);
  if (!href) {
    const slug = asString(raw.doc_slug ?? raw.docSlug);
    if (slug && /^[a-z0-9-]+$/i.test(slug)) {
      href = `/chinh-sach#${encodeURIComponent(slug)}`;
    }
  }
  if (!href || !title) return null;
  return {
    title,
    section: section && section !== title ? section : undefined,
    href,
  };
}

/**
 * Safely extracts structured UI cards (recommended products, pending add-to-cart confirmation,
 * order status cards, policy citations) from an assistant `tool_result` SSE event.
 * All image and link URLs are passed through `sanitizeSafeUrl`.
 */
export function extractToolCardData(event: ToolResultEventLike): ExtractedToolCardData {
  const products: AssistantProductCard[] = [];
  const pendingCartActions: AssistantPendingCartAction[] = [];
  const orders: AssistantOrderCard[] = [];
  const citations: AssistantCitation[] = [];

  const toolName = (event.name ?? "").trim().toLowerCase();
  const data = isRecord(event.data) ? event.data : {};
  const requiresAuth = Boolean(event.requiresAuth || data.requires_auth || data.requiresAuth);
  const requiresConfirmation = Boolean(
    event.requiresConfirmation || data.requires_confirmation || data.requiresConfirmation
  );

  // 1. Add-to-cart confirmation payload (UC-AI04: never auto-checkout; always confirm!)
  const isCartTool = toolName.includes("cart");
  const cartCandidate =
    data.pending_cart_action ??
    data.cart_action ??
    (isCartTool || requiresConfirmation ? data : null);
  if (cartCandidate) {
    const action = parsePendingCartAction(
      cartCandidate,
      requiresAuth,
      `cart-${event.id || asString((cartCandidate as Record<string, unknown>).sku_id ?? (cartCandidate as Record<string, unknown>).skuId) || "1"}`
    );
    if (action) {
      pendingCartActions.push(action);
    }
  }

  // 2. Product cards
  const productSources: unknown[] = [];
  if (Array.isArray(data.products)) productSources.push(...data.products);
  if (Array.isArray(data.items) && !toolName.includes("order") && !toolName.includes("policy")) {
    productSources.push(...data.items);
  }
  if (Array.isArray(data.recommendations)) productSources.push(...data.recommendations);
  if (isRecord(data.product)) productSources.push(data.product);

  const seenProductKeys = new Set<string>();
  for (const item of productSources) {
    const card = parseProductCard(item);
    if (!card) continue;
    const key = card.id || card.slug;
    if (seenProductKeys.has(key)) continue;
    seenProductKeys.add(key);
    products.push(card);
    if (products.length >= 6) break;
  }

  // 3. Order status cards
  const orderSources: unknown[] = [];
  if (Array.isArray(data.orders)) orderSources.push(...data.orders);
  if (isRecord(data.order)) orderSources.push(data.order);
  if (toolName.includes("order") && Array.isArray(data.items)) {
    orderSources.push(...data.items);
  }
  if (toolName.includes("order") && (data.order_code || data.orderCode)) {
    orderSources.push(data);
  }

  const seenOrders = new Set<string>();
  for (const item of orderSources) {
    const card = parseOrderCard(item);
    if (!card || seenOrders.has(card.orderCode)) continue;
    seenOrders.add(card.orderCode);
    orders.push(card);
    if (orders.length >= 4) break;
  }

  // 4. Policy citations
  const citationSources: unknown[] = [];
  if (Array.isArray(data.citations)) citationSources.push(...data.citations);
  if (Array.isArray(data.chunks)) citationSources.push(...data.chunks);
  if (Array.isArray(data.policies)) citationSources.push(...data.policies);
  if (toolName.includes("policy") && Array.isArray(data.items)) {
    citationSources.push(...data.items);
  }

  const seenCitations = new Set<string>();
  for (const item of citationSources) {
    const citation = parseCitation(item);
    if (!citation) continue;
    const key = `${citation.href}|${citation.title}`;
    if (seenCitations.has(key)) continue;
    seenCitations.add(key);
    citations.push(citation);
    if (citations.length >= 5) break;
  }

  return {
    products,
    pendingCartActions,
    orders,
    citations,
    requiresAuth,
  };
}

// ---- Review summary normalizer ----

export const MIN_REVIEWS_FOR_SUMMARY = 10;

export function normalizeReviewSummary(
  raw: unknown,
  fallbackProductId = ""
): ReviewSummaryDTO | null {
  if (!isRecord(raw)) return null;
  const src = isRecord(raw.summary)
    ? raw.summary
    : isRecord(raw.review_summary)
      ? raw.review_summary
      : raw;

  const productId = asString(src.product_id ?? src.productId) || fallbackProductId;
  const reviewCount = Math.max(
    0,
    asInt(src.review_count ?? src.reviewCount ?? src.total_reviews ?? src.totalReviews) ?? 0
  );
  const minReviewsRequired = Math.max(
    1,
    asInt(src.min_reviews_required ?? src.minReviewsRequired) ?? MIN_REVIEWS_FOR_SUMMARY
  );
  const averageRating = Math.max(
    0,
    Math.min(5, asNumber(src.average_rating ?? src.averageRating ?? src.rating) ?? 0)
  );
  const summaryText = asString(
    typeof src.summary === "string" ? src.summary : (src.overview ?? src.summary_text)
  );
  const pros = asStringList(src.pros ?? src.strengths ?? src.highlights, 8);
  const cons = asStringList(src.cons ?? src.weaknesses ?? src.drawbacks, 8);
  const availableFlag = typeof src.available === "boolean" ? src.available : true;
  const aiGenerated =
    typeof src.ai_generated === "boolean" ? src.ai_generated : src.aiGenerated !== false;
  const updatedAt = asString(src.updated_at ?? src.updatedAt) || undefined;

  const hasContent = summaryText.length > 0 || pros.length > 0 || cons.length > 0;
  const available =
    availableFlag &&
    reviewCount >= Math.max(MIN_REVIEWS_FOR_SUMMARY, minReviewsRequired) &&
    hasContent;

  return {
    productId,
    available,
    reviewCount,
    minReviewsRequired,
    averageRating: Number(averageRating.toFixed(1)),
    summary: summaryText,
    pros,
    cons,
    aiGenerated,
    updatedAt,
  };
}

export function shouldDisplayReviewSummary(dto: ReviewSummaryDTO | null): boolean {
  if (!dto) return false;
  const threshold = Math.max(MIN_REVIEWS_FOR_SUMMARY, dto.minReviewsRequired);
  if (!dto.available || dto.reviewCount < threshold) return false;
  return dto.summary.length > 0 || dto.pros.length > 0 || dto.cons.length > 0;
}

// ---- Seller AI report normalizer ----

export function normalizeSellerPeriod(raw: unknown): SellerReportPeriod {
  const clean = asString(raw).toLowerCase();
  if (clean === "7d" || clean === "30d" || clean === "90d") return clean;
  return "30d";
}

export function normalizeSellerReport(
  raw: unknown,
  fallback: { period?: SellerReportPeriod; question?: string } = {}
): SellerReportResult {
  const src = isRecord(raw) && isRecord(raw.report) ? raw.report : isRecord(raw) ? raw : {};
  const period = normalizeSellerPeriod(src.period ?? fallback.period);
  const answer = asString(src.answer ?? src.narrative ?? src.analysis ?? src.summary ?? src.text);
  const generatedAt = asString(src.generated_at ?? src.generatedAt) || undefined;

  const kpis: SellerReportKpi[] = [];
  if (Array.isArray(src.kpis)) {
    for (const item of src.kpis) {
      if (!isRecord(item)) continue;
      const label = asString(item.label ?? item.name ?? item.title);
      const rawVal = item.value;
      const value =
        typeof rawVal === "string"
          ? rawVal.trim()
          : typeof rawVal === "number" && Number.isFinite(rawVal)
            ? formatInt(rawVal)
            : "";
      if (!label || !value) continue;
      const rawTone = asString(item.tone).toLowerCase();
      const tone =
        rawTone === "pos" || rawTone === "neg" || rawTone === "warn" ? rawTone : "neutral";
      kpis.push({
        label,
        value,
        caption: asString(item.caption ?? item.sub_label ?? item.delta) || undefined,
        tone,
      });
    }
  }

  const rows: SellerReportRow[] = [];
  const metrics = isRecord(src.metrics) ? src.metrics : null;
  if (metrics) {
    if (kpis.length === 0) {
      const grossRevenue = asInt(metrics.gross_revenue_vnd ?? metrics.grossRevenueVnd);
      const totalOrders = asInt(metrics.total_orders ?? metrics.totalOrders);
      const completedOrders = asInt(metrics.completed_orders ?? metrics.completedOrders);
      const cancelledOrders = asInt(metrics.cancelled_orders ?? metrics.cancelledOrders);
      const cancelRatePct = asNumber(metrics.cancel_rate_pct ?? metrics.cancelRatePct);
      const availableBal = asInt(metrics.available_balance_vnd ?? metrics.availableBalanceVnd);

      if (typeof grossRevenue === "number") {
        kpis.push({ label: "Doanh thu gộp", value: formatVnd(grossRevenue), tone: "pos" });
      }
      if (typeof totalOrders === "number") {
        kpis.push({
          label: "Tổng số đơn",
          value: formatInt(totalOrders),
          caption:
            typeof completedOrders === "number"
              ? `${formatInt(completedOrders)} đơn hoàn thành`
              : undefined,
        });
      }
      if (typeof cancelRatePct === "number") {
        kpis.push({
          label: "Tỷ lệ hủy đơn",
          value: formatPercent(cancelRatePct, 1, false),
          caption:
            typeof cancelledOrders === "number"
              ? `${formatInt(cancelledOrders)} đơn đã hủy`
              : undefined,
          tone: cancelRatePct >= 10 ? "neg" : cancelRatePct >= 5 ? "warn" : "neutral",
        });
      }
      if (typeof availableBal === "number") {
        kpis.push({ label: "Số dư khả dụng", value: formatVnd(availableBal) });
      }
    }

    const topProducts = Array.isArray(metrics.top_products)
      ? metrics.top_products
      : Array.isArray(metrics.topProducts)
        ? metrics.topProducts
        : [];
    for (const item of topProducts) {
      if (!isRecord(item)) continue;
      const label = asString(item.product_name ?? item.productName ?? item.name);
      if (!label) continue;
      rows.push({
        label,
        subLabel: asString(item.product_id ?? item.productId ?? item.sku_code) || undefined,
        unitsSold: asInt(item.units_sold ?? item.unitsSold ?? item.quantity),
        revenueVnd: asInt(item.revenue_vnd ?? item.revenueVnd),
      });
    }
  }

  let tableColumns: string[] | undefined;
  let genericRows: Array<Record<string, string | number>> | undefined;
  if (rows.length === 0 && Array.isArray(src.rows) && src.rows.length > 0) {
    const parsedGeneric: Array<Record<string, string | number>> = [];
    const colSet = new Set<string>();
    for (const row of src.rows) {
      if (!isRecord(row)) continue;
      const label = asString(row.product_name ?? row.name ?? row.label ?? row.sku_code);
      const units = asInt(row.units_sold ?? row.quantity ?? row.orders);
      const rev = asInt(row.revenue_vnd ?? row.amount_vnd ?? row.revenue);
      if (label && (typeof units === "number" || typeof rev === "number")) {
        rows.push({
          label,
          subLabel: asString(row.sku_code ?? row.category ?? row.sub_label) || undefined,
          unitsSold: units,
          revenueVnd: rev,
          extra: asString(row.status ?? row.note) || undefined,
        });
      } else {
        const cleanObj: Record<string, string | number> = {};
        for (const [k, v] of Object.entries(row)) {
          if (typeof v === "string" || (typeof v === "number" && Number.isFinite(v))) {
            cleanObj[k] = v;
            colSet.add(k);
          }
        }
        if (Object.keys(cleanObj).length > 0) parsedGeneric.push(cleanObj);
      }
    }
    if (rows.length === 0 && parsedGeneric.length > 0) {
      tableColumns = Array.from(colSet);
      genericRows = parsedGeneric;
    }
  }

  return {
    period,
    question: fallback.question,
    answer:
      answer ||
      "Chưa có nội dung phân tích chi tiết cho câu hỏi này. Bạn hãy thử đặt câu hỏi cụ thể hơn về doanh thu, đơn hàng hoặc tồn kho.",
    kpis,
    rows,
    tableColumns,
    genericRows,
    generatedAt,
  };
}

// ---- Seller describe-product & suggest-reply normalizers ----

export function normalizeDescribeProduct(raw: unknown): DescribeProductResult {
  const src = isRecord(raw) && isRecord(raw.result) ? raw.result : isRecord(raw) ? raw : {};
  const titleSuggestion =
    asString(src.title_suggestion ?? src.titleSuggestion ?? src.title) || undefined;
  const description = asString(src.description ?? src.content ?? src.text);
  const highlights = asStringList(src.highlights ?? src.bullets ?? src.key_points, 10);
  const aiGenerated =
    typeof src.ai_generated === "boolean" ? src.ai_generated : src.aiGenerated !== false;

  return {
    titleSuggestion,
    description,
    highlights,
    aiGenerated,
  };
}

export function normalizeSuggestReply(raw: unknown): SuggestReplyResult {
  const src = isRecord(raw) ? raw : {};
  const list = Array.isArray(src.suggestions)
    ? src.suggestions
    : Array.isArray(src.replies)
      ? src.replies
      : [];
  const seen = new Set<string>();
  const suggestions: string[] = [];
  for (const item of list) {
    let text = "";
    if (typeof item === "string") {
      text = item.trim();
    } else if (isRecord(item)) {
      text = asString(item.text ?? item.content ?? item.body ?? item.reply);
    }
    if (!text || seen.has(text)) continue;
    seen.add(text);
    suggestions.push(text);
    if (suggestions.length >= 5) break;
  }
  return { suggestions };
}

// ---- Pure request validators (used by BFF routes & unit-tested directly) ----

export const MAX_CHAT_MESSAGES = 30;
export const MAX_CHAT_MESSAGE_LENGTH = 2000;

export function validateAssistantMessages(
  raw: unknown
): { ok: true; messages: AssistantChatInputMessage[] } | { ok: false; reason: string } {
  if (!Array.isArray(raw) || raw.length === 0) {
    return { ok: false, reason: "Vui lòng nhập nội dung câu hỏi." };
  }
  if (raw.length > MAX_CHAT_MESSAGES) {
    return { ok: false, reason: `Hội thoại tối đa ${MAX_CHAT_MESSAGES} lượt tin nhắn.` };
  }
  const messages: AssistantChatInputMessage[] = [];
  for (const item of raw) {
    if (!isRecord(item)) {
      return { ok: false, reason: "Định dạng tin nhắn không hợp lệ." };
    }
    const role = item.role;
    if (role !== "user" && role !== "assistant") {
      return { ok: false, reason: "Vai trò tin nhắn không hợp lệ." };
    }
    if (typeof item.content !== "string") {
      return { ok: false, reason: "Nội dung tin nhắn không hợp lệ." };
    }
    const content = item.content.trim();
    if (!content) {
      return { ok: false, reason: "Nội dung tin nhắn không được để trống." };
    }
    if (content.length > MAX_CHAT_MESSAGE_LENGTH) {
      return {
        ok: false,
        reason: `Mỗi tin nhắn tối đa ${MAX_CHAT_MESSAGE_LENGTH} ký tự.`,
      };
    }
    messages.push({ role, content });
  }
  return { ok: true, messages };
}

export interface ValidatedDescribeProductInput {
  name: string;
  category_name: string;
  attributes: Record<string, string>;
  colors: string[];
  sizes: string[];
  price_vnd: number;
  tone: string;
}

export function validateDescribeProductInput(
  raw: unknown
): { ok: true; data: ValidatedDescribeProductInput } | { ok: false; reason: string } {
  if (!isRecord(raw)) {
    return { ok: false, reason: "Dữ liệu sản phẩm không hợp lệ." };
  }
  const name = asString(raw.name);
  if (name.length < 3) {
    return { ok: false, reason: "Tên sản phẩm cần tối thiểu 3 ký tự để AI tạo mô tả." };
  }
  if (name.length > 200) {
    return { ok: false, reason: "Tên sản phẩm tối đa 200 ký tự." };
  }
  const categoryName = asString(raw.category_name ?? raw.category).slice(0, 120);
  const colors = asStringList(raw.colors, 20);
  const sizes = asStringList(raw.sizes, 20);
  const priceVnd = Math.max(0, asInt(raw.price_vnd) ?? 0);
  const tone = asString(raw.tone).slice(0, 40) || "practical";

  const attributes: Record<string, string> = {};
  if (isRecord(raw.attributes)) {
    let count = 0;
    for (const [k, v] of Object.entries(raw.attributes)) {
      const cleanKey = k.trim().slice(0, 60);
      const cleanVal = asString(v).slice(0, 200);
      if (cleanKey && cleanVal) {
        attributes[cleanKey] = cleanVal;
        count++;
        if (count >= 20) break;
      }
    }
  }
  if (colors.length > 0 && !attributes.colors) {
    attributes.colors = colors.join(", ");
  }
  if (sizes.length > 0 && !attributes.sizes) {
    attributes.sizes = sizes.join(", ");
  }

  return {
    ok: true,
    data: {
      name,
      category_name: categoryName,
      attributes,
      colors,
      sizes,
      price_vnd: priceVnd,
      tone,
    },
  };
}

export interface ValidatedSellerReportInput {
  question: string;
  period: SellerReportPeriod;
}

export function validateSellerReportInput(
  raw: unknown
): { ok: true; data: ValidatedSellerReportInput } | { ok: false; reason: string } {
  if (!isRecord(raw)) {
    return { ok: false, reason: "Vui lòng nhập câu hỏi cần phân tích." };
  }
  const question = asString(raw.question);
  if (question.length < 3) {
    return { ok: false, reason: "Câu hỏi cần tối thiểu 3 ký tự." };
  }
  if (question.length > 500) {
    return { ok: false, reason: "Câu hỏi tối đa 500 ký tự." };
  }
  const period = normalizeSellerPeriod(raw.period);
  return { ok: true, data: { question, period } };
}
