package network

import (
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/xnet"
	"time"
)

// MQTT and Moonraker remain open simultaneously. These are active TCP port
// slots, not the listener's pool size. DHCP and DNS have separate UDP slots.
const TCPPorts = 2
const UDPPorts = 2
const PassivePeers = 4
const PollInterval = 5 * time.Millisecond

var BackoffPoll = lneto.BackoffStrategy(func(uint) time.Duration { return PollInterval })

func GoStack(stack *xnet.StackAsync) xnet.StackGo {
	return stack.StackGo(BackoffPoll, xnet.StackGoConfig{
		TCPDialTimeout: 2 * time.Second, TCPDialRetries: 1,
		ListenerPoolConfig: xnet.TCPPoolConfig{
			PoolSize: 2, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024,
			EstablishedTimeout: 2 * time.Second, ClosingTimeout: 2 * time.Second,
			NewBackoff: func() lneto.BackoffStrategy { return BackoffPoll },
		},
	})
}
