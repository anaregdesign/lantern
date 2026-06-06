import type { Route } from "./+types/illuminate";
import { PlaceholderPage } from "~/components/shared/PlaceholderPage/PlaceholderPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Illuminate — Lantern Admin" }];
}

export default function IlluminateRoute() {
  return (
    <PlaceholderPage
      title="Illuminate"
      trackingIssue="#F4"
      description="Sigma.js neighborhood explorer with SPT / MST switching will land here."
    />
  );
}
