import type { Route } from "./+types/edges._index";
import { BrowseEdgesPage } from "~/components/browse-edges/BrowseEdgesPage/BrowseEdgesPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Edges — Lantern Admin" }];
}

export default function EdgesIndexRoute() {
  return <BrowseEdgesPage />;
}
