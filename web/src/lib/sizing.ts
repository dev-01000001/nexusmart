import type { SizeCode } from "../data/products";
import { getShopById, type ShopSizeRow } from "../data/shops";
import { PLATFORM_FREESHIP_THRESHOLD } from "../data/vouchers";
import { getProvinceByCode, getShippingFeeForProvince } from "./provinces";

export type BodyShape = "gay" | "vua" | "day";
export type FitPreference = "om" | "vua" | "rong";

export interface BodyProfile {
  heightCm: number;
  weightKg: number;
  shape: BodyShape;
  preferredFit?: FitPreference;
}

export const DEFAULT_BODY_PROFILE: BodyProfile = {
  heightCm: 173,
  weightKg: 68,
  shape: "vua",
  preferredFit: "vua",
};

export const BODY_SHAPE_LABELS: Record<BodyShape, { label: string; hint: string }> = {
  gay: { label: "Gầy / Thanh mảnh", hint: "Vai gọn, thân người nhỏ hơn mức cân nặng trung bình." },
  vua: { label: "Cân đối / Vừa", hint: "Tỷ lệ vai, ngực và vòng eo hài hòa theo chiều cao." },
  day: { label: "Đầy đặn / Vai rộng", hint: "Ngực, vai hoặc vòng bụng đầy hơn mức trung bình." },
};

export interface SizeRecommendation {
  size: SizeCode;
  alternativeSize?: SizeCode;
  confidenceLabel: string;
  reason: string;
  shopName: string;
  shopSizeNote: string;
  measurements: ShopSizeRow | null;
}

const ORDERED_SIZES: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

function clamp(val: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, val));
}

/**
 * Recommends a garment size using height, weight, 3 body shapes (gay/vua/day),
 * fit preference, and the specific shop's size chart & fit bias.
 */
export function recommendSize(params: {
  heightCm: number;
  weightKg: number;
  shape: BodyShape;
  preferredFit?: FitPreference;
  garmentGroup: "top" | "bottom" | "jogger" | "underwear" | "sock";
  shopId?: string;
  availableSizes?: SizeCode[];
  shoeSizeEu?: number;
}): SizeRecommendation {
  const {
    heightCm,
    weightKg,
    shape,
    preferredFit = "vua",
    garmentGroup,
    shopId,
    availableSizes = ORDERED_SIZES,
    shoeSizeEu,
  } = params;

  const shop = shopId ? getShopById(shopId) : undefined;
  const shopName = shop?.name ?? "Chuẩn sàn NexusMart";
  const shopSizeNote =
    shop?.sizeCharts?.sizeNote ??
    "Thông số tiêu chuẩn dựa trên rập cơ bản cho vóc dáng người Việt Nam.";

  if (garmentGroup === "sock") {
    const inferredShoe = shoeSizeEu ?? (heightCm >= 170 ? 42 : 40);
    const chosen: SizeCode = inferredShoe >= 42 ? "L" : "M";
    return {
      size: chosen,
      confidenceLabel: "Độ khớp cao",
      reason: `Dựa theo cỡ giày ước tính ${inferredShoe} EU (cỡ M cho giày 38-41, cỡ L cho giày 42-45).`,
      shopName,
      shopSizeNote,
      measurements: null,
    };
  }

  const rows: ShopSizeRow[] =
    garmentGroup === "top" ? (shop?.sizeCharts?.top ?? []) : (shop?.sizeCharts?.bottom ?? []);

  // Baseline continuous index 0 (S) .. 4 (2XL)
  let baseIndex = 2; // L
  if (rows.length > 0) {
    let bestRowIdx = 0;
    let bestPenalty = Number.POSITIVE_INFINITY;
    rows.forEach((row, idx) => {
      const hMid = (row.heightMin + row.heightMax) / 2;
      const wMid = (row.weightMin + row.weightMax) / 2;
      const hOut =
        heightCm < row.heightMin
          ? row.heightMin - heightCm
          : heightCm > row.heightMax
            ? heightCm - row.heightMax
            : 0;
      const wOut =
        weightKg < row.weightMin
          ? row.weightMin - weightKg
          : weightKg > row.weightMax
            ? weightKg - row.weightMax
            : 0;
      const penalty =
        hOut * 1.4 +
        wOut * 2.2 +
        Math.abs(heightCm - hMid) * 0.25 +
        Math.abs(weightKg - wMid) * 0.6;
      if (penalty < bestPenalty) {
        bestPenalty = penalty;
        bestRowIdx = idx;
      }
    });
    baseIndex = bestRowIdx;
  } else {
    const hScore = (heightCm - 158) / 6.5;
    const wScore = (weightKg - 52) / 8.5;
    baseIndex = hScore * 0.4 + wScore * 0.6;
  }

  // Body shape adjustment (3 shapes: gay / vua / day)
  const shapeDelta = shape === "gay" ? -0.35 : shape === "day" ? 0.55 : 0;

  // Shop fit bias adjustment
  const fitBias = shop?.sizeCharts?.fitBias ?? "regular";
  const biasDelta = fitBias === "slim" ? 0.35 : fitBias === "relaxed" ? -0.25 : 0;

  // Fit preference adjustment
  const prefDelta = preferredFit === "om" ? -0.45 : preferredFit === "rong" ? 0.55 : 0;

  const adjusted = baseIndex + shapeDelta + biasDelta + prefDelta;
  const roundedIdx = clamp(Math.round(adjusted), 0, ORDERED_SIZES.length - 1);
  let chosenSize = ORDERED_SIZES[roundedIdx];

  if (availableSizes.length > 0 && !availableSizes.includes(chosenSize)) {
    chosenSize = availableSizes[Math.min(roundedIdx, availableSizes.length - 1)];
  }

  const frac = adjusted - roundedIdx;
  let alternativeSize: SizeCode | undefined;
  if (frac > 0.2 && roundedIdx < ORDERED_SIZES.length - 1) {
    alternativeSize = ORDERED_SIZES[roundedIdx + 1];
  } else if (frac < -0.2 && roundedIdx > 0) {
    alternativeSize = ORDERED_SIZES[roundedIdx - 1];
  }

  const matchedRow = rows.find((r) => r.size === chosenSize) ?? null;

  const shapeLabel = BODY_SHAPE_LABELS[shape].label.toLowerCase();
  const biasDesc =
    fitBias === "slim"
      ? "form ôm gọn của xưởng"
      : fitBias === "relaxed"
        ? "form thoáng của xưởng"
        : "form tiêu chuẩn của xưởng";

  return {
    size: chosenSize,
    alternativeSize,
    confidenceLabel: Math.abs(frac) <= 0.22 ? "Rất phù hợp" : "Khớp tốt (có thể cân nhắc 2 cỡ)",
    reason: `Với ${heightCm} cm · ${weightKg} kg · dáng ${shapeLabel} theo ${biasDesc} ${shopName}, cỡ ${chosenSize} là lựa chọn vừa vặn nhất.${alternativeSize ? ` Nếu phân vân có thể thử thêm cỡ ${alternativeSize}.` : ""}`,
    shopName,
    shopSizeNote,
    measurements: matchedRow,
  };
}

