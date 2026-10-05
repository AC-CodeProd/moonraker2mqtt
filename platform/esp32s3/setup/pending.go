package setup

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"moonraker2mqtt/platform/esp32s3/store"
)

// Pending is a volatile handoff, not persistence. Publish its first word last.
// The hardware adapter invalidates it before a boot write to avoid retry loops.
const PendingSize = 4096
const pendingMagic = 0x33535452 // changed format: operation is CRC-covered

type Operation uint32

const (
	SaveOperation   Operation = 1
	RepairOperation Operation = 2
)

type Pending struct {
	Operation Operation
	Payload   []byte
}

func validOperation(op Operation) bool { return op == SaveOperation || op == RepairOperation }
func pendingChecksum(op []byte, payload []byte) uint32 {
	return crc32.Update(crc32.ChecksumIEEE(op), crc32.IEEETable, payload)
}
func Stage(dst []byte, payload []byte) error { return StageOperation(dst, payload, SaveOperation) }
func StageOperation(dst []byte, payload []byte, op Operation) error {
	if len(dst) != PendingSize {
		return errors.New("invalid retention region")
	}
	if !validOperation(op) {
		return errors.New("invalid pending operation")
	}
	if _, err := Decode(payload); err != nil {
		return err
	}
	for i := range dst {
		dst[i] = 0
	}
	binary.LittleEndian.PutUint32(dst[4:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(dst[12:], uint32(op))
	binary.LittleEndian.PutUint32(dst[8:], pendingChecksum(dst[12:16], payload))
	copy(dst[16:], payload)
	binary.LittleEndian.PutUint32(dst, pendingMagic)
	return nil
}

// Take is the normal-save compatibility API; it cannot silently downgrade repair.
func Take(src []byte, softwareReset bool) ([]byte, error) {
	p, err := TakeOperation(src, softwareReset)
	if err != nil || p == nil {
		return nil, err
	}
	if p.Operation != SaveOperation {
		return nil, errors.New("repair requires operation-aware consumer")
	}
	return p.Payload, nil
}
func TakeOperation(src []byte, softwareReset bool) (*Pending, error) {
	if len(src) != PendingSize {
		return nil, errors.New("invalid retention region")
	}
	magic := binary.LittleEndian.Uint32(src)
	binary.LittleEndian.PutUint32(src, 0)
	if !softwareReset || magic != pendingMagic {
		return nil, nil
	}
	n := binary.LittleEndian.Uint32(src[4:])
	if n == 0 || n > MaxSettings {
		return nil, errors.New("invalid pending length")
	}
	op := Operation(binary.LittleEndian.Uint32(src[12:]))
	if !validOperation(op) {
		return nil, errors.New("invalid pending operation")
	}
	p := append([]byte(nil), src[16:16+int(n)]...)
	if pendingChecksum(src[12:16], p) != binary.LittleEndian.Uint32(src[8:]) {
		return nil, errors.New("invalid pending checksum")
	}
	if _, err := Decode(p); err != nil {
		return nil, err
	}
	return &Pending{Operation: op, Payload: p}, nil
}

// ApplyPending runs only at boot, after invalidating RTC and before radio.
// Validate again before invoking any backend operation, including reads.
func ApplyPending(s *store.Store, p *Pending) error {
	if p == nil {
		return nil
	}
	if !validOperation(p.Operation) {
		return errors.New("invalid pending operation")
	}
	if _, err := Decode(p.Payload); err != nil {
		return err
	}
	if p.Operation == RepairOperation {
		return s.Repair(p.Payload, true)
	}
	return s.Save(p.Payload)
}
