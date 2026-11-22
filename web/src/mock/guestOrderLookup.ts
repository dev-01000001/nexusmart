// Dữ liệu và logic tra cứu đơn vãng lai mẫu (ngoài phạm vi backend 0b — cần chống dò mã đơn).

import { DEMO_USER, INITIAL_ORDERS } from "@/data";
import type { Order } from "@/data";

export interface GuestLookupSample {
  code: string;
  phone: string;
  hint: string;
}

export const GUEST_LOOKUP_DEFAULT_PHONE = DEMO_USER.phone;

export const GUEST_LOOKUP_SAMPLES: GuestLookupSample[] = [
  {
    code: "NM-261120-109",
    phone: DEMO_USER.phone,
    hint: "Chờ thanh toán · Cần hoàn tất",
  },
  {
    code: "NM-261120-104",
    phone: DEMO_USER.phone,
    hint: "Chờ xác nhận · Có thể hủy đơn",
  },
  {
    code: "NM-261117-082",
    phone: DEMO_USER.phone,
    hint: "Đang giao hàng · Vận đơn SPX",
  },
  {
    code: "NM-261112-061",
    phone: DEMO_USER.phone,
    hint: "Đã giao · Đổi trả 45 ngày",
  },
];

export function createInitialGuestLookupOrders(): Order[] {
  return INITIAL_ORDERS.map((order) => ({
    ...order,
    items: order.items.map((item) => ({ ...item })),
    timeline: order.timeline.map((ev) => ({ ...ev })),
  }));
}

function digitsOnly(phone: string): string {
  return phone.replace(/\D/g, "");
}

export function lookupGuestSampleOrder(
  orders: Order[],
  code: string,
  phone: string
): Order | undefined {
  const cleanCode = code.trim().toUpperCase();
  const cleanPhone = digitsOnly(phone);
  if (!cleanCode || cleanPhone.length < 8) return undefined;
  return orders.find(
    (o) =>
      (o.orderCode.toUpperCase() === cleanCode || o.parentOrderCode?.toUpperCase() === cleanCode) &&
      digitsOnly(o.buyerPhone).endsWith(cleanPhone.slice(-8))
  );
}

export function cancelGuestSampleOrder(orders: Order[], orderId: string, reason: string): Order[] {
  const nowIso = new Date().toISOString();
  return orders.map((o) =>
    o.id === orderId
      ? {
          ...o,
          status: "da_huy",
          paymentStatus: o.isPaid ? "da_hoan_tien" : o.paymentStatus,
          cancelReason: reason,
          updatedAt: nowIso,
          timeline: [
            ...o.timeline,
            {
              timestamp: nowIso,
              status: "da_huy",
              title: "Đơn hàng đã hủy",
              note: o.isPaid
                ? `${reason} (Hoàn tiền tự động trong 24 giờ qua kênh thanh toán gốc).`
                : reason,
            },
          ],
        }
      : o
  );
}

export function requestEditGuestSampleAddress(
  orders: Order[],
  orderId: string,
  address: string,
  reason = "Cập nhật địa chỉ nhận hàng"
): Order[] {
  const cleanAddr = address.trim();
  if (!cleanAddr) return orders;
  const nowIso = new Date().toISOString();
  return orders.map((o) =>
    o.id === orderId
      ? {
          ...o,
          shippingAddress: o.status === "cho_xac_nhan" ? cleanAddr : o.shippingAddress,
          addressEditRequest: {
            requestedAddress: cleanAddr,
            reason,
            status: o.status === "cho_xac_nhan" ? "da_cap_nhat" : "cho_shop_duyet",
            requestedAt: nowIso,
          },
          updatedAt: nowIso,
          timeline: [
            ...o.timeline,
            {
              timestamp: nowIso,
              status: o.status,
              title:
                o.status === "cho_xac_nhan"
                  ? "Đã cập nhật địa chỉ nhận hàng"
                  : "Gửi yêu cầu đổi địa chỉ tới gian hàng",
              note: `Địa chỉ mới: ${cleanAddr}`,
            },
          ],
        }
      : o
  );
}
