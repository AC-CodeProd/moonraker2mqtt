package diagjson

// Notification owns the stock json.Marshal result on every platform.
// The legacy borrow flag is ignored. Publication remains synchronous; no
// exclusive encoder lease or standard-library instrumentation is required.
type Notification struct{ data []byte }

func (n *Notification) Encode(v any, borrow bool) (err error) { n.data, err = Marshal(1, v); return }
func (n *Notification) EncodeStatus(v any, borrow bool) (err error) {
	n.data, err = Marshal(2, v)
	return
}
func (n *Notification) Bytes() []byte { return n.data }
func (n *Notification) Release()      { n.data = nil }
