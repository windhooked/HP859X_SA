/* musashi_config.h — Phase-0 configuration for the HP 8593A emulator build.
 * Pointed to by -DMUSASHI_CNF so Musashi includes this instead of m68kconf.h.
 */
#ifndef MUSASHI_CONFIG__HEADER
#define MUSASHI_CONFIG__HEADER

#define M68K_OPT_OFF             0
#define M68K_OPT_ON              1
#define M68K_OPT_SPECIFY_HANDLER 2

#define M68K_COMPILE_FOR_MAME      M68K_OPT_OFF

/* Emulate all variants so the CPU-type can be changed at runtime. */
#define M68K_EMULATE_010           M68K_OPT_ON
#define M68K_EMULATE_EC020         M68K_OPT_ON
#define M68K_EMULATE_020           M68K_OPT_ON
#define M68K_EMULATE_030           M68K_OPT_ON
#define M68K_EMULATE_040           M68K_OPT_ON

#define M68K_SEPARATE_READS        M68K_OPT_OFF
#define M68K_SIMULATE_PD_WRITES    M68K_OPT_OFF

/* Autovector all interrupts by default; no per-device ack callback yet. */
#define M68K_EMULATE_INT_ACK       M68K_OPT_OFF

#define M68K_EMULATE_BKPT_ACK      M68K_OPT_OFF
#define M68K_EMULATE_TRACE         M68K_OPT_OFF
#define M68K_EMULATE_RESET         M68K_OPT_OFF
#define M68K_CMPILD_HAS_CALLBACK   M68K_OPT_OFF
#define M68K_RTE_HAS_CALLBACK      M68K_OPT_OFF
#define M68K_TAS_HAS_CALLBACK      M68K_OPT_OFF
#define M68K_ILLG_HAS_CALLBACK     M68K_OPT_OFF
#define M68K_TRAP_HAS_CALLBACK     M68K_OPT_OFF
#define M68K_EMULATE_FC            M68K_OPT_OFF
#define M68K_MONITOR_PC            M68K_OPT_OFF

/* Instruction hook is required by the single-step implementation. */
#define M68K_INSTRUCTION_HOOK      M68K_OPT_ON
#define M68K_INSTRUCTION_CALLBACK(pc) musashi_instr_hook(pc)

#define M68K_EMULATE_PREFETCH      M68K_OPT_OFF
#define M68K_EMULATE_ADDRESS_ERROR M68K_OPT_OFF

#define M68K_LOG_ENABLE            M68K_OPT_OFF
#define M68K_LOG_1010_1111         M68K_OPT_OFF
#define M68K_LOG_TRAP              M68K_OPT_OFF

/* Disable PMMU check on every access; no MMU in the HP 8593A. */
#define M68K_EMULATE_PMMU          M68K_OPT_OFF

#define M68K_USE_64_BIT            M68K_OPT_ON

#endif /* MUSASHI_CONFIG__HEADER */
