import React from "react";
import { PageTransition } from "@/components/PageTransition";

export default function ShopTemplate({ children }: { children: React.ReactNode }) {
  return <PageTransition>{children}</PageTransition>;
}
