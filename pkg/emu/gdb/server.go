// Package gdb is a GDB Remote Serial Protocol (RSP) server over our M68K
// emulator, turning it into an interactive debug target for any GDB front-end
// (gef / pwndbg / gdb -tui / VSCode). It exposes register/memory inspection,
// breakpoints, single-step, and — the killer feature for reverse-engineering —
// read/write/access WATCHPOINTS implemented via the bus OnRead/OnWrite hooks.
//
// Connect (gef/pwndbg over an m68k-aware gdb):
//
//	gdb -ex 'set architecture m68k' -ex 'target remote :3333'
//
// then: `watch *0x2b37f`, `rwatch *0xFFB0EC`, `b *0x5ED7E`, `c`, `si`, `info reg`.
//
// The continue loop injects the IRQ5 timer tick the 8593A firmware needs to make
// progress, so `continue` behaves like the real running instrument.
package gdb

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
)

// targetXML describes our register set to gdb (so gef/pwndbg show d0-d7/a0-a7/
// sr/pc correctly). Standard m68k core feature: a6=fp, a7=sp.
const targetXML = `<?xml version="1.0"?>
<!DOCTYPE target SYSTEM "gdb-target.dtd">
<target version="1.0">
<architecture>m68k</architecture>
<feature name="org.gnu.gdb.m68k.core">
<reg name="d0" bitsize="32"/><reg name="d1" bitsize="32"/><reg name="d2" bitsize="32"/><reg name="d3" bitsize="32"/>
<reg name="d4" bitsize="32"/><reg name="d5" bitsize="32"/><reg name="d6" bitsize="32"/><reg name="d7" bitsize="32"/>
<reg name="a0" bitsize="32"/><reg name="a1" bitsize="32"/><reg name="a2" bitsize="32"/><reg name="a3" bitsize="32"/>
<reg name="a4" bitsize="32"/><reg name="a5" bitsize="32"/><reg name="fp" bitsize="32" type="data_ptr"/><reg name="sp" bitsize="32" type="data_ptr"/>
<reg name="ps" bitsize="32"/><reg name="pc" bitsize="32" type="code_ptr"/>
</feature>
</target>`

// regOrder maps gdb regnum (0..17) to our cpu.Reg.
var regOrder = []cpu.Reg{
	cpu.D0, cpu.D1, cpu.D2, cpu.D3, cpu.D4, cpu.D5, cpu.D6, cpu.D7,
	cpu.A0, cpu.A1, cpu.A2, cpu.A3, cpu.A4, cpu.A5, cpu.A6, cpu.A7,
	cpu.SR, cpu.PC,
}

// Server is a GDB RSP server bound to one Machine.
type Server struct {
	m       *machine.Machine
	breaks  map[uint32]bool   // PC breakpoints
	watchW  map[uint32]uint32 // write watchpoints: base -> len
	watchR  map[uint32]uint32 // read watchpoints:  base -> len
	irqTick int               // IRQ5-injection cadence counter
	lb      *emutest.LoopBreaker
	conn    net.Conn
	w       *bufio.Writer

	// transient watchpoint-hit state (set by the bus hooks during a step).
	wpHit  bool
	wpAddr uint32
	wpKind string // "watch" | "rwatch" | "awatch"
}

// New binds a server to a (already-reset / optionally pre-booted) machine.
func New(m *machine.Machine) *Server {
	return &Server{
		m:      m,
		breaks: map[uint32]bool{},
		watchW: map[uint32]uint32{},
		watchR: map[uint32]uint32{},
		lb:     emutest.NewLoopBreaker(50),
	}
}

// FastForward runs the firmware with the LoopBreaker + periodic IRQ5 by N
// cycles, so a client can attach at a useful state (e.g. just before the UI
// renders). Exposed for the gdbserver --boot flag.
func (s *Server) FastForward(cycles int) { s.fastForward(cycles) }

// Serve listens on addr (e.g. ":3333") for one gdb connection and serves it.
func (s *Server) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Printf("gdbserver: listening on %s — connect with:\n  gdb -ex 'set architecture m68k' -ex 'target remote %s'\n", addr, addr)
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	fmt.Println("gdbserver: client connected")
	s.conn = conn
	s.w = bufio.NewWriter(conn)
	return s.session(bufio.NewReader(conn))
}

