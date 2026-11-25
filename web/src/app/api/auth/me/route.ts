import { cookies } from "next/headers";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/server/config";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { type GatewayUser, assertSession, getSession, toSessionUser } from "@/lib/server/session";
import { clearAuthCookies } from "@/lib/server/tokens";

interface UpdateMeInput {
  fullName?: string;
  full_name?: string;
  phone?: string;
  avatarUrl?: string;
  avatar_url?: string;
}

export const GET = apiRoute(async () => {
  const store = await cookies();
  const hadCookies = Boolean(store.get(ACCESS_COOKIE)?.value || store.get(REFRESH_COOKIE)?.value);
  const session = await getSession();
  if (!session && hadCookies) {
    clearAuthCookies(store);
  }
  return Response.json(
    { user: session?.user ?? null },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const PATCH = apiRoute(
  async (req) => {
    await assertSession();
    const body = await readJson<UpdateMeInput>(req);
    const payload = {
      full_name: (body.fullName ?? body.full_name ?? "").trim(),
      phone: (body.phone ?? "").trim(),
      avatar_url: (body.avatarUrl ?? body.avatar_url ?? "").trim(),
    };

    const res = await gw<{ user: GatewayUser }>("/api/v1/users/me", {
      method: "PATCH",
      body: payload,
    });

    return Response.json(
      { user: toSessionUser(res.user) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      ALREADY_EXISTS: "Số điện thoại này đã được sử dụng cho tài khoản khác.",
      INVALID_ARGUMENT: "Thông tin cập nhật chưa hợp lệ. Bạn vui lòng kiểm tra lại.",
    },
  }
);
