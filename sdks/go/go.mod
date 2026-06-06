module github.com/anaregdesign/lantern/sdks/go

go 1.26

require (
	connectrpc.com/connect v1.20.0
	github.com/anaregdesign/lantern/pb v0.1.1
	golang.org/x/net v0.55.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.81.1 // indirect
)

replace github.com/anaregdesign/lantern/pb => ../../pb
