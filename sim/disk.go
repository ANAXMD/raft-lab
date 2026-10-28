package sim

import "github.com/ANAXMD/raft-lab/raft"

// Disk is a fake disk with the one property that matters for Raft:
// writes are NOT durable until Sync(). Crash() throws away everything
// written since the last Sync(), exactly like pulling the power cord.
type Disk struct {
	durable raft.Persisted
	pending []op
}

type op struct {
	hs   *raft.HardState
	ents []raft.Entry
}

// Save buffers a write (hard state and/or log suffix replacement).
func (d *Disk) Save(hs *raft.HardState, ents []raft.Entry) {
	var o op
	if hs != nil {
		c := *hs
		o.hs = &c
	}
	o.ents = append([]raft.Entry(nil), ents...)
	d.pending = append(d.pending, o)
}

// Sync makes all buffered writes durable.
func (d *Disk) Sync() {
	for _, o := range d.pending {
		if o.hs != nil {
			d.durable.HardState = *o.hs
		}
		if len(o.ents) > 0 {
			first := int(o.ents[0].Index)
			if first-1 > len(d.durable.Log) {
				panic("sim.Disk: log gap on disk (driver bug)")
			}
			d.durable.Log = append(d.durable.Log[:first-1], o.ents...)
		}
	}
	d.pending = nil
}

// Crash drops every write that was not synced.
func (d *Disk) Crash() { d.pending = nil }

// Load returns a copy of the durable state, as a restarting node would read it.
func (d *Disk) Load() raft.Persisted {
	return raft.Persisted{
		HardState: d.durable.HardState,
		Log:       append([]raft.Entry(nil), d.durable.Log...),
	}
}