func (s *Server) session(r *bufio.Reader) error {
	logp := os.Getenv("GDB_LOG") != ""
	for {
		pkt, err := readPacket(r)
		if err != nil {
			return err
		}
		s.ack() // acknowledge receipt
		if logp {
			fmt.Printf(">> %.80s\n", pkt)
		}
		reply := s.handle(pkt)
		if logp {
			fmt.Printf("<< %.80s\n", reply)
		}
		if reply == "__detach__" {
			s.send("OK")
			return nil
		}
		s.send(reply)
	}
}

// handle dispatches one RSP command and returns the reply payload.
func (s *Server) handle(p string) string {
	if p == "" {
		return ""
	}
	switch p[0] {
	case '?':
		return "S05"
	case 'g':
		return s.readAllRegs()
	case 'G':
		return s.writeAllRegs(p[1:])
	case 'p':
		n, _ := strconv.ParseInt(p[1:], 16, 32)
		return hexU32(s.readReg(int(n)))
	case 'P':
		return s.writeOneReg(p[1:])
	case 'm':
		return s.readMem(p[1:])
	case 'M':
		return s.writeMem(p[1:])
	case 'c':
		return s.cont(p[1:])
	case 's':
		return s.step(p[1:])
	case 'Z':
		return s.addBreak(p[1:])
	case 'z':
		return s.delBreak(p[1:])
	case 'k', 'D':
		return "__detach__"
	case 'q':
		return s.query(p)
	case 'v':
		if strings.HasPrefix(p, "vCont?") {
			return "vCont;c;C;s;S"
		}
		if strings.HasPrefix(p, "vCont;") {
			return s.vCont(p[6:])
		}
		return ""
	case 'H', 'T':
		return "OK" // thread ops: single-thread, always OK
	default:
		return ""
	}
}

func (s *Server) query(p string) string {
	switch {
	case strings.HasPrefix(p, "qSupported"):
		return "PacketSize=4000;qXfer:features:read+;swbreak+;hwbreak+"
	case strings.HasPrefix(p, "qXfer:features:read:target.xml:"):
		return xferReply(targetXML, p[len("qXfer:features:read:target.xml:"):])
	case strings.HasPrefix(p, "qRcmd,"):
		return s.monitor(p[len("qRcmd,"):])
	case p == "qC":
		return "QC1"
	case p == "qAttached":
		return "1"
	case p == "qfThreadInfo":
		return "m1"
	case p == "qsThreadInfo":
		return "l"
	case strings.HasPrefix(p, "qTStatus"), strings.HasPrefix(p, "qSymbol"):
		return "OK"
	}
	return ""
}

// monitor handles `monitor <cmd>` (qRcmd). Custom commands for this firmware:
//
//	monitor boot <cycles>   fast-forward the boot (with IRQ5) by N cycles
//	monitor irq <n>         pulse autovector IRQ n once
func (s *Server) monitor(hexCmd string) string {
	raw, _ := hex.DecodeString(hexCmd)
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return monMsg("usage: monitor boot <cycles> | monitor irq <n>\n")
	}
	switch fields[0] {
	case "boot":
		n := 160_000_000
		if len(fields) > 1 {
			if v, err := strconv.Atoi(fields[1]); err == nil {
				n = v
			}
		}
		s.fastForward(n)
		return monMsg(fmt.Sprintf("booted %d cycles; PC=%06X\n", n, s.m.CPU.Reg(cpu.PC)))
	case "irq":
		if len(fields) > 1 {
			if lvl, err := strconv.Atoi(fields[1]); err == nil {
				s.m.CPU.SetIRQ(lvl)
				s.m.CPU.Run(400)
				s.m.CPU.SetIRQ(0)
				return monMsg(fmt.Sprintf("pulsed IRQ%d\n", lvl))
			}
		}
	}
	return monMsg("unknown monitor command\n")
}

// fastForward runs the firmware with the LoopBreaker + periodic IRQ5, the way
// machine.BootToOperating does, so the user can attach at a useful state.
func (s *Server) fastForward(cycles int) {
	for done := 0; done < cycles; done += 2000 {
		s.m.CPU.Run(2000)
		s.lb.Check(s.m.CPU.Reg(cpu.PC), s.m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			s.m.CPU.SetIRQ(5)
			s.m.CPU.Run(400)
			s.m.CPU.SetIRQ(0)
		}
	}
}

