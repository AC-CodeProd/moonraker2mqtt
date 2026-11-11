package tcp

import (
	"errors"
	"github.com/soypat/lneto"
	"math"
	"net"
	"sync"
)

var ErrDialStorageBusy = errors.New("native dial storage busy")
var ErrDialStorageExhausted = errors.New("native dial storage token exhausted")

// DialStorage owns one fixed backing set. It must never be copied after Init.
// Conn, Handler, mutex and the token remain fresh for every acquisition.
// This is not a pool and does not provide an allocation fallback.
type DialStorage struct {
	mu          sync.Mutex
	initialized bool
	state       uint8 // 0 free, 1 leased, 2 retiring
	token       uint64
	active      *Conn
	tx          [4096]byte
	rx          [1024]byte
	queue       [4]ringidx
	reasm       [maxReasmSegments]reasmSeg
}

func (s *DialStorage) Init() { s.mu.Lock(); s.initialized = true; s.mu.Unlock() }

func (s *DialStorage) Acquire(backoff lneto.BackoffStrategy) (*Conn, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return nil, 0, lneto.ErrInvalidConfig
	}
	if s.state != 0 {
		return nil, 0, ErrDialStorageBusy
	}
	if s.token == math.MaxUint64 {
		return nil, 0, ErrDialStorageExhausted
	}
	if backoff == nil {
		return nil, 0, lneto.ErrMissingHALConfig
	}
	s.token++
	c := &Conn{storage: s, storageToken: s.token, opsDone: make(chan struct{})}
	// Seed capacity before the real Configure: SliceReuse never grows these arrays.
	c.h.bufTx.slist.pkts = s.queue[:0:len(s.queue)]
	c.h.reasm.held = s.reasm[:0:len(s.reasm)]
	err := c.Configure(ConnConfig{TxBuf: s.tx[:], RxBuf: s.rx[:], TxPacketQueueSize: len(s.queue), RWBackoff: backoff})
	if err != nil {
		c.h = Handler{}
		return nil, 0, err
	}
	s.state = 1
	s.active = c
	return c, s.token, nil
}

// Retire is called only after app I/O and dial/cancel joins. detach MUST abort
// this Conn and detach all native nodes under its stack lock; it must not wait
// for the calling I/O operation or invoke this function from an I/O callback.
// Native operations admitted before retirement (including Berkeley snapshots)
// are drained after detach, before any storage is cleared or freed.
// A detach error leaves the owner permanently retiring: fail closed.
func (s *DialStorage) Retire(c *Conn, token uint64, detach func() error) error {
	if detach == nil {
		return lneto.ErrInvalidConfig
	}
	s.mu.Lock()
	if s.state != 1 || s.active != c || s.token != token {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.state = 2
	c.mu.Lock()
	c.retiring = true
	if c.nativeOps == 0 {
		close(c.opsDone)
	}
	c.mu.Unlock()
	s.mu.Unlock()
	if err := detach(); err != nil {
		return err
	}
	<-c.opsDone
	c.mu.Lock()
	// Fresh Conn remains permanently retired; no reopening into shared storage.
	c.h = Handler{}
	c.remoteAddr = nil
	c.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != 2 || s.active != c || s.token != token {
		return net.ErrClosed
	}
	clear(s.tx[:])
	clear(s.rx[:])
	clear(s.queue[:])
	clear(s.reasm[:])
	s.active = nil
	s.state = 0
	return nil
}

// PinNativeCall covers lookup-to-return, not merely the eventual native Read
// or Write. Berkeley calls this while still holding its lookup mutex.
func (c *Conn) PinNativeCall() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.storage == nil {
		return false, nil
	}
	if c.retiring || c.nativeOps == math.MaxUint64 {
		return false, net.ErrClosed
	}
	c.nativeOps++
	return true, nil
}
func (c *Conn) UnpinNativeCall() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nativeOps == 0 {
		panic("unpaired native call")
	}
	c.nativeOps--
	if c.retiring && c.nativeOps == 0 {
		close(c.opsDone)
	}
}
