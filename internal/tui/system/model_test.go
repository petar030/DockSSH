package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
)

type fakeBackend struct {
	steps    []string
	requests int
	sub      *fakeSub
}

func (f *fakeBackend) RequestRefresh(p backend.Page) error {
	f.steps = append(f.steps, "refresh:"+string(p))
	f.requests++
	return nil
}
func (f *fakeBackend) Subscribe(context.Context, backend.Page, backend.EventFilter) (backend.Subscription, error) {
	f.steps = append(f.steps, "subscribe")
	f.sub = &fakeSub{events: make(chan backend.EventEnvelope, 2)}
	return f.sub, nil
}

type fakeSub struct {
	events chan backend.EventEnvelope
	closed bool
}

func (s *fakeSub) Events() <-chan backend.EventEnvelope { return s.events }
func (s *fakeSub) Close() error                         { s.closed = true; return nil }

type fakeAPI struct {
	diskRequests int
	container    []backendsystem.ContainerPruneOptions
	image        []backendsystem.ImagePruneOptions
	volume       []backendsystem.VolumePruneOptions
	network      []backendsystem.NetworkPruneOptions
	system       []backendsystem.SystemPruneOptions
	result       backendsystem.PruneResult
	err          error
}

func (f *fakeAPI) RequestDiskUsage() error { f.diskRequests++; return nil }
func (f *fakeAPI) PruneContainers(_ context.Context, v backendsystem.ContainerPruneOptions) (backendsystem.PruneResult, error) {
	f.container = append(f.container, v)
	return f.result, f.err
}
func (f *fakeAPI) PruneImages(_ context.Context, v backendsystem.ImagePruneOptions) (backendsystem.PruneResult, error) {
	f.image = append(f.image, v)
	return f.result, f.err
}
func (f *fakeAPI) PruneVolumes(_ context.Context, v backendsystem.VolumePruneOptions) (backendsystem.PruneResult, error) {
	f.volume = append(f.volume, v)
	return f.result, f.err
}
func (f *fakeAPI) PruneNetworks(_ context.Context, v backendsystem.NetworkPruneOptions) (backendsystem.PruneResult, error) {
	f.network = append(f.network, v)
	return f.result, f.err
}
func (f *fakeAPI) PruneSystem(_ context.Context, v backendsystem.SystemPruneOptions) (backendsystem.PruneResult, error) {
	f.system = append(f.system, v)
	return f.result, f.err
}
func ready(t *testing.T) (Model, *fakeBackend, *fakeAPI) {
	b, a := &fakeBackend{}, &fakeAPI{}
	m, c := New(context.Background(), b, a).Activate()
	m, c = m.Update(c())
	batch := c().(tea.BatchMsg)
	_ = batch[1]()
	_ = batch[2]()
	if strings.Join(b.steps, ",") != "subscribe,refresh:system" || a.diskRequests != 1 {
		t.Fatalf("startup=%v disk=%d", b.steps, a.diskRequests)
	}
	return m, b, a
}
func key(v string) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Text: v, Code: []rune(v)[0]}) }

