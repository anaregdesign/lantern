import type { Route } from "./+types/browse";
import { PlaceholderPage } from "~/components/shared/PlaceholderPage/PlaceholderPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Browse — Lantern Admin" }];
}

export default function BrowseRoute() {
  return (
    <PlaceholderPage
      title="Browse"
      trackingIssue="#F2"
      description="Vertex and edge listing by prefix will land here."
    />
  );
}
