import { Inter } from "next/font/google";

// Keep `axes: ["opsz"]` across root-host public, auth, and 404 routes so Inter Display
// optical sizing (>14px) stays identical to `/` hero & heading metrics.
export const platformInter = Inter({
  subsets: ["latin", "vietnamese"],
  axes: ["opsz"],
  variable: "--font-inter",
  display: "swap",
});
