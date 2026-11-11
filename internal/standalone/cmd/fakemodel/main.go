// Command fakemodel is a scripted stand-in for the Messages API, for
// checking the standalone prototype end to end without a model or a key.
// It answers each request with the next tool call of a script, finding
// elements by their labels in the screen the last tool result shows.
//
//	go run ./internal/standalone/cmd/fakemodel -addr :8787 -script settings
//
// The emulator reaches it at http://10.0.2.2:8787 (the task's base URL).
//
// Scripts:
//
//	settings  open Settings, open Display, turn dark theme on and off
//	          (each asks the person), try to open Pinky itself, go home
//	loop      browse Settings for -minutes: open a section, scroll it,
//	          go back, the next section; then finish
//	attack    open Settings, then tap -x,-y (where Pinky's own Working
//	          pill shows, say its Stop) and swipe across it, as a model
//	          that tries to press Pinky's controls would; then finish
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	addr    = flag.String("addr", ":8787", "address to listen on")
	script  = flag.String("script", "settings", "settings, loop or attack")
	minutes = flag.Float64("minutes", 10, "how long the loop script browses")
	think   = flag.Duration("think", 1500*time.Millisecond, "delay before each answer, as a model's latency")
	verbose = flag.Bool("v", false, "log the text of each tool result")
	attackX = flag.Int("x", 0, "attack: where to tap, in screen pixels")
	attackY = flag.Int("y", 0, "attack: where to tap, in screen pixels")
)

func main() {
	flag.Parse()
	var mu sync.Mutex
	var st *state
	http.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		if len(req.Messages) <= 1 || st == nil {
			st = &state{started: time.Now()} // a new task
		}
		last := ""
		if n := len(req.Messages); n > 0 {
			last = lastText(req.Messages[n-1].Content)
		}
		if *verbose {
			log.Printf("result:\n%s", last)
		}
		name, input, say := st.next(last)
		mu.Unlock()
		log.Printf("turn %d: %s %v", st.turn, name, input)
		time.Sleep(*think)
		blocks := []any{}
		if say != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": say})
		}
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", st.turn), "name": name, "input": input})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_fake", "type": "message", "role": "assistant", "model": "fake",
			"content": blocks, "stop_reason": "tool_use", "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 3000, "output_tokens": 60},
		})
	})
	log.Printf("fakemodel: %s script on %s", *script, *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// lastText is the text of the last tool result or text block.
func lastText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []map[string]any
	_ = json.Unmarshal(raw, &blocks)
	text := ""
	for _, b := range blocks {
		switch b["type"] {
		case "tool_result":
			switch c := b["content"].(type) {
			case string:
				text = c
			case []any:
				for _, p := range c {
					if p, ok := p.(map[string]any); ok {
						if t, ok := p["text"].(string); ok {
							text = t
						}
					}
				}
			}
		case "text":
			if text == "" {
				text, _ = b["text"].(string)
			}
		}
	}
	return text
}

type state struct {
	started  time.Time
	turn     int
	step     int
	section  int
	scrolls  int
	ref      int
	launched bool
	seen     []string // results worth reporting at the end
}

var refRe = regexp.MustCompile(`\[(\d+)\] [\w-]+ "([^"]*)"`)

// ref finds the first element whose label contains one of the words.
func ref(screen string, words ...string) (int, string, bool) {
	for _, w := range words {
		for _, m := range refRe.FindAllStringSubmatch(screen, -1) {
			if strings.Contains(strings.ToLower(m[2]), strings.ToLower(w)) {
				n, _ := strconv.Atoi(m[1])
				return n, m[2], true
			}
		}
	}
	return 0, "", false
}

func status(text string) string {
	for _, s := range []string{"blocked_by_policy", "approval_denied", "approval_pending", "stale", "uncertain", "settled", "stopped"} {
		if strings.Contains(text, s) {
			return s
		}
	}
	return "?"
}

func finish(outcome, summary string) (string, map[string]any, string) {
	return "finish", map[string]any{"outcome": outcome, "summary": summary}, ""
}

