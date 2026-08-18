module github.com/anaregdesign/lantern/mcp

go 1.26

require (
	connectrpc.com/grpchealth v1.5.0
	github.com/anaregdesign/lantern/pb v0.12.0
	github.com/anaregdesign/lantern/sdks/go v0.24.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
	google.golang.org/protobuf v1.36.12
)

require (
	connectrpc.com/connect v1.20.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace (
	github.com/anaregdesign/lantern/pb => ../pb
	github.com/anaregdesign/lantern/sdks/go => ../sdks/go
)