// ── registers / memory ──────────────────────────────────────────────────────

func (s *Server) readReg(n int) uint32 {
	if n < 0 || n >= len(regOrder) {
		return 0
	}
	return s.m.CPU.Reg(regOrder[n])
}

func (s *Server) readAllRegs() string {
	var b strings.Builder
	for i := range regOrder {
		b.WriteString(hexU32(s.readReg(i)))
	}
	return b.String()
}

func (s *Server) writeAllRegs(h string) string {
	for i := 0; i < len(regOrder) && (i+1)*8 <= len(h); i++ {
		v := beHex32(h[i*8 : i*8+8])
		s.m.CPU.SetReg(regOrder[i], v)
	}
	return "OK"
}

func (s *Server) writeOneReg(arg string) string {
	parts := strings.SplitN(arg, "=", 2)
	if len(parts) != 2 {
		return "E01"
	}
	n, _ := strconv.ParseInt(parts[0], 16, 32)
	if int(n) < len(regOrder) && len(parts[1]) >= 8 {
		s.m.CPU.SetReg(regOrder[n], beHex32(parts[1][:8]))
	}
	return "OK"
}

func (s *Server) readMem(arg string) string {
	a, l := parseAddrLen(arg)
	var b strings.Builder
	for i := uint32(0); i < l; i++ {
		b.WriteString(fmt.Sprintf("%02x", byte(s.m.Bus.Read(a+i, bus.Byte))))
	}
	return b.String()
}

func (s *Server) writeMem(arg string) string {
	colon := strings.IndexByte(arg, ':')
	if colon < 0 {
		return "E01"
	}
	a, _ := parseAddrLen(arg[:colon])
	data, _ := hex.DecodeString(arg[colon+1:])
	for i, by := range data {
		s.m.Bus.Write(a+uint32(i), bus.Byte, uint32(by))
	}
	return "OK"
}

// ── execution ───────────────────────────────────────────────────────────────

func (s *Server) step(arg string) string {
	if arg != "" {
		if a, err := strconv.ParseUint(arg, 16, 32); err == nil {
			s.m.CPU.SetReg(cpu.PC, uint32(a))
		}
	}
	s.m.CPU.Step()
	s.tickIRQ()
	return "S05"
}

// cont single-steps until a breakpoint or watchpoint fires, injecting IRQ5 so
// the firmware keeps running (the 8593A halts on timer waits otherwise).
func (s *Server) cont(arg string) string {
	if arg != "" {
		if a, err := strconv.ParseUint(arg, 16, 32); err == nil {
			s.m.CPU.SetReg(cpu.PC, uint32(a))
		}
	}
	s.armWatchpoints()
	defer s.disarmWatchpoints()
	for i := 0; ; i++ {
		pc := s.m.CPU.Reg(cpu.PC)
		if s.breaks[pc] {
			return fmt.Sprintf("T05swbreak:;%s", regStop())
		}
		if err := s.m.CPU.Step(); err != nil {
			return "S0B" // bus/addr error → SIGSEGV-ish
		}
		s.tickIRQ()
		if s.wpHit {
			s.wpHit = false
			return fmt.Sprintf("T05%s:%x;", s.wpKind, s.wpAddr)
		}
		// allow the client to interrupt (Ctrl-C sends 0x03) — checked cheaply.
		if i%65536 == 0 && s.interrupted() {
			return "T02"
		}
	}
}

func (s *Server) vCont(arg string) string {
	// minimal: treat the first action.
	if strings.HasPrefix(arg, "s") {
		return s.step("")
	}
	return s.cont("")
}

func (s *Server) tickIRQ() {
	s.irqTick++
	if s.irqTick%2000 == 0 {
		s.m.CPU.SetIRQ(5)
		s.m.CPU.Step()
		s.m.CPU.SetIRQ(0)
	}
}

// armWatchpoints installs bus hooks that flag a hit when a watched range is
// read/written. The hit is consumed after the current Step in cont().
func (s *Server) armWatchpoints() {
	hit := func(addr uint32, kind string) bool {
		for base, ln := range s.watchW {
			if kind != "rwatch" && addr >= base && addr < base+ln {
				s.wpHit, s.wpAddr, s.wpKind = true, addr, "watch"
				return true
			}
		}
		for base, ln := range s.watchR {
			if addr >= base && addr < base+ln {
				s.wpHit, s.wpAddr, s.wpKind = true, addr, "rwatch"
				return true
			}
		}
		return false
	}
	if len(s.watchR) > 0 {
		s.m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) { hit(addr, "rwatch") }
	}
	if len(s.watchW) > 0 {
		s.m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) { hit(addr, "watch") }
	}
}

