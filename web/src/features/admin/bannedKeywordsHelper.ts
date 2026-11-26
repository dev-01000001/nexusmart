import type {
  GatewayBannedContentProductSample,
  GatewayRescanProductsResponse,
} from "../../lib/gateway-types.ts";
import type { ValidationResult } from "./adminComplaintsTypes.ts";
import { isValidAdminPathId } from "./AdminReturnsHelpers.ts";

export const RESCAN_BATCH_LIMIT = 500;
export const MAX_RESCAN_SAMPLES = 20;
const MAX_RESCAN_BATCHES = 200;

export interface ValidatedRescanInput {
  keyword_ids: string[];
  dry_run: boolean;
  cursor: string;
  limit: number;
}

export function validateRescanBannedKeywordsInput(
  input: unknown
): ValidationResult<ValidatedRescanInput> {
  const rec =
    input && typeof input === "object" && !Array.isArray(input)
      ? (input as Record<string, unknown>)
      : {};

  const keywordIds: string[] = [];
  if (rec.keyword_ids !== undefined && rec.keyword_ids !== null) {
    if (!Array.isArray(rec.keyword_ids) || rec.keyword_ids.length > 100) {
      return {
        ok: false,
        code: "INVALID_KEYWORD_IDS",
        message: "Danh sách mã từ khoá cấm không hợp lệ.",
      };
    }
    for (const item of rec.keyword_ids) {
      if (typeof item !== "string" || !isValidAdminPathId(item)) {
        return {
          ok: false,
          code: "INVALID_KEYWORD_IDS",
          message: "Danh sách mã từ khoá cấm không hợp lệ.",
        };
      }
      keywordIds.push(item.trim());
    }
  }

  if (typeof rec.dry_run !== "boolean") {
    return {
      ok: false,
      code: "INVALID_ARGUMENT",
      message: "Chế độ chạy thử quét lại phải là giá trị đúng/sai hợp lệ.",
    };
  }

  const cursor = typeof rec.cursor === "string" ? rec.cursor.trim().slice(0, 256) : "";
  let limit = RESCAN_BATCH_LIMIT;
  if (rec.limit !== undefined && rec.limit !== null) {
    if (typeof rec.limit !== "number" || !Number.isInteger(rec.limit) || rec.limit < 1) {
      return {
        ok: false,
        code: "INVALID_LIMIT",
        message: "Kích thước lô quét không hợp lệ.",
      };
    }
    limit = Math.min(RESCAN_BATCH_LIMIT, rec.limit);
  }

  return {
    ok: true,
    value: {
      keyword_ids: keywordIds,
      dry_run: rec.dry_run,
      cursor,
      limit,
    },
  };
}

export interface RescanBatchRequest {
  keyword_ids?: string[];
  dry_run: boolean;
  cursor: string;
  limit: number;
}

export interface RescanAggregateProgress {
  batches: number;
  scannedCount: number;
  blockedCount: number;
  flaggedCount: number;
  hasMore: boolean;
  aborted: boolean;
  samples: GatewayBannedContentProductSample[];
}

export class RescanBatchInterruptedError extends Error {
  readonly failedBatch: number;
  readonly partialProgress: RescanAggregateProgress;
  readonly cause: unknown;

  constructor(failedBatch: number, partialProgress: RescanAggregateProgress, cause: unknown) {
    super(`Rescan interrupted at batch ${failedBatch}`);
    this.name = "RescanBatchInterruptedError";
    this.failedBatch = failedBatch;
    this.partialProgress = partialProgress;
    this.cause = cause;
  }
}

/**
 * Loops `POST /admin/banned-keywords/rescan` in cursor batches of 500 products (E11)
 * until `has_more` is false or `signal` is aborted, aggregating counts and capping samples at 20.
 */
export async function runBannedKeywordRescanBatches(
  invokeBatch: (
    req: RescanBatchRequest,
    signal?: AbortSignal
  ) => Promise<GatewayRescanProductsResponse>,
  options: { keywordIds?: string[]; dryRun?: boolean; signal?: AbortSignal } = {},
  onProgress?: (progress: RescanAggregateProgress) => void
): Promise<RescanAggregateProgress> {
  let cursor = "";
  let batches = 0;
  let scannedCount = 0;
  let blockedCount = 0;
  let flaggedCount = 0;
  let hasMore = true;
  let aborted = false;
  const seenSampleIds = new Set<string>();
  const samples: GatewayBannedContentProductSample[] = [];

  const snapshot = (): RescanAggregateProgress => ({
    batches,
    scannedCount,
    blockedCount,
    flaggedCount,
    hasMore,
    aborted,
    samples: [...samples],
  });

  while (hasMore && batches < MAX_RESCAN_BATCHES) {
    if (options.signal?.aborted) {
      aborted = true;
      break;
    }

    const nextBatchNo = batches + 1;
    let res: GatewayRescanProductsResponse;
    try {
      res = await invokeBatch(
        {
          ...(options.keywordIds && options.keywordIds.length > 0
            ? { keyword_ids: options.keywordIds }
            : {}),
          dry_run: Boolean(options.dryRun),
          cursor,
          limit: RESCAN_BATCH_LIMIT,
        },
        options.signal
      );
    } catch (err) {
      if (options.signal?.aborted) {
        aborted = true;
        break;
      }
      throw new RescanBatchInterruptedError(nextBatchNo, snapshot(), err);
    }

    batches = nextBatchNo;
    scannedCount += Math.max(0, Math.trunc(res.scanned_count ?? 0));
    blockedCount += Math.max(0, Math.trunc(res.blocked_count ?? 0));
    flaggedCount += Math.max(0, Math.trunc(res.flagged_count ?? 0));

    for (const sample of res.samples ?? []) {
      if (samples.length >= MAX_RESCAN_SAMPLES) break;
      if (!sample?.product_id || seenSampleIds.has(sample.product_id)) continue;
      seenSampleIds.add(sample.product_id);
      samples.push(sample);
    }

    const nextCursor = (res.next_cursor ?? "").trim();
    hasMore = Boolean(res.has_more) && Boolean(nextCursor) && nextCursor !== cursor;
    cursor = nextCursor;

    if (options.signal?.aborted) {
      aborted = true;
      onProgress?.(snapshot());
      break;
    }

    onProgress?.(snapshot());
  }

  return snapshot();
}
