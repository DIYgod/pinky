package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/api"
)

// runAsk gives Pinky's own agent a task on a phone and waits for the
// outcome: `pinky ask <device> "<task>"`. The gateway runs the task with
// the model set in Settings > Agents > Ask Pinky.
func runAsk(args []string) error {
	fs := flag.NewFlagSet("pinky ask", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New(`usage: pinky ask <device> "<task>"`)
	}
	device, task := rest[0], strings.Join(rest[1:], " ")

	ctl, err := control()
	if err != nil {
		return err
	}
	defer ctl.Close()

	var res struct {
		End     string  `json:"end"`
		Summary string  `json:"summary"`
		Steps   int     `json:"steps"`
		Cost    float64 `json:"cost"`
		Tokens  int64   `json:"tokens"`
		Priced  bool    `json:"priced"`
	}
	if err := ctl.Call("ask", api.AskParams{Device: device, Task: task}, &res); err != nil {
		return err
	}

	cost := ""
	switch {
	case res.Priced && res.Cost > 0:
		cost = fmt.Sprintf(", $%.3f", res.Cost)
	case res.Tokens > 0:
		cost = fmt.Sprintf(", %d tokens", res.Tokens)
	}
	fmt.Printf("%s (%d steps%s)\n", res.End, res.Steps, cost)
	if res.Summary != "" {
		fmt.Println(res.Summary)
	}
	return nil
}
