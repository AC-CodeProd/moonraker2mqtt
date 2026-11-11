//go:build tinygo || jsonborrowtest

package diagjson

import "encoding/json"

// Notification owns either an exclusive encoder lease or an ordinary result.
// Defer Release before Encode; never copy a live Notification.
type Notification struct {
	lease    json.MarshalLease
	owned    []byte
	borrowed bool
}

func (n *Notification) Encode(v any, borrow bool) (err error) {
	n.borrowed = borrow
	if borrow {
		return n.lease.EncodeOrigin(v, 1)
	}
	n.owned, err = Marshal(1, v)
	return err
}

// EncodeStatus shares the existing lease mechanics, retaining producer2 for
// unqualified clients. Notification.Encode and producer1 remain unchanged.
func (n *Notification) EncodeStatus(v any, borrow bool) (err error) {
	n.borrowed = borrow
	if borrow {
		return n.lease.EncodeOrigin(v, 2)
	}
	n.owned, err = Marshal(2, v)
	return err
}

func (n *Notification) Bytes() []byte {
	if n.borrowed {
		return n.lease.Bytes()
	}
	return n.owned
}
func (n *Notification) Release() { n.lease.Release(); n.owned = nil; n.borrowed = false }
