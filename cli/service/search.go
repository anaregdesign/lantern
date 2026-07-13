package service

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/cli/parser"
	client "github.com/anaregdesign/lantern/sdks/go"
)

type searchClient interface {
	SearchVerticesPage(context.Context, string, ...client.SearchOption) (client.SearchPage, error)
}

type searchHitOutput struct {
	Key              string          `json:"key"`
	Score            float64         `json:"score"`
	Vertex           json.RawMessage `json:"vertex,omitempty"`
	ProjectionStatus string          `json:"projection_status"`
}

type searchPageOutput struct {
	Hits                []searchHitOutput `json:"hits"`
	NextCursor          string            `json:"next_cursor"`
	EffectiveLimit      uint32            `json:"effective_limit"`
	Truncated           bool              `json:"truncated"`
	ContinuationLimited bool              `json:"continuation_limited"`
}

// RunSearch is the one request/output implementation shared by the raw REPL
// grammar and the top-level Cobra search command. JSON is a single bounded
// page document; all=true resolves to NDJSON unless the caller explicitly
// selected TSV, so exhaustive output is streamed rather than accumulated.
func (c *CLIService) RunSearch(ctx context.Context, command parser.Search) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("%w: client is nil", ErrSearch)
	}
	return runSearch(ctx, c.client, command, c.out)
}

