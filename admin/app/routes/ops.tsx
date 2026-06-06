import type { Route } from "./+types/ops";
import { OpsPage } from "~/components/ops/OpsPage/OpsPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Ops — Lantern Admin" }];
}

export default function OpsRoute() {
  return <OpsPage />;
}
