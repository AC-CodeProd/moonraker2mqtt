// Package store implements a bounded two-sector NOR record protocol.
// It is NOT a hardware flash driver. Callers serialize all operations.
package store

import "errors"

const SectorSize = 4096
const HeaderSize = 32
const MaxPayload = SectorSize - HeaderSize
const Version = 1
const magic = 0x53334346
const commit = 0x434f4d54

var ErrMissing = errors.New("no committed settings")
var ErrCorrupt = errors.New("corrupt settings")
var ErrVersion = errors.New("unsupported settings version")
var ErrAmbiguous = errors.New("ambiguous generations")
var ErrSize = errors.New("payload out of bounds")
var ErrVerify = errors.New("flash readback mismatch")
var ErrConfirmation = errors.New("explicit two-slot reset confirmation required")
var ErrRepairNotRequired = errors.New("storage does not require reset")

// RecoveryRequired excludes I/O failures: inaccessible storage must not be erased.
func RecoveryRequired(err error) bool {
	return err == ErrCorrupt || err == ErrVersion || err == ErrAmbiguous
}

// Repair is a separate, explicitly destructive operation, never a Save fallback.
// Only the two Backend-reserved slots can be erased. An interrupted repair may
// lose both old records; callers must invalidate staging first and never retry
// automatically. Healthy/blank storage and read errors cause no writes.
func (s *Store) Repair(payload []byte, confirmed bool) error {
	if !confirmed {
		return ErrConfirmation
	}
	if len(payload) > MaxPayload {
		return ErrSize
	}
	_, err := s.scan()
	if !RecoveryRequired(err) {
		if err != nil && err != ErrMissing {
			return err
		}
		return ErrRepairNotRequired
	}
	var readback [SectorSize]byte
	for slot := 0; slot < 2; slot++ {
		if err = s.Flash.Erase(slot); err != nil {
			return err
		}
		if err = s.Flash.Read(slot, 0, readback[:]); err != nil {
			return err
		}
		if !erased(readback[:]) {
			return ErrVerify
		}
	}
	return s.Save(payload)
}

// Backend operates ONLY on two reserved sectors. Read must return uncached,
// coherent bytes or an error; Erase and Program must finish synchronously.
// Program supports aligned 4-byte writes, splitting page crossings internally.
// Errors may leave the targeted sector partially changed; never the other slot.
// No cache-off interval may surround Go code: safety belongs inside Backend.
type Backend interface {
	Read(slot, offset int, dst []byte) error
	Erase(slot int) error
	Program(slot, offset int, src []byte) error
}

type Store struct{ Flash Backend }
type Record struct {
	Generation uint64
	Payload    []byte
	Slot       int
}

