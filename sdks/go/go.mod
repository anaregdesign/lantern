module github.com/anaregdesign/lantern/sdks/go

go 1.26

require (
	connectrpc.com/connect v1.20.0
	github.com/anaregdesign/lantern/core v0.17.0
	github.com/anaregdesign/lantern/pb v0.11.0
	golang.org/x/net v0.56.0
	google.golang.org/protobuf v1.36.11
)

require golang.org/x/text v0.38.0 // indirect

replace github.com/anaregdesign/lantern/pb => ../../pb