export interface DeliveryEstimate {
  shopId: string;
  shopName: string;
  originCity: string;
  destinationProvinceName: string;
  carrier: "SPX" | "GHN" | "NexusFast";
  etaLabel: string;
  standardFeeVnd: number;
  fastAvailable: boolean;
  fastFeeVnd: number;
  fastEtaLabel: string;
  shopFreeShipThreshold: number;
  platformFreeShipThreshold: number;
  isShopFreeShipReached: boolean;
  isPlatformFreeShipReached: boolean;
  coInspectionNote: string;
}

/**
 * Estimates shipping fee, carrier, SLA ETA, and freeship eligibility from a shop warehouse
 * to any of the 34 Vietnamese provinces (2-level administrative model).
 */
export function estimateDelivery(params: {
  shopId: string;
  provinceCode: string;
  orderSubtotal?: number;
  deliveryMethod?: "standard" | "fast";
}): DeliveryEstimate {
  const { shopId, provinceCode, orderSubtotal = 0, deliveryMethod = "standard" } = params;
  const shop = getShopById(shopId);
  const dest = getProvinceByCode(provinceCode) ?? getProvinceByCode("HCM")!;
  const origin = shop ? getProvinceByCode(shop.provinceCode) : undefined;

  const sameProvince = Boolean(origin && origin.code === dest.code);
  const sameRegion = Boolean(origin && origin.region === dest.region);

  const baseFee = getShippingFeeForProvince(dest.code, 1);
  const standardFeeVnd = sameProvince ? Math.max(18000, baseFee - 4000) : baseFee;

  let etaLabel = dest.etaDays;
  if (sameProvince) {
    etaLabel = "1 ngày làm việc";
  } else if (sameRegion) {
    etaLabel = "1-2 ngày làm việc";
  } else {
    etaLabel = "2-4 ngày làm việc";
  }

  const fastAvailable = Boolean(shop?.fastDeliveryEnabled && sameProvince);
  const fastFeeVnd = shop?.fastDeliveryFee ?? 39000;
  const fastEtaLabel = fastAvailable
    ? (shop?.fastDeliveryNote ?? "Giao nhanh nội thành 2-4 giờ")
    : "Giao nhanh chỉ áp dụng nội thành tại tỉnh kho của gian hàng";

  const carrier: "SPX" | "GHN" | "NexusFast" =
    deliveryMethod === "fast" && fastAvailable
      ? "NexusFast"
      : dest.region === "bac"
        ? "GHN"
        : "SPX";

  const shopFreeShipThreshold = shop?.freeShipThreshold ?? 199000;

  return {
    shopId,
    shopName: shop?.name ?? "Gian hàng NexusMart",
    originCity: shop?.city ?? "Thành phố Hồ Chí Minh",
    destinationProvinceName: dest.name,
    carrier,
    etaLabel: deliveryMethod === "fast" && fastAvailable ? "2-4 giờ hôm nay" : etaLabel,
    standardFeeVnd,
    fastAvailable,
    fastFeeVnd,
    fastEtaLabel,
    shopFreeShipThreshold,
    platformFreeShipThreshold: PLATFORM_FREESHIP_THRESHOLD,
    isShopFreeShipReached: orderSubtotal >= shopFreeShipThreshold,
    isPlatformFreeShipReached: orderSubtotal >= PLATFORM_FREESHIP_THRESHOLD,
    coInspectionNote: "Hỗ trợ đồng kiểm ngoại quan khi nhận hàng.",
  };
}
