package romloader_test

import (
	"encoding/binary"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// eepromDir returns the absolute path to hp8593a_eeproms/ regardless of where
// the test binary is executed from.
func eepromDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file = .../pkg/emu/romloader/romloader_test.go
	return filepath.Join(filepath.Dir(file), "../../../hp8593a_eeproms")
}

// TestBuildResetVector is the primary sanity check: the first two longwords of
// the assembled ROM must be the HP 8593A reset vectors. Values are for the
// Rev L 98.06.15 Opt-027 firmware (the canonical image).
func TestBuildResetVector(t *testing.T) {
	rom, err := romloader.LoadDir(eepromDir(t))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(rom) != romloader.ROMSize {
		t.Fatalf("len(rom) = %d, want %d", len(rom), romloader.ROMSize)
	}

	sp := binary.BigEndian.Uint32(rom[0:4])
	pc := binary.BigEndian.Uint32(rom[4:8])

	if sp != 0x00FF948A {
		t.Errorf("reset SP = %#010X, want 0x00FF948A (Rev L)", sp)
	}
	if pc != 0x00001B34 {
		t.Errorf("reset PC = %#010X, want 0x00001B34 (Rev L)", pc)
	}
}

// TestBuildSizeMismatch verifies that Build rejects chips of the wrong size.
func TestBuildSizeMismatch(t *testing.T) {
	bad := make([]byte, romloader.ChipSize-1)
	ok := make([]byte, romloader.ChipSize)
	if _, err := romloader.Build(ok, ok, ok, bad); err == nil {
		t.Error("Build with wrong-size chip should return error")
	}
}
