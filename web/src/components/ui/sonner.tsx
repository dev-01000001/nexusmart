"use client";

import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { Toaster as Sonner, type ToasterProps } from "sonner";

const Toaster = ({ position = "bottom-right", toastOptions, ...props }: ToasterProps) => {
  return (
    <Sonner
      theme="light"
      position={position}
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      toastOptions={{
        ...toastOptions,
        classNames: {
          toast:
            "group toast bg-[#1F3038] text-white border border-white/10 shadow-overlay rounded-lg px-4 py-3 text-sm",
          title: "text-white font-medium",
          description: "text-white/80 text-xs",
          actionButton:
            "bg-white text-[#1F3038] font-medium rounded-md px-2.5 py-1 text-xs hover:bg-white/90",
          cancelButton:
            "bg-white/15 text-white font-medium rounded-md px-2.5 py-1 text-xs hover:bg-white/25",
          closeButton: "bg-[#1F3038] text-white border-white/20 hover:bg-white/15",
          ...toastOptions?.classNames,
        },
      }}
      {...props}
    />
  );
};

export { Toaster };
