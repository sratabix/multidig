package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func press(t *testing.T, m *Model, key tea.KeyPressMsg) {
	t.Helper()
	if _, cmd := m.handleKey(key); cmd != nil {
		t.Fatalf("key %q returned an unexpected command", key.String())
	}
}

func TestArrowsCycleSort(t *testing.T) {
	m := fixture()
	if m.sort != sortAnswer {
		t.Fatalf("initial sort = %s", m.sort)
	}

	want := []sortMode{sortRegion, sortLatency, sortResolver, sortAnswer}
	for _, w := range want {
		press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
		if m.sort != w {
			t.Fatalf("right gave %s, want %s", m.sort, w)
		}
	}

	press(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.sort != sortResolver {
		t.Fatalf("left gave %s, want %s", m.sort, sortResolver)
	}
}

func TestArrowsDoNotSwitchRecordType(t *testing.T) {
	m := fixture()
	press(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.activeType != 0 {
		t.Fatalf("right changed the record type to %d", m.activeType)
	}

	press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.activeTypeName() != "MX" {
		t.Fatalf("tab landed on %q, want MX", m.activeTypeName())
	}
	press(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.activeTypeName() != "A" {
		t.Fatalf("shift+tab landed on %q, want A", m.activeTypeName())
	}
}

func TestSortByLatencyKeepsFailuresLast(t *testing.T) {
	m := fixture()
	m.sort = sortLatency
	m.recompute()

	seenFailure := false
	for _, r := range m.rows {
		if r.Failed() {
			seenFailure = true
			continue
		}
		if seenFailure {
			t.Fatalf("%s sorts after a failed resolver", r.Server.IP)
		}
	}
}

func batchLen(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("startRun did not return a batch")
	}
	return len(batch)
}

func TestStartingARunSchedulesTheNextOne(t *testing.T) {
	m := New(Config{Domain: "example.com", Types: []string{"A"}, WatchEvery: time.Second, AutoRefresh: true})
	watching := batchLen(t, m.startRun())
	m.cancel()

	m.watch = false
	idle := batchLen(t, m.startRun())
	m.cancel()

	if watching != idle+1 {
		t.Fatalf("auto-refresh added %d commands to the run, want 1", watching-idle)
	}
}

func TestTheRerunIsTimedFromTheStartOfTheRun(t *testing.T) {
	m := New(Config{Domain: "example.com", Types: []string{"A"}, WatchEvery: 30 * time.Millisecond, AutoRefresh: true})
	m.runID = 7

	start := time.Now()
	msg := m.scheduleRerun()()
	elapsed := time.Since(start)

	if got, ok := msg.(rerunMsg); !ok || int(got) != 7 {
		t.Fatalf("scheduled %#v, want a rerun of run 7", msg)
	}
	if elapsed < 30*time.Millisecond {
		t.Errorf("the rerun fired after %s, want at least the 30ms interval", elapsed)
	}
}

func TestFinishingARunDoesNotDelayTheNextOne(t *testing.T) {
	m := New(Config{Domain: "example.com", Types: []string{"A"}, AutoRefresh: true})
	m.running = true

	_, cmd := m.Update(resultMsg{runID: m.runID, open: false})
	if m.running {
		t.Error("the run is still marked as running")
	}
	if cmd != nil {
		t.Error("the finished run scheduled a second rerun on top of the interval")
	}
}

func TestAutoRefreshDefaultsToTheDefaultInterval(t *testing.T) {
	m := New(Config{Domain: "example.com", Types: []string{"A"}, AutoRefresh: true})
	if !m.watch {
		t.Error("auto-refresh was requested but is off")
	}
	if m.cfg.WatchEvery != DefaultRefresh {
		t.Errorf("interval = %s, want %s", m.cfg.WatchEvery, DefaultRefresh)
	}
}

func TestAutoRefreshTogglesWithoutAnInterval(t *testing.T) {
	m := New(Config{Domain: "example.com", Types: []string{"A"}})
	if m.watch {
		t.Fatal("auto-refresh is on without being asked for")
	}

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'w'}); cmd == nil {
		t.Error("toggling auto-refresh on did not start a run")
	}
	if !m.watch {
		t.Fatal("w did not turn auto-refresh on")
	}
	if m.cancel != nil {
		m.cancel()
	}

	press(t, m, tea.KeyPressMsg{Code: 'w'})
	if m.watch {
		t.Error("w did not turn auto-refresh off")
	}
}
