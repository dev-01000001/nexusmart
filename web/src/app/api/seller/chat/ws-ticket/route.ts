import { buildChatWsUrl } from "@/lib/server/config";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface RawWsTicketResponse {
  ticket?: string;
  expires_at?: string;
}

export const POST = apiRoute(async () => {
  await assertSellerSession();

  const res = await gw<RawWsTicketResponse>("/api/v1/chat/ws-ticket", {
    method: "POST",
  });

  const ticket = typeof res.ticket === "string" ? res.ticket.trim() : "";
  if (!ticket) {
    throw new GatewayError("unavailable");
  }

  return Response.json(
    {
      ticket,
      ws_url: buildChatWsUrl(ticket),
      expires_at: typeof res.expires_at === "string" ? res.expires_at : "",
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
