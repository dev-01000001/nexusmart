"use client";

import * as React from "react";

const PortalContainerContext = React.createContext<HTMLElement | null>(null);

export function usePortalContainer(): HTMLElement | null {
  return React.useContext(PortalContainerContext);
}

export function UiScope({ children, ...props }: React.ComponentProps<"div">): React.JSX.Element {
  const [container, setContainer] = React.useState<HTMLElement | null>(null);

  return (
    <PortalContainerContext.Provider value={container}>
      <div data-ui="true" {...props}>
        {children}
        <div data-ui-portal="" ref={setContainer} />
      </div>
    </PortalContainerContext.Provider>
  );
}
