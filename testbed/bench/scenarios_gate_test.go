// Package bench_test hosts cross-cutting gates over the bench-harness
// scenario corpus (testbed/bench/scenarios/*.yaml).
//
// TestScenarioTemplates_MatchWireSchema is the schema-drift gate (#934):
// ghz resolves request messages via server reflection and rejects any JSON
// key that is not a field of the resolved message, so a proto change that
// retires a field silently orphans every scenario template that still sends
// it — the failure then only surfaces as a red nightly leak-gate run, days
// after the schema PR merged (exactly how #866 broke broad_illuminate).
// This test reproduces ghz's construction path at `go test ./...` time: it
// renders every data_template with ghz-style template data and unmarshals
// the result via protojson (which, like ghz, accepts both original and
// lowerCamel field names and rejects unknown ones) against the request
// message resolved from the scenario's `call` name.
package bench_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"

	// Register the graph.v1 file descriptors (messages + both services) in
	// protoregistry.GlobalFiles so `call` names resolve.
	_ "github.com/anaregdesign/lantern/pb/graph/v1"
)

// scenarioCall is one (call, data_template) pair wherever it appears in a
// scenario document.
type scenarioCall struct {
	Call         string `yaml:"call"`
	DataTemplate string `yaml:"data_template"`
}

// scenarioDoc is the subset of the scenario schema that names RPCs. Keep in
// sync with the extraction sites in testbed/bench/run.sh (target.call,
// target.calls[], subscribe, subscribe.consumers[]).
type scenarioDoc struct {
	Name   string `yaml:"name"`
	Target struct {
		Call         string         `yaml:"call"`
		DataTemplate string         `yaml:"data_template"`
		Calls        []scenarioCall `yaml:"calls"`
	} `yaml:"target"`
	Subscribe struct {
		Call         string         `yaml:"call"`
		DataTemplate string         `yaml:"data_template"`
		Consumers    []scenarioCall `yaml:"consumers"`
	} `yaml:"subscribe"`
}

// calls flattens every (call, data_template) pair in the document, tagged
// with where it came from so failures point at the offending YAML path.
func (d scenarioDoc) calls() map[string]scenarioCall {
	out := map[string]scenarioCall{}
	if d.Target.Call != "" {
		out["target"] = scenarioCall{d.Target.Call, d.Target.DataTemplate}
	}
	for i, c := range d.Target.Calls {
		out[fmt.Sprintf("target.calls[%d]", i)] = c
	}
	if d.Subscribe.Call != "" {
		out["subscribe"] = scenarioCall{d.Subscribe.Call, d.Subscribe.DataTemplate}
	}
	for i, c := range d.Subscribe.Consumers {
		out[fmt.Sprintf("subscribe.consumers[%d]", i)] = c
	}
	return out
}

// ghzTemplateData mirrors the call-scoped variables ghz injects into
// data_template. Only the fields the scenario corpus actually uses are
// modelled; extend when a scenario starts using more of ghz's surface.
type ghzTemplateData struct {
	RequestNumber int64
	Timestamp     string
}

// ghzFuncs approximates the sprig subset the corpus uses (ghz embeds sprig).
// Numeric args are `any` because sprig coerces (cast.ToInt64) — templates mix
// int64 fields with literal ints.
func ghzFuncs() template.FuncMap {
	toInt64 := func(v any) int64 {
		switch n := v.(type) {
		case int:
			return int64(n)
		case int64:
			return n
		default:
			panic(fmt.Sprintf("unsupported integer type %T", v))
		}
	}
	return template.FuncMap{
		"mod": func(a, b any) int64 { return toInt64(a) % toInt64(b) },
		"add": func(vs ...any) int64 {
			var sum int64
			for _, v := range vs {
				sum += toInt64(v)
			}
			return sum
		},
		"b64enc": func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"now":    time.Now,
		"dateModify": func(d string, t time.Time) (time.Time, error) {
			dur, err := time.ParseDuration(d)
			if err != nil {
				return time.Time{}, err
			}
			return t.Add(dur), nil
		},
		"date": func(layout string, t time.Time) string { return t.Format(layout) },
	}
}

// requestDescriptor resolves "pkg.Service/Method" to the method's input
// message descriptor via the global protoregistry — the same resolution ghz
// performs over server reflection.
func requestDescriptor(call string) (protoreflect.MessageDescriptor, error) {
	svcName, method, ok := strings.Cut(call, "/")
	if !ok {
		return nil, fmt.Errorf("call %q is not Service/Method", call)
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(svcName))
	if err != nil {
		return nil, fmt.Errorf("service %q not registered: %w", svcName, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a service", svcName)
	}
	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("service %q has no method %q", svcName, method)
	}
	return md.Input(), nil
}

func TestScenarioTemplates_MatchWireSchema(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("scenarios", "*.yaml"))
	if err != nil {
		t.Fatalf("glob scenarios: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no scenario yamls found — glob root moved?")
	}

	// Two samples: RequestNumber 0 exercises the zero/modulo edge, a large
	// value exercises the padded printf/b64enc contrib-id paths.
	samples := []ghzTemplateData{
		{RequestNumber: 0, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)},
		{RequestNumber: 987654321, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)},
	}

	for _, path := range paths {
		stem := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(stem, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var doc scenarioDoc
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse yaml: %v", err)
			}
			calls := doc.calls()
			if len(calls) == 0 {
				t.Fatal("scenario declares no target/subscribe calls — run.sh could not drive it")
			}
			for site, c := range calls {
				if c.Call == "" {
					t.Errorf("%s: empty call", site)
					continue
				}
				if strings.TrimSpace(c.DataTemplate) == "" {
					t.Errorf("%s (%s): empty data_template", site, c.Call)
					continue
				}
				desc, err := requestDescriptor(c.Call)
				if err != nil {
					t.Errorf("%s: %v", site, err)
					continue
				}
				tmpl, err := template.New(site).Funcs(ghzFuncs()).Parse(c.DataTemplate)
				if err != nil {
					t.Errorf("%s (%s): parse template: %v", site, c.Call, err)
					continue
				}
				for _, data := range samples {
					var sb strings.Builder
					if err := tmpl.Execute(&sb, data); err != nil {
						t.Errorf("%s (%s): render @%d: %v", site, c.Call, data.RequestNumber, err)
						continue
					}
					msg := dynamicpb.NewMessage(desc)
					if err := protojson.Unmarshal([]byte(sb.String()), msg); err != nil {
						t.Errorf("%s (%s): rendered template does not match %s:\n  %s\n  %v",
							site, c.Call, desc.FullName(), strings.TrimSpace(sb.String()), err)
					}
				}
			}
		})
	}
}
