package x

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"testing"
)

func Test(t *testing.T) {
	// D0-7
	lsb, err := os.ReadFile("../../hp8593a_eeproms/08592-80083_U6_top1.bin")
	if err != nil {
		log.Fatalf("%v", err)
	}
	// D8-15
	msb, err := os.ReadFile("../../hp8593a_eeproms/08592-80085_U23_top2.bin")
	if err != nil {
		log.Fatalf("%v", err)
	}

	// D0-7
	lsbtop, err := os.ReadFile("../../hp8593a_eeproms/08592-80084_U7_top0.bin")
	if err != nil {
		log.Fatalf("%v", err)
	}
	// D8-15
	msbtop, err := os.ReadFile("../../hp8593a_eeproms/08592-80086_U24_top3.bin")
	if err != nil {
		log.Fatalf("%v", err)
	}
	fh, err := os.Create("../../hp8593a_eeproms/rom.hex")
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer fh.Close()

	buf := bytes.NewBuffer(nil)
	for i, v := range lsb {
		buf.WriteByte(msb[i])
		buf.WriteByte(v)
		_, err := fmt.Fprintf(fh, "%4X: %02X%02X\n", i, msb[i], v)
		if err != nil {
			log.Fatalf("%v", err)
		}
	}
	if err := fh.Sync(); err != nil {
		log.Fatalf("%v", err)
	}
	fh.Close()

	for i, v := range lsbtop {
		buf.WriteByte(msbtop[i])
		buf.WriteByte(v)
	}

	os.WriteFile("../../hp8593a_eeproms/rom.bin", buf.Bytes(), 0644)
	os.WriteFile("../../hp8593a_eeproms/rom_dump.hex", []byte(hex.Dump(buf.Bytes())), 0644)

}
