package model

import "github.com/google/uuid"

// JobStorage is what a job's present backup files occupy (D-225). The
// totals come from one aggregate per page of jobs; perDestination,
// newestRun and trend are field resolvers that read the job id kept
// here (the job was authorized by the parent field).
type JobStorage struct {
	TotalBytes    float64 `json:"totalBytes"`
	ArtifactCount int     `json:"artifactCount"`
	BackupCount   int     `json:"backupCount"`
	jobID         uuid.UUID
}

// NewJobStorage builds the model for the job jobID.
func NewJobStorage(jobID uuid.UUID, totalBytes float64, artifactCount, backupCount int) *JobStorage {
	return &JobStorage{TotalBytes: totalBytes, ArtifactCount: artifactCount, BackupCount: backupCount, jobID: jobID}
}

// JobID returns the job the storage belongs to.
func (s *JobStorage) JobID() uuid.UUID { return s.jobID }
