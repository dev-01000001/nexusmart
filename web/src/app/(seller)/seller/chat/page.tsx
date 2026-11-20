import React from "react";
import { requireSellerPage } from "@/features/seller/sellerServer";
import { SellerChatClient } from "./SellerChatClient";

export const dynamic = "force-dynamic";

export default async function SellerChatPage() {
  await requireSellerPage();
  return <SellerChatClient />;
}
