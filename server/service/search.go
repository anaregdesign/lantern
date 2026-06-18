package service

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// errSearchDisabled is the FAILED_PRECONDITION payload returned when the
// SearchVertices RPC is invoked but the server-side index was never built
// (LANTERN_SEARCH_ENABLED=false). It is a fixed sentinel so clients and the
// admin UI can present a calm "search is turned off" state rather than
// treating it as a transient failure.
var errSearchDisabled = errors.New("vertex search is disabled on this server; set LANTERN_SEARCH_ENABLED=true to enable it")

// SearchVertices returns vertices ranked by full-text relevance over their
// indexed content (key + value), most relevant first. It is the content
// counterpart to ScanVertices' lexicographic key walk: callers search by
// remembered topic words instead of an exact key prefix.
//
// Gating: when the server was started without the search index
// (LANTERN_SEARCH_ENABLED=false) the RPC returns FAILED_PRECONDITION. The
// decision lives here, not in the cache, because the core backend returns an
// empty result for a disabled index, an unanalysable query, and a genuine
// no-match alike — only the composition root knows whether
// EnableSearchIndex was called (#624).
//
// Pagination: the request limit is clamped to (0, MaxLimit]; a zero value
// falls back to DefaultLimit. The hit count is bounded exactly by the
// clamped limit because the cache applies it after its liveness and prefix
// filters. An empty or unanalysable query yields zero hits (not an error).
// Prefix, when non-empty, scopes hits to vertices whose key carries it.
func (s *LanternService) SearchVertices(ctx context.Context, in *pb.SearchVerticesRequest) (*pb.SearchVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if !s.search.Enabled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errSearchDisabled)
	}
	start := time.Now()
	limit := clampLimit(in.GetLimit(), s.search.DefaultLimit, s.search.MaxLimit)

	ranked := s.cache.SearchVertices(in.GetQuery(), int(limit), in.GetPrefix())
	hits := make([]*pb.SearchHit, 0, len(ranked))
	for _, r := range ranked {
		hits = append(hits, &pb.SearchHit{Key: r.ID, Score: r.Score})
	}
	s.metrics.OnSearch(len(hits), time.Since(start))
	return &pb.SearchVerticesResponse{Hits: hits}, nil
}
