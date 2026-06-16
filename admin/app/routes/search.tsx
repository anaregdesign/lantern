import type { Route } from "./+types/search";
import { SearchPage } from "~/components/search/SearchPage/SearchPage";

export function meta(_: Route.MetaArgs) {
  return [{ title: "Search — Lantern Admin" }];
}

export default function SearchRoute() {
  return <SearchPage />;
}
