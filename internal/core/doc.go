// Package core is the pure decision core of the auto-scaling controller.
//
// It implements docs/spec/decision-policy.md and the signal-quality rules of
// docs/spec/lifecycle-and-failures.md. Every exported function is a pure
// function of its arguments: no I/O, no clock reads, no package-level state.
// The current time and every AWS-derived value arrive inside the inputs.
//
// One control-loop cycle uses the core in three steps:
//
//	signals := core.Classify(obs, capacity, mem.LastConsumedCPU, cfg)
//	mem = core.Advance(mem, cycleID, signals, capacity, cfg)
//	decision := core.Decide(core.PolicyInput{...Memory: mem...})
package core
