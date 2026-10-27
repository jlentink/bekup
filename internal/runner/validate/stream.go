package validate

import (
	"github.com/jlentink/bekup/internal/runnerpb"
)

// Stream validates the chunk messages that follow a VerifyDump or Restore
// header: every further message must be a non-empty chunk of at most
// runnerpb.MaxChunkBytes, the running total must never exceed the
// header's total_bytes, and Complete checks that exactly total_bytes
// arrived. A Stream is used by one goroutine.
type Stream struct {
	total    uint64
	received uint64
}

// maxChunkMessageBytes bounds one chunk message (chunk + envelope).
const maxChunkMessageBytes = runnerpb.MaxMessageBytes

// VerifyChunk validates a VerifyDump message after the header and returns
// its data.
func (s *Stream) VerifyChunk(req *runnerpb.VerifyDumpRequest) ([]byte, error) {
	if err := checkEnvelope(req, maxChunkMessageBytes); err != nil {
		return nil, err
	}
	if req.GetHeader() != nil || req.GetChunk() == nil {
		return nil, fieldError("chunk", "only chunks may follow the header")
	}
	return s.add(req.GetChunk())
}

// RestoreChunk validates a Restore message after the header and returns
// its data.
func (s *Stream) RestoreChunk(req *runnerpb.RestoreRequest) ([]byte, error) {
	if err := checkEnvelope(req, maxChunkMessageBytes); err != nil {
		return nil, err
	}
	if req.GetHeader() != nil || req.GetChunk() == nil {
		return nil, fieldError("chunk", "only chunks may follow the header")
	}
	return s.add(req.GetChunk())
}

func (s *Stream) add(c *runnerpb.DataChunk) ([]byte, error) {
	d := c.GetData()
	if len(d) == 0 || len(d) > runnerpb.MaxChunkBytes {
		return nil, fieldError("chunk.data", "must be 1 byte to 1 MiB")
	}
	if uint64(len(d)) > s.total-s.received {
		return nil, fieldError("chunk.data", "more data than total_bytes")
	}
	s.received += uint64(len(d))
	return d, nil
}

// Received returns the bytes accepted so far.
func (s *Stream) Received() uint64 { return s.received }

// Complete reports whether exactly total_bytes have been received; call it
// when the client closes its side of the stream.
func (s *Stream) Complete() error {
	if s.received != s.total {
		return fieldError("chunk", "stream ended before total_bytes")
	}
	return nil
}
