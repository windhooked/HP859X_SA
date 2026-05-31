/* bridge.h — Go-visible declarations for the Musashi bridge. */
#ifndef MUSASHI_BRIDGE__HEADER
#define MUSASHI_BRIDGE__HEADER

#include <stdint.h>

/* Execute exactly n instructions then return. */
void musashi_step(int n);

/* Run for (at least) cycles M68K bus cycles, returning actual cycles executed.
 * Unlike musashi_step, this passes the full cycle budget to m68k_execute and
 * lets the core retire as many instructions as fit. Use for bulk execution
 * (e.g. skipping busy-wait delay loops) without thousands of cgo crossings. */
int musashi_run(int cycles);

/* Instruction hook — called by Musashi via M68K_INSTRUCTION_CALLBACK.
 * Declared here so the config header can reference it. */
void musashi_instr_hook(unsigned int pc);

/* Run up to `cycles` but stop AT stop_pc (before it executes). Returns cycles
 * executed; musashi_stopped() is non-zero if stop_pc was reached. */
int musashi_run_until(int cycles, unsigned int stop_pc);
int musashi_stopped(void);

/* One-time initialisation: calls m68k_init and sets CPU type to 68000. */
void musashi_init(void);

/* Discard the reset-exception penalty cycles that m68k_pulse_reset() stores
 * in RESET_CYCLES. Without this, the first m68k_execute() call after reset
 * returns immediately (consuming the penalty) without executing any instruction.
 * Must be implemented in musashi_core.c which has access to the private
 * RESET_CYCLES macro via m68kcpu.h. */
void musashi_drain_reset(void);

/* Disassemble one instruction at pc into buf (caller supplies buf, min 80 bytes).
 * Returns the number of bytes consumed by the instruction.
 * Reads memory via the active bus (activeBus must be set). */
unsigned int musashi_disasm(unsigned int pc, char *buf, unsigned int buf_len);

#endif /* MUSASHI_BRIDGE__HEADER */
