package service

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/anaregdesign/lantern/cli/parser"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// CLIService owns the shared REPL/one-shot dispatcher and its output sink.
// Feature-specific execution stays in coherent siblings such as search.go.
type CLIService struct {
	client *client.Lantern
	out    io.Writer
}

// Option configures CLIService without splitting the REPL and one-shot
// execution paths. WithOutput is primarily used by Cobra and tests; the REPL
// keeps stdout as its default.
type Option func(*CLIService)

func WithOutput(w io.Writer) Option {
	return func(service *CLIService) {
		if w != nil {
			service.out = w
		}
	}
}

func NewCLIService(client *client.Lantern, opts ...Option) *CLIService {
	service := &CLIService{
		client: client,
		out:    os.Stdout,
	}
	for _, apply := range opts {
		apply(service)
	}
	return service
}

func (c *CLIService) Run(ctx context.Context, str string) error {
	s, err := parser.NewSource(str)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return ErrInvalidVerb
	}
	return c.runSource(ctx, s)
}

// RunArgs dispatches an already-split token stream (verb + arguments)
// through the same grammar the REPL parses from a raw line. The one-shot
// verb-first CLI commands (`lantern-cli get vertex <key>`, …) call this with
// cobra's argv so the one-liner surface and the REPL share exactly one
// grammar and one dispatcher (#672).
func (c *CLIService) RunArgs(ctx context.Context, args []string) error {
	return c.runSource(ctx, parser.NewSourceFromTokens(args))
}
