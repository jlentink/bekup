package runner

import "github.com/jlentink/bekup/internal/runnerpb"

// methodClass is the concurrency class of an RPC (D-127).
type methodClass int

const (
	// classUnlimited: cheap status calls (Health).
	classUnlimited methodClass = iota + 1
	// classRun: calls that start a container or change a target database;
	// bounded by limits.max_concurrent_runs.
	classRun
	// classControl: short control calls; bounded by controlConcurrency.
	classControl
)

// methodClasses classifies every RPC of runner.v1.RunnerService. A method
// that is missing here is denied (deny by default); a test asserts that the
// table covers exactly the service's methods.
var methodClasses = map[string]methodClass{
	runnerpb.RunnerService_Health_FullMethodName:                     classUnlimited,
	runnerpb.RunnerService_Probe_FullMethodName:                      classControl,
	runnerpb.RunnerService_FetchServerCertificate_FullMethodName:     classControl,
	runnerpb.RunnerService_FetchHostKey_FullMethodName:               classControl,
	runnerpb.RunnerService_PrePull_FullMethodName:                    classControl,
	runnerpb.RunnerService_ListImages_FullMethodName:                 classControl,
	runnerpb.RunnerService_Dump_FullMethodName:                       classRun,
	runnerpb.RunnerService_DumpGlobals_FullMethodName:                classRun,
	runnerpb.RunnerService_VerifyDump_FullMethodName:                 classRun,
	runnerpb.RunnerService_Restore_FullMethodName:                    classRun,
	runnerpb.RunnerService_CreateDatabase_FullMethodName:             classRun,
	runnerpb.RunnerService_RenameSwapDatabase_FullMethodName:         classRun,
	runnerpb.RunnerService_DropTempDatabase_FullMethodName:           classRun,
	runnerpb.RunnerService_ScratchStats_FullMethodName:               classRun,
	runnerpb.RunnerService_DropScratchDatabase_FullMethodName:        classRun,
	runnerpb.RunnerService_NeutralizeRestoredDatabase_FullMethodName: classRun,
}

// limiter bounds concurrent calls per class with non-blocking semaphores:
// a full class fails fast with ResourceExhausted instead of queueing.
type limiter struct {
	run     chan struct{}
	control chan struct{}
}

func newLimiter(runs, control int) *limiter {
	return &limiter{run: make(chan struct{}, runs), control: make(chan struct{}, control)}
}

// acquire takes a slot of class c; ok is false when the class is full. The
// returned release must be called exactly once when ok.
func (l *limiter) acquire(c methodClass) (release func(), ok bool) {
	var sem chan struct{}
	switch c {
	case classUnlimited:
		return func() {}, true
	case classRun:
		sem = l.run
	case classControl:
		sem = l.control
	default:
		return nil, false
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	default:
		return nil, false
	}
}

// activeRuns is the number of calls currently holding a run slot.
func (l *limiter) activeRuns() int { return len(l.run) }

// maxRuns is the run slot capacity.
func (l *limiter) maxRuns() int { return cap(l.run) }
