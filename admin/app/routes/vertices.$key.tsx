import type { Route } from "./+types/vertices.$key";
import { Navigate, useParams, useSearchParams } from "react-router";
import { VertexDetailPage } from "~/components/edit-vertex/VertexDetailPage/VertexDetailPage";

export function meta({ params }: Route.MetaArgs) {
  const key = decodeKey(params.key);
  return [{ title: key ? `${key} — Lantern Admin` : "Vertex — Lantern Admin" }];
}

/**
 * Single-vertex detail/edit route. URL segment is percent-encoded so
 * keys with slashes or unicode survive a hard reload.
 */
export default function VertexDetailRoute() {
  const { key } = useParams<{ key: string }>();
  const [searchParams] = useSearchParams();
  const decoded = decodeKey(key);
  if (!decoded) {
    return <Navigate to="/vertices" replace />;
  }
  return (
    <VertexDetailPage
      vertexKey={decoded}
      initialEdit={searchParams.get("edit") === "1"}
    />
  );
}

function decodeKey(raw: string | undefined): string {
  if (!raw) return "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}
