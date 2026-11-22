import React from "react";
import { requireRole } from "@/lib/server/auth";
import { SellerShell } from "./SellerShell";

export default async function SellerLayout({ children }: { children: React.ReactNode }) {
  const user = await requireRole(["seller"], "/seller");
  return <SellerShell user={user}>{children}</SellerShell>;
}
