// Package mysqlit holds the integration tests of the MySQL/MariaDB restore
// path (task 4.3, internal/restore/mysql.go). They live in their own
// package because the runner must run inside a container (a test binary
// built for linux) and the restore package's TestMain starts the app
// database, which that container does not have. The package has no
// non-test code.
package mysqlit
