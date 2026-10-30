//go:build js && wasm

// Command wasm exposes the deterministic simulator to the browser as a global
// `RaftSim` object:
//
//	RaftSim.start(seed, onEvent[, optionsJSON])  begin a live session
//	RaftSim.sendCommand(commandJSON) -> string   "" on success, else an error message
//	RaftSim.exportSession() -> string            JSON recording of the run so far
//	RaftSim.replay(sessionJSON, onEvent) -> string
//	                                             re-run a recording (synchronously emits every
//	                                             event from t=0), then continue it live
//	RaftSim.setSpeed(x)                          virtual seconds per wall second (0 = paused)
//
// Wall-clock pacing only decides HOW OFTEN the simulator is stepped. Every user
// command is applied through Sim.Do, which stamps it with the virtual time at
// which it took effect, so the exported session replays bit for bit no matter
// how the live run was paced.
package main

import (
	"encoding/json"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/ANAXMD/raft-lab/sim"
)

const tickWall = 20 * time.Millisecond

var (
	mu     sync.Mutex
	s      *sim.Sim
	stopCh chan struct{}
	speed  = 0.5 // virtual seconds per wall second
)

// options are the optional settings accepted by RaftSim.start.
type options struct {
	N           int     `json:"n"`
	PreVote     bool    `json:"preVote"`
	CheckQuorum bool    `json:"checkQuorum"`
	SkipSync    bool    `json:"skipSync"`
	DropProb    float64 `json:"dropProb"`
	DupProb     float64 `json:"dupProb"`
}

// legacy aliases used by the first version of the UI
var kindAlias = map[string]string{"kill": sim.CmdCrash, "start": sim.CmdRestart}

func emitter(cb js.Value) func(sim.Event) {
	return func(ev sim.Event) {
		if cb.Type() != js.TypeFunction {
			return
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		cb.Invoke(string(data))
	}
}

// begin stops any running session and starts the pacing loop for sim x.
// The caller must hold mu.
func begin(x *sim.Sim) {
	if stopCh != nil {
		close(stopCh)
	}
	stop := make(chan struct{})
	stopCh = stop
	s = x

	go func() {
		ticker := time.NewTicker(tickWall)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				if s == x && speed > 0 {
					step := int64(speed * float64(tickWall/time.Millisecond))
					if step < 1 {
						step = 1
					}
					s.Run(step)
				}
				mu.Unlock()
			}
		}
	}()
}

func start(this js.Value, args []js.Value) any {
	var seed int64 = 1
	var cb js.Value
	var opts options
	for i, a := range args {
		switch {
		case i == 0 && a.Type() == js.TypeNumber:
			seed = int64(a.Int())
		case a.Type() == js.TypeFunction:
			cb = a
		case a.Type() == js.TypeString:
			_ = json.Unmarshal([]byte(a.String()), &opts)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	begin(sim.New(sim.Config{
		Seed: seed, N: opts.N, PreVote: opts.PreVote, CheckQuorum: opts.CheckQuorum,
		SkipSync: opts.SkipSync, DropProb: opts.DropProb, DupProb: opts.DupProb,
		OnEvent: emitter(cb),
	}))
	return nil
}

func sendCommand(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return "missing command"
	}
	var raw []byte
	switch args[0].Type() {
	case js.TypeString:
		raw = []byte(args[0].String())
	case js.TypeObject:
		raw = []byte(js.Global().Get("JSON").Call("stringify", args[0]).String())
	default:
		return "command must be a JSON string or object"
	}

	var cmd sim.Command
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return "bad command: " + err.Error()
	}
	cmd.Kind = strings.ToLower(strings.TrimSpace(cmd.Kind))
	if k, ok := kindAlias[cmd.Kind]; ok {
		cmd.Kind = k
	}

	mu.Lock()
	defer mu.Unlock()
	if s == nil {
		return "no session running"
	}
	if err := s.Do(cmd); err != nil {
		return err.Error()
	}
	return ""
}

func exportSession(this js.Value, args []js.Value) any {
	mu.Lock()
	defer mu.Unlock()
	if s == nil {
		return ""
	}
	data, err := json.MarshalIndent(s.Session(), "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func replay(this js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return "replay needs a session JSON string"
	}
	f, err := sim.ParseSession([]byte(args[0].String()))
	if err != nil {
		return err.Error()
	}
	var cb js.Value
	if len(args) > 1 {
		cb = args[1]
	}

	mu.Lock()
	defer mu.Unlock()
	r, err := sim.Replay(f, emitter(cb))
	if err != nil {
		return err.Error()
	}
	begin(r) // the replayed run carries on live from where the recording ended
	return ""
}

func setSpeed(this js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeNumber {
		return nil
	}
	v := args[0].Float()
	if v < 0 {
		v = 0
	}
	if v > 20 {
		v = 20
	}
	mu.Lock()
	speed = v
	mu.Unlock()
	return nil
}

func main() {
	api := js.Global().Get("Object").New()
	api.Set("start", js.FuncOf(start))
	api.Set("sendCommand", js.FuncOf(sendCommand))
	api.Set("exportSession", js.FuncOf(exportSession))
	api.Set("replay", js.FuncOf(replay))
	api.Set("setSpeed", js.FuncOf(setSpeed))
	js.Global().Set("RaftSim", api)

	select {}
}
