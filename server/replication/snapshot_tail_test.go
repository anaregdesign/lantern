package replication

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type tailMetadataTestStream struct {
	snapshotSliceStream
	headers http.Header
}

func (s *tailMetadataTestStream) ResponseHeader() http.Header { return s.headers }

func TestSnapshotTailMetadataValidatedBeforeInstall(t *testing.T) {
	token := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name    string
		require bool
		values  []string
		valid   bool
	}{
		{name: "required-valid", require: true, values: []string{token}, valid: true},
		{name: "required-missing", require: true},
		{name: "required-empty", require: true, values: []string{""}},
		{name: "required-duplicate", require: true, values: []string{token, token}},
		{name: "required-short", require: true, values: []string{"ab"}},
		{name: "required-nonhex", require: true, values: []string{strings.Repeat("z", 64)}},
		{name: "legacy-absent", valid: true},
		{name: "legacy-unsolicited", values: []string{token}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			format := pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			underlying := &tailMetadataTestStream{
				snapshotSliceStream: snapshotSliceStream{frames: []*pb.SnapshotResponse{
					{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{Format: format}}},
					{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{}}},
				}},
				headers: http.Header{snapshotTailHeader: tc.values},
			}
			installer := &scriptedSnapshotInstaller{required: format}
			stream := &snapshotTailStream{stream: underlying, require: tc.require}
			_, err := installSnapshot(context.Background(), installer, stream)
			if tc.valid {
				if err != nil || installer.installCount() != 1 {
					t.Fatalf("valid metadata: error=%v installs=%d", err, installer.installCount())
				}
				if tc.require && stream.token != token {
					t.Fatalf("token = %q", stream.token)
				}
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition || installer.installCount() != 0 {
				t.Fatalf("invalid metadata reached installer: error=%v installs=%d", err, installer.installCount())
			}
		})
	}
}