func runSearch(ctx context.Context, searcher searchClient, command parser.Search, out io.Writer) error {
	opts, format, err := searchOptions(command)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSearch, err)
	}
	if out == nil {
		return fmt.Errorf("%w: output is nil", ErrSearch)
	}

	if !command.All {
		page, err := searcher.SearchVerticesPage(ctx, command.Query, opts...)
		if err != nil {
			return actionableSearchError(err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return writeSearchPage(out, page, format)
	}

	return streamSearchPages(ctx, searcher, command.Query, opts, format, out)
}

func searchOptions(command parser.Search) ([]client.SearchOption, string, error) {
	mode := strings.ToLower(strings.TrimSpace(command.Mode))
	if mode == "" {
		mode = "server"
	}
	projection := strings.ToLower(strings.TrimSpace(command.Projection))
	if projection == "" {
		projection = "key-score"
	}
	format := strings.ToLower(strings.TrimSpace(command.Format))
	if format == "" {
		if command.All {
			format = "ndjson"
		} else {
			format = "json"
		}
	}
	if format != "json" && format != "ndjson" && format != "tsv" {
		return nil, "", fmt.Errorf("unknown search format %q (want json|ndjson|tsv)", command.Format)
	}
	if command.All && format == "json" {
		return nil, "", errors.New("all=true requires format=ndjson or format=tsv")
	}

	var cursor []byte
	var err error
	if command.Cursor != "" {
		cursor, err = base64.RawURLEncoding.DecodeString(command.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("cursor must be URL-safe base64 without padding: %w", err)
		}
	}

	var projectionValue client.SearchProjection
	switch projection {
	case "key-score":
		projectionValue = client.SearchProjectionKeyScore
	case "full-vertex":
		projectionValue = client.SearchProjectionFullVertex
	default:
		return nil, "", fmt.Errorf("unknown search projection %q (want key-score|full-vertex)", command.Projection)
	}

	opts := []client.SearchOption{
		client.WithSearchLimit(command.Limit),
		client.WithSearchPrefix(command.Prefix),
		client.WithSearchProjection(projectionValue),
		client.WithSearchCursor(cursor),
	}
	switch mode {
	case "server":
	case "any":
		opts = append(opts, client.WithMatchMode(client.MatchAny))
	case "all":
		opts = append(opts, client.WithMatchMode(client.MatchAll))
	case "min-should":
		opts = append(opts, client.WithMatchMode(client.MatchMinShould))
	default:
		return nil, "", fmt.Errorf("unknown search mode %q (want server|any|all|min-should)", command.Mode)
	}
	if command.MinShould != 0 {
		opts = append(opts, client.WithMinShouldMatch(command.MinShould))
	}
	if command.Fuzziness != 0 {
		opts = append(opts, client.WithFuzziness(command.Fuzziness))
	}
	if command.PrefixTerms {
		opts = append(opts, client.WithPrefixTerms())
	}
	if command.Phrase {
		opts = append(opts, client.WithPhrase())
	}
	if err := client.ValidateSearchOptions(opts...); err != nil {
		return nil, "", err
	}
	return opts, format, nil
}

func streamSearchPages(ctx context.Context, searcher searchClient, query string, opts []client.SearchOption, format string, out io.Writer) error {
	var tsv *csv.Writer
	if format == "tsv" {
		tsv = csv.NewWriter(out)
		tsv.Comma = '\t'
	}
	cursor := []byte(nil)
	for {
		pageOpts := append([]client.SearchOption(nil), opts...)
		if cursor != nil {
			pageOpts = append(pageOpts, client.WithSearchCursor(cursor))
		}
		page, err := searcher.SearchVerticesPage(ctx, query, pageOpts...)
		if err != nil {
			return actionableSearchError(err)
		}
		for _, hit := range page.Hits {
			if err := ctx.Err(); err != nil {
				return err
			}
			encoded, err := searchHitForOutput(hit)
			if err != nil {
				return err
			}
			if err := writeSearchHit(out, tsv, encoded, format); err != nil {
				return err
			}
		}
		if len(page.NextCursor) == 0 {
			if tsv != nil {
				tsv.Flush()
				if err := tsv.Error(); err != nil {
					return fmt.Errorf("write search TSV: %w", err)
				}
			}
			if page.ContinuationLimited {
				return actionableSearchError(client.ErrSearchContinuationLimited)
			}
			return nil
		}
		cursor = append(cursor[:0], page.NextCursor...)
	}
}

func writeSearchPage(out io.Writer, page client.SearchPage, format string) error {
	hits := make([]searchHitOutput, 0, len(page.Hits))
	for _, hit := range page.Hits {
		encoded, err := searchHitForOutput(hit)
		if err != nil {
			return err
		}
		hits = append(hits, encoded)
	}
	if format == "json" {
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(searchPageOutput{
			Hits:                hits,
			NextCursor:          base64.RawURLEncoding.EncodeToString(page.NextCursor),
			EffectiveLimit:      page.EffectiveLimit,
			Truncated:           page.Truncated,
			ContinuationLimited: page.ContinuationLimited,
		}); err != nil {
			return fmt.Errorf("write search page: %w", err)
		}
		return nil
	}
	var tsv *csv.Writer
	if format == "tsv" {
		tsv = csv.NewWriter(out)
		tsv.Comma = '\t'
	}
	for _, hit := range hits {
		if err := writeSearchHit(out, tsv, hit, format); err != nil {
			return err
		}
	}
	if tsv != nil {
		tsv.Flush()
		if err := tsv.Error(); err != nil {
			return fmt.Errorf("write search TSV: %w", err)
		}
	}
	return nil
}

func writeSearchHit(out io.Writer, tsv *csv.Writer, hit searchHitOutput, format string) error {
	switch format {
	case "ndjson":
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(hit); err != nil {
			return fmt.Errorf("write search NDJSON: %w", err)
		}
	case "tsv":
		if tsv == nil {
			return errors.New("search TSV encoder is nil")
		}
		if err := tsv.Write([]string{hit.Key, strconv.FormatFloat(hit.Score, 'g', -1, 64), hit.ProjectionStatus, string(hit.Vertex)}); err != nil {
			return fmt.Errorf("write search TSV: %w", err)
		}
		// csv.Writer owns an internal buffer. Flush each complete record so
		// all=true remains genuinely streaming; RFC-style quoting still keeps
		// embedded tabs/newlines lossless for a real TSV reader.
		tsv.Flush()
		if err := tsv.Error(); err != nil {
			return fmt.Errorf("write search TSV: %w", err)
		}
	default:
		return fmt.Errorf("unsupported streaming search format %q", format)
	}
	return nil
}

func searchProjectionStatusString(status client.SearchHitProjectionStatus) string {
	switch status {
	case client.SearchHitProjectionKeyScore:
		return "key-score"
	case client.SearchHitProjectionSnapshot:
		return "snapshot"
	case client.SearchHitProjectionMissing:
		return "missing"
	case client.SearchHitProjectionReplaced:
		return "replaced"
	default:
		return "unspecified"
	}
}

func searchHitForOutput(hit client.SearchHit) (searchHitOutput, error) {
	out := searchHitOutput{
		Key:              hit.Key,
		Score:            hit.Score,
		ProjectionStatus: searchProjectionStatusString(hit.ProjectionStatus),
	}
	if hit.Vertex != nil {
		vertex, err := client.MarshalVertexJSON(hit.Vertex)
		if err != nil {
			return searchHitOutput{}, fmt.Errorf("marshal projected vertex %q: %w", hit.Key, err)
		}
		out.Vertex = vertex
	}
	return out, nil
}

func actionableSearchError(err error) error {
	switch {
	case errors.Is(err, client.ErrSearchPositionsDisabled):
		return fmt.Errorf("phrase search is unavailable: restart the server with LANTERN_SEARCH_POSITIONS=true or omit phrase: %w", err)
	case errors.Is(err, client.ErrSearchDisabled):
		return fmt.Errorf("content search is unavailable: restart the server with LANTERN_SEARCH_ENABLED=true: %w", err)
	case errors.Is(err, client.ErrSearchIndexIncomplete):
		return fmt.Errorf("content search is unavailable: the local index is incomplete and requires a bounded rebuild: %w", err)
	case errors.Is(err, client.ErrSearchCursorStale):
		return fmt.Errorf("search cursor is stale: restart explicitly from page 1 on the same endpoint: %w", err)
	case errors.Is(err, client.ErrSearchCursorInvalid):
		return fmt.Errorf("search cursor is invalid for this query, option set, projection, config, or endpoint: %w", err)
	case errors.Is(err, client.ErrSearchContinuationLimited):
		return fmt.Errorf("search continuation reached the server's bounded session cap; narrow the query or raise the server session limits: %w", err)
	default:
		return err
	}
}
