/*
 * musashi_core.c — unity build: compiles all Musashi C translation units as a
 * single file so cgo can find them without a separate build step.
 *
 * The #include paths assume this file lives at:
 *   pkg/emu/cpu/musashi/musashi_core.c
 * and the Musashi source tree is at:
 *   third_party/musashi/
 *
 * Include order matters:
 *  - musashi_instr_hook must be declared before m68kcpu.c is included,
 *    because m68kconf.h maps M68K_INSTRUCTION_CALLBACK(pc) to that symbol.
 *  - m68kcpu.c internally #includes m68kfpu.c; do NOT include it here again.
 *  - softfloat.c provides the FPU support library referenced by m68kcpu.h.
 */
#include "bridge.h"   /* declares musashi_instr_hook (defined in bridge.c) */

#include "../../../../third_party/musashi/softfloat/softfloat.c"
#include "../../../../third_party/musashi/m68kcpu.c"   /* also pulls in m68kfpu.c */
#include "../../../../third_party/musashi/m68kdasm.c"
#include "../../../../third_party/musashi/m68kops.c"

/* Defined here (not in bridge.c) because RESET_CYCLES and m68ki_cpu are only
 * accessible from within the Musashi translation unit via m68kcpu.h. */
void musashi_drain_reset(void) {
    RESET_CYCLES = 0;
}
