import React from "react";

export default function SellerTemplate({ children }: { children: React.ReactNode }) {
  return (
    <div className="animate-in fade-in duration-150 motion-reduce:animate-none">{children}</div>
  );
}
