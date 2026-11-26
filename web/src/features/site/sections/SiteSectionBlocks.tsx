import React from "react";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { SafeActionLink } from "../SafeActionLink";
import { parseNumericStat } from "../siteMetrics";
import type { SiteSectionBlock } from "../templateTypes";
import { sanitizeSafeImageUrl } from "../templateValidator";

export const SITE_PRIMARY_BTN_CLS = cn(
  buttonVariants({ variant: "default", size: "lg" }),
  "min-h-[46px] px-6 text-[15px] font-semibold shadow-none"
);

export const SITE_SECONDARY_BTN_CLS = cn(
  buttonVariants({ variant: "secondary", size: "lg" }),
  "min-h-[46px] px-5 text-[15px] font-medium shadow-none"
);

export const SITE_SECTION_LINK_CLS =
  "inline-flex min-h-9 items-center gap-1 py-1 text-[15px] font-semibold text-brand transition-opacity hover:opacity-80";

const ASPECT_CLASSES: Record<string, string> = {
  "16:9": "aspect-video",
  "3:4": "aspect-[3/4]",
  "1:1": "aspect-square",
  "4:3": "aspect-[4/3]",
};

export function renderStatCard(
  block: Extract<SiteSectionBlock, { type: "stat" }>,
  itemClass: string
): React.ReactNode {
  const parsed = parseNumericStat(block.props.value);
  return (
    <div key={block.id} className={itemClass}>
      <span
        className="text-[clamp(28px,3vw,42px)] leading-[1.06] font-bold tracking-[-0.02em] text-brand tabular-nums"
        data-reveal={parsed ? "count" : undefined}
        data-count-to={parsed ? String(parsed.to) : undefined}
        data-count-prefix={parsed?.prefix || undefined}
        data-count-suffix={parsed?.suffix || undefined}
      >
        {block.props.value}
      </span>
      <span className="text-[15px] font-semibold text-foreground">{block.props.label}</span>
      {block.props.note?.trim() && (
        <span className="text-[13px] leading-[1.45] text-muted-foreground">{block.props.note}</span>
      )}
    </div>
  );
}

