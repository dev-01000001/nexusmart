import type {
  GatewayBannedContentProductSample,
  GatewayRescanProductsResponse,
} from "@/lib/gateway-types";

export const RESCAN_BATCH_LIMIT = 500;
export const MAX_RESCAN_SAMPLES = 20;
const MAX_RESCAN_BATCHES = 200;

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
