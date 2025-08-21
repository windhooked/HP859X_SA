package join

import (
	"bytes"
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
	fo, err := os.OpenFile("../../hp8593a_eeproms/U23_U6.bin", os.O_CREATE|os.O_TRUNC, 777)
	if err != nil {
		log.Fatalf("%v", err)
	}
	buf := bytes.NewBuffer(nil)
	for i, v := range lsb {
		//word := (msb[i] << 0xff) | v
		//fmt.Fprintf(fo, "%c%c", rune(msb[i]), rune(v))
		buf.WriteByte(msb[i])
		buf.WriteByte(v)
	}
	fo.Write(buf.Bytes())
	//hex.Dump(word)
	
}

