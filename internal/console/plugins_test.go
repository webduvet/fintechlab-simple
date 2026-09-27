package console

import (
	"errors"
	"testing"
	"time"
)

func plugin(id string) Plugin {
	return Plugin{
		ID: id, Name: "Platform " + id, BaseURL: "http://127.0.0.1:3109/",
		HealthPath: "/status", Clock: ClockFollows,
		Actions: []PluginAction{{ID: "sweep", Label: "Sweep", Path: "/sim/sweep"}},
	}
}

// TestARegistrationLapsesWithoutRenewalAndRenewingRevivesIt: a platform that
// stops renewing is "lapsed" after the TTL, not deleted, and the next renewal
// makes it live again with the descriptor it sent.
func TestARegistrationLapsesWithoutRenewalAndRenewingRevivesIt(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	c := &Catalogue{now: func() time.Time { return now }}
	if _, err := c.Register(plugin("p1")); err != nil {
		t.Fatal(err)
	}
	if _, info, _ := c.Plugin("p1"); info.State != PluginLive {
		t.Fatalf("fresh registration is %s", info.State)
	}
	now = now.Add(PluginTTL + time.Second)
	if _, info, _ := c.Plugin("p1"); info.State != PluginLapsed {
		t.Fatalf("after the TTL it is %s", info.State)
	}
	renewed := plugin("p1")
	renewed.Name = "Renamed"
	if _, err := c.Register(renewed); err != nil {
		t.Fatal(err)
	}
	s, ok := c.Get("p1")
	if !ok || s.Name != "Renamed" || s.Plugin.State != PluginLive || s.BaseURL != "http://127.0.0.1:3109" {
		t.Fatalf("after renewal: %+v %+v", s, s.Plugin)
	}
}

// TestPluginsComeFirstAndStayOutOfTheLabsOwnList: All puts the system under
// test first; the lab's own Services slice — which decides what counts as
// "the lab talking to itself" in the diagram — never gains a plugin.
func TestPluginsComeFirstAndStayOutOfTheLabsOwnList(t *testing.T) {
	c := &Catalogue{Services: []Service{{ID: "worldline"}}}
	if _, err := c.Register(plugin("p1")); err != nil {
		t.Fatal(err)
	}
	all := c.All()
	if len(all) != 2 || all[0].ID != "p1" || all[1].ID != "worldline" {
		t.Fatalf("All = %v", all)
	}
	if len(c.Services) != 1 {
		t.Fatal("registering a plugin changed the lab's own list")
	}
}

// TestOnlyALiveWallClockPlatformLocksTheClock.
func TestOnlyALiveWallClockPlatformLocksTheClock(t *testing.T) {
	c := &Catalogue{}
	if _, err := c.Register(plugin("p1")); err != nil {
		t.Fatal(err)
	}
	if _, locked := c.WallClockPlugin(); locked {
		t.Fatal("a following platform locked the clock")
	}
	wall := plugin("p2")
	wall.Clock = ClockWall
	if _, err := c.Register(wall); err != nil {
		t.Fatal(err)
	}
	if name, locked := c.WallClockPlugin(); !locked || name != "Platform p2" {
		t.Fatalf("wall platform: %q %v", name, locked)
	}
	c.Unregister("p2")
	if _, locked := c.WallClockPlugin(); locked {
		t.Fatal("a stopped wall platform still locks the clock")
	}
}

func TestABadDescriptorSaysWhy(t *testing.T) {
	p := plugin("p1")
	p.Actions = append(p.Actions, PluginAction{ID: "sweep", Label: "again", Path: "/x"})
	_, err := (&Catalogue{}).Register(p)
	if !errors.Is(err, ErrBadPlugin) {
		t.Fatalf("duplicate action id: %v", err)
	}
}
