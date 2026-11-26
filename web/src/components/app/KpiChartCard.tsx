"use client";

import * as React from "react";
import { Info } from "lucide-react";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export interface KpiChartCardProps {
  title: string;
  tooltip?: string;
  value: React.ReactNode;
  chart?: React.ReactNode;
  footer?: React.ReactNode;
  className?: string;
}

export function KpiChartCard({
  title,
  tooltip,
  value,
  chart,
  footer,
  className,
}: KpiChartCardProps) {
  return (
    <Card className={cn("gap-3", className)}>
      <CardHeader className="gap-1">
        <div className="flex items-center justify-between gap-2">
          {tooltip ? (
            <TooltipProvider delayDuration={150}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    className="inline-flex min-w-0 items-center gap-1.5 text-left text-[13px] font-medium text-foreground underline decoration-border decoration-dotted underline-offset-4 hover:decoration-muted-foreground"
                  >
                    <CardTitle className="truncate text-[13px] font-medium">{title}</CardTitle>
                    <Info className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  </button>
                </TooltipTrigger>
                <TooltipContent side="top" className="max-w-xs">
                  {tooltip}
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          ) : (
            <CardTitle className="truncate text-[13px] font-medium">{title}</CardTitle>
          )}
        </div>
        <div className="text-2xl leading-8 font-semibold tracking-tight text-foreground tabular-nums">
          {value}
        </div>
      </CardHeader>
      {chart ? <CardContent className="pt-0">{chart}</CardContent> : null}
      {footer ? (
        <CardFooter className="border-t border-border pt-3 text-xs text-muted-foreground">
          {footer}
        </CardFooter>
      ) : null}
    </Card>
  );
}
