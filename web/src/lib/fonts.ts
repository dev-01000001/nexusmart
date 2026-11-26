import {
  Be_Vietnam_Pro,
  IBM_Plex_Sans,
  Inter,
  Plus_Jakarta_Sans,
  Work_Sans,
} from "next/font/google";

export const inter = Inter({
  subsets: ["latin", "vietnamese"],
  variable: "--font-inter",
  display: "swap",
});

export const siteWorkSans = Work_Sans({
  subsets: ["latin", "vietnamese"],
  variable: "--font-site-work-sans",
  display: "swap",
  preload: false,
});

export const siteBeVietnamPro = Be_Vietnam_Pro({
  subsets: ["latin", "vietnamese"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-site-be-vietnam-pro",
  display: "swap",
  preload: false,
});

export const siteIbmPlexSans = IBM_Plex_Sans({
  subsets: ["latin", "vietnamese"],
  variable: "--font-site-ibm-plex-sans",
  display: "swap",
  preload: false,
});

export const sitePlusJakartaSans = Plus_Jakarta_Sans({
  subsets: ["latin", "vietnamese"],
  variable: "--font-site-plus-jakarta-sans",
  display: "swap",
  preload: false,
});

export const fontVariableClassNames: string = [
  inter.variable,
  siteWorkSans.variable,
  siteBeVietnamPro.variable,
  siteIbmPlexSans.variable,
  sitePlusJakartaSans.variable,
].join(" ");
