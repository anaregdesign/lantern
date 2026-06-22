module github.com/anaregdesign/lantern

go 1.26

require (
	connectrpc.com/connect v1.20.0
	connectrpc.com/grpchealth v1.4.0
	github.com/anaregdesign/lantern/core v0.13.0
	github.com/anaregdesign/lantern/mcp v0.0.0-00010101000000-000000000000
	github.com/anaregdesign/lantern/pb v0.8.0
	github.com/anaregdesign/lantern/sdks/go v0.18.0
	github.com/anaregdesign/lantern/server v0.0.0-00010101000000-000000000000
	github.com/manifoldco/promptui v0.9.0
	github.com/modelcontextprotocol/go-sdk v1.6.1
	github.com/spf13/cobra v1.10.2
	golang.org/x/net v0.56.0
	google.golang.org/protobuf v1.36.11
)

require (
	connectrpc.com/grpcreflect v1.3.0 // indirect
	github.com/RoaringBitmap/roaring/v2 v2.18.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.24.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chzyer/readline v0.0.0-20180603132655-2972be24d48e // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.81.1 // indirect
)

replace (
	github.com/anaregdesign/lantern/core => ./core
	github.com/anaregdesign/lantern/mcp => ./mcp
	github.com/anaregdesign/lantern/pb => ./pb
	github.com/anaregdesign/lantern/sdks/go => ./sdks/go
	github.com/anaregdesign/lantern/server => ./server
)
