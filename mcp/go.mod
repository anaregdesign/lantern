module github.com/anaregdesign/lantern/mcp

go 1.26

require (
	github.com/anaregdesign/lantern/pb v0.3.0
	github.com/anaregdesign/lantern/sdks/go v0.11.0
	github.com/modelcontextprotocol/go-sdk v1.6.1
)

require (
	connectrpc.com/connect v1.20.0 // indirect
	github.com/anaregdesign/lantern/core v0.3.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace (
	github.com/anaregdesign/lantern/pb => ../pb
	github.com/anaregdesign/lantern/sdks/go => ../sdks/go
)
