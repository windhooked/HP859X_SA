/*
 * bridge.c — C-side helpers for the Musashi M68K cgo adapter.
 *
 * Phase 1+: memory read/write callbacks are provided by Go (bus_callbacks.go)
 * via //export, so the required Musashi externs m68k_read/write_memory_* are
 * linked from the Go side. This file only provides:
 *   - musashi_init / musashi_step / musashi_instr_hook
 *   - disassembler read stubs (m68kdasm.c requires these separately from the
 *     normal memory callbacks but they can simply alias through to them)
 */

#include "m68k.h"
#include "bridge.h"

/* -------------------------------------------------------------------------- */
/* Disassembler read stubs                                                     */
/* -------------------------------------------------------------------------- */

/* m68kdasm.c requires m68k_read_disassembler_{8,16,32} as separate symbols.
 * Forward to the primary read callbacks (provided by bus_callbacks.go).
 */
unsigned int m68k_read_disassembler_8(unsigned int a)  { return m68k_read_memory_8(a);  }
unsigned int m68k_read_disassembler_16(unsigned int a) { return m68k_read_memory_16(a); }
unsigned int m68k_read_disassembler_32(unsigned int a) { return m68k_read_memory_32(a); }

/* -------------------------------------------------------------------------- */
/* Single-step                                                                 */
/* -------------------------------------------------------------------------- */

/* musashi_step executes exactly n instructions.
 *
 * m68k_execute(1) passes a 1-cycle budget. Every M68K instruction takes at
 * least 4 cycles, so the do-while loop in Musashi's execute path always runs
 * exactly once before the remaining-cycle check exits. One call = one insn.
 */
void musashi_step(int n) {
    for (int i = 0; i < n; i++) {
        m68k_execute(1);
    }
}

/* musashi_run passes the full cycle budget to m68k_execute, allowing the core
 * to retire as many instructions as fit within the budget. Returns the actual
 * number of cycles executed (Musashi may overshoot by up to one instruction's
 * cycle count). Use for bulk execution to avoid cgo-crossing overhead of a
 * tight Step() loop.
 */
int musashi_run(int cycles) {
    return m68k_execute(cycles);
}

/* Stop-at-PC support for RunUntil: the instruction hook ends the timeslice the
 * moment the core is about to execute g_stop_pc, so a chunked fast-forward can
 * land exactly on a target instruction (e.g. a render entry) and hand off to
 * single-stepping with a shadow call stack. g_stop_pc = 0xFFFFFFFF disables. */
static unsigned int g_stop_pc = 0xFFFFFFFFu;
static int g_stopped = 0;

/* musashi_instr_hook is called by the Musashi core on every instruction
 * (wired via M68K_INSTRUCTION_CALLBACK in m68kconf.h with SPECIFY_HANDLER),
 * with the PC of the instruction about to execute.
 */
void musashi_instr_hook(unsigned int pc) {
    if (pc == g_stop_pc) {
        g_stopped = 1;
        m68k_end_timeslice();
    }
}

/* musashi_run_until runs up to `cycles` but ends the instant the core is about
 * to execute stop_pc (PC stops AT stop_pc). Returns cycles executed;
 * musashi_stopped() reports whether stop_pc was reached. */
int musashi_run_until(int cycles, unsigned int stop_pc) {
    g_stop_pc = stop_pc;
    g_stopped = 0;
    int c = m68k_execute(cycles);
    g_stop_pc = 0xFFFFFFFFu;
    return c;
}

int musashi_stopped(void) { return g_stopped; }

/* -------------------------------------------------------------------------- */
/* Initialisation                                                               */
/* -------------------------------------------------------------------------- */

void musashi_init(void) {
    m68k_init();
    m68k_set_cpu_type(M68K_CPU_TYPE_68000);
    /* Instruction hook wired statically via M68K_INSTRUCTION_CALLBACK */
}

/* -------------------------------------------------------------------------- */
/* Disassembler                                                                 */
/* -------------------------------------------------------------------------- */

/* musashi_disasm disassembles one M68K instruction at pc using the active bus
 * for memory reads.  buf must be at least 80 bytes.  Returns the byte-size of
 * the disassembled instruction so callers can advance pc.
 */
unsigned int musashi_disasm(unsigned int pc, char *buf, unsigned int buf_len) {
    (void)buf_len; /* m68k_disassemble always writes ≤ ~60 chars */
    return m68k_disassemble(buf, pc, M68K_CPU_TYPE_68000);
}
