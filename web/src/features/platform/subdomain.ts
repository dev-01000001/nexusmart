// Pure preview of the address a shop name would get. Client-safe and unit tested.
// The real subdomain is chosen and validated by the backend when the seller builds the site.
import { RESERVED_SUBDOMAINS, SUBDOMAIN_PATTERN } from "../../lib/site/host";

const MAX_LABEL = 40;

/** Turn a Vietnamese shop name into a subdomain label: "Xưởng Dệt Bạch Đằng" -> "xuong-det-bach-dang". */
export function suggestSubdomain(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_LABEL)
    .replace(/-+$/g, "");
}

export type SubdomainCheck = "empty" | "short" | "reserved" | "ok";

export function checkSubdomain(label: string): SubdomainCheck {
  if (!label) return "empty";
  if (!SUBDOMAIN_PATTERN.test(label)) return "short";
  if (RESERVED_SUBDOMAINS.has(label)) return "reserved";
  return "ok";
}
