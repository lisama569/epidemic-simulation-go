// Command episim runs local, deterministic epidemic scenarios:
//
//	episim demo                 built-in grouped SEIR + capacity demo
//	episim demo-metapop         built-in metapopulation demo
//	episim run -f scenario.json run a (possibly legacy v1) config file
//	episim gen -f scenario.json generate seeded synthetic observations
//	episim estimate -f scenario.json [-cp path]  estimate beta with CI and
//	                                            resumable checkpoint
//	episim determinism -f scenario.json         compare serial vs parallel runs
//	episim resume-demo           demonstrate checkpoint-continued estimation
//
// Everything runs locally with generated inputs; no network or real data.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/estimate"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "demo":
		err = runScenario(demoScenario(), "demo grouped SEIR")
	case "demo-metapop":
		err = runScenario(demoMetapop(), "demo metapop SEIR")
	case "run":
		err = cmdRun(args)
	case "gen":
		err = cmdGen(args)
	case "estimate":
		err = cmdEstimate(args)
	case "determinism":
		err = cmdDeterminism(args)
	case "resume-demo":
		err = cmdResumeDemo()
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		if ve, ok := model.AsValidationError(err); ok {
			fmt.Fprintf(os.Stderr, "REJECTED [%s]: %s\n", ve.Reason, ve.Message)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: episim [demo|demo-metapop|run|gen|estimate|determinism|resume-demo] [-f file] [-cp checkpoint]")
}

func loadScenarioFile(args []string) (*model.Scenario, string, error) {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	file := fs.String("f", "", "scenario JSON file (v2 or legacy v1)")
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return nil, "", err
	}
	sc, err := model.Normalize(raw)
	if err != nil {
		return nil, "", err
	}
	return sc, *file, nil
}

func cmdRun(args []string) error {
	sc, path, err := loadScenarioFile(args)
	if err != nil {
		return err
	}
	return runScenario(sc, "config "+path)
}

func runScenario(sc *model.Scenario, label string) error {
	fmt.Printf("== %s ==\n", label)
	fmt.Println(sc.String())
	if err := sc.Validate(); err != nil {
		return err
	}
	res, err := engine.Run(sc)
	if err != nil {
		return err
	}
	last := res.Trajectory[len(res.Trajectory)-1]
	var s, e, i, q, r float64
	for g := range last.Pop {
		s += last.Pop[g][model.Susceptible]
		e += last.Pop[g][model.Exposed]
		i += last.Pop[g][model.Infected]
		q += last.Pop[g][model.Quarantined]
		r += last.Pop[g][model.Recovered]
	}
	fmt.Printf("day=%.1f final: S=%.1f E=%.1f I=%.1f Q=%.1f R=%.1f total=%.1f\n",
		last.T, s, e, i, q, r, s+e+i+q+r)
	var peakI float64
	var peakDay float64
	var totalInf float64
	for _, st := range res.Trajectory {
		var ii float64
		for g := range st.Pop {
			ii += st.Pop[g][model.Infected]
		}
		if ii > peakI {
			peakI, peakDay = ii, st.T
		}
	}
	for _, row := range res.NewInfections {
		for _, v := range row {
			totalInf += v
		}
	}
	fmt.Printf("peak infected=%.1f at day %.1f; cumulative infections=%.1f\n", peakI, peakDay, totalInf)
	fmt.Printf("capacity events: %d\n", len(res.CapacityEvents))
	for _, ev := range res.CapacityEvents {
		fmt.Printf("  day=%.2f group=%d %s requested=%.1f applied=%.1f (%s)\n",
			ev.Day, ev.Group, ev.Kind, ev.Requested, ev.Applied, ev.Reason)
	}
	return nil
}

func cmdGen(args []string) error {
	sc, _, err := loadScenarioFile(args)
	if err != nil {
		return err
	}
	seq, err := synthetic.Generate(sc)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(seq)
}

func cmdEstimate(args []string) error {
	fs := flag.NewFlagSet("estimate", flag.ContinueOnError)
	file := fs.String("f", "", "scenario JSON file")
	cp := fs.String("cp", "", "checkpoint path for resumable estimation")
	lo := fs.Float64("lo", 0.0, "beta bracket low")
	hi := fs.Float64("hi", 2.0, "beta bracket high")
	iters := fs.Int("iters", 100, "max iterations")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	sc, err := model.Normalize(raw)
	if err != nil {
		return err
	}
	// Observations are generated locally from the scenario's own seeded RNG;
	// in real use this sequence would be the input data series.
	obs, err := synthetic.Generate(sc)
	if err != nil {
		return err
	}
	res, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: *lo, BetaHigh: *hi, Tolerance: 1e-5, MaxIterations: *iters,
		CheckpointPath: *cp,
	})
	if err != nil {
		return err
	}
	fmt.Printf("status=%s resumed=%v iters=%d beta_hat=%.6f CI95=[%.6f,%.6f] clipped=%v nll=%.4f\n",
		res.Status, res.Resumed, res.Iterations, res.BetaHat, res.CI95Lower, res.CI95Upper, res.CIClipped, res.FinalNLL)
	fmt.Printf("judgement: %s\n", res.Why)
	fmt.Printf("(true beta used to generate observations = %.6f)\n", obs.Beta)
	return nil
}

func cmdDeterminism(args []string) error {
	sc, _, err := loadScenarioFile(args)
	if err != nil {
		return err
	}
	scenarios := make([]*model.Scenario, 8)
	for k := range scenarios {
		scenarios[k] = sc
	}
	serial, serr := engine.RunScenarios(scenarios, 1)
	parallel, perr := engine.RunScenarios(scenarios, 4)
	for k := range scenarios {
		if serr[k] != nil || perr[k] != nil {
			return fmt.Errorf("run %d failed: %v/%v", k, serr[k], perr[k])
		}
		if !trajectoriesEqual(serial[k].Trajectory, parallel[k].Trajectory) {
			return fmt.Errorf("run %d differs between serial and parallel", k)
		}
	}
	fmt.Println("determinism OK: 8 scenarios identical with 1 and 4 workers")
	return nil
}

func trajectoriesEqual(a, b []*model.State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].T != b[i].T {
			return false
		}
		for g := range a[i].Pop {
			for c := range a[i].Pop[g] {
				if a[i].Pop[g][c] != b[i].Pop[g][c] {
					return false
				}
			}
		}
	}
	return true
}
