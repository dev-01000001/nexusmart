import React from "react";
import { requireSellerPage } from "@/features/seller/sellerServer";
import SellerInteractionClient from "./SellerInteractionClient";

export const dynamic = "force-dynamic";

export default async function SellerInteractionPage() {
  await requireSellerPage();
  return <SellerInteractionClient />;
}
