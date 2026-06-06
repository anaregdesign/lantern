import type { Route } from "./+types/illuminate";
import { IlluminatePage } from "~/components/illuminate/IlluminatePage/IlluminatePage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Illuminate — Lantern Admin" }];
}

export default function IlluminateRoute() {
  return <IlluminatePage />;
}
