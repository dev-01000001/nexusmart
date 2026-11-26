// Safe, zero-dependency markdown renderer for buyer & seller AI text (no dangerouslySetInnerHTML).
import { createElement as h, Fragment, type ComponentType, type ReactNode } from "react";
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";

export type AssistantLinkComponent = ComponentType<{
  href: string;
  className?: string;
  children?: ReactNode;
}>;
export interface AssistantMarkdownClassNames {
  paragraph?: string;
  list?: string;
  listItem?: string;
  strong?: string;
  link?: string;
}
export interface RenderAssistantMarkdownOptions {
  stripUuids?: boolean;
  streaming?: boolean;
  LinkComponent?: AssistantLinkComponent;
  classNames?: AssistantMarkdownClassNames;
}

const BULLET_RE = /^\s*[-*•]\s+/;
const TOKEN_RE = /\*\*(?!\*)(.+?)\*\*|`([^`]+)`|\[([^[\]]+)\]\(([^()\s]+)\)/g;
const LINK_URL_RE = /\]\(([^()\s]+)\)/g;
const UUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
const ID_LBL = "(?:mã(?: (?:sp|sản phẩm|sku|id|hội thoại|đơn(?: hàng)?))?|id|sku)";
const SP = "[ \\t]{0,4}";
const UUID_RE = new RegExp(
  `\\(${SP}(?:${ID_LBL}${SP}[:：]${SP})?\`?${UUID}\`?${SP}\\)|${ID_LBL}${SP}[:：]${SP}\`?${UUID}\`?(?:${SP}[,;])?|\`?\\b${UUID}\\b\`?`,
  "gi"
);
const STREAM_ID_RE = new RegExp(`\\(${SP}${ID_LBL}${SP}[:：]${SP}\`?[0-9a-f-]{0,36}\`?$`, "i");

/** Allows ONLY safe internal paths starting with a single `/` (validated by sanitizeSafeUrl). */
export function sanitizeAssistantLinkUrl(raw: string): string {
  const safe = sanitizeSafeUrl(raw);
  return safe.startsWith("/") ? safe : "";
}

function stripUuidsFromLine(line: string, isTail: boolean): string {
  const urls: string[] = [];
  const clean = line.replace(/\u0000/g, "");
  const mask = clean.replace(LINK_URL_RE, (_, u: string) => `](\u0000${urls.push(u) - 1}\u0000)`);
  let out = mask.replace(UUID_RE, "");
  if (isTail) out = out.replace(STREAM_ID_RE, "");
  if (out === mask) return line;
  return out
    .replace(/[ \t]{2,}/g, " ")
    .replace(/([(\[])(?: ?[,;] ?| )/g, "$1")
    .replace(/(?: ?[,;] ?| )([)\]])/g, "$1")
    .replace(/ ([,.;:!?])/g, "$1")
    .replace(/\(\)|\[\]/g, "")
    .replace(/\u0000(\d+)\u0000/g, (_, idx: string) => urls[Number(idx)] ?? "");
}

function cleanPlain(text: string, isTail: boolean): string {
  let out = text.replace(/\*\*(?=\S|$)/g, "").replace(/`(?=\S)/g, "");
  if (!isTail) return out;
  out = out.replace(/\[([^[\]]+)\]\([^\s)]*$/, "$1");
  let end = out.length;
  while (end > 0 && (out[end - 1] === "*" || out[end - 1] === "`")) end--;
  return end === out.length ? out : out.slice(0, end);
}

export function renderAssistantMarkdown(
  raw: string,
  opts: RenderAssistantMarkdownOptions = {}
): ReactNode {
  if (!raw) return null;
  const { stripUuids = true, streaming = false, classNames: cls, LinkComponent } = opts;
  const rawLines = raw.split(/\r?\n/);
  const blocks: ReactNode[] = [];
  let para: ReactNode[][] = [];
  let items: ReactNode[][] = [];
  const renderInline = (line: string, pfx: string, isTail: boolean): ReactNode[] => {
    const nodes: ReactNode[] = [];
    let last = 0;
    let n = 0;
    for (const m of line.matchAll(TOKEN_RE)) {
      const start = m.index ?? 0;
      if (start > last) nodes.push(cleanPlain(line.slice(last, start), false));
      const [, bold, code, label, rawUrl] = m;
      const key = `${pfx}-${n++}`;
      if (bold) nodes.push(h("strong", { key, className: cls?.strong }, bold));
      else if (code) nodes.push(code);
      else if (label && rawUrl) {
        const href = sanitizeAssistantLinkUrl(rawUrl);
        const text = label.replace(/\*\*(.+?)\*\*/g, "$1").replace(/`([^`]+)`/g, "$1");
        nodes.push(
          href && text.trim()
            ? h(LinkComponent ?? "a", { key, href, className: cls?.link }, text)
            : text
        );
      }
      last = start + m[0].length;
    }
    if (last < line.length) nodes.push(cleanPlain(line.slice(last), isTail));
    return nodes.filter((node) => node !== "");
  };
  const flush = () => {
    if (para.length) {
      const k = `p-${blocks.length}`;
      const kids = para.flatMap((ln, i) => (i ? [h("br", { key: `${k}b${i}` }), ...ln] : ln));
      blocks.push(h("p", { key: k, className: cls?.paragraph }, kids));
      para = [];
    }
    if (items.length) {
      const k = `ul-${blocks.length}`;
      const lis = items.map((it, i) => h("li", { key: `${k}-${i}`, className: cls?.listItem }, it));
      blocks.push(h("ul", { key: k, className: cls?.list }, lis));
      items = [];
    }
  };
  rawLines.forEach((rawLine, i) => {
    if (!rawLine.trim()) return flush();
    const isTail = streaming && i === rawLines.length - 1;
    const bullet = BULLET_RE.test(rawLine);
    let line = bullet ? rawLine.replace(BULLET_RE, "") : rawLine.trim();
    if (stripUuids) line = stripUuidsFromLine(line, isTail).trim();
    if (!line) return;
    const nodes = renderInline(line, `l${i}`, isTail);
    if (nodes.every((x) => typeof x === "string" && !x.trim())) return;
    if (bullet ? para.length : items.length) flush();
    (bullet ? items : para).push(nodes);
  });
  flush();
  return blocks.length ? h(Fragment, null, blocks) : null;
}
