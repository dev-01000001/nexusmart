import React from "react";

export function PageTransition({
  children,
  fast = false,
}: {
  children: React.ReactNode;
  fast?: boolean;
}) {
  return (
    <div className={fast ? "nm-page-enter nm-page-enter-fast" : "nm-page-enter"}>{children}</div>
  );
}
