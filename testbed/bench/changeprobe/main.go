// Command changeprobe measures an application-owned public CDC consumer.
// It resumes only its last fully applied opaque cursor after a bounded stream,
// and records gaps/rebootstrap separately. It never calls private replication.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"os"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/encoding/protojson"
)

type consumerReport struct {
	Frames        uint64 `json:"frames"`
	Invalidations uint64 `json:"invalidations"`
	CursorCommits uint64 `json:"cursor_commits"`
	Reopens       uint64 `json:"reopens"`
	Gaps          uint64 `json:"gaps"`
	Failure       string `json:"failure_category,omitempty"`
}

type changeSource interface {
	WatchChanges(context.Context, client.WatchChangesOptions) iter.Seq2[*client.ChangeFrame, error]
}

func consume(ctx context.Context, source changeSource, options client.WatchChangesOptions) (consumerReport, error) {
	var report consumerReport
	for {
		var failure error
		for frame, err := range source.WatchChanges(ctx, options) {
			if err != nil {
				failure = err
				break
			}
			// This diagnostic consumer has no resident cache. Counting each
			// invalidation is its effect; commit follows the entire frame.
			report.Frames++
			report.Invalidations += uint64(len(frame.Invalidations))
			if len(frame.Cursor.Bytes()) > 0 {
				options.Cursor = frame.Cursor
				options.Bootstrap = false
				report.CursorCommits++
			}
		}
		if ctx.Err() != nil {
			if report.CursorCommits == 0 {
				return consumerFailure(report, "no_progress", ctx.Err())
			}
			return report, nil
		}
		// Admission bounds close a valid stream. Reopen the same endpoint,
		// using the last applied cursor, with fresh Server authorization.
		switch connect.CodeOf(failure) {
		case connect.CodeDeadlineExceeded:
			if options.Bootstrap {
				return consumerFailure(report, "bootstrap_timeout", failure)
			}
		case connect.CodeFailedPrecondition:
			report.Gaps++
			options.Bootstrap, options.Cursor = true, client.ScopedChangeCursor{}
		default:
			category := "rpc_" + connect.CodeOf(failure).String()
			if errors.Is(failure, client.ErrChangeGap) {
				category = "protocol_gap"
			}
			return consumerFailure(report, category, failure)
		}
		report.Reopens++
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return report, nil
		case <-timer.C:
		}
	}
}

func consumerFailure(report consumerReport, category string, cause error) (consumerReport, error) {
	report.Failure = category
	if cause == nil {
		cause = errors.New("stream ended without a terminal status")
	}
	return report, fmt.Errorf("%s: %w", category, cause)
}

func writeReport(output string, report consumerReport) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(raw, '\n'), 0600)
}

// Detailed transport errors may contain endpoint or application context. Keep
// them outside public artifact globs; only the fixed category enters a report.
func privateFailure(cause error) (string, error) {
	directory, err := os.MkdirTemp("", "lantern-changeprobe-failure-")
	if err != nil {
		return "", err
	}
	detail := cause.Error()
	if len(detail) > 64<<10 {
		detail = detail[:64<<10]
	}
	if err := os.WriteFile(directory+"/failure.log", []byte(detail+"\n"), 0600); err != nil {
		return directory, err
	}
	return directory, nil
}

func main() {
	endpoint := flag.String("endpoint", "", "fixed public endpoint")
	request := flag.String("request", "", "WatchChanges JSON request, bootstrap only")
	duration := flag.Duration("duration", 0, "complete consumer duration")
	output := flag.String("out", "", "content-free JSON report")
	caFile := flag.String("ca-file", "", "public CA for HTTPS")
	flag.Parse()
	if err := run(*endpoint, *request, *duration, *output, *caFile); err != nil {
		fmt.Fprintln(os.Stderr, "changeprobe: public consumer validation failed")
		if directory, privateErr := privateFailure(err); privateErr == nil {
			fmt.Fprintln(os.Stderr, "changeprobe: private failure evidence retained at", directory)
		} else {
			fmt.Fprintln(os.Stderr, "changeprobe: could not retain private failure evidence")
		}
		os.Exit(1)
	}
}

func run(endpoint, raw string, duration time.Duration, output, caFile string) error {
	var request pb.WatchChangesRequest
	if protojson.Unmarshal([]byte(raw), &request) != nil || !request.Bootstrap || len(request.Cursor) != 0 || duration <= 0 || output == "" {
		return errors.New("invalid bootstrap request or duration")
	}
	options := client.WatchChangesOptions{Prefix: request.Prefix, Bootstrap: true}
	switch request.Projection {
	case pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY:
		options.Projection = client.ChangeIdentity
	case pb.ChangeProjection_CHANGE_PROJECTION_VALUE:
		options.Projection = client.ChangeValue
	default:
		return errors.New("explicit projection required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid public origin")
	}
	sdkOptions := []client.Option{client.WithConnectClientOption(connect.WithGRPC())}
	if token := os.Getenv("LANTERN_BENCH_AUTH_TOKEN"); token != "" {
		if parsed.Scheme != "https" {
			return errors.New("credential requires HTTPS")
		}
		sdkOptions = append(sdkOptions, client.WithAuthToken(token))
	}
	if parsed.Scheme == "https" {
		raw, err := os.ReadFile(caFile)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(raw) {
			return errors.New("invalid public CA")
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
		defer transport.CloseIdleConnections()
		sdkOptions = append(sdkOptions, client.WithHTTPClient(&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}))
	} else if parsed.Scheme != "http" || parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return errors.New("OFF requires an explicit local origin")
	}
	sdk, err := client.NewLantern(endpoint, sdkOptions...)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	report, err := consume(ctx, sdk, options)
	return errors.Join(err, writeReport(output, report))
}