func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func put32(b []byte, v uint32) {
	for i := 0; i < 4; i++ {
		b[i] = byte(v)
		v >>= 8
	}
}
func u64(b []byte) uint64      { return uint64(u32(b)) | uint64(u32(b[4:]))<<32 }
func put64(b []byte, v uint64) { put32(b, uint32(v)); put32(b[4:], uint32(v>>32)) }
func crcPart(crc uint32, b []byte) uint32 {
	for _, v := range b {
		crc ^= uint32(v)
		for j := 0; j < 8; j++ {
			mask := uint32(0) - (crc & 1)
			crc = (crc >> 1) ^ (0xedb88320 & mask)
		}
	}
	return crc
}
func checksum(b []byte, n int) uint32 {
	return ^crcPart(crcPart(^uint32(0), b[:20]), b[HeaderSize:HeaderSize+n])
}
func erased(b []byte) bool {
	for _, v := range b {
		if v != 255 {
			return false
		}
	}
	return true
}
func same(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// inspect: 0 erased, 1 invalid/torn, 2 valid, 3 unsupported committed version.
func inspect(b []byte) (int, int) {
	if erased(b) {
		return 0, 0
	}
	if u32(b[28:]) != commit || u32(b) != magic {
		return 1, 0
	}
	if b[4] != Version || b[5] != 0 {
		return 3, 0
	}
	if b[6] != HeaderSize || b[7] != 0 || u32(b[24:]) != 0 {
		return 1, 0
	}
	n := u32(b[16:])
	if n > MaxPayload {
		return 1, 0
	}
	if u32(b[20:]) != checksum(b, int(n)) {
		return 1, 0
	}
	// Padding is canonical too, so faults outside CRC-covered payload detected.
	if !erased(b[HeaderSize+int(n):]) {
		return 1, 0
	}
	return 2, int(n)
}

func (s *Store) scan() (Record, error) {
	var buffers [2][SectorSize]byte
	var states [2]int
	var lengths [2]int
	for i := 0; i < 2; i++ {
		if err := s.Flash.Read(i, 0, buffers[i][:]); err != nil {
			return Record{}, err
		}
		states[i], lengths[i] = inspect(buffers[i][:])
	}
	if states[0] == 3 || states[1] == 3 {
		return Record{}, ErrVersion
	}
	slot := -1
	for i := 0; i < 2; i++ {
		if states[i] == 2 {
			if slot < 0 {
				slot = i
				continue
			}
			diff := u64(buffers[i][8:]) - u64(buffers[slot][8:])
			if diff == 0 || diff == uint64(1)<<63 {
				return Record{}, ErrAmbiguous
			}
			if diff < uint64(1)<<63 {
				slot = i
			}
		}
	}
	if slot < 0 {
		if states[0] == 0 && states[1] == 0 {
			return Record{}, ErrMissing
		}
		return Record{}, ErrCorrupt
	}
	payload := make([]byte, lengths[slot])
	copy(payload, buffers[slot][HeaderSize:])
	return Record{u64(buffers[slot][8:]), payload, slot}, nil
}
func (s *Store) Load() (Record, error) { return s.scan() }

// Save writes only the inactive sector. Commit is a separate final word,
// after full-body readback. If it reports an error, reboot/Load decides whether
// the commit completed; callers must not assume an error means old is latest.
// Fully blank storage can initialize. Both corrupt sectors require explicit
// recovery outside this API; Save never erases evidence automatically.
func (s *Store) Save(payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrSize
	}
	old, err := s.scan()
	if err != nil && err != ErrMissing {
		return err
	}
	slot := 0
	gen := uint64(1)
	if err == nil {
		slot = 1 - old.Slot
		gen = old.Generation + 1
	}
	var body, readback [SectorSize]byte
	for i := range body {
		body[i] = 255
	}
	put32(body[:], magic)
	body[4] = Version
	body[5] = 0
	body[6] = HeaderSize
	body[7] = 0
	put64(body[8:], gen)
	put32(body[16:], uint32(len(payload)))
	put32(body[24:], 0)
	copy(body[HeaderSize:], payload)
	put32(body[20:], checksum(body[:], len(payload)))
	if err = s.Flash.Erase(slot); err != nil {
		return err
	}
	if err = s.Flash.Read(slot, 0, readback[:]); err != nil {
		return err
	}
	if !erased(readback[:]) {
		return ErrVerify
	}
	// Skip commit word; writes are bounded and 4-byte aligned.
	if err = s.Flash.Program(slot, 0, body[:28]); err != nil {
		return err
	}
	end := (HeaderSize + len(payload) + 3) &^ 3
	if end > HeaderSize {
		if err = s.Flash.Program(slot, HeaderSize, body[HeaderSize:end]); err != nil {
			return err
		}
	}
	if err = s.Flash.Read(slot, 0, readback[:]); err != nil {
		return err
	}
	if !same(body[:], readback[:]) {
		return ErrVerify
	}
	var marker [4]byte
	put32(marker[:], commit)
	if err = s.Flash.Program(slot, 28, marker[:]); err != nil {
		return err
	}
	if err = s.Flash.Read(slot, 0, readback[:]); err != nil {
		return err
	}
	put32(body[28:], commit)
	if !same(body[:], readback[:]) {
		return ErrVerify
	}
	return nil
}
