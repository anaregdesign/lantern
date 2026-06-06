// Package cmd implements the `lantern` CLI: a cobra-based command tree that
// exposes every Lantern gRPC RPC as a one-shot subcommand and ships the legacy
// interactive prompt as `lantern repl`.
//
// The help text on every subcommand is intentionally long-form and
// example-heavy because this CLI is expected to be driven by both humans and
// by generative-AI coding agents. Each command documents its semantics,
// flags, output shape, and exit codes so an agent can use the CLI without
// reading server source.
package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	"github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

// connection / transport flags (global)
var (
	flagHost        string
	flagPort        int
	flagAddress     string
	flagTimeout     time.Duration
	flagTLS         bool
	flagTLSCA       string
	flagTLSCert     string
	flagTLSKey      string
	flagTLSServer   string
	flagInsecureTLS bool
	flagCompression string
	flagChunkSize   int
)

// rootCmd is the top-level command. It prints help when invoked with no
// subcommand so that "lantern" alone is a no-op rather than dropping into a
// REPL — the REPL is now `lantern repl`.
var rootCmd = &cobra.Command{
	Use:   "lantern",
	Short: "CLI for the Lantern in-memory key-vertex store",
	Long: `lantern is the official command-line client for the Lantern gRPC server
(github.com/anaregdesign/lantern).

WHAT LANTERN IS
  Lantern is an in-memory graph KVS. Every vertex and every edge carries a
  TTL and decays on its own; edges are additive (AddEdge sums weight onto
  the same (tail, head) pair, PutEdge replaces it). The server exposes a
  small gRPC surface — this CLI wraps every RPC as a scriptable subcommand
  plus an interactive REPL.

COMMAND LAYOUT
  vertex      get / put / delete vertices (single or batch)
  edge        get / add / put / delete edges (single or batch)
  illuminate  walk the graph from a seed; optional server-side optimization
  bulk        stream NDJSON from a file or stdin (bulk load)
  repl        legacy interactive prompt
  version     print client version
  completion  generate shell completions (bash, zsh, fish, powershell)

GLOBAL CONNECTION FLAGS
  -H, --host         server hostname (default "localhost")
  -p, --port         server port (default 6380)
      --address      full host:port; overrides --host/--port when set
      --timeout      per-RPC deadline (default 5s, 0 disables)
      --tls          dial with TLS instead of plaintext
      --tls-ca       path to a PEM bundle for verifying the server cert
      --tls-cert     client cert (mTLS)
      --tls-key      client key (mTLS)
      --tls-server-name  override the SNI / verification hostname
      --insecure-skip-verify  skip server cert verification (dev only)
      --compression  per-call compressor: "none" (default) or "gzip"
      --chunk-size   batch chunk size for multi-key write/delete (default 1000)

OUTPUT
  All read commands print JSON on stdout. All write commands print a single
  "OK" line on stdout when they succeed. Errors go to stderr.

EXIT CODES
  0  success
  1  invalid arguments or local error (parse, file I/O)
  2  gRPC error returned by the server (NotFound, InvalidArgument, …)

EXAMPLES
  # one-shot writes against a local server
  lantern vertex put alice '{"name":"Alice"}' --value-type json --ttl 1h
  lantern edge add alice bob 1.5 --ttl 30m
  lantern edge add alice bob 0.5      # weight now totals 2.0

  # batch delete (uses DeleteVertices)
  lantern vertex delete alice bob carol

  # walk from a seed and emit the 1-hop neighborhood as JSON
  lantern illuminate alice --step 1 --k 10

  # against a TLS server
  lantern --tls --tls-ca ./ca.pem -H lantern.example.com -p 443 vertex get alice
`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command. It is the only function main.go calls.
func Execute() {
	if err := rootCmd.ExecuteContext(signalContext()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		// Exit code 2 if the error came from the server (gRPC errors carry an
		// "rpc error:" prefix). Use 1 for everything else.
		if strings.Contains(err.Error(), "rpc error") {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// signalContext returns a context that is cancelled on SIGINT / SIGTERM so
// long-running commands (REPL, bulk loaders) shut down cleanly.
func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagHost, "host", "H", "localhost", "server hostname")
	pf.IntVarP(&flagPort, "port", "p", 6380, "server port")
	pf.StringVar(&flagAddress, "address", "", "full host:port (overrides --host/--port)")
	pf.DurationVar(&flagTimeout, "timeout", 5*time.Second, "per-RPC timeout (0 disables)")
	pf.BoolVar(&flagTLS, "tls", false, "use TLS")
	pf.StringVar(&flagTLSCA, "tls-ca", "", "PEM bundle for server cert verification")
	pf.StringVar(&flagTLSCert, "tls-cert", "", "client certificate for mTLS")
	pf.StringVar(&flagTLSKey, "tls-key", "", "client key for mTLS")
	pf.StringVar(&flagTLSServer, "tls-server-name", "", "SNI / verification hostname override")
	pf.BoolVar(&flagInsecureTLS, "insecure-skip-verify", false, "skip server cert verification (DEV ONLY)")
	pf.StringVar(&flagCompression, "compression", "none", `per-call compressor: "none" or "gzip"`)
	pf.IntVar(&flagChunkSize, "chunk-size", 1000, "batch chunk size for multi-key write/delete")
}

// target returns the resolved server URL (with scheme) the SDK
// expects since the Connect-only collapse (#367). Flag semantics
// stay the same: --host/--port build a host:port pair, --address
// overrides it, and --tls flips http → https.
func target() string {
	hostport := flagAddress
	if hostport == "" {
		hostport = net.JoinHostPort(flagHost, strconv.Itoa(flagPort))
	}
	scheme := "http"
	if flagTLS || flagTLSCA != "" || flagTLSCert != "" {
		scheme = "https"
	}
	return scheme + "://" + hostport
}

// dial constructs a *client.Lantern using the resolved global flags.
// The caller is responsible for calling Close on the returned client.
func dial() (*client.Lantern, error) {
	opts := []client.Option{
		client.WithDefaultTimeout(flagTimeout),
		client.WithBatchChunkSize(flagChunkSize),
	}
	if flagCompression != "" && flagCompression != "none" {
		opts = append(opts, client.WithConnectClientOption(
			connect.WithSendCompression(flagCompression),
		))
	}
	hc, err := buildHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("http client: %w", err)
	}
	opts = append(opts, client.WithHTTPClient(hc))
	return client.NewLantern(target(), opts...)
}

// buildHTTPClient returns an h2c-flavoured *http.Client when TLS is
// off, or a real TLS-backed http2.Transport when --tls / --tls-ca /
// --tls-cert is supplied. Replaces the legacy buildTLSCreds /
// credentials.NewTLS path that fed gRPC dial options.
func buildHTTPClient() (*http.Client, error) {
	if !(flagTLS || flagTLSCA != "" || flagTLSCert != "") {
		// Plain h2c: same pattern as sdks/go/connect_h2c.go's
		// defaultH2CClient (kept inline so the CLI does not have to
		// reach into an SDK internal helper).
		return &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, addr)
				},
			},
		}, nil
	}
	cfg, err := buildTLSConfig()
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}}, nil
}

func buildTLSConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		ServerName:         flagTLSServer,
		InsecureSkipVerify: flagInsecureTLS, //nolint:gosec // opt-in via flag
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"h2"},
	}
	if flagTLSCA != "" {
		caPEM, err := os.ReadFile(flagTLSCA)
		if err != nil {
			return nil, fmt.Errorf("read CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA bundle %q", flagTLSCA)
		}
		cfg.RootCAs = pool
	}
	if flagTLSCert != "" || flagTLSKey != "" {
		if flagTLSCert == "" || flagTLSKey == "" {
			return nil, fmt.Errorf("--tls-cert and --tls-key must be supplied together")
		}
		pair, err := tls.LoadX509KeyPair(flagTLSCert, flagTLSKey)
		if err != nil {
			return nil, fmt.Errorf("load client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}
