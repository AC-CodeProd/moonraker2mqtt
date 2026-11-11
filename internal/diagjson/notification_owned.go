//go:build !tinygo && !jsonborrowtest

package diagjson

// Notification keeps the hosted and unqualified client ownership contract.
type Notification struct{ data []byte }

func (n *Notification) Encode(v any, borrow bool) (err error) { n.data, err = Marshal(1, v); return }
func (n *Notification) EncodeStatus(v any, borrow bool) (err error) {
	n.data, err = Marshal(2, v)
	return
}
func (n *Notification) Bytes() []byte { return n.data }
func (n *Notification) Release()      { n.data = nil }
