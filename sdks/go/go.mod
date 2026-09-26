module github.com/anaregdesign/lantern/sdks/go

go 1.27.0

require (
	connectrpc.com/connect v1.21.0
	github.com/anaregdesign/lantern/pb v0.13.1
	google.golang.org/protobuf v1.36.12
)

replace github.com/anaregdesign/lantern/pb => ../../pb
