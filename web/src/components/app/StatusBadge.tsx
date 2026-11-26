import * as React from "react";
import type { LucideIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { StatusTone } from "./statusTone";

export interface StatusBadgeProps {
  tone: StatusTone;
  children: React.ReactNode;
  icon?: LucideIcon;
  className?: string;
}

export function StatusBadge({ tone, children, icon: Icon, className }: StatusBadgeProps) {
  return (
    <Badge variant={tone} className={cn("leading-4 font-medium", className)}>
      {Icon ? <Icon className="size-3 shrink-0" aria-hidden="true" /> : null}
      <span>{children}</span>
    </Badge>
  );
}