export function renderSingleBlock(
  block: SiteSectionBlock,
  previewMode: boolean,
  opts: { fillImageHeight?: boolean } = {}
): React.ReactNode {
  switch (block.type) {
    case "heading":
      return block.props.level === "h2" ? (
        <h2
          key={block.id}
          className="text-[clamp(22px,2vw,28px)] leading-[1.2] font-semibold text-inherit"
        >
          {block.props.text}
        </h2>
      ) : (
        <h3 key={block.id} className="text-lg leading-[1.3] font-semibold text-inherit">
          {block.props.text}
        </h3>
      );
    case "paragraph":
      return (
        <p key={block.id} className="max-w-[60ch] text-base leading-[1.6] text-inherit">
          {block.props.text}
        </p>
      );
    case "badge": {
      const tone = block.props.tone || "neutral";
      if (tone === "accent") {
        return (
          <Badge key={block.id} variant="default" className="rounded-sm px-3 py-1 text-[13px]">
            {block.props.label}
          </Badge>
        );
      }
      if (tone === "band") {
        return (
          <Badge
            key={block.id}
            variant="secondary"
            className="rounded-sm bg-[var(--site-band-bg)] px-3 py-1 text-[13px] text-[var(--site-band-ink)]"
          >
            {block.props.label}
          </Badge>
        );
      }
      return (
        <Badge key={block.id} variant="secondary" className="rounded-sm px-3 py-1 text-[13px]">
          {block.props.label}
        </Badge>
      );
    }
    case "cta": {
      const styleClass =
        block.props.style === "secondary"
          ? SITE_SECONDARY_BTN_CLS
          : block.props.style === "link"
            ? "inline-flex w-fit items-center py-1 text-[15px] font-semibold text-brand underline-offset-4 hover:underline"
            : SITE_PRIMARY_BTN_CLS;
      return (
        <SafeActionLink
          key={block.id}
          href={block.props.href}
          label={block.props.label}
          className={styleClass}
          previewMode={previewMode}
        />
      );
    }
    case "image": {
      const imgUrl = sanitizeSafeImageUrl(block.props.image_url);
      if (!imgUrl) return null;
      const aspectCls = ASPECT_CLASSES[block.props.aspect || "4:3"] ?? "aspect-[4/3]";
      return (
        <figure key={block.id} className="m-0 flex min-w-0 flex-col gap-2">
          <div
            className={cn(
              "w-full overflow-hidden rounded-lg bg-muted",
              opts.fillImageHeight ? "h-full min-h-[320px] lg:min-h-[360px]" : aspectCls
            )}
            data-aspect={block.props.aspect || "4:3"}
            data-reveal="clip"
          >
            <img
              src={imgUrl}
              alt={block.props.alt || block.props.caption || "Ảnh cửa hàng"}
              width={1200}
              height={900}
              className="block size-full object-cover"
            />
          </div>
          {block.props.caption?.trim() && (
            <figcaption className="text-[13px] leading-[1.45] text-muted-foreground">
              {block.props.caption}
            </figcaption>
          )}
        </figure>
      );
    }
    case "stat":
      return renderStatCard(
        block,
        "flex min-w-0 flex-col gap-1.5 rounded-lg border border-border bg-background p-[clamp(20px,2.5vw,28px)]"
      );
    case "policy":
      return (
        <article
          key={block.id}
          className="flex min-w-0 flex-col gap-2 rounded-lg border border-border bg-card p-[clamp(20px,2.5vw,28px)] text-card-foreground"
        >
          <h3 className="text-lg font-semibold text-foreground">{block.props.heading}</h3>
          <p className="max-w-[56ch] text-[15px] leading-[1.55] text-muted-foreground">
            {block.props.detail}
          </p>
        </article>
      );
    case "quote":
      return (
        <blockquote
          key={block.id}
          className="m-0 flex flex-col gap-2 rounded-lg border border-border bg-card px-5 py-4 text-card-foreground"
        >
          <p className="text-base leading-[1.6] text-inherit">“{block.props.body}”</p>
          <cite className="text-[13px] font-semibold text-muted-foreground not-italic">
            {block.props.author}
            {block.props.role?.trim() ? `, ${block.props.role}` : ""}
          </cite>
        </blockquote>
      );
    case "feature":
      return (
        <div
          key={block.id}
          className="flex flex-col gap-1.5 rounded-lg border border-border bg-card px-5 py-4 text-card-foreground"
        >
          {block.props.tag?.trim() && (
            <span className="text-[13px] font-semibold text-brand">{block.props.tag}</span>
          )}
          <h3 className="text-base font-semibold text-foreground">{block.props.title}</h3>
          <p className="text-sm leading-[1.5] text-muted-foreground">{block.props.detail}</p>
        </div>
      );
  }
}

export function renderInlineBlocks(blocks: SiteSectionBlock[] | undefined, previewMode: boolean) {
  if (!blocks || blocks.length === 0) return null;
  const inlineBadgesAndCtas = blocks.filter((b) => b.type === "badge" || b.type === "cta");
  const flowBlocks = blocks.filter((b) => b.type !== "badge" && b.type !== "cta");
  return (
    <>
      {inlineBadgesAndCtas.length > 0 && (
        <div className="mt-3 flex flex-wrap items-center gap-2.5">
          {inlineBadgesAndCtas.map((b) => renderSingleBlock(b, previewMode))}
        </div>
      )}
      {flowBlocks.length > 0 && (
        <div className="mt-5 flex flex-col gap-4">
          {flowBlocks.map((b) => renderSingleBlock(b, previewMode))}
        </div>
      )}
    </>
  );
}

export function renderMaskedHeading(
  Tag: "h1" | "h2",
  text: string,
  className: string,
  immediate = false
) {
  return (
    <Tag
      className={cn(className, immediate && "lines")}
      data-reveal={immediate ? undefined : "lines"}
    >
      <span className="line">
        <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
          {text}
        </span>
      </span>
    </Tag>
  );
}
