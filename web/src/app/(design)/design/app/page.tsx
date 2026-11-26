import { DesignAppClient, type DesignAppPattern } from "./DesignAppClient";

const VALID_PATTERNS: readonly DesignAppPattern[] = [
  "a1",
  "a2",
  "a3",
  "a4",
  "a5",
  "a6",
  "a7",
  "a8",
  "a9",
];

export default async function DesignAppPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const raw = Array.isArray(params.kieu) ? params.kieu[0] : params.kieu;
  const clean = (raw ?? "a1").toLowerCase() as DesignAppPattern;
  const kieu: DesignAppPattern = VALID_PATTERNS.includes(clean) ? clean : "a1";

  return <DesignAppClient kieu={kieu} />;
}
