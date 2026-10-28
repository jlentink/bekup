package jobkeys

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/pipeline"
)

// Pipeline returns the pipeline's encryption configuration for the key
// material (task 3.1). The passphrase moves to the returned value: the
// caller destroys Encryption.Passphrase after the run instead of calling
// k.Destroy.
func (k *KeyMaterial) Pipeline() (pipeline.Encryption, error) {
	if k == nil {
		return pipeline.Encryption{}, fmt.Errorf("%w: no key material", pipeline.ErrEncryptionConfig)
	}
	var e pipeline.Encryption
	switch k.Mode {
	case ModePassphrase:
		e = pipeline.PassphraseEncryption(k.Passphrase, k.PassphraseVersion)
	case ModeRecipient:
		e = pipeline.RecipientEncryption(k.RecipientVersion, k.Recipients...)
	case ModeNone:
		e = pipeline.OptedOutEncryption()
	default:
		return pipeline.Encryption{}, fmt.Errorf("%w: unknown mode", pipeline.ErrEncryptionConfig)
	}
	if err := e.Validate(); err != nil {
		return pipeline.Encryption{}, err
	}
	k.Passphrase = nil
	return e, nil
}

// jobEncryptor is the pipeline.Encryptor of one job.
type jobEncryptor struct {
	s   *Service
	job uuid.UUID
}

// Encryptor returns the pipeline.Encryptor of job: each call reads the
// job's current key (KeyMaterialFor). The caller of BackupEncryption owns
// and destroys the returned passphrase.
func (s *Service) Encryptor(job uuid.UUID) pipeline.Encryptor { return jobEncryptor{s: s, job: job} }

// BackupEncryption implements pipeline.Encryptor.
func (e jobEncryptor) BackupEncryption(ctx context.Context) (pipeline.Encryption, error) {
	km, err := e.s.KeyMaterialFor(ctx, e.job)
	if err != nil {
		return pipeline.Encryption{}, err
	}
	enc, err := km.Pipeline()
	if err != nil {
		km.Destroy()
		return pipeline.Encryption{}, err
	}
	return enc, nil
}
