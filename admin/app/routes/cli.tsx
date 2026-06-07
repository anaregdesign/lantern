import type { Route } from "./+types/cli";
import { CliPage } from "~/components/cli/CliPage/CliPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "CLI — Lantern Admin" }];
}

export default function CliRoute() {
  return <CliPage />;
}
