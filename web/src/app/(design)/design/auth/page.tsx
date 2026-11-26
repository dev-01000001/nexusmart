import { DesignAuthClient } from "./DesignAuthClient";

export default async function DesignAuthPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const raw = Array.isArray(params["bien-the"]) ? params["bien-the"][0] : params["bien-the"];
  const variant = raw === "embedded" ? "embedded" : "standalone";

  return <DesignAuthClient variant={variant} />;
}
