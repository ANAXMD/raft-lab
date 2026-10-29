package main

import (
	"encoding/json"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

// Command defines the schema for incoming commands from JavaScript.
type Command struct {
	Kind string `json:"kind"`
	Node int    `json:"node"`
}

var (
	mu     sync.Mutex
	s      *sim.Sim
	stopCh chan struct{}
)

func start(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}

	var seed int64 = 1
	var onEventCallback js.Value

	if len(args) > 0 {
		if args[0].Type() == js.TypeNumber {
			seed = int64(args[0].Int())
		} else if args[0].Type() == js.TypeFunction {
			onEventCallback = args[0]
		}
	}
	if len(args) > 1 && args[1].Type() == js.TypeFunction {
		onEventCallback = args[1]
	}

	mu.Lock()
	if stopCh != nil {
		close(stopCh)
	}
	stop := make(chan struct{})
	stopCh = stop

	cfg := sim.Config{
		Seed: seed,
		OnEvent: func(ev sim.Event) {
			data, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if onEventCallback.Type() == js.TypeFunction {
				onEventCallback.Invoke(string(data))
			}
		},
	}
	s = sim.New(cfg)
	mu.Unlock()

	go func(stop <-chan struct{}) {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				if s != nil {
					s.Run(10)
				}
				mu.Unlock()
			}
		}
	}(stop)

	return nil
}

func sendCommand(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return nil
	}

	var raw []byte
	if args[0].Type() == js.TypeString {
		raw = []byte(args[0].String())
	} else if args[0].Type() == js.TypeObject {
		str := js.Global().Get("JSON").Call("stringify", args[0]).String()
		raw = []byte(str)
	} else {
		return nil
	}

	var cmd Command
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	if s == nil {
		return nil
	}

	switch strings.TrimSpace(strings.ToLower(cmd.Kind)) {
	case "kill", "crash":
		s.Crash(raft.NodeID(cmd.Node))
	case "start", "restart":
		s.Restart(raft.NodeID(cmd.Node))
	case "isolate":
		s.Isolate(raft.NodeID(cmd.Node))
	case "heal":
		s.Heal()
	}

	return nil
}

func main() {
	raftSim := js.Global().Get("Object").New()
	raftSim.Set("start", js.FuncOf(start))
	raftSim.Set("sendCommand", js.FuncOf(sendCommand))
	js.Global().Set("RaftSim", raftSim)

	select {}
}
