"use client";

import * as React from "react";
import { CheckCircle2, ChevronDown, ChevronUp, CircleDashed, HelpCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import { StatusBadge } from "./StatusBadge";
import type { StatusTone } from "./statusTone";
import { setupProgress, type SetupStepState } from "./setupProgress";

export interface SetupGuideStep extends SetupStepState {
  title: string;
  description?: string;
  badge?: { tone: StatusTone; label: string };
  primary?: React.ReactNode;
  secondary?: React.ReactNode;
  image?: { src: string; alt: string };
}

export interface SetupGuideProps {
  title: string;
  steps: SetupGuideStep[];
  storageKey: string;
  className?: string;
}

export function SetupGuide({ title, steps, storageKey, className }: SetupGuideProps) {
  const summary = React.useMemo(() => setupProgress(steps), [steps]);
  const defaultOpenStepId = React.useMemo(() => {
    const firstPending = steps.find((s) => s.status !== "done");
    return firstPending?.id ?? null;
  }, [steps]);

  const [guideOpen, setGuideOpen] = React.useState(true);
  const [openStepId, setOpenStepId] = React.useState<string | null>(defaultOpenStepId);

  React.useEffect(() => {
    if (!storageKey) return;
    try {
      const saved = window.localStorage.getItem(storageKey);
      if (saved === "collapsed") setGuideOpen(false);
      else if (saved === "expanded") setGuideOpen(true);
    } catch {
      // Ignore storage read errors
    }
  }, [storageKey]);

  const handleGuideOpenChange = React.useCallback(
    (nextOpen: boolean) => {
      setGuideOpen(nextOpen);
      if (!storageKey) return;
      try {
        window.localStorage.setItem(storageKey, nextOpen ? "expanded" : "collapsed");
      } catch {
        // Ignore storage write errors
      }
    },
    [storageKey]
  );

  const percent = summary.total > 0 ? Math.round((summary.done / summary.total) * 100) : 0;

  return (
    <Card className={cn("gap-0 py-0", className)}>
      <Collapsible open={guideOpen} onOpenChange={handleGuideOpenChange}>
        <CardHeader className="gap-2 py-4 md:py-5">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1 space-y-1.5">
              <CardTitle className="text-sm font-semibold">{title}</CardTitle>
              {summary.complete ? (
                <div className="inline-flex items-center gap-1.5 text-[13px] font-medium text-badge-success-ink">
                  <CheckCircle2 className="size-4 shrink-0" aria-hidden="true" />
                  <span>✓ Đã thiết lập xong</span>
                </div>
              ) : (
                <div className="flex flex-wrap items-center gap-3 text-[13px] text-muted-foreground tabular-nums">
                  <span>
                    {summary.done} / {summary.total} hoàn thành
                  </span>
                  <Progress value={percent} className="h-1.5 w-32" aria-label="Tiến độ thiết lập" />
                </div>
              )}
            </div>
            <CollapsibleTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={
                  guideOpen ? "Thu gọn hướng dẫn thiết lập" : "Mở rộng hướng dẫn thiết lập"
                }
              >
                {guideOpen ? (
                  <ChevronUp className="size-4" aria-hidden="true" />
                ) : (
                  <ChevronDown className="size-4" aria-hidden="true" />
                )}
              </Button>
            </CollapsibleTrigger>
          </div>
        </CardHeader>

        <CollapsibleContent>
          <CardContent className="border-t border-border px-2 py-2 md:px-3 md:py-3">
            <div className="flex flex-col gap-1">
              {steps.map((step) => {
                const isOpen = openStepId === step.id;
                return (
                  <Collapsible
                    key={step.id}
                    open={isOpen}
                    onOpenChange={(next) => setOpenStepId(next ? step.id : null)}
                    className={cn(
                      "rounded-lg transition-colors",
                      isOpen ? "bg-muted/70" : "hover:bg-muted/40"
                    )}
                  >
                    <CollapsibleTrigger asChild>
                      <button
                        type="button"
                        className="flex w-full items-center gap-3 px-3 py-2.5 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      >
                        {step.status === "done" ? (
                          <CheckCircle2
                            className="size-5 shrink-0 text-brand"
                            aria-label="Đã hoàn thành"
                          />
                        ) : step.status === "unknown" ? (
                          <HelpCircle
                            className="size-5 shrink-0 text-muted-foreground"
                            aria-label="Chưa kiểm tra được"
                          />
                        ) : (
                          <CircleDashed
                            className="size-5 shrink-0 text-input"
                            aria-label="Chưa hoàn thành"
                          />
                        )}
                        <span
                          className={cn(
                            "min-w-0 flex-1 truncate font-medium",
                            step.status === "done"
                              ? "text-muted-foreground line-through"
                              : "text-foreground"
                          )}
                        >
                          {step.title}
                        </span>
                        <span className="flex shrink-0 flex-wrap items-center gap-1.5">
                          {step.optional ? <Badge variant="neutral">Tuỳ chọn</Badge> : null}
                          {step.status === "unknown" ? (
                            <StatusBadge tone="neutral">Chưa kiểm tra được</StatusBadge>
                          ) : null}
                          {step.badge ? (
                            <StatusBadge tone={step.badge.tone}>{step.badge.label}</StatusBadge>
                          ) : null}
                        </span>
                      </button>
                    </CollapsibleTrigger>

                    <CollapsibleContent>
                      <div className="flex flex-col gap-4 px-3 pt-0 pb-3.5 pl-11 sm:flex-row sm:items-center sm:justify-between">
                        <div className="min-w-0 flex-1 space-y-3">
                          {step.description ? (
                            <p className="text-[13px] leading-[18px] text-muted-foreground">
                              {step.description}
                            </p>
                          ) : null}
                          {step.primary || step.secondary ? (
                            <div className="flex flex-wrap items-center gap-2">
                              {step.primary}
                              {step.secondary}
                            </div>
                          ) : null}
                        </div>
                        {step.image ? (
                          <div className="hidden shrink-0 overflow-hidden rounded-lg border border-border bg-card sm:block">
                            <img
                              src={step.image.src}
                              alt={step.image.alt}
                              className="h-24 w-36 object-cover"
                            />
                          </div>
                        ) : null}
                      </div>
                    </CollapsibleContent>
                  </Collapsible>
                );
              })}
            </div>
          </CardContent>
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}
