"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { ChevronDown, ExternalLink, Menu, PanelLeft, Search, type LucideIcon } from "lucide-react";
import { NexusMark } from "@/components/Icons";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  useSidebar,
} from "@/components/ui/sidebar";
import { UiScope } from "@/components/ui/ui-scope";
import { cn } from "@/lib/utils";
import { resolveActiveNav } from "./navMatch";

export interface AppNavItem {
  href: string;
  label: string;
  icon: LucideIcon;
  badge?: number;
  external?: boolean;
  children?: Array<{ href: string; label: string }>;
  match?: "exact" | "prefix";
}

export interface AppNavGroup {
  title: string;
  items: AppNavItem[];
}

export interface AppCommandItem {
  label: string;
  group: string;
  href: string;
  keywords?: string[];
}

export interface AppAccount {
  name: string;
  subtitle?: string;
  menu: Array<{
    label: string;
    href?: string;
    onSelect?: () => void;
    destructive?: boolean;
  }>;
}

export interface AppShellProps {
  area: "seller" | "admin";
  areaLabel: string;
  homeHref: string;
  navGroups: AppNavGroup[];
  commandItems: AppCommandItem[];
  account: AppAccount;
  storageKey: string;
  mobileNav?: React.ReactNode;
  children: React.ReactNode;
}

function getInitials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "NM";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return `${words[0][0] ?? ""}${words[words.length - 1][0] ?? ""}`.toUpperCase();
}

