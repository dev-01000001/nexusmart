import { PLATFORM_TEMPLATES } from "@/features/site/templateCatalog";
import { DesignSiteClient } from "./DesignSiteClient";

export default async function DesignSitePage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const raw = Array.isArray(params.mau) ? params.mau[0] : params.mau;
  const matched = PLATFORM_TEMPLATES.find((t) => t.id === raw) ?? PLATFORM_TEMPLATES[0];

  return <DesignSiteClient initialTemplateId={matched.id} />;
}
