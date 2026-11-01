package provider

import (
	"net"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

func TestNewListenerRequiresCertifiedReplicationSendLimit(t *testing.T) {
	config := NetConfig{Port: 0, MaxRecvMsgBytes: 1024, MaxSendMsgBytes: 512}
	certified := runtimeCertified{valid: true, replicationSendMaxBytes: 256}
	publicReceipts := publicReceiptsCertified{valid: true}
	if listener, cleanup, err := NewListener(config, certified, publicReceipts, nil); err == nil {
		cleanup()
		listener.Close()
		t.Fatal("NewListener accepted a send limit different from frame admission")
	} else if !strings.Contains(err.Error(), "differs from certified replication frame admission") {
		t.Fatalf("NewListener mismatch error = %v", err)
	}

	certified.replicationSendMaxBytes = config.MaxSendMsgBytes
	listener, cleanup, err := NewListener(config, certified, publicReceipts, nil)
	if err != nil {
		t.Fatalf("NewListener matching limit: %v", err)
	}
	if listener == nil {
		t.Fatal("NewListener matching limit returned nil listener")
	}
	cleanup()
}

func TestNewListenerAdoptionRequiresCertification(t *testing.T) {
	for _, phase := range []string{"runtime", "receipts", "frame", "negative_limit"} {
		t.Run(phase, func(t *testing.T) {
			owner, err := listenerlaunch.Reserve(t.Context(), false)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			address, _ := owner.Addresses()
			addr, err := net.ResolveTCPAddr("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			config := NetConfig{Port: addr.Port, MaxSendMsgBytes: 512}
			certified := runtimeCertified{valid: true, replicationSendMaxBytes: 512}
			receipts := publicReceiptsCertified{valid: true}
			switch phase {
			case "runtime":
				certified.valid = false
			case "receipts":
				receipts.valid = false
			case "frame":
				certified.replicationSendMaxBytes = 256
			case "negative_limit":
				config.MaxRecvMsgBytes = -1
			}
			if _, cleanup, err := NewListener(config, certified, receipts, owner); err == nil {
				cleanup()
				t.Fatal("uncertified adoption")
			}
			if owner.Adopted() == nil {
				t.Fatal("failed guard consumed listener")
			}
			config.MaxRecvMsgBytes = 0
			certified.valid = true
			certified.replicationSendMaxBytes = 512
			receipts.valid = true
			listener, cleanup, err := NewListener(config, certified, receipts, owner)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if listener.Addr().String() != address || owner.Adopted() != nil {
				t.Fatal("certified adoption replaced socket")
			}
		})
	}
}
