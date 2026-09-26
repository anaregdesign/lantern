module github.com/anaregdesign/lantern/mcp

go 1.27.0

require (
	connectrpc.com/grpchealth v1.5.0
	github.com/anaregdesign/lantern/pb v0.13.1
	github.com/anaregdesign/lantern/sdks/go v0.24.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	google.golang.org/protobuf v1.36.12
)

require (
	connectrpc.com/connect v1.21.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace (
	github.com/anaregdesign/lantern/pb => ../pb
	github.com/anaregdesign/lantern/sdks/go => ../sdks/go
)
