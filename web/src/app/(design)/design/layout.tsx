import type { Metadata } from "next";
import { ErrorState } from "@/components/app";
import { Toaster } from "@/components/ui/sonner";
import { UiScope } from "@/components/ui/ui-scope";
import { ADMIN_STAFF_ROLES } from "@/lib/admin-rbac";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { requireRole } from "@/lib/server/session";
import "@/styles/ui.css";

export const metadata: Metadata = {
  title: "Thiết kế (nội bộ)",
  robots: {
    index: false,
    follow: false,
  },
};

export const dynamic = "force-dynamic";

export default async function DesignLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  try {
    await requireRole(...ADMIN_STAFF_ROLES);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <UiScope className="flex min-h-svh items-center justify-center bg-background p-6 text-sm text-foreground">
          <div className="w-full max-w-md">
            <ErrorState
              title="Chưa thể xác minh phiên quản trị"
              description={SAFE_MESSAGES[err.kind]}
              retryHref="/design"
            />
          </div>
        </UiScope>
      );
    }
    throw err;
  }

  return (
    <UiScope className="min-h-svh bg-background text-sm text-foreground">
      {children}
      <Toaster />
    </UiScope>
  );
}
