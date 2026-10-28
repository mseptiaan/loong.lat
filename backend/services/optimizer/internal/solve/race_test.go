//go:build race

package solve

// raceEnabled lets the heaviest tests scale themselves down: the race detector slows this CPU-bound solver
// by an order of magnitude, and what it is there to check (the presets sharing one clustering) does not
// need a dozen jobs.
const raceEnabled = true
