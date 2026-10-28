package scheduler

import "time"

// CompileStored decodes a schedule as stored in jobs.schedule and compiles
// it, exactly as the scheduler loop does: the configurable policy
// (min_interval) is not re-checked (D-143). Read-only consumers such as
// the job calendar (task 3.14) use it.
func CompileStored(raw []byte) (*Plan, error) {
	s, err := decodeStored(raw)
	if err != nil {
		return nil, err
	}
	return Compile(s)
}

// LoadZone loads an IANA zone name with the rules of schedule zones: an
// explicit, host-independent name ("Local" and the empty name are
// rejected); errors wrap ErrInvalid.
func LoadZone(name string) (*time.Location, error) { return loadZone(name) }
