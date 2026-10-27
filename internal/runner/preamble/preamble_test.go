package preamble

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
)

// decode is a reference decoder that applies exactly the rules of Script.
// It returns the records and the remaining payload.
func decode(in []byte) (map[RecordType][]byte, []byte, error) {
	r := bufio.NewReader(bytes.NewReader(in))
	line := func() (string, error) {
		s, err := r.ReadString('\n')
		if err != nil {
			return "", errors.New("truncated")
		}
		return strings.TrimSuffix(s, "\n"), nil
	}
	h, err := line()
	if err != nil || h != Header {
		return nil, nil, errors.New("bad header")
	}
	out := map[RecordType][]byte{}
	for n := 0; ; n++ {
		l, err := line()
		if err != nil {
			return nil, nil, err
		}
		if l == Trailer {
			break
		}
		if n+1 > MaxRecords {
			return nil, nil, errors.New("too many records")
		}
		t, ln, ok := strings.Cut(l, " ")
		if !ok || strings.Contains(ln, " ") {
			return nil, nil, errors.New("bad record header")
		}
		if ln == "" || strings.Trim(ln, "0123456789") != "" || (len(ln) > 1 && ln[0] == '0') || len(ln) > 5 {
			return nil, nil, errors.New("bad length")
		}
		size, err := strconv.Atoi(ln)
		if err != nil {
			return nil, nil, errors.New("bad length")
		}
		if size > MaxRecordBytes {
			return nil, nil, errors.New("too large")
		}
		rt := RecordType(t)
		if _, ok := rt.File(); !ok {
			return nil, nil, errors.New("unknown type")
		}
		if _, dup := out[rt]; dup {
			return nil, nil, errors.New("duplicate")
		}
		b64, err := line()
		if err != nil {
			return nil, nil, err
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(data) != size {
			return nil, nil, errors.New("bad data")
		}
		out[rt] = data
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	return out, rest, nil
}

func TestEncodeRoundTrip(t *testing.T) {
	binary := make([]byte, 256)
	for i := range binary {
		binary[i] = byte(i)
	}
	recs := []Record{
		{RecordPGPass, []byte("db.example.com:5432:*:backup:pa\\:ss'\"$`\\\n")},
		{RecordMyCnf, []byte("[client]\npassword=\"a\\\"b\"\n")},
		{RecordCA, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")},
		{RecordClientCert, binary},
		{RecordClientKey, nil},
	}
	enc, err := Encode(recs)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("PGDMP\x00\x01payload\nEND\n")
	got, rest, err := decode(append(enc, payload...))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(rest, payload) {
		t.Errorf("payload = %q", rest)
	}
	for _, r := range recs {
		if !bytes.Equal(got[r.Type], r.Data) {
			t.Errorf("%s = %q, want %q", r.Type, got[r.Type], r.Data)
		}
	}
	if !strings.HasPrefix(string(enc), Header+"\npgpass ") || !strings.HasSuffix(string(enc), "\n"+Trailer+"\n") {
		t.Errorf("framing: %q", enc)
	}
	// Empty preamble.
	enc, err = Encode(nil)
	if err != nil || string(enc) != Header+"\n"+Trailer+"\n" {
		t.Errorf("empty = %q %v", enc, err)
	}
}

func TestEncodeInvalid(t *testing.T) {
	tests := map[string][]Record{
		"unknown":   {{Type: "evil", Data: []byte("x")}},
		"path":      {{Type: "../x", Data: []byte("x")}},
		"duplicate": {{RecordCA, []byte("a")}, {RecordCA, []byte("b")}},
		"too large": {{RecordCA, make([]byte, MaxRecordBytes+1)}},
		"too many": {
			{RecordCA, nil}, {RecordPGPass, nil}, {RecordMyCnf, nil},
			{RecordClientCert, nil}, {RecordClientKey, nil}, {RecordClientKey, nil},
		},
	}
	for name, recs := range tests {
		if _, err := Encode(recs); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Encode([]Record{{RecordCA, make([]byte, MaxRecordBytes)}}); err != nil {
		t.Errorf("max size: %v", err)
	}
}

// TestScriptMatchesGo keeps Script and the Go constants in sync.
func TestScriptMatchesGo(t *testing.T) {
	for typ, f := range files {
		short := strings.TrimPrefix(string(f), engine.TmpDir+"/")
		want := "    " + string(typ) + ") f=$d/" + short + " ;;"
		if !strings.Contains(Script, want) {
			t.Errorf("Script lacks %q", want)
		}
	}
	for _, want := range []string{
		"d=" + engine.TmpDir + "\n",
		"[ \"$line\" = '" + Header + "' ]",
		"[ \"$line\" = '" + Trailer + "' ] && break",
		"[ \"$n\" -le " + strconv.Itoa(MaxRecords) + " ]",
		"[ \"$len\" -le " + strconv.Itoa(MaxRecordBytes) + " ]",
		"[ \"${#len}\" -le " + strconv.Itoa(len(strconv.Itoa(MaxRecordBytes))) + " ]",
		"exit " + strconv.Itoa(ExitCode) + ";",
		"umask 077\n",
		"exec \"$@\"\n",
	} {
		if !strings.Contains(Script, want) {
			t.Errorf("Script lacks %q", want)
		}
	}
	if strings.Count(Script, ") f=$d/") != len(files) {
		t.Error("Script has record types without a Go constant")
	}
	// No interpolation markers or command execution beyond the fixed set.
	for _, bad := range []string{"eval", "`", "source ", ". /", "$(cat", "/proc"} {
		if strings.Contains(Script, bad) {
			t.Errorf("Script contains %q", bad)
		}
	}
	ep := Entrypoint()
	if len(ep) != 4 || ep[0] != "/bin/sh" || ep[1] != "-c" || ep[2] != Script || ep[3] != "--" {
		t.Errorf("Entrypoint = %q", ep)
	}
}

func TestTypeForFile(t *testing.T) {
	for _, f := range []engine.File{engine.FilePGPass, engine.FileMyCnf, engine.FileCA, engine.FileClientCert, engine.FileClientKey} {
		typ, ok := TypeForFile(f)
		if !ok {
			t.Fatalf("%s has no record type", f)
		}
		if back, _ := typ.File(); back != f {
			t.Errorf("%s -> %s -> %s", f, typ, back)
		}
	}
	if _, ok := TypeForFile("/etc/passwd"); ok {
		t.Error("unknown file mapped")
	}
	if _, ok := RecordType("x").File(); ok {
		t.Error("unknown type mapped")
	}
}

func TestForCommand(t *testing.T) {
	m := &Material{
		PGPass: []byte("h:1:*:u:p\n"), MyCnf: []byte("[client]\n"), CA: []byte("ca"),
		ClientCert: []byte("cert"), ClientKey: []byte("key"),
	}
	cmd := engine.Command{Files: []engine.File{engine.FilePGPass, engine.FileCA}}
	recs, err := ForCommand(cmd, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Type != RecordPGPass || recs[1].Type != RecordCA || string(recs[1].Data) != "ca" {
		t.Errorf("records = %+v", recs)
	}
	// Material for unused files is never sent.
	for _, r := range recs {
		if r.Type == RecordClientKey || r.Type == RecordMyCnf {
			t.Error("unused material sent")
		}
	}
	if _, err := ForCommand(engine.Command{Files: []engine.File{engine.FileClientKey}}, &Material{}); !errors.Is(err, ErrMissingFile) {
		t.Errorf("missing: %v", err)
	}
	if _, err := ForCommand(engine.Command{Files: []engine.File{"/etc/shadow"}}, m); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("unknown file: %v", err)
	}
	if _, err := ForCommand(engine.Command{Files: []engine.File{engine.FileCA, engine.FileCA}}, m); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("duplicate file: %v", err)
	}
	if recs, err := ForCommand(engine.Command{}, nil); err != nil || len(recs) != 0 {
		t.Errorf("no files: %v %v", recs, err)
	}
}

// FuzzEncodeDecode: any record set Encode accepts decodes to the same
// data, the payload after the trailer is untouched, and decode never
// accepts trailing garbage inside the preamble.
func FuzzEncodeDecode(f *testing.F) {
	f.Add([]byte("pw\n'\"$\\"), []byte("-----BEGIN-----\n"), []byte("payload"), uint8(3))
	f.Add([]byte{}, []byte{0, 1, 2, 255}, []byte{}, uint8(0))
	f.Fuzz(func(t *testing.T, a, b, payload []byte, mask uint8) {
		var recs []Record
		types := []RecordType{RecordPGPass, RecordMyCnf, RecordCA, RecordClientCert, RecordClientKey}
		for i, typ := range types {
			if mask&(1<<i) == 0 {
				continue
			}
			d := a
			if i%2 == 1 {
				d = b
			}
			recs = append(recs, Record{typ, d})
		}
		enc, err := Encode(recs)
		if err != nil {
			if len(a) <= MaxRecordBytes && len(b) <= MaxRecordBytes {
				t.Fatalf("Encode: %v", err)
			}
			return
		}
		got, rest, err := decode(append(enc, payload...))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !bytes.Equal(rest, payload) || len(got) != len(recs) {
			t.Fatalf("rest %q, %d records", rest, len(got))
		}
		for _, r := range recs {
			if !bytes.Equal(got[r.Type], r.Data) {
				t.Fatalf("%s mismatch", r.Type)
			}
		}
	})
}

// FuzzDecode: the reference decoder (the script's rules) never panics on
// arbitrary input.
func FuzzDecode(f *testing.F) {
	enc, err := Encode([]Record{{RecordCA, []byte("x")}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(enc)
	f.Add([]byte(Header + "\nca 01\nAA==\nEND\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, _, err := decode(in)
		if err == nil && len(got) > MaxRecords {
			t.Fatal("too many records accepted")
		}
	})
}
