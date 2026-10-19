package clashapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

type usersTestInbound struct {
	adapter.Inbound
	tag   string
	stats []adapter.UserStat
}

func (i *usersTestInbound) Tag() string { return i.tag }

func (i *usersTestInbound) UserStats() []adapter.UserStat { return i.stats }

type usersTestInboundManager struct {
	adapter.InboundManager
	inbounds []adapter.Inbound
}

func (m *usersTestInboundManager) Inbounds() []adapter.Inbound { return m.inbounds }

func TestUsersSampler(t *testing.T) {
	alice := &usersTestInbound{tag: "sudoku-in", stats: []adapter.UserStat{
		{Name: "alice", Conns: 1, Up: 1000, Down: 2000, Used: 9000, Limit: 1e9},
	}}
	sampler := newUsersSampler(&usersTestInboundManager{inbounds: []adapter.Inbound{alice}})

	snapshot := sampler.snapshot()
	if snapshot.Count != 1 || snapshot.Active != 1 || len(snapshot.Users) != 1 {
		t.Fatalf("initial snapshot = %+v", snapshot)
	}
	user := snapshot.Users[0]
	if user.Name != "alice" || user.Up != 0 || user.Down != 0 {
		t.Fatalf("first sample must only report totals: %+v", user)
	}
	if user.UpTotal != 1000 || user.DownTotal != 2000 || user.Used != 9000 || user.Limit != 1e9 {
		t.Fatalf("totals = %+v", user)
	}

	// One second later 2000 bytes up and 500 bytes down were added.
	alice.stats[0].Up = 3000
	alice.stats[0].Down = 2500
	sampler.sample(time.Now().Add(time.Second))

	snapshot = sampler.snapshot()
	user = snapshot.Users[0]
	if user.Up != 2000 || user.Down != 500 {
		t.Fatalf("rates = up %d down %d, want 2000/500", user.Up, user.Down)
	}
	if snapshot.Up != 2000 || snapshot.Down != 500 {
		t.Fatalf("sum rates = %+v", snapshot)
	}
}

func TestUsersSamplerEmpty(t *testing.T) {
	sampler := newUsersSampler(nil)
	snapshot := sampler.snapshot()
	if snapshot.Count != 0 || snapshot.Active != 0 || snapshot.Users == nil || len(snapshot.Users) != 0 {
		t.Fatalf("empty snapshot = %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"count":0,"active":0,"up":0,"down":0,"users":[]}` {
		t.Fatalf("empty snapshot JSON = %s", encoded)
	}
}

func TestUsersSamplerDropsRemovedUsers(t *testing.T) {
	inbound := &usersTestInbound{tag: "sudoku-in", stats: []adapter.UserStat{{Name: "alice", Up: 1000}}}
	manager := &usersTestInboundManager{inbounds: []adapter.Inbound{inbound}}
	sampler := newUsersSampler(manager)

	inbound.stats = nil
	sampler.sample(time.Now().Add(time.Second))
	if len(sampler.snapshot().Users) != 0 {
		t.Fatalf("removed user still reported: %+v", sampler.snapshot())
	}
	if len(sampler.previous) != 0 {
		t.Fatalf("previous state kept for a removed user: %+v", sampler.previous)
	}
}
