import React from "react";
import { requireSellerPage } from "@/features/seller/sellerServer";
import SellerProfileClient from "./SellerProfileClient";

export const dynamic = "force-dynamic";

export default async function SellerProfileOpsPage() {
  await requireSellerPage();
  return <SellerProfileClient />;
}
