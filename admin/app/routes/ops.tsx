import type { Route } from "./+types/ops";
import { PlaceholderPage } from "~/components/shared/PlaceholderPage/PlaceholderPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Ops — Lantern Admin" }];
}

export default function OpsRoute() {
  return (
    <PlaceholderPage
      title="Ops"
      trackingIssue="#F5"
      description="Server status and replication panels will land here."
    />
  );
}
