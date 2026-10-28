package retention

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	now   = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	destA = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	destB = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
)

// fixture builds runs and artifacts.
type fixture struct {
	runs  []Run
	arts  []Artifact
	names map[uuid.UUID]string
}

func newFixture() *fixture { return &fixture{names: map[uuid.UUID]string{}} }

// run adds a run aged `age` with n items; present lists, per destination,
// the item indexes stored there (verified) and whether the manifest is.
type placement struct {
	dest     uuid.UUID
	items    []int // item indexes with a present, verified artifact
	manifest bool
	lock     time.Time
	unverify []int // item indexes stored but unverified
}

func (f *fixture) run(name string, age time.Duration, n int, ps ...placement) uuid.UUID {
	id := uuid.New()
	f.names[id] = name
	r := Run{ID: id, Time: now.Add(-age)}
	for range n {
		r.Items = append(r.Items, uuid.New())
	}
	f.runs = append(f.runs, r)
	for _, p := range ps {
		for _, i := range p.items {
			it := r.Items[i]
			f.arts = append(f.arts, Artifact{ID: uuid.New(), RunID: id, DestinationID: p.dest, ItemID: &it,
				Key: fmt.Sprintf("%s/%d", name, i), Verified: true, LockUntil: p.lock})
		}
		for _, i := range p.unverify {
			it := r.Items[i]
			f.arts = append(f.arts, Artifact{ID: uuid.New(), RunID: id, DestinationID: p.dest, ItemID: &it,
				Key: fmt.Sprintf("%s/u%d", name, i), LockUntil: p.lock})
		}
		if p.manifest {
			f.arts = append(f.arts, Artifact{ID: uuid.New(), RunID: id, DestinationID: p.dest,
				Key: name + "/manifest.json", Verified: true, LockUntil: p.lock})
		}
	}
	return id
}

func full(dest uuid.UUID, n int) placement {
	p := placement{dest: dest, manifest: true}
	for i := range n {
		p.items = append(p.items, i)
	}
	return p
}

func (f *fixture) plan(pol Policy) map[uuid.UUID]DestinationPlan {
	out := map[uuid.UUID]DestinationPlan{}
	for _, p := range Plan(Input{Policy: pol, Now: now, Runs: f.runs, Artifacts: f.arts}) {
		if !checkPlan(p) {
			panic("plan invariant violated")
		}
		out[p.DestinationID] = p
	}
	return out
}

func (f *fixture) deleted(p DestinationPlan) []string {
	out := make([]string, 0, len(p.Delete))
	for _, d := range p.Delete {
		out = append(out, f.names[d.RunID])
	}
	slices.Sort(out)
	return out
}

const day = 24 * time.Hour