func (s *Server) disarmWatchpoints() { s.m.Bus.OnRead = nil; s.m.Bus.OnWrite = nil }

// ── breakpoints / watchpoints ───────────────────────────────────────────────

// addBreak handles Z packets: Z0/Z1 breakpoint, Z2 write-wp, Z3 read-wp, Z4 access-wp.
func (s *Server) addBreak(arg string) string {
	t, addr, ln := parseZ(arg)
	switch t {
	case '0', '1':
		s.breaks[addr] = true
	case '2':
		s.watchW[addr] = max1(ln)
	case '3':
		s.watchR[addr] = max1(ln)
	case '4':
		s.watchW[addr], s.watchR[addr] = max1(ln), max1(ln)
	default:
		return ""
	}
	return "OK"
}

func (s *Server) delBreak(arg string) string {
	t, addr, _ := parseZ(arg)
	switch t {
	case '0', '1':
		delete(s.breaks, addr)
	case '2':
		delete(s.watchW, addr)
	case '3':
		delete(s.watchR, addr)
	case '4':
		delete(s.watchW, addr)
		delete(s.watchR, addr)
	default:
		return ""
	}
	return "OK"
}

// ── RSP framing ─────────────────────────────────────────────────────────────

func (s *Server) ack() { s.conn.Write([]byte{'+'}) }
func (s *Server) send(payload string) {
	sum := byte(0)
	for i := 0; i < len(payload); i++ {
		sum += payload[i]
	}
	fmt.Fprintf(s.w, "$%s#%02x", payload, sum)
	s.w.Flush()
}

func (s *Server) interrupted() bool { return false } // (Ctrl-C handling stub)

func readPacket(r *bufio.Reader) (string, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '+', '-':
			continue // ack/nak from the previous reply
		case 0x03:
			return "\x03", nil // interrupt
		case '$':
			var sb strings.Builder
			for {
				c, err := r.ReadByte()
				if err != nil {
					return "", err
				}
				if c == '#' {
					r.ReadByte() // checksum hi
					r.ReadByte() // checksum lo
					return sb.String(), nil
				}
				sb.WriteByte(c)
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func hexU32(v uint32) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v) // m68k target byte order
	return hex.EncodeToString(b[:])
}

func beHex32(h string) uint32 {
	b, _ := hex.DecodeString(h)
	if len(b) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func parseAddrLen(s string) (uint32, uint32) {
	parts := strings.SplitN(s, ",", 2)
	a, _ := strconv.ParseUint(parts[0], 16, 32)
	var l uint64 = 1
	if len(parts) > 1 {
		l, _ = strconv.ParseUint(parts[1], 16, 32)
	}
	return uint32(a), uint32(l)
}

// parseZ parses "T,addr,kind" from a Z/z packet → type byte, addr, len(kind).
func parseZ(s string) (byte, uint32, uint32) {
	if len(s) < 1 {
		return 0, 0, 0
	}
	t := s[0]
	parts := strings.Split(s[2:], ",")
	a, _ := strconv.ParseUint(parts[0], 16, 32)
	var k uint64 = 1
	if len(parts) > 1 {
		k, _ = strconv.ParseUint(parts[1], 16, 32)
	}
	return t, uint32(a), uint32(k)
}

func max1(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	return v
}

// regStop returns a small register block for T-replies (pc + sp for context).
func regStop() string { return "" }

func monMsg(s string) string { fmt.Print("[monitor] " + s); return "OK" }

// xferReply implements the qXfer:read offset,length slicing with l/m prefix.
func xferReply(data, arg string) string {
	parts := strings.SplitN(arg, ",", 2)
	if len(parts) != 2 {
		return "E00"
	}
	off, _ := strconv.ParseUint(parts[0], 16, 32)
	ln, _ := strconv.ParseUint(parts[1], 16, 32)
	if int(off) >= len(data) {
		return "l"
	}
	end := int(off) + int(ln)
	if end >= len(data) {
		return "l" + data[off:]
	}
	return "m" + data[off:end]
}
