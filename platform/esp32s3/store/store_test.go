package store

import (
	"bytes"
	"errors"
	"hash/crc32"
	"testing"
)

var errCut = errors.New("power cut")

type memory struct {
	data     [2][SectorSize]byte
	budget   int
	changed  int
	failRead bool
	silent   bool
}

func blank() *memory {
	m := &memory{budget: -1}
	for i := range m.data {
		for j := range m.data[i] {
			m.data[i][j] = 255
		}
	}
	return m
}
func (m *memory) Read(slot, off int, b []byte) error {
	if m.failRead {
		return errCut
	}
	if slot < 0 || slot > 1 || off < 0 || off+len(b) > SectorSize {
		return ErrSize
	}
	copy(b, m.data[slot][off:])
	return nil
}
func (m *memory) tick() error {
	if m.budget == 0 {
		return errCut
	}
	if m.budget > 0 {
		m.budget--
	}
	m.changed++
	return nil
}
func (m *memory) Erase(slot int) error {
	if slot < 0 || slot > 1 {
		return ErrSize
	}
	for j := range m.data[slot] {
		if err := m.tick(); err != nil {
			return err
		}
		if !m.silent {
			m.data[slot][j] = 255
		}
	}
	return nil
}
func (m *memory) Program(slot, off int, b []byte) error {
	if slot < 0 || slot > 1 || off < 0 || off+len(b) > SectorSize || off%4 != 0 || len(b)%4 != 0 {
		return ErrSize
	}
	for i, v := range b {
		if m.data[slot][off+i]&v != v {
			return ErrVerify
		}
		if err := m.tick(); err != nil {
			return err
		}
		if !m.silent {
			m.data[slot][off+i] &= v
		}
	}
	return nil
}
func mustSave(t *testing.T, s *Store, p []byte) {
	t.Helper()
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
}
func TestRoundTripBounds(t *testing.T) {
	m := blank()
	s := Store{m}
	if _, e := s.Load(); e != ErrMissing {
		t.Fatal(e)
	}
	for _, n := range []int{0, 1, 3, 4, 255, 256, MaxPayload} {
		p := bytes.Repeat([]byte{byte(n)}, n)
		mustSave(t, &s, p)
		r, e := s.Load()
		if e != nil || !bytes.Equal(r.Payload, p) {
			t.Fatalf("n=%d err=%v", n, e)
		}
	}
	before := m.data
	if s.Save(make([]byte, MaxPayload+1)) != ErrSize || m.data != before {
		t.Fatal("oversize mutated flash")
	}
}
func TestChecksumReference(t *testing.T) {
	var b [SectorSize]byte
	copy(b[:20], []byte("12345678901234567890"))
	copy(b[HeaderSize:], []byte("abcdef"))
	p := append(append([]byte{}, b[:20]...), b[HeaderSize:HeaderSize+6]...)
	if checksum(b[:], 6) != crc32.ChecksumIEEE(p) {
		t.Fatal("CRC mismatch")
	}
}
func TestEveryByteCut(t *testing.T) {
	m := blank()
	s := Store{m}
	old := []byte("old retained credentials")
	mustSave(t, &s, old)
	// Fill the inactive sector with another valid older record so erase cuts
	// leave arbitrary remnants rather than starting with a conveniently blank slot.
	mustSave(t, &s, []byte("older inactive record"))
	mustSave(t, &s, old)
	for activeSlot := 0; activeSlot < 2; activeSlot++ {
		if activeSlot == 1 {
			mustSave(t, &s, old)
		}
		baseline := *m
		newPayload := bytes.Repeat([]byte{0x5a}, MaxPayload)
		complete := baseline
		complete.budget = -1
		complete.changed = 0
		mustSave(t, &Store{&complete}, newPayload)
		total := complete.changed
		for cut := 0; cut <= total; cut++ {
			trial := baseline
			trial.budget = cut
			trial.changed = 0
			ss := Store{&trial}
			e := ss.Save(newPayload)
			trial.budget = -1
			r, re := ss.Load()
			if re != nil {
				t.Fatalf("cut %d load %v", cut, re)
			}
			if !bytes.Equal(r.Payload, old) && !bytes.Equal(r.Payload, newPayload) {
				t.Fatalf("cut %d torn record accepted", cut)
			}
			if trial.data[activeSlot] != baseline.data[activeSlot] {
				t.Fatalf("cut %d active sector modified", cut)
			}
			if cut < total && e == nil {
				t.Fatalf("cut %d unexpectedly succeeded", cut)
			}
		}
		t.Logf("verified %d cut positions for active slot %d (partial erase, body, final commit)", total+1, activeSlot)
	}
}
func TestAllSingleBitCorruption(t *testing.T) {
	m := blank()
	mustSave(t, &Store{m}, []byte("settings"))
	base := *m
	for pos := 0; pos < SectorSize; pos++ {
		for bit := 0; bit < 8; bit++ {
			trial := base
			trial.data[0][pos] ^= 1 << bit
			if _, e := (&Store{&trial}).Load(); e == nil {
				t.Fatalf("accepted bit fault at %d bit %d", pos, bit)
			}
		}
	}
	t.Log("verified every sector bit corruption")
}
func TestFallbackAndRefusal(t *testing.T) {
	m := blank()
	s := Store{m}
	mustSave(t, &s, []byte("old"))
	mustSave(t, &s, []byte("new"))
	m.data[1][HeaderSize] ^= 1
	r, e := s.Load()
	if e != nil || string(r.Payload) != "old" {
		t.Fatal(r, e)
	}
	m.data[0][HeaderSize] ^= 1
	before := m.data
	if s.Save([]byte("overwrite")) != ErrCorrupt || m.data != before {
		t.Fatal("corruption erased")
	}
	m = blank()
	m.failRead = true
	if (&Store{m}).Save(nil) != errCut {
		t.Fatal("missing backend ignored")
	}
}
func TestUnsupportedAndAmbiguous(t *testing.T) {
	m := blank()
	s := Store{m}
	mustSave(t, &s, []byte("one"))
	mustSave(t, &s, []byte("two"))
	m.data[1][4] = 2
	before := m.data
	if s.Save(nil) != ErrVersion || m.data != before {
		t.Fatal("future version erased")
	}
	m = blank()
	s.Flash = m
	mustSave(t, &s, nil)
	mustSave(t, &s, nil)
	for _, gen := range []uint64{1, 1 + (uint64(1) << 63)} {
		put64(m.data[1][8:], gen)
		put32(m.data[1][20:], checksum(m.data[1][:], 0))
		if _, e := s.Load(); e != ErrAmbiguous {
			t.Fatal(gen, e)
		}
	}
}
func TestGenerationWrap(t *testing.T) {
	m := blank()
	s := Store{m}
	mustSave(t, &s, []byte("old"))
	put64(m.data[0][8:], ^uint64(0))
	put32(m.data[0][20:], checksum(m.data[0][:], 3))
	mustSave(t, &s, []byte("new"))
	r, e := s.Load()
	if e != nil || r.Generation != 0 || string(r.Payload) != "new" {
		t.Fatal(r, e)
	}
}
func TestSilentWriteRefused(t *testing.T) {
	m := blank()
	s := Store{m}
	mustSave(t, &s, []byte("old"))
	m.silent = true
	if s.Save([]byte("new")) != ErrVerify {
		t.Fatal("silent failure ignored")
	}
	r, e := s.Load()
	if e != nil || string(r.Payload) != "old" {
		t.Fatal(r, e)
	}
}
func FuzzLoad(f *testing.F) {
	f.Add([]byte("junk"))
	f.Add(bytes.Repeat([]byte{255}, SectorSize*2))
	f.Fuzz(func(t *testing.T, b []byte) {
		m := blank()
		copy(m.data[0][:], b)
		if len(b) > SectorSize {
			copy(m.data[1][:], b[SectorSize:])
		}
		r, e := (&Store{m}).Load()
		if e == nil && len(r.Payload) > MaxPayload {
			t.Fatal("unbounded record")
		}
	})
}
