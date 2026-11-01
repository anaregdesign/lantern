package provider

import (
	"errors"
	"net"
	"strconv"

	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
)

func NewListener(
	n NetConfig,
	certified runtimeCertified,
	publicReceipts publicReceiptsCertified,
	launch *listenerlaunch.Owner,
) (net.Listener, func(), error) {
	if !certified.valid || !publicReceipts.valid {
		return nil, nil, errors.New("serving runtime or public receipt state is not certified")
	}
	if n.MaxRecvMsgBytes < 0 || n.MaxSendMsgBytes < 0 {
		return nil, nil, errors.New("connect message size limits must be zero (unlimited) or positive")
	}
	if certified.replicationSendMaxBytes != n.MaxSendMsgBytes {
		return nil, nil, errors.New("listener send limit differs from certified replication frame admission")
	}
	var listener net.Listener
	var err error
	if launch != nil {
		listener, err = launch.Public(n.Port)
	} else {
		listener, err = net.Listen("tcp", ":"+strconv.Itoa(n.Port))
	}
	if err != nil {
		return nil, nil, err
	}
	return listener, func() { _ = listener.Close() }, nil
}
