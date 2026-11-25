import type { NextConfig } from "next";
import { resolveChatWsOrigin } from "./src/lib/server/config";

const chatWsOrigin = resolveChatWsOrigin();
const connectSrc = chatWsOrigin ? `'self' ${chatWsOrigin}` : "'self'";

const nextConfig: NextConfig = {
  // Each worker/CI job may build into its own dir (e.g. NEXT_DIST_DIR=.next-qa)
  // so parallel builds never overwrite a running `next start`.
  distDir: process.env.NEXT_DIST_DIR || ".next",
  output: "standalone",
  images: {
    unoptimized: true,
  },
  reactStrictMode: true,
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
        ],
      },
      {
        // Apply site-wide minimal CSP only to non-/api routes so BFF routes such as
        // /api/shipping/labels/:code preserve their own sandbox Content-Security-Policy.
        source: "/((?!api(?:/|$)).*)",
        headers: [
          {
            key: "Content-Security-Policy",
            value: `object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' https: data: blob:; connect-src ${connectSrc}`,
          },
        ],
      },
      {
        source: "/dat-lai-mat-khau",
        headers: [{ key: "Referrer-Policy", value: "no-referrer" }],
      },
      {
        source: "/s/:host/dat-lai-mat-khau",
        headers: [{ key: "Referrer-Policy", value: "no-referrer" }],
      },
    ];
  },
};

export default nextConfig;
