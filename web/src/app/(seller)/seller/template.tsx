import React from "react";
import { PageTransition } from "@/components/PageTransition";

export default function SellerTemplate({ children }: { children: React.ReactNode }) {
  return <PageTransition fast>{children}</PageTransition>;
}
