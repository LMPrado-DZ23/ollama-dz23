import { AppSidebar } from "@/components/AppSidebar";
import AgenticConsole from "@/components/AgenticConsole";
import { SidebarLayout } from "@/components/layout/layout";
import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/agentic")({
  component: AgenticRoute,
});

function AgenticRoute() {
  return (
    <SidebarLayout title="Agentic" sidebar={<AppSidebar current="agentic" />}>
      <AgenticConsole />
    </SidebarLayout>
  );
}
