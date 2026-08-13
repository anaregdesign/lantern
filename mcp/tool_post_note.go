package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type postNoteInput struct {
	Text     string   `json:"text" jsonschema:"The signal, one or two sentences (e.g. 'build broken on main since 14:02, suspect PR 866'). Write for a sibling agent that has zero context."`
	Severity string   `json:"severity,omitempty" jsonschema:"Optional severity: info | warn | blocker. Default info."`
	Links    []string `json:"links,omitempty" jsonschema:"Resource keys this note is about (e.g. ['repo.lantern.ci','branch.main']). Linking is what makes the note surface in whats_happening for those resources."`
	TTL      string   `json:"ttl" jsonschema:"Required decay horizon (string) - when will this signal stop being true? Buckets (monotonic): seconds (~30s) | transient (~2m) | turn (~10m) | conversation (~1h) | task (~4h) | workday (~12h) | day (~24h) | week (~7d) | sprint (~14d) | month (~30d) | quarter (~90d) | durable (~180d). Pick the SHORTER plausible bucket; a stale warning is noise."`
}

type postNoteOutput struct {
	NoteID    string   `json:"note_id"`
	Author    string   `json:"author"`
	Severity  string   `json:"severity"`
	Links     []string `json:"links,omitempty"`
	ExpiresAt string   `json:"expires_at"`
}

const postNoteDescription = "Post a blackboard note the whole fleet can see: environment state, hazards, coordination signals ('build broken on main', 'API X flaky, backoff in place', 'migrating table users — writes frozen'). Link it to the resource keys it concerns so whats_happening surfaces it to anyone working nearby. Notes decay on the TTL you choose and vanish on their own — post facts about NOW, not documentation. severity=blocker is the loudest signal; use it sparingly."

func registerPostNote(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "post_note",
		Description: postNoteDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postNoteInput) (*mcp.CallToolResult, postNoteOutput, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, postNoteOutput{}, fmt.Errorf("post_note: text must not be empty")
		}
		sev := in.Severity
		switch sev {
		case "":
			sev = "info"
		case "info", "warn", "blocker":
		default:
			return nil, postNoteOutput{}, fmt.Errorf("post_note: severity %q (want info|warn|blocker)", in.Severity)
		}
		bucket, err := ttl.ParseBucket(in.TTL)
		if err != nil {
			return nil, postNoteOutput{}, fmt.Errorf("post_note: %w", err)
		}

		author := identity.Resolve()
		var rnd [4]byte
		_, _ = rand.Read(rnd[:])
		noteID := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(rnd[:]))
		key := noteKeyPrefix + noteID

		payload, err := encodeRecord(noteRecord{Text: in.Text, Author: author, Severity: sev, Links: in.Links})
		if err != nil {
			return nil, postNoteOutput{}, fmt.Errorf("post_note: %w", err)
		}
		d, _ := r.ResolveCapped(bucket)
		outcome, err := lc.PutVertex(ctx, key, payload, d)
		if err != nil {
			return nil, postNoteOutput{}, mapSDKError("post_note", err)
		}
		if err := requireAppliedPut("post_note", outcome); err != nil {
			return nil, postNoteOutput{}, err
		}

		// Link note→resource so the note rides into whats_happening's
		// neighborhood for each linked resource, and author→note so the
		// note shows up around its author too.
		edges := make([]client.EdgeInput, 0, len(in.Links)+1)
		for _, res := range in.Links {
			if strings.TrimSpace(res) == "" {
				continue
			}
			edges = append(edges, client.EdgeInput{Tail: key, Head: res, Weight: 1, Expiration: expirationIn(d)})
		}
		edges = append(edges, client.EdgeInput{Tail: agentKey(author), Head: key, Weight: 1, Expiration: expirationIn(d)})
		if _, err := lc.AddEdges(ctx, edges); err != nil {
			return nil, postNoteOutput{}, mapSDKError("post_note", err)
		}

		out := postNoteOutput{
			NoteID:    noteID,
			Author:    author,
			Severity:  sev,
			Links:     in.Links,
			ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339),
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"Posted %s note %s (expires≈%s)%s.",
				sev, noteID, out.ExpiresAt, linkSuffix(in.Links))}},
		}, out, nil
	})
}

func linkSuffix(links []string) string {
	if len(links) == 0 {
		return ""
	}
	return fmt.Sprintf(", linked to %s", strings.Join(links, ", "))
}
