module github.com/anaregdesign/lantern/sdks/go

go 1.27.3

require (
	connectrpc.com/connect v1.21.0
	github.com/anaregdesign/lantern/pb v0.15.0
	google.golang.org/protobuf v1.36.12
)

replace github.com/anaregdesign/lantern/pb => ../../pb
