package main

import (
	"encoding/binary"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	evKey    = 1   // EV_KEY
	keyPower = 116 // KEY_POWER
)

// watchPowerButton powers the machine off when its power button is pressed,
// which is how hypervisors and BMCs ask a guest to shut down (ACPI).
func watchPowerButton() {
	watching := map[string]bool{}
	for {
		devices, _ := filepath.Glob("/dev/input/event*")
		for _, dev := range devices {
			if !watching[dev] {
				watching[dev] = true
				go readPowerKey(dev)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func readPowerKey(dev string) {
	f, err := os.Open(dev)
	if err != nil {
		return
	}
	defer f.Close()
	// struct input_event on 64-bit Linux: 16 bytes of time, u16 type, u16 code, s32 value.
	event := make([]byte, 24)
	for {
		if _, err := f.Read(event); err != nil {
			return
		}
		typ := binary.LittleEndian.Uint16(event[16:])
		code := binary.LittleEndian.Uint16(event[18:])
		value := int32(binary.LittleEndian.Uint32(event[20:]))
		if typ == evKey && code == keyPower && value == 1 {
			log.Printf("power button pressed")
			go shutdown(unix.LINUX_REBOOT_CMD_POWER_OFF, "power button")
		}
	}
}
