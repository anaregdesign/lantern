import type { Route } from "./+types/vertices._index";
import { BrowseVerticesPage } from "~/components/browse-vertices/BrowseVerticesPage/BrowseVerticesPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Vertices — Lantern Admin" }];
}

export default function VerticesIndexRoute() {
  return <BrowseVerticesPage />;
}