func TestInfoDiskKeyOverflowPreservesAndSanitizes(t *testing.T) {
	m, b, a := ready(t)
	now := time.Now()
	m, _ = m.Update(eventReceivedMsg{generation: m.generation, open: true, event: backend.EventEnvelope{Time: now, Key: backend.RefreshKey{Kind: backendsystem.RefreshKindInfo}, Payload: backendsystem.InfoUpdated{Engine: backendsystem.EngineVersion{Version: "29\x1b[31m", APIVersion: "1.52"}, Host: backendsystem.HostInfo{Name: "host\nunsafe", CPUs: 4}}}})
	wrong := backendsystem.DiskUsageUpdated{Images: backendsystem.ResourceDiskUsage{Count: 99}}
	m, _ = m.Update(eventReceivedMsg{generation: m.generation, open: true, event: backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendsystem.RefreshKindInfo}, Payload: wrong}})
	if m.hasDisk {
		t.Fatal("mismatched disk key applied")
	}
	disk := backendsystem.DiskUsageUpdated{Images: backendsystem.ResourceDiskUsage{Count: 2}, VolumeItems: []backendsystem.VolumeDiskUsage{{Name: "v", Size: 0}}}
	m, _ = m.Update(eventReceivedMsg{generation: m.generation, open: true, event: backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendsystem.RefreshKindDiskUsage}, Payload: disk}})
	m.diskMode = 3
	view := m.SetSize(120, 25).View()
	if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "host\nunsafe") || !strings.Contains(view, "unknown or 0 B") {
		t.Fatalf("view=%s", view)
	}
	if narrow := m.SetSize(90, 15).View(); !strings.Contains(narrow, "VOLUMES") {
		t.Fatalf("narrow layout hid disk usage: %s", narrow)
	}
	oldInfo := m.info
	m, _ = m.Update(eventReceivedMsg{generation: m.generation, open: true, event: backend.EventEnvelope{Key: backend.RefreshKey{Kind: backendsystem.RefreshKindInfo}, Payload: backend.RefreshFailed{Err: errors.New("fail")}}})
	if m.info.Engine.Version != oldInfo.Engine.Version || !m.stale {
		t.Fatal("failure discarded last info")
	}
	beforeB, beforeD := b.requests, a.diskRequests
	m, c := m.Update(eventReceivedMsg{generation: m.generation, open: true, event: backend.EventEnvelope{Payload: backend.SubscriberOverflow{}}})
	batch := c().(tea.BatchMsg)
	recovery := batch[1]().(tea.BatchMsg)
	_ = recovery[0]()
	_ = recovery[1]()
	if b.requests != beforeB+1 || a.diskRequests != beforeD+1 {
		t.Fatalf("overflow=%d/%d", b.requests-beforeB, a.diskRequests-beforeD)
	}
}

func TestEveryScopedPruneGuardAndShapes(t *testing.T) {
	m, _, a := ready(t)
	for _, kind := range []overlayMode{containerPrune, imagePrune, volumePrune, networkPrune} {
		m.openForm(kind)
		var c tea.Cmd
		m, c = m.handleOverlay(key("enter"))
		if c != nil {
			t.Fatalf("empty %v prune submitted", kind)
		}
	}
	m.openForm(containerPrune)
	m.fields[0] = "24h"
	m, c := m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.container) != 1 || a.container[0].Until != "24h" {
		t.Fatalf("container=%+v", a.container)
	}
	m.openForm(imagePrune)
	m.dangling = true
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.image) != 1 || a.image[0].Dangling == nil || !*a.image[0].Dangling {
		t.Fatalf("image=%+v", a.image)
	}
	m.openForm(volumePrune)
	m.fields[1] = "fixture"
	m.fields[2] = "yes"
	m.allVolumes = true
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.volume) != 1 || !a.volume[0].All || a.volume[0].Labels["fixture"] != "yes" {
		t.Fatalf("volume=%+v", a.volume)
	}
	m.openForm(networkPrune)
	m.fields[1] = "fixture"
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if len(a.network) != 1 {
		t.Fatal("network prune missing")
	}
}

func TestSystemTokenPolicyReportAndJoinedOutcome(t *testing.T) {
	m, _, a := ready(t)
	m.openForm(systemPrune)
	m.fields[0] = "wrong"
	m, c := m.handleOverlay(key("enter"))
	if c != nil || len(a.system) != 0 {
		t.Fatal("wrong token submitted")
	}
	a.err = &backend.AppError{Code: backend.ErrorPermissionDenied, Operation: "prune system"}
	m.fields[0] = backendsystem.SystemPruneConfirmation
	m.includeVolumes = true
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if !m.systemPruneDenied || m.overlay != systemPrune || len(a.system) != 1 {
		t.Fatal("policy denial not retained")
	}
	m.systemPruneDenied = false
	a.err = nil
	a.result = backendsystem.PruneResult{Report: backendsystem.PruneReport{ContainersDeleted: []string{"one"}, SpaceReclaimed: 1024}}
	m.openForm(containerPrune)
	m.fields[0] = "24h"
	m, c = m.handleOverlay(key("enter"))
	m, disk := m.Update(c())
	if !m.reportVisible || !strings.Contains(m.reportView(), "1.0 KiB") || disk == nil {
		t.Fatal("typed report not shown")
	}
	a.err = errors.New("refresh submission failed")
	a.result = backendsystem.PruneResult{Command: backend.CommandResult{OperationID: "system.prune.containers"}}
	m.openForm(containerPrune)
	m.fields[0] = "24h"
	m, c = m.handleOverlay(key("enter"))
	m, _ = m.Update(c())
	if !strings.Contains(m.notice, "final disk state uncertain") || !m.stale {
		t.Fatalf("joined outcome=%q", m.notice)
	}
}
