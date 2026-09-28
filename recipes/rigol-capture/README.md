# Rigol Capture & Compare

**Physical verification pending.** This package targets a Rigol DG812 and MHO954 using locally configured SCPI profiles. Addresses and limits stay in the host configuration. Imported package names do not establish verified compatibility.

1. Map the bench project, DG812 channel-1 inspection, and MHO954 channel-1 stopped capture actions.
2. Separately disable both generator outputs, apply locally validated settings and explicitly authorize output enable using your existing controls. Recipe parameters record intended values only; changing them never writes hardware.
3. Acquire and stop manually at the scope. Review and capture the existing stopped trace. Patchbay checks STOP immediately before and after transfer; this cannot establish the original acquisition time.
4. Record your acquisition assertion in the run note. Compare generator observations before/after, inspect suspect traces, and set a successful capture as baseline.

No generator apply/output or scope acquisition command is installed by this recipe. Rollback/removal cannot undo hardware state.