function AppShellInner({
  area,
  areaLabel,
  homeHref,
  navGroups,
  commandItems,
  account,
  storageKey,
  mobileNav,
  children,
}: AppShellProps) {
  const pathname = usePathname() || "/";
  const searchParams = useSearchParams();
  const rawSearch = searchParams.toString();
  const search = rawSearch ? `?${rawSearch}` : "";
  const router = useRouter();
  const { toggleSidebar, setOpen, setOpenMobile } = useSidebar();
  const [commandOpen, setCommandOpen] = React.useState(false);

  const { activeHref, activeChildHref } = React.useMemo(
    () =>
      resolveActiveNav(
        navGroups.flatMap((group) => group.items),
        pathname,
        search,
        homeHref
      ),
    [navGroups, pathname, search, homeHref]
  );

  // Default to icon-collapsed on tablet (768–1023px) if user has not persisted a preference
  React.useEffect(() => {
    const mql = window.matchMedia("(min-width: 768px) and (max-width: 1023px)");
    const syncTablet = () => {
      try {
        if (window.localStorage.getItem(storageKey) !== null) return;
      } catch {
        // Ignore storage read errors
      }
      if (mql.matches) {
        setOpen(false);
      }
    };
    syncTablet();
    mql.addEventListener("change", syncTablet);
    return () => mql.removeEventListener("change", syncTablet);
  }, [storageKey, setOpen]);

  // Global Cmd/Ctrl + K shortcut
  React.useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCommandOpen((prev) => !prev);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const groupedCommands = React.useMemo(() => {
    const map = new Map<string, AppCommandItem[]>();
    for (const item of commandItems) {
      const list = map.get(item.group) ?? [];
      list.push(item);
      map.set(item.group, list);
    }
    return [...map.entries()];
  }, [commandItems]);

  const initials = getInitials(account.name);

  return (
    <>
      {/* Topbar 56px */}
      <header
        data-area={area}
        className="sticky top-0 z-30 flex h-14 w-full shrink-0 items-center justify-between gap-2 bg-topbar px-3 text-topbar-foreground md:px-4"
      >
        <div className="flex min-w-0 items-center gap-2">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={toggleSidebar}
            aria-label="Mở hoặc thu gọn menu điều hướng"
            className="shrink-0 text-topbar-foreground hover:bg-topbar-input hover:text-white"
          >
            <Menu className="size-4 md:hidden" aria-hidden="true" />
            <PanelLeft className="hidden size-4 md:block" aria-hidden="true" />
          </Button>

          <Link
            href={homeHref}
            className="flex min-w-0 items-center gap-2 rounded-md py-1 pr-1 text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <NexusMark size={20} accentColor="var(--ui-brand)" className="shrink-0 text-white" />
            <span className="hidden font-semibold tracking-tight text-white sm:inline">
              NexusMart
            </span>
            <Badge
              variant="secondary"
              className="rounded-md border-0 bg-topbar-input px-2 py-0.5 text-[11px] font-medium text-white"
            >
              {areaLabel}
            </Badge>
          </Link>
        </div>

        {/* Center Command Trigger */}
        <div className="flex flex-1 justify-center px-1">
          <button
            type="button"
            onClick={() => setCommandOpen(true)}
            aria-label="Tìm kiếm hoặc mở thao tác nhanh"
            className="hidden h-9 w-full max-w-[480px] items-center justify-between gap-2 rounded-lg bg-topbar-input px-3 text-left text-[13px] text-topbar-placeholder transition-colors hover:bg-topbar-input/85 focus-visible:ring-2 focus-visible:ring-ring md:flex"
          >
            <span className="flex min-w-0 items-center gap-2">
              <Search className="size-4 shrink-0" aria-hidden="true" />
              <span className="truncate">Tìm kiếm hoặc thao tác…</span>
            </span>
            <kbd className="shrink-0 rounded bg-topbar px-1.5 py-0.5 text-[11px] font-medium text-topbar-foreground">
              ⌘K
            </kbd>
          </button>
        </div>

        {/* Right: Mobile Search Icon + Account Menu */}
        <div className="flex shrink-0 items-center gap-1.5">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={() => setCommandOpen(true)}
            aria-label="Tìm kiếm nhanh"
            className="text-topbar-foreground hover:bg-topbar-input hover:text-white md:hidden"
          >
            <Search className="size-4" aria-hidden="true" />
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Menu tài khoản"
                className="flex max-w-[175px] items-center gap-2 rounded-lg px-2 py-1 text-left text-topbar-foreground transition-colors hover:bg-topbar-input hover:text-white focus-visible:ring-2 focus-visible:ring-ring sm:max-w-[240px]"
              >
                <Avatar className="size-7 shrink-0">
                  <AvatarFallback className="bg-brand text-xs font-semibold text-brand-foreground">
                    {initials}
                  </AvatarFallback>
                </Avatar>
                <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-white">
                  {account.name}
                </span>
                <ChevronDown className="size-3.5 shrink-0 opacity-75" aria-hidden="true" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuLabel className="font-normal">
                <div className="flex flex-col gap-0.5">
                  <span className="truncate text-sm font-semibold text-foreground">
                    {account.name}
                  </span>
                  {account.subtitle ? (
                    <span className="truncate text-xs text-muted-foreground">
                      {account.subtitle}
                    </span>
                  ) : null}
                </div>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              {account.menu.map((item) =>
                item.href ? (
                  <DropdownMenuItem
                    key={item.label}
                    asChild
                    variant={item.destructive ? "destructive" : "default"}
                  >
                    <Link href={item.href}>{item.label}</Link>
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem
                    key={item.label}
                    onSelect={item.onSelect}
                    variant={item.destructive ? "destructive" : "default"}
                  >
                    {item.label}
                  </DropdownMenuItem>
                )
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      {/* Body: Left Sidebar + Main Content */}
      <div className="flex min-h-[calc(100svh-3.5rem)] w-full flex-1">
        <Sidebar
          collapsible="icon"
          className="top-14 h-[calc(100svh-3.5rem)] border-r border-sidebar-border bg-sidebar"
        >
          <SidebarContent className="gap-1 py-2">
            <nav aria-label="Điều hướng chính" className="flex flex-col gap-1">
              {navGroups.map((group) => (
                <SidebarGroup key={group.title} className="px-2 py-1.5">
                  {group.title ? (
                    <SidebarGroupLabel className="px-2 text-[11px] font-semibold tracking-wider text-sidebar-muted uppercase">
                      {group.title}
                    </SidebarGroupLabel>
                  ) : null}
                  <SidebarGroupContent>
                    <SidebarMenu>
                      {group.items.map((item) => {
                        const Icon = item.icon;
                        const active = item.href === activeHref;

                        return (
                          <SidebarMenuItem key={item.href}>
                            <SidebarMenuButton
                              asChild
                              isActive={active}
                              tooltip={item.label}
                              className={cn(
                                "h-9 rounded-lg px-2.5 text-[13px] font-medium text-sidebar-foreground transition-colors",
                                active &&
                                  "bg-sidebar-accent font-semibold text-sidebar-accent-foreground shadow-card"
                              )}
                            >
                              {item.external ? (
                                <a
                                  href={item.href}
                                  target="_blank"
                                  rel="noreferrer"
                                  onClick={() => setOpenMobile(false)}
                                >
                                  <Icon
                                    className={cn(
                                      "size-4 shrink-0",
                                      active ? "text-brand" : "text-sidebar-muted"
                                    )}
                                    aria-hidden="true"
                                  />
                                  <span className="min-w-0 flex-1 truncate">{item.label}</span>
                                  <ExternalLink
                                    className="ml-auto size-3.5 shrink-0 text-sidebar-muted group-data-[collapsible=icon]:hidden"
                                    aria-hidden="true"
                                  />
                                </a>
                              ) : (
                                <Link
                                  href={item.href}
                                  aria-current={active && !activeChildHref ? "page" : undefined}
                                  onClick={() => setOpenMobile(false)}
                                >
                                  <Icon
                                    className={cn(
                                      "size-4 shrink-0",
                                      active ? "text-brand" : "text-sidebar-muted"
                                    )}
                                    aria-hidden="true"
                                  />
                                  <span className="min-w-0 flex-1 truncate">{item.label}</span>
                                  {typeof item.badge === "number" && item.badge > 0 ? (
                                    <Badge
                                      variant="neutral"
                                      className="ml-auto px-1.5 py-0 text-[11px] tabular-nums group-data-[collapsible=icon]:hidden"
                                    >
                                      {item.badge}
                                    </Badge>
                                  ) : null}
                                </Link>
                              )}
                            </SidebarMenuButton>

                            {active && item.children && item.children.length > 0 ? (
                              <SidebarMenuSub>
                                {item.children.map((sub) => {
                                  const subActive = sub.href === activeChildHref;
                                  return (
                                    <SidebarMenuSubItem key={sub.href}>
                                      <SidebarMenuSubButton
                                        asChild
                                        isActive={subActive}
                                        className={cn(
                                          "text-[13px]",
                                          subActive &&
                                            "bg-sidebar-accent font-semibold text-sidebar-accent-foreground"
                                        )}
                                      >
                                        <Link
                                          href={sub.href}
                                          aria-current={subActive ? "page" : undefined}
                                          onClick={() => setOpenMobile(false)}
                                        >
                                          <span className="truncate">{sub.label}</span>
                                        </Link>
                                      </SidebarMenuSubButton>
                                    </SidebarMenuSubItem>
                                  );
                                })}
                              </SidebarMenuSub>
                            ) : null}
                          </SidebarMenuItem>
                        );
                      })}
                    </SidebarMenu>
                  </SidebarGroupContent>
                </SidebarGroup>
              ))}
            </nav>
          </SidebarContent>
        </Sidebar>

        <main
          className={cn(
            "min-w-0 flex-1 bg-background p-4 md:p-6 xl:p-8",
            mobileNav ? "pb-20 md:pb-6 xl:pb-8" : ""
          )}
        >
          {children}
        </main>
      </div>

      {/* Mobile bottom nav slot (< 768px) */}
      {mobileNav ? (
        <div className="fixed inset-x-0 bottom-0 z-30 border-t border-border bg-card shadow-overlay md:hidden">
          {mobileNav}
        </div>
      ) : null}

      {/* Command Palette (Ctrl/Cmd + K) */}
      <CommandDialog
        open={commandOpen}
        onOpenChange={setCommandOpen}
        title="Tìm kiếm hoặc mở thao tác nhanh"
        description="Gõ tên trang hoặc thao tác trong khu vực làm việc."
      >
        <CommandInput placeholder="Tìm trang hoặc thao tác nhanh…" />
        <CommandList>
          <CommandEmpty>Không tìm thấy kết quả phù hợp.</CommandEmpty>
          {groupedCommands.map(([groupName, items]) => (
            <CommandGroup key={groupName} heading={groupName}>
              {items.map((item) => (
                <CommandItem
                  key={`${groupName}-${item.href}-${item.label}`}
                  value={[item.label, ...(item.keywords ?? [])].join(" ")}
                  onSelect={() => {
                    setCommandOpen(false);
                    router.push(item.href);
                  }}
                >
                  <span>{item.label}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          ))}
        </CommandList>
      </CommandDialog>
    </>
  );
}

export function AppShell(props: AppShellProps) {
  return (
    <UiScope className="min-h-svh bg-background text-sm text-foreground">
      <SidebarProvider storageKey={props.storageKey} className="flex min-h-svh flex-col">
        <AppShellInner {...props} />
      </SidebarProvider>
    </UiScope>
  );
}
