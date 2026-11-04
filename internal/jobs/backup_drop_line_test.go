package jobs

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

func TestDestinationDropLine(t *testing.T) {
	const dest = "d7f3b458-0000-4000-8000-000000000001"
	raw := "/srv/stack/user@example.com/upload/.0123.partial: server says hello"
	cases := []struct {
		name string
		err  error
		code storage.ErrorCode
		want string
	}{
		{
			name: "size after closing",
			err: fmt.Errorf("sftp: %w", storage.WithDetail(
				fmt.Errorf("%w: %s: the server reported 0 of 43202150 bytes after closing", storage.ErrChecksumMismatch, raw),
				"the server reported "+storage.FormatSizes(0, 43202150)+" after closing")),
			code: storage.CodeChecksumMismatch,
			want: "database app: destination " + dest + " dropped (error: the data did not match the expected checksum: the server reported 0 B of 41.2 MiB after closing)",
		},
		{
			name: "permission with an operation detail",
			err:  storage.WithDetail(fmt.Errorf("sftp: create temporary file: %w: %s", storage.ErrPermission, raw), "create temporary file (SFTP status SSH_FX_PERMISSION_DENIED)"),
			code: storage.CodePermission,
			want: "database app: destination " + dest + " dropped (error: the destination refused access (check permissions or credentials): create temporary file (SFTP status SSH_FX_PERMISSION_DENIED))",
		},
		{
			name: "host key mismatch without a detail",
			err:  storage.WithCode(storage.CodeHostKeyMismatch, errors.New("pinned SHA256:aaa, presented SHA256:bbb at "+raw)),
			code: storage.CodeHostKeyMismatch,
			want: "database app: destination " + dest + " dropped (error: " + storage.CodeHostKeyMismatch.Message() + ")",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &pipeline.DestinationResult{Outcome: pipeline.OutcomeDropped, Reason: pipeline.ReasonError, Code: tc.code, Err: tc.err}
			got := destinationDropLine("database app", dest, d)
			if got != tc.want {
				t.Fatalf("line = %q\nwant   %q", got, tc.want)
			}
			if strings.Contains(got, "/srv") || strings.Contains(got, "example.com") || strings.Contains(got, "SHA256:") {
				t.Fatalf("raw error leaked: %q", got)
			}
		})
	}
}
