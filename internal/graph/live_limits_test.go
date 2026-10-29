package graph

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jlentink/bekup/internal/graph/apierror"
)

// TestSetLimits swaps the graphql.* limits at runtime (config reload,
// D-200): the next operation uses the new values.
func TestSetLimits(t *testing.T) {
	env := newEnv(t, nil)
	q := "{ serverInfo { version insecureTransport devMode } }"
	if _, resp := env.query(t, q); len(resp.Errors) != 0 {
		t.Fatalf("before: %+v", resp.Errors)
	}
	if err := env.gql.SetLimits(Limits{MaxComplexity: 3, MaxDepth: 12, MaxBodyBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if w, resp := env.query(t, q); errorCode(resp) != string(apierror.QueryTooComplex) || w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("complexity after lowering: %d %+v", w.Code, resp)
	}
	if err := env.gql.SetLimits(Limits{MaxComplexity: 1000, MaxDepth: 1, MaxBodyBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if _, resp := env.query(t, q); errorCode(resp) != string(apierror.QueryTooDeep) {
		t.Fatalf("depth after lowering: %+v", resp)
	}
	if err := env.gql.SetLimits(Limits{MaxComplexity: 1000, MaxDepth: 12, MaxBodyBytes: 30}); err != nil {
		t.Fatal(err)
	}
	if w := env.raw(http.MethodPost, "application/json", strings.NewReader(`{"query":"{ serverInfo { version } }"}`)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body after lowering: %d", w.Code)
	}
	if got := env.gql.CurrentLimits(); got.MaxBodyBytes != 30 {
		t.Fatalf("CurrentLimits = %+v", got)
	}
}

func TestPrepareLimitsRejects(t *testing.T) {
	env := newEnv(t, nil)
	before := env.gql.CurrentLimits()
	for _, l := range []Limits{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {-1, 5, 5}} {
		if _, err := env.gql.PrepareLimits(l); err == nil {
			t.Errorf("PrepareLimits(%+v) accepted", l)
		}
	}
	commit, err := env.gql.PrepareLimits(Limits{MaxComplexity: 7, MaxDepth: 7, MaxBodyBytes: 7})
	if err != nil {
		t.Fatal(err)
	}
	if env.gql.CurrentLimits() != before {
		t.Fatal("Prepare applied before the commit")
	}
	commit()
	if env.gql.CurrentLimits().MaxDepth != 7 {
		t.Fatal("commit did not apply")
	}
}

func TestSetLimitsConcurrent(t *testing.T) {
	env := newEnv(t, nil)
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 50 {
				env.raw(http.MethodPost, "application/json", strings.NewReader(`{"query":"{ serverInfo { version } }"}`))
			}
		}()
		go func() {
			defer wg.Done()
			for j := range 50 {
				if err := env.gql.SetLimits(Limits{MaxComplexity: 100 + i + j, MaxDepth: 5 + j%5, MaxBodyBytes: 4096}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