func TestPlanTable(t *testing.T) {
	type tc struct {
		name   string
		build  func(f *fixture)
		policy Policy
		want   map[uuid.UUID][]string // deleted run names per destination
		newest map[uuid.UUID]string
	}
	tests := []tc{
		{
			name: "keep_last only",
			build: func(f *fixture) {
				for i := range 5 {
					f.run(fmt.Sprintf("r%d", i), time.Duration(i)*day, 2, full(destA, 2))
				}
			},
			policy: Policy{KeepLast: 2},
			want:   map[uuid.UUID][]string{destA: {"r2", "r3", "r4"}},
			newest: map[uuid.UUID]string{destA: "r0"},
		},
		{
			name: "keep_days only",
			build: func(f *fixture) {
				for i := range 5 {
					f.run(fmt.Sprintf("r%d", i), time.Duration(i)*day+time.Hour, 1, full(destA, 1))
				}
			},
			policy: Policy{KeepDays: 2},
			want:   map[uuid.UUID][]string{destA: {"r2", "r3", "r4"}},
		},
		{
			name: "both: union of last N and younger than X days",
			build: func(f *fixture) {
				for i := range 6 {
					f.run(fmt.Sprintf("r%d", i), time.Duration(i)*day+time.Hour, 1, full(destA, 1))
				}
			},
			policy: Policy{KeepLast: 1, KeepDays: 3},
			want:   map[uuid.UUID][]string{destA: {"r3", "r4", "r5"}},
		},
		{
			name: "newest complete is always kept even when older than keep_days",
			build: func(f *fixture) {
				f.run("old0", 40*day, 1, full(destA, 1))
				f.run("old1", 50*day, 1, full(destA, 1))
			},
			policy: Policy{KeepDays: 7},
			want:   map[uuid.UUID][]string{destA: {"old1"}},
			newest: map[uuid.UUID]string{destA: "old0"},
		},
		{
			name: "incomplete runs: newer kept, older eligible",
			build: func(f *fixture) {
				f.run("newer_partial", 1*day, 2, placement{dest: destA, items: []int{0}, manifest: true})
				f.run("complete", 2*day, 2, full(destA, 2))
				f.run("older_partial", 3*day, 2, placement{dest: destA, items: []int{1}, manifest: true})
				f.run("older_unverified", 4*day, 1, placement{dest: destA, unverify: []int{0}, manifest: true})
				f.run("older_no_manifest", 5*day, 1, placement{dest: destA, items: []int{0}})
			},
			policy: Policy{KeepLast: 1},
			want:   map[uuid.UUID][]string{destA: {"older_no_manifest", "older_partial", "older_unverified"}},
			newest: map[uuid.UUID]string{destA: "complete"},
		},
		{
			name: "no complete run: nothing deleted",
			build: func(f *fixture) {
				f.run("p1", 10*day, 2, placement{dest: destA, items: []int{0}, manifest: true})
				f.run("p2", 20*day, 2, placement{dest: destA, items: []int{1}, manifest: true})
			},
			policy: Policy{KeepLast: 1},
			want:   map[uuid.UUID][]string{destA: nil},
		},
		{
			name: "locked runs are kept",
			build: func(f *fixture) {
				f.run("r0", 0, 1, full(destA, 1))
				p := full(destA, 1)
				p.lock = now.Add(day)
				f.run("locked", 5*day, 1, p)
				p.lock = now.Add(-time.Hour) // expired lock
				f.run("expired", 6*day, 1, p)
			},
			policy: Policy{KeepLast: 1},
			want:   map[uuid.UUID][]string{destA: {"expired"}},
		},
		{
			name: "destinations with different completeness",
			build: func(f *fixture) {
				// r0 is complete on A only (B was dropped), r1 on both.
				f.run("r0", 0, 2, full(destA, 2), placement{dest: destB, items: []int{0}})
				f.run("r1", day, 2, full(destA, 2), full(destB, 2))
				f.run("r2", 2*day, 2, full(destA, 2), full(destB, 2))
			},
			policy: Policy{KeepLast: 1},
			want: map[uuid.UUID][]string{
				destA: {"r1", "r2"},
				destB: {"r2"}, // r0 newer incomplete kept, r1 newest complete kept
			},
			newest: map[uuid.UUID]string{destA: "r0", destB: "r1"},
		},
		{
			name: "no policy keeps everything",
			build: func(f *fixture) {
				f.run("r0", 0, 1, full(destA, 1))
				f.run("r1", day, 1, full(destA, 1))
			},
			policy: Policy{},
			want:   map[uuid.UUID][]string{destA: nil},
		},
		{
			name: "run without items is never complete",
			build: func(f *fixture) {
				f.run("r0", 0, 1, full(destA, 1))
				f.run("empty", day, 0, placement{dest: destA, manifest: true})
			},
			policy: Policy{KeepLast: 1},
			want:   map[uuid.UUID][]string{destA: {"empty"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			tt.build(f)
			plans := f.plan(tt.policy)
			for dest, want := range tt.want {
				p, ok := plans[dest]
				if !ok {
					t.Fatalf("no plan for %s", dest)
				}
				if got := f.deleted(p); !slices.Equal(got, want) {
					t.Errorf("dest %s: deleted %v, want %v (keep %v)", dest, got, want, p.Keep)
				}
				if n, ok := tt.newest[dest]; ok && f.names[p.NewestComplete] != n {
					t.Errorf("dest %s: newest complete %q, want %q", dest, f.names[p.NewestComplete], n)
				}
				if p.NewestComplete != uuid.Nil {
					if _, kept := p.Keep[p.NewestComplete]; !kept {
						t.Error("newest complete not kept")
					}
				}
			}
		})
	}
}

func TestPlanOrderAndManifestLast(t *testing.T) {
	f := newFixture()
	f.run("r0", 0, 3, full(destA, 3))
	f.run("r1", day, 3, full(destA, 3))
	f.run("r2", 2*day, 3, full(destA, 3))
	p := f.plan(Policy{KeepLast: 1})[destA]
	if len(p.Delete) != 2 || f.names[p.Delete[0].RunID] != "r2" || f.names[p.Delete[1].RunID] != "r1" {
		t.Fatalf("want oldest first, got %v", f.deleted(p))
	}
	for _, d := range p.Delete {
		if d.Manifest == nil || len(d.Dumps) != 3 || !d.Complete {
			t.Errorf("deletion %+v", d)
		}
		for _, a := range d.Dumps {
			if a.Manifest() {
				t.Error("manifest among dumps")
			}
		}
	}
	if p.DeleteCount() != 8 || len(p.NewestArtifacts) != 4 {
		t.Errorf("count %d newest %d", p.DeleteCount(), len(p.NewestArtifacts))
	}
}

func TestPlanIgnoresUnknownRuns(t *testing.T) {
	f := newFixture()
	f.run("r0", 0, 1, full(destA, 1))
	it := uuid.New()
	// An artifact of a run not in Runs (still running) is never touched.
	f.arts = append(f.arts, Artifact{ID: uuid.New(), RunID: uuid.New(), DestinationID: destA, ItemID: &it, Verified: true})
	p := f.plan(Policy{KeepLast: 1})[destA]
	if len(p.Delete) != 0 {
		t.Fatalf("deleted %v", p.Delete)
	}
}

func TestPlanTieBreakDeterministic(t *testing.T) {
	f := newFixture()
	a := f.run("a", day, 1, full(destA, 1))
	b := f.run("b", day, 1, full(destA, 1))
	for range 20 {
		p := f.plan(Policy{KeepLast: 1})[destA]
		want := a
		if b.String() > a.String() {
			want = b
		}
		if p.NewestComplete != want || len(p.Delete) != 1 {
			t.Fatalf("tie break not deterministic: %v", p.NewestComplete)
		}
	}
}

func TestCheckPlanRejectsViolations(t *testing.T) {
	f := newFixture()
	f.run("r0", 0, 1, full(destA, 1))
	f.run("r1", day, 1, full(destA, 1))
	p := Plan(Input{Policy: Policy{KeepLast: 1}, Now: now, Runs: f.runs, Artifacts: f.arts})[0]
	if !checkPlan(p) {
		t.Fatal("valid plan rejected")
	}
	bad := p
	bad.Delete = append(slices.Clone(p.Delete), RunDeletion{RunID: p.NewestComplete})
	if checkPlan(bad) {
		t.Error("deleting the newest complete run accepted")
	}
	bad = p
	bad.Keep = map[uuid.UUID]KeepReason{}
	if checkPlan(bad) {
		t.Error("newest complete not kept accepted")
	}
	bad = p
	other := p.Delete[0]
	other.Dumps = []Artifact{{RunID: other.RunID, DestinationID: destB, ItemID: &uuid.UUID{}}}
	bad.Delete = []RunDeletion{other}
	if checkPlan(bad) {
		t.Error("artifact of another destination accepted")
	}
	none := DestinationPlan{DestinationID: destA, Delete: []RunDeletion{{RunID: uuid.New()}}}
	if checkPlan(none) {
		t.Error("deletion without a complete run accepted")
	}
}
