// Command rollguard pushes config changes in waves and rolls back on SLO
// breaches. Right now it drives a simulated fleet; the engine only needs the
// rollout.Fleet and rollout.Signals interfaces to point at a real one.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/youssefm1/rollguard/internal/config"
	"github.com/youssefm1/rollguard/internal/demo"
	"github.com/youssefm1/rollguard/internal/rollout"
	"github.com/youssefm1/rollguard/internal/sim"
)

const usage = `rollguard: progressive config rollout with SLO-based rollback

usage:
  rollguard demo  [-scenario timeout|latency|safe|all] [-out dir] [-seed n] [-cells n] [-quiet]
  rollguard push  -config new.json [-cells n] [-waves 1,10,50,100] [-bake 60s] [-events out.jsonl]
  rollguard plan  [-cells n] [-waves 1,10,50,100] [-change id]
  rollguard check -config new.json
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "push":
		err = cmdPush(os.Args[2:])
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rollguard:", err)
		os.Exit(1)
	}
}

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	scen := fs.String("scenario", "timeout", "timeout, latency, safe, or all")
	out := fs.String("out", "out", "directory for graphs and event logs")
	seed := fs.Int64("seed", 1, "random seed")
	cells := fs.Int("cells", 200, "fleet size")
	quiet := fs.Bool("quiet", false, "only print the summary")
	fs.Parse(args)

	names := []string{*scen}
	if *scen == "all" {
		names = []string{"timeout", "latency", "safe"}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		sc, ok := demo.Scenarios[name]
		if !ok {
			return fmt.Errorf("unknown scenario %q", name)
		}
		p := demo.DefaultParams()
		p.Seed = *seed
		p.Cells = *cells
		var logs = map[string][]rollout.Event{}
		p.OnEvent = func(label string, ev rollout.Event, at time.Duration) {
			logs[label] = append(logs[label], ev)
			if !*quiet && label == "progressive" {
				printEvent(at, ev)
			}
		}
		fmt.Printf("== scenario %s: %s\n", sc.Name, sc.Story)
		for _, ch := range config.Diff(demo.Baseline, sc.Apply(demo.Baseline)) {
			fmt.Println("  ", ch)
		}
		prog, bb, err := demo.Compare(sc, p)
		if err != nil {
			return err
		}
		fmt.Println()
		summary(prog)
		summary(bb)

		svg := filepath.Join(*out, sc.Name+".svg")
		if err := os.WriteFile(svg, demo.Graph(sc, prog, bb), 0o644); err != nil {
			return err
		}
		for label, evs := range logs {
			f := filepath.Join(*out, sc.Name+"-"+strings.ReplaceAll(label, " ", "-")+".jsonl")
			if err := writeEvents(f, evs); err != nil {
				return err
			}
		}
		fmt.Printf("  graph: %s\n\n", svg)
	}
	return nil
}

func summary(r demo.Run) {
	res := r.Result
	line := fmt.Sprintf("  %-12s %-11s exposed %3d/%d cells", r.Label, res.Outcome, res.ExposedCells, r.Cells)
	if res.Outcome != rollout.Completed {
		line += fmt.Sprintf(", caught in wave %d after %s", res.FailedWave, res.TimeToDetect())
	}
	line += fmt.Sprintf(", peak fleet err %.2f%%, %.0f failed requests above baseline", r.PeakFleetErr()*100, r.ExcessErrors())
	fmt.Println(line)
}

func printEvent(at time.Duration, ev rollout.Event) {
	t := fmt.Sprintf("t+%3.0fs", at.Seconds())
	switch ev.Kind {
	case "sample":
		fmt.Printf("  %s  %-6s canary err %6.2f%% p99 %5s | control err %5.2f%% p99 %5s  %s\n", t, waveTag(ev.Wave),
			ev.CanaryErr*100, ev.CanaryP99.Round(time.Millisecond), ev.ControlErr*100, ev.ControlP99.Round(time.Millisecond), ev.Detail)
	case "push":
		fmt.Printf("  %s  %-6s push %g%% (+%d cells) %s\n", t, waveTag(ev.Wave), ev.Percent, ev.Cells, ev.Detail)
	default:
		fmt.Printf("  %s  %-6s %s %s\n", t, waveTag(ev.Wave), strings.ToUpper(ev.Kind), ev.Detail)
	}
}

func waveTag(w int) string {
	if w == 0 {
		return ""
	}
	return fmt.Sprintf("w%d", w)
}

func cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	path := fs.String("config", "", "config to roll out (JSON)")
	cells := fs.Int("cells", 200, "simulated fleet size")
	waves := fs.String("waves", "1,10,50,100", "cumulative wave percents")
	bake := fs.Duration("bake", 60*time.Second, "watch time per wave")
	events := fs.String("events", "", "write the event log here (JSONL)")
	seed := fs.Int64("seed", 1, "random seed")
	fs.Parse(args)
	if *path == "" {
		return fmt.Errorf("push needs -config")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := config.DefaultSchema.Validate(cfg); err != nil {
		return err
	}
	pcts, err := parseWaves(*waves)
	if err != nil {
		return err
	}

	clock := sim.NewClock(time.Second)
	fleet := sim.NewFleet(*cells, demo.DefaultParams().Regions, demo.Baseline, sim.DefaultModel, *seed)
	clock.OnTick(func(time.Time) { fleet.Step(clock.Step()) })
	clock.Sleep(10 * time.Second)

	fmt.Printf("rolling out %s (%s)\n", cfg.Name, cfg.Version())
	for _, ch := range config.Diff(demo.Baseline, cfg) {
		fmt.Println("  ", ch)
	}
	opts := rollout.DefaultOptions()
	opts.Waves = pcts
	opts.Bake = *bake
	opts.OnEvent = func(ev rollout.Event) { printEvent(clock.Elapsed(), ev) }
	res, err := rollout.New(fleet, fleet, clock, opts).Run(context.Background(), cfg)
	if *events != "" {
		if werr := writeEvents(*events, res.Events); werr != nil {
			return werr
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("\n%s", res.Outcome)
	if res.Reason != "" {
		fmt.Printf(": %s", res.Reason)
	}
	fmt.Println()
	if res.Outcome != rollout.Completed {
		os.Exit(3)
	}
	return nil
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	cells := fs.Int("cells", 200, "fleet size")
	waves := fs.String("waves", "1,10,50,100", "cumulative wave percents")
	change := fs.String("change", "example", "change id (salts cell selection)")
	fs.Parse(args)
	pcts, err := parseWaves(*waves)
	if err != nil {
		return err
	}
	fleet := sim.NewFleet(*cells, demo.DefaultParams().Regions, demo.Baseline, sim.DefaultModel, 1)
	plan, err := rollout.NewPlan(fleet.Cells(), pcts, *change)
	if err != nil {
		return err
	}
	for _, w := range plan.Waves {
		regions := map[string]int{}
		for _, c := range w.Cells {
			regions[c[:strings.LastIndex(c, "-")]]++
		}
		var rs []string
		for r, n := range regions {
			rs = append(rs, fmt.Sprintf("%s:%d", r, n))
		}
		sort.Strings(rs)
		fmt.Printf("wave %d  %5g%%  +%-3d cells (total %3d)  %s\n", w.Index, w.Percent, len(w.Cells), w.Cumulative, strings.Join(rs, " "))
	}
	return nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	path := fs.String("config", "", "config to validate (JSON)")
	fs.Parse(args)
	if *path == "" {
		return fmt.Errorf("check needs -config")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := config.DefaultSchema.Validate(cfg); err != nil {
		return err
	}
	fmt.Printf("%s ok (version %s)\n", cfg.Name, cfg.Version())
	return nil
}

func parseWaves(s string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("bad wave %q", part)
		}
		out = append(out, v)
	}
	return out, nil
}

func writeEvents(path string, evs []rollout.Event) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, ev := range evs {
		if err := enc.Encode(ev); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}
