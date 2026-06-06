import type { Route } from "./+types/_index";
import { LandingPage } from "~/components/landing/LandingPage/LandingPage";

export function meta(_: Route.MetaArgs) {
  return [
    { title: "Lantern Admin" },
    {
      name: "description",
      content: "Browser-based control surface for the Lantern graph KVS.",
    },
  ];
}

export default function IndexRoute() {
  return <LandingPage />;
}
