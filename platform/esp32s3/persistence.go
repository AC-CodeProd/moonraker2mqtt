//go:build tinygo && esp32s3

package esp32s3

/*
#include <stdint.h>
int settings_io(uint32_t,uint32_t,uint32_t,uint32_t *,uint32_t);
void settings_seal(void);
uint32_t settings_reason(void);
void settings_restart(void);
uint32_t settings_retained_read(uint32_t);
void settings_retained_write(uint32_t,uint32_t);
*/
import "C"
import (
	"encoding/binary"
	"errors"
	"moonraker2mqtt/platform/esp32s3/setup"
	"moonraker2mqtt/platform/esp32s3/store"
	"unsafe"
)

var ErrBootFlash = errors.New("boot flash operation refused or failed")

type bootFlash struct{}

func (bootFlash) Read(slot, offset int, b []byte) error    { return flashIO(0, slot, offset, b) }
func (bootFlash) Program(slot, offset int, b []byte) error { return flashIO(1, slot, offset, b) }
func (bootFlash) Erase(slot int) error                     { return flashIO(2, slot, 0, nil) }
func flashIO(op, slot, offset int, b []byte) error {
	if op != 0 && QualificationReadOnly != "false" {
		return ErrBootFlash
	}
	if slot < 0 || slot > 1 || offset < 0 || offset > 4096 || len(b) > 4096-offset || offset%4 != 0 || len(b)%4 != 0 {
		return ErrBootFlash
	}
	var aligned [64]uint32
	if op == 2 {
		if C.settings_io(2, C.uint32_t(slot), 0, nil, 0) != 0 {
			return ErrBootFlash
		}
		return nil
	}
	for len(b) > 0 {
		n := len(b)
		if n > 256 {
			n = 256
		}
		if op == 1 {
			for i := 0; i < n/4; i++ {
				aligned[i] = binary.LittleEndian.Uint32(b[i*4:])
			}
		}
		if C.settings_io(C.uint32_t(op), C.uint32_t(slot), C.uint32_t(offset), (*C.uint32_t)(unsafe.Pointer(&aligned[0])), C.uint32_t(n)) != 0 {
			return ErrBootFlash
		}
		if op == 0 {
			for i := 0; i < n/4; i++ {
				binary.LittleEndian.PutUint32(b[i*4:], aligned[i])
			}
		}
		offset += n
		b = b[n:]
	}
	return nil
}
func retained() []byte {
	b := make([]byte, setup.PendingSize)
	for i := 0; i < len(b)/4; i++ {
		binary.LittleEndian.PutUint32(b[i*4:], uint32(C.settings_retained_read(C.uint32_t(i))))
	}
	return b
}
func stagePending(payload []byte) error {
	return stageOperation(payload, setup.SaveOperation)
}
func stageRepair(payload []byte) error {
	return stageOperation(payload, setup.RepairOperation)
}
func stageOperation(payload []byte, op setup.Operation) error {
	b := make([]byte, setup.PendingSize)
	if err := setup.StageOperation(b, payload, op); err != nil {
		return err
	}
	C.settings_retained_write(0, 0)
	for i := 1; i < len(b)/4; i++ {
		C.settings_retained_write(C.uint32_t(i), C.uint32_t(binary.LittleEndian.Uint32(b[i*4:])))
	}
	C.settings_retained_write(0, C.uint32_t(binary.LittleEndian.Uint32(b)))
	return nil
}

// Must run before constructing/enabling radio, with no background goroutines.
func bootSettings() (setup.Settings, error) {
	s := store.Store{Flash: bootFlash{}}
	if QualificationReadOnly != "false" {
		// Do not interpret/apply a retained save/repair during qualification.
		// Discard its publish marker; load committed settings read-only below.
		cancelPending()
	} else {
		b := retained()
		pending, err := setup.TakeOperation(b, uint32(C.settings_reason()) == 3)
		C.settings_retained_write(0, 0) // invalidate BEFORE attempting a flash operation
		if err != nil {
			return setup.Settings{}, err
		}
		if pending != nil {
			if err = setup.ApplyPending(&s, pending); err != nil {
				return setup.Settings{}, err
			}
		}
	}
	record, err := s.Load()
	if err != nil {
		return setup.Settings{}, err
	}
	return setup.Decode(record.Payload)
}

// Failed response cancels the volatile operation. An unrelated software reset
// must never commit an unacknowledged save or destructive repair.
func cancelPending() { C.settings_retained_write(0, 0) }
func sealBootFlash() { C.settings_seal() }
func rebootSave() {
	C.settings_restart()
	for {
	}
}
