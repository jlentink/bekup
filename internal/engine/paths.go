package engine

// File is a fixed path in the container's tmpfs that the stdin preamble
// (task 2.5) writes with mode 0600 before it execs the tool. Builders only
// reference these constants; secrets never appear in argv or env (D-032).
type File string

// TmpDir is the directory holding all preamble files. HOME is /tmp.
const TmpDir = "/tmp/bekup"

// Preamble files.
const (
	// FileCA is the pinned server certificate: the client's only trust
	// anchor in TLSPinned mode.
	FileCA File = TmpDir + "/ca.pem"
	// FileClientCert and FileClientKey are the optional client certificate.
	FileClientCert File = TmpDir + "/client-cert.pem"
	FileClientKey  File = TmpDir + "/client-key.pem"
	// FilePGPass is the PostgreSQL password file (PGPassLine).
	FilePGPass File = TmpDir + "/pgpass"
	// FileMyCnf is the MySQL/MariaDB option file (MySQLOptionFile). It is
	// passed with --defaults-file so no other option file is read.
	FileMyCnf File = TmpDir + "/my.cnf"
)
