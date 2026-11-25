import React from "react";
import { PageTransition } from "@/components/PageTransition";

export default function AdminTemplate({ children }: { children: React.ReactNode }) {
  return <PageTransition fast>{children}</PageTransition>;
}
