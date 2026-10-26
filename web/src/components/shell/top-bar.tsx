import { Bell, LogOut, Menu, UserRound } from "lucide-react";
import { useState } from "react";

import { Wordmark } from "@/components/brand/logo";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { SessionUser } from "@/lib/session";

import { SidebarNav } from "./sidebar";

const ROLE_LABEL = { admin: "Admin", maintainer: "Maintainer", owner: "Backup owner" } as const;

function MobileNav({ user }: { user: SessionUser }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="lg:hidden" aria-label="Open navigation">
          <Menu aria-hidden="true" />
        </Button>
      </DialogTrigger>
      <DialogContent className="top-0 left-0 h-dvh max-w-72 translate-x-0 translate-y-0 content-start gap-6 rounded-none border-y-0 border-l-0 bg-sidebar p-5 sm:max-w-72">
        <DialogTitle asChild>
          <div>
            <Wordmark />
          </div>
        </DialogTitle>
        <DialogDescription className="sr-only">Main navigation</DialogDescription>
        <SidebarNav
          role={user.role}
          onNavigate={() => {
            setOpen(false);
          }}
        />
      </DialogContent>
    </Dialog>
  );
}

/** Placeholder bell: the live inbox and unread badge arrive in task 6.10. */
function NotificationBell() {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="Notifications" className="relative">
          <Bell aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>Notifications</TooltipContent>
    </Tooltip>
  );
}

function UserMenu({ user }: { user: SessionUser }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="h-8 gap-2 px-2" aria-label="Account menu">
          <span
            aria-hidden="true"
            className="inline-flex size-6 items-center justify-center rounded-sm bg-primary font-mono text-2xs font-semibold text-primary-foreground"
          >
            {user.displayName.slice(0, 1)}
          </span>
          <span className="hidden text-[0.8125rem] font-medium sm:inline">{user.displayName}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="font-medium">{user.displayName}</span>
          <span className="font-mono text-xs font-normal text-muted-foreground">
            {user.username} · {ROLE_LABEL[user.role]}
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled>
          <UserRound aria-hidden="true" /> Profile and security
        </DropdownMenuItem>
        <DropdownMenuItem disabled>
          <LogOut aria-hidden="true" /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function TopBar({ user, title }: { user: SessionUser; title: string }) {
  return (
    <header className="sticky top-0 z-30 flex h-(--shell-topbar-height) items-center gap-3 border-b bg-background/90 px-4 backdrop-blur-sm sm:px-6">
      <MobileNav user={user} />
      <p className="truncate text-[0.8125rem] font-medium text-muted-foreground">{title}</p>
      <div className="ml-auto flex items-center gap-1.5">
        <ThemeToggle className="mr-1.5 hidden sm:inline-flex" />
        <NotificationBell />
        <UserMenu user={user} />
      </div>
    </header>
  );
}
