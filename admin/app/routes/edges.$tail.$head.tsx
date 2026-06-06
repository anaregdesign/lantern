import type { Route } from "./+types/edges.$tail.$head";
import { Navigate, useParams } from "react-router";
import { EdgeDetailPage } from "~/components/edit-edge/EdgeDetailPage/EdgeDetailPage";

export function meta({ params }: Route.MetaArgs) {
  const tail = decodeKey(params.tail);
  const head = decodeKey(params.head);
  return [
    {
      title:
        tail && head
          ? `${tail} → ${head} — Lantern Admin`
          : "Edge — Lantern Admin",
    },
  ];
}

export default function EdgeDetailRoute() {
  const { tail, head } = useParams<{ tail: string; head: string }>();
  const decodedTail = decodeKey(tail);
  const decodedHead = decodeKey(head);
  if (!decodedTail || !decodedHead) {
    return <Navigate to="/edges" replace />;
  }
  return <EdgeDetailPage tail={decodedTail} head={decodedHead} />;
}

function decodeKey(raw: string | undefined): string {
  if (!raw) return "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}
