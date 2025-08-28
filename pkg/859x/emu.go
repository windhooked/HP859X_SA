package x

import (
	"log"
	"os"

	uc "github.com/unicorn-engine/unicorn/bindings/go/unicorn"
)

const ORG = 0x0000

func Emu() {
	// D0-7
	code, err := os.ReadFile("../../hp8593a_eeproms/rom.bin")
	if err != nil {
		log.Fatalf("%v", err)
	}

	mu, _ := uc.NewUnicorn(uc.ARCH_M68K, uc.MODE_BIG_ENDIAN)
	// mov eax, 1234
	//code := []byte{184, 210, 4, 0, 0}
	mu.MemMap(ORG, 0xFFFF0000)
	mu.MemWrite(ORG, code)
	if err := mu.Start(ORG, ORG+1024); err != nil {
		panic(err.Error())
	}

	d0, _ := mu.RegRead(uc.M68K_REG_D0)
	d1, _ := mu.RegRead(uc.M68K_REG_D1)
	d2, _ := mu.RegRead(uc.M68K_REG_D2)
	d3, _ := mu.RegRead(uc.M68K_REG_D3)
	d4, _ := mu.RegRead(uc.M68K_REG_D4)
	d5, _ := mu.RegRead(uc.M68K_REG_D5)
	d6, _ := mu.RegRead(uc.M68K_REG_D6)
	d7, _ := mu.RegRead(uc.M68K_REG_D7)

	a0, _ := mu.RegRead(uc.M68K_REG_A0)
	a1, _ := mu.RegRead(uc.M68K_REG_A1)
	a2, _ := mu.RegRead(uc.M68K_REG_A2)
	a3, _ := mu.RegRead(uc.M68K_REG_A3)
	a4, _ := mu.RegRead(uc.M68K_REG_A4)
	a5, _ := mu.RegRead(uc.M68K_REG_A5)
	a6, _ := mu.RegRead(uc.M68K_REG_A6)
	a7, _ := mu.RegRead(uc.M68K_REG_A7)

	pc, _ := mu.RegRead(uc.M68K_REG_PC)
	sr, _ := mu.RegRead(uc.M68K_REG_SR)

	log.Printf(">>> A0 = 0x%x\t\t>>> D0 = 0x%x\n", a0, d0)
	log.Printf(">>> A1 = 0x%x\t\t>>> D1 = 0x%x\n", a1, d1)
	log.Printf(">>> A2 = 0x%x\t\t>>> D2 = 0x%x\n", a2, d2)
	log.Printf(">>> A3 = 0x%x\t\t>>> D3 = 0x%x\n", a3, d3)
	log.Printf(">>> A4 = 0x%x\t\t>>> D4 = 0x%x\n", a4, d4)
	log.Printf(">>> A5 = 0x%x\t\t>>> D5 = 0x%x\n", a5, d5)
	log.Printf(">>> A6 = 0x%x\t\t>>> D6 = 0x%x\n", a6, d6)
	log.Printf(">>> A7 = 0x%x\t\t>>> D7 = 0x%x\n", a7, d7)
	log.Printf(">>> PC = 0x%x\n", pc)
	log.Printf(">>> SR = 0x%x\n", sr)
}