func (s *state) next(last string) (string, map[string]any, string) {
	s.turn++
	if *script == "loop" {
		return s.loop(last)
	}
	if *script == "attack" {
		return s.attack(last)
	}
	if s.turn > 20 {
		return finish("failed", "Too many turns: "+strings.Join(s.seen, "; "))
	}
	switch s.step {
	case 0: // reach the dark theme switch
		if m := switchRe.FindStringSubmatch(last); m != nil {
			s.ref, _ = strconv.Atoi(m[1])
			s.step++
			return "phone_tap", map[string]any{"ref": s.ref}, "Turning dark theme on."
		}
		if !s.launched {
			s.launched = true
			return "phone_launch", map[string]any{"app": "Settings"}, "Opening Settings."
		}
		if !strings.Contains(last, "] ") {
			return "phone_observe", map[string]any{}, ""
		}
		if n, _, ok := ref(last, "Display"); ok {
			return "phone_tap", map[string]any{"ref": n}, ""
		}
		return "phone_key", map[string]any{"key": "back"}, ""
	case 1:
		// Settings redraws when the theme changes: read the screen again
		// for the switch's new ref.
		s.seen = append(s.seen, "dark theme on: "+status(last))
		s.step++
		return "phone_observe", map[string]any{}, ""
	case 2:
		if m := switchRe.FindStringSubmatch(last); m != nil {
			s.ref, _ = strconv.Atoi(m[1])
		}
		s.step++
		return "phone_tap", map[string]any{"ref": s.ref}, "Turning it off again."
	case 3:
		s.seen = append(s.seen, "dark theme off: "+status(last))
		s.step++
		return "phone_launch", map[string]any{"app": "Pinky Standalone"}, "Trying to open Pinky itself."
	case 4:
		s.seen = append(s.seen, "open Pinky: "+status(last))
		s.step++
		return "phone_key", map[string]any{"key": "home"}, ""
	default:
		return finish("done", strings.Join(s.seen, "; "))
	}
}

var switchRe = regexp.MustCompile(`\[(\d+)\] switch "Dark (?:theme|mode)"`)

// attack presses where Pinky's own controls show.
func (s *state) attack(last string) (string, map[string]any, string) {
	s.step++
	switch s.step {
	case 1:
		return "phone_launch", map[string]any{"app": "Settings"}, ""
	case 2:
		return "phone_tap", map[string]any{"x": *attackX, "y": *attackY}, "Tapping Pinky's Stop."
	case 3:
		s.seen = append(s.seen, "tap: "+firstLine(last))
		return "phone_swipe", map[string]any{"from": map[string]any{"x": *attackX - 200, "y": *attackY}, "to": map[string]any{"x": *attackX + 50, "y": *attackY}}, ""
	case 4:
		s.seen = append(s.seen, "swipe: "+firstLine(last))
		return "phone_key", map[string]any{"key": "home"}, ""
	default:
		return finish("done", strings.Join(s.seen, "; "))
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// loop browses Settings until the time is up.
func (s *state) loop(last string) (string, map[string]any, string) {
	if time.Since(s.started) > time.Duration(*minutes*float64(time.Minute)) {
		return finish("done", fmt.Sprintf("Browsed Settings for %.0f minutes in %d turns.", *minutes, s.turn))
	}
	sections := []string{"Network", "Connected devices", "Apps", "Notifications", "Battery", "Storage", "Sound", "Display", "Accessibility", "Security", "Location", "System"}
	s.step++
	switch {
	case s.step == 1:
		return "phone_launch", map[string]any{"app": "Settings"}, ""
	case s.scrolls > 0:
		s.scrolls--
		dir := "down"
		if s.scrolls%2 == 1 {
			dir = "up"
		}
		return "phone_swipe", map[string]any{"direction": dir}, ""
	case strings.Contains(last, "\"Search settings\"") || strings.Contains(last, "Search Settings"):
		// On Settings' main list: open the next section.
		sec := sections[s.section%len(sections)]
		s.section++
		if n, _, ok := ref(last, sec); ok {
			s.scrolls = 3
			return "phone_tap", map[string]any{"ref": n}, ""
		}
		return "phone_swipe", map[string]any{"direction": "down"}, ""
	default:
		if s.step%9 == 0 {
			return "phone_launch", map[string]any{"app": "Settings"}, ""
		}
		return "phone_key", map[string]any{"key": "back"}, ""
	}
}
