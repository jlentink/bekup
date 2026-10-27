package runnerpb

import (
	"bytes"
	"regexp"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// credentialName matches field names that must carry the secret option.
var credentialName = regexp.MustCompile(`password|passphrase|private_key|key_pem|secret|token|credential`)

// notCredentials are matching names that carry no credential material.
var notCredentials = map[protoreflect.FullName]bool{
	"runner.v1.DumpResult.roles_without_passwords": true, // a flag
}

// TestCredentialFieldsAnnotated walks every message of the runner API and
// fails when a credential-looking field lacks [(runner.v1.secret) = true] or
// is not a bytes field (strings cannot be zeroed).
func TestCredentialFieldsAnnotated(t *testing.T) {
	msgs := File_runner_v1_runner_proto.Messages()
	var secretFields int
	var walk func(protoreflect.MessageDescriptors)
	walk = func(ms protoreflect.MessageDescriptors) {
		for i := range ms.Len() {
			md := ms.Get(i)
			fields := md.Fields()
			for j := range fields.Len() {
				fd := fields.Get(j)
				isSecret := IsSecretField(fd)
				if credentialName.MatchString(string(fd.Name())) && !isSecret && !notCredentials[fd.FullName()] {
					t.Errorf("%s: credential-looking field without [(secret) = true]", fd.FullName())
				}
				if isSecret {
					secretFields++
					if fd.Kind() != protoreflect.BytesKind {
						t.Errorf("%s: secret fields must be bytes so they can be zeroed", fd.FullName())
					}
				}
			}
			walk(md.Messages())
		}
	}
	walk(msgs)
	if secretFields < 5 {
		t.Fatalf("only %d secret fields found; annotation lookup broken?", secretFields)
	}
}

// TestNoFreeFormFields guards D-030: the API has no command, argument,
// environment, image or path fields.
func TestNoFreeFormFields(t *testing.T) {
	forbidden := regexp.MustCompile(`^(cmd|command|commands|args|argv|arguments|env|environment|image|image_name|path|file|script|entrypoint|options|flags)$`)
	msgs := File_runner_v1_runner_proto.Messages()
	for i := range msgs.Len() {
		fields := msgs.Get(i).Fields()
		for j := range fields.Len() {
			if fd := fields.Get(j); forbidden.MatchString(string(fd.Name())) {
				t.Errorf("%s: free-form field not allowed", fd.FullName())
			}
		}
	}
}

func TestZeroSecrets(t *testing.T) {
	pw := []byte("db-password")
	key := []byte("ssh-private-key")
	pass := []byte("ssh-passphrase")
	clientKey := []byte("client-key")
	req := &RestoreRequest{Msg: &RestoreRequest_Header{Header: &RestoreHeader{
		Ctx:      &RunContext{RunId: "r1"},
		Database: "app",
		Target: &Target{
			Host:              "db",
			User:              "backup",
			Password:          pw,
			Pins:              []*CertificatePin{{AnchorDer: []byte{1, 2}}},
			ClientCertificate: &ClientCertificate{CertPem: []byte("cert"), KeyPem: clientKey},
			SshTunnel:         &SshTunnel{Host: "bastion", PrivateKeyPem: key, Passphrase: pass},
		},
	}}}
	ZeroSecrets(req)
	for name, b := range map[string][]byte{"password": pw, "private key": key, "passphrase": pass, "client key": clientKey} {
		if !bytes.Equal(b, make([]byte, len(b))) {
			t.Errorf("%s not zeroed: %q", name, b)
		}
	}
	tg := req.GetHeader().GetTarget()
	if tg.GetPassword() != nil || tg.GetSshTunnel().GetPrivateKeyPem() != nil || tg.GetClientCertificate().GetKeyPem() != nil {
		t.Error("secret fields not cleared")
	}
	// Non-secret data is untouched.
	if tg.GetHost() != "db" || tg.GetUser() != "backup" || string(tg.GetClientCertificate().GetCertPem()) != "cert" ||
		len(tg.GetPins()) != 1 || req.GetHeader().GetDatabase() != "app" {
		t.Errorf("non-secret data changed: %v", req)
	}

	// No-ops.
	ZeroSecrets(nil)
	ZeroSecrets((*Target)(nil))
	chunk := &RestoreRequest{Msg: &RestoreRequest_Chunk{Chunk: &DataChunk{Data: []byte("data")}}}
	ZeroSecrets(chunk)
	if string(chunk.GetChunk().GetData()) != "data" {
		t.Error("chunk data changed")
	}
}

func TestHasSecretFields(t *testing.T) {
	for _, tc := range []struct {
		m    proto.Message
		want bool
	}{
		{&DumpRequest{}, true},
		{&RestoreRequest{}, true},
		{&FetchServerCertificateRequest{}, true}, // SSH tunnel
		{&DataChunk{}, false},
		{&HealthRequest{}, false},
		{&DumpResponse{}, false},
		{&PrePullRequest{}, false},
	} {
		if got := HasSecretFields(tc.m.ProtoReflect().Descriptor()); got != tc.want {
			t.Errorf("%T: got %v", tc.m, got)
		}
	}
}

func TestContainsSecrets(t *testing.T) {
	for _, tc := range []struct {
		m    proto.Message
		want bool
	}{
		{nil, false},
		{&RestoreRequest{Msg: &RestoreRequest_Chunk{Chunk: &DataChunk{Data: []byte("x")}}}, false},
		{&RestoreRequest{Msg: &RestoreRequest_Header{Header: &RestoreHeader{Target: &Target{Host: "db"}}}}, false},
		{&RestoreRequest{Msg: &RestoreRequest_Header{Header: &RestoreHeader{Target: &Target{Password: []byte("pw")}}}}, true},
		{&DumpRequest{Target: &Target{SshTunnel: &SshTunnel{Passphrase: []byte("p")}}}, true},
		{&FetchServerCertificateRequest{SshTunnel: &SshTunnel{Password: []byte("p")}}, true},
	} {
		if got := ContainsSecrets(tc.m); got != tc.want {
			t.Errorf("%v: got %v", tc.m, got)
		}
	}
}
