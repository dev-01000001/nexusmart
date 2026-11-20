import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Each worker/CI job may build into its own dir (e.g. NEXT_DIST_DIR=.next-qa)
  // so parallel builds never overwrite a running `next start`.
  distDir: process.env.NEXT_DIST_DIR || ".next",
  output: "standalone",
  images: {
    unoptimized: true,
  },
  reactStrictMode: true,
};

export default nextConfig;
